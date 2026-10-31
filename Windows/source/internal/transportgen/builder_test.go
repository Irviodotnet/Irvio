// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package transportgen

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/stunprobe"
)

func TestPreferredBindPlannerUsesNormalizedDefaultRoutes(t *testing.T) {
	t.Parallel()
	snapshot, err := networkstate.Normalize(
		[]networkstate.Route{
			{Family: networkstate.FamilyIPv4, LinkIndex: 8, Priority: 20, Gateway: netip.MustParseAddr("198.51.100.1")},
			{Family: networkstate.FamilyIPv4, LinkIndex: 7, Priority: 10, Gateway: netip.MustParseAddr("192.0.2.1")},
			{Family: networkstate.FamilyIPv6, LinkIndex: 9, Priority: 10, Gateway: netip.MustParseAddr("2001:db8:2::1")},
		},
		[]networkstate.Link{
			{Index: 8, Name: "backup", Up: true, MTU: 1500, Addresses: []netip.Prefix{netip.MustParsePrefix("198.51.100.2/24")}},
			{Index: 7, Name: "primary", Up: true, MTU: 1500, Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.2/24")}},
			{Index: 9, Name: "v6", Up: true, MTU: 1480, Addresses: []netip.Prefix{netip.MustParsePrefix("2001:db8:2::2/64")}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := (PreferredBindPlanner{}).Plan(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if plan.IPv4 != netip.MustParseAddr("192.0.2.2") || plan.IPv6 != netip.MustParseAddr("2001:db8:2::2") {
		t.Fatalf("unexpected preferred bind plan: %+v", plan)
	}
}

func TestPreferredBindPlannerUsesRoutePreferredSource(t *testing.T) {
	t.Parallel()
	snapshot, err := networkstate.Normalize(
		[]networkstate.Route{{
			Family: networkstate.FamilyIPv4, LinkIndex: 7, Gateway: netip.MustParseAddr("192.0.2.1"),
			PreferredSource: netip.MustParseAddr("192.0.2.3"),
		}},
		[]networkstate.Link{{
			Index: 7, Name: "wan", Up: true, MTU: 1500,
			Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.2/24"), netip.MustParsePrefix("192.0.2.3/24")},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := (PreferredBindPlanner{}).Plan(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if plan.IPv4 != netip.MustParseAddr("192.0.2.3") {
		t.Fatalf("preferred-source bind plan=%+v", plan)
	}
}

func TestGenerationOwnsSharedEngineAndFencesStaleSessions(t *testing.T) {
	builder := loopbackBuilder(t, nil)
	generation, err := builder.Build(context.Background(), loopbackState(7))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = generation.Close() })
	session, err := generation.NewPreparedSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if session.TransportGeneration() != 7 || generation.Engine().Stats().ActiveSessions != 1 {
		t.Fatalf("session was not generation-bound: session=%d stats=%+v", session.TransportGeneration(), generation.Engine().Stats())
	}
	if !generation.MarkStale() || generation.MarkStale() {
		t.Fatal("stale transition was not one-way")
	}
	if _, err := generation.NewPreparedSession(context.Background()); !errors.Is(err, ErrStale) {
		t.Fatalf("new session on stale generation error = %v", err)
	}
	if _, _, err := session.Agent().GetLocalUserCredentials(); err != nil {
		t.Fatalf("stale handover prematurely killed existing session: %v", err)
	}
	local, _ := generation.Mux().LocalAddr(socketmux.IPv4)
	if err := generation.Close(); err != nil {
		t.Fatal(err)
	}
	if generation.Lifecycle() != LifecycleClosed || generation.Engine().Stats().ActiveSessions != 0 {
		t.Fatalf("generation did not close cleanly: lifecycle=%d stats=%+v", generation.Lifecycle(), generation.Engine().Stats())
	}
	rebound, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(local))
	if err != nil {
		t.Fatalf("closed generation retained its physical socket: %v", err)
	}
	_ = rebound.Close()
}

func TestGenerationBindsSTUNObservationToSameMux(t *testing.T) {
	server, serverDone := startBindingServer(t)
	builder := loopbackBuilder(t, map[socketmux.AddressFamily]netip.AddrPort{socketmux.IPv4: server.LocalAddr().(*net.UDPAddr).AddrPort()})
	generation, err := builder.Build(context.Background(), loopbackState(11))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = generation.Close() })
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	observations := generation.Observations()
	if len(observations) != 1 {
		t.Fatalf("observation count = %d", len(observations))
	}
	observation := observations[0]
	if observation.NetworkGeneration != 11 || observation.MuxPort != generation.Mux().Port() ||
		observation.LocalEndpoint.Port() != generation.Mux().Port() || observation.MappedEndpoint != observation.LocalEndpoint {
		t.Fatalf("observation crossed transport ownership: %+v", observation)
	}
	session, err := generation.NewPreparedSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(session.LocalDescription().Candidates) != 1 {
		t.Fatal("direct STUN result was not suppressed as duplicate srflx")
	}
}

func TestGenerationSTUNPoolFallsBackAndRefreshes(t *testing.T) {
	dead, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer dead.Close()
	server, mappedPort, requests := startMutableBindingServer(t, 51011)
	config := loopbackConfig(nil)
	config.STUNServerPools = map[socketmux.AddressFamily][]netip.AddrPort{
		socketmux.IPv4: {dead.LocalAddr().(*net.UDPAddr).AddrPort(), server.LocalAddr().(*net.UDPAddr).AddrPort()},
	}
	builder, err := NewBuilder(config)
	if err != nil {
		t.Fatal(err)
	}
	// A caller must not be able to mutate the frozen generation's seed pool.
	config.STUNServerPools[socketmux.IPv4][1] = dead.LocalAddr().(*net.UDPAddr).AddrPort()
	generation, err := builder.Build(context.Background(), loopbackState(16))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = generation.Close() })
	if got := generation.Observations()[0].MappedEndpoint.Port(); got != 51011 {
		t.Fatalf("initial mapped port = %d", got)
	}
	mappedPort.Store(53011)
	session, err := generation.NewPreparedSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if got := srflxEndpoint(session.LocalDescription()); got != netip.MustParseAddrPort("198.51.100.10:53011") {
		t.Fatalf("refresh through backup STUN = %s", got)
	}
	if requests.Load() < 2 {
		t.Fatalf("successful fallback STUN requests = %d, want at least 2", requests.Load())
	}
}

