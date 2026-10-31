// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package m4b

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/protocol"
)

type m9ProbeReport struct {
	Remote         string `json:"remote"`
	ProbeReachable bool   `json:"probe_reachable"`
	ProbeLatency   int64  `json:"probe_latency_nanos"`
	SocketMark     uint32 `json:"socket_mark"`
}

func TestM9_CandidateProbeUsesM8BypassWithoutActiveExit(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	assertNormalUserExecutable(t, "HYDRA_M8_KILLSWITCH")
	lab := newLab(t, "m9-probe")
	topology := buildM8Topology(lab)
	server, serverRelease, observations := startM8Internet(t, lab, "m9-target", topology.WAN1Namespace, topology.WAN14, topology.WAN16)
	policy, policyRelease, _ := startM8Policy(t, lab, topology.ClientNamespace, "m9-policy")

	before := countM8Observations(t, observations)
	for index, item := range []struct {
		name   string
		target netip.AddrPort
		iface  string
	}{
		{"ipv4", netip.AddrPortFrom(topology.WAN14, 18080), topology.ClientWAN1If},
		{"ipv6", netip.AddrPortFrom(topology.WAN16, 18080), topology.ClientWAN1If},
	} {
		resultPath := filepath.Join(lab.artifactDir, "m9-candidate-probe-"+strconv.Itoa(index)+".json")
		child := lab.startChild("m9-candidate-probe-"+item.name, topology.ClientNamespace, os.Getenv("HYDRA_M8_KILLSWITCH"),
			"--mode", "candidate-probe", "--remote", item.target.String(), "--result", resultPath)
		waitM8Child(t, child)
		var result m9ProbeReport
		if err := readJSON(resultPath, &result); err != nil {
			t.Fatal(err)
		}
		if !result.ProbeReachable || result.Remote != item.target.String() || result.SocketMark != m7OuterMark || result.ProbeLatency < 0 {
			t.Fatalf("marked CandidateProbe %s failed: %+v", item.name, result)
		}
		t.Logf("CandidateProbe %s target=%s mark=%#x latency=%s", item.name, result.Remote, result.SocketMark, time.Duration(result.ProbeLatency))
		runM8ProbeExpect(t, lab, topology.ClientNamespace, "http", item.target, item.iface, 0, false)
	}
	if after := countM8Observations(t, observations); after != before {
		t.Fatalf("ordinary same-destination probes reached WAN: before=%d after=%d", before, after)
	}
	rules := lab.inNamespace(topology.ClientNamespace, "nft", "-j", "list", "table", "inet", "hydra_protect")
	requireCounter(t, rules, "accept-bypass", 2)
	if strings.Contains(rules, topology.WAN14.String()) || strings.Contains(rules, topology.WAN16.String()) {
		t.Fatal("CandidateProbe target appeared in kill-switch policy")
	}

	if err := touch(policyRelease); err != nil {
		t.Fatal(err)
	}
	waitM8Child(t, policy)
	if err := touch(serverRelease); err != nil {
		t.Fatal(err)
	}
	waitM8Child(t, server)
}

type m9FullProvider struct {
	candidates  []exitselector.Candidate
	calls       atomic.Uint64
	unavailable atomic.Bool
}

type m9SequenceProvider struct {
	mu      sync.Mutex
	batches [][]exitselector.Candidate
	calls   atomic.Uint64
}

func (p *m9SequenceProvider) GetCandidates(ctx context.Context, request exitselector.CandidateRequest) (exitselector.CandidateBatch, error) {
	if err := ctx.Err(); err != nil {
		return exitselector.CandidateBatch{}, err
	}
	index := int(p.calls.Add(1) - 1)
	p.mu.Lock()
	var source []exitselector.Candidate
	if index < len(p.batches) {
		source = append([]exitselector.Candidate(nil), p.batches[index]...)
	}
	p.mu.Unlock()
	excluded := make(map[peer.ID]struct{}, len(request.ExcludePeerIDs))
	for _, id := range request.ExcludePeerIDs {
		excluded[id] = struct{}{}
	}
	candidates := make([]exitselector.Candidate, 0, min(request.Limit, len(source)))
	for _, candidate := range source {
		if _, skip := excluded[candidate.PeerID]; skip {
			continue
		}
		candidates = append(candidates, candidate)
		if len(candidates) == request.Limit {
			break
		}
	}
	return exitselector.CandidateBatch{
		Country: request.Country, AvailablePeerCount: len(candidates),
		GeneratedAt: time.Now().UTC(), Candidates: candidates,
	}, nil
}

