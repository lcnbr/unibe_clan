//go:build linux

package collector

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"codex-usage-dashboard/internal/model"
)

const (
	maxProcDirectoryEntries = 8192
	maxProcStatusLine       = 16 << 10
	maxProcStatBytes        = 4096
	maxProcCgroupBytes      = 4096
	defaultNixStoreRoot     = "/nix/store"
	nixStoreHashLength      = 32
	nixStoreHashAlphabet    = "0123456789abcdfghijklmnpqrsvwxyz"
)

type liveCodexProcess struct {
	pid       int
	startTime uint64
	version   string
}

type liveCodexVersionDetector struct {
	procRoot  string
	storeRoot string
	// allowedTargets is a strict full-target allowlist supplied by root-owned
	// configuration. An empty map always makes detection return unknown.
	allowedTargets map[string]string
	collectorPID   int
	collectorUID   uint32
}

func defaultLiveCodexVersionDetector(allowedTargets map[string]string) func(context.Context) string {
	detector := liveCodexVersionDetector{
		procRoot:       "/proc",
		storeRoot:      defaultNixStoreRoot,
		allowedTargets: allowedTargets,
		collectorPID:   os.Getpid(),
		collectorUID:   uint32(os.Geteuid()),
	}
	return detector.detect
}

// detect locates only same-user Codex executables whose /proc target resolves
// to the same immutable file in the trusted Nix store, then returns the newest
// observed version. It never executes a discovered binary. Process identifiers
// and executable paths never leave this function, and all discovery failures
// are deliberately silent so observability cannot disturb quota collection.
func (detector liveCodexVersionDetector) detect(ctx context.Context) string {
	if detector.procRoot == "" || detector.storeRoot == "" || detector.collectorPID <= 0 ||
		len(detector.allowedTargets) == 0 {
		return ""
	}
	collectorCgroup, ok := readProcCgroup(
		filepath.Join(detector.procRoot, strconv.Itoa(detector.collectorPID), "cgroup"),
	)
	if !ok {
		return ""
	}
	processes, err := boundedNumericProcEntries(detector.procRoot, maxProcDirectoryEntries)
	if err != nil {
		return ""
	}

	var candidates []liveCodexProcess
	for _, pid := range processes {
		if ctx.Err() != nil {
			return ""
		}
		if pid == detector.collectorPID {
			continue
		}
		executable := filepath.Join(detector.procRoot, strconv.Itoa(pid), "exe")
		version := trustedNixCodexVersion(executable, detector.storeRoot, detector.allowedTargets)
		if version == "" {
			continue
		}
		candidateCgroup, ok := readProcCgroup(filepath.Join(detector.procRoot, strconv.Itoa(pid), "cgroup"))
		if !ok || cgroupIsSameOrDescendant(candidateCgroup, collectorCgroup) {
			continue
		}
		status, ok := readProcStatus(filepath.Join(detector.procRoot, strconv.Itoa(pid), "status"))
		if !ok || !status.matchesUID(detector.collectorUID) || status.parentPID == detector.collectorPID {
			continue
		}
		if executableBasename(filepath.Join(detector.procRoot, strconv.Itoa(status.parentPID), "exe")) == "codex-usage-dashboard" {
			continue
		}
		started, ok := readProcStartTime(filepath.Join(detector.procRoot, strconv.Itoa(pid), "stat"))
		if !ok {
			continue
		}
		// Re-read after the UID/start-time checks so an exec or PID-reuse race
		// cannot attach an earlier trusted version to a different process.
		if confirmed := trustedNixCodexVersion(executable, detector.storeRoot, detector.allowedTargets); confirmed != version {
			continue
		}
		if confirmedCgroup, confirmedOK := readProcCgroup(filepath.Join(detector.procRoot, strconv.Itoa(pid), "cgroup")); !confirmedOK || confirmedCgroup != candidateCgroup ||
			cgroupIsSameOrDescendant(confirmedCgroup, collectorCgroup) {
			continue
		}
		confirmedStatus, confirmedStatusOK := readProcStatus(
			filepath.Join(detector.procRoot, strconv.Itoa(pid), "status"),
		)
		if !confirmedStatusOK || confirmedStatus != status {
			continue
		}
		confirmedStart, confirmedStartOK := readProcStartTime(
			filepath.Join(detector.procRoot, strconv.Itoa(pid), "stat"),
		)
		if !confirmedStartOK || confirmedStart != started {
			continue
		}
		candidates = append(candidates, liveCodexProcess{pid: pid, startTime: started, version: version})
	}
	if len(candidates) == 0 {
		return ""
	}

	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].startTime == candidates[right].startTime {
			return candidates[left].pid > candidates[right].pid
		}
		return candidates[left].startTime > candidates[right].startTime
	})
	return candidates[0].version
}

