// kanban is a small self-hosted kanban board server.
// Persists to a JSON file, exposes an HTTP API + a static drag-and-drop frontend.
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed static
var staticFS embed.FS

func main() {
	listen      := flag.String("listen", "127.0.0.1:8765", "address to listen on (host:port)")
	statePath   := flag.String("state", defaultStatePath(), "path to the JSON state file")
	agentsFlag  := flag.String("agents", "", "comma-separated agent names for @-mention suggestions")
	attachFlag  := flag.String("attachments", "", "directory to store uploaded files (default: <state-dir>/attachments)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	board, err := NewBoard(*statePath)
	if err != nil {
		logger.Error("board load failed", "error", err)
		os.Exit(1)
	}

	agents := splitTrimmed(*agentsFlag)
	attachDir := *attachFlag
	if attachDir == "" {
		attachDir = filepath.Join(filepath.Dir(*statePath), "attachments")
	}
	api := NewMux(board, agents, attachDir)

	// Serve the embedded static frontend at /.
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		logger.Error("static fs", "error", err)
		os.Exit(1)
	}
	staticHandler := http.FileServer(http.FS(static))

	root := http.NewServeMux()
	root.Handle("/api/", logRequests(logger, api))
	root.Handle("/", staticHandler)

	authUser := os.Getenv("KANBAN_USER")
	authPass := os.Getenv("KANBAN_PASS")
	handler := requireBasicAuth(authUser, authPass, root)
	authMode := "off"
	if authUser != "" || authPass != "" {
		authMode = "basic"
	}
	logger.Info("kanban starting", "listen", *listen, "state", *statePath, "cards", len(board.ListCards()), "auth", authMode)

	srv := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func splitTrimmed(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func defaultStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "kanban-state.json"
	}
	return filepath.Join(home, ".kanban", "state.json")
}

// logRequests wraps a handler with a one-line access log per request.
func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, code: 200}
		next.ServeHTTP(rw, r)
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.code,
			"dur_ms", time.Since(start).Milliseconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(c int) {
	s.code = c
	s.ResponseWriter.WriteHeader(c)
}