func (p *m9FullProvider) GetCandidates(ctx context.Context, request exitselector.CandidateRequest) (exitselector.CandidateBatch, error) {
	if err := ctx.Err(); err != nil {
		return exitselector.CandidateBatch{}, err
	}
	p.calls.Add(1)
	if p.unavailable.Load() {
		return exitselector.CandidateBatch{}, errors.New("controlled CandidateProvider outage")
	}
	excluded := make(map[peer.ID]struct{}, len(request.ExcludePeerIDs))
	for _, id := range request.ExcludePeerIDs {
		excluded[id] = struct{}{}
	}
	candidates := make([]exitselector.Candidate, 0, min(request.Limit, len(p.candidates)))
	for _, candidate := range p.candidates {
		if _, skip := excluded[candidate.PeerID]; skip {
			continue
		}
		candidates = append(candidates, candidate)
		if len(candidates) == request.Limit {
			break
		}
	}
	return exitselector.CandidateBatch{
		Country: request.Country, AvailablePeerCount: len(candidates),
		GeneratedAt: time.Now().UTC(), Candidates: candidates,
	}, nil
}

type m9FullProbe struct {
	latencies map[peer.ID]time.Duration
}

func (p m9FullProbe) Probe(ctx context.Context, candidate exitselector.Candidate) (exitselector.ProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return exitselector.ProbeResult{}, err
	}
	return exitselector.ProbeResult{
		Reachable: true, Latency: p.latencies[candidate.PeerID],
		ObservedAt: time.Now().UTC(), NetworkEpoch: candidate.NetworkEpoch,
	}, nil
}

type m9FullConnectResponse struct {
	result exitselector.ConnectResult
	err    error
}

type m9FullConnectCall struct {
	ctx       context.Context
	candidate exitselector.Candidate
	response  chan m9FullConnectResponse
}

type m9FullConnector struct {
	calls chan m9FullConnectCall
}

func (c *m9FullConnector) Connect(ctx context.Context, candidate exitselector.Candidate) (exitselector.ConnectResult, error) {
	call := m9FullConnectCall{ctx: ctx, candidate: candidate, response: make(chan m9FullConnectResponse, 1)}
	select {
	case c.calls <- call:
	case <-ctx.Done():
		return exitselector.ConnectResult{}, ctx.Err()
	}
	select {
	case response := <-call.response:
		return response.result, response.err
	case <-ctx.Done():
		return exitselector.ConnectResult{}, ctx.Err()
	}
}

type m9FullConnection struct {
	events      chan exitselector.ConnectionEvent
	client      connectChild
	closeOnce   sync.Once
	closeResult error
}

func (c *m9FullConnection) Events() <-chan exitselector.ConnectionEvent { return c.events }

