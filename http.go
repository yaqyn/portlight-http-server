package main

import (
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"time"
)

func handler(logger *slog.Logger) http.Handler {
	started := time.Now()
	var requests atomic.Uint64
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"service": "Portlight", "description": "A small Go HTTP service, built to run predictably.", "version": version})
	})
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"service": "Portlight", "version": version})
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"service": "Portlight", "status": "ok", "version": version, "started_at": started.UTC().Format(time.RFC3339), "uptime_seconds": int64(time.Since(started).Seconds()), "requests_total": requests.Load()})
	})
	mux.HandleFunc("GET /time", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"service": "Portlight", "status": "ok", "now": time.Now().UTC().Format(time.RFC3339Nano)})
	})
	for _, path := range []string{"/healthz", "/livez", "/readyz"} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})
	}
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestNumber := requests.Add(1)
		start := time.Now()
		requestID := rand.Text()
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Service-Version", version)
		w.Header().Set("X-Request-Count", strconv.FormatUint(requestNumber, 10))
		recorder := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Cache-Control", "no-store")
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("request panic", "panic", recovered, "stack", string(debug.Stack()))
				if !recorder.wrote {
					writeJSON(recorder, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				}
			}
			logger.Info("request", "request_id", requestID, "method", r.Method, "path", r.URL.Path, "status", recorder.status, "bytes_written", recorder.bytes, "duration_ms", time.Since(start).Milliseconds())
		}()
		known := r.URL.Path == "/" || r.URL.Path == "/version" || r.URL.Path == "/status" || r.URL.Path == "/time" || r.URL.Path == "/healthz" || r.URL.Path == "/livez" || r.URL.Path == "/readyz"
		if known && r.Method == http.MethodOptions {
			w.Header().Set("Allow", "GET, HEAD, OPTIONS")
			recorder.WriteHeader(http.StatusNoContent)
			return
		}
		if known && r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeJSON(recorder, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		if !known {
			writeJSON(recorder, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if r.URL.RawQuery != "" {
			writeJSON(recorder, http.StatusBadRequest, map[string]string{"error": "query parameters are not supported"})
			return
		}
		if r.Method == http.MethodHead {
			mux.ServeHTTP(&headResponseWriter{ResponseWriter: recorder}, r)
			return
		}
		mux.ServeHTTP(recorder, r)
	})
}

type headResponseWriter struct{ http.ResponseWriter }

func (w *headResponseWriter) Write(p []byte) (int, error) { return len(p), nil }

type responseWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
	bytes  int
}

func (w *responseWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.wrote {
		return
	}
	w.status = status
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, err
}
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
