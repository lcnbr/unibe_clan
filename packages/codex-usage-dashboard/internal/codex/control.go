package codex

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- required by the RFC 6455 handshake, not used for security.
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	webSocketGUID       = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	maxHandshakeBytes   = 16 << 10
	controlRequestPath  = "/"
	controlWriteTimeout = 10 * time.Second
)

// ControlConfig selects an already-running per-user Codex App Server. The
// control socket is private to that user and exposes the supported App Server
// protocol over WebSocket.
type ControlConfig struct {
	SocketPath       string
	MaxLineBytes     int
	HandshakeTimeout time.Duration
	ClientName       string
	ClientTitle      string
	ClientVersion    string
}

// ConnectControl connects to an existing per-user App Server without loading,
// resuming, or subscribing to any thread. Callers can use LoadedThreads to
// read the server's runtime status for interactive threads that predate hooks.
func ConnectControl(ctx context.Context, cfg ControlConfig) (*Client, error) {
	if cfg.SocketPath == "" {
		return nil, ErrClosed
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

	handshakeCtx, cancel := context.WithTimeout(ctx, cfg.HandshakeTimeout)
	defer cancel()
	transport, err := dialControlWebSocket(handshakeCtx, cfg.SocketPath, cfg.MaxLineBytes)
	if err != nil {
		return nil, err
	}
	client := newClient(transport, transport, transport.Close, cfg.MaxLineBytes)
	if err := client.Initialize(handshakeCtx, cfg.ClientName, cfg.ClientTitle, cfg.ClientVersion); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

type controlWebSocket struct {
	conn    net.Conn
	reader  *bufio.Reader
	maxLine int

	writeGateOnce sync.Once
	writeGate     chan struct{}
	readBuf       []byte
	closed        sync.Once
}

func dialControlWebSocket(ctx context.Context, socketPath string, maxLine int) (*controlWebSocket, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, ErrClosed
	}
	reader := bufio.NewReaderSize(conn, 4096)
	deadline, ok := ctx.Deadline()
	if ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := performWebSocketHandshake(conn, reader); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return &controlWebSocket{conn: conn, reader: reader, maxLine: maxLine}, nil
}

func performWebSocketHandshake(conn net.Conn, reader *bufio.Reader) error {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return ErrClosed
	}
	key := base64.StdEncoding.EncodeToString(nonce)
	request := "GET " + controlRequestPath + " HTTP/1.1\r\n" +
		"Host: localhost\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if err := writeFull(conn, []byte(request)); err != nil {
		return ErrClosed
	}

	total := 0
	status, err := readHeaderLine(reader, &total)
	if err != nil || strings.TrimSpace(status) != "HTTP/1.1 101 Switching Protocols" {
		return ErrProtocol
	}
	headers := make(map[string]string)
	for {
		line, err := readHeaderLine(reader, &total)
		if err != nil {
			return err
		}
		if line == "\r\n" {
			break
		}
		name, value, present := strings.Cut(line, ":")
		if !present {
			return ErrProtocol
		}
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		if name == "" || headers[name] != "" {
			return ErrProtocol
		}
		headers[name] = value
	}
	if !headerHasToken(headers["upgrade"], "websocket") ||
		!headerHasToken(headers["connection"], "upgrade") {
		return ErrProtocol
	}
	digest := sha1.Sum([]byte(key + webSocketGUID)) // #nosec G401 -- mandated by RFC 6455.
	expectedAccept := base64.StdEncoding.EncodeToString(digest[:])
	if headers["sec-websocket-accept"] != expectedAccept {
		return ErrProtocol
	}
	return nil
}

