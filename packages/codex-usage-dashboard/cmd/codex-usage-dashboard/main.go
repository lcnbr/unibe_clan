package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	osuser "os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"codex-usage-dashboard/internal/activity"
	"codex-usage-dashboard/internal/collector"
	usagehistory "codex-usage-dashboard/internal/history"
	"codex-usage-dashboard/internal/hub"
	"codex-usage-dashboard/internal/web"
)

var version = "dev"

func main() {
	ctx, cancel := signalContext()
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "codex-usage-dashboard: %v\n", err)
		os.Exit(1)
	}
}

func signalContext() (context.Context, context.CancelFunc) {
	return signalNotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

var signalNotifyContext = func(parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, signals...)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("a mode is required")
	}
	switch args[0] {
	case "serve":
		return runServe(ctx, args[1:], stdout, stderr)
	case "collector":
		return runCollector(ctx, args[1:], stderr)
	case "hook-report":
		return runHookReport(ctx, args[1:], os.Stdin, stdout, stderr)
	case "version", "--version", "-version":
		_, err := fmt.Fprintln(stdout, version)
		return err
	case "help", "--help", "-h":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown mode %q", args[0])
	}
}

func printUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, `Usage:
  codex-usage-dashboard serve [options]
  codex-usage-dashboard collector [options]
  codex-usage-dashboard hook-report --socket PATH
  codex-usage-dashboard version

The serve mode accepts snapshots only over a peer-authenticated Unix socket
and rejects non-loopback HTTP listeners.`)
}

func runHookReport(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("hook-report", flag.ContinueOnError)
	flags.SetOutput(stderr)
	socketPath := flags.String("socket", "", "dashboard activity Unix socket")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("hook-report accepts no positional arguments")
	}
	if *socketPath == "" || !filepath.IsAbs(*socketPath) {
		return errors.New("hook-report socket must be an absolute path")
	}
	return activity.Report(ctx, *socketPath, stdin, stdout)
}

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("user cannot be empty")
	}
	*values = append(*values, value)
	return nil
}

type hostList []string

func (values *hostList) String() string { return strings.Join(*values, ",") }
func (values *hostList) Set(value string) error {
	if value == "" {
		return errors.New("host cannot be empty")
	}
	*values = append(*values, value)
	return nil
}

type anchorList map[string]string

func (values *anchorList) String() string {
	if values == nil || len(*values) == 0 {
		return ""
	}
	entries := make([]string, 0, len(*values))
	for username, email := range *values {
		entries = append(entries, username+"="+email)
	}
	sort.Strings(entries)
	return strings.Join(entries, ",")
}

func (values *anchorList) Set(value string) error {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 {
		return errors.New("anchor must use username=email")
	}
	username := strings.TrimSpace(parts[0])
	email := strings.TrimSpace(parts[1])
	if username == "" || email == "" || !strings.Contains(email, "@") {
		return errors.New("anchor must contain a username and account email")
	}
	if *values == nil {
		*values = make(anchorList)
	}
	if _, exists := (*values)[username]; exists {
		return fmt.Errorf("anchor user %q is repeated", username)
	}
	(*values)[username] = email
	return nil
}