func TestGenerationSTUNPoolReachesThirdSeedWithinStartupBudget(t *testing.T) {
	deadA, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer deadA.Close()
	deadB, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer deadB.Close()
	live, _, requests := startMutableBindingServer(t, 51011)
	config := loopbackConfig(nil)
	config.STUN = stunprobe.DefaultConfig()
	config.STUNServerPools = map[socketmux.AddressFamily][]netip.AddrPort{
		socketmux.IPv4: {
			deadA.LocalAddr().(*net.UDPAddr).AddrPort(),
			deadB.LocalAddr().(*net.UDPAddr).AddrPort(),
			live.LocalAddr().(*net.UDPAddr).AddrPort(),
		},
	}
	builder, err := NewBuilder(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	generation, err := builder.Build(ctx, loopbackState(17))
	if err != nil {
		t.Fatalf("healthy third STUN seed was not attempted within startup budget: %v", err)
	}
	defer generation.Close()
	if requests.Load() == 0 || generation.Observations()[0].MappedEndpoint.Port() != 51011 {
		t.Fatalf("healthy third STUN seed was not observed: requests=%d observations=%+v", requests.Load(), generation.Observations())
	}
}

func TestGenerationRejectsSTUNPoolBeyondTransactionCapacity(t *testing.T) {
	config := loopbackConfig(nil)
	config.STUNRegistry.MaxInFlight = 1
	config.STUNServerPools = map[socketmux.AddressFamily][]netip.AddrPort{
		socketmux.IPv4: {netip.MustParseAddrPort("127.0.0.1:3478"), netip.MustParseAddrPort("127.0.0.2:3478")},
	}
	if _, err := NewBuilder(config); err == nil {
		t.Fatal("oversized parallel STUN pool accepted")
	}
}

func TestGenerationRefreshesSRFLXForEachPreparedSession(t *testing.T) {
	server, mappedPort, requests := startMutableBindingServer(t, 51011)
	builder := loopbackBuilder(t, map[socketmux.AddressFamily]netip.AddrPort{
		socketmux.IPv4: server.LocalAddr().(*net.UDPAddr).AddrPort(),
	})
	generation, err := builder.Build(context.Background(), loopbackState(12))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = generation.Close() })

	mappedPort.Store(53011)
	session, err := generation.NewPreparedSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	want := netip.MustParseAddrPort("198.51.100.10:53011")
	got := srflxEndpoint(session.LocalDescription())
	if got != want {
		t.Fatalf("new ICE attempt reused stale SRFLX: got %s want %s (STUN Binding requests=%d)", got, want, requests.Load())
	}
	if requests.Load() < 2 {
		t.Fatalf("new ICE attempt did not perform a fresh STUN Binding request: requests=%d", requests.Load())
	}
}