func (c *m9FullConnection) Close() error {
	c.closeOnce.Do(func() {
		if err := touch(c.client.releasePath); err != nil {
			c.closeResult = err
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		c.closeResult = c.client.child.wait(ctx)
	})
	return c.closeResult
}

type m9FullPath struct {
	name               string
	lab                *lab
	topology           m7Topology
	exit               connectChild
	exitReady          iceSignalReport
	remote             string
	candidate          exitselector.Candidate
	internet           *childProcess
	internetRelease    string
	direct             *childProcess
	directRelease      string
	directObservations string
}

func TestM9_FullStackPersistentActiveFailureSelectsReserve(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M5B_SIGNALHELPER")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	assertNormalUserExecutable(t, "HYDRA_M8_KILLSWITCH")

	root := newLab(t, "m9-full-failover")
	clientNamespace := root.namespace("client")
	root.inNamespace(clientNamespace, "ip", "link", "set", "lo", "up")
	pathA := startM9FullPath(t, root, clientNamespace, "a", 1)
	pathB := startM9FullPath(t, root, clientNamespace, "b", 2)
	policy, policyRelease, _ := startM8Policy(t, root, clientNamespace, "m9-protection")

	provider := &m9FullProvider{candidates: []exitselector.Candidate{pathA.candidate, pathB.candidate}}
	connector := &m9FullConnector{calls: make(chan m9FullConnectCall, 8)}
	localPeer := newM9PeerID(t)
	config := exitselector.DefaultConfig(
		localPeer, "DE", provider,
		m9FullProbe{latencies: map[peer.ID]time.Duration{pathA.candidate.PeerID: 10 * time.Millisecond, pathB.candidate.PeerID: 20 * time.Millisecond}},
		connector, func() bool { return true },
		exitselector.UnderlayState{NetworkGeneration: networkstate.Generation(1), IPv4Usable: true, IPv6Usable: true},
	)
	config.FastConnectTimeout = 1900 * time.Millisecond
	config.RefreshMinimum = time.Minute
	config.RefreshMaximum = time.Minute
	selector, err := exitselector.New(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := selector.Start(ctx); err != nil {
		t.Fatal(err)
	}

	initial := waitM9FullConnectCall(t, connector.calls, pathA.candidate.PeerID, 5*time.Second)
	connectionA, clientA, resultA := connectM9FullPath(t, root, pathA, "a-client")
	initial.response <- m9FullConnectResponse{result: exitselector.ConnectResult{Outcome: exitselector.OutcomeConnected, Connection: connectionA}}
	waitM9SelectorState(t, selector, exitselector.StateConnected, pathA.candidate.PeerID, 5*time.Second)
	probeA4 := runM7Probe(t, root, clientNamespace, "http", endpointValue(pathA.topology.Internet4, 18080))
	probeA6 := runM7Probe(t, root, clientNamespace, "http", endpointValue(pathA.topology.Internet6, 18080))
	assertM7Probe(t, probeA4, pathA.topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))
	assertM7Probe(t, probeA6, pathA.topology.ExitWAN6, netip.MustParseAddr("fd77::2"))

	beforeDirect := countM8Observations(t, pathA.directObservations)
	leak4, checkpoint4, continue4, result4 := startM8LeakProbe(t, root, clientNamespace, "m9-switch-leak4", pathA.topology.ClientOuterIf, endpointValue(pathA.topology.ExitOuter4, 18081))
	leak6, checkpoint6, continue6, result6 := startM8LeakProbe(t, root, clientNamespace, "m9-switch-leak6", pathA.topology.ClientOuterIf, endpointValue(pathA.topology.ExitOuter6, 18081))
	waitM8File(t, leak4, checkpoint4)
	waitM8File(t, leak6, checkpoint6)

	if err := pathA.exit.child.stop(); err != nil {
		t.Fatal(err)
	}
	failureAt := time.Now()
	connectionA.events <- exitselector.ConnectionEvent{Type: exitselector.ConnectionFailed, At: failureAt}
	recovery := waitM9FullConnectCall(t, connector.calls, pathA.candidate.PeerID, time.Second)
	for _, path := range []string{continue4, continue6} {
		if err := touch(path); err != nil {
			t.Fatal(err)
		}
	}
	waitM8Child(t, leak4)
	waitM8Child(t, leak6)
	assertM9LeakProbe(t, result4)
	assertM9LeakProbe(t, result6)
	if after := countM8Observations(t, pathA.directObservations); after != beforeDirect {
		t.Fatalf("ordinary traffic leaked during Exit recovery: before=%d after=%d", beforeDirect, after)
	}
	select {
	case <-recovery.ctx.Done():
	case <-time.After(2200 * time.Millisecond):
		t.Fatal("ACTIVE recovery exceeded the two-second usable-underlay budget")
	}
	reserve := waitM9FullConnectCall(t, connector.calls, pathB.candidate.PeerID, time.Second)
	connectionB, clientB, resultB := connectM9FullPath(t, root, pathB, "b-client")
	reserve.response <- m9FullConnectResponse{result: exitselector.ConnectResult{Outcome: exitselector.OutcomeConnected, Connection: connectionB}}
	waitM9SelectorState(t, selector, exitselector.StateConnected, pathB.candidate.PeerID, 5*time.Second)
	failoverDuration := time.Since(failureAt)
	probeB4 := runM7Probe(t, root, clientNamespace, "http", endpointValue(pathB.topology.Internet4, 18080))
	probeB6 := runM7Probe(t, root, clientNamespace, "http", endpointValue(pathB.topology.Internet6, 18080))
	assertM7Probe(t, probeB4, pathB.topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))
	assertM7Probe(t, probeB6, pathB.topology.ExitWAN6, netip.MustParseAddr("fd77::2"))
	if probeA4.ObservedSource == probeB4.ObservedSource || probeA6.ObservedSource == probeB6.ObservedSource {
		t.Fatalf("Internet source did not change with Exit: A=%s/%s B=%s/%s", probeA4.ObservedSource, probeA6.ObservedSource, probeB4.ObservedSource, probeB6.ObservedSource)
	}
	if clientA.PeerID == "" || clientB.PeerID == "" || resultA.RemotePeerID != pathA.exitReady.PeerID || resultB.RemotePeerID != pathB.exitReady.PeerID {
		t.Fatalf("authenticated peer binding missing: clientA=%s remoteA=%s clientB=%s remoteB=%s", clientA.PeerID, resultA.RemotePeerID, clientB.PeerID, resultB.RemotePeerID)
	}
	stats := selector.Stats()
	if stats.Failovers != 1 || stats.SamePeerRecoveries != 0 || stats.ConnectLatencyP95 > 2100*time.Millisecond {
		t.Fatalf("unexpected M9 full-stack stats: %+v", stats)
	}
	t.Logf("M9 full-stack A=%s B=%s recovery_budget=%s failover=%s probe_p50=%s probe_p95=%s connect_p50=%s connect_p95=%s source4=%s->%s source6=%s->%s", pathA.exitReady.PeerID, pathB.exitReady.PeerID, config.ActiveRecoveryBudget, failoverDuration, stats.ProbeLatencyP50, stats.ProbeLatencyP95, stats.ConnectLatencyP50, stats.ConnectLatencyP95, probeA4.ObservedSource, probeB4.ObservedSource, probeA6.ObservedSource, probeB6.ObservedSource)

	if err := selector.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []*m9FullPath{pathA, pathB} {
		for _, release := range []string{path.exit.releasePath, path.internetRelease, path.directRelease} {
			if err := touch(release); err != nil {
				t.Fatal(err)
			}
		}
		for _, child := range []*childProcess{path.exit.child, path.internet, path.direct} {
			if child == pathA.exit.child {
				continue
			}
			waitM8Child(t, child)
		}
	}
	if err := touch(policyRelease); err != nil {
		t.Fatal(err)
	}
	waitM8Child(t, policy)
	root.inNamespace(clientNamespace, "test", "!", "-e", "/sys/class/net/hydra0")
}

