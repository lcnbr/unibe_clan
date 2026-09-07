package collector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const testNixHash = "lp8pgfpak48rdgxn3pqgjq51i05kjj7i"

func TestLiveCodexVersionSelectsNewestTrustedSameUserOutsideCollectorCgroup(t *testing.T) {
	root := t.TempDir()
	procRoot := filepath.Join(root, "proc")
	storeRoot := filepath.Join(root, "nix", "store")
	mustMkdir(t, procRoot, 0o700)
	mustMkdir(t, storeRoot, 0o700)
	writeFakeProcCgroup(t, procRoot, 900, "0::/system.slice/collector.service")
	uid := uint32(os.Geteuid())

	oldTarget := writeTestStoreExecutable(t, storeRoot, "0.151.0", "codex")
	newTarget := writeTestStoreExecutable(t, storeRoot, "0.153.4", ".codex-wrapped")
	wrongUIDTarget := writeTestStoreExecutable(t, storeRoot, "9.9.9", "codex-raw")
	collectorTarget := writeTestStoreExecutable(t, storeRoot, "8.8.8", "codex")
	writeFakeProcProcess(t, procRoot, 101, 1, [4]uint32{uid, uid, uid, uid}, 20, oldTarget, "0::/user.slice/interactive")
	writeFakeProcProcess(t, procRoot, 102, 1, [4]uint32{uid, uid, uid, uid}, 30, newTarget, "0::/user.slice/interactive")
	writeFakeProcProcess(t, procRoot, 103, 1, [4]uint32{uid + 1, uid + 1, uid + 1, uid + 1}, 99, wrongUIDTarget, "0::/user.slice/other")
	writeFakeProcProcess(t, procRoot, 104, 1, [4]uint32{uid, uid, uid, uid}, 100, collectorTarget, "0::/system.slice/collector.service")
	writeFakeProcProcess(t, procRoot, 105, 900, [4]uint32{uid, uid, uid, uid}, 101, collectorTarget, "0::/user.slice/interactive")
	writeFakeProcProcess(t, procRoot, 106, 800, [4]uint32{uid, uid, uid, uid}, 102, collectorTarget, "0::/user.slice/interactive")
	writeFakeProcExecutable(t, procRoot, 800, "/fake/codex-usage-dashboard")
	writeFakeProcProcess(t, procRoot, 107, 1, [4]uint32{uid, uid, uid, uid}, 103, "/home/user/codex", "0::/user.slice/interactive")

	detector := liveCodexVersionDetector{
		procRoot: procRoot, storeRoot: storeRoot,
		allowedTargets: map[string]string{
			oldTarget: "0.151.0", newTarget: "0.153.4",
			wrongUIDTarget: "9.9.9", collectorTarget: "8.8.8",
		},
		collectorPID: 900, collectorUID: uid,
	}
	if got := detector.detect(context.Background()); got != "0.153.4" {
		t.Fatalf("detected version = %q, want newest trusted version", got)
	}
}

func TestLiveCodexVersionExcludesEntireCollectorCgroup(t *testing.T) {
	root := t.TempDir()
	procRoot := filepath.Join(root, "proc")
	storeRoot := filepath.Join(root, "nix", "store")
	mustMkdir(t, procRoot, 0o700)
	mustMkdir(t, storeRoot, 0o700)
	uid := uint32(os.Geteuid())
	target := writeTestStoreExecutable(t, storeRoot, "0.153.4", "codex")
	writeFakeProcCgroup(t, procRoot, 900, "0::/system.slice/collector.service")
	// This process is neither a direct child nor currently parented by the
	// dashboard, but remains in the collector service's cgroup.
	writeFakeProcProcess(t, procRoot, 300, 1, [4]uint32{uid, uid, uid, uid}, 50, target, "0::/system.slice/collector.service/app-server.scope")
	detector := liveCodexVersionDetector{
		procRoot: procRoot, storeRoot: storeRoot, allowedTargets: map[string]string{target: "0.153.4"},
		collectorPID: 900, collectorUID: uid,
	}
	if got := detector.detect(context.Background()); got != "" {
		t.Fatalf("collector-cgroup descendant reported as %q", got)
	}
	writeFakeProcCgroup(t, procRoot, 300, "0::/system.slice/collector.service-other")
	if got := detector.detect(context.Background()); got != "0.153.4" {
		t.Fatalf("cgroup prefix lookalike reported as %q, want trusted version", got)
	}
	writeFakeProcCgroup(t, procRoot, 300, "1:name=systemd:/system.slice/other.service")
	if got := detector.detect(context.Background()); got != "" {
		t.Fatalf("non-v2 candidate cgroup did not fail closed: %q", got)
	}

	if err := os.Remove(filepath.Join(procRoot, "900", "cgroup")); err != nil {
		t.Fatal(err)
	}
	if got := detector.detect(context.Background()); got != "" {
		t.Fatalf("missing collector cgroup did not fail closed: %q", got)
	}
}

