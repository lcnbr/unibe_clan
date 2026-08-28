//go:build linux

package activity

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
)

type Server struct {
	Tracker        *Tracker
	OnChange       func()
	SocketPath     string
	SocketMode     os.FileMode
	SocketGroupID  *int
	MaxPayload     int64
	ReadTimeout    time.Duration
	MaxConnections int
}

type serverReply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (server *Server) Serve(ctx context.Context) error {
	if server.Tracker == nil {
		return errors.New("activity tracker is required")
	}
	if server.SocketPath == "" {
		return errors.New("activity socket path is required")
	}
	if server.SocketGroupID != nil && *server.SocketGroupID < 0 {
		return errors.New("activity socket group ID must be non-negative")
	}
	if server.SocketMode == 0 {
		server.SocketMode = 0o660
	}
	if server.MaxPayload <= 0 {
		server.MaxPayload = 4 << 10
	}
	if server.ReadTimeout <= 0 {
		server.ReadTimeout = 5 * time.Second
	}
	if server.MaxConnections <= 0 {
		server.MaxConnections = 8
	}
	if err := removeStaleSocket(server.SocketPath); err != nil {
		return err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: server.SocketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("listen on activity socket: %w", err)
	}
	listener.SetUnlinkOnClose(false)
	ownedSocket, err := os.Lstat(server.SocketPath)
	if err != nil || ownedSocket.Mode()&os.ModeSocket == 0 {
		_ = listener.Close()
		return errors.New("inspect newly created activity socket")
	}
	defer func() {
		_ = removeOwnedSocket(server.SocketPath, ownedSocket)
		_ = listener.Close()
	}()
	if server.SocketGroupID != nil {
		if err := os.Chown(server.SocketPath, -1, *server.SocketGroupID); err != nil {
			return fmt.Errorf("set activity socket group: %w", err)
		}
	}
	if err := os.Chmod(server.SocketPath, server.SocketMode.Perm()); err != nil {
		return fmt.Errorf("set activity socket permissions: %w", err)
	}
	serveCtx, cancel := context.WithCancel(ctx)
	var expiryWorker sync.WaitGroup
	expiryWorker.Add(1)
	go func() {
		defer expiryWorker.Done()
		server.Tracker.runExpiry(serveCtx, server.OnChange)
	}()
	defer func() {
		cancel()
		expiryWorker.Wait()
	}()

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-serveCtx.Done():
			_ = listener.Close()
		case <-done:
		}
	}()

	var workers sync.WaitGroup
	workerSlots := make(chan struct{}, server.MaxConnections)
	defer workers.Wait()
	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept activity connection: %w", err)
		}
		select {
		case workerSlots <- struct{}{}:
		default:
			writeServerReply(conn, serverReply{Error: "busy"})
			_ = conn.Close()
			continue
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-workerSlots }()
			server.handle(conn)
		}()
	}
}

func (server *Server) handle(conn *net.UnixConn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(server.ReadTimeout))
	uid, err := peerUID(conn)
	if err != nil {
		writeServerReply(conn, serverReply{Error: "peer_credentials"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(conn, server.MaxPayload+1))
	if err != nil || int64(len(data)) > server.MaxPayload {
		writeServerReply(conn, serverReply{Error: "invalid_payload"})
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var event Event
	if err := decoder.Decode(&event); err != nil {
		writeServerReply(conn, serverReply{Error: "invalid_payload"})
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeServerReply(conn, serverReply{Error: "invalid_payload"})
		return
	}
	if err := server.Tracker.Apply(uid, event); err != nil {
		writeServerReply(conn, serverReply{Error: "rejected"})
		return
	}
	writeServerReply(conn, serverReply{OK: true})
	if server.OnChange != nil {
		server.OnChange()
	}
}

func writeServerReply(conn *net.UnixConn, reply serverReply) {
	_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
	_ = json.NewEncoder(conn).Encode(reply)
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect activity socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket activity path %s", path)
	}
	probe, probeErr := net.DialTimeout("unix", path, 250*time.Millisecond)
	if probeErr == nil {
		_ = probe.Close()
		return fmt.Errorf("activity socket is already accepting connections")
	}
	if errors.Is(probeErr, os.ErrNotExist) {
		return nil
	}
	if !errors.Is(probeErr, syscall.ECONNREFUSED) {
		return fmt.Errorf("cannot verify that activity socket is stale: %w", probeErr)
	}
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reinspect activity socket: %w", err)
	}
	if current.Mode()&os.ModeSocket == 0 || !os.SameFile(info, current) {
		return errors.New("activity socket changed while checking staleness")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale activity socket: %w", err)
	}
	return nil
}

func removeOwnedSocket(path string, owned os.FileInfo) error {
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSocket == 0 || !os.SameFile(owned, current) {
		return nil
	}
	return os.Remove(path)
}

func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credential *syscall.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credential, socketErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
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