func isCodexExecutableBasename(base string) bool {
	return base == "codex" || base == "codex-raw" || base == ".codex-wrapped"
}

// trustedNixCodexVersion is intentionally read-only: it derives a version
// from an exact Nix store target and verifies that /proc and the collector's
// mount namespace identify the same immutable file. It never opens the file
// for content and never executes it.
func trustedNixCodexVersion(procExecutable, storeRoot string, allowedTargets map[string]string) string {
	if len(allowedTargets) == 0 {
		return ""
	}
	target, err := os.Readlink(procExecutable)
	if err != nil {
		return ""
	}
	version, storeItem, ok := parseNixCodexTarget(target, storeRoot)
	if !ok {
		return ""
	}
	allowedVersion, allowed := allowedTargets[target]
	if !allowed || model.SanitizeCodexVersion(allowedVersion) != version {
		return ""
	}

	storeInfo, err := os.Stat(storeRoot)
	if err != nil || !storeInfo.IsDir() {
		return ""
	}
	itemInfo, err := os.Stat(storeItem)
	if err != nil || !itemInfo.IsDir() || itemInfo.Mode().Perm()&0o222 != 0 {
		return ""
	}
	binInfo, err := os.Stat(filepath.Dir(target))
	if err != nil || !binInfo.IsDir() || binInfo.Mode().Perm()&0o222 != 0 {
		return ""
	}
	targetInfo, err := os.Stat(target)
	if err != nil || !targetInfo.Mode().IsRegular() || targetInfo.Mode().Perm()&0o222 != 0 {
		return ""
	}
	procInfo, err := os.Stat(procExecutable)
	if err != nil || !os.SameFile(procInfo, targetInfo) {
		return ""
	}
	trustedUID, ok := fileOwnerUID(storeInfo)
	if !ok || !ownedByUID(itemInfo, trustedUID) || !ownedByUID(binInfo, trustedUID) ||
		!ownedByUID(targetInfo, trustedUID) {
		return ""
	}

	confirmedTarget, err := os.Readlink(procExecutable)
	if err != nil || confirmedTarget != target {
		return ""
	}
	return version
}

func parseNixCodexTarget(target, storeRoot string) (version, storeItem string, ok bool) {
	if !filepath.IsAbs(storeRoot) || filepath.Clean(storeRoot) != storeRoot ||
		target != filepath.Clean(target) {
		return "", "", false
	}
	rootParts := strings.Split(storeRoot, string(filepath.Separator))
	targetParts := strings.Split(target, string(filepath.Separator))
	if len(targetParts) != len(rootParts)+3 {
		return "", "", false
	}
	for index := range rootParts {
		if targetParts[index] != rootParts[index] {
			return "", "", false
		}
	}
	component := targetParts[len(rootParts)]
	if len(component) <= nixStoreHashLength+len("-codex-") ||
		component[nixStoreHashLength] != '-' ||
		!validNixStoreHash(component[:nixStoreHashLength]) ||
		!strings.HasPrefix(component[nixStoreHashLength+1:], "codex-") ||
		targetParts[len(rootParts)+1] != "bin" ||
		!isCodexExecutableBasename(targetParts[len(rootParts)+2]) {
		return "", "", false
	}
	version = model.SanitizeCodexVersion(
		strings.TrimPrefix(component[nixStoreHashLength+1:], "codex-"),
	)
	if version == "" {
		return "", "", false
	}
	return version, filepath.Join(storeRoot, component), true
}

func validNixStoreHash(hash string) bool {
	if len(hash) != nixStoreHashLength {
		return false
	}
	for _, char := range hash {
		if !strings.ContainsRune(nixStoreHashAlphabet, char) {
			return false
		}
	}
	return true
}

func fileOwnerUID(info os.FileInfo) (uint32, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Uid, true
}

func ownedByUID(info os.FileInfo, uid uint32) bool {
	owner, ok := fileOwnerUID(info)
	return ok && owner == uid
}