func runServe(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listenAddress := flags.String("listen", "127.0.0.1:8787", "literal loopback HTTP address")
	socketPath := flags.String("socket", "/run/codex-usage-dashboard/ingest.sock", "collector ingest Unix socket")
	activitySocketPath := flags.String("activity-socket", "/run/codex-usage-dashboard/activity.sock", "managed hook activity Unix socket")
	activitySocketGroup := flags.String("activity-socket-group", "", "group owning the managed hook activity socket")
	activityLease := flags.Duration("activity-lease", 30*time.Minute, "active-turn expiry without a hook event")
	staleAfter := flags.Duration("stale-after", 90*time.Second, "age after which last-good data is stale")
	historyFile := flags.String("history-file", "", "absolute file for retained reset history (empty keeps history in memory)")
	historyRetention := flags.Duration("history-retention", 366*24*time.Hour, "completed reset history retention")
	maxPayload := flags.Int64("max-payload", 64<<10, "maximum snapshot size in bytes")
	demo := flags.Bool("demo", false, "seed preview-only account data")
	additionalAllowedHosts := hostList{}
	flags.Var(&additionalAllowedHosts, "allowed-host", "exact additional HTTP Host name or IP (repeatable; omit ports)")
	users := stringList{}
	flags.Var(&users, "user", "collector Linux username (repeatable)")
	anchors := anchorList{}
	flags.Var(&anchors, "anchor", "expected anchor mapping as username=email (repeatable)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("serve accepts no positional arguments")
	}
	if len(users) == 0 {
		users = stringList{
			"codex",
			"codex-1",
			"codex-2",
			"codex-3",
			"lcnbr",
			"nfink",
			"vhirschi",
			"zeno",
		}
	}
	listenHost, err := validateListenAddress(*listenAddress)
	if err != nil {
		return err
	}
	if *socketPath == "" || !filepath.IsAbs(*socketPath) {
		return errors.New("ingest socket must be an absolute path")
	}
	if *activitySocketPath == "" || !filepath.IsAbs(*activitySocketPath) {
		return errors.New("activity socket must be an absolute path")
	}
	if filepath.Clean(*activitySocketPath) == filepath.Clean(*socketPath) {
		return errors.New("activity and ingest sockets must use different paths")
	}
	if *activityLease <= 0 || *activityLease > 24*time.Hour {
		return errors.New("activity-lease must be greater than zero and no more than 24 hours")
	}
	if *historyFile != "" && !filepath.IsAbs(*historyFile) {
		return errors.New("history file must be an absolute path")
	}
	if *maxPayload < 1024 || *maxPayload > 1<<20 {
		return errors.New("max-payload must be between 1024 and 1048576 bytes")
	}
	activitySocketGroupID, err := resolveGroupID(*activitySocketGroup)
	if err != nil {
		return err
	}

	identities, err := resolveIdentities(users, anchors)
	if err != nil {
		return err
	}
	state, err := hub.New(identities, *staleAfter)
	if err != nil {
		return err
	}
	activityIdentities := make([]activity.Identity, 0, len(identities))
	for _, identity := range identities {
		activityIdentities = append(activityIdentities, activity.Identity{
			Username: identity.Username,
			UID:      identity.UID,
		})
	}
	activityTracker, err := activity.New(activityIdentities, *activityLease)
	if err != nil {
		return err
	}
	state.SetActivitySource(func() []hub.ActivityRef {
		turns := activityTracker.Snapshot()
		refs := make([]hub.ActivityRef, 0, len(turns))
		for _, turn := range turns {
			refs = append(refs, hub.ActivityRef{
				Username:  turn.Username,
				SessionID: turn.SessionID,
				StartedAt: turn.StartedAt,
				UpdatedAt: turn.UpdatedAt,
				Running:   turn.Running,
			})
		}
		return refs
	})
	retainedHistory, err := usagehistory.Open(*historyFile, *historyRetention)
	if err != nil {
		return err
	}
	state.SetHistory(retainedHistory)
	if *demo {
		state.SeedDemo()
	}

	allowedHosts := append([]string{listenHost}, additionalAllowedHosts...)
	handler, err := (hub.HTTPHandler{
		Hub:          state,
		History:      retainedHistory,
		Assets:       web.Files(),
		AllowedHosts: allowedHosts,
	}).Handler()
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		return errors.New("cannot open the loopback HTTP listener")
	}
	defer listener.Close()

	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		IdleTimeout:       75 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	ingest := &hub.IngestServer{
		Hub:            state,
		SocketPath:     *socketPath,
		MaxPayload:     *maxPayload,
		ReadTimeout:    5 * time.Second,
		MaxConnections: max(8, len(identities)*2),
	}
	activityIngest := &activity.Server{
		Tracker:        activityTracker,
		OnChange:       state.Refresh,
		SocketPath:     *activitySocketPath,
		SocketGroupID:  activitySocketGroupID,
		MaxConnections: max(8, len(identities)*2),
	}

	errC := make(chan error, 3)
	go func() { errC <- ingest.Serve(ctx) }()
	go func() { errC <- activityIngest.Serve(ctx) }()
	go func() {
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errC <- err
	}()
	_, _ = fmt.Fprintf(stdout, "listening on http://%s\n", listener.Addr())

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		return nil
	case err := <-errC:
		if err == nil {
			return nil
		}
		return errors.New("dashboard service stopped")
	}
}

