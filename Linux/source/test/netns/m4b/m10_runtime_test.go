// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
	"github.com/Irviodotnet/Irvio/internal/underlay"
)

type m10RuntimeStatus struct {
	State               irvioruntime.State    `json:"state"`
	RuntimeGeneration   uint64                `json:"runtime_generation"`
	NetworkGeneration   uint64                `json:"network_generation"`
	TransportGeneration uint64                `json:"transport_generation"`
	SessionGeneration   uint64                `json:"session_generation"`
	ActivePeer          string                `json:"active_peer"`
	ReservePeer         string                `json:"reserve_peer"`
	TunnelLeaseID       string                `json:"tunnel_lease_id"`
	TunnelLeaseExpiry   string                `json:"tunnel_lease_expiry"`
	AWGKeyEpoch         uint64                `json:"awg_key_epoch"`
	SelectedEndpoint    string                `json:"selected_endpoint"`
	MuxPort             uint16                `json:"mux_port"`
	ProtectedMode       uint8                 `json:"protected_mode"`
	ProtectionAdopted   bool                  `json:"protection_adopted"`
	UnderlayIPv4        underlay.Reachability `json:"underlay_ipv4"`
	UnderlayIPv6        underlay.Reachability `json:"underlay_ipv6"`
}

type m10RuntimeEvent struct {
	Message string `json:"msg"`
	m10RuntimeStatus
}

func (e m10RuntimeEvent) isRuntimeEvent() bool {
	return e.Message == "IRVIOD_RUNTIME_EVENT" || e.Message == "HYDRAD_RUNTIME_EVENT"
}

func decodeM10RuntimeEvents(data []byte) []m10RuntimeEvent {
	lines := strings.Split(string(data), "\n")
	events := make([]m10RuntimeEvent, 0, len(lines))
	for _, line := range lines {
		var event m10RuntimeEvent
		if json.Unmarshal([]byte(line), &event) == nil {
			events = append(events, event)
		}
	}
	return events
}

