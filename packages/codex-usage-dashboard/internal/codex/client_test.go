package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClientHandshakeAccountLimitsAndNotification(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close() })
	client := newClient(clientConn, clientConn, clientConn.Close, 64<<10)
	t.Cleanup(func() { _ = client.Close() })

	serverDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(serverConn)
		read := func(wantMethod string) (map[string]json.RawMessage, error) {
			if !scanner.Scan() {
				return nil, fmt.Errorf("missing %s", wantMethod)
			}
			var message map[string]json.RawMessage
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
				return nil, err
			}
			var method string
			if err := json.Unmarshal(message["method"], &method); err != nil || method != wantMethod {
				return nil, fmt.Errorf("got method %q, want %q", method, wantMethod)
			}
			if _, present := message["jsonrpc"]; present {
				return nil, fmt.Errorf("unexpected jsonrpc header")
			}
			return message, nil
		}
		initialize, err := read("initialize")
		if err != nil {
			serverDone <- err
			return
		}
		if _, err := serverConn.Write(append([]byte(`{"id":`), append(initialize["id"], []byte(",\"result\":{\"userAgent\":\"test\"}}\n")...)...)); err != nil {
			serverDone <- err
			return
		}
		if _, err := read("initialized"); err != nil {
			serverDone <- err
			return
		}
		account, err := read("account/read")
		if err != nil {
			serverDone <- err
			return
		}
		if _, err := serverConn.Write([]byte(`{"id":` + string(account["id"]) + `,"result":{"account":{"type":"chatgpt","email":"user@example.com","planType":"pro"},"requiresOpenaiAuth":true}}` + "\n")); err != nil {
			serverDone <- err
			return
		}
		limits, err := read("account/rateLimits/read")
		if err != nil {
			serverDone <- err
			return
		}
		response := `{"id":` + string(limits["id"]) + `,"result":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":25,"windowDurationMins":15,"resetsAt":1730947200}},"rateLimitsByLimitId":{"codex":{"limitId":"codex","limitName":"Codex","planType":"pro","primary":{"usedPercent":25,"windowDurationMins":15,"resetsAt":1730947200},"credits":{"hasCredits":true,"unlimited":false,"balance":"3.5"},"individualLimit":{"limit":"10","used":"2","remainingPercent":80,"resetsAt":1730947200}}},"rateLimitResetCredits":{"availableCount":2,"credits":[{"id":"opaque-secret"}]}}}`
		if _, err := serverConn.Write([]byte(response + "\n")); err != nil {
			serverDone <- err
			return
		}
		if _, err := serverConn.Write([]byte("{\"method\":\"account/rateLimits/updated\",\"params\":{\"rateLimits\":{}}}\n")); err != nil {
			serverDone <- err
			return
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Initialize(ctx, "test_client", "Test Client", "1.0.0"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	account, err := client.Account(ctx)
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if account.Account == nil || account.Account.Email == nil || *account.Account.Email != "user@example.com" {
		t.Fatalf("unexpected account: %#v", account.Account)
	}
	limits, err := client.RateLimits(ctx)
	if err != nil {
		t.Fatalf("RateLimits: %v", err)
	}
	if got := limits.RateLimitsByLimitID["codex"].Primary.UsedPercent; got != 25 {
		t.Fatalf("used percent = %d, want 25", got)
	}
	if limits.ResetCredits == nil || limits.ResetCredits.AvailableCount != 2 {
		t.Fatalf("unexpected reset credit summary: %#v", limits.ResetCredits)
	}
	select {
	case method := <-client.Notifications():
		if method != "account/rateLimits/updated" {
			t.Fatalf("notification = %q", method)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for notification")
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestRPCErrorDoesNotExposeRemoteMessage(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	client := newClient(clientConn, clientConn, clientConn.Close, 4096)
	defer client.Close()
	go func() {
		scanner := bufio.NewScanner(serverConn)
		if !scanner.Scan() {
			return
		}
		var request struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(scanner.Bytes(), &request)
		_, _ = fmt.Fprintf(serverConn, "{\"id\":%d,\"error\":{\"code\":-32000,\"message\":\"sk-secret user@example.com\"}}\n", request.ID)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := client.Account(ctx)
	if err == nil {
		t.Fatal("expected RPC error")
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "example.com") {
		t.Fatalf("remote error leaked: %q", err)
	}
}

func TestClientRejectsOversizedInput(t *testing.T) {
	reader, writer := net.Pipe()
	client := newClient(reader, reader, reader.Close, 64)
	defer client.Close()
	go func() {
		_, _ = writer.Write([]byte(strings.Repeat("x", 256) + "\n"))
		_ = writer.Close()
	}()
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("client did not reject oversized line")
	}
	if !errors.Is(client.Err(), ErrProtocol) {
		t.Fatalf("Err = %v, want ErrProtocol", client.Err())
	}
}

func TestClientRejectsMissingRequiredResultFields(t *testing.T) {
	tests := []struct {
		name   string
		result string
		call   func(context.Context, *Client) error
	}{
		{
			name:   "account requires auth flag",
			result: `{"account":null}`,
			call: func(ctx context.Context, client *Client) error {
				_, err := client.Account(ctx)
				return err
			},
		},
		{
			name:   "rate limits cannot be null",
			result: `{"rateLimits":null}`,
			call: func(ctx context.Context, client *Client) error {
				_, err := client.RateLimits(ctx)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			defer serverConn.Close()
			client := newClient(clientConn, clientConn, clientConn.Close, 4096)
			defer client.Close()
			go func() {
				scanner := bufio.NewScanner(serverConn)
				if !scanner.Scan() {
					return
				}
				var request struct {
					ID int64 `json:"id"`
				}
				_ = json.Unmarshal(scanner.Bytes(), &request)
				_, _ = fmt.Fprintf(serverConn, "{\"id\":%d,\"result\":%s}\n", request.ID, test.result)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := test.call(ctx, client); !errors.Is(err, ErrProtocol) {
				t.Fatalf("error = %v, want ErrProtocol", err)
			}
		})
	}
}

func TestStartSpawnsAndHandshakesWithSubprocess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, err := Start(ctx, Config{
		Path:             os.Args[0],
		CommandArgs:      []string{"-test.run=TestAppServerHelper", "--", "app-server-helper"},
		HandshakeTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	account, err := client.Account(ctx)
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if account.Account == nil || account.Account.Email == nil || *account.Account.Email != "helper@example.com" {
		t.Fatalf("unexpected helper account: %#v", account.Account)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAppServerHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "app-server-helper" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(2)
		}
		switch request.Method {
		case "initialize":
			fmt.Printf("{\"id\":%d,\"result\":{}}\n", *request.ID)
		case "initialized":
		case "account/read":
			fmt.Printf("{\"id\":%d,\"result\":{\"account\":{\"type\":\"chatgpt\",\"email\":\"helper@example.com\",\"planType\":\"plus\"},\"requiresOpenaiAuth\":true}}\n", *request.ID)
		}
	}
	os.Exit(0)
}
