package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
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
	if body["requests_total"] != float64(1) {
		t.Fatalf("unexpected request count: %v", body)
	}
	if body["go_version"] == "" || body["goroutines"].(float64) < 1 {
		t.Fatalf("missing runtime metadata: %v", body)
	}
}

func TestStatusRequestCountIncludesAllRoutes(t *testing.T) {
	h := handler(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/healthz", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/missing", nil))
	status := httptest.NewRecorder()
	h.ServeHTTP(status, httptest.NewRequest("GET", "/status", nil))
	var body map[string]any
	if err := json.Unmarshal(status.Body.Bytes(), &body); err != nil || body["requests_total"] != float64(3) {
		t.Fatalf("status=%d body=%s", status.Code, status.Body.String())
	}
}

func TestResponseRequestCountIsMonotonic(t *testing.T) {
	h := handler(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for index, expected := range []string{"1", "2", "3"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest("GET", "/healthz", nil))
		if response.Header().Get("X-Request-Count") != expected {
			t.Fatalf("request %d count=%q", index+1, response.Header().Get("X-Request-Count"))
		}
	}
}

func TestTimeEndpointReportsUTC(t *testing.T) {
	response := httptest.NewRecorder()
	handler(slog.Default()).ServeHTTP(response, httptest.NewRequest("GET", "/time", nil))
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != 200 {
		t.Fatalf("time: %d %s", response.Code, response.Body.String())
	}
	if _, err := time.Parse(time.RFC3339Nano, body["now"]); err != nil || body["status"] != "ok" {
		t.Fatalf("invalid time response: %v", body)
	}
}

func TestRequestEndpointDescribesTheCurrentRequest(t *testing.T) {
	request := httptest.NewRequest("GET", "https://example.test/request", nil)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "portlight-check/1.0")
	response := httptest.NewRecorder()
	handler(slog.Default()).ServeHTTP(response, request)
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["method"] != "GET" || body["protocol"] != "HTTP/1.1" || body["host"] != "example.test" || body["accept"] != "application/json" || body["user_agent"] != "portlight-check/1.0" {
		t.Fatalf("request response: %d %s", response.Code, response.Body.String())
	}
}