func TestM10_IrviodOwnsFullProductionInternetLifecycle(t *testing.T) {
	requirePrivileged(t)
	irviod := assertNormalUserExecutable(t, "HYDRA_M4B_HYDRAD")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")

	lab := newLab(t, "m10-runtime")
	topology := buildM7Topology(lab)
	lab.inNamespace(topology.ClientNamespace, "ip", "route", "add", "default", "via", topology.ExitOuter4.String(), "dev", topology.ClientOuterIf)
	lab.inNamespace(topology.ClientNamespace, "ip", "-6", "route", "add", "default", "via", topology.ExitOuter6.String(), "dev", topology.ClientOuterIf)
	lab.inNamespace(topology.ExitNamespace, "ip", "route", "add", "default", "via", topology.Internet4.String(), "dev", topology.ExitWANIf)
	lab.inNamespace(topology.ExitNamespace, "ip", "-6", "route", "add", "default", "via", topology.Internet6.String(), "dev", topology.ExitWANIf)

	internet, internetRelease, observations := startM7InternetServer(t, lab, topology)
	runtimeDir := lab.tempDir("m10-runtime")
	bootstrapDir := filepath.Join(runtimeDir, "bootstrap")
	exitDir := filepath.Join(runtimeDir, "exit")
	clientDir := filepath.Join(runtimeDir, "client")
	reuseClientDir := filepath.Join(runtimeDir, "reuse-client")
	for _, directory := range []string{bootstrapDir, exitDir, clientDir, reuseClientDir} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	bootstrapProcess := lab.startChild("m10-bootstrap", topology.ExitNamespace, irviod,
		"--data-dir", bootstrapDir, "--listen-port", "42000", "--print-node-info")
	bootstrapInfo := waitNodeInfo(t, lab, bootstrapProcess, "m10-bootstrap.stdout.log", 1)
	bootstrap := selectMultiaddr(t, bootstrapInfo, "ip4", topology.ExitOuter4)

	exitProcess := lab.startChild("m10-exit", topology.ExitNamespace, irviod,
		m10ExitArgs(topology, exitDir, bootstrap, false)...)
	exitInfo := waitNodeInfo(t, lab, exitProcess, "m10-exit.stdout.log", 1)

	clientArgs := []string{
		"--data-dir", clientDir,
		"--listen-port", "42001",
		"--bootstrap", bootstrap,
		"--runtime", "--connect", "DE",
		"--udp-port", "41001",
		"--tun-name", "hydra0",
		"--bypass-prefix", netip.PrefixFrom(topology.ClientOuter4, 24).Masked().String(),
		"--lease-ttl", "3s", "--lease-sweep", "100ms",
		"--lease-renew-lead", "1s", "--lease-renew-retry", "300ms",
		"--print-node-info", "--print-runtime-events",
	}
	clientProcess := lab.startChild("m10-client", topology.ClientNamespace, irviod, clientArgs...)
	clientInfo := waitNodeInfo(t, lab, clientProcess, "m10-client.stdout.log", 1)
	status := waitM10RuntimeState(t, lab, clientProcess, "m10-client.stderr.log", irvioruntime.StateConnected, 30*time.Second)
	if status.ActivePeer != exitInfo.PeerID || status.RuntimeGeneration < 2 || status.NetworkGeneration == 0 ||
		status.TransportGeneration != status.NetworkGeneration || status.SessionGeneration == 0 ||
		status.TunnelLeaseID == "" || status.AWGKeyEpoch == 0 || status.SelectedEndpoint == "" ||
		status.MuxPort != 41001 || status.ProtectedMode == 0 {
		t.Fatalf("incomplete production runtime status: %+v exit=%s client=%s", status, exitInfo.PeerID, clientInfo.PeerID)
	}
	initialExpiry, err := time.Parse(time.RFC3339Nano, status.TunnelLeaseExpiry)
	if err != nil {
		t.Fatalf("invalid redacted lease expiry %q: %v", status.TunnelLeaseExpiry, err)
	}

	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080)), topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))
	renewed := waitM10LeaseRenewal(t, lab, clientProcess, "m10-client.stderr.log", status.TunnelLeaseID, initialExpiry, 10*time.Second)
	if renewed.ActivePeer != status.ActivePeer || renewed.SessionGeneration != status.SessionGeneration || renewed.AWGKeyEpoch != status.AWGKeyEpoch {
		t.Fatalf("lease renewal changed immutable session identity: before=%+v after=%+v", status, renewed)
	}
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080)), topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))

	if err := stopM10Child(exitProcess, 12*time.Second); err != nil {
		t.Fatalf("stop pre-rotation Exit hydrad: %v", err)
	}
	exitProcess = lab.startChild("m10-exit-rotated", topology.ExitNamespace, irviod,
		m10ExitArgs(topology, exitDir, bootstrap, true)...)
	rotatedInfo := waitNodeInfo(t, lab, exitProcess, "m10-exit-rotated.stdout.log", 1)
	if rotatedInfo.PeerID != exitInfo.PeerID {
		t.Fatalf("AWG rotation changed Irvio identity: before=%s after=%s", exitInfo.PeerID, rotatedInfo.PeerID)
	}
	rotated := waitM10RotatedSession(t, lab, clientProcess, "m10-client.stderr.log", status, 45*time.Second)
	if rotated.AWGKeyEpoch != status.AWGKeyEpoch+1 || rotated.ActivePeer != exitInfo.PeerID ||
		rotated.SessionGeneration <= status.SessionGeneration || rotated.TunnelLeaseID == "" {
		t.Fatalf("rotated session did not replace the old binding: before=%+v after=%+v", status, rotated)
	}
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080)), topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))

	tunIfIndex := m10InterfaceIndex(t, lab, topology.ClientNamespace, "hydra0")
	newClientAddress := netip.MustParseAddr("198.18.77.3")
	runIPBatch(t, lab, topology.ClientNamespace, "m10-runtime-migration", fmt.Sprintf(
		"addr del %s/24 dev %s\naddr add %s/24 dev %s\nroute replace default via %s dev %s src %s\n",
		topology.ClientOuter4, topology.ClientOuterIf, newClientAddress, topology.ClientOuterIf,
		topology.ExitOuter4, topology.ClientOuterIf, newClientAddress))
	migrated := waitM10MigratedSession(t, lab, clientProcess, "m10-client.stderr.log", rotated, 30*time.Second)
	if migrated.ActivePeer != rotated.ActivePeer || migrated.NetworkGeneration <= rotated.NetworkGeneration ||
		migrated.TransportGeneration != migrated.NetworkGeneration || migrated.SessionGeneration <= rotated.SessionGeneration ||
		migrated.TunnelLeaseID != rotated.TunnelLeaseID || migrated.AWGKeyEpoch != rotated.AWGKeyEpoch ||
		migrated.MuxPort == 0 || migrated.MuxPort == rotated.MuxPort ||
		m10InterfaceIndex(t, lab, topology.ClientNamespace, "hydra0") != tunIfIndex {
		t.Fatalf("production migration violated logical tunnel ownership: before=%+v after=%+v ifindex=%d", rotated, migrated, tunIfIndex)
	}
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080)), topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))

	killM10Child(t, clientProcess)
	lab.inNamespace(topology.ClientNamespace, "nft", "list", "table", "inet", "hydra_protect")
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080), topology.ClientOuterIf, 0, false)
	clientProcess = lab.startChild("m10-client-adopt", topology.ClientNamespace, irviod, clientArgs...)
	restartedInfo := waitNodeInfo(t, lab, clientProcess, "m10-client-adopt.stdout.log", 1)
	if restartedInfo.PeerID != clientInfo.PeerID {
		t.Fatalf("Client Irvio identity changed after unexpected restart: before=%s after=%s", clientInfo.PeerID, restartedInfo.PeerID)
	}
	restarted := waitM10RuntimeState(t, lab, clientProcess, "m10-client-adopt.stderr.log", irvioruntime.StateConnected, 30*time.Second)
	if restarted.ActivePeer != migrated.ActivePeer || !restarted.ProtectionAdopted || restarted.ProtectedMode == 0 ||
		restarted.TunnelLeaseID == "" || restarted.AWGKeyEpoch != migrated.AWGKeyEpoch {
		t.Fatalf("restarted runtime did not reconcile protected ownership: before=%+v after=%+v", migrated, restarted)
	}
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080)), topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))

	restartedExpiry, err := time.Parse(time.RFC3339Nano, restarted.TunnelLeaseExpiry)
	if err != nil {
		t.Fatalf("invalid restarted lease expiry %q: %v", restarted.TunnelLeaseExpiry, err)
	}
	killM10Child(t, clientProcess)
	lab.inNamespace(topology.ClientNamespace, "nft", "list", "table", "inet", "hydra_protect")
	runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080), topology.ClientOuterIf, 0, false)
	waitM10LeaseExpiry(t, restartedExpiry, 300*time.Millisecond)
	reuseArgs := []string{
		"--data-dir", reuseClientDir,
		"--listen-port", "42003",
		"--bootstrap", bootstrap,
		"--runtime", "--connect", "DE",
		"--udp-port", "41003",
		"--tun-name", "hydra0",
		"--bypass-prefix", netip.PrefixFrom(newClientAddress, 24).Masked().String(),
		"--lease-ttl", "3s", "--lease-sweep", "100ms",
		"--lease-renew-lead", "1s", "--lease-renew-retry", "300ms",
		"--print-node-info", "--print-runtime-events",
	}
	clientProcess = lab.startChild("m10-client-reuse", topology.ClientNamespace, irviod, reuseArgs...)
	reuseInfo := waitNodeInfo(t, lab, clientProcess, "m10-client-reuse.stdout.log", 1)
	if reuseInfo.PeerID == clientInfo.PeerID {
		t.Fatal("safe-reuse client unexpectedly retained expired lease owner identity")
	}
	reused := waitM10RuntimeState(t, lab, clientProcess, "m10-client-reuse.stderr.log", irvioruntime.StateConnected, 30*time.Second)
	if reused.ActivePeer != restarted.ActivePeer || !reused.ProtectionAdopted || reused.TunnelLeaseID == "" ||
		reused.TunnelLeaseID == restarted.TunnelLeaseID {
		t.Fatalf("expired lease was not safely reallocated to the new owner: expired=%+v reused=%+v", restarted, reused)
	}
	assertM7Probe(t, runM7Probe(t, lab, topology.ClientNamespace, "http", endpointValue(topology.Internet4, 18080)), topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))

	lab.inNamespace(topology.ClientNamespace, "test", "-e", "/sys/class/net/hydra0")
	lab.inNamespace(topology.ExitNamespace, "test", "-e", "/sys/class/net/hydra0")
	lab.inNamespace(topology.ClientNamespace, "ip", "route", "show", "table", "51820", "default", "dev", "hydra0")
	lab.inNamespace(topology.ClientNamespace, "nft", "list", "table", "inet", "hydra_protect")
	lab.inNamespace(topology.ExitNamespace, "nft", "list", "table", "inet", "hydra_exit")

	if err := stopM10Child(clientProcess, 12*time.Second); err != nil {
		t.Fatalf("stop Client hydrad: %v", err)
	}
	if err := stopM10Child(exitProcess, 12*time.Second); err != nil {
		t.Fatalf("stop Exit hydrad: %v", err)
	}
	if err := stopM10Child(bootstrapProcess, 12*time.Second); err != nil {
		t.Fatalf("stop bootstrap hydrad: %v", err)
	}
	if err := touch(internetRelease); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := internet.wait(ctx); err != nil {
		t.Fatalf("stop deterministic Internet: %v", err)
	}

	lab.inNamespace(topology.ClientNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
	lab.inNamespace(topology.ExitNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
	lab.inNamespace(topology.ClientNamespace, "nft", "list", "ruleset")
	lab.captureNamespace(topology.ClientNamespace, "m10-client", false)
	lab.captureNamespace(topology.ExitNamespace, "m10-exit", true)
	lab.captureNamespace(topology.InternetNamespace, "m10-internet", false)
	if data, err := os.ReadFile(observations); err != nil || len(data) == 0 {
		t.Fatalf("missing independent Internet observation: bytes=%d err=%v", len(data), err)
	}
}

func m10ExitArgs(topology m7Topology, dataDir, bootstrap string, rotate bool) []string {
	args := []string{
		"--data-dir", dataDir,
		"--listen-port", "42002",
		"--bootstrap", bootstrap,
		"--runtime",
		"--exit", "--country", "DE",
		"--udp-port", "41002",
		"--tun-name", "hydra0",
		"--exit-wan", topology.ExitWANIf,
		"--exit-tun-address", "10.77.0.1/30",
		"--exit-ipv4-pool", "10.77.0.0/30",
		"--exit-management-prefix", topology.ExitWAN4.String() + "/32",
		"--lease-ttl", "3s", "--lease-sweep", "100ms",
		"--print-node-info", "--print-runtime-events",
	}
	if rotate {
		args = append(args, "--rotate-awg-key")
	}
	return args
}

func waitM10RuntimeState(t *testing.T, lab *lab, child *childProcess, file string, expected irvioruntime.State, maximum time.Duration) m10RuntimeStatus {
	t.Helper()
	path := filepath.Join(lab.artifactDir, file)
	ctx, cancel := context.WithTimeout(context.Background(), maximum)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var latest m10RuntimeStatus
	for {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		for _, event := range decodeM10RuntimeEvents(data) {
			if !event.isRuntimeEvent() {
				continue
			}
			latest = event.m10RuntimeStatus
			if latest.State == expected {
				return latest
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for hydrad runtime state %d: %v; latest=%+v", expected, ctx.Err(), latest)
		case <-child.done:
			child.mu.Lock()
			err := child.waitErr
			child.mu.Unlock()
			t.Fatalf("hydrad exited before runtime state %d: %v; latest=%+v", expected, err, latest)
		case <-ticker.C:
		}
	}
}

func waitM10LeaseRenewal(
	t *testing.T,
	lab *lab,
	child *childProcess,
	file string,
	leaseID string,
	initialExpiry time.Time,
	maximum time.Duration,
) m10RuntimeStatus {
	t.Helper()
	path := filepath.Join(lab.artifactDir, file)
	ctx, cancel := context.WithTimeout(context.Background(), maximum)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var latest m10RuntimeStatus
	for {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		for _, event := range decodeM10RuntimeEvents(data) {
			if !event.isRuntimeEvent() {
				continue
			}
			latest = event.m10RuntimeStatus
			expiry, parseErr := time.Parse(time.RFC3339Nano, latest.TunnelLeaseExpiry)
			if latest.TunnelLeaseID == leaseID && parseErr == nil && expiry.After(initialExpiry) {
				return latest
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for lease %s renewal: %v; latest=%+v", leaseID, ctx.Err(), latest)
		case <-child.done:
			child.mu.Lock()
			err := child.waitErr
			child.mu.Unlock()
			t.Fatalf("hydrad exited before lease renewal: %v; latest=%+v", err, latest)
		case <-ticker.C:
		}
	}
}

func waitM10RotatedSession(
	t *testing.T,
	lab *lab,
	child *childProcess,
	file string,
	previous m10RuntimeStatus,
	maximum time.Duration,
) m10RuntimeStatus {
	t.Helper()
	path := filepath.Join(lab.artifactDir, file)
	ctx, cancel := context.WithTimeout(context.Background(), maximum)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var latest m10RuntimeStatus
	for {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		for _, event := range decodeM10RuntimeEvents(data) {
			if !event.isRuntimeEvent() {
				continue
			}
			latest = event.m10RuntimeStatus
			if latest.State == irvioruntime.StateConnected &&
				latest.ActivePeer == previous.ActivePeer &&
				latest.AWGKeyEpoch > previous.AWGKeyEpoch &&
				latest.SessionGeneration > previous.SessionGeneration {
				return latest
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for rotated AWG session: %v; previous=%+v latest=%+v", ctx.Err(), previous, latest)
		case <-child.done:
			child.mu.Lock()
			err := child.waitErr
			child.mu.Unlock()
			t.Fatalf("Client hydrad exited before AWG rotation recovery: %v; latest=%+v", err, latest)
		case <-ticker.C:
		}
	}
}

func waitM10MigratedSession(
	t *testing.T,
	lab *lab,
	child *childProcess,
	file string,
	previous m10RuntimeStatus,
	maximum time.Duration,
) m10RuntimeStatus {
	t.Helper()
	path := filepath.Join(lab.artifactDir, file)
	ctx, cancel := context.WithTimeout(context.Background(), maximum)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var latest m10RuntimeStatus
	for {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		for _, event := range decodeM10RuntimeEvents(data) {
			if !event.isRuntimeEvent() {
				continue
			}
			latest = event.m10RuntimeStatus
			if latest.State == irvioruntime.StateConnected &&
				latest.NetworkGeneration > previous.NetworkGeneration &&
				latest.TransportGeneration == latest.NetworkGeneration &&
				latest.SessionGeneration > previous.SessionGeneration {
				return latest
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for production network migration: %v; previous=%+v latest=%+v", ctx.Err(), previous, latest)
		case <-child.done:
			child.mu.Lock()
			err := child.waitErr
			child.mu.Unlock()
			t.Fatalf("Client hydrad exited during network migration: %v; latest=%+v", err, latest)
		case <-ticker.C:
		}
	}
}

func m10InterfaceIndex(t *testing.T, lab *lab, namespace, name string) int {
	t.Helper()
	value := strings.TrimSpace(lab.inNamespace(namespace, "cat", "/sys/class/net/"+name+"/ifindex"))
	index, err := strconv.Atoi(value)
	if err != nil || index <= 0 {
		t.Fatalf("invalid %s ifindex %q: %v", name, value, err)
	}
	return index
}

func stopM10Child(child *childProcess, maximum time.Duration) error {
	select {
	case <-child.done:
		return child.wait(context.Background())
	default:
	}
	if err := syscall.Kill(-child.command.Process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), maximum)
	defer cancel()
	if err := child.wait(ctx); err != nil {
		return fmt.Errorf("wait for graceful hydrad shutdown: %w", err)
	}
	return nil
}

func waitM10LeaseExpiry(t *testing.T, validUntil time.Time, sweep time.Duration) {
	t.Helper()
	deadline := validUntil.Add(sweep)
	delay := time.Until(deadline)
	if delay <= 0 {
		return
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	maximum := time.NewTimer(delay + 2*time.Second)
	defer maximum.Stop()
	select {
	case <-timer.C:
	case <-maximum.C:
		t.Fatalf("lease expiry barrier exceeded: valid_until=%s sweep=%s", validUntil, sweep)
	}
}