func TestLiveCodexVersionRequiresNonEmptyAllowlist(t *testing.T) {
	detector := liveCodexVersionDetector{
		procRoot: "/proc", storeRoot: defaultNixStoreRoot,
		collectorPID: os.Getpid(), collectorUID: uint32(os.Geteuid()),
	}
	if got := detector.detect(context.Background()); got != "" {
		t.Fatalf("empty allowlist returned %q", got)
	}
}

func TestTrustedNixCodexVersionAcceptsExactImmutableTargets(t *testing.T) {
	root := t.TempDir()
	storeRoot := filepath.Join(root, "nix", "store")
	procRoot := filepath.Join(root, "proc")
	mustMkdir(t, storeRoot, 0o700)
	mustMkdir(t, procRoot, 0o700)
	for _, test := range []struct {
		version string
		base    string
	}{
		{version: "0.151.0", base: ".codex-wrapped"},
		{version: "0.144.1", base: "codex-raw"},
		{version: "0.153.4", base: "codex"},
		{version: "0.154.0-alpha.1+build.7", base: "codex"},
	} {
		t.Run(test.base+"-"+test.version, func(t *testing.T) {
			target := writeTestStoreExecutable(t, storeRoot, test.version, test.base)
			procExecutable := filepath.Join(procRoot, strings.ReplaceAll(test.version+test.base, "/", "_"))
			if err := os.Symlink(target, procExecutable); err != nil {
				t.Fatal(err)
			}
			if got := trustedNixCodexVersion(procExecutable, storeRoot, nil); got != "" {
				t.Fatalf("empty allowlist derived version %q", got)
			}
			if got := trustedNixCodexVersion(procExecutable, storeRoot, map[string]string{target: test.version}); got != test.version {
				t.Fatalf("allowlisted version = %q, want %q", got, test.version)
			}
			if got := trustedNixCodexVersion(procExecutable, storeRoot, map[string]string{}); got != "" {
				t.Fatalf("empty strict allowlist accepted %q", got)
			}
		})
	}
}

