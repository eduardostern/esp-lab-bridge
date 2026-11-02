// ESP Lab Bridge is a tiny local companion for the hosted ESP Lab.
//
// It starts the native esp32sim engine, exposes the Tasmota web server on loopback,
// and listens on http://127.0.0.1:8788 so the hosted app can ask for a session.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

type config struct {
	listen  string
	engine  string
	assets  string
	verbose bool
	origins []string
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }

func (s *stringList) Set(value string) error {
	*s = append(*s, strings.TrimSpace(value))
	return nil
}

func main() {
	var origins stringList
	cfg := config{}
	flag.StringVar(&cfg.listen, "listen", "127.0.0.1:8788", "loopback address to listen on")
	flag.StringVar(&cfg.engine, "engine", "", "path to esp32sim (default: next to this binary, ESP32SIM_BIN, or PATH)")
	flag.StringVar(&cfg.assets, "assets", "", "firmware cache directory (default: the user cache directory)")
	flag.BoolVar(&cfg.verbose, "verbose", false, "print engine output")
	flag.Var(&origins, "origin", "allowed browser origin (repeatable)")
	flag.Parse()

	if err := loopbackOnly(cfg.listen); err != nil {
		log.Fatal(err)
	}
	if len(origins) == 0 {
		origins = defaultOrigins()
	}
	cfg.origins = origins

	engine, err := findEngine(cfg.engine)
	if err != nil {
		log.Printf("warning: %v", err)
		engine = ""
	}
	store := newAssetStore(cfg.assets, cfg.verbose)
	manager := newSessionManager(engine, store, cfg.verbose)
	defer manager.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler(manager, engine))
	mux.HandleFunc("/session", sessionHandler(manager))
	mux.HandleFunc("/session/", sessionHandler(manager))
	mux.HandleFunc("/", pageHandler)

	server := &http.Server{
		Addr:              cfg.listen,
		Handler:           withCORS(mux, cfg.origins),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()

	log.Printf("ESP Lab Bridge %s listening on http://%s", version, cfg.listen)
	if engine == "" {
		log.Printf("esp32sim was not found; put it next to the bridge or set ESP32SIM_BIN")
	}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func defaultOrigins() []string {
	return []string{
		"https://esp-lab.stern.workers.dev",
		"http://localhost:3000",
		"http://127.0.0.1:3000",
	}
}

func loopbackOnly(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	switch strings.Trim(host, "[]") {
	case "127.0.0.1", "localhost", "::1":
		return nil
	default:
		return fmt.Errorf("the bridge only listens on loopback; use 127.0.0.1:8788")
	}
}

func findEngine(explicit string) (string, error) {
	var candidates []string
	if explicit != "" {
		candidates = append(candidates, explicit)
	}
	if env := os.Getenv("ESP32SIM_BIN"); env != "" {
		candidates = append(candidates, env)
	}
	if executable, err := os.Executable(); err == nil {
		name := "esp32sim"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), name))
	}
	candidates = append(candidates, "esp32sim")

	for _, candidate := range candidates {
		if candidate == "esp32sim" {
			if path, err := exec.LookPath(candidate); err == nil {
				return path, nil
			}
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			absolute, err := filepath.Abs(candidate)
			if err != nil {
				return candidate, nil
			}
			return absolute, nil
		}
	}
	return "", errors.New("esp32sim not found (put it next to esp-lab-bridge or set ESP32SIM_BIN)")
}

func healthHandler(manager *sessionManager, engine string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"version": version,
			"engine":  engine != "",
			"session": manager.Current(),
		})
	}
}

func sessionHandler(manager *sessionManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if r.URL.Path != "/session" {
				http.NotFound(w, r)
				return
			}
			startSession(w, r, manager)
		case http.MethodDelete:
			id := strings.TrimPrefix(r.URL.Path, "/session/")
			if id == "/session" {
				id = ""
			}
			if manager.Stop(id) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.NotFound(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func startSession(w http.ResponseWriter, r *http.Request, manager *sessionManager) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var request SessionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	session, err := manager.Start(r.Context(), request)
	if err != nil {
		var bad *badRequest
		if errors.As(err, &bad) {
			writeError(w, http.StatusBadRequest, bad.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, session)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func withCORS(next http.Handler, origins []string) http.Handler {
	allowed := make(map[string]bool, len(origins))
	for _, origin := range origins {
		allowed[strings.TrimRight(origin, "/")] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		origin := strings.TrimRight(r.Header.Get("Origin"), "/")
		// The local status page posts to /session from the same loopback origin.
		// That origin is not in the hosted allowlist, so accept it explicitly.
		if origin != "" && !allowed[origin] && !sameOrigin(r, origin) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "content-type")
		w.Header().Set("Access-Control-Max-Age", "600")
		if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request, origin string) bool {
	return origin == "http://"+r.Host || origin == "https://"+r.Host
}

func loopbackHost(hostport string) bool {
	host := hostport
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	}
	switch strings.Trim(host, "[]") {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}
