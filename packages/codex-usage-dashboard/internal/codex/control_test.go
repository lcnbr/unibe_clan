package codex

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1" // #nosec G505 -- test implementation of the RFC 6455 handshake.
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConnectControlListsOnlyAllowlistedRuntimeThreadMetadata(t *testing.T) {
	socketPath := shortControlSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		if err := acceptTestWebSocket(reader, conn); err != nil {
			serverDone <- err
			return
		}
		initialize, err := readTestClientMessage(reader, "initialize")
		if err != nil {
			serverDone <- err
			return
		}
		if err := writeTestServerMessage(conn, `{"id":`+string(initialize["id"])+`,"result":{"userAgent":"test"}}`); err != nil {
			serverDone <- err
			return
		}
		if _, err := readTestClientMessage(reader, "initialized"); err != nil {
			serverDone <- err
			return
		}
		list, err := readTestClientMessage(reader, "thread/list")
		if err != nil {
			serverDone <- err
			return
		}
		response := `{"id":` + string(list["id"]) + `,"result":{"data":[` +
			`{"id":"thread-1","sessionId":"session-1","source":"cli","name":"Safe name","parentThreadId":null,` +
			`"status":{"type":"active","activeFlags":["waitingOnApproval"]},` +
			`"createdAt":10,"updatedAt":20,"preview":"private prompt",` +
			`"cwd":"/private/repo","gitInfo":{"branch":"secret"},"turns":[{"input":"secret"}]}` +
			`],"nextCursor":null}}`
		serverDone <- writeTestServerMessage(conn, response)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, err := ConnectControl(ctx, ControlConfig{SocketPath: socketPath, MaxLineBytes: 64 << 10})
	if err != nil {
		t.Fatalf("ConnectControl: %v", err)
	}
	defer client.Close()
	threads, err := client.Threads(ctx, 64)
	if err != nil || len(threads.Threads) != 1 {
		t.Fatalf("Threads = %#v, error %v", threads, err)
	}
	thread := threads.Threads[0]
	if thread.ID != "thread-1" || thread.SessionID != "session-1" || thread.Source != SessionSourceCLI ||
		thread.Name == nil || *thread.Name != "Safe name" ||
		thread.ParentThreadID != nil || thread.Status.Type != "active" ||
		thread.CreatedAt != 10 || thread.UpdatedAt != 20 {
		t.Fatalf("thread allowlist = %#v", thread)
	}
	payload, err := json.Marshal(thread)
	if err != nil || strings.Contains(string(payload), "private") ||
		strings.Contains(string(payload), "secret") || strings.Contains(string(payload), "activeFlags") ||
		strings.Contains(string(payload), "turns") || strings.Contains(string(payload), "cwd") {
		t.Fatalf("private thread data survived allowlist: %s (error %v)", payload, err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestConnectControlRejectsInvalidWebSocketAccept(t *testing.T) {
	socketPath := shortControlSocketPath(t)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil || line == "\r\n" {
				break
			}
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: invalid\r\n\r\n")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := ConnectControl(ctx, ControlConfig{SocketPath: socketPath}); err != ErrProtocol {
		t.Fatalf("ConnectControl error = %v, want ErrProtocol", err)
	}
}

func TestPerformWebSocketHandshakeRejectsOversizedUnterminatedLineWithoutOverread(t *testing.T) {
	source := &repeatedByteReader{remaining: maxHandshakeBytes * 4, value: 'x'}
	reader := bufio.NewReaderSize(source, 1024)
	if err := performWebSocketHandshake(&discardWriteConn{}, reader); !errors.Is(err, ErrProtocol) {
		t.Fatalf("performWebSocketHandshake error = %v, want ErrProtocol", err)
	}
	if source.read != maxHandshakeBytes {
		t.Fatalf("source bytes read = %d, want exactly %d", source.read, maxHandshakeBytes)
	}
}

func TestControlWebSocketReadsFragmentedTextAroundPingAndPong(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	deadline := time.Now().Add(2 * time.Second)
	_ = clientConn.SetDeadline(deadline)
	_ = serverConn.SetDeadline(deadline)
	transport := &controlWebSocket{
		conn:    clientConn,
		reader:  bufio.NewReader(clientConn),
		maxLine: 1024,
	}

	serverDone := make(chan error, 1)
	go func() {
		if err := writeTestServerFrame(serverConn, 0x01, []byte(`{"ok":`)); err != nil {
			serverDone <- err
			return
		}
		if err := writeTestServerFrame(serverConn, 0x89, []byte("probe")); err != nil {
			serverDone <- err
			return
		}
		first, payload, err := readTestClientFrame(bufio.NewReader(serverConn))
		if err != nil {
			serverDone <- err
			return
		}
		if first != 0x8a || string(payload) != "probe" {
			serverDone <- fmt.Errorf("pong = first %#x payload %q", first, payload)
			return
		}
		if err := writeTestServerFrame(serverConn, 0x8a, []byte("server-pong")); err != nil {
			serverDone <- err
			return
		}
		serverDone <- writeTestServerFrame(serverConn, 0x80, []byte("true}"))
	}()

	buffer := make([]byte, 64)
	n, err := transport.Read(buffer)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got, want := string(buffer[:n]), "{\"ok\":true}\n"; got != want {
		t.Fatalf("Read = %q, want %q", got, want)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestControlWebSocketAcceptsAndRepliesToValidClose(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "empty"},
		{name: "normal_with_reason", payload: testClosePayload(1000, "done")},
		{name: "application_code_and_utf8_reason", payload: testClosePayload(3000, "all done ☃")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			defer clientConn.Close()
			defer serverConn.Close()
			deadline := time.Now().Add(2 * time.Second)
			_ = clientConn.SetDeadline(deadline)
			_ = serverConn.SetDeadline(deadline)
			transport := &controlWebSocket{
				conn:    clientConn,
				reader:  bufio.NewReader(clientConn),
				maxLine: 1024,
			}

			serverDone := make(chan error, 1)
			go func() {
				if err := writeTestServerFrame(serverConn, 0x88, test.payload); err != nil {
					serverDone <- err
					return
				}
				first, payload, err := readTestClientFrame(bufio.NewReader(serverConn))
				if err != nil {
					serverDone <- err
					return
				}
				if first != 0x88 || !bytes.Equal(payload, test.payload) {
					serverDone <- fmt.Errorf("close reply = first %#x payload %x", first, payload)
					return
				}
				serverDone <- nil
			}()

			if _, err := transport.readTextMessage(); !errors.Is(err, io.EOF) {
				t.Fatalf("readTextMessage error = %v, want EOF", err)
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestControlWebSocketRejectsMalformedControlAndCloseFrames(t *testing.T) {
	tests := []struct {
		name  string
		frame []byte
	}{
		{name: "fragmented_close", frame: testServerFrame(0x08, testClosePayload(1000, ""))},
		{name: "fragmented_ping", frame: testServerFrame(0x09, []byte("ping"))},
		{name: "oversized_close", frame: testServerFrame(0x88, bytes.Repeat([]byte{'x'}, 126))},
		{name: "oversized_pong", frame: testServerFrame(0x8a, bytes.Repeat([]byte{'x'}, 126))},
		{name: "one_byte_close", frame: testServerFrame(0x88, []byte{0x03})},
		{name: "reserved_close_code", frame: testServerFrame(0x88, testClosePayload(1005, ""))},
		{name: "out_of_range_close_code", frame: testServerFrame(0x88, testClosePayload(5000, ""))},
		{name: "invalid_utf8_reason", frame: testServerFrame(0x88, append(testClosePayload(1000, ""), 0xff))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &controlWebSocket{
				reader:  bufio.NewReader(bytes.NewReader(test.frame)),
				maxLine: 1024,
			}
			if _, err := transport.readTextMessage(); !errors.Is(err, ErrProtocol) {
				t.Fatalf("readTextMessage error = %v, want ErrProtocol", err)
			}
		})
	}
}

func TestControlWebSocketWriteContextCancelsBlockedSocketWrite(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	started := make(chan struct{})
	transport := &controlWebSocket{
		conn:    &writeSignalingConn{Conn: clientConn, started: started},
		maxLine: 1024,
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan struct {
		n   int
		err error
	}, 1)
	go func() {
		n, err := transport.WriteContext(ctx, []byte("{}\n"))
		result <- struct {
			n   int
			err error
		}{n: n, err: err}
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("socket write did not start")
	}
	cancel()
	select {
	case got := <-result:
		if got.n != 0 || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("WriteContext = (%d, %v), want (0, context.Canceled)", got.n, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked socket write did not stop after cancellation")
	}
}

type repeatedByteReader struct {
	remaining int
	read      int
	value     byte
}

func (reader *repeatedByteReader) Read(destination []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	n := len(destination)
	if n > reader.remaining {
		n = reader.remaining
	}
	for index := 0; index < n; index++ {
		destination[index] = reader.value
	}
	reader.remaining -= n
	reader.read += n
	return n, nil
}

type writeSignalingConn struct {
	net.Conn
	started chan struct{}
	once    sync.Once
}

func (conn *writeSignalingConn) Write(payload []byte) (int, error) {
	conn.once.Do(func() { close(conn.started) })
	return conn.Conn.Write(payload)
}

type discardWriteConn struct {
	net.Conn
}

func (conn *discardWriteConn) Write(payload []byte) (int, error) {
	return len(payload), nil
}

func shortControlSocketPath(t *testing.T) string {
	t.Helper()
	// Linux limits Unix-domain socket paths to roughly 108 bytes. Nix shells
	// can place testing.T.TempDir below a path that is already longer than that.
	directory, err := os.MkdirTemp("/tmp", "codex-control-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "control.sock")
}

func acceptTestWebSocket(reader *bufio.Reader, writer io.Writer) error {
	key := ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if line == "\r\n" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Sec-WebSocket-Key") {
			key = strings.TrimSpace(value)
		}
	}
	if key == "" {
		return fmt.Errorf("missing websocket key")
	}
	digest := sha1.Sum([]byte(key + webSocketGUID)) // #nosec G401 -- mandated by RFC 6455.
	accept := base64.StdEncoding.EncodeToString(digest[:])
	_, err := io.WriteString(writer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: "+accept+"\r\n\r\n")
	return err
}

func readTestClientMessage(reader *bufio.Reader, wantMethod string) (map[string]json.RawMessage, error) {
	first, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	second, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	if first != 0x81 || second&0x80 == 0 {
		return nil, fmt.Errorf("invalid client frame")
	}
	length := uint64(second & 0x7f)
	switch length {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(reader, extended[:]); err != nil {
			return nil, err
		}
		length = uint64(binary.BigEndian.Uint16(extended[:]))
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(reader, extended[:]); err != nil {
			return nil, err
		}
		length = binary.BigEndian.Uint64(extended[:])
	}
	var mask [4]byte
	if _, err := io.ReadFull(reader, mask[:]); err != nil {
		return nil, err
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	for index := range payload {
		payload[index] ^= mask[index%len(mask)]
	}
	var message map[string]json.RawMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, err
	}
	var method string
	if err := json.Unmarshal(message["method"], &method); err != nil || method != wantMethod {
		return nil, fmt.Errorf("method = %q, want %q", method, wantMethod)
	}
	return message, nil
}

func writeTestServerMessage(writer io.Writer, message string) error {
	return writeTestServerFrame(writer, 0x81, []byte(message))
}

func writeTestServerFrame(writer io.Writer, first byte, payload []byte) error {
	return writeFull(writer, testServerFrame(first, payload))
}

func testServerFrame(first byte, payload []byte) []byte {
	header := []byte{first}
	switch {
	case len(payload) < 126:
		header = append(header, byte(len(payload)))
	case len(payload) <= 0xffff:
		header = append(header, 126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(len(payload)))
	default:
		header = append(header, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(len(payload)))
	}
	return append(header, payload...)
}

func testClosePayload(code uint16, reason string) []byte {
	payload := make([]byte, 2, 2+len(reason))
	binary.BigEndian.PutUint16(payload, code)
	return append(payload, reason...)
}

func readTestClientFrame(reader *bufio.Reader) (byte, []byte, error) {
	first, err := reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	second, err := reader.ReadByte()
	if err != nil {
		return 0, nil, err
	}
	if second&0x80 == 0 {
		return 0, nil, fmt.Errorf("client frame is not masked")
	}
	length := uint64(second & 0x7f)
	switch length {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(reader, extended[:]); err != nil {
			return 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(extended[:]))
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(reader, extended[:]); err != nil {
			return 0, nil, err
		}
		length = binary.BigEndian.Uint64(extended[:])
	}
	var mask [4]byte
	if _, err := io.ReadFull(reader, mask[:]); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, err
	}
	for index := range payload {
		payload[index] ^= mask[index%len(mask)]
	}
	return first, payload, nil
}