func TestGenerationRefreshesSRFLXAcrossRepeatedSessions(t *testing.T) {
	server, mappedPort, requests := startMutableBindingServer(t, 50011)
	builder := loopbackBuilder(t, map[socketmux.AddressFamily]netip.AddrPort{
		socketmux.IPv4: server.LocalAddr().(*net.UDPAddr).AddrPort(),
	})
	generation, err := builder.Build(context.Background(), loopbackState(13))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = generation.Close() })

	const cycles = 100
	for cycle := uint32(0); cycle < cycles; cycle++ {
		port := uint32(51011) + cycle
		mappedPort.Store(port)
		session, prepareErr := generation.NewPreparedSession(context.Background())
		if prepareErr != nil {
			t.Fatalf("cycle %d prepare: %v", cycle, prepareErr)
		}
		want := netip.AddrPortFrom(netip.MustParseAddr("198.51.100.10"), uint16(port))
		if got := srflxEndpoint(session.LocalDescription()); got != want {
			_ = session.Close()
			t.Fatalf("cycle %d SRFLX=%s, want %s", cycle, got, want)
		}
		if closeErr := session.Close(); closeErr != nil {
			t.Fatalf("cycle %d close: %v", cycle, closeErr)
		}
	}
	if got, want := requests.Load(), uint64(cycles+1); got != want {
		t.Fatalf("STUN Binding requests=%d, want %d", got, want)
	}
	if stats := generation.Engine().Stats(); stats.SessionsCreated != cycles || stats.SessionsClosed != cycles || stats.ActiveSessions != 0 {
		t.Fatalf("session lifecycle after repeated remaps: %+v", stats)
	}
}

func TestGenerationFencesLateSRFLXResponseFromCanceledAttempt(t *testing.T) {
	server := startControlledBindingServer(t, 50011)
	builder := loopbackBuilder(t, map[socketmux.AddressFamily]netip.AddrPort{
		socketmux.IPv4: server.conn.LocalAddr().(*net.UDPAddr).AddrPort(),
	})
	generation, err := builder.Build(context.Background(), loopbackState(14))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = generation.Close() })

	oldContext, cancelOld := context.WithCancel(context.Background())
	oldResult := make(chan error, 1)
	go func() {
		_, prepareErr := generation.NewPreparedSession(oldContext)
		oldResult <- prepareErr
	}()
	oldRequest := server.nextRequest(t)
	cancelOld()
	if prepareErr := <-oldResult; !errors.Is(prepareErr, context.Canceled) {
		t.Fatalf("canceled old prepare error = %v", prepareErr)
	}

	newResult := make(chan struct {
		session *icesession.PreparedSession
		err     error
	}, 1)
	go func() {
		session, prepareErr := generation.NewPreparedSession(context.Background())
		newResult <- struct {
			session *icesession.PreparedSession
			err     error
		}{session: session, err: prepareErr}
	}()
	newRequest := server.nextRequest(t)
	if err := server.respond(newRequest, 53011); err != nil {
		t.Fatal(err)
	}
	result := <-newResult
	if result.err != nil {
		t.Fatal(result.err)
	}
	t.Cleanup(func() { _ = result.session.Close() })
	if err := server.respond(oldRequest, 51011); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if got, want := srflxEndpoint(result.session.LocalDescription()), netip.MustParseAddrPort("198.51.100.10:53011"); got != want {
		t.Fatalf("late old STUN response rolled back fresh SRFLX: got %s want %s", got, want)
	}
}

