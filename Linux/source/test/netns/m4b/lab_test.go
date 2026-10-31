// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

const (
	privilegedEnvironment = "HYDRA_M4B_PRIVILEGED"
	namespacePrefix       = "hm4b-"
	commandOutputLimit    = 1 << 20
	childOutputLimit      = 4 << 20
	commandTimeout        = 20 * time.Second
)

var (
	privileged = os.Getenv(privilegedEnvironment) == "1"
	suite      *suiteState
	labCounter atomic.Uint32
)

type suiteState struct {
	runID       string
	artifactDir string
	commandLog  *os.File
	logMu       sync.Mutex
}

type hostSnapshot struct {
	Namespaces string            `json:"namespaces"`
	Links      string            `json:"links"`
	Addresses  string            `json:"addresses"`
	RoutesIPv4 string            `json:"routes_ipv4"`
	RoutesIPv6 string            `json:"routes_ipv6"`
	RulesIPv4  string            `json:"rules_ipv4"`
	RulesIPv6  string            `json:"rules_ipv6"`
	NFTables   string            `json:"nftables"`
	Cgroups    string            `json:"hydra_cgroups"`
	Sysctls    map[string]string `json:"sysctls"`
}

func TestMain(m *testing.M) {
	if !privileged {
		os.Exit(m.Run())
	}
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "%s=1 requires an already-built test runner executed as root\n", privilegedEnvironment)
		os.Exit(2)
	}
	state, err := newSuiteState()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	suite = state
	defer state.commandLog.Close()
	startedAt := time.Now().UTC()
	if err := state.captureEnvironment(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "M4b environment capture failed; artifacts: %s: %v\n", state.artifactDir, err)
		os.Exit(2)
	}
	before, err := state.captureHost(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "M4b host snapshot before tests failed; artifacts: %s: %v\n", state.artifactDir, err)
		os.Exit(2)
	}
	if err := state.writeJSON("host-before.json", before); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	code := m.Run()
	after, afterErr := state.captureHost(context.Background())
	if afterErr == nil {
		afterErr = state.writeJSON("host-after.json", after)
	}
	if afterErr != nil {
		fmt.Fprintf(os.Stderr, "M4b host snapshot after tests failed; artifacts: %s: %v\n", state.artifactDir, afterErr)
		code = 1
	} else if !reflect.DeepEqual(before, after) {
		_ = state.writeJSON("host-integrity-diff.json", map[string]hostSnapshot{"before": before, "after": after})
		fmt.Fprintf(os.Stderr, "M4b host integrity check failed; artifacts: %s\n", state.artifactDir)
		code = 1
	}
	if leftovers, err := state.command(context.Background(), "ip", "netns", "list"); err != nil {
		fmt.Fprintf(os.Stderr, "M4b final namespace check failed: %v\n", err)
		code = 1
	} else {
		for _, line := range strings.Split(leftovers, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), namespacePrefix+state.runID) {
				fmt.Fprintf(os.Stderr, "M4b namespace leaked: %s; artifacts: %s\n", line, state.artifactDir)
				code = 1
			}
		}
	}
	if err := state.writeJSON("results.json", map[string]any{
		"run_id": state.runID, "started_at": startedAt, "completed_at": time.Now().UTC(),
		"exit_code": code, "host_integrity": reflect.DeepEqual(before, after) && afterErr == nil,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "M4b result artifact failed; artifacts: %s: %v\n", state.artifactDir, err)
		code = 1
	}
	fmt.Fprintf(os.Stderr, "IRVIO M4b artifacts: %s\n", state.artifactDir)
	os.Exit(code)
}