func TestParseNixCodexTargetRejectsLookalikes(t *testing.T) {
	storeRoot := "/nix/store"
	valid := storeRoot + "/" + testNixHash + "-codex-0.153.4/bin/codex"
	if version, _, ok := parseNixCodexTarget(valid, storeRoot); !ok || version != "0.153.4" {
		t.Fatalf("valid target = (%q, %v)", version, ok)
	}
	invalid := []string{
		"/home/user/codex",
		"nix/store/" + testNixHash + "-codex-0.153.4/bin/codex",
		"/nix/storehouse/" + testNixHash + "-codex-0.153.4/bin/codex",
		"//nix/store/" + testNixHash + "-codex-0.153.4/bin/codex",
		storeRoot + "/" + testNixHash + "-codex-0.153.4/bin/../bin/codex",
		storeRoot + "/" + testNixHash[:31] + "-codex-0.153.4/bin/codex",
		storeRoot + "/" + testNixHash + "a-codex-0.153.4/bin/codex",
		storeRoot + "/" + "ep8pgfpak48rdgxn3pqgjq51i05kjj7i" + "-codex-0.153.4/bin/codex",
		storeRoot + "/" + testNixHash + "-codex-cli-0.153.4/bin/codex",
		storeRoot + "/" + testNixHash + "-my-codex-0.153.4/bin/codex",
		storeRoot + "/" + testNixHash + "-codex-v0.153.4/bin/codex",
		storeRoot + "/" + testNixHash + "-codex-0.153/bin/codex",
		storeRoot + "/" + testNixHash + "-codex-0.153.4/bin/codex-helper",
		storeRoot + "/" + testNixHash + "-codex-0.153.4/bin/codex-code-mode-host",
		storeRoot + "/" + testNixHash + "-codex-0.153.4/bin/codex (deleted)",
		storeRoot + "/" + testNixHash + "-codex-0.153.4/libexec/codex",
		storeRoot + "/" + testNixHash + "-codex-0.153.4/bin/codex/extra",
	}
	for _, target := range invalid {
		if version, _, ok := parseNixCodexTarget(target, storeRoot); ok || version != "" {
			t.Errorf("invalid target %q parsed as (%q, %v)", target, version, ok)
		}
	}
	if _, _, ok := parseNixCodexTarget(valid, "relative/store"); ok {
		t.Fatal("relative store root accepted")
	}
}

