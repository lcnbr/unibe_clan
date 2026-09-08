package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"unicode"
)

const (
	defaultMaxLineBytes          = 1 << 20
	maxLoadedThreadMetadataReads = 1024
	maxLoadedThreadPageSize      = 64
)

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

	writeGate chan struct{}
	mu        sync.Mutex
	nextID    int64
	pending   map[int64]chan rpcResponse
	readErr   error

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
		writeGate:     make(chan struct{}, 1),
	}
	c.writeGate <- struct{}{}
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
	return c.notify(ctx, "initialized", struct{}{})
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

// AccountUsage reads the optional all-Codex lifetime token total. A null
// value is a successful response and remains distinguishable from an RPC or
// protocol failure.
func (c *Client) AccountUsage(ctx context.Context) (AccountUsageResponse, error) {
	var wire struct {
		Summary json.RawMessage `json:"summary"`
	}
	if err := c.call(ctx, "account/usage/read", nil, &wire); err != nil {
		return AccountUsageResponse{}, err
	}
	if len(wire.Summary) == 0 || bytes.Equal(bytes.TrimSpace(wire.Summary), []byte("null")) {
		return AccountUsageResponse{}, ErrProtocol
	}
	var summary struct {
		LifetimeTokens *int64 `json:"lifetimeTokens"`
	}
	if err := json.Unmarshal(wire.Summary, &summary); err != nil {
		return AccountUsageResponse{}, ErrProtocol
	}
	return AccountUsageResponse{LifetimeTokens: summary.LifetimeTokens}, nil
}

var interactiveThreadSourceKinds = []string{"cli", "vscode", "exec", "appServer", "unknown"}

// Threads returns only the metadata required to resolve hook session IDs to
// safe task names. App Server may return substantially more data; the narrow
// wire type makes that data unreachable after decoding. sourceKinds is always
// explicit because an omitted filter excludes exec and appServer sessions on
// supported Codex versions.
func (c *Client) Threads(ctx context.Context, limit int) (ThreadListResponse, error) {
	if limit <= 0 || limit > 64 {
		limit = 64
	}
	params := struct {
		Limit          int      `json:"limit"`
		SortKey        string   `json:"sortKey"`
		SortDirection  string   `json:"sortDirection"`
		Archived       bool     `json:"archived"`
		UseStateDBOnly bool     `json:"useStateDbOnly"`
		SourceKinds    []string `json:"sourceKinds"`
	}{
		Limit:          limit,
		SortKey:        "updated_at",
		SortDirection:  "desc",
		Archived:       false,
		UseStateDBOnly: true,
		SourceKinds:    append([]string(nil), interactiveThreadSourceKinds...),
	}
	var wire struct {
		Data json.RawMessage `json:"data"`
	}
	if err := c.call(ctx, "thread/list", params, &wire); err != nil {
		return ThreadListResponse{}, err
	}
	if len(wire.Data) == 0 || bytes.Equal(bytes.TrimSpace(wire.Data), []byte("null")) {
		return ThreadListResponse{}, ErrProtocol
	}
	var threads []Thread
	if err := json.Unmarshal(wire.Data, &threads); err != nil {
		return ThreadListResponse{}, ErrProtocol
	}
	return ThreadListResponse{Threads: threads}, nil
}

// LoadedThreads reads metadata for threads already loaded by this App Server.
// It never resumes, subscribes to, or otherwise loads a thread. The number of
// metadata reads is strictly bounded independently of the peer's loaded set;
// limit is applied after rejecting subagents and unusable metadata, collapsing
// duplicate session IDs, and ranking active sessions ahead of idle ones so
// those rows cannot hide an interactive active session.
func (c *Client) LoadedThreads(ctx context.Context, limit int) (ThreadListResponse, error) {
	if limit <= 0 || limit > 64 {
		limit = 64
	}
	for attempt := 0; attempt < 2; attempt++ {
		loaded, err := c.loadedThreadIDs(ctx, maxLoadedThreadMetadataReads)
		if err != nil {
			return ThreadListResponse{}, err
		}
		threads, err := c.readLoadedThreadMetadata(ctx, loaded.ThreadIDs)
		if err == nil {
			return ThreadListResponse{Threads: selectLoadedThreads(threads, limit)}, nil
		}
		var rpcErr *RPCError
		if attempt != 0 || !errors.As(err, &rpcErr) || ctx.Err() != nil {
			return ThreadListResponse{}, err
		}
		// A loaded thread may be removed between thread/loaded/list and
		// thread/read. Re-read the complete inventory once instead of
		// publishing a partial result or guessing which remote RPC codes mean
		// "not loaded" across Codex versions.
	}
	return ThreadListResponse{}, ErrProtocol
}

func (c *Client) readLoadedThreadMetadata(ctx context.Context, threadIDs []string) ([]Thread, error) {
	threads := make([]Thread, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		thread, err := c.readThreadMetadata(ctx, threadID)
		if err != nil {
			return nil, err
		}
		if thread.ParentThreadID != nil || !thread.Source.Allowed() ||
			!validOpaqueID(thread.SessionID) ||
			(thread.Status.Type != "active" && thread.Status.Type != "idle") {
			continue
		}
		threads = append(threads, thread)
	}
	return threads, nil
}

func selectLoadedThreads(threads []Thread, limit int) []Thread {
	bySession := make(map[string]Thread, len(threads))
	for _, candidate := range threads {
		current, exists := bySession[candidate.SessionID]
		if !exists || loadedThreadPreferred(candidate, current) {
			bySession[candidate.SessionID] = candidate
		}
	}

	selected := make([]Thread, 0, len(bySession))
	for _, thread := range bySession {
		selected = append(selected, thread)
	}
	sort.Slice(selected, func(left, right int) bool {
		return loadedThreadPreferred(selected[left], selected[right])
	})
	if len(selected) > limit {
		selected = selected[:limit]
	}
	return selected
}