func (s *suiteState) captureEnvironment(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, commandTimeout)
	defer cancel()
	var lines []string
	for _, command := range [][]string{
		{"uname", "-a"}, {"ip", "-Version"}, {"nft", "--version"}, {"sysctl", "--version"},
	} {
		output, err := s.command(ctx, command[0], command[1:]...)
		if err != nil {
			return err
		}
		lines = append(lines, "$ "+strings.Join(command, " "), strings.TrimSpace(output))
	}
	for _, optional := range []string{"conntrack", "tcpdump"} {
		path, err := exec.LookPath(optional)
		if err != nil {
			lines = append(lines, optional+": unavailable")
		} else {
			lines = append(lines, optional+": "+path)
		}
	}
	return os.WriteFile(filepath.Join(s.artifactDir, "environment.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func newSuiteState() (*suiteState, error) {
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, fmt.Errorf("generate M4b run ID: %w", err)
	}
	runID := hex.EncodeToString(random[:])
	artifactDir := os.Getenv("HYDRA_M4B_ARTIFACT_DIR")
	if artifactDir == "" {
		artifactDir = filepath.Join(os.TempDir(), "hydra-m4b-"+runID)
	}
	if !filepath.IsAbs(artifactDir) || !strings.HasPrefix(filepath.Clean(artifactDir), filepath.Clean(os.TempDir())+string(os.PathSeparator)) {
		return nil, fmt.Errorf("artifact directory must be an absolute child of %s", os.TempDir())
	}
	if err := os.Mkdir(artifactDir, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact directory: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(artifactDir, "commands.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create command log: %w", err)
	}
	return &suiteState{runID: runID, artifactDir: artifactDir, commandLog: logFile}, nil
}

func requirePrivileged(t *testing.T) {
	t.Helper()
	if !privileged {
		t.Skip("set HYDRA_M4B_PRIVILEGED=1 and execute the prebuilt runner as root")
	}
	if os.Geteuid() != 0 {
		t.Fatal("privileged runner does not have effective UID 0")
	}
}

func TestM4B_Prerequisites(t *testing.T) {
	requirePrivileged(t)
	for _, name := range []string{"ip", "nft", "sysctl"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatalf("required tool %s is unavailable: %v", name, err)
		}
		if !filepath.IsAbs(path) {
			t.Fatalf("required tool %s resolved to non-absolute path %q", name, path)
		}
	}
	for _, variable := range []string{"HYDRA_M4B_NATPROBE", "HYDRA_M4B_RFC5780", "HYDRA_M4B_SOCKETMUX", "HYDRA_M4B_HYDRAD", "HYDRA_M4B_STUNTEST", "HYDRA_M4B_SOCKETTEST", "HYDRA_M5A_ICEHELPER"} {
		assertNormalUserExecutable(t, variable)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	assertExecutableOwnedByNonRoot(t, "privileged test runner", executable)
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	probe := namespacePrefix + suite.runID + "-000-probe"
	if _, err := suite.command(ctx, "ip", "netns", "add", probe); err != nil {
		t.Fatalf("CAP_NET_ADMIN/network namespace creation unavailable: %v", err)
	}
	if _, err := suite.command(ctx, "ip", "netns", "delete", probe); err != nil {
		t.Fatalf("delete prerequisite probe namespace: %v", err)
	}
}

func assertNormalUserExecutable(t *testing.T, variable string) string {
	t.Helper()
	path := os.Getenv(variable)
	if !filepath.IsAbs(path) {
		t.Fatalf("%s must contain an absolute helper path", variable)
	}
	assertExecutableOwnedByNonRoot(t, variable, path)
	return path
}

func assertExecutableOwnedByNonRoot(t *testing.T, label, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s %s: %v", label, path, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("%s is not an executable regular file: %s", label, path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid == 0 {
		t.Fatalf("%s must be built by a non-root user: %s", label, path)
	}
}