func readHeaderLine(reader *bufio.Reader, total *int) (string, error) {
	remaining := maxHandshakeBytes - *total
	if remaining <= 0 {
		return "", ErrProtocol
	}

	fragment, err := reader.ReadSlice('\n')
	if len(fragment) > remaining {
		*total = maxHandshakeBytes
		return "", ErrProtocol
	}
	*total += len(fragment)
	if err == nil {
		if !bytes.HasSuffix(fragment, []byte("\r\n")) {
			return "", ErrProtocol
		}
		return string(fragment), nil
	}
	if !errors.Is(err, bufio.ErrBufferFull) || *total >= maxHandshakeBytes {
		return "", ErrProtocol
	}

	// Allocate at most the remaining handshake budget for a line that spans
	// the reader's fixed-size buffer. ReadSlice keeps each streamed fragment
	// bounded, and the single pre-grown builder cannot grow past the global
	// handshake limit.
	var line strings.Builder
	line.Grow(remaining)
	_, _ = line.Write(fragment)
	for {
		fragment, err = reader.ReadSlice('\n')
		remaining = maxHandshakeBytes - *total
		if len(fragment) > remaining {
			*total = maxHandshakeBytes
			return "", ErrProtocol
		}
		*total += len(fragment)
		_, _ = line.Write(fragment)
		if err == nil {
			value := line.String()
			if !strings.HasSuffix(value, "\r\n") {
				return "", ErrProtocol
			}
			return value, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) || *total >= maxHandshakeBytes {
			return "", ErrProtocol
		}
	}
}

func headerHasToken(value, expected string) bool {
	for _, token := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(token), expected) {
			return true
		}
	}
	return false
}

func (transport *controlWebSocket) Read(destination []byte) (int, error) {
	for len(transport.readBuf) == 0 {
		payload, err := transport.readTextMessage()
		if err != nil {
			return 0, err
		}
		transport.readBuf = append(payload, '\n')
	}
	count := copy(destination, transport.readBuf)
	transport.readBuf = transport.readBuf[count:]
	return count, nil
}

func (transport *controlWebSocket) Write(payload []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), controlWriteTimeout)
	defer cancel()
	return transport.WriteContext(ctx, payload)
}

// WriteContext writes one JSON-lines payload as a WebSocket text frame. The
// context bounds both waiting for another writer and a blocked socket write.
func (transport *controlWebSocket) WriteContext(ctx context.Context, payload []byte) (int, error) {
	if len(payload) < 2 || payload[len(payload)-1] != '\n' ||
		bytes.Contains(payload[:len(payload)-1], []byte{'\n'}) ||
		len(payload) > transport.maxLine {
		return 0, ErrProtocol
	}
	if err := transport.writeFrameContext(ctx, 0x1, payload[:len(payload)-1]); err != nil {
		return 0, err
	}
	return len(payload), nil
}

func (transport *controlWebSocket) Close() error {
	var err error
	transport.closed.Do(func() { err = transport.conn.Close() })
	return err
}

func (transport *controlWebSocket) readTextMessage() ([]byte, error) {
	var fragments bytes.Buffer
	started := false
	for {
		first, second, payload, err := transport.readFrame()
		if err != nil {
			return nil, err
		}
		final := first&0x80 != 0
		opcode := first & 0x0f
		if first&0x70 != 0 || second&0x80 != 0 {
			return nil, ErrProtocol
		}
		switch opcode {
		case 0x8:
			if err := validateClosePayload(payload); err != nil {
				return nil, err
			}
			if err := transport.writeFrame(0x8, payload); err != nil {
				return nil, err
			}
			return nil, io.EOF
		case 0x9:
			if err := transport.writeFrame(0xA, payload); err != nil {
				return nil, err
			}
			continue
		case 0xA:
			continue
		case 0x1:
			if started {
				return nil, ErrProtocol
			}
			started = true
		case 0x0:
			if !started {
				return nil, ErrProtocol
			}
		default:
			return nil, ErrProtocol
		}
		remaining := transport.maxLine - fragments.Len() - 1
		if remaining < 0 || len(payload) > remaining {
			return nil, ErrProtocol
		}
		_, _ = fragments.Write(payload)
		if final {
			message := fragments.Bytes()
			if !utf8.Valid(message) {
				return nil, ErrProtocol
			}
			return append([]byte(nil), message...), nil
		}
	}
}

