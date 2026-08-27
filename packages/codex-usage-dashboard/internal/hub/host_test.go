package hub

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAllowedHostPolicy(t *testing.T) {
	state, err := New([]Identity{{Username: "codex", UID: 123}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	handler := testHTTPHandler(t, HTTPHandler{
		Hub: state,
		AllowedHosts: []string{
			"127.0.0.1",
			"[::1]",
			"itphlies.tailb3264.ts.net",
		},
	})

	tests := []struct {
		name           string
		host           string
		forwardedHost  string
		expectedStatus int
	}{
		{name: "loopback", host: "127.0.0.1", expectedStatus: http.StatusOK},
		{name: "loopback port", host: "127.0.0.1:8787", expectedStatus: http.StatusOK},
		{name: "loopback terminal dot", host: "127.0.0.1.:8787", expectedStatus: http.StatusOK},
		{name: "IPv6 bare", host: "::1", expectedStatus: http.StatusOK},
		{name: "IPv6 bracketed", host: "[::1]", expectedStatus: http.StatusOK},
		{name: "IPv6 bracketed port", host: "[::1]:8787", expectedStatus: http.StatusOK},
		{name: "DNS", host: "itphlies.tailb3264.ts.net", expectedStatus: http.StatusOK},
		{name: "DNS port", host: "itphlies.tailb3264.ts.net:443", expectedStatus: http.StatusOK},
		{name: "DNS case and terminal dot", host: "ITPHLIES.TAILB3264.TS.NET.:443", expectedStatus: http.StatusOK},
		{name: "untrusted forwarded host ignored", host: "itphlies.tailb3264.ts.net", forwardedHost: "attacker.invalid", expectedStatus: http.StatusOK},
		{name: "empty", host: "", expectedStatus: http.StatusMisdirectedRequest},
		{name: "other DNS", host: "attacker.invalid", expectedStatus: http.StatusMisdirectedRequest},
		{name: "DNS suffix", host: "itphlies.tailb3264.ts.net.attacker.invalid", expectedStatus: http.StatusMisdirectedRequest},
		{name: "DNS prefix", host: "attacker-itphlies.tailb3264.ts.net", expectedStatus: http.StatusMisdirectedRequest},
		{name: "userinfo", host: "attacker@itphlies.tailb3264.ts.net", expectedStatus: http.StatusMisdirectedRequest},
		{name: "nonnumeric port", host: "itphlies.tailb3264.ts.net:https", expectedStatus: http.StatusMisdirectedRequest},
		{name: "zero port", host: "itphlies.tailb3264.ts.net:0", expectedStatus: http.StatusMisdirectedRequest},
		{name: "large port", host: "itphlies.tailb3264.ts.net:65536", expectedStatus: http.StatusMisdirectedRequest},
		{name: "malformed bracket", host: "[::1", expectedStatus: http.StatusMisdirectedRequest},
		{name: "bracket suffix", host: "[::1].attacker.invalid", expectedStatus: http.StatusMisdirectedRequest},
		{name: "other IP", host: "127.0.0.2:8787", expectedStatus: http.StatusMisdirectedRequest},
		{name: "localhost not configured", host: "localhost:8787", expectedStatus: http.StatusMisdirectedRequest},
		{name: "forwarded host cannot authorize", host: "attacker.invalid", forwardedHost: "itphlies.tailb3264.ts.net", expectedStatus: http.StatusMisdirectedRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://placeholder.invalid/healthz", nil)
			request.Host = test.host
			if test.forwardedHost != "" {
				request.Header.Set("X-Forwarded-Host", test.forwardedHost)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.expectedStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.expectedStatus)
			}
			if response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("response is missing security headers")
			}
			if test.expectedStatus == http.StatusMisdirectedRequest && response.Body.String() != "misdirected request\n" {
				t.Fatalf("rejection body = %q", response.Body.String())
			}
		})
	}
}

func TestHTTPHandlerRejectsInvalidAllowedHosts(t *testing.T) {
	tests := []struct {
		name  string
		hosts []string
	}{
		{name: "none"},
		{name: "empty", hosts: []string{""}},
		{name: "whitespace", hosts: []string{" itphlies.tailb3264.ts.net"}},
		{name: "wildcard", hosts: []string{"*.tailb3264.ts.net"}},
		{name: "URL", hosts: []string{"https://itphlies.tailb3264.ts.net"}},
		{name: "DNS port", hosts: []string{"itphlies.tailb3264.ts.net:443"}},
		{name: "IPv6 port", hosts: []string{"[::1]:8787"}},
		{name: "scoped IPv6", hosts: []string{"fe80::1%lo"}},
		{name: "empty DNS label", hosts: []string{"itphlies..ts.net"}},
		{name: "leading hyphen", hosts: []string{"-itphlies.ts.net"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := (HTTPHandler{AllowedHosts: test.hosts}).Handler(); err == nil {
				t.Fatal("Handler unexpectedly accepted invalid allowed hosts")
			}
		})
	}
}
