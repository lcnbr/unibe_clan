package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

const defaultMaxLineBytes = 1 << 20

var (
	// ErrClosed means that the app-server transport is no longer available.
	ErrClosed = errors.New("codex app-server closed")
	// ErrProtocol means app-server produced an invalid or oversized message.
	ErrProtocol = errors.New("codex app-server protocol error")
)

// RPCError intentionally excludes the remote error message. Remote messages
// can contain account or credential details and must never reach logs.
type RPCError struct {
	Code int `json:"code"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("codex app-server rpc error code %d", e.Code)
}

type rpcResponse struct {
	result json.RawMessage
	err    error
}

type wireError struct {
	Code int `json:"code"`
}

type wireEnvelope struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *wireError      `json:"error"`
}

// Client is a concurrency-safe JSON-lines client for Codex App Server.
type Client struct {
	reader io.Reader
	writer io.Writer
	close  func() error

	maxLine int

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcResponse
	readErr error

	done          chan struct{}
	notifications chan string
	failOnce      sync.Once
	closeOnce     sync.Once
}

func newClient(reader io.Reader, writer io.Writer, closeFn func() error, maxLine int) *Client {
	if maxLine <= 0 {
		maxLine = defaultMaxLineBytes
	}
	if closeFn == nil {
		closeFn = func() error { return nil }
	}
	c := &Client{
		reader:        reader,
		writer:        writer,
		close:         closeFn,
		maxLine:       maxLine,
		nextID:        1,
		pending:       make(map[int64]chan rpcResponse),
		done:          make(chan struct{}),
		notifications: make(chan string, 1),
	}
	go c.readLoop()
	return c
}

// Initialize completes the required initialize/initialized handshake.
func (c *Client) Initialize(ctx context.Context, name, title, version string) error {
	params := struct {
		ClientInfo struct {
			Name    string `json:"name"`
			Title   string `json:"title"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}{}
	params.ClientInfo.Name = name
	params.ClientInfo.Title = title
	params.ClientInfo.Version = version
	var result struct{}
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return err
	}
	return c.notify("initialized", struct{}{})
}

// Account reads non-secret account identity and plan information.
func (c *Client) Account(ctx context.Context) (AccountResponse, error) {
	var wire struct {
		Account            json.RawMessage `json:"account"`
		RequiresOpenAIAuth *bool           `json:"requiresOpenaiAuth"`
	}
	err := c.call(ctx, "account/read", struct {
		RefreshToken bool `json:"refreshToken"`
	}{RefreshToken: false}, &wire)
	if err != nil {
		return AccountResponse{}, err
	}
	if len(wire.Account) == 0 || wire.RequiresOpenAIAuth == nil {
		return AccountResponse{}, ErrProtocol
	}
	result := AccountResponse{RequiresOpenAIAuth: *wire.RequiresOpenAIAuth}
	if !bytes.Equal(bytes.TrimSpace(wire.Account), []byte("null")) {
		var account Account
		if err := json.Unmarshal(wire.Account, &account); err != nil || account.Type == "" {
			return AccountResponse{}, ErrProtocol
		}
		result.Account = &account
	}
	return result, nil
}

// RateLimits reads all ChatGPT quota buckets and reset windows.
func (c *Client) RateLimits(ctx context.Context) (RateLimitsResponse, error) {
	var wire struct {
		RateLimits          json.RawMessage              `json:"rateLimits"`
		RateLimitsByLimitID map[string]RateLimitSnapshot `json:"rateLimitsByLimitId"`
		ResetCredits        *ResetCreditsSummary         `json:"rateLimitResetCredits"`
	}
	if err := c.call(ctx, "account/rateLimits/read", nil, &wire); err != nil {
		return RateLimitsResponse{}, err
	}
	if len(wire.RateLimits) == 0 || bytes.Equal(bytes.TrimSpace(wire.RateLimits), []byte("null")) {
		return RateLimitsResponse{}, ErrProtocol
	}
	var primary RateLimitSnapshot
	if err := json.Unmarshal(wire.RateLimits, &primary); err != nil {
		return RateLimitsResponse{}, ErrProtocol
	}
	return RateLimitsResponse{
		RateLimits:          primary,
		RateLimitsByLimitID: wire.RateLimitsByLimitID,
		ResetCredits:        wire.ResetCredits,
	}, nil
}

