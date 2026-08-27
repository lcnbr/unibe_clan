//go:build linux

package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"codex-usage-dashboard/internal/model"
)

type IngestServer struct {
	Hub            *Hub
	SocketPath     string
	MaxPayload     int64
	ReadTimeout    time.Duration
	MaxConnections int
}

type ingestReply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (s *IngestServer) Serve(ctx context.Context) error {
	if s.Hub == nil {
		return errors.New("ingest hub is required")
	}
	if s.SocketPath == "" {
		return errors.New("ingest socket path is required")
	}
	if s.MaxPayload <= 0 {
		s.MaxPayload = 64 << 10
	}
	if s.ReadTimeout <= 0 {
		s.ReadTimeout = 5 * time.Second
	}
	if s.MaxConnections <= 0 {
		s.MaxConnections = 8
	}
	if err := removeStaleSocket(s.SocketPath); err != nil {
		return err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: s.SocketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("listen on ingest socket: %w", err)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(s.SocketPath)
	}()
	if err := os.Chmod(s.SocketPath, 0o660); err != nil {
		return fmt.Errorf("set ingest socket permissions: %w", err)
	}

	var workers sync.WaitGroup
	workerSlots := make(chan struct{}, s.MaxConnections)
	defer workers.Wait()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept ingest connection: %w", err)
		}
		select {
		case workerSlots <- struct{}{}:
		default:
			writeIngestReply(conn, ingestReply{Error: "busy"})
			_ = conn.Close()
			continue
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-workerSlots }()
			s.handle(conn)
		}()
	}
}

func (s *IngestServer) handle(conn *net.UnixConn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(s.ReadTimeout))
	uid, err := peerUID(conn)
	if err != nil {
		writeIngestReply(conn, ingestReply{Error: "peer_credentials"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(conn, s.MaxPayload+1))
	if err != nil || int64(len(data)) > s.MaxPayload {
		writeIngestReply(conn, ingestReply{Error: "invalid_payload"})
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var snapshot model.Snapshot
	if err := decoder.Decode(&snapshot); err != nil {
		writeIngestReply(conn, ingestReply{Error: "invalid_payload"})
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeIngestReply(conn, ingestReply{Error: "invalid_payload"})
		return
	}
	if err := s.Hub.Apply(uid, snapshot); err != nil {
		writeIngestReply(conn, ingestReply{Error: "rejected"})
		return
	}
	writeIngestReply(conn, ingestReply{OK: true})
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func writeIngestReply(conn *net.UnixConn, reply ingestReply) {
	_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
	_ = json.NewEncoder(conn).Encode(reply)
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect ingest socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket path %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale ingest socket: %w", err)
	}
	return nil
}

func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credential *syscall.Ucred
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		credential, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil {
		return 0, err
	}
	if socketErr != nil {
		return 0, socketErr
	}
	if credential == nil {
		return 0, errors.New("missing peer credentials")
	}
	return credential.Uid, nil
}