func TestM9_FullStackUnderlayOutagePreservesActiveExit(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M5B_SIGNALHELPER")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	assertNormalUserExecutable(t, "HYDRA_M8_KILLSWITCH")

	root := newLab(t, "m9-full-underlay")
	clientNamespace := root.namespace("client")
	root.inNamespace(clientNamespace, "ip", "link", "set", "lo", "up")
	pathA := startM9FullPath(t, root, clientNamespace, "a", 1)
	policy, policyRelease, _ := startM8Policy(t, root, clientNamespace, "m9-underlay-protection")
	fallback := m9Candidate(t, newM9PeerID(t), netip.MustParseAddrPort("198.18.250.2:4001"), 2)
	provider := &m9FullProvider{candidates: []exitselector.Candidate{pathA.candidate, fallback}}
	connector := &m9FullConnector{calls: make(chan m9FullConnectCall, 8)}
	config := exitselector.DefaultConfig(
		newM9PeerID(t), "DE", provider,
		m9FullProbe{latencies: map[peer.ID]time.Duration{pathA.candidate.PeerID: 10 * time.Millisecond, fallback.PeerID: 20 * time.Millisecond}},
		connector, func() bool { return true },
		exitselector.UnderlayState{NetworkGeneration: networkstate.Generation(1), IPv4Usable: true, IPv6Usable: true},
	)
	config.FastConnectTimeout = 1900 * time.Millisecond
	config.RefreshMinimum = time.Minute
	config.RefreshMaximum = time.Minute
	selector, err := exitselector.New(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := selector.Start(ctx); err != nil {
		t.Fatal(err)
	}
	initial := waitM9FullConnectCall(t, connector.calls, pathA.candidate.PeerID, 5*time.Second)
	connectionA, _, _ := connectM9FullPath(t, root, pathA, "underlay-a-client")
	initial.response <- m9FullConnectResponse{result: exitselector.ConnectResult{Outcome: exitselector.OutcomeConnected, Connection: connectionA}}
	waitM9SelectorState(t, selector, exitselector.StateConnected, pathA.candidate.PeerID, 5*time.Second)
	assertM7Probe(t, runM7Probe(t, root, clientNamespace, "http", endpointValue(pathA.topology.Internet4, 18080)), pathA.topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))
	waitM9SelectorSnapshot(t, selector, func(snapshot exitselector.Snapshot) bool {
		return provider.calls.Load() >= 2 && snapshot.Reserve != nil && snapshot.Reserve.Candidate.PeerID == fallback.PeerID
	}, 2*time.Second)
	provider.unavailable.Store(true)
	if err := selector.RefreshNow(); err != nil {
		t.Fatal(err)
	}
	waitM9SelectorSnapshot(t, selector, func(snapshot exitselector.Snapshot) bool {
		return snapshot.State == exitselector.StateConnected && !snapshot.ProviderAvailable && snapshot.Active != nil && snapshot.Active.Candidate.PeerID == pathA.candidate.PeerID
	}, 2*time.Second)
	assertM7Probe(t, runM7Probe(t, root, clientNamespace, "http", endpointValue(pathA.topology.Internet4, 18080)), pathA.topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))

	root.installNFT(clientNamespace, "m9-transient-fault", fmt.Sprintf(`table inet hydra_m9_transient {
  chain output {
    type filter hook output priority -250; policy accept;
    oifname %q udp sport %d counter drop
  }
}
`, pathA.topology.ClientOuterIf, clientMuxPort))
	transientStarted := time.Now()
	connectionA.events <- exitselector.ConnectionEvent{Type: exitselector.ConnectionDisconnected, At: transientStarted}
	transientRecovery := waitM9FullConnectCall(t, connector.calls, pathA.candidate.PeerID, time.Second)
	root.inNamespace(clientNamespace, "nft", "delete", "table", "inet", "hydra_m9_transient")
	transientProbe := runM7Probe(t, root, clientNamespace, "http", endpointValue(pathA.topology.Internet4, 18080))
	assertM7Probe(t, transientProbe, pathA.topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))
	transientRecovery.response <- m9FullConnectResponse{result: exitselector.ConnectResult{Outcome: exitselector.OutcomeConnected, Connection: connectionA}}
	waitM9SelectorState(t, selector, exitselector.StateConnected, pathA.candidate.PeerID, 2*time.Second)
	transientDuration := time.Since(transientStarted)
	if transientDuration >= config.ActiveRecoveryBudget {
		t.Fatalf("transient ACTIVE recovery exceeded budget: %s", transientDuration)
	}

	root.inNamespace(clientNamespace, "ip", "link", "set", pathA.topology.ClientOuterIf, "down")
	if err := selector.UpdateUnderlay(exitselector.UnderlayState{NetworkGeneration: networkstate.Generation(1)}); err != nil {
		t.Fatal(err)
	}
	waitM9SelectorSnapshot(t, selector, func(snapshot exitselector.Snapshot) bool {
		return snapshot.State == exitselector.StateWaitingForUnderlay && snapshot.Active != nil && snapshot.Active.Candidate.PeerID == pathA.candidate.PeerID
	}, 2*time.Second)
	requestsBefore := provider.calls.Load()
	timer := time.NewTimer(2200 * time.Millisecond)
	<-timer.C
	if provider.calls.Load() != requestsBefore {
		t.Fatal("underlay outage consumed a provider request")
	}
	select {
	case call := <-connector.calls:
		t.Fatalf("underlay outage consumed fallback %s", call.candidate.PeerID)
	default:
	}
	root.inNamespace(clientNamespace, "ip", "link", "set", pathA.topology.ClientOuterIf, "up")
	if err := selector.UpdateUnderlay(exitselector.UnderlayState{NetworkGeneration: networkstate.Generation(2), IPv4Usable: true, IPv6Usable: true}); err != nil {
		t.Fatal(err)
	}
	recovery := waitM9FullConnectCall(t, connector.calls, pathA.candidate.PeerID, time.Second)
	assertM7Probe(t, runM7Probe(t, root, clientNamespace, "http", endpointValue(pathA.topology.Internet4, 18080)), pathA.topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))
	recovery.response <- m9FullConnectResponse{result: exitselector.ConnectResult{Outcome: exitselector.OutcomeConnected, Connection: connectionA}}
	waitM9SelectorState(t, selector, exitselector.StateConnected, pathA.candidate.PeerID, 3*time.Second)
	if stats := selector.Stats(); stats.UnderlayPauses != 1 || stats.SamePeerRecoveries != 2 || stats.Failovers != 0 || stats.ProviderFailures == 0 {
		t.Fatalf("underlay recovery stats=%+v", stats)
	}
	t.Logf("M9 transient=%s provider-outage=ACTIVE-sticky underlay-outage=%s ACTIVE=%s NetworkGeneration=1->2 RESERVE-unused=%s", transientDuration, 2200*time.Millisecond, pathA.candidate.PeerID, fallback.PeerID)

	if err := selector.Close(); err != nil {
		t.Fatal(err)
	}
	for _, release := range []string{pathA.exit.releasePath, pathA.internetRelease, pathA.directRelease, policyRelease} {
		if err := touch(release); err != nil {
			t.Fatal(err)
		}
	}
	for _, child := range []*childProcess{pathA.exit.child, pathA.internet, pathA.direct, policy} {
		waitM8Child(t, child)
	}
}