// Notifications yields coalesced account change notifications. Callers must
// always refetch full state rather than treating notifications as snapshots.
func (c *Client) Notifications() <-chan string { return c.notifications }

// Done is closed when the transport becomes unusable or Client.Close is called.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err returns a stable local error without exposing server-provided text.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.readErr == nil {
		return nil
	}
	return c.readErr
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	responseC := make(chan rpcResponse, 1)
	c.pending[id] = responseC
	c.mu.Unlock()

	request := struct {
		Method string `json:"method"`
		ID     int64  `json:"id"`
		Params any    `json:"params,omitempty"`
	}{Method: method, ID: id, Params: params}
	if err := c.writeJSON(request); err != nil {
		c.removePending(id)
		return err
	}

	select {
	case response := <-responseC:
		if response.err != nil {
			return response.err
		}
		if out == nil {
			return nil
		}
		if len(response.result) == 0 {
			return ErrProtocol
		}
		if err := json.Unmarshal(response.result, out); err != nil {
			return ErrProtocol
		}
		return nil
	case <-ctx.Done():
		c.removePending(id)
		return ctx.Err()
	case <-c.done:
		c.removePending(id)
		if err := c.Err(); err != nil {
			return err
		}
		return ErrClosed
	}
}

func (c *Client) notify(method string, params any) error {
	return c.writeJSON(struct {
		Method string `json:"method"`
		Params any    `json:"params,omitempty"`
	}{Method: method, Params: params})
}

func (c *Client) writeJSON(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return ErrProtocol
	}
	if len(payload)+1 > c.maxLine {
		return ErrProtocol
	}
	payload = append(payload, '\n')

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	select {
	case <-c.done:
		return ErrClosed
	default:
	}
	if err := writeFull(c.writer, payload); err != nil {
		c.fail(ErrClosed)
		return ErrClosed
	}
	return nil
}

func writeFull(writer io.Writer, payload []byte) error {
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

func (c *Client) readLoop() {
	scanner := bufio.NewScanner(c.reader)
	scanner.Buffer(make([]byte, 4096), c.maxLine)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var message wireEnvelope
		if err := json.Unmarshal(line, &message); err != nil {
			c.fail(ErrProtocol)
			return
		}
		if message.Method != "" {
			c.handleServerMessage(message)
			continue
		}
		if len(message.ID) == 0 {
			c.fail(ErrProtocol)
			return
		}
		var id int64
		if err := json.Unmarshal(message.ID, &id); err != nil {
			c.fail(ErrProtocol)
			return
		}
		c.mu.Lock()
		responseC := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if responseC == nil {
			continue
		}
		response := rpcResponse{result: message.Result}
		if message.Error != nil {
			response.err = &RPCError{Code: message.Error.Code}
		}
		responseC <- response
	}
	if scanner.Err() != nil {
		c.fail(ErrProtocol)
		return
	}
	c.fail(ErrClosed)
}

func (c *Client) handleServerMessage(message wireEnvelope) {
	if len(message.ID) != 0 {
		// The collector does not use auth modes that require server-initiated
		// requests. Return a generic method-not-found response and never echo
		// request parameters or remote text.
		_ = c.writeServerError(message.ID)
		return
	}
	switch message.Method {
	case "account/updated", "account/rateLimits/updated":
		select {
		case c.notifications <- message.Method:
		default:
		}
	}
}

func (c *Client) writeServerError(id json.RawMessage) error {
	var safeID any
	var numeric int64
	if err := json.Unmarshal(id, &numeric); err == nil {
		safeID = numeric
	} else {
		var text string
		if err := json.Unmarshal(id, &text); err != nil || len(text) > 128 {
			return ErrProtocol
		}
		safeID = text
	}
	return c.writeJSON(struct {
		ID    any `json:"id"`
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{ID: safeID, Error: struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}{Code: -32601, Message: "Method not found"}})
}

func (c *Client) removePending(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) fail(err error) {
	c.failOnce.Do(func() {
		if err == nil {
			err = ErrClosed
		}
		c.mu.Lock()
		c.readErr = err
		c.pending = make(map[int64]chan rpcResponse)
		c.mu.Unlock()
		close(c.done)
	})
}

// Close terminates the transport and its app-server subprocess. It is safe to
// call more than once.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.fail(ErrClosed)
		err = c.close()
	})
	return err
}