func resolveGroupID(name string) (*int, error) {
	if name == "" {
		return nil, nil
	}
	group, err := osuser.LookupGroup(name)
	if err != nil {
		return nil, fmt.Errorf("activity socket group %q does not exist", name)
	}
	groupID, err := strconv.ParseUint(group.Gid, 10, 32)
	if err != nil || groupID > uint64(^uint(0)>>1) {
		return nil, fmt.Errorf("activity socket group %q has an invalid GID", name)
	}
	result := int(groupID)
	return &result, nil
}

func resolveIdentities(usernames []string, anchors anchorList) ([]hub.Identity, error) {
	identities := make([]hub.Identity, 0, len(usernames))
	seen := make(map[string]bool, len(usernames))
	for _, username := range usernames {
		entry, err := osuser.Lookup(username)
		if err != nil {
			return nil, fmt.Errorf("Linux user %q does not exist", username)
		}
		uid, err := strconv.ParseUint(entry.Uid, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("Linux user %q has an invalid UID", username)
		}
		seen[username] = true
		identities = append(identities, hub.Identity{
			Username:      username,
			UID:           uint32(uid),
			ExpectedEmail: anchors[username],
		})
	}
	for username := range anchors {
		if !seen[username] {
			return nil, fmt.Errorf("anchor user %q is not a configured collector", username)
		}
	}
	return identities, nil
}

func validateListenAddress(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return "", errors.New("listen must be a host:port address")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", errors.New("listen must use a literal loopback IP address")
	}
	numericPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil || numericPort == 0 {
		return "", errors.New("listen port must be between 1 and 65535")
	}
	return ip.String(), nil
}

func runCollector(ctx context.Context, args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("collector", flag.ContinueOnError)
	flags.SetOutput(stderr)
	username := flags.String("username", "", "fixed Linux username represented by this collector")
	codexPath := flags.String("codex-bin", "codex", "pinned Codex CLI executable")
	socketPath := flags.String("socket", "/run/codex-usage-dashboard/ingest.sock", "dashboard ingest Unix socket")
	authPath := flags.String("auth-file", "", "auth file whose metadata triggers process recycling")
	pollInterval := flags.Duration("poll-interval", 30*time.Second, "full refresh interval")
	recycleInterval := flags.Duration("recycle-interval", 5*time.Minute, "app-server recycle interval")
	statInterval := flags.Duration("stat-interval", 5*time.Second, "auth metadata check interval")
	requestTimeout := flags.Duration("request-timeout", 10*time.Second, "app-server request timeout")
	publishTimeout := flags.Duration("publish-timeout", 5*time.Second, "Unix socket publish timeout")
	maxPayload := flags.Int("max-payload", 64<<10, "maximum snapshot size in bytes")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("collector accepts no positional arguments")
	}
	if strings.TrimSpace(*username) == "" {
		return errors.New("--username is required")
	}
	logger := log.New(stderr, "", log.LstdFlags|log.LUTC)
	instance, err := collector.New(collector.Config{
		Username:        *username,
		CodexPath:       *codexPath,
		SocketPath:      *socketPath,
		AuthPath:        *authPath,
		PollInterval:    *pollInterval,
		RecycleInterval: *recycleInterval,
		StatInterval:    *statInterval,
		RequestTimeout:  *requestTimeout,
		PublishTimeout:  *publishTimeout,
		MaxPayload:      *maxPayload,
		Logger:          logger,
	})
	if err != nil {
		return err
	}
	return instance.Run(ctx)
}