func boundedNumericProcEntries(procRoot string, directoryLimit int) ([]int, error) {
	if directoryLimit <= 0 {
		return []int{}, nil
	}
	directory, err := os.Open(procRoot)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	names, err := directory.Readdirnames(directoryLimit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(names) > directoryLimit {
		return nil, errors.New("process directory exceeds entry limit")
	}
	// The directory read is the single work bound. Do not impose a smaller PID
	// cap here: /proc is normally returned in ascending PID order, so doing so
	// systematically hid newer, higher-PID Codex clients on busy hosts.
	processes := make([]int, 0, len(names))
	for _, name := range names {
		pid, parseErr := strconv.Atoi(name)
		if parseErr != nil || pid <= 0 || strconv.Itoa(pid) != name {
			continue
		}
		processes = append(processes, pid)
	}
	sort.Ints(processes)
	return processes, nil
}

type procStatus struct {
	realUID       uint32
	effectiveUID  uint32
	savedUID      uint32
	filesystemUID uint32
	parentPID     int
}

func (status procStatus) matchesUID(uid uint32) bool {
	return status.realUID == uid && status.effectiveUID == uid &&
		status.savedUID == uid && status.filesystemUID == uid
}

func readProcStatus(path string) (procStatus, bool) {
	file, err := os.Open(path)
	if err != nil {
		return procStatus{}, false
	}
	defer file.Close()

	var result procStatus
	foundUID := false
	foundParent := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 256), maxProcStatusLine)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "Uid:"):
			fields := strings.Fields(strings.TrimPrefix(line, "Uid:"))
			if len(fields) != 4 {
				return procStatus{}, false
			}
			values := []*uint32{&result.realUID, &result.effectiveUID, &result.savedUID, &result.filesystemUID}
			for index, field := range fields {
				parsed, parseErr := strconv.ParseUint(field, 10, 32)
				if parseErr != nil {
					return procStatus{}, false
				}
				*values[index] = uint32(parsed)
			}
			foundUID = true
		case strings.HasPrefix(line, "PPid:"):
			fields := strings.Fields(strings.TrimPrefix(line, "PPid:"))
			if len(fields) != 1 {
				return procStatus{}, false
			}
			parent, parseErr := strconv.Atoi(fields[0])
			if parseErr != nil || parent < 0 {
				return procStatus{}, false
			}
			result.parentPID = parent
			foundParent = true
		}
		if foundUID && foundParent {
			return result, true
		}
	}
	return procStatus{}, false
}

func readProcStartTime(path string) (uint64, bool) {
	file, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	payload, err := io.ReadAll(io.LimitReader(file, maxProcStatBytes+1))
	_ = file.Close()
	if err != nil || len(payload) == 0 || len(payload) > maxProcStatBytes {
		return 0, false
	}
	closing := strings.LastIndexByte(string(payload), ')')
	if closing < 0 {
		return 0, false
	}
	// Fields after the parenthesized command start with field 3 (state), so
	// starttime (field 22) is index 19 in this bounded suffix.
	fields := strings.Fields(string(payload[closing+1:]))
	if len(fields) <= 19 {
		return 0, false
	}
	started, parseErr := strconv.ParseUint(fields[19], 10, 64)
	if parseErr != nil {
		return 0, false
	}
	return started, true
}

func executableBasename(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	target = strings.TrimSuffix(target, " (deleted)")
	return filepath.Base(target)
}

func readProcCgroup(path string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	payload, err := io.ReadAll(io.LimitReader(file, maxProcCgroupBytes+1))
	_ = file.Close()
	if err != nil || len(payload) == 0 || len(payload) > maxProcCgroupBytes {
		return "", false
	}
	value := strings.TrimSuffix(string(payload), "\n")
	if value == "" || strings.ContainsAny(value, "\x00\r") {
		return "", false
	}
	return value, true
}

func cgroupIsSameOrDescendant(candidate, collector string) bool {
	candidatePath, candidateOK := cgroupV2Path(candidate)
	collectorPath, collectorOK := cgroupV2Path(collector)
	if !candidateOK || !collectorOK {
		return true
	}
	if candidatePath == collectorPath || collectorPath == "/" {
		return true
	}
	return strings.HasPrefix(candidatePath, strings.TrimSuffix(collectorPath, "/")+"/")
}

func cgroupV2Path(value string) (string, bool) {
	for _, line := range strings.Split(value, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[0] == "0" && parts[1] == "" &&
			strings.HasPrefix(parts[2], "/") && filepath.Clean(parts[2]) == parts[2] {
			return parts[2], true
		}
	}
	return "", false
}
