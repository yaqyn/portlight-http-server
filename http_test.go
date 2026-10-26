package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestLogRecordsWrittenBodyBytes(t *testing.T) {
	var logs bytes.Buffer
	h := handler(slog.New(slog.NewJSONHandler(&logs, nil)))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["bytes_written"] != float64(response.Body.Len()) || response.Body.Len() == 0 {
		t.Fatalf("body=%d log=%s", response.Body.Len(), &logs)
	}
}

func TestStatusReportsServiceUptime(t *testing.T) {
	response := httptest.NewRecorder()
	handler(slog.Default()).ServeHTTP(response, httptest.NewRequest("GET", "/status", nil))
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != 200 {
		t.Fatalf("status: %d %s", response.Code, response.Body.String())
	}
	if uptime, ok := body["uptime_seconds"].(float64); !ok || uptime < 0 {
		t.Fatalf("missing uptime: %v", body)
	}
	if started, ok := body["started_at"].(string); !ok {
		t.Fatalf("missing start time: %v", body)
	} else if _, err := time.Parse(time.RFC3339, started); err != nil {
		t.Fatalf("invalid start time: %q", started)
	}
}

func TestAddress(t *testing.T) {
	for _, tc := range []struct {
		port, want string
		invalid    bool
	}{{"", ":8991", false}, {"8080", ":8080", false}, {"1", ":1", false}, {"65535", ":65535", false}, {"0", "", true}, {"65536", "", true}, {"abc", "", true}, {"-1", "", true}} {
		got, err := address(tc.port)
		if (err != nil) != tc.invalid || got != tc.want {
			t.Errorf("address(%q) = %q, %v", tc.port, got, err)
		}
	}
}

func TestRequestIDsMatchResponseAndLogsAndAreUnique(t *testing.T) {
	var logs bytes.Buffer
	h := handler(slog.New(slog.NewJSONHandler(&logs, nil)))
	previous := ""
	for _, path := range []string{"/", "/missing"} {
		logs.Reset()
		response := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("X-Request-ID", "untrusted-client-id")
		h.ServeHTTP(response, req)
		id := response.Header().Get("X-Request-ID")
		var entry map[string]any
		if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		if id == "" || id == previous || id == "untrusted-client-id" || entry["request_id"] != id {
			t.Fatalf("id=%q previous=%q log=%s", id, previous, &logs)
		}
		previous = id
	}
}

func TestInformationalResponseDoesNotHideFinalStatus(t *testing.T) {
	statuses := make(chan int, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		recorder.WriteHeader(http.StatusEarlyHints)
		recorder.WriteHeader(http.StatusCreated)
		recorder.Write([]byte("created"))
		statuses <- recorder.status
	}))
	defer server.Close()
	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	observed := <-statuses
	if response.StatusCode != http.StatusCreated || observed != http.StatusCreated || string(body) != "created" {
		t.Fatalf("status=%d logged=%d body=%q", response.StatusCode, observed, body)
	}
}

func TestHTTPContract(t *testing.T) {
	h := handler(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/", 200}, {"GET", "/version", 200}, {"GET", "/status", 200}, {"GET", "/healthz", 200}, {"GET", "/livez", 200}, {"GET", "/readyz", 200}, {"HEAD", "/", 200}, {"GET", "/missing", 404}, {"POST", "/missing", 404}, {"POST", "/", 405}, {"POST", "/status", 405}, {"POST", "/healthz", 405}, {"POST", "/livez", 405}} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
			if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing response safeguards")
			}
			if w.Header().Get("Content-Security-Policy") != "default-src 'none'; frame-ancestors 'none'" {
				t.Fatal("missing content security policy")
			}
			if tc.method == "GET" && tc.status == 200 {
				var body map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if (tc.path == "/" || tc.path == "/version" || tc.path == "/status") && body["service"] != "Portlight" {
					t.Fatal("wrong service identity")
				}
				if tc.path == "/version" && body["version"] == "" {
					t.Fatal("version endpoint omitted build version")
				}
				if tc.path != "/" && tc.path != "/version" && body["status"] != "ok" {
					t.Fatal("probe not healthy")
				}
			}
			if tc.method == "GET" && tc.status == 404 {
				var body map[string]string
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] != "not found" {
					t.Fatalf("unexpected not-found response: %s", w.Body)
				}
			}
			if tc.status == http.StatusMethodNotAllowed {
				var body map[string]string
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] != "method not allowed" {
					t.Fatalf("unexpected method response: %s", w.Body)
				}
				if w.Header().Get("Allow") != "GET, HEAD" {
					t.Fatal("missing Allow header")
				}
			}
		})
	}
}
