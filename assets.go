package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	factoryFile = "tasmota32.factory.bin"
	romFile     = "esp32_rev300_rom.elf"

	tasmotaURL = "https://github.com/arendst/Tasmota/releases/download/v16.0.0/tasmota32.factory.bin"
	tasmotaSHA = "a0c6ed5fa59e18885db435cb22cb0456d6c9bce92477092462224ab6fa5942eb"

	romTarballURL = "https://github.com/espressif/esp-rom-elfs/releases/download/20260528/esp-rom-elfs-20260528.tar.gz"
	romTarballSHA = "caa463d3cbef2430a5a35847c1d9f2f152403b17a802050927ff60c8da54fe46"
	romSHA        = "920b70635440517866aab2230964a570d2cf2b676658d93c52fbac108c1cca31"
)

type assetStore struct {
	dir     string
	mu      sync.Mutex
	client  *http.Client
	verbose bool
}

type assetPaths struct {
	Factory string
	ROM     string
}

func newAssetStore(dir string, verbose bool) *assetStore {
	if dir == "" {
		dir = defaultCacheDir()
	}
	return &assetStore{
		dir:     dir,
		client:  &http.Client{Timeout: 2 * time.Minute},
		verbose: verbose,
	}
}

func defaultCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "esp-lab-bridge", "firmware")
}

func (a *assetStore) Ensure(ctx context.Context) (assetPaths, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := os.MkdirAll(a.dir, 0o755); err != nil {
		return assetPaths{}, err
	}
	factory := filepath.Join(a.dir, factoryFile)
	if !matchesSHA(factory, tasmotaSHA) {
		if a.verbose {
			fmt.Printf("downloading %s\n", tasmotaURL)
		}
		if err := a.download(ctx, tasmotaURL, factory, tasmotaSHA); err != nil {
			return assetPaths{}, fmt.Errorf("download Tasmota image: %w", err)
		}
	}
	rom := filepath.Join(a.dir, romFile)
	if !matchesSHA(rom, romSHA) {
		if a.verbose {
			fmt.Printf("downloading %s\n", romTarballURL)
		}
		if err := a.downloadROM(ctx, rom); err != nil {
			return assetPaths{}, fmt.Errorf("download ESP32 mask ROM: %w", err)
		}
	}
	return assetPaths{Factory: factory, ROM: rom}, nil
}

func (a *assetStore) download(ctx context.Context, url, target, expected string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("user-agent", "esp-lab-bridge/"+version)
	response, err := a.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}

	temporary, err := os.CreateTemp(a.dir, ".download-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)

	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temporary, hash), response.Body); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != expected {
		return fmt.Errorf("SHA-256 mismatch: got %s", got)
	}
	_ = os.Remove(target)
	return os.Rename(name, target)
}

func (a *assetStore) downloadROM(ctx context.Context, target string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, romTarballURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("user-agent", "esp-lab-bridge/"+version)
	response, err := a.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}

	tarball, err := os.CreateTemp(a.dir, ".rom-*.tar.gz")
	if err != nil {
		return err
	}
	tarballName := tarball.Name()
	defer os.Remove(tarballName)

	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tarball, hash), response.Body); err != nil {
		_ = tarball.Close()
		return err
	}
	if err := tarball.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != romTarballSHA {
		return fmt.Errorf("tarball SHA-256 mismatch: got %s", got)
	}

	file, err := os.Open(tarballName)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg || path.Base(header.Name) != romFile {
			continue
		}
		return writeVerified(target, reader, romSHA)
	}
	return fmt.Errorf("%s not found in %s", romFile, romTarballURL)
}

func writeVerified(target string, source io.Reader, expected string) error {
	temporary, err := os.CreateTemp(filepath.Dir(target), ".asset-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)

	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temporary, hash), source); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != expected {
		return fmt.Errorf("SHA-256 mismatch: got %s", got)
	}
	_ = os.Remove(target)
	return os.Rename(name, target)
}

func matchesSHA(path, expected string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false
	}
	return strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), expected)
}