func TestRoutesEndpointListsDiscoverablePaths(t *testing.T) {
	response := httptest.NewRecorder()
	handler(slog.Default()).ServeHTTP(response, httptest.NewRequest("GET", "/routes", nil))
	var body struct {
		Routes []string `json:"routes"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
		t.Fatalf("routes: %d %s", response.Code, response.Body.String())
	}
	if !slices.Contains(body.Routes, "/status") || !slices.Contains(body.Routes, "/routes") {
		t.Fatalf("routes=%v", body.Routes)
	}
}

func TestBuildEndpointDescribesTheRuntimeTarget(t *testing.T) {
	response := httptest.NewRecorder()
	handler(slog.Default()).ServeHTTP(response, httptest.NewRequest("GET", "/build", nil))
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
		t.Fatalf("build: %d %s", response.Code, response.Body.String())
	}
	if body["goos"] == "" || body["goarch"] == "" || body["go_version"] == "" || body["version"] != version {
		t.Fatalf("build=%v", body)
	}
}

func TestMemoryEndpointReportsRuntimeUsage(t *testing.T) {
	response := httptest.NewRecorder()
	handler(slog.Default()).ServeHTTP(response, httptest.NewRequest("GET", "/memory", nil))
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
		t.Fatalf("memory: %d %s", response.Code, response.Body.String())
	}
	if body["allocated_bytes"].(float64) <= 0 || body["heap_objects"].(float64) <= 0 {
		t.Fatalf("memory=%v", body)
	}
}

func TestMetricsExposeRequestCountAndUptime(t *testing.T) {
	h := handler(slog.Default())
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/healthz", nil))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "portlight_requests_total 2") || !strings.Contains(response.Body.String(), "portlight_uptime_seconds ") {
		t.Fatalf("metrics: %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("content type: %q", response.Header().Get("Content-Type"))
	}
	for _, want := range []string{"# HELP portlight_requests_total", "# TYPE portlight_requests_total counter", "# HELP portlight_uptime_seconds", "# TYPE portlight_uptime_seconds gauge"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("metrics omitted %q: %s", want, response.Body.String())
		}
	}
}

func TestVersionEndpointSupportsConditionalRequests(t *testing.T) {
	h := handler(slog.Default())
	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest("GET", "/version", nil))
	etag := first.Header().Get("ETag")
	request := httptest.NewRequest("GET", "/version", nil)
	request.Header.Set("If-None-Match", etag)
	conditional := httptest.NewRecorder()
	h.ServeHTTP(conditional, request)
	if etag == "" || first.Header().Get("Cache-Control") != "no-cache" || conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
		t.Fatalf("etag=%q first=%v conditional=%d %q", etag, first.Header(), conditional.Code, conditional.Body.String())
	}
}

func TestVersionConditionalRequestsAcceptListsWildcardsAndWeakTags(t *testing.T) {
	h := handler(slog.Default())
	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/version", nil))
	etag := first.Header().Get("ETag")
	for _, condition := range []string{
		`"another-version", ` + etag,
		"W/" + etag,
		"*",
	} {
		t.Run(condition, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/version", nil)
			request.Header.Set("If-None-Match", condition)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != http.StatusNotModified || response.Body.Len() != 0 {
				t.Fatalf("condition=%q status=%d body=%q", condition, response.Code, response.Body.String())
			}
		})
	}

	request := httptest.NewRequest(http.MethodGet, "/version", nil)
	request.Header.Set("If-None-Match", `"another-version"`)
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("nonmatching condition returned %d", response.Code)
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
		cacheControl string
	}{{"GET", "/", 200, "no-store"}, {"GET", "/version", 200, "no-cache"}, {"GET", "/status", 200, "no-store"}, {"GET", "/request", 200, "no-store"}, {"GET", "/healthz", 200, "no-store"}, {"GET", "/livez", 200, "no-store"}, {"GET", "/readyz", 200, "no-store"}, {"HEAD", "/", 200, "no-store"}, {"OPTIONS", "/status", 204, "no-store"}, {"GET", "/missing", 404, "no-store"}, {"POST", "/missing", 404, "no-store"}, {"POST", "/", 405, "no-store"}, {"POST", "/status", 405, "no-store"}, {"POST", "/healthz", 405, "no-store"}, {"POST", "/livez", 405, "no-store"}} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
			if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != tc.cacheControl {
				t.Fatal("missing response safeguards")
			}
			if w.Header().Get("X-Service-Version") != version {
				t.Fatal("missing service version header")
			}
			if _, err := time.Parse(time.RFC3339, w.Header().Get("X-Service-Started-At")); err != nil {
				t.Fatal("missing service start header")
			}
			if w.Header().Get("Permissions-Policy") == "" || w.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
				t.Fatal("missing browser isolation headers")
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
			if tc.method == http.MethodOptions && w.Header().Get("Allow") != "GET, HEAD, OPTIONS" {
				t.Fatal("OPTIONS omitted allowed methods")
			}
		})
	}
}

func TestHeadReturnsHeadersWithoutAResponseBody(t *testing.T) {
	h := handler(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for _, path := range []string{"/", "/version", "/status", "/request", "/metrics", "/healthz", "/livez", "/readyz"} {
		get := httptest.NewRecorder()
		h.ServeHTTP(get, httptest.NewRequest(http.MethodGet, path, nil))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodHead, path, nil))
		if w.Code != 200 || w.Body.Len() != 0 {
			t.Fatalf("%s: status=%d body=%q headers=%v", path, w.Code, w.Body.String(), w.Header())
		}
		if w.Header().Get("Content-Type") != get.Header().Get("Content-Type") {
			t.Fatalf("%s: HEAD content type %q, GET content type %q", path, w.Header().Get("Content-Type"), get.Header().Get("Content-Type"))
		}
		if w.Header().Get("Content-Length") != strconv.Itoa(get.Body.Len()) {
			t.Fatalf("%s: HEAD length %q, GET body length %d", path, w.Header().Get("Content-Length"), get.Body.Len())
		}
	}
}

func TestConditionalHeadOmitsRepresentationLength(t *testing.T) {
	h := handler(slog.Default())
	request := httptest.NewRequest(http.MethodHead, "/version", nil)
	request.Header.Set("If-None-Match", strconv.Quote(version))
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusNotModified || response.Body.Len() != 0 || response.Header().Get("Content-Length") != "" {
		t.Fatalf("status=%d body=%q headers=%v", response.Code, response.Body.String(), response.Header())
	}
}

func TestKnownRoutesRejectAmbiguousQueryParameters(t *testing.T) {
	h := handler(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for _, path := range []string{"/?debug=1", "/status?format=text", "/readyz?full=true"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var body map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusBadRequest || body["error"] != "query parameters are not supported" {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}