func TestGenerationSnapshotsInputs(t *testing.T) {
	servers := map[socketmux.AddressFamily]netip.AddrPort{}
	config := loopbackConfig(servers)
	builder, err := NewBuilder(config)
	if err != nil {
		t.Fatal(err)
	}
	servers[socketmux.IPv4] = netip.MustParseAddrPort("127.0.0.1:9")
	state := loopbackState(3)
	generation, err := builder.Build(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	defer generation.Close()
	state.Snapshot.Links[0].MTU = 1
	if generation.Snapshot().Links[0].MTU != 65536 {
		t.Fatal("generation retained mutable snapshot storage")
	}
}

func TestGenerationOwnsOneDataPlaneAndClosesItBeforeMux(t *testing.T) {
	generation, err := loopbackBuilder(t, nil).Build(context.Background(), loopbackState(19))
	if err != nil {
		t.Fatal(err)
	}
	var closed atomic.Bool
	dataPlane, err := generation.InstallDataPlane(func(ctx context.Context, mux *socketmux.Mux, id networkstate.Generation) (io.Closer, error) {
		if ctx.Err() != nil || mux != generation.Mux() || id != 19 {
			t.Fatal("data-plane factory received incorrect generation ownership")
		}
		return closeFunc(func() error {
			if _, ok := mux.LocalAddr(socketmux.IPv4); !ok {
				t.Error("mux closed before generation data plane")
			}
			closed.Store(true)
			return nil
		}), nil
	})
	if err != nil || dataPlane == nil || generation.DataPlane() == nil {
		t.Fatalf("install data plane: value=%v err=%v", dataPlane, err)
	}
	if _, err := generation.InstallDataPlane(func(context.Context, *socketmux.Mux, networkstate.Generation) (io.Closer, error) {
		return closeFunc(func() error { return nil }), nil
	}); !errors.Is(err, ErrDataPlaneExists) {
		t.Fatalf("second data plane error = %v", err)
	}
	if err := generation.Close(); err != nil {
		t.Fatal(err)
	}
	if !closed.Load() || generation.DataPlane() != nil {
		t.Fatal("generation did not close and release its data plane")
	}
}

func TestGenerationFencesDataPlaneFinishingAfterStale(t *testing.T) {
	generation, err := loopbackBuilder(t, nil).Build(context.Background(), loopbackState(23))
	if err != nil {
		t.Fatal(err)
	}
	defer generation.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	var closed atomic.Bool
	done := make(chan error, 1)
	go func() {
		_, installErr := generation.InstallDataPlane(func(ctx context.Context, _ *socketmux.Mux, _ networkstate.Generation) (io.Closer, error) {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return closeFunc(func() error { closed.Store(true); return nil }), nil
		})
		done <- installErr
	}()
	<-started
	if !generation.MarkStale() {
		t.Fatal("generation did not become stale")
	}
	close(release)
	if err := <-done; !errors.Is(err, ErrStale) {
		t.Fatalf("late data plane error = %v", err)
	}
	if !closed.Load() || generation.DataPlane() != nil {
		t.Fatal("late stale data plane was retained")
	}
}

type closeFunc func() error

func (f closeFunc) Close() error { return f() }

func loopbackBuilder(t *testing.T, servers map[socketmux.AddressFamily]netip.AddrPort) *Builder {
	t.Helper()
	builder, err := NewBuilder(loopbackConfig(servers))
	if err != nil {
		t.Fatal(err)
	}
	return builder
}

func loopbackConfig(servers map[socketmux.AddressFamily]netip.AddrPort) Config {
	config := DefaultConfig(BindPlannerFunc(func(networkstate.Snapshot) (BindPlan, error) {
		return BindPlan{IPv4: netip.MustParseAddr("127.0.0.1")}, nil
	}))
	config.STUNServers = servers
	config.Validation.AllowLoopback = true
	config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	config.STUN.InitialRTO = 25 * time.Millisecond
	config.STUN.MaxRTO = 50 * time.Millisecond
	config.STUN.MaxAttempts = 2
	return config
}

func loopbackState(generation networkstate.Generation) networkstate.State {
	return networkstate.State{
		Generation: generation,
		Snapshot: networkstate.Snapshot{
			Routes: []networkstate.Route{{Family: networkstate.FamilyIPv4, LinkIndex: 1}},
			Links:  []networkstate.Link{{Index: 1, Name: "lo", Up: true, MTU: 65536, Addresses: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8")}}},
		},
	}
}

func startBindingServer(t *testing.T) (*net.UDPConn, <-chan error) {
	t.Helper()
	server, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	done := make(chan error, 1)
	go func() {
		buffer := make([]byte, 2048)
		var first sync.Once
		for {
			count, source, readErr := server.ReadFromUDPAddrPort(buffer)
			if readErr != nil {
				first.Do(func() { done <- readErr })
				return
			}
			request := stun.NewWithOptions(stun.WithStrict(true))
			if err := request.UnmarshalBinary(buffer[:count]); err != nil {
				first.Do(func() { done <- err })
				return
			}
			response := stun.MustBuild(stun.BindingSuccess)
			response.TransactionID = request.TransactionID
			response.WriteTransactionID()
			mapped := stun.XORMappedAddress{IP: net.IP(source.Addr().AsSlice()), Port: int(source.Port())}
			if err := mapped.AddTo(response); err != nil {
				first.Do(func() { done <- err })
				return
			}
			_, writeErr := server.WriteToUDPAddrPort(response.Raw, source)
			first.Do(func() { done <- writeErr })
			if writeErr != nil {
				return
			}
		}
	}()
	return server, done
}

func startMutableBindingServer(t *testing.T, initialPort uint32) (*net.UDPConn, *atomic.Uint32, *atomic.Uint64) {
	t.Helper()
	server, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	mappedPort := &atomic.Uint32{}
	mappedPort.Store(initialPort)
	requests := &atomic.Uint64{}
	t.Cleanup(func() { _ = server.Close() })
	go func() {
		buffer := make([]byte, 2048)
		for {
			count, source, readErr := server.ReadFromUDPAddrPort(buffer)
			if readErr != nil {
				return
			}
			request := stun.NewWithOptions(stun.WithStrict(true))
			if request.UnmarshalBinary(buffer[:count]) != nil {
				continue
			}
			requests.Add(1)
			response := stun.MustBuild(stun.BindingSuccess)
			response.TransactionID = request.TransactionID
			response.WriteTransactionID()
			mapped := stun.XORMappedAddress{IP: net.ParseIP("198.51.100.10"), Port: int(mappedPort.Load())}
			if mapped.AddTo(response) != nil {
				continue
			}
			_, _ = server.WriteToUDPAddrPort(response.Raw, source)
		}
	}()
	return server, mappedPort, requests
}

type controlledBindingRequest struct {
	message *stun.Message
	source  netip.AddrPort
}

type controlledBindingServer struct {
	conn     *net.UDPConn
	requests chan controlledBindingRequest
}

func startControlledBindingServer(t *testing.T, initialPort int) *controlledBindingServer {
	t.Helper()
	conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	server := &controlledBindingServer{conn: conn, requests: make(chan controlledBindingRequest, 2)}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buffer := make([]byte, 2048)
		first := true
		for {
			count, source, readErr := conn.ReadFromUDPAddrPort(buffer)
			if readErr != nil {
				return
			}
			request := stun.NewWithOptions(stun.WithStrict(true))
			if request.UnmarshalBinary(buffer[:count]) != nil {
				continue
			}
			item := controlledBindingRequest{message: request, source: source}
			if first {
				first = false
				_ = server.respond(item, initialPort)
				continue
			}
			server.requests <- item
		}
	}()
	return server
}

func (s *controlledBindingServer) nextRequest(t *testing.T) controlledBindingRequest {
	t.Helper()
	select {
	case request := <-s.requests:
		return request
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for controlled STUN Binding request")
		return controlledBindingRequest{}
	}
}

func (s *controlledBindingServer) respond(request controlledBindingRequest, mappedPort int) error {
	response := stun.MustBuild(stun.BindingSuccess)
	response.TransactionID = request.message.TransactionID
	response.WriteTransactionID()
	mapped := stun.XORMappedAddress{IP: net.ParseIP("198.51.100.10"), Port: mappedPort}
	if err := mapped.AddTo(response); err != nil {
		return err
	}
	_, err := s.conn.WriteToUDPAddrPort(response.Raw, request.source)
	return err
}

func srflxEndpoint(description icesession.Description) netip.AddrPort {
	for _, candidate := range description.Candidates {
		if candidate.Type == icesession.CandidateTypeServerReflexive {
			return netip.AddrPortFrom(candidate.Address, candidate.Port)
		}
	}
	return netip.AddrPort{}
}

func TestAgentTimingRejectsKeepaliveAtDisconnectedTimeout(t *testing.T) {
	config := icesession.DefaultSessionConfig(netip.MustParseAddr("127.0.0.1"))
	config.Validation.AllowLoopback = true
	config.Timing.DisconnectedTimeout = time.Second
	config.Timing.KeepaliveInterval = time.Second
	if err := config.Validate(); err == nil {
		t.Fatal("invalid lifecycle timing was accepted")
	}
}
