//go:build linux

package activity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func startActivityServer(t *testing.T, tracker *Tracker, onChange func()) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	socketPath := filepath.Join(t.TempDir(), "activity.sock")
	server := &Server{
		Tracker:        tracker,
		OnChange:       onChange,
		SocketPath:     socketPath,
		MaxPayload:     4 << 10,
		ReadTimeout:    time.Second,
		MaxConnections: 4,
	}
	errC := make(chan error, 1)
	go func() { errC <- server.Serve(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Lstat(socketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("activity socket did not appear")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return socketPath, func() {
		cancel()
		select {
		case err := <-errC:
			if err != nil {
				t.Errorf("activity server shutdown: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("activity server did not stop")
		}
	}
}

func sendRawActivity(t *testing.T, socketPath string, payload []byte) serverReply {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := conn.(*net.UnixConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var reply serverReply
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	return reply
}

func TestReporterServerEndToEndSanitizesAndAuthenticatesPeer(t *testing.T) {
	uid := uint32(os.Getuid())
	tracker, err := New([]Identity{{Username: "real-user", UID: uid}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var changes atomic.Int32
	socketPath, stop := startActivityServer(t, tracker, func() { changes.Add(1) })
	defer stop()

	secret := "secret-prompt-tool-path-repository-environment-sentinel"
	payload := `{
		"hook_event_name":"UserPromptSubmit",
		"session_id":"session-1",
		"turn_id":"turn-1",
		"username":"spoofed-user",
		"prompt":"` + secret + `",
		"preview":"` + secret + `",
		"tool_input":{"command":"` + secret + `"},
		"cwd":"/` + secret + `",
		"repository":{"remote":"` + secret + `"},
		"environment":{"TOKEN":"` + secret + `"}
	}`
	var output bytes.Buffer
	if err := Report(context.Background(), socketPath, strings.NewReader(payload), &output); err != nil {
		t.Fatalf("sanitized report failed: %v", err)
	}
	if output.String() != "{}\n" {
		t.Fatalf("start hook output = %q", output.String())
	}
	chats := tracker.Snapshot()
	if len(chats) != 1 || chats[0].Username != "real-user" || chats[0].SessionID != "session-1" || !chats[0].Running {
		t.Fatalf("unexpected authenticated state: %#v", chats)
	}

	output.Reset()
	stopPayload := `{"hook_event_name":"Stop","session_id":"session-1","turn_id":"turn-1","prompt":"` + secret + `"}`
	if err := Report(context.Background(), socketPath, strings.NewReader(stopPayload), &output); err != nil {
		t.Fatalf("stop report failed: %v", err)
	}
	if output.String() != "{}\n" {
		t.Fatalf("Stop output = %q", output.String())
	}
	if got := tracker.Snapshot(); len(got) != 1 || got[0].Running {
		t.Fatalf("Stop did not retain one idle chat: %#v", got)
	}

	output.Reset()
	endPayload := `{"hook_event_name":"SessionEnd","session_id":"session-1","cwd":"/` + secret + `"}`
	if err := Report(context.Background(), socketPath, strings.NewReader(endPayload), &output); err != nil {
		t.Fatalf("session-end report failed: %v", err)
	}
	if output.String() != "{}\n" {
		t.Fatalf("SessionEnd output = %q", output.String())
	}
	if got := tracker.Snapshot(); len(got) != 0 {
		t.Fatalf("SessionEnd left a chat: %#v", got)
	}
	if changes.Load() != 3 {
		t.Fatalf("OnChange calls = %d, want 3", changes.Load())
	}
}

func TestServerRejectsSpoofFieldsAndUntrustedPeer(t *testing.T) {
	uid := uint32(os.Getuid())
	tracker, err := New([]Identity{{Username: "real-user", UID: uid}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	socketPath, stop := startActivityServer(t, tracker, nil)
	defer stop()

	spoof := []byte(`{"schemaVersion":1,"action":"start","sessionId":"session","turnId":"turn","username":"spoofed"}\n`)
	if reply := sendRawActivity(t, socketPath, spoof); reply.Error != "invalid_payload" || reply.OK {
		t.Fatalf("spoof response = %#v", reply)
	}
	if got := tracker.Snapshot(); len(got) != 0 {
		t.Fatalf("spoof payload changed state: %#v", got)
	}

	untrusted, err := New([]Identity{{Username: "someone-else", UID: uid + 1}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	untrustedSocket, stopUntrusted := startActivityServer(t, untrusted, nil)
	defer stopUntrusted()
	valid, err := json.Marshal(activityEvent(ActionStart, "session", "turn"))
	if err != nil {
		t.Fatal(err)
	}
	if reply := sendRawActivity(t, untrustedSocket, append(valid, '\n')); reply.Error != "rejected" || reply.OK {
		t.Fatalf("untrusted peer response = %#v", reply)
	}
}

func TestServerSetsConfiguredSocketGroupAndMode(t *testing.T) {
	uid := uint32(os.Getuid())
	tracker, err := New([]Identity{{Username: "real-user", UID: uid}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	socketPath := filepath.Join(t.TempDir(), "activity.sock")
	groupID := os.Getgid()
	server := &Server{
		Tracker:       tracker,
		SocketPath:    socketPath,
		SocketGroupID: &groupID,
	}
	errC := make(chan error, 1)
	go func() { errC <- server.Serve(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		info, statErr := os.Lstat(socketPath)
		if statErr == nil {
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				cancel()
				t.Fatal("activity socket has no Linux stat metadata")
			}
			if int(stat.Gid) == groupID && info.Mode().Perm() == 0o660 {
				break
			}
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("activity socket did not receive group %d and mode 0660", groupID)
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-errC:
		if err != nil {
			t.Fatalf("activity server shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("activity server did not stop")
	}
}

func TestServerRejectsNegativeSocketGroupBeforeCreatingPath(t *testing.T) {
	tracker, err := New([]Identity{{Username: "real-user", UID: uint32(os.Getuid())}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(t.TempDir(), "activity.sock")
	groupID := -1
	err = (&Server{
		Tracker:       tracker,
		SocketPath:    socketPath,
		SocketGroupID: &groupID,
	}).Serve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "group ID must be non-negative") {
		t.Fatalf("Serve() error = %v", err)
	}
	if _, statErr := os.Lstat(socketPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid configuration created an activity path: %v", statErr)
	}
}

func TestServerExpiresAndNotifiesWithoutAnotherRequest(t *testing.T) {
	uid := uint32(os.Getuid())
	tracker, err := New([]Identity{{Username: "real-user", UID: uid}}, 40*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	notifications := make(chan struct{}, 4)
	socketPath, stop := startActivityServer(t, tracker, func() { notifications <- struct{}{} })
	defer stop()
	valid, err := json.Marshal(activityEvent(ActionStart, "session", "turn"))
	if err != nil {
		t.Fatal(err)
	}
	if reply := sendRawActivity(t, socketPath, append(valid, '\n')); !reply.OK {
		t.Fatalf("start response = %#v", reply)
	}
	select {
	case <-notifications:
	case <-time.After(time.Second):
		t.Fatal("accepted event did not notify")
	}
	select {
	case <-notifications:
	case <-time.After(2 * time.Second):
		t.Fatal("lease expiry did not notify")
	}
	if got := tracker.Snapshot(); len(got) != 0 {
		t.Fatalf("background expiry left activity: %#v", got)
	}
}

func TestServerRefusesLiveSocketAndRemovesStaleSocket(t *testing.T) {
	directory := t.TempDir()
	socketPath := filepath.Join(directory, "activity.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := removeStaleSocket(socketPath); err == nil {
		listener.Close()
		t.Fatal("live activity socket was removed")
	}
	if info, err := os.Lstat(socketPath); err != nil || info.Mode()&os.ModeSocket == 0 {
		listener.Close()
		t.Fatalf("live socket path was not preserved: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := removeStaleSocket(socketPath); err != nil {
		t.Fatalf("stale socket was not removable: %v", err)
	}
	if _, err := os.Lstat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket path remains: %v", err)
	}
}

func TestServerCleanupDoesNotUnlinkReplacementSocket(t *testing.T) {
	uid := uint32(os.Getuid())
	tracker, err := New([]Identity{{Username: "real-user", UID: uid}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	socketPath := filepath.Join(t.TempDir(), "activity.sock")
	server := &Server{Tracker: tracker, SocketPath: socketPath, ReadTimeout: time.Second}
	errC := make(chan error, 1)
	go func() { errC <- server.Serve(ctx) }()
	waitForSocket(t, socketPath)

	if err := os.Remove(socketPath); err != nil {
		cancel()
		t.Fatal(err)
	}
	replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	replacement.SetUnlinkOnClose(false)
	defer func() {
		_ = replacement.Close()
		_ = os.Remove(socketPath)
	}()

	cancel()
	select {
	case err := <-errC:
		if err != nil {
			t.Fatalf("activity server shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("activity server did not stop")
	}
	if info, err := os.Lstat(socketPath); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("old server removed replacement socket: %v", err)
	}
}

func waitForSocket(t *testing.T, socketPath string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if info, err := os.Lstat(socketPath); err == nil && info.Mode()&os.ModeSocket != 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("activity socket did not appear")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestReporterMapsOnlyMinimalFields(t *testing.T) {
	tests := []struct {
		name       string
		hook       string
		wantAction Action
		wantTurn   bool
	}{
		{name: "session start", hook: "SessionStart", wantAction: ActionSessionStart},
		{name: "start", hook: "UserPromptSubmit", wantAction: ActionStart, wantTurn: true},
		{name: "pre tool refresh", hook: "PreToolUse", wantAction: ActionRefresh, wantTurn: true},
		{name: "post tool refresh", hook: "PostToolUse", wantAction: ActionRefresh, wantTurn: true},
		{name: "stop", hook: "Stop", wantAction: ActionEndTurn, wantTurn: true},
		{name: "subagent stop", hook: "SubagentStop", wantAction: ActionEndTurn, wantTurn: true},
		{name: "session end", hook: "SessionEnd", wantAction: ActionEndSession},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			socketPath := filepath.Join(directory, "capture.sock")
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			captured := make(chan map[string]any, 1)
			go func() {
				conn, acceptErr := listener.AcceptUnix()
				if acceptErr != nil {
					captured <- nil
					return
				}
				defer conn.Close()
				var wire map[string]any
				decodeErr := json.NewDecoder(conn).Decode(&wire)
				if decodeErr != nil {
					captured <- nil
					return
				}
				captured <- wire
				_ = json.NewEncoder(conn).Encode(serverReply{OK: true})
			}()

			input := `{"hook_event_name":"` + test.hook + `","session_id":"session","turn_id":"turn","prompt":"secret","task_name":"` + strings.Repeat("x", 2000) + `","tool_input":{"secret":true},"cwd":"/secret","transcript_path":"/secret/transcript","source":"secret-source"}`
			var output bytes.Buffer
			reporter := Reporter{SocketPath: socketPath, Timeout: time.Second}
			if err := reporter.Run(context.Background(), strings.NewReader(input), &output); err != nil {
				t.Fatal(err)
			}
			wire := <-captured
			if wire == nil {
				t.Fatal("could not decode reporter payload")
			}
			if wire["schemaVersion"] != float64(SchemaVersion) || wire["action"] != string(test.wantAction) || wire["sessionId"] != "session" {
				t.Fatalf("wire event = %#v", wire)
			}
			if _, ok := wire["turnId"]; ok != test.wantTurn {
				t.Fatalf("turnId presence = %v in %#v", ok, wire)
			}
			wantFields := 3
			if test.wantTurn {
				wantFields = 4
			}
			if len(wire) != wantFields {
				t.Fatalf("reporter forwarded extra fields: %#v", wire)
			}
			if output.String() != "{}\n" {
				t.Fatalf("output = %q, want fail-open JSON", output.String())
			}
		})
	}
}

func TestReporterFailsOpenWithoutLeakingPayloadErrors(t *testing.T) {
	inputs := []string{
		`not-json-secret-sentinel`,
		`{"hook_event_name":"Unsupported","prompt":"secret-sentinel"}`,
		`{"hook_event_name":"Stop","session_id":"bad/path","turn_id":"turn","prompt":"secret-sentinel"}`,
	}
	for _, input := range inputs {
		var output bytes.Buffer
		reporter := Reporter{
			SocketPath: filepath.Join(t.TempDir(), "missing.sock"),
			Timeout:    10 * time.Millisecond,
		}
		if err := reporter.Run(context.Background(), strings.NewReader(input), &output); err != nil {
			t.Fatalf("fail-open report returned %v", err)
		}
		if output.String() != "{}\n" {
			t.Fatalf("fail-open output = %q", output.String())
		}
	}

	var output bytes.Buffer
	valid := `{"hook_event_name":"UserPromptSubmit","session_id":"session","turn_id":"turn"}`
	reporter := Reporter{
		SocketPath: filepath.Join(t.TempDir(), "missing.sock"),
		Timeout:    10 * time.Millisecond,
	}
	if err := reporter.Run(context.Background(), strings.NewReader(valid), &output); err != nil {
		t.Fatalf("missing socket was not fail-open: %v", err)
	}
	if output.String() != "{}\n" {
		t.Fatalf("missing socket output = %q", output.String())
	}
}

func TestReporterRejectsMalformedInjectedAndOversizedInput(t *testing.T) {
	tests := []string{
		`{"hook_event_name":"Unknown","session_id":"session","turn_id":"turn"}`,
		`{"hook_event_name":"Stop","session_id":"bad/path","turn_id":"turn"}`,
		`{"hook_event_name":"Stop","session_id":"session","turn_id":"line\nbreak"}`,
		`{"hook_event_name":"Stop","session_id":"session","turn_id":"turn"} {}`,
		`[]`,
	}
	for _, input := range tests {
		if _, err := readHookEvent(strings.NewReader(input), DefaultMaxHookPayload); !errors.Is(err, ErrHookReport) {
			t.Errorf("input %q error = %v", input, err)
		}
	}
	oversized := `{"hook_event_name":"Stop","session_id":"session","turn_id":"turn","prompt":"` + strings.Repeat("x", 256) + `"}`
	if _, err := readHookEvent(strings.NewReader(oversized), 128); !errors.Is(err, ErrHookReport) {
		t.Fatalf("oversized input error = %v", err)
	}
}

func TestReporterStreamsLargeDiscardedHookFields(t *testing.T) {
	secret := strings.Repeat("large-secret-field-", 32<<10)
	tests := []struct {
		name       string
		payload    string
		wantAction Action
	}{
		{
			name: "large prompt before lifecycle fields",
			payload: `{"prompt":"` + secret + `","metadata":{"hook_event_name":"nested-spoof"},` +
				`"hook_event_name":"UserPromptSubmit","session_id":"session","turn_id":"turn"}`,
			wantAction: ActionStart,
		},
		{
			name: "large assistant message after lifecycle fields",
			payload: `{"hook_event_name":"Stop","session_id":"session","turn_id":"turn",` +
				`"last_assistant_message":"` + secret + `","tool_response":[true,null,-12.5e+3]}`,
			wantAction: ActionEndTurn,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event, err := readHookEvent(strings.NewReader(test.payload), DefaultMaxHookPayload)
			if err != nil {
				t.Fatalf("large valid hook was rejected: %v", err)
			}
			if event.Action != test.wantAction || event.SessionID != "session" || event.TurnID != "turn" {
				t.Fatalf("projected event = %#v", event)
			}
		})
	}
}

func TestReporterStreamingProjectionRejectsMalformedDiscardedValues(t *testing.T) {
	inputs := []string{
		`{"hook_event_name":"Stop","session_id":"session","turn_id":"turn","extra":"bad\q"}`,
		`{"hook_event_name":"Stop","session_id":"session","turn_id":"turn","extra":01}`,
		`{"hook_event_name":"Stop","session_id":"session","turn_id":"turn","extra":[true,]}`,
		`{"hook_event_name":"Stop","session_id":"session","turn_id":"turn","extra":{"x":false,}}`,
	}
	for _, input := range inputs {
		if _, err := readHookEvent(strings.NewReader(input), DefaultMaxHookPayload); !errors.Is(err, ErrHookReport) {
			t.Errorf("malformed discarded value %q error = %v", input, err)
		}
	}
}

type delayedReader struct {
	reader strings.Reader
	delay  time.Duration
	once   sync.Once
}

func newDelayedReader(value string, delay time.Duration) *delayedReader {
	return &delayedReader{reader: *strings.NewReader(value), delay: delay}
}

func (reader *delayedReader) Read(destination []byte) (int, error) {
	reader.once.Do(func() { time.Sleep(reader.delay) })
	return reader.reader.Read(destination)
}

func TestReporterUsesOneDeadlineForProjectionAndTransport(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "deadline.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		var event Event
		if json.NewDecoder(conn).Decode(&event) != nil {
			return
		}
		<-release
		_ = json.NewEncoder(conn).Encode(serverReply{OK: true})
	}()

	payload := `{"hook_event_name":"Stop","session_id":"session","turn_id":"turn"}`
	var output bytes.Buffer
	reporter := Reporter{SocketPath: socketPath, Timeout: 500 * time.Millisecond}
	started := time.Now()
	if err := reporter.Run(context.Background(), newDelayedReader(payload, 300*time.Millisecond), &output); err != nil {
		close(release)
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("deadline test server did not stop")
	}
	if elapsed >= 700*time.Millisecond {
		t.Fatalf("report deadline restarted after projection: elapsed %s", elapsed)
	}
	if output.String() != "{}\n" {
		t.Fatalf("deadline hook output = %q", output.String())
	}
}
