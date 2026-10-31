// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"

	"github.com/Irviodotnet/Irvio/internal/awgbinding"
	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/icesignal"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/Irviodotnet/Irvio/internal/tunnelsignal"
)

type closeRecorder struct {
	mu         sync.Mutex
	closed     bool
	promoted   bool
	err        error
	promoteErr error
}

func (c *closeRecorder) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.err
}

func (c *closeRecorder) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *closeRecorder) Promote(context.Context) error {
	c.mu.Lock()
	c.promoted = true
	err := c.promoteErr
	c.mu.Unlock()
	return err
}

func (c *closeRecorder) isPromoted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.promoted
}

type releaseFailureClient struct {
	err      error
	releases int
}

func (c *releaseFailureClient) Renew(_ context.Context, lease tunnellease.Lease) (tunnellease.Lease, error) {
	return lease, nil
}

func (c *releaseFailureClient) Release(context.Context, tunnellease.Lease) error {
	c.releases++
	return c.err
}

func TestRuntimeConnectionTreatsUnconfirmedReleaseAsBestEffort(t *testing.T) {
	now := time.Now().UTC()
	var id tunnellease.ID
	id[0] = 1
	lease := tunnellease.Lease{
		ID: id, ExitPeer: peer.ID("exit"), ClientPeer: peer.ID("client"), Generation: 1,
		IPv4: netip.MustParsePrefix("10.77.0.2/32"), ValidUntil: now.Add(time.Hour),
	}
	client := &releaseFailureClient{err: errors.New("direct control unavailable")}
	config := tunnellease.DefaultRenewerConfig(client, lease)
	config.ReleaseOnClose = true
	config.LeadTime = time.Minute
	config.RetryDelay = time.Second
	config.RequestTimeout = 50 * time.Millisecond
	renewer, err := tunnellease.NewRenewer(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := renewer.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	resource := new(closeRecorder)
	connection := newRuntimeConnection(
		nil, make(chan ice.ConnectionState), resource, renewer,
		slog.New(slog.NewTextHandler(io.Discard, nil)), peer.ID("exit"), nil,
		exitselector.ConnectionDiagnostics{},
	)
	if err := connection.Close(); err != nil {
		t.Fatalf("unconfirmed remote RELEASE blocked local teardown: %v", err)
	}
	if client.releases != 1 {
		t.Fatalf("release attempts = %d, want 1", client.releases)
	}
	if !resource.isClosed() {
		t.Fatal("local Data Plane resource remained installed")
	}
}

func TestRuntimeConnectionKeepsLocalTeardownFailureFatal(t *testing.T) {
	now := time.Now().UTC()
	var id tunnellease.ID
	id[0] = 2
	lease := tunnellease.Lease{
		ID: id, ExitPeer: peer.ID("exit"), ClientPeer: peer.ID("client"), Generation: 1,
		IPv4: netip.MustParsePrefix("10.77.0.3/32"), ValidUntil: now.Add(time.Hour),
	}
	client := new(releaseFailureClient)
	config := tunnellease.DefaultRenewerConfig(client, lease)
	config.ReleaseOnClose = true
	config.LeadTime = time.Minute
	config.RetryDelay = time.Second
	config.RequestTimeout = 50 * time.Millisecond
	renewer, err := tunnellease.NewRenewer(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := renewer.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	localErr := errors.New("local Data Plane cleanup failed")
	resource := &closeRecorder{err: localErr}
	connection := newRuntimeConnection(
		nil, make(chan ice.ConnectionState), resource, renewer,
		slog.New(slog.NewTextHandler(io.Discard, nil)), peer.ID("exit"), nil,
		exitselector.ConnectionDiagnostics{},
	)
	if err := connection.Close(); !errors.Is(err, localErr) {
		t.Fatalf("Close error = %v, want local cleanup error", err)
	}
	if client.releases != 1 {
		t.Fatalf("release attempts = %d, want 1", client.releases)
	}
	if !resource.isClosed() {
		t.Fatal("local Data Plane resource close was not attempted")
	}
}

func TestRuntimeConnectionClassifiesAndLogsLocalPromotionFailure(t *testing.T) {
	failure := errors.New("configure tunnel DNS servers: resolvectl operation=dns interface=hydra0: exit status 1")
	resource := &closeRecorder{promoteErr: localPromotionFailure("tunnel_dns_activate", failure)}
	var output bytes.Buffer
	connection := &runtimeConnection{
		resource:   resource,
		logger:     slog.New(slog.NewJSONHandler(&output, nil)),
		remotePeer: peer.ID("fixture-exit"),
		diagnostics: exitselector.ConnectionDiagnostics{
			NetworkGeneration: 7, SessionGeneration: 11,
			SelectedEndpoint: netip.MustParseAddrPort("198.51.100.10:51820"),
		},
	}
	err := connection.Promote(context.Background())
	var outcome *exitselector.OutcomeError
	if !errors.As(err, &outcome) || outcome.Outcome != exitselector.OutcomeLocalPromotionFailed || !errors.Is(err, failure) {
		t.Fatalf("promotion error = %#v", err)
	}
	logLine := output.String()
	for _, want := range []string{
		"CLIENT_DATA_PLANE_PROMOTION_FAILED", `"failure_domain":"local"`,
		`"session_generation":11`, `"transport_generation":7`, "configure tunnel DNS servers",
	} {
		if !strings.Contains(logLine, want) {
			t.Fatalf("promotion log %q missing %q", logLine, want)
		}
	}
}

func TestRuntimeConnectionDoesNotClassifyUnmarkedPromotionFailureAsLocal(t *testing.T) {
	failure := errors.New("controlled session-control BIND failure")
	connection := &runtimeConnection{
		resource:   &closeRecorder{promoteErr: failure},
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		remotePeer: peer.ID("fixture-exit"),
	}
	err := connection.Promote(context.Background())
	var outcome *exitselector.OutcomeError
	if !errors.As(err, &outcome) || outcome.Outcome != exitselector.OutcomeFailed || !errors.Is(err, failure) {
		t.Fatalf("unmarked promotion error was misclassified: %#v", err)
	}
}

func TestRuntimeConnectionRepeatedLocalPromotionFailureReleasesLeases(t *testing.T) {
	if runtime.GOOS == "windows" {
		previous := runtime.GOMAXPROCS(1)
		t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	}
	client := new(releaseFailureClient)
	cycle := func(index int) {
		t.Helper()
		var id tunnellease.ID
		id[0] = byte(index + 1)
		lease := tunnellease.Lease{
			ID: id, ExitPeer: peer.ID("exit"), ClientPeer: peer.ID("client"), Generation: uint64(index + 1),
			IPv4: netip.MustParsePrefix("10.77.0.2/32"), ValidUntil: time.Now().Add(time.Hour),
		}
		config := tunnellease.DefaultRenewerConfig(client, lease)
		config.ReleaseOnClose = true
		config.LeadTime = time.Minute
		config.RetryDelay = time.Second
		config.RequestTimeout = 50 * time.Millisecond
		renewer, err := tunnellease.NewRenewer(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := renewer.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		resource := &closeRecorder{promoteErr: localPromotionFailure("tunnel_dns_activate", errors.New("controlled local activation failure"))}
		connection := newRuntimeConnection(
			nil, make(chan ice.ConnectionState), resource, renewer,
			slog.New(slog.NewTextHandler(io.Discard, nil)), peer.ID("exit"), nil,
			exitselector.ConnectionDiagnostics{NetworkGeneration: 1, SessionGeneration: uint64(index + 1)},
		)
		var outcome *exitselector.OutcomeError
		if err := connection.Promote(context.Background()); !errors.As(err, &outcome) || outcome.Outcome != exitselector.OutcomeLocalPromotionFailed {
			t.Fatalf("attempt %d promotion error = %v", index+1, err)
		}
		if err := connection.Close(); err != nil {
			t.Fatalf("attempt %d close error = %v", index+1, err)
		}
		if !resource.isClosed() {
			t.Fatalf("attempt %d left local resource open", index+1)
		}
	}
	// Warm Go's worker/event cache before the sequential resource measurement, without changing its limits.
	for index := range 20 {
		cycle(index)
	}
	client.releases = 0
	runtime.GC()
	baselineHandles := runtimeResourceCount(t)
	baselineGoroutines := runtime.NumGoroutine()
	for index := range 100 {
		cycle(index)
	}
	if client.releases != 100 {
		t.Fatalf("lease releases = %d, want 100", client.releases)
	}
	runtime.GC()
	afterHandles := runtimeResourceCount(t)
	if afterHandles > baselineHandles+2 {
		t.Fatalf("OS resources grew from %d to %d", baselineHandles, afterHandles)
	}
	afterGoroutines := runtime.NumGoroutine()
	if afterGoroutines > baselineGoroutines+2 {
		t.Fatalf("goroutines grew from %d to %d", baselineGoroutines, afterGoroutines)
	}
	t.Logf("100 local promotion failures: OS_resources=%d->%d goroutines=%d->%d leases_released=%d", baselineHandles, afterHandles, baselineGoroutines, afterGoroutines, client.releases)
}

func TestClientConnectorRunsAuthenticatedICESessionLeaseChain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	clientHost := newRuntimeHost(t)
	exitHost := newRuntimeHost(t)
	if err := clientHost.Connect(ctx, peer.AddrInfo{ID: exitHost.ID(), Addrs: exitHost.Addrs()}); err != nil {
		t.Fatal(err)
	}
	clientGeneration := newRuntimeGeneration(t)
	exitGeneration := newRuntimeGeneration(t)

	validation := icesession.DefaultValidationConfig()
	validation.AllowLoopback = true
	signalConfig := icesignal.DefaultConfig()
	signalConfig.Validation = validation
	signalConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	service, err := icesignal.NewService(
		exitHost, exitHost.ID(),
		icesignal.SessionFactoryFunc(func(ctx context.Context, _ peer.ID, _ icesession.Description) (*icesession.PreparedSession, error) {
			return exitGeneration.NewPreparedSession(ctx)
		}),
		icesignal.AuthorizerFunc(func(_ context.Context, remote peer.ID, purpose icesession.SessionPurpose) error {
			if remote != clientHost.ID() || purpose != icesession.PurposeExit {
				return errors.New("unauthorized")
			}
			return nil
		}),
		signalConfig,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	signalClient, err := icesignal.NewClient(clientHost, signalConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = signalClient.Close() })

	allocatorConfig := tunnellease.DefaultConfig(exitHost.ID())
	allocatorConfig.IPv4Pool = netip.MustParsePrefix("10.77.0.0/29")
	allocatorConfig.TTL = time.Minute
	allocatorConfig.Random = rand.Reader
	allocator, err := tunnellease.New(allocatorConfig)
	if err != nil {
		t.Fatal(err)
	}
	revoked := make(chan tunnellease.Lease, 1)
	lifecycleConfig := tunnellease.DefaultLifecycleConfig(
		allocator,
		tunnellease.ResourceRevokerFunc(func(_ context.Context, lease tunnellease.Lease, _ tunnellease.RevocationReason) error {
			revoked <- lease
			return nil
		}),
	)
	lifecycleConfig.SweepInterval = 10 * time.Millisecond
	lifecycle, err := tunnellease.NewLifecycle(lifecycleConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lifecycle.Close(context.Background()) })
	leaseService, err := tunnelsignal.NewLifecycleService(exitHost, lifecycle, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = leaseService.Close() })
	leaseClient, err := tunnelsignal.NewClient(clientHost, time.Second)
	if err != nil {
		t.Fatal(err)
	}

	private := exitHost.Peerstore().PrivKey(exitHost.ID())
	now := time.Now().UTC()
	directory := &bindingDirectory{envelope: signedBinding(t, private, exitHost.ID(), 3, 31, now)}
	registry := awgbinding.NewRegistry()
	installed := make(chan InstallRequest, 1)
	resource := new(closeRecorder)
	connector, err := NewClientConnector(ClientConnectorConfig{
		LocalPeer: clientHost.ID(), Generation: func() *transportgen.Generation { return clientGeneration },
		Signaling: signalClient, Leases: leaseClient,
		Bindings: BindingResolver{Directory: directory, Registry: registry, Now: time.Now},
		Installer: SessionInstallerFunc(func(_ context.Context, request InstallRequest) (io.Closer, error) {
			installed <- request
			return resource, nil
		}),
		Validation: validation, RenewalLead: 20 * time.Second,
		RenewalRetry: 10 * time.Millisecond, RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	exitConnected := make(chan *iceconnect.ConnectedSession, 1)
	exitErr := make(chan error, 1)
	go acceptRuntimeICE(ctx, service, exitHost.ID(), clientHost.ID(), validation, exitConnected, exitErr)
	result, err := connector.Connect(ctx, exitselector.Candidate{PeerID: exitHost.ID()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != exitselector.OutcomeConnected || result.Connection == nil {
		t.Fatalf("unexpected connector result: %+v", result)
	}
	if resource.isPromoted() {
		t.Fatal("connector published staged resource before selector promotion")
	}
	promotable, ok := result.Connection.(exitselector.PromotableConnection)
	if !ok {
		t.Fatal("production connection is not generation-fenced")
	}
	if err := promotable.Promote(ctx); err != nil {
		t.Fatal(err)
	}
	if !resource.isPromoted() {
		t.Fatal("selector promotion did not publish staged resource")
	}
	var request InstallRequest
	select {
	case request = <-installed:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if request.RemotePeer != exitHost.ID() || request.RemoteAWGKeyEpoch != 3 ||
		request.SelectedEndpoint.RemotePeer != exitHost.ID() ||
		request.SelectedEndpoint.Address.Port() != exitGeneration.Mux().Port() ||
		request.Lease.ClientPeer != clientHost.ID() || request.Lease.ExitPeer != exitHost.ID() {
		t.Fatalf("installer received an unbound session: %+v", request)
	}
	diagnostics, ok := result.Connection.(exitselector.DiagnosableConnection)
	if !ok {
		t.Fatal("production connection has no redacted diagnostics")
	}
	detail := diagnostics.Diagnostics()
	if detail.NetworkGeneration != clientGeneration.ID() || detail.SessionGeneration == 0 ||
		detail.TunnelLeaseID != request.Lease.ID.String() || detail.TunnelLeaseValidUntil != request.Lease.ValidUntil ||
		detail.AWGKeyEpoch != request.RemoteAWGKeyEpoch {
		t.Fatalf("connection diagnostics=%+v", detail)
	}
	if current, ok := registry.Current(exitHost.ID()); !ok ||
		current.PublicKey != request.RemoteAWGKey || current.KeyEpoch != request.RemoteAWGKeyEpoch {
		t.Fatal("installed AWG key did not come from the authenticated binding registry")
	}

	var remote *iceconnect.ConnectedSession
	select {
	case remote = <-exitConnected:
	case err := <-exitErr:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if remote.Result().RemotePeer != clientHost.ID() ||
		request.ICE.Result().SelectedRemoteCandidate.Type != remote.Result().SelectedLocalCandidate.Type {
		t.Fatal("both authenticated ICE sides did not establish the direct selected pair")
	}
	if err := result.Connection.Close(); err != nil {
		t.Fatal(err)
	}
	if !resource.isClosed() {
		t.Fatal("session installer resource was not closed")
	}
	select {
	case lease := <-revoked:
		if lease.ID != request.Lease.ID {
			t.Fatal("connector released a different TunnelLease")
		}
	case <-ctx.Done():
		t.Fatal("authenticated TunnelLease was not revoked on connection close")
	}
	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}
}

func newRuntimeHost(t *testing.T) host.Host {
	t.Helper()
	value, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"),
		libp2p.DisableRelay(),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = value.Close() })
	return value
}

func newRuntimeGeneration(t *testing.T) *transportgen.Generation {
	t.Helper()
	planner := transportgen.BindPlannerFunc(func(networkstate.Snapshot) (transportgen.BindPlan, error) {
		return transportgen.BindPlan{IPv4: netip.MustParseAddr("127.0.0.1")}, nil
	})
	config := transportgen.DefaultConfig(planner)
	config.Validation.AllowLoopback = true
	config.GatherTimeout = time.Second
	config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	builder, err := transportgen.NewBuilder(config)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := builder.Build(context.Background(), networkstate.State{Generation: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = generation.Close() })
	return generation
}

func acceptRuntimeICE(
	ctx context.Context,
	service *icesignal.Service,
	localPeer peer.ID,
	remotePeer peer.ID,
	validation icesession.ValidationConfig,
	connected chan<- *iceconnect.ConnectedSession,
	failed chan<- error,
) {
	accepted, err := service.NextAccepted(ctx)
	if err != nil {
		failed <- err
		return
	}
	attempt, err := iceconnect.New(accepted.Local, iceconnect.Binding{
		SessionID: accepted.SessionID, Initiator: accepted.Initiator, Responder: accepted.Responder,
		LocalPeer: localPeer, RemotePeer: remotePeer, Purpose: accepted.Purpose,
		LocalRole: accepted.LocalRole, RemoteRole: accepted.RemoteRole,
	}, iceconnect.RemoteState{
		SessionID: accepted.SessionID, PeerID: remotePeer,
		Role: accepted.RemoteRole, Description: accepted.RemoteDescription,
	}, iceConnectConfig(validation))
	if err != nil {
		_ = accepted.Local.Close()
		failed <- err
		return
	}
	if err := attempt.LoadRemoteCandidates(ctx); err != nil {
		_ = attempt.Close()
		failed <- err
		return
	}
	session, err := attempt.Connect(ctx)
	if err != nil {
		_ = attempt.Close()
		failed <- err
		return
	}
	connected <- session
}
