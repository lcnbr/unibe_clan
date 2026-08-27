package codex

import (
	"context"
	"io"
	"os/exec"
	"sync"
	"time"
)

const (
	defaultHandshakeTimeout = 10 * time.Second
	defaultClientName       = "codex_usage_dashboard"
	defaultClientTitle      = "Codex Usage Dashboard"
	defaultClientVersion    = "1.0.0"
)

// Config controls the Codex App Server subprocess. CommandArgs is primarily
// useful for testing; production callers should leave it empty.
type Config struct {
	Path             string
	CommandArgs      []string
	MaxLineBytes     int
	HandshakeTimeout time.Duration
	ClientName       string
	ClientTitle      string
	ClientVersion    string
}

// Start launches `codex app-server --stdio`, starts the JSON-lines reader, and
// completes the protocol handshake before returning.
func Start(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Path == "" {
		cfg.Path = "codex"
	}
	if len(cfg.CommandArgs) == 0 {
		cfg.CommandArgs = []string{"app-server", "--stdio"}
	}
	if cfg.MaxLineBytes <= 0 {
		cfg.MaxLineBytes = defaultMaxLineBytes
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = defaultHandshakeTimeout
	}
	if cfg.ClientName == "" {
		cfg.ClientName = defaultClientName
	}
	if cfg.ClientTitle == "" {
		cfg.ClientTitle = defaultClientTitle
	}
	if cfg.ClientVersion == "" {
		cfg.ClientVersion = defaultClientVersion
	}

	processCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(processCtx, cfg.Path, cfg.CommandArgs...)
	// App-server errors can contain upstream details. The collector reports only
	// local error categories, so raw child stderr is intentionally discarded.
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, ErrClosed
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = stdin.Close()
		return nil, ErrClosed
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, ErrClosed
	}

	waitDone := make(chan struct{})
	var waitOnce sync.Once
	wait := func() {
		waitOnce.Do(func() {
			_ = cmd.Wait()
			close(waitDone)
		})
	}
	closeProcess := func() error {
		cancel()
		_ = stdin.Close()
		select {
		case <-waitDone:
		case <-time.After(2 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-waitDone
		}
		return nil
	}

	client := newClient(stdout, stdin, closeProcess, cfg.MaxLineBytes)
	go func() {
		wait()
		client.fail(ErrClosed)
	}()

	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, cfg.HandshakeTimeout)
	defer handshakeCancel()
	if err := client.Initialize(handshakeCtx, cfg.ClientName, cfg.ClientTitle, cfg.ClientVersion); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}
