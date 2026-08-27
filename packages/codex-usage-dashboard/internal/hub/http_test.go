package hub

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	usagehistory "codex-usage-dashboard/internal/history"
	"codex-usage-dashboard/internal/model"
)

func testHTTPHandler(t *testing.T, config HTTPHandler) http.Handler {
	t.Helper()
	if config.AllowedHosts == nil {
		config.AllowedHosts = []string{"127.0.0.1"}
	}
	handler, err := config.Handler()
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestHTTPStatusHealthAndSecurityHeaders(t *testing.T) {
	state, _ := New([]Identity{{Username: "codex", UID: 123}}, time.Minute)
	server := httptest.NewServer(testHTTPHandler(t, HTTPHandler{Hub: state}))
	defer server.Close()

	for _, path := range []string{"/healthz", "/api/v1/status"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.StatusCode)
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s missing no-store", path)
		}
		csp := response.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Fatalf("%s has weak CSP: %q", path, csp)
		}
		if response.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s missing nosniff", path)
		}
	}

	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/status", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed || response.Header.Get("Allow") != "GET" {
		t.Fatalf("POST status contract = %d Allow %q", response.StatusCode, response.Header.Get("Allow"))
	}
}

func TestSSEStartsWithSnapshotAndReconnects(t *testing.T) {
	state, _ := New([]Identity{{Username: "codex", UID: 123}}, time.Minute)
	server := httptest.NewServer(testHTTPHandler(t, HTTPHandler{Hub: state}))
	defer server.Close()

	readInitial := func() model.StatusResponse {
		t.Helper()
		client := &http.Client{Timeout: 2 * time.Second}
		response, err := client.Get(server.URL + "/api/v1/events")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		reader := bufio.NewReader(response.Body)
		var data string
		var sawID, sawEvent bool
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "id: ") {
				sawID = true
			}
			if line == "event: snapshot" {
				sawEvent = true
			}
			if strings.HasPrefix(line, "data: ") {
				data = strings.TrimPrefix(line, "data: ")
			}
		}
		if !sawID || !sawEvent || data == "" {
			t.Fatalf("incomplete SSE event: id=%v event=%v data=%q", sawID, sawEvent, data)
		}
		var status model.StatusResponse
		if err := json.Unmarshal([]byte(data), &status); err != nil {
			t.Fatal(err)
		}
		return status
	}

	first := readInitial()
	second := readInitial()
	if len(first.Accounts) != 1 || len(second.Accounts) != 1 {
		t.Fatal("reconnect did not receive an immediate full state")
	}
}

func TestHistoryEndpointIsReadOnlyAndOmitsAccountIdentity(t *testing.T) {
	retained, err := usagehistory.Open("", []string{"codex"}, 14*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	state, _ := New([]Identity{{Username: "codex", UID: 123}}, time.Minute)
	server := httptest.NewServer(testHTTPHandler(t, HTTPHandler{Hub: state, History: retained}))
	defer server.Close()

	response, err := http.Get(server.URL + "/api/v1/history")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(payload), `"username":"codex"`) {
		t.Fatalf("unexpected history response: status=%d body=%q", response.StatusCode, payload)
	}
	for _, forbidden := range []string{"email", "planType", "credits", "Spark"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("history response contains %q", forbidden)
		}
	}

	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/history", nil)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusMethodNotAllowed || response.Header.Get("Allow") != "GET" {
		t.Fatalf("POST history status = %d Allow %q", response.StatusCode, response.Header.Get("Allow"))
	}
}

func TestSSEStreamsUpdatesAndStaleTransition(t *testing.T) {
	state, _ := New([]Identity{{Username: "codex", UID: 123}}, 40*time.Millisecond)
	if err := state.Apply(123, testSnapshot("codex", time.Now().UTC(), 19)); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(testHTTPHandler(t, HTTPHandler{Hub: state}))
	defer server.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(server.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	first := readSnapshotEvent(t, reader)
	if first.Accounts[0].Stale {
		t.Fatal("fresh initial SSE snapshot was stale")
	}

	updated := testSnapshot("codex", time.Now().UTC(), 41)
	if err := state.Apply(123, updated); err != nil {
		t.Fatal(err)
	}
	second := readSnapshotEvent(t, reader)
	if got := second.Accounts[0].Limits[0].Primary.UsedPercent; got != 41 {
		t.Fatalf("streamed update used percentage = %d", got)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status := readSnapshotEvent(t, reader)
		if status.Accounts[0].Stale {
			return
		}
	}
	t.Fatal("periodic SSE snapshots never reported the stale transition")
}

func readSnapshotEvent(t *testing.T, reader *bufio.Reader) model.StatusResponse {
	t.Helper()
	var data string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
	}
	if data == "" {
		t.Fatal("SSE event had no data")
	}
	var status model.StatusResponse
	if err := json.Unmarshal([]byte(data), &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestSSEConnectionLimit(t *testing.T) {
	state, _ := New([]Identity{{Username: "codex", UID: 123}}, time.Minute)
	handler := testHTTPHandler(t, HTTPHandler{Hub: state, sseSlots: make(chan struct{}, 1)})
	server := httptest.NewServer(handler)
	defer server.Close()

	first, err := http.Get(server.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Body.Close()

	client := &http.Client{Timeout: time.Second}
	second, err := client.Get(server.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Body.Close()
	if second.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second stream status = %d, want 503", second.StatusCode)
	}
}
