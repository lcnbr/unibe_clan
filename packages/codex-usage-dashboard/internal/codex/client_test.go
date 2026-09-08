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
		usage, err := read("account/usage/read")
		if err != nil {
			serverDone <- err
			return
		}
		if _, err := serverConn.Write([]byte(`{"id":` + string(usage["id"]) + `,"result":{"summary":{"lifetimeTokens":123456,"peakDailyTokens":999},"dailyUsageBuckets":[{"tokens":7}]}}` + "\n")); err != nil {
			serverDone <- err
			return
		}
		threads, err := read("thread/list")
		if err != nil {
			serverDone <- err
			return
		}
		var threadParams struct {
			SourceKinds []string `json:"sourceKinds"`
		}
		if err := json.Unmarshal(threads["params"], &threadParams); err != nil ||
			strings.Join(threadParams.SourceKinds, ",") != "cli,vscode,exec,appServer,unknown" {
			serverDone <- fmt.Errorf("thread sourceKinds = %#v (error %v)", threadParams.SourceKinds, err)
			return
		}
		threadResponse := `{"id":` + string(threads["id"]) + `,"result":{"data":[{"id":"opaque-thread-1","sessionId":"session-1","source":"exec","name":"Safe name","createdAt":10,"updatedAt":20,"preview":"private prompt","path":"/private/path","cwd":"/repo","gitInfo":{"branch":"secret"},"turns":[{"input":"secret"}]}],"nextCursor":"opaque"}}`
		if _, err := serverConn.Write([]byte(threadResponse + "\n")); err != nil {
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
	if limits.ResetCredits == nil || limits.ResetCredits.AvailableCount == nil || *limits.ResetCredits.AvailableCount != 2 {
		t.Fatalf("unexpected reset credit summary: %#v", limits.ResetCredits)
	}
	usage, err := client.AccountUsage(ctx)
	if err != nil || usage.LifetimeTokens == nil || *usage.LifetimeTokens != 123456 {
		t.Fatalf("AccountUsage = %#v, error %v", usage, err)
	}
	threads, err := client.Threads(ctx, 64)
	if err != nil || len(threads.Threads) != 1 || threads.Threads[0].ID != "opaque-thread-1" ||
		threads.Threads[0].SessionID != "session-1" || threads.Threads[0].Source != SessionSourceExec ||
		threads.Threads[0].Name == nil || *threads.Threads[0].Name != "Safe name" {
		t.Fatalf("Threads = %#v, error %v", threads, err)
	}
	threadPayload, err := json.Marshal(threads.Threads[0])
	if err != nil || strings.Contains(string(threadPayload), "private") ||
		strings.Contains(string(threadPayload), "secret") || strings.Contains(string(threadPayload), "turns") {
		t.Fatalf("thread allowlist retained private data: %s (error %v)", threadPayload, err)
	}
	resetPayload, err := json.Marshal(limits.ResetCredits)
	if err != nil || strings.Contains(string(resetPayload), "opaque-secret") || strings.Contains(string(resetPayload), `"credits"`) {
		t.Fatalf("reset-credit details escaped the allowlist: %s (error %v)", resetPayload, err)
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

func TestResetCreditCountDistinguishesMissingFromZero(t *testing.T) {
	var missing ResetCreditsSummary
	if err := json.Unmarshal([]byte(`{}`), &missing); err != nil {
		t.Fatal(err)
	}
	if missing.AvailableCount != nil {
		t.Fatalf("missing availableCount decoded as %#v", missing.AvailableCount)
	}
	var zero ResetCreditsSummary
	if err := json.Unmarshal([]byte(`{"availableCount":0}`), &zero); err != nil {
		t.Fatal(err)
	}
	if zero.AvailableCount == nil || *zero.AvailableCount != 0 {
		t.Fatalf("authoritative zero decoded as %#v", zero.AvailableCount)
	}
}

func TestLoadedThreadsScansPastSubagentsAndDeduplicatesSessions(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	client := newClient(clientConn, clientConn, clientConn.Close, 64<<10)
	defer client.Close()

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
				return nil, fmt.Errorf("method = %q, want %q", method, wantMethod)
			}
			return message, nil
		}
		respond := func(request map[string]json.RawMessage, result string) error {
			_, err := serverConn.Write([]byte(`{"id":` + string(request["id"]) + `,"result":` + result + "}\n"))
			return err
		}

		first, err := read("thread/loaded/list")
		if err != nil {
			serverDone <- err
			return
		}
		var firstParams struct {
			Cursor *string `json:"cursor"`
			Limit  int     `json:"limit"`
		}
		if err := json.Unmarshal(first["params"], &firstParams); err != nil ||
			firstParams.Cursor != nil || firstParams.Limit != 64 {
			serverDone <- fmt.Errorf("first loaded params = %#v (error %v)", firstParams, err)
			return
		}
		firstIDs := make([]string, 64)
		for index := range firstIDs {
			firstIDs[index] = fmt.Sprintf("subagent-%02d", index)
		}
		encodedFirstIDs, _ := json.Marshal(firstIDs)
		if err := respond(first, `{"data":`+string(encodedFirstIDs)+`,"nextCursor":"after-subagents"}`); err != nil {
			serverDone <- err
			return
		}

		second, err := read("thread/loaded/list")
		if err != nil {
			serverDone <- err
			return
		}
		var secondParams struct {
			Cursor *string `json:"cursor"`
			Limit  int     `json:"limit"`
		}
		if err := json.Unmarshal(second["params"], &secondParams); err != nil ||
			secondParams.Cursor == nil || *secondParams.Cursor != "after-subagents" || secondParams.Limit != 64 {
			serverDone <- fmt.Errorf("second loaded params = %#v (error %v)", secondParams, err)
			return
		}
		lastIDs := []string{"missing-session", "root-active", "root-idle"}
		encodedLastIDs, _ := json.Marshal(lastIDs)
		if err := respond(second, `{"data":`+string(encodedLastIDs)+`,"nextCursor":null}`); err != nil {
			serverDone <- err
			return
		}

		for _, id := range append(firstIDs, lastIDs...) {
			request, err := read("thread/read")
			if err != nil {
				serverDone <- err
				return
			}
			var params struct {
				ThreadID     string `json:"threadId"`
				IncludeTurns bool   `json:"includeTurns"`
			}
			if err := json.Unmarshal(request["params"], &params); err != nil ||
				params.ThreadID != id || params.IncludeTurns {
				serverDone <- fmt.Errorf("thread/read params = %#v for %q (error %v)", params, id, err)
				return
			}
			thread := map[string]any{
				"id": id, "sessionId": "shared-session", "source": "cli",
				"name": "Safe", "parentThreadId": nil,
				"status": map[string]any{"type": "idle"}, "createdAt": 10, "updatedAt": 20,
			}
			if strings.HasPrefix(id, "subagent-") {
				thread["parentThreadId"] = "parent"
				thread["source"] = map[string]any{"subAgent": map[string]any{"thread_spawn": map[string]any{}}}
			}
			if id == "missing-session" {
				delete(thread, "sessionId")
			}
			if id == "root-active" {
				thread["status"] = map[string]any{"type": "active"}
				thread["name"] = "Running"
				thread["createdAt"] = 5
			}
			if id == "root-idle" {
				thread["source"] = "appServer"
				thread["name"] = "Newest"
				thread["updatedAt"] = 30
			}
			encodedThread, _ := json.Marshal(thread)
			if err := respond(request, `{"thread":`+string(encodedThread)+`}`); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	threads, err := client.LoadedThreads(ctx, 64)
	if err != nil {
		t.Fatalf("LoadedThreads: %v", err)
	}
	if len(threads.Threads) != 1 || threads.Threads[0].ID != "root-active" ||
		threads.Threads[0].SessionID != "shared-session" ||
		threads.Threads[0].Status.Type != "active" {
		t.Fatalf("loaded roots = %#v", threads.Threads)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestLoadedThreadsPrioritizesActiveSessionAfterIdleLimit(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	client := newClient(clientConn, clientConn, clientConn.Close, 64<<10)
	defer client.Close()

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
				return nil, fmt.Errorf("method = %q, want %q", method, wantMethod)
			}
			return message, nil
		}
		respond := func(request map[string]json.RawMessage, result string) error {
			_, err := serverConn.Write([]byte(`{"id":` + string(request["id"]) + `,"result":` + result + "}\n"))
			return err
		}

		first, err := read("thread/loaded/list")
		if err != nil {
			serverDone <- err
			return
		}
		idleIDs := make([]string, 64)
		for index := range idleIDs {
			idleIDs[index] = fmt.Sprintf("idle-%02d", index)
		}
		encodedIdleIDs, _ := json.Marshal(idleIDs)
		if err := respond(first, `{"data":`+string(encodedIdleIDs)+`,"nextCursor":"after-idle"}`); err != nil {
			serverDone <- err
			return
		}

		second, err := read("thread/loaded/list")
		if err != nil {
			serverDone <- err
			return
		}
		var secondParams struct {
			Cursor *string `json:"cursor"`
		}
		if err := json.Unmarshal(second["params"], &secondParams); err != nil ||
			secondParams.Cursor == nil || *secondParams.Cursor != "after-idle" {
			serverDone <- fmt.Errorf("second loaded params = %#v (error %v)", secondParams, err)
			return
		}
		if err := respond(second, `{"data":["active-after-limit"],"nextCursor":null}`); err != nil {
			serverDone <- err
			return
		}

		for index, id := range append(idleIDs, "active-after-limit") {
			request, err := read("thread/read")
			if err != nil {
				serverDone <- err
				return
			}
			var params struct {
				ThreadID string `json:"threadId"`
			}
			if err := json.Unmarshal(request["params"], &params); err != nil || params.ThreadID != id {
				serverDone <- fmt.Errorf("thread/read ID = %q, want %q (error %v)", params.ThreadID, id, err)
				return
			}
			status := "idle"
			if id == "active-after-limit" {
				status = "active"
			}
			thread := map[string]any{
				"id": id, "sessionId": "session-" + id, "source": "cli",
				"name": "Safe", "parentThreadId": nil,
				"status":    map[string]any{"type": status},
				"createdAt": index + 1, "updatedAt": index + 1,
			}
			encodedThread, _ := json.Marshal(thread)
			if err := respond(request, `{"thread":`+string(encodedThread)+`}`); err != nil {
				serverDone <- err
				return
			}
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	threads, err := client.LoadedThreads(ctx, 64)
	if err != nil {
		t.Fatalf("LoadedThreads: %v", err)
	}
	if len(threads.Threads) != 64 {
		t.Fatalf("loaded thread count = %d, want 64", len(threads.Threads))
	}
	if threads.Threads[0].ID != "active-after-limit" || threads.Threads[0].Status.Type != "active" {
		t.Fatalf("first loaded thread = %#v, want later active thread", threads.Threads[0])
	}
	for _, thread := range threads.Threads {
		if thread.ID == "idle-00" {
			t.Fatalf("oldest idle thread survived limit: %#v", thread)
		}
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestLoadedThreadsRetriesInventoryWhenThreadDisappears(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	client := newClient(clientConn, clientConn, clientConn.Close, 64<<10)
	defer client.Close()

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
				return nil, fmt.Errorf("method = %q, want %q", method, wantMethod)
			}
			return message, nil
		}
		respond := func(request map[string]json.RawMessage, result string) error {
			_, err := serverConn.Write([]byte(`{"id":` + string(request["id"]) + `,"result":` + result + "}\n"))
			return err
		}

		firstList, err := read("thread/loaded/list")
		if err != nil {
			serverDone <- err
			return
		}
		if err := respond(firstList, `{"data":["disappeared","still-active"],"nextCursor":null}`); err != nil {
			serverDone <- err
			return
		}
		goneRead, err := read("thread/read")
		if err != nil {
			serverDone <- err
			return
		}
		var goneParams struct {
			ThreadID string `json:"threadId"`
		}
		if err := json.Unmarshal(goneRead["params"], &goneParams); err != nil || goneParams.ThreadID != "disappeared" {
			serverDone <- fmt.Errorf("first thread/read = %#v (error %v)", goneParams, err)
			return
		}
		if _, err := serverConn.Write([]byte(`{"id":` + string(goneRead["id"]) + `,"error":{"code":-32001,"message":"thread no longer loaded"}}` + "\n")); err != nil {
			serverDone <- err
			return
		}

		secondList, err := read("thread/loaded/list")
		if err != nil {
			serverDone <- err
			return
		}
		if err := respond(secondList, `{"data":["still-active"],"nextCursor":null}`); err != nil {
			serverDone <- err
			return
		}
		activeRead, err := read("thread/read")
		if err != nil {
			serverDone <- err
			return
		}
		var activeParams struct {
			ThreadID string `json:"threadId"`
		}
		if err := json.Unmarshal(activeRead["params"], &activeParams); err != nil || activeParams.ThreadID != "still-active" {
			serverDone <- fmt.Errorf("retried thread/read = %#v (error %v)", activeParams, err)
			return
		}
		if err := respond(activeRead, `{"thread":{"id":"still-active","sessionId":"active-session","source":"cli","name":"Still active","parentThreadId":null,"status":{"type":"active"},"createdAt":10,"updatedAt":20}}`); err != nil {
			serverDone <- err
			return
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	threads, err := client.LoadedThreads(ctx, 64)
	if err != nil {
		t.Fatalf("LoadedThreads: %v", err)
	}
	if len(threads.Threads) != 1 || threads.Threads[0].ID != "still-active" ||
		threads.Threads[0].Status.Type != "active" {
		t.Fatalf("loaded threads after retry = %#v", threads.Threads)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestSessionSourceAllowlistExcludesObjectAndFutureSources(t *testing.T) {
	tests := []struct {
		payload string
		want    SessionSource
	}{
		{`"cli"`, SessionSourceCLI},
		{`"vscode"`, SessionSourceVSCode},
		{`"exec"`, SessionSourceExec},
		{`"appServer"`, SessionSourceAppServer},
		{`"unknown"`, SessionSourceUnknown},
		{`"future"`, ""},
		{`{"custom":"private-client-name"}`, ""},
		{`{"subAgent":{"thread_spawn":{"threadId":"private"}}}`, ""},
	}
	for _, test := range tests {
		var source SessionSource
		if err := json.Unmarshal([]byte(test.payload), &source); err != nil {
			t.Fatalf("Unmarshal(%s): %v", test.payload, err)
		}
		if source != test.want || source.Allowed() != (test.want != "") {
			t.Fatalf("source %s = %q allowed=%v, want %q", test.payload, source, source.Allowed(), test.want)
		}
		encoded, err := json.Marshal(source)
		if err != nil || strings.Contains(string(encoded), "private") {
			t.Fatalf("source re-encoding leaked input: %s (error %v)", encoded, err)
		}
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
		{
			name:   "usage summary cannot be null",
			result: `{"summary":null}`,
			call: func(ctx context.Context, client *Client) error {
				_, err := client.AccountUsage(ctx)
				return err
			},
		},
		{
			name:   "thread data cannot be null",
			result: `{"data":null}`,
			call: func(ctx context.Context, client *Client) error {
				_, err := client.Threads(ctx, 64)
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Start(ctx, Config{
		Path:             os.Args[0],
		CommandArgs:      []string{"-test.run=TestAppServerHelper", "--", "app-server-helper"},
		HandshakeTimeout: 5 * time.Second,
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