func TestM9_FullStackDeadReserveColdAndFreshBatch(t *testing.T) {
	requirePrivileged(t)
	assertNormalUserExecutable(t, "HYDRA_M5B_SIGNALHELPER")
	assertNormalUserExecutable(t, "HYDRA_M7_INTERNET")
	assertNormalUserExecutable(t, "HYDRA_M8_KILLSWITCH")

	root := newLab(t, "m9-full-exhaustion")
	clientNamespace := root.namespace("client")
	root.inNamespace(clientNamespace, "ip", "link", "set", "lo", "up")
	pathA := startM9FullPath(t, root, clientNamespace, "a", 1)
	pathC := startM9FullPath(t, root, clientNamespace, "c", 3)
	pathD := startM9FullPath(t, root, clientNamespace, "d", 4)
	policy, policyRelease, _ := startM8Policy(t, root, clientNamespace, "m9-exhaustion-protection")
	deadB := m9Candidate(t, newM9PeerID(t), netip.MustParseAddrPort("198.18.250.2:4001"), 2)
	provider := &m9SequenceProvider{batches: [][]exitselector.Candidate{
		{pathA.candidate, deadB, pathC.candidate},
		nil,
		{pathD.candidate},
		nil,
	}}
	connector := &m9FullConnector{calls: make(chan m9FullConnectCall, 12)}
	probeLatencies := map[peer.ID]time.Duration{
		pathA.candidate.PeerID: 10 * time.Millisecond,
		deadB.PeerID:           20 * time.Millisecond,
		pathC.candidate.PeerID: 30 * time.Millisecond,
		pathD.candidate.PeerID: 10 * time.Millisecond,
	}
	config := exitselector.DefaultConfig(
		newM9PeerID(t), "DE", provider, m9FullProbe{latencies: probeLatencies}, connector,
		func() bool { return true },
		exitselector.UnderlayState{NetworkGeneration: networkstate.Generation(1), IPv4Usable: true, IPv6Usable: true},
	)
	config.FastConnectTimeout = 1900 * time.Millisecond
	config.RefreshMinimum = time.Minute
	config.RefreshMaximum = time.Minute
	selector, err := exitselector.New(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := selector.Start(ctx); err != nil {
		t.Fatal(err)
	}

	initial := waitM9FullConnectCall(t, connector.calls, pathA.candidate.PeerID, 5*time.Second)
	connectionA, _, _ := connectM9FullPath(t, root, pathA, "exhaust-a-client")
	initial.response <- m9FullConnectResponse{result: exitselector.ConnectResult{Outcome: exitselector.OutcomeConnected, Connection: connectionA}}
	waitM9SelectorState(t, selector, exitselector.StateConnected, pathA.candidate.PeerID, 5*time.Second)
	probeA := runM7Probe(t, root, clientNamespace, "http", endpointValue(pathA.topology.Internet4, 18080))
	assertM7Probe(t, probeA, pathA.topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))

	if err := pathA.exit.child.stop(); err != nil {
		t.Fatal(err)
	}
	connectionA.events <- exitselector.ConnectionEvent{Type: exitselector.ConnectionFailed, At: time.Now().UTC()}
	recoveryA := waitM9FullConnectCall(t, connector.calls, pathA.candidate.PeerID, time.Second)
	waitM9ContextDone(t, recoveryA.ctx, 2200*time.Millisecond, "ACTIVE A recovery")
	reserveB := waitM9FullConnectCall(t, connector.calls, deadB.PeerID, time.Second)
	reserveStarted := time.Now()
	reserveB.response <- m9FullConnectResponse{
		result: exitselector.ConnectResult{Outcome: exitselector.OutcomeUnavailable},
		err:    &exitselector.OutcomeError{Outcome: exitselector.OutcomeUnavailable, Err: errors.New("controlled dead RESERVE")},
	}
	coldC := waitM9FullConnectCall(t, connector.calls, pathC.candidate.PeerID, time.Second)
	if delay := time.Since(reserveStarted); delay >= config.FastConnectTimeout {
		t.Fatalf("dead RESERVE was given an additional recovery window: %s", delay)
	}
	connectionC, _, _ := connectM9FullPath(t, root, pathC, "exhaust-c-client")
	coldC.response <- m9FullConnectResponse{result: exitselector.ConnectResult{Outcome: exitselector.OutcomeConnected, Connection: connectionC}}
	waitM9SelectorState(t, selector, exitselector.StateConnected, pathC.candidate.PeerID, 5*time.Second)
	probeC := runM7Probe(t, root, clientNamespace, "http", endpointValue(pathC.topology.Internet4, 18080))
	assertM7Probe(t, probeC, pathC.topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))
	waitM9SelectorSnapshot(t, selector, func(snapshot exitselector.Snapshot) bool {
		return provider.calls.Load() >= 2 && snapshot.State == exitselector.StateConnected && snapshot.Reserve == nil && snapshot.Cold == nil
	}, 2*time.Second)

	if err := pathC.exit.child.stop(); err != nil {
		t.Fatal(err)
	}
	connectionC.events <- exitselector.ConnectionEvent{Type: exitselector.ConnectionFailed, At: time.Now().UTC()}
	recoveryC := waitM9FullConnectCall(t, connector.calls, pathC.candidate.PeerID, time.Second)
	waitM9ContextDone(t, recoveryC.ctx, 2200*time.Millisecond, "ACTIVE C recovery")
	freshD := waitM9FullConnectCall(t, connector.calls, pathD.candidate.PeerID, 2*time.Second)
	connectionD, _, _ := connectM9FullPath(t, root, pathD, "exhaust-d-client")
	freshD.response <- m9FullConnectResponse{result: exitselector.ConnectResult{Outcome: exitselector.OutcomeConnected, Connection: connectionD}}
	waitM9SelectorState(t, selector, exitselector.StateConnected, pathD.candidate.PeerID, 5*time.Second)
	probeD := runM7Probe(t, root, clientNamespace, "http", endpointValue(pathD.topology.Internet4, 18080))
	assertM7Probe(t, probeD, pathD.topology.ExitWAN4, netip.MustParseAddr("10.77.0.2"))
	if probeA.ObservedSource == probeC.ObservedSource || probeC.ObservedSource == probeD.ObservedSource || probeA.ObservedSource == probeD.ObservedSource {
		t.Fatalf("Internet sources are not distinct: A=%s C=%s D=%s", probeA.ObservedSource, probeC.ObservedSource, probeD.ObservedSource)
	}
	stats := selector.Stats()
	if stats.Failovers != 2 || stats.FreshBatchRequests != 1 || stats.Unavailable < 3 || provider.calls.Load() < 3 {
		t.Fatalf("exhaustion/fresh-batch stats=%+v provider_calls=%d", stats, provider.calls.Load())
	}
	t.Logf("M9 fallback A=%s dead-B=%s C=%s fresh-D=%s provider_requests=%d sources=%s->%s->%s", pathA.candidate.PeerID, deadB.PeerID, pathC.candidate.PeerID, pathD.candidate.PeerID, provider.calls.Load(), probeA.ObservedSource, probeC.ObservedSource, probeD.ObservedSource)

	if err := selector.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []*m9FullPath{pathA, pathC, pathD} {
		for _, release := range []string{path.exit.releasePath, path.internetRelease, path.directRelease} {
			if err := touch(release); err != nil {
				t.Fatal(err)
			}
		}
		for _, child := range []*childProcess{path.internet, path.direct} {
			waitM8Child(t, child)
		}
	}
	waitM8Child(t, pathD.exit.child)
	if err := touch(policyRelease); err != nil {
		t.Fatal(err)
	}
	waitM8Child(t, policy)
}