func (transport *controlWebSocket) readFrame() (byte, byte, []byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(transport.reader, header[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return 0, 0, nil, io.EOF
		}
		return 0, 0, nil, ErrProtocol
	}
	if header[0]&0x70 != 0 || header[1]&0x80 != 0 {
		return 0, 0, nil, ErrProtocol
	}
	opcode := header[0] & 0x0f
	control := opcode&0x08 != 0
	lengthCode := header[1] & 0x7f
	if control && (header[0]&0x80 == 0 || lengthCode > 125) {
		return 0, 0, nil, ErrProtocol
	}

	length := uint64(lengthCode)
	switch lengthCode {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(transport.reader, extended[:]); err != nil {
			return 0, 0, nil, ErrProtocol
		}
		length = uint64(binary.BigEndian.Uint16(extended[:]))
		if length < 126 {
			return 0, 0, nil, ErrProtocol
		}
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(transport.reader, extended[:]); err != nil {
			return 0, 0, nil, ErrProtocol
		}
		length = binary.BigEndian.Uint64(extended[:])
		if length>>63 != 0 || length <= 0xffff {
			return 0, 0, nil, ErrProtocol
		}
	}
	if !control && (transport.maxLine <= 0 || length >= uint64(transport.maxLine)) {
		return 0, 0, nil, ErrProtocol
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(transport.reader, payload); err != nil {
		return 0, 0, nil, ErrProtocol
	}
	return header[0], header[1], payload, nil
}

func (transport *controlWebSocket) writeFrame(opcode byte, payload []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), controlWriteTimeout)
	defer cancel()
	return transport.writeFrameContext(ctx, opcode, payload)
}

func (transport *controlWebSocket) writeFrameContext(ctx context.Context, opcode byte, payload []byte) error {
	if opcode&0x08 != 0 && len(payload) > 125 {
		return ErrProtocol
	}
	ctx, cancel := context.WithTimeout(ctx, controlWriteTimeout)
	defer cancel()

	transport.writeGateOnce.Do(func() {
		transport.writeGate = make(chan struct{}, 1)
		transport.writeGate <- struct{}{}
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-transport.writeGate:
	}
	defer func() { transport.writeGate <- struct{}{} }()
	if err := ctx.Err(); err != nil {
		return err
	}

	deadline, _ := ctx.Deadline()
	if err := transport.conn.SetWriteDeadline(deadline); err != nil {
		return ErrClosed
	}
	deadlineApplied := make(chan struct{})
	stopDeadline := context.AfterFunc(ctx, func() {
		_ = transport.conn.SetWriteDeadline(time.Now())
		close(deadlineApplied)
	})
	defer func() {
		if !stopDeadline() {
			<-deadlineApplied
		}
		_ = transport.conn.SetWriteDeadline(time.Time{})
	}()

	mask := make([]byte, 4)
	if _, err := rand.Read(mask); err != nil {
		return ErrClosed
	}
	header := make([]byte, 0, 14)
	header = append(header, 0x80|(opcode&0x0f))
	switch {
	case len(payload) < 126:
		header = append(header, 0x80|byte(len(payload)))
	case len(payload) <= 0xffff:
		header = append(header, 0x80|126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(len(payload)))
	default:
		header = append(header, 0x80|127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(len(payload)))
	}
	header = append(header, mask...)
	masked := make([]byte, len(payload))
	for index := range payload {
		masked[index] = payload[index] ^ mask[index%len(mask)]
	}
	if err := writeFull(transport.conn, header); err != nil {
		_ = transport.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return ErrClosed
	}
	if err := writeFull(transport.conn, masked); err != nil {
		_ = transport.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return ErrClosed
	}
	return nil
}

func validateClosePayload(payload []byte) error {
	if len(payload) == 1 {
		return ErrProtocol
	}
	if len(payload) == 0 {
		return nil
	}
	code := binary.BigEndian.Uint16(payload[:2])
	if !validCloseCode(code) || !utf8.Valid(payload[2:]) {
		return ErrProtocol
	}
	return nil
}

func validCloseCode(code uint16) bool {
	switch code {
	case 1000, 1001, 1002, 1003, 1007, 1008, 1009, 1010, 1011, 1012, 1013, 1014:
		return true
	default:
		return code >= 3000 && code <= 4999
	}
}