func (s *suiteState) captureHost(parent context.Context) (hostSnapshot, error) {
	ctx, cancel := context.WithTimeout(parent, commandTimeout)
	defer cancel()
	commands := []struct {
		name string
		args []string
		json bool
	}{
		{name: "namespaces", args: []string{"ip", "-j", "netns", "list"}, json: true},
		{name: "links", args: []string{"ip", "-j", "link", "show"}, json: true},
		{name: "addresses", args: []string{"ip", "-j", "addr", "show"}, json: true},
		{name: "routes4", args: []string{"ip", "-j", "route", "show", "table", "all"}, json: true},
		{name: "routes6", args: []string{"ip", "-j", "-6", "route", "show", "table", "all"}, json: true},
		{name: "rules4", args: []string{"ip", "-j", "rule", "show"}, json: true},
		{name: "rules6", args: []string{"ip", "-j", "-6", "rule", "show"}, json: true},
		{name: "nft", args: []string{"nft", "-j", "list", "ruleset"}, json: true},
	}
	values := make(map[string]string, len(commands))
	for _, item := range commands {
		output, err := s.command(ctx, item.args[0], item.args[1:]...)
		if err != nil {
			return hostSnapshot{}, fmt.Errorf("capture host %s: %w", item.name, err)
		}
		if item.json {
			output, err = normalizeJSON([]byte(output))
			if err != nil {
				return hostSnapshot{}, fmt.Errorf("normalize host %s: %w", item.name, err)
			}
		}
		values[item.name] = output
	}
	sysctls := make(map[string]string, 3)
	for _, key := range []string{
		"net.ipv4.ip_forward", "net.ipv6.conf.all.forwarding", "net.ipv4.conf.all.src_valid_mark",
	} {
		value, err := s.command(ctx, "sysctl", "-n", key)
		if err != nil {
			return hostSnapshot{}, fmt.Errorf("capture host sysctl %s: %w", key, err)
		}
		sysctls[key] = strings.TrimSpace(value)
	}
	cgroups, err := captureIrvioCgroups()
	if err != nil {
		return hostSnapshot{}, fmt.Errorf("capture host Irvio cgroups: %w", err)
	}
	return hostSnapshot{
		Namespaces: values["namespaces"], Links: values["links"], Addresses: values["addresses"],
		RoutesIPv4: values["routes4"], RoutesIPv6: values["routes6"], RulesIPv4: values["rules4"], RulesIPv6: values["rules6"], NFTables: values["nft"], Cgroups: cgroups, Sysctls: sysctls,
	}, nil
}

func captureIrvioCgroups() (string, error) {
	parent, err := currentCgroupParent()
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return "", err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && irviodCgroupName.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return strings.Join(names, "\n"), nil
}

func currentCgroupParent() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "0::") {
			relative := strings.TrimPrefix(line, "0::")
			return filepath.Join("/sys/fs/cgroup", filepath.Clean("/"+relative)), nil
		}
	}
	return "", os.ErrNotExist
}

func normalizeJSON(raw []byte) (string, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	value = normalizeValue(value)
	encoded, err := json.Marshal(value)
	return string(encoded), err
}

func normalizeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{
			"bytes", "packets", "handle", "stats", "stats64", "expires", "used",
			"preferred_life_time", "valid_life_time",
		} {
			delete(typed, key)
		}
		for key, child := range typed {
			typed[key] = normalizeValue(child)
		}
		return typed
	case []any:
		for index, child := range typed {
			typed[index] = normalizeValue(child)
		}
		sort.SliceStable(typed, func(left, right int) bool {
			a, _ := json.Marshal(typed[left])
			b, _ := json.Marshal(typed[right])
			return bytes.Compare(a, b) < 0
		})
		return typed
	default:
		return value
	}
}