func startM9FullPath(t *testing.T, owner *lab, clientNamespace, name string, ordinal int) *m9FullPath {
	t.Helper()
	pathLab := newLab(t, "m9-full-path-"+name)
	exitNamespace := owner.namespace("exit-" + name)
	internetNamespace := owner.namespace("internet-" + name)
	suffix := fmt.Sprintf("%x%03x", ordinal, owner.index&0xfff)
	topology := m7Topology{
		ClientNamespace: clientNamespace, ExitNamespace: exitNamespace, InternetNamespace: internetNamespace,
		ClientOuterIf: "hc" + suffix, ExitOuterIf: "he" + suffix, ExitWANIf: "hw" + suffix, InternetIf: "hi" + suffix,
		ClientOuter4: netip.MustParseAddr(fmt.Sprintf("198.18.%d.1", 80+ordinal)),
		ExitOuter4:   netip.MustParseAddr(fmt.Sprintf("198.18.%d.2", 80+ordinal)),
		ClientOuter6: netip.MustParseAddr(fmt.Sprintf("2001:db8:%x::1", 0x80+ordinal)),
		ExitOuter6:   netip.MustParseAddr(fmt.Sprintf("2001:db8:%x::2", 0x80+ordinal)),
		ExitWAN6:     netip.MustParseAddr(fmt.Sprintf("2001:db8:%x::1", 0x90+ordinal)),
		Internet6:    netip.MustParseAddr(fmt.Sprintf("2001:db8:%x::2", 0x90+ordinal)),
	}
	wan4 := [][2]string{{"203.0.113.1", "203.0.113.2"}, {"198.51.100.1", "198.51.100.2"}, {"192.0.2.1", "192.0.2.2"}, {"203.0.114.1", "203.0.114.2"}}
	if ordinal < 1 || ordinal > len(wan4) {
		t.Fatalf("invalid M9 path ordinal %d", ordinal)
	}
	topology.ExitWAN4 = netip.MustParseAddr(wan4[ordinal-1][0])
	topology.Internet4 = netip.MustParseAddr(wan4[ordinal-1][1])
	owner.createVeth(clientNamespace, topology.ClientOuterIf, exitNamespace, topology.ExitOuterIf)
	owner.createVeth(exitNamespace, topology.ExitWANIf, internetNamespace, topology.InternetIf)
	for _, namespace := range []string{exitNamespace, internetNamespace} {
		owner.inNamespace(namespace, "ip", "link", "set", "lo", "up")
	}
	for _, item := range []struct {
		namespace, iface string
		ipv4, ipv6       netip.Addr
	}{
		{clientNamespace, topology.ClientOuterIf, topology.ClientOuter4, topology.ClientOuter6},
		{exitNamespace, topology.ExitOuterIf, topology.ExitOuter4, topology.ExitOuter6},
		{exitNamespace, topology.ExitWANIf, topology.ExitWAN4, topology.ExitWAN6},
		{internetNamespace, topology.InternetIf, topology.Internet4, topology.Internet6},
	} {
		owner.inNamespace(item.namespace, "ip", "link", "set", item.iface, "up")
		owner.inNamespace(item.namespace, "ip", "addr", "add", item.ipv4.String()+"/24", "dev", item.iface)
		owner.inNamespace(item.namespace, "ip", "-6", "addr", "add", item.ipv6.String()+"/64", "dev", item.iface, "nodad")
	}
	internet, internetRelease, _ := startM7InternetServer(t, pathLab, topology)
	direct, directRelease, directObservations := startM8Internet(t, pathLab, "direct-"+name, exitNamespace, topology.ExitOuter4, topology.ExitOuter6)
	exit, exitReady := startM7Peer(t, pathLab, topology, "responder", m9M7Config(), "")
	remote := selectSignalAddress(t, exitReady.ListenAddresses, "ipv4")
	endpoint := m9ControlEndpoint(t, remote)
	exitPeer, err := peer.Decode(exitReady.PeerID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	return &m9FullPath{
		name: name, lab: pathLab, topology: topology, exit: exit, exitReady: exitReady, remote: remote,
		candidate: exitselector.Candidate{
			PeerID: exitPeer, Country: "DE", ControlEndpoint: endpoint,
			ProtocolVersion: protocol.ProtocolVersion, Capabilities: protocol.CapExit, Accepting: true,
			AvailableBandwidthMbps: 100, Uptime: exitselector.UptimeMature,
			NetworkEpoch: uint64(ordinal), GeneratedAt: now, ExpiresAt: now.Add(time.Hour),
		},
		internet: internet, internetRelease: internetRelease,
		direct: direct, directRelease: directRelease, directObservations: directObservations,
	}
}

func connectM9FullPath(t *testing.T, owner *lab, path *m9FullPath, label string) (*m9FullConnection, iceSignalReport, iceSignalReport) {
	t.Helper()
	connectionLab := newLab(t, "m9-full-"+label)
	client, clientReady := startM7Peer(t, connectionLab, path.topology, "initiator", m9M7Config(), path.remote)
	waitDirectAWGReady(t, client, path.exit)
	for _, start := range []string{path.exit.awgStartPath, client.awgStartPath} {
		if err := touch(start); err != nil {
			t.Fatal(err)
		}
	}
	clientResult := waitConnectReport(t, client)
	exitResult := waitConnectReport(t, path.exit)
	assertConnectedReports(t, clientResult, exitResult, true, false)
	assertM7Ownership(t, clientReady, clientResult, path.exitReady, exitResult)
	return &m9FullConnection{events: make(chan exitselector.ConnectionEvent, 8), client: client}, clientReady, clientResult
}

func m9M7Config() struct {
	name                string
	outer4, outer6      bool
	protect4, protect6  bool
	reject4, reject6    bool
	probe4, probe6, dns bool
} {
	return struct {
		name                string
		outer4, outer6      bool
		protect4, protect6  bool
		reject4, reject6    bool
		probe4, probe6, dns bool
	}{name: "m9-full", outer4: true, protect4: true, protect6: true, probe4: true, probe6: true}
}

func newM9PeerID(t *testing.T) peer.ID {
	t.Helper()
	_, publicKey, err := libp2pcrypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func m9ControlEndpoint(t *testing.T, raw string) netip.AddrPort {
	t.Helper()
	address, err := multiaddr.NewMultiaddr(raw)
	if err != nil {
		t.Fatal(err)
	}
	ipText, err := address.ValueForProtocol(multiaddr.P_IP4)
	if err != nil {
		t.Fatal(err)
	}
	portText, err := address.ValueForProtocol(multiaddr.P_TCP)
	if err != nil {
		t.Fatal(err)
	}
	ip, err := netip.ParseAddr(ipText)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		t.Fatalf("invalid candidate control port %q", portText)
	}
	return netip.AddrPortFrom(ip, uint16(port))
}

func m9Candidate(t *testing.T, id peer.ID, endpoint netip.AddrPort, epoch uint64) exitselector.Candidate {
	t.Helper()
	now := time.Now().UTC()
	return exitselector.Candidate{
		PeerID: id, Country: "DE", ControlEndpoint: endpoint,
		ProtocolVersion: protocol.ProtocolVersion, Capabilities: protocol.CapExit, Accepting: true,
		AvailableBandwidthMbps: 100, Uptime: exitselector.UptimeMature,
		NetworkEpoch: epoch, GeneratedAt: now, ExpiresAt: now.Add(time.Hour),
	}
}

func waitM9FullConnectCall(t *testing.T, calls <-chan m9FullConnectCall, expected peer.ID, timeout time.Duration) m9FullConnectCall {
	t.Helper()
	select {
	case call := <-calls:
		if call.candidate.PeerID != expected {
			t.Fatalf("connector candidate=%s want=%s", call.candidate.PeerID, expected)
		}
		return call
	case <-time.After(timeout):
		t.Fatalf("connector did not request %s", expected)
		return m9FullConnectCall{}
	}
}

func waitM9ContextDone(t *testing.T, ctx context.Context, timeout time.Duration, label string) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(timeout):
		t.Fatalf("%s exceeded bounded deadline", label)
	}
}

