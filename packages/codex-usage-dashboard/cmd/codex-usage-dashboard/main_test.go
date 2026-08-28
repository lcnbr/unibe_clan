package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateListenAddress(t *testing.T) {
	valid := map[string]string{
		"127.0.0.1:8787": "127.0.0.1",
		"127.12.4.9:443": "127.12.4.9",
		"[::1]:8787":     "::1",
	}
	for address, expectedHost := range valid {
		host, err := validateListenAddress(address)
		if err != nil {
			t.Errorf("validateListenAddress(%q): %v", address, err)
		} else if host != expectedHost {
			t.Errorf("validateListenAddress(%q) host = %q, want %q", address, host, expectedHost)
		}
	}
	invalid := []string{
		"0.0.0.0:8787",
		"[::]:8787",
		"192.168.1.2:8787",
		"localhost:8787",
		"127.0.0.1:0",
		"127.0.0.1:-1",
		"127.0.0.1:65536",
		"127.0.0.1",
		"::1:8787",
	}
	for _, address := range invalid {
		if _, err := validateListenAddress(address); err == nil {
			t.Errorf("validateListenAddress(%q) unexpectedly succeeded", address)
		}
	}
}

func TestHookReportFailsOpenAfterValidConfiguration(t *testing.T) {
	var output bytes.Buffer
	err := runHookReport(
		context.Background(),
		[]string{"--socket", filepath.Join(t.TempDir(), "missing.sock")},
		strings.NewReader(`{"prompt":"must not leak"}`),
		&output,
		io.Discard,
	)
	if err != nil {
		t.Fatalf("runHookReport: %v", err)
	}
	if output.String() != "{}\n" {
		t.Fatalf("hook output = %q, want empty JSON object", output.String())
	}
}

func TestHookReportRejectsRelativeSocketConfiguration(t *testing.T) {
	err := runHookReport(
		context.Background(),
		[]string{"--socket", "activity.sock"},
		strings.NewReader(`{}`),
		io.Discard,
		io.Discard,
	)
	if err == nil {
		t.Fatal("runHookReport unexpectedly accepted a relative socket")
	}
}

func TestAnchorList(t *testing.T) {
	anchors := anchorList{}
	if err := anchors.Set("codex-dummy-0=localunitarity@gmail.com"); err != nil {
		t.Fatalf("Set first anchor: %v", err)
	}
	if err := anchors.Set("codex-dummy-1=localunitarity+1@gmail.com"); err != nil {
		t.Fatalf("Set second anchor: %v", err)
	}
	if got := anchors["codex-dummy-1"]; got != "localunitarity+1@gmail.com" {
		t.Fatalf("second anchor = %q", got)
	}
	if err := anchors.Set("codex-dummy-0=other@example.com"); err == nil {
		t.Fatal("duplicate anchor user unexpectedly accepted")
	}
	if err := anchors.Set("missing-separator"); err == nil {
		t.Fatal("malformed anchor unexpectedly accepted")
	}
}

func TestServeRejectsUnsafeActivityConfigurationBeforeUserLookup(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "relative socket",
			args: []string{"--activity-socket", "activity.sock"},
			want: "activity socket must be an absolute path",
		},
		{
			name: "shared socket",
			args: []string{
				"--socket", "/tmp/codex-dashboard.sock",
				"--activity-socket", "/tmp/codex-dashboard.sock",
			},
			want: "activity and ingest sockets must use different paths",
		},
		{
			name: "unbounded lease",
			args: []string{"--activity-lease", "25h"},
			want: "activity-lease must be greater than zero and no more than 24 hours",
		},
		{
			name: "unknown socket group",
			args: []string{"--activity-socket-group", "codex-dashboard-group-that-does-not-exist"},
			want: "activity socket group \"codex-dashboard-group-that-does-not-exist\" does not exist",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := runServe(context.Background(), test.args, io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("runServe() error = %v, want %q", err, test.want)
			}
		})
	}
}