func TestTrustedNixCodexVersionRejectsWritableMissingAndWrongAllowlist(t *testing.T) {
	root := t.TempDir()
	storeRoot := filepath.Join(root, "nix", "store")
	procRoot := filepath.Join(root, "proc")
	mustMkdir(t, storeRoot, 0o700)
	mustMkdir(t, procRoot, 0o700)
	target := writeTestStoreExecutable(t, storeRoot, "0.153.4", "codex")
	procExecutable := filepath.Join(procRoot, "exe")
	if err := os.Symlink(target, procExecutable); err != nil {
		t.Fatal(err)
	}
	if got := trustedNixCodexVersion(procExecutable, storeRoot, map[string]string{target: "9.9.9"}); got != "" {
		t.Fatalf("mismatched allowlist version accepted as %q", got)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := trustedNixCodexVersion(procExecutable, storeRoot, map[string]string{target: "0.153.4"}); got != "" {
		t.Fatalf("writable target accepted as %q", got)
	}
	if err := os.Chmod(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if got := trustedNixCodexVersion(procExecutable, storeRoot, map[string]string{target: "0.153.4"}); got != "" {
		t.Fatalf("missing target accepted as %q", got)
	}
}

func TestBoundedNumericProcEntriesFiltersAndSorts(t *testing.T) {
	procRoot := t.TempDir()
	for _, name := range []string{"8", "not-a-pid", "2", "01", "7", "3"} {
		mustMkdir(t, filepath.Join(procRoot, name), 0o700)
	}
	processes, err := boundedNumericProcEntries(procRoot, 64)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(processes), "[2 3 7 8]"; got != want {
		t.Fatalf("numeric processes = %s, want %s", got, want)
	}
	if none, err := boundedNumericProcEntries(procRoot, 0); err != nil || len(none) != 0 {
		t.Fatalf("zero directory bound = %#v, %v", none, err)
	}
}

func TestBoundedNumericProcEntriesFailsClosedOnOverflow(t *testing.T) {
	procRoot := t.TempDir()
	for _, name := range []string{"1", "2", "3"} {
		mustMkdir(t, filepath.Join(procRoot, name), 0o700)
	}

	processes, err := boundedNumericProcEntries(procRoot, 2)
	if err == nil {
		t.Fatalf("overflow returned processes %v without an error", processes)
	}
	if processes != nil {
		t.Fatalf("overflow returned partial process list %v", processes)
	}
}

func TestLiveCodexVersionConsidersProcessBeyondFormerPIDCap(t *testing.T) {
	const formerPIDCap = 4096
	root := t.TempDir()
	procRoot := filepath.Join(root, "proc")
	storeRoot := filepath.Join(root, "nix", "store")
	mustMkdir(t, procRoot, 0o700)
	mustMkdir(t, storeRoot, 0o700)
	writeFakeProcCgroup(t, procRoot, 900, "0::/system.slice/collector.service")
	for offset := 0; offset <= formerPIDCap; offset++ {
		mustMkdir(t, filepath.Join(procRoot, strconv.Itoa(10_000+offset)), 0o700)
	}

	newestPID := 10_000 + formerPIDCap
	uid := uint32(os.Geteuid())
	target := writeTestStoreExecutable(t, storeRoot, "0.153.4", "codex-raw")
	writeFakeProcProcess(t, procRoot, newestPID, 1, [4]uint32{uid, uid, uid, uid}, 999_999, target, "0::/user.slice/interactive")
	processes, err := boundedNumericProcEntries(procRoot, maxProcDirectoryEntries)
	if err != nil {
		t.Fatal(err)
	}
	if len(processes) != formerPIDCap+2 || processes[len(processes)-1] != newestPID {
		t.Fatalf("enumerated %d processes ending at %d, want collector plus %d candidates ending at %d", len(processes), processes[len(processes)-1], formerPIDCap+1, newestPID)
	}
	detector := liveCodexVersionDetector{
		procRoot: procRoot, storeRoot: storeRoot, allowedTargets: map[string]string{target: "0.153.4"},
		collectorPID: 900, collectorUID: uid,
	}
	if got := detector.detect(context.Background()); got != "0.153.4" {
		t.Fatalf("detected version = %q", got)
	}
}

func writeFakeProcProcess(
	t *testing.T,
	procRoot string,
	pid int,
	parentPID int,
	uids [4]uint32,
	startTime uint64,
	executableTarget string,
	cgroup string,
) {
	t.Helper()
	directory := filepath.Join(procRoot, strconv.Itoa(pid))
	mustMkdir(t, directory, 0o700)
	status := fmt.Sprintf(
		"Name:\tcodex\nPPid:\t%d\nUid:\t%d\t%d\t%d\t%d\n",
		parentPID, uids[0], uids[1], uids[2], uids[3],
	)
	if err := os.WriteFile(filepath.Join(directory, "status"), []byte(status), 0o600); err != nil {
		t.Fatal(err)
	}
	fields := make([]string, 20)
	for index := range fields {
		fields[index] = "0"
	}
	fields[0] = "S"
	fields[1] = strconv.Itoa(parentPID)
	fields[19] = strconv.FormatUint(startTime, 10)
	stat := strconv.Itoa(pid) + " (codex test) " + strings.Join(fields, " ") + "\n"
	if err := os.WriteFile(filepath.Join(directory, "stat"), []byte(stat), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFakeProcCgroup(t, procRoot, pid, cgroup)
	writeFakeProcExecutable(t, procRoot, pid, executableTarget)
}

func writeFakeProcExecutable(t *testing.T, procRoot string, pid int, target string) {
	t.Helper()
	directory := filepath.Join(procRoot, strconv.Itoa(pid))
	mustMkdir(t, directory, 0o700)
	path := filepath.Join(directory, "exe")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func writeFakeProcCgroup(t *testing.T, procRoot string, pid int, cgroup string) {
	t.Helper()
	directory := filepath.Join(procRoot, strconv.Itoa(pid))
	mustMkdir(t, directory, 0o700)
	if err := os.WriteFile(filepath.Join(directory, "cgroup"), []byte(cgroup+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTestStoreExecutable(t *testing.T, storeRoot, version, base string) string {
	t.Helper()
	item := filepath.Join(storeRoot, testNixHash+"-codex-"+version)
	bin := filepath.Join(item, "bin")
	mustMkdir(t, bin, 0o755)
	if err := os.Chmod(item, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(bin, base)
	if err := os.WriteFile(target, []byte("not executed\n"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bin, 0o555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(item, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(item, 0o755)
		_ = os.Chmod(bin, 0o755)
	})
	return target
}

func mustMkdir(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(path, mode); err != nil {
		t.Fatal(err)
	}
}