func (s *suiteState) writeJSON(name string, value any) error {
	path := filepath.Join(s.artifactDir, filepath.Clean(name))
	if filepath.Dir(path) != s.artifactDir {
		return errors.New("artifact path escaped run directory")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(value)
	closeErr := file.Close()
	return errors.Join(encodeErr, closeErr)
}

type limitedBuffer struct {
	buffer    bytes.Buffer
	remaining int
	truncated bool
}

func (b *limitedBuffer) Write(payload []byte) (int, error) {
	original := len(payload)
	if b.remaining > 0 {
		accepted := min(len(payload), b.remaining)
		_, _ = b.buffer.Write(payload[:accepted])
		b.remaining -= accepted
		if accepted != len(payload) {
			b.truncated = true
		}
	} else if len(payload) > 0 {
		b.truncated = true
	}
	return original, nil
}

func (b *limitedBuffer) String() string {
	if b.truncated {
		return b.buffer.String() + "\n[output truncated]\n"
	}
	return b.buffer.String()
}

func (s *suiteState) command(parent context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, commandTimeout)
	defer cancel()
	stdout := &limitedBuffer{remaining: commandOutputLimit}
	stderr := &limitedBuffer{remaining: commandOutputLimit}
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	s.logMu.Lock()
	fmt.Fprintf(s.commandLog, "$ %s %s\n%s%s", name, strings.Join(args, " "), stdout.String(), stderr.String())
	_ = s.commandLog.Sync()
	s.logMu.Unlock()
	if ctx.Err() != nil {
		return stdout.String(), fmt.Errorf("command %s timed out: %w", name, ctx.Err())
	}
	if err != nil {
		return stdout.String(), fmt.Errorf("command %s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

var safeResourceName = regexp.MustCompile(`^[a-z0-9-]+$`)

type lab struct {
	t              *testing.T
	name           string
	artifactDir    string
	namespaces     []string
	hostLinks      []string
	tempDirs       []string
	children       []*childProcess
	controlCgroups []string
	closed         sync.Once
	index          uint32
}

func newLab(t *testing.T, label string) *lab {
	t.Helper()
	requirePrivileged(t)
	index := labCounter.Add(1)
	name := fmt.Sprintf("%s%s-%03x", namespacePrefix, suite.runID, index)
	directory := filepath.Join(suite.artifactDir, fmt.Sprintf("%03x-%s", index, label))
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatalf("create lab artifact directory: %v", err)
	}
	lab := &lab{t: t, name: name, artifactDir: directory, index: index}
	t.Cleanup(func() {
		if err := lab.Close(); err != nil {
			t.Errorf("cleanup M4b lab: %v", err)
		}
	})
	return lab
}

func (l *lab) namespace(role string) string {
	l.t.Helper()
	name := l.name + "-" + role
	if !safeResourceName.MatchString(name) {
		l.t.Fatalf("unsafe namespace name %q", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	if _, err := suite.command(ctx, "ip", "netns", "add", name); err != nil {
		l.t.Fatalf("create namespace %s: %v", name, err)
	}
	l.namespaces = append(l.namespaces, name)
	return name
}

func (l *lab) interfaceName(role byte) string {
	return fmt.Sprintf("hy%c%04x", role, l.index&0xffff)
}

func (l *lab) tempDir(label string) string {
	l.t.Helper()
	if !safeResourceName.MatchString(label) {
		l.t.Fatalf("unsafe temporary directory label %q", label)
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("hydra-m4b-runtime-%s-%03x-%s", suite.runID, l.index, label))
	if err := os.Mkdir(path, 0o700); err != nil {
		l.t.Fatalf("create temporary runtime directory: %v", err)
	}
	l.tempDirs = append(l.tempDirs, path)
	return path
}

func (l *lab) run(name string, args ...string) string {
	l.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	output, err := suite.command(ctx, name, args...)
	if err != nil {
		l.t.Fatal(err)
	}
	return output
}

func (l *lab) inNamespace(namespace, name string, args ...string) string {
	l.t.Helper()
	all := append([]string{"netns", "exec", namespace, name}, args...)
	return l.run("ip", all...)
}

func (l *lab) createVeth(leftNamespace, leftName, rightNamespace, rightName string) {
	l.t.Helper()
	if len(leftName) > 15 || len(rightName) > 15 || !safeResourceName.MatchString(leftName) || !safeResourceName.MatchString(rightName) {
		l.t.Fatalf("unsafe veth names %q and %q", leftName, rightName)
	}
	l.run("ip", "link", "add", leftName, "type", "veth", "peer", "name", rightName)
	l.hostLinks = append(l.hostLinks, leftName, rightName)
	l.run("ip", "link", "set", leftName, "netns", leftNamespace)
	l.run("ip", "link", "set", rightName, "netns", rightNamespace)
}

func (l *lab) Close() error {
	var closeErr error
	l.closed.Do(func() {
		for index := len(l.children) - 1; index >= 0; index-- {
			closeErr = errors.Join(closeErr, l.children[index].stop())
		}
		for _, path := range l.controlCgroups {
			if !irviodCgroupName.MatchString(filepath.Base(path)) {
				closeErr = errors.Join(closeErr, fmt.Errorf("refuse unsafe cgroup cleanup %q", path))
				continue
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				closeErr = errors.Join(closeErr, fmt.Errorf("remove Irvio control cgroup %s: %w", path, err))
			}
		}
		for _, name := range l.namespaces {
			closeErr = errors.Join(closeErr, l.captureFinalNamespace(name))
		}
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		for index := len(l.namespaces) - 1; index >= 0; index-- {
			name := l.namespaces[index]
			if !strings.HasPrefix(name, l.name+"-") || !safeResourceName.MatchString(name) {
				closeErr = errors.Join(closeErr, fmt.Errorf("refuse unsafe namespace cleanup %q", name))
				continue
			}
			if _, err := suite.command(ctx, "ip", "netns", "delete", name); err != nil && !strings.Contains(err.Error(), "No such file") {
				closeErr = errors.Join(closeErr, err)
			}
		}
		for _, name := range l.hostLinks {
			if len(name) <= 15 && safeResourceName.MatchString(name) {
				_, _ = suite.command(ctx, "ip", "link", "delete", name)
			}
		}
		for _, path := range l.tempDirs {
			base := filepath.Base(path)
			if filepath.Dir(path) != os.TempDir() || !strings.HasPrefix(base, "hydra-m4b-runtime-"+suite.runID+"-") || !safeResourceName.MatchString(base) {
				closeErr = errors.Join(closeErr, fmt.Errorf("refuse unsafe temporary directory cleanup %q", path))
				continue
			}
			if err := os.RemoveAll(path); err != nil {
				closeErr = errors.Join(closeErr, fmt.Errorf("remove temporary runtime directory %s: %w", path, err))
			}
		}
	})
	return closeErr
}

func (l *lab) captureFinalNamespace(namespace string) error {
	role := strings.TrimPrefix(namespace, l.name+"-")
	if role == namespace || !safeResourceName.MatchString(role) {
		return fmt.Errorf("refuse unsafe namespace artifact name %q", namespace)
	}
	commands := map[string][]string{
		"address.json":  {"ip", "-j", "addr", "show"},
		"route-v4.json": {"ip", "-j", "route", "show", "table", "all"},
		"route-v6.json": {"ip", "-j", "-6", "route", "show", "table", "all"},
		"nft.json":      {"nft", "-j", "list", "ruleset"},
	}
	var result error
	for suffix, command := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		args := append([]string{"netns", "exec", namespace}, command...)
		output, err := suite.command(ctx, "ip", args...)
		cancel()
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		path := filepath.Join(l.artifactDir, "final-"+role+"-"+suffix)
		if err := os.WriteFile(path, []byte(output), 0o600); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

type childProcess struct {
	command  *exec.Cmd
	done     chan struct{}
	waitErr  error
	mu       sync.Mutex
	stopOnce sync.Once
}

type boundedLog struct {
	file      *os.File
	mu        sync.Mutex
	remaining int
	marked    bool
}

func (l *boundedLog) Write(payload []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	original := len(payload)
	accepted := 0
	if l.remaining > 0 {
		accepted = min(len(payload), l.remaining)
		if _, err := l.file.Write(payload[:accepted]); err != nil {
			return 0, err
		}
		l.remaining -= accepted
	}
	if accepted < original && !l.marked {
		l.marked = true
		_, _ = l.file.WriteString("\n[child output truncated]\n")
	}
	return original, nil
}

func (l *boundedLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file.Close()
}

func (l *lab) startChild(label, namespace, binary string, args ...string) *childProcess {
	l.t.Helper()
	stdout, err := os.OpenFile(filepath.Join(l.artifactDir, label+".stdout.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		l.t.Fatal(err)
	}
	stderr, err := os.OpenFile(filepath.Join(l.artifactDir, label+".stderr.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = stdout.Close()
		l.t.Fatal(err)
	}
	commandName := "ip"
	all := append([]string{"netns", "exec", namespace, binary}, args...)
	controlCgroup, requiresHostCgroup := expectedControlCgroup(args)
	if requiresHostCgroup {
		path, err := exec.LookPath("nsenter")
		if err != nil {
			_ = stdout.Close()
			_ = stderr.Close()
			l.t.Fatalf("M11 accepted-control test requires nsenter: %v", err)
		}
		commandName = path
		all = append([]string{"--net=/var/run/netns/" + namespace, binary}, args...)
	}
	stdoutLog := &boundedLog{file: stdout, remaining: childOutputLimit}
	stderrLog := &boundedLog{file: stderr, remaining: childOutputLimit}
	command := exec.Command(commandName, all...)
	command.Stdout = stdoutLog
	command.Stderr = stderrLog
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if requiresHostCgroup {
		l.controlCgroups = append(l.controlCgroups, controlCgroup)
	}
	suite.logMu.Lock()
	fmt.Fprintf(suite.commandLog, "$ %s %s\n", commandName, strings.Join(all, " "))
	_ = suite.commandLog.Sync()
	suite.logMu.Unlock()
	if err := command.Start(); err != nil {
		_ = stdoutLog.Close()
		_ = stderrLog.Close()
		l.t.Fatalf("start %s: %v", label, err)
	}
	child := &childProcess{command: command, done: make(chan struct{})}
	l.children = append(l.children, child)
	go func() {
		err := command.Wait()
		_ = stdoutLog.Close()
		_ = stderrLog.Close()
		child.mu.Lock()
		child.waitErr = err
		child.mu.Unlock()
		close(child.done)
	}()
	return child
}

var irviodCgroupName = regexp.MustCompile(`^hydrad-[0-9a-f]{16}$`)

func expectedControlCgroup(args []string) (string, bool) {
	var dataDir string
	publicNode := false
	exit := false
	protectedMode := false
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--public-node":
			publicNode = true
		case "--exit":
			exit = true
		case "--protected-mode":
			protectedMode = true
		case "--data-dir":
			if index+1 < len(args) {
				dataDir = args[index+1]
				index++
			}
		}
	}
	if !(publicNode || exit) || !protectedMode || dataDir == "" {
		return "", false
	}
	parent, err := currentCgroupParent()
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256([]byte(dataDir))
	name := fmt.Sprintf("hydrad-%x", digest[:8])
	return filepath.Join(parent, name), true
}

func TestControlCgroupRequirement(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"protected-exit", []string{"--exit", "--protected-mode", "--data-dir", "/tmp/irvio-test-profile"}, true},
		{"protected-public", []string{"--public-node", "--protected-mode", "--data-dir", "/tmp/irvio-test-profile"}, true},
		{"protected-client", []string{"--protected-mode", "--data-dir", "/tmp/irvio-test-profile"}, false},
		{"unprotected-exit", []string{"--exit", "--data-dir", "/tmp/irvio-test-profile"}, false},
		{"missing-profile", []string{"--exit", "--protected-mode"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, needed := expectedControlCgroup(tc.args)
			if needed != tc.want || needed && (!filepath.IsAbs(path) || !irviodCgroupName.MatchString(filepath.Base(path))) {
				t.Fatalf("control cgroup: path=%q needed=%v want=%v", path, needed, tc.want)
			}
		})
	}
}

func (c *childProcess) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.waitErr
	}
}

func (c *childProcess) stop() error {
	var result error
	c.stopOnce.Do(func() {
		select {
		case <-c.done:
			return
		default:
		}
		if err := syscall.Kill(-c.command.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
			result = err
		}
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-c.done:
		case <-timer.C:
			if err := syscall.Kill(-c.command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
				result = errors.Join(result, err)
			}
			<-c.done
		}
	})
	return result
}

func waitForFile(ctx context.Context, path string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return nil
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func readJSON(path string, value any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewDecoder(file).Decode(value)
}

func touch(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}
