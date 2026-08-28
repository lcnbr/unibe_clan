package activity

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"
)

const (
	// Hook documents can legitimately contain large prompts, tool results, and
	// assistant messages. The projector retains only three short lifecycle
	// fields, but still bounds the amount of discarded input it will scan.
	DefaultMaxHookPayload = int64(8 << 20)
	MaximumHookPayload    = int64(16 << 20)
	DefaultReportTimeout  = time.Second
)

var ErrHookReport = errors.New("activity hook report failed")

type Reporter struct {
	SocketPath     string
	MaxHookPayload int64
	Timeout        time.Duration
}

// Report applies the secure reporter defaults used by the hook-report CLI.
func Report(ctx context.Context, socketPath string, input io.Reader, output io.Writer) error {
	return (Reporter{SocketPath: socketPath}).Run(ctx, input, output)
}

// Run reduces a hook document to its three allowed input fields before making
// a connection. Unknown fields are decoded into no destination and are never
// forwarded, logged, or retained by the activity daemon. Once configuration
// has been validated it deliberately fails open: malformed input and telemetry
// transport failures still produce valid empty hook output and return success,
// so observability can never block a Codex turn.
func (reporter Reporter) Run(ctx context.Context, input io.Reader, output io.Writer) error {
	if reporter.SocketPath == "" || input == nil || output == nil {
		return ErrHookReport
	}
	if reporter.MaxHookPayload <= 0 {
		reporter.MaxHookPayload = DefaultMaxHookPayload
	}
	if reporter.MaxHookPayload > MaximumHookPayload {
		return ErrHookReport
	}
	if reporter.Timeout <= 0 {
		reporter.Timeout = DefaultReportTimeout
	}
	deadline := time.Now().Add(reporter.Timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_, _ = io.WriteString(output, "{}\n")
	event, err := readHookEvent(input, reporter.MaxHookPayload)
	if err != nil {
		return nil
	}
	if err := sendEvent(ctx, reporter.SocketPath, deadline, event); err != nil {
		return nil
	}
	return nil
}

type hookInput struct {
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
	TurnID        string `json:"turn_id"`
}

func readHookEvent(reader io.Reader, maxPayload int64) (Event, error) {
	input, err := projectHookInput(reader, maxPayload)
	if err != nil {
		return Event{}, ErrHookReport
	}

	event := Event{
		SchemaVersion: SchemaVersion,
		SessionID:     input.SessionID,
		TurnID:        input.TurnID,
	}
	switch input.HookEventName {
	case "SessionStart":
		event.Action = ActionSessionStart
		event.TurnID = ""
	case "UserPromptSubmit":
		event.Action = ActionStart
	case "PreToolUse", "PostToolUse":
		event.Action = ActionRefresh
	case "Stop", "SubagentStop":
		event.Action = ActionEndTurn
	case "SessionEnd":
		event.Action = ActionEndSession
		event.TurnID = ""
	default:
		return Event{}, ErrHookReport
	}
	if err := event.Validate(); err != nil {
		return Event{}, ErrHookReport
	}
	return event, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return ErrHookReport
}

func sendEvent(ctx context.Context, socketPath string, deadline time.Time, event Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return ErrHookReport
	}
	payload = append(payload, '\n')

	dialer := net.Dialer{Deadline: deadline}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return ErrHookReport
	}
	defer conn.Close()
	if err := conn.SetDeadline(deadline); err != nil {
		return ErrHookReport
	}
	stopCancellation := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
	})
	defer stopCancellation()
	if err := writeAll(conn, payload); err != nil {
		return ErrHookReport
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return ErrHookReport
	}
	if err := unixConn.CloseWrite(); err != nil {
		return ErrHookReport
	}

	reader := bufio.NewReader(io.LimitReader(conn, 4097))
	replyBytes, err := reader.ReadBytes('\n')
	if err != nil || len(replyBytes) > 4096 {
		return ErrHookReport
	}
	var reply serverReply
	if err := json.Unmarshal(replyBytes, &reply); err != nil || !reply.OK {
		return ErrHookReport
	}
	return nil
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}
