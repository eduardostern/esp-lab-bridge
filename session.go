package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	sessionSeconds = 600
	maxScriptBytes = 2 << 20
)

type SessionRequest struct {
	Board    string `json:"board"`
	FlashMB  int    `json:"flashMb"`
	WifiSpec string `json:"wifiSpec"`
	Script   string `json:"script"`
}

type sessionInfo struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	Port      int       `json:"port"`
	StartedAt time.Time `json:"startedAt"`
}

type session struct {
	info     sessionInfo
	cmd      *exec.Cmd
	dir      string
	done     chan struct{}
	stopOnce sync.Once
	output   *lockedBuffer
}

type sessionManager struct {
	mu      sync.Mutex
	startMu sync.Mutex
	current *session
	engine  string
	assets  *assetStore
	verbose bool
}

type badRequest struct{ message string }

func (e *badRequest) Error() string { return e.message }

func newSessionManager(engine string, assets *assetStore, verbose bool) *sessionManager {
	return &sessionManager{engine: engine, assets: assets, verbose: verbose}
}

func (m *sessionManager) Current() *sessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return nil
	}
	info := m.current.info
	return &info
}

func (m *sessionManager) Start(ctx context.Context, request SessionRequest) (sessionInfo, error) {
	if err := validateSessionRequest(request); err != nil {
		return sessionInfo{}, &badRequest{err.Error()}
	}
	if m.engine == "" {
		return sessionInfo{}, errors.New("esp32sim is not available; put it next to the bridge or set ESP32SIM_BIN")
	}

	// One session is enough: starting another replaces the previous one.
	m.startMu.Lock()
	defer m.startMu.Unlock()
	m.mu.Lock()
	previous := m.current
	m.current = nil
	m.mu.Unlock()
	if previous != nil {
		previous.stop()
	}

	assets, err := m.assets.Ensure(ctx)
	if err != nil {
		return sessionInfo{}, err
	}
	dir, err := os.MkdirTemp("", "esp-lab-bridge-*")
	if err != nil {
		return sessionInfo{}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	flashPath := filepath.Join(dir, "tasmota.flash.bin")
	if err := writeFlash(assets.Factory, flashPath, request.FlashMB); err != nil {
		cleanup()
		return sessionInfo{}, fmt.Errorf("prepare flash image: %w", err)
	}
	scriptPath := filepath.Join(dir, "session.script")
	if err := os.WriteFile(scriptPath, []byte(request.Script), 0o600); err != nil {
		cleanup()
		return sessionInfo{}, fmt.Errorf("write script: %w", err)
	}

	port, err := freePort()
	if err != nil {
		cleanup()
		return sessionInfo{}, err
	}
	args := []string{
		"--chip", "esp32",
		"--boot", "rom",
		"--rom", assets.ROM,
		"--flash-image", flashPath,
		"--board", request.Board,
		"--net", "nat",
		"--hostfwd", fmt.Sprintf("tcp:127.0.0.1:%d-:80", port),
		"--realtime",
		"--max-seconds", fmt.Sprintf("%d", sessionSeconds),
		"--console", "none",
		"--no-dump",
		"--script", scriptPath,
	}
	if request.WifiSpec != "" {
		args = append(args, "--wifi", request.WifiSpec)
	}

	cmd := exec.Command(m.engine, args...)
	output := &lockedBuffer{}
	cmd.Stdout = io.Discard
	cmd.Stderr = output
	if m.verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = io.MultiWriter(os.Stderr, output)
	}

	current := &session{
		info: sessionInfo{
			ID:        randomID(),
			URL:       fmt.Sprintf("http://127.0.0.1:%d/", port),
			Port:      port,
			StartedAt: time.Now(),
		},
		cmd:    cmd,
		dir:    dir,
		done:   make(chan struct{}),
		output: output,
	}

	if err := cmd.Start(); err != nil {
		cleanup()
		return sessionInfo{}, fmt.Errorf("start esp32sim: %w", err)
	}
	go func() {
		_ = cmd.Wait()
		close(current.done)
		_ = os.RemoveAll(current.dir)
		m.mu.Lock()
		if m.current == current {
			m.current = nil
		}
		m.mu.Unlock()
	}()

	if err := waitForTasmota(ctx, port, current.done, 45*time.Second); err != nil {
		current.stop()
		detail := lastLines(output.String(), 8)
		if detail != "" {
			return sessionInfo{}, fmt.Errorf("%w: %s", err, detail)
		}
		return sessionInfo{}, err
	}

	m.mu.Lock()
	m.current = current
	m.mu.Unlock()
	return current.info, nil
}

func (m *sessionManager) Stop(id string) bool {
	m.mu.Lock()
	current := m.current
	if current == nil || (id != "" && current.info.ID != id) {
		m.mu.Unlock()
		return false
	}
	m.current = nil
	m.mu.Unlock()
	current.stop()
	return true
}

func (m *sessionManager) Close() {
	m.mu.Lock()
	current := m.current
	m.current = nil
	m.mu.Unlock()
	if current != nil {
		current.stop()
	}
}

func (s *session) stop() {
	s.stopOnce.Do(func() {
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
		}
		_ = os.RemoveAll(s.dir)
	})
}

func validateSessionRequest(request SessionRequest) error {
	if strings.TrimSpace(request.Board) == "" {
		return errors.New("board is required")
	}
	if len(request.Board) > 64 {
		return errors.New("board is too long")
	}
	if request.FlashMB < 1 || request.FlashMB > 32 {
		return errors.New("flashMb must be between 1 and 32")
	}
	if strings.TrimSpace(request.WifiSpec) == "" {
		return errors.New("wifiSpec is required; the Tasmota web UI needs a network")
	}
	if len(request.WifiSpec) > 256 {
		return errors.New("wifiSpec is too long")
	}
	if strings.TrimSpace(request.Script) == "" {
		return errors.New("script is required")
	}
	if len(request.Script) > maxScriptBytes {
		return errors.New("script is too large")
	}
	return nil
}

func writeFlash(factoryPath, target string, flashMB int) error {
	source, err := os.Open(factoryPath)
	if err != nil {
		return err
	}
	defer source.Close()

	size := int64(flashMB) * 1024 * 1024
	destination, err := os.Create(target)
	if err != nil {
		return err
	}
	defer destination.Close()

	written, err := io.Copy(destination, source)
	if err != nil {
		return err
	}
	if written > size {
		return fmt.Errorf("Tasmota image is larger than %d MB", flashMB)
	}
	padding := bytes.Repeat([]byte{0xff}, 64*1024)
	for written < size {
		chunk := int64(len(padding))
		if remaining := size - written; remaining < chunk {
			chunk = remaining
		}
		n, err := destination.Write(padding[:int(chunk)])
		if err != nil {
			return err
		}
		written += int64(n)
	}
	return destination.Sync()
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func waitForTasmota(ctx context.Context, port int, done <-chan struct{}, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 1200 * time.Millisecond}
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	var upSince time.Time
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return errors.New("Tasmota exited before its web server came up")
		default:
		}
		response, err := client.Get(url)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode < 500 {
				if upSince.IsZero() {
					upSince = time.Now()
				} else if time.Since(upSince) >= 2*time.Second {
					// Tasmota reboots once after saving Wi-Fi and again after the template.
					// Require a short stable window so the returned URL is not the first boot.
					return nil
				}
			} else {
				upSince = time.Time{}
			}
		} else {
			upSince = time.Time{}
		}
		time.Sleep(700 * time.Millisecond)
	}
	return errors.New("Tasmota did not start its web server in time. Check the Wi-Fi settings")
}

func randomID() string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}

func lastLines(text string, count int) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
