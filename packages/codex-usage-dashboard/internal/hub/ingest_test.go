//go:build linux

package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startTestIngest(t *testing.T, state *Hub, maxPayload int64) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	socket := filepath.Join(t.TempDir(), "ingest.sock")
	server := &IngestServer{
		Hub:            state,
		SocketPath:     socket,
		MaxPayload:     maxPayload,
		ReadTimeout:    time.Second,
		MaxConnections: 2,
	}
	errC := make(chan error, 1)
	go func() { errC <- server.Serve(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Lstat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("ingest socket did not appear")
		}
		time.Sleep(5 * time.Millisecond)
	}
	stop := func() {
		cancel()
		select {
		case err := <-errC:
			if err != nil {
				t.Errorf("ingest shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("ingest server did not stop")
		}
	}
	return socket, stop
}

func sendPayload(t *testing.T, socket string, payload []byte) map[string]any {
	t.Helper()
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	unixConn := conn.(*net.UnixConn)
	if err := unixConn.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var reply map[string]any
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	return reply
}

func TestIngestUsesPeerUIDAndRejectsSpoofAndUnknownFields(t *testing.T) {
	uid := uint32(os.Getuid())
	now := time.Now().UTC()
	state, _ := New([]Identity{{Username: "codex", UID: uid}}, time.Minute)
	socket, stop := startTestIngest(t, state, 64<<10)
	defer stop()

	valid, _ := json.Marshal(testSnapshot("codex", now, 23))
	reply := sendPayload(t, socket, append(valid, '\n'))
	if reply["ok"] != true {
		t.Fatalf("valid snapshot rejected: %#v", reply)
	}

	spoofed, _ := json.Marshal(testSnapshot("codex-1", now, 23))
	reply = sendPayload(t, socket, append(spoofed, '\n'))
	if reply["error"] != "rejected" {
		t.Fatalf("spoofed username response: %#v", reply)
	}

	withSecret := []byte(fmt.Sprintf(`{"schemaVersion":1,"username":"codex","state":"signed_out","limits":[],"observedAt":%q,"accessToken":"secret-sentinel"}`, now.Format(time.RFC3339Nano)))
	reply = sendPayload(t, socket, append(withSecret, '\n'))
	if reply["error"] != "invalid_payload" {
		t.Fatalf("credential-like unknown field response: %#v", reply)
	}
}

func TestIngestRejectsOversizedAndUntrustedUID(t *testing.T) {
	uid := uint32(os.Getuid())
	state, _ := New([]Identity{{Username: "codex", UID: uid + 1}}, time.Minute)
	socket, stop := startTestIngest(t, state, 1024)
	defer stop()

	reply := sendPayload(t, socket, []byte(`{"padding":"`+strings.Repeat("x", 1100)+`"}`))
	if reply["error"] != "invalid_payload" {
		t.Fatalf("oversized payload response: %#v", reply)
	}

	snapshot, _ := json.Marshal(testSnapshot("codex", time.Now().UTC(), 5))
	reply = sendPayload(t, socket, append(snapshot, '\n'))
	if reply["error"] != "rejected" {
		t.Fatalf("untrusted UID response: %#v", reply)
	}
}

func TestSecondIngestServerCannotUnlinkLiveSocket(t *testing.T) {
	uid := uint32(os.Getuid())
	state, _ := New([]Identity{{Username: "codex", UID: uid}}, time.Minute)
	socket, stop := startTestIngest(t, state, 64<<10)
	defer stop()

	duplicate := &IngestServer{Hub: state, SocketPath: socket}
	if err := duplicate.Serve(context.Background()); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("duplicate server error = %v", err)
	}
	valid, _ := json.Marshal(testSnapshot("codex", time.Now().UTC(), 17))
	if reply := sendPayload(t, socket, append(valid, '\n')); reply["ok"] != true {
		t.Fatalf("duplicate startup damaged live server: %#v", reply)
	}
}

func TestOwnedSocketCleanupPreservesReplacement(t *testing.T) {
	directory := t.TempDir()
	originalPath := filepath.Join(directory, "ingest.sock")
	original, err := net.ListenUnix("unix", &net.UnixAddr{Name: originalPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	original.SetUnlinkOnClose(false)
	owned, err := os.Lstat(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}

	replacementPath := filepath.Join(directory, "replacement.sock")
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: replacementPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	replacement.SetUnlinkOnClose(false)
	defer func() {
		_ = replacement.Close()
		_ = os.Remove(originalPath)
	}()
	if err := os.Remove(originalPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementPath, originalPath); err != nil {
		t.Fatal(err)
	}
	if err := removeOwnedSocket(originalPath, owned); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("unix", originalPath, time.Second)
	if err != nil {
		t.Fatalf("owned cleanup removed replacement socket: %v", err)
	}
	_ = conn.Close()
}