func loadedThreadPreferred(candidate, current Thread) bool {
	candidateActive := candidate.Status.Type == "active"
	currentActive := current.Status.Type == "active"
	if candidateActive != currentActive {
		return candidateActive
	}
	if candidate.UpdatedAt != current.UpdatedAt {
		return candidate.UpdatedAt > current.UpdatedAt
	}
	if candidate.CreatedAt != current.CreatedAt {
		return candidate.CreatedAt > current.CreatedAt
	}
	if candidate.SessionID != current.SessionID {
		return candidate.SessionID < current.SessionID
	}
	return candidate.ID < current.ID
}

func (c *Client) loadedThreadIDs(ctx context.Context, limit int) (LoadedThreadListResponse, error) {
	if limit <= 0 || limit > maxLoadedThreadMetadataReads {
		limit = maxLoadedThreadMetadataReads
	}
	result := make([]string, 0, limit)
	seenIDs := make(map[string]bool, limit)
	seenCursors := make(map[string]bool)
	var cursor *string
	for len(result) < limit {
		pageLimit := min(limit-len(result), maxLoadedThreadPageSize)
		params := struct {
			Cursor *string `json:"cursor,omitempty"`
			Limit  int     `json:"limit"`
		}{Cursor: cursor, Limit: pageLimit}
		var wire struct {
			Data       json.RawMessage `json:"data"`
			NextCursor *string         `json:"nextCursor"`
		}
		if err := c.call(ctx, "thread/loaded/list", params, &wire); err != nil {
			return LoadedThreadListResponse{}, err
		}
		if len(wire.Data) == 0 || bytes.Equal(bytes.TrimSpace(wire.Data), []byte("null")) {
			return LoadedThreadListResponse{}, ErrProtocol
		}
		var ids []string
		if err := json.Unmarshal(wire.Data, &ids); err != nil || ids == nil {
			return LoadedThreadListResponse{}, ErrProtocol
		}
		for _, id := range ids {
			if !validOpaqueID(id) {
				return LoadedThreadListResponse{}, ErrProtocol
			}
			if !seenIDs[id] {
				seenIDs[id] = true
				result = append(result, id)
				if len(result) == limit {
					break
				}
			}
		}
		if len(result) == limit || wire.NextCursor == nil {
			break
		}
		next := *wire.NextCursor
		if !validOpaqueCursor(next) || seenCursors[next] {
			return LoadedThreadListResponse{}, ErrProtocol
		}
		seenCursors[next] = true
		cursor = &next
		if len(ids) == 0 {
			return LoadedThreadListResponse{}, ErrProtocol
		}
	}
	return LoadedThreadListResponse{ThreadIDs: result}, nil
}

func (c *Client) readThreadMetadata(ctx context.Context, threadID string) (Thread, error) {
	if !validOpaqueID(threadID) {
		return Thread{}, ErrProtocol
	}
	params := struct {
		ThreadID     string `json:"threadId"`
		IncludeTurns bool   `json:"includeTurns"`
	}{ThreadID: threadID, IncludeTurns: false}
	var wire struct {
		Thread json.RawMessage `json:"thread"`
	}
	if err := c.call(ctx, "thread/read", params, &wire); err != nil {
		return Thread{}, err
	}
	if len(wire.Thread) == 0 || bytes.Equal(bytes.TrimSpace(wire.Thread), []byte("null")) {
		return Thread{}, ErrProtocol
	}
	var thread Thread
	if err := json.Unmarshal(wire.Thread, &thread); err != nil {
		return Thread{}, ErrProtocol
	}
	if thread.ID != threadID || !validOpaqueID(thread.ID) {
		return Thread{}, ErrProtocol
	}
	return thread, nil
}

func validOpaqueID(value string) bool {
	return validOpaqueText(value, 128)
}

func validOpaqueCursor(value string) bool {
	return validOpaqueText(value, 4096)
}

func validOpaqueText(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) || unicode.Is(unicode.Cf, char) ||
			unicode.Is(unicode.Cs, char) || unicode.Is(unicode.Co, char) {
			return false
		}
	}
	return true
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
	if err := c.writeJSON(ctx, request); err != nil {
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

func (c *Client) notify(ctx context.Context, method string, params any) error {
	return c.writeJSON(ctx, struct {
		Method string `json:"method"`
		Params any    `json:"params,omitempty"`
	}{Method: method, Params: params})
}

func (c *Client) writeJSON(ctx context.Context, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return ErrProtocol
	}
	if len(payload)+1 > c.maxLine {
		return ErrProtocol
	}
	payload = append(payload, '\n')

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	case <-c.writeGate:
	}
	defer func() { c.writeGate <- struct{}{} }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	default:
	}
	if err := writeFullContext(ctx, c.writer, payload); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if errors.Is(err, ErrProtocol) {
			return ErrProtocol
		}
		c.fail(ErrClosed)
		return ErrClosed
	}
	return nil
}

type contextWriter interface {
	WriteContext(context.Context, []byte) (int, error)
}

func writeFullContext(ctx context.Context, writer io.Writer, payload []byte) error {
	if writerWithContext, ok := writer.(contextWriter); ok {
		written, err := writerWithContext.WriteContext(ctx, payload)
		if err != nil {
			return err
		}
		if written != len(payload) {
			return io.ErrShortWrite
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeFull(writer, payload)
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
	ctx, cancel := context.WithTimeout(context.Background(), defaultHandshakeTimeout)
	defer cancel()
	return c.writeJSON(ctx, struct {
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
