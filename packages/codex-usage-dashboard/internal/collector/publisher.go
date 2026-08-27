package collector

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"codex-usage-dashboard/internal/model"
)

var errPublish = errors.New("collector publish failed")

func publishUnix(ctx context.Context, socketPath string, maxPayload int, snapshot model.Snapshot) error {
	snapshot.Normalize()
	if err := snapshot.Validate(); err != nil {
		return errPublish
	}
	payload, err := json.Marshal(snapshot)
	if err != nil || len(payload)+1 > maxPayload {
		return errPublish
	}
	payload = append(payload, '\n')

	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return errPublish
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(defaultPublishTimeout))
	}
	if err := writeAll(conn, payload); err != nil {
		return errPublish
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return errPublish
	}
	if err := unixConn.CloseWrite(); err != nil {
		return errPublish
	}

	reader := bufio.NewReader(io.LimitReader(conn, 4097))
	reply, err := reader.ReadBytes('\n')
	if err != nil || len(reply) > 4096 {
		return errPublish
	}
	var acknowledgement struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(reply, &acknowledgement); err != nil || !acknowledgement.OK {
		return errPublish
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