func waitM9SelectorState(t *testing.T, selector *exitselector.Selector, state exitselector.State, active peer.ID, timeout time.Duration) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot := selector.Current()
		if snapshot.State == state && snapshot.Active != nil && snapshot.Active.Candidate.PeerID == active {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("selector state=%d active=%v want state=%d active=%s", snapshot.State, snapshot.Active, state, active)
		case <-ticker.C:
		}
	}
}

func waitM9SelectorSnapshot(t *testing.T, selector *exitselector.Selector, ready func(exitselector.Snapshot) bool, timeout time.Duration) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot := selector.Current()
		if ready(snapshot) {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("selector snapshot did not reach expected state: %+v", snapshot)
		case <-ticker.C:
		}
	}
}

func assertM9LeakProbe(t *testing.T, path string) {
	t.Helper()
	var result m8ControlReport
	if err := readJSON(path, &result); err != nil {
		t.Fatal(err)
	}
	if result.AttemptedPackets != 64 {
		t.Fatalf("M9 continuous leak probe attempted %d packets", result.AttemptedPackets)
	}
}

var _ exitselector.CandidateProvider = (*m9FullProvider)(nil)
var _ exitselector.CandidateProvider = (*m9SequenceProvider)(nil)
var _ exitselector.CandidateProbe = m9FullProbe{}
var _ exitselector.Connector = (*m9FullConnector)(nil)
var _ exitselector.Connection = (*m9FullConnection)(nil)
