// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package integration

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun/tuntest"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"
	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/connsupervisor"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/icesignal"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
)

func TestM5C3_FullTransportGenerationMigration(t *testing.T) {
	runFullTransportGenerationMigration(t, false)
}

func runFullTransportGenerationMigration(t *testing.T, encryptedDataPlane bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	stunServer := startMigrationSTUNServer(t)
	initiatorHost := newM5BHost(t)
	validation := icesession.DefaultValidationConfig()
	validation.AllowLoopback = true
	signalConfig := icesignal.DefaultConfig()
	signalConfig.Validation = validation
	signalConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	remotes := make([]migrationRemote, 0, 2)
	for range 2 {
		responderHost := newM5BHost(t)
		if err := initiatorHost.Connect(ctx, peer.AddrInfo{ID: responderHost.ID(), Addrs: responderHost.Addrs()}); err != nil {
			t.Fatal(err)
		}
		responder := newLifecycleEngine(t, icesession.NetworkUDP4)
		service, err := icesignal.NewService(
			responderHost, responderHost.ID(),
			icesignal.SessionFactoryFunc(func(ctx context.Context, _ peer.ID, _ icesession.Description) (*icesession.PreparedSession, error) {
				return responder.engine.Prepare(ctx)
			}),
			icesignal.AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }),
			signalConfig,
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = service.Close() })
		client, err := icesignal.NewClient(initiatorHost, signalConfig)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		remotes = append(remotes, migrationRemote{id: responderHost.ID(), engine: responder, service: service, client: client})
	}

	addresses := map[networkstate.Generation]netip.Addr{
		1: netip.MustParseAddr("127.0.0.2"),
		2: netip.MustParseAddr("127.0.0.3"),
	}
	transports := make(map[networkstate.Generation]*migrationTransport, 2)
	var transportsMu sync.Mutex
	var encrypted *migrationAWGState
	if encryptedDataPlane {
		encrypted = newMigrationAWGState(t, len(remotes))
		defer encrypted.Close()
	}
	factory := transportgen.FactoryFunc(func(buildContext context.Context, state networkstate.State) (transportgen.Transport, error) {
		address, ok := addresses[state.Generation]
		if !ok {
			return nil, errors.New("unexpected migration generation")
		}
		config := transportgen.DefaultConfig(transportgen.BindPlannerFunc(func(networkstate.Snapshot) (transportgen.BindPlan, error) {
			return transportgen.BindPlan{IPv4: address}, nil
		}))
		config.STUNServers = map[socketmux.AddressFamily]netip.AddrPort{socketmux.IPv4: stunServer.LocalAddr().(*net.UDPAddr).AddrPort()}
		config.Validation.AllowLoopback = true
		config.Logger = signalConfig.Logger
		config.STUN.InitialRTO = 25 * time.Millisecond
		config.STUN.MaxRTO = 50 * time.Millisecond
		config.STUN.MaxAttempts = 3
		builder, err := transportgen.NewBuilder(config)
		if err != nil {
			return nil, err
		}
		generation, err := builder.Build(buildContext, state)
		if err != nil {
			return nil, err
		}
		transport := &migrationTransport{generation: generation}
		transportsMu.Lock()
		transports[state.Generation] = transport
		transportsMu.Unlock()
		return transport, nil
	})
	records := make(map[networkstate.Generation][]migrationRecord, 2)
	var recordsMu sync.Mutex
	migrator := transportgen.MigratorFunc(func(migrateContext context.Context, transport transportgen.Transport) error {
		owned := transport.(*migrationTransport)
		if encrypted != nil {
			if err := owned.installAWG(encrypted.localPrivate); err != nil {
				return err
			}
		}
		generationRecords := make([]migrationRecord, 0, len(remotes))
		for index, remote := range remotes {
			record, connected, err := connectMigrationGeneration(
				migrateContext, owned.generation, remote.client, remote.service,
				initiatorHost.ID(), remote.id, validation,
			)
			if err != nil {
				return err
			}
			if encrypted != nil {
				if err := encrypted.Connect(migrateContext, owned, index, remote, connected, &record); err != nil {
					_ = connected.Close()
					return err
				}
			}
			owned.addConnected(connected)
			generationRecords = append(generationRecords, record)
		}
		recordsMu.Lock()
		records[transport.ID()] = generationRecords
		recordsMu.Unlock()
		return nil
	})
	source := &migrationStateSource{
		current: networkstate.State{Generation: 1},
		changes: make(chan networkstate.State, 2), errors: make(chan error, 1),
	}
	manager, err := transportgen.NewManager(transportgen.ManagerConfig{
		Source: source, Factory: factory, Migrator: migrator,
		EventCapacity: 32, BuildTimeout: 10 * time.Second, MaxConcurrentBuilds: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitMigrationManagerEvent(t, manager.Events(), transportgen.ManagerActivated, 1)
	first := loadMigrationRecords(t, &recordsMu, records, 1)
	firstTransport := loadMigrationTransport(t, &transportsMu, transports, 1)
	for _, record := range first {
		if record.selectedLocal.Addr() != addresses[1] || record.observation.MappedEndpoint != record.observation.LocalEndpoint ||
			record.observation.NetworkGeneration != 1 || record.observation.MuxPort != firstTransport.generation.Mux().Port() {
			t.Fatalf("first generation did not use its own STUN and path: %+v", record)
		}
	}
	if got := firstTransport.generation.Engine().Stats().ActiveSessions; got != 2 {
		t.Fatalf("first shared engine active sessions = %d, want 2", got)
	}
	if encrypted != nil {
		assertMigrationAWG(t, first, firstTransport)
	}

	source.changes <- networkstate.State{Generation: 2}
	waitMigrationManagerEvent(t, manager.Events(), transportgen.ManagerActivated, 2)
	second := loadMigrationRecords(t, &recordsMu, records, 2)
	secondTransport := loadMigrationTransport(t, &transportsMu, transports, 2)
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("migration records = %d/%d, want 2/2", len(first), len(second))
	}
	for index, record := range second {
		if record.selectedLocal.Addr() != addresses[2] || record.observation.MappedEndpoint != record.observation.LocalEndpoint ||
			record.observation.NetworkGeneration != 2 || record.observation.MuxPort != secondTransport.generation.Mux().Port() {
			t.Fatalf("second generation reused stale STUN or path state: %+v", record)
		}
		prior := first[index]
		if prior.sessionID == record.sessionID || prior.ufrag == record.ufrag || prior.password == record.password || prior.agent == record.agent {
			t.Fatalf("peer %d reused SessionID, credentials, or Agent", index)
		}
	}
	if firstTransport.generation.Mux() == secondTransport.generation.Mux() ||
		first[0].observation.MappedEndpoint == second[0].observation.MappedEndpoint {
		t.Fatal("network migration reused a Mux or STUN endpoint")
	}
	if got := secondTransport.generation.Engine().Stats().ActiveSessions; got != 2 {
		t.Fatalf("second shared engine active sessions = %d, want 2", got)
	}
	if encrypted != nil {
		assertMigrationAWG(t, second, secondTransport)
		if firstTransport.awg == secondTransport.awg || firstTransport.awgTUN == secondTransport.awgTUN {
			t.Fatal("network migration reused the stale generation's AWG data plane")
		}
	}
	for index, remote := range remotes {
		if got := remote.engine.engine.Stats().ActiveSessions; got != 1 {
			t.Fatalf("remote %d retained a stale session after migration: %d", index, got)
		}
	}
	if firstTransport.generation.Lifecycle() != transportgen.LifecycleClosed ||
		secondTransport.generation.Lifecycle() != transportgen.LifecycleReady || manager.Current().ID() != 2 {
		t.Fatal("transport generation activation did not close the stale generation")
	}
}

type migrationStateSource struct {
	current networkstate.State
	changes chan networkstate.State
	errors  chan error
}

type migrationRemote struct {
	id      peer.ID
	engine  *lifecycleEngine
	service *icesignal.Service
	client  *icesignal.Client
}

func (s *migrationStateSource) Current() networkstate.State        { return s.current }
func (s *migrationStateSource) Changes() <-chan networkstate.State { return s.changes }
func (s *migrationStateSource) Errors() <-chan error               { return s.errors }
func (s *migrationStateSource) Close() error                       { return nil }

type migrationTransport struct {
	generation *transportgen.Generation
	mu         sync.Mutex
	connected  []*migrationConnected
	awg        *awg.Transport
	awgTUN     *tuntest.ChannelTUN
}

func (t *migrationTransport) ID() networkstate.Generation { return t.generation.ID() }
func (t *migrationTransport) MarkStale() bool             { return t.generation.MarkStale() }
func (t *migrationTransport) addConnected(connected *migrationConnected) {
	t.mu.Lock()
	t.connected = append(t.connected, connected)
	t.mu.Unlock()
}

func (t *migrationTransport) installAWG(privateKey awg.Key) error {
	tunDevice := tuntest.NewChannelTUN()
	dataPlane, err := t.generation.InstallDataPlane(func(
		_ context.Context, mux *socketmux.Mux, generation networkstate.Generation,
	) (io.Closer, error) {
		return awg.NewTransport(awg.Config{
			Mux: mux, NetworkGeneration: generation, TUN: tunDevice.TUN(), PrivateKey: privateKey,
			Obfuscation: m6Obfuscation(),
		})
	})
	if err != nil {
		return err
	}
	transport, ok := dataPlane.(*awg.Transport)
	if !ok {
		_ = dataPlane.Close()
		return errors.New("transport generation installed an unexpected M6 data plane")
	}
	t.awg, t.awgTUN = transport, tunDevice
	return nil
}
func (t *migrationTransport) Close() error {
	t.mu.Lock()
	connected := append([]*migrationConnected(nil), t.connected...)
	t.connected = nil
	t.mu.Unlock()
	errs := make([]error, 0, len(connected)+1)
	for _, session := range connected {
		errs = append(errs, session.Close())
	}
	errs = append(errs, t.generation.Close())
	return errors.Join(errs...)
}

type migrationConnected struct {
	local, remote *iceconnect.ConnectedSession
	closeOnce     sync.Once
	closeErr      error
}

func (c *migrationConnected) Close() error {
	c.closeOnce.Do(func() { c.closeErr = errors.Join(c.local.Close(), c.remote.Close()) })
	return c.closeErr
}

type migrationRecord struct {
	sessionID     icesession.SessionID
	ufrag         string
	password      string
	agent         *ice.Agent
	selectedLocal netip.AddrPort
	observation   transportgen.Observation
	awgHandshake  bool
	awgRemote     netip.AddrPort
}

type migrationAWGRemote struct {
	transport *awg.Transport
	tun       *tuntest.ChannelTUN
}

type migrationAWGState struct {
	localPrivate, localPublic awg.Key
	remotePrivate             []awg.Key
	remotePublic              []awg.Key
	innerLocal                netip.Addr
	innerRemote               []netip.Addr
	remotes                   []*migrationAWGRemote
}

func newMigrationAWGState(t *testing.T, count int) *migrationAWGState {
	t.Helper()
	localPrivate, localPublic := m6KeyPair(t, 91)
	state := &migrationAWGState{
		localPrivate: localPrivate, localPublic: localPublic, innerLocal: netip.MustParseAddr("10.99.0.1"),
		remotePrivate: make([]awg.Key, count), remotePublic: make([]awg.Key, count),
		innerRemote: make([]netip.Addr, count), remotes: make([]*migrationAWGRemote, count),
	}
	for index := 0; index < count; index++ {
		state.remotePrivate[index], state.remotePublic[index] = m6KeyPair(t, byte(101+index*17))
		state.innerRemote[index] = netip.AddrFrom4([4]byte{10, 99, 0, byte(index + 2)})
	}
	return state
}

func (s *migrationAWGState) Connect(
	ctx context.Context,
	local *migrationTransport,
	index int,
	remote migrationRemote,
	connected *migrationConnected,
	record *migrationRecord,
) error {
	sessionGeneration := connsupervisor.SessionGeneration(local.ID())
	localPath, err := awg.SelectedEndpointFromICE(
		connected.local.Result(), local.ID(), sessionGeneration, remote.id,
	)
	if err != nil {
		return err
	}
	if err := local.awg.AddPeer(awg.PeerConfig{
		RemotePeer: remote.id, PublicKey: s.remotePublic[index], Endpoint: localPath,
		AllowedIPs: []netip.Prefix{netip.PrefixFrom(s.innerRemote[index], s.innerRemote[index].BitLen())},
	}); err != nil {
		return err
	}
	remoteResult := connected.remote.Result()
	remoteGeneration := networkstate.Generation(remoteResult.TransportGeneration)
	remotePath, err := awg.SelectedEndpointFromICE(
		remoteResult, remoteGeneration, sessionGeneration, remoteResult.RemotePeer,
	)
	if err != nil {
		return err
	}
	remoteDataPlane := s.remotes[index]
	if remoteDataPlane == nil {
		remoteTUN := tuntest.NewChannelTUN()
		remoteTransport, createErr := awg.NewTransport(awg.Config{
			Mux: remote.engine.mux, NetworkGeneration: remoteGeneration, TUN: remoteTUN.TUN(),
			PrivateKey: s.remotePrivate[index], Obfuscation: m6Obfuscation(),
			Peers: []awg.PeerConfig{{
				RemotePeer: remoteResult.RemotePeer, PublicKey: s.localPublic, Endpoint: remotePath,
				AllowedIPs: []netip.Prefix{netip.PrefixFrom(s.innerLocal, s.innerLocal.BitLen())},
			}},
		})
		if createErr != nil {
			return createErr
		}
		remoteDataPlane = &migrationAWGRemote{transport: remoteTransport, tun: remoteTUN}
		s.remotes[index] = remoteDataPlane
	} else if err := remoteDataPlane.transport.UpdatePeerEndpoint(s.localPublic, remotePath); err != nil {
		return err
	}
	if err := local.awg.TriggerHandshake(s.remotePublic[index]); err != nil {
		return err
	}
	if _, err := local.awg.WaitHandshake(ctx, s.remotePublic[index]); err != nil {
		return err
	}
	if _, err := remoteDataPlane.transport.WaitHandshake(ctx, s.localPublic); err != nil {
		return err
	}
	if err := migrationSendInner(ctx, local.awgTUN, remoteDataPlane.tun,
		m6InnerPacket(s.innerLocal, s.innerRemote[index], byte(index+1))); err != nil {
		return err
	}
	if err := migrationSendInner(ctx, remoteDataPlane.tun, local.awgTUN,
		m6InnerPacket(s.innerRemote[index], s.innerLocal, byte(index+11))); err != nil {
		return err
	}
	record.awgHandshake = true
	record.awgRemote = localPath.Address
	return nil
}

func (s *migrationAWGState) Close() {
	for _, remote := range s.remotes {
		if remote != nil {
			_ = remote.transport.Close()
		}
	}
}

func migrationSendInner(ctx context.Context, source, destination *tuntest.ChannelTUN, packet []byte) error {
	select {
	case source.Outbound <- append([]byte(nil), packet...):
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case received := <-destination.Inbound:
		if string(received) != string(packet) {
			return errors.New("migration decrypted inner packet mismatch")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func assertMigrationAWG(t *testing.T, records []migrationRecord, transport *migrationTransport) {
	t.Helper()
	if transport.awg == nil || transport.awgTUN == nil || transport.awg.PeerCount() != len(records) {
		t.Fatalf("generation %d did not install one shared AWG data plane", transport.ID())
	}
	for _, record := range records {
		if !record.awgHandshake || !record.awgRemote.IsValid() {
			t.Fatalf("generation %d lacks encrypted migration evidence: %+v", transport.ID(), record)
		}
	}
	if transport.awg.Bind().Port() != transport.generation.Mux().Port() ||
		transport.awg.Bind().Stats().SendPackets == 0 || transport.awg.Bind().Stats().ReceivePackets == 0 {
		t.Fatalf("generation %d AWG did not use its Irvio mux: %+v", transport.ID(), transport.awg.Bind().Stats())
	}
}

func connectMigrationGeneration(
	ctx context.Context,
	generation *transportgen.Generation,
	client *icesignal.Client,
	service *icesignal.Service,
	localPeer, remotePeer peer.ID,
	validation icesession.ValidationConfig,
) (migrationRecord, *migrationConnected, error) {
	local, err := generation.NewPreparedSession(ctx)
	if err != nil {
		return migrationRecord{}, nil, err
	}
	keepLocal := false
	defer func() {
		if !keepLocal {
			_ = local.Close()
		}
	}()
	exchange, err := client.Exchange(ctx, remotePeer, local)
	if err != nil {
		return migrationRecord{}, nil, err
	}
	accepted, err := service.NextAccepted(ctx)
	if err != nil {
		return migrationRecord{}, nil, err
	}
	keepRemote := false
	defer func() {
		if !keepRemote {
			_ = accepted.Local.Close()
		}
	}()
	connectConfig := iceconnect.DefaultConfig()
	connectConfig.Validation = validation
	localAttempt, err := iceconnect.New(local, iceconnect.Binding{
		SessionID: exchange.SessionID, Initiator: exchange.Initiator, Responder: exchange.Responder,
		LocalPeer: localPeer, RemotePeer: remotePeer, Purpose: exchange.Purpose,
		LocalRole: exchange.LocalRole, RemoteRole: exchange.RemoteRole,
	}, iceconnect.RemoteState{
		SessionID: exchange.SessionID, PeerID: remotePeer, Role: exchange.RemoteRole, Description: exchange.RemoteDescription,
	}, connectConfig)
	if err != nil {
		return migrationRecord{}, nil, err
	}
	remoteAttempt, err := iceconnect.New(accepted.Local, iceconnect.Binding{
		SessionID: accepted.SessionID, Initiator: accepted.Initiator, Responder: accepted.Responder,
		LocalPeer: remotePeer, RemotePeer: localPeer, Purpose: accepted.Purpose,
		LocalRole: accepted.LocalRole, RemoteRole: accepted.RemoteRole,
	}, iceconnect.RemoteState{
		SessionID: accepted.SessionID, PeerID: localPeer, Role: accepted.RemoteRole, Description: accepted.RemoteDescription,
	}, connectConfig)
	if err != nil {
		_ = localAttempt.Close()
		return migrationRecord{}, nil, err
	}
	if err := localAttempt.LoadRemoteCandidates(ctx); err != nil {
		_ = remoteAttempt.Close()
		return migrationRecord{}, nil, err
	}
	if err := remoteAttempt.LoadRemoteCandidates(ctx); err != nil {
		_ = localAttempt.Close()
		_ = remoteAttempt.Close()
		return migrationRecord{}, nil, err
	}
	remoteDone := make(chan struct {
		connected *iceconnect.ConnectedSession
		err       error
	}, 1)
	go func() {
		connected, err := remoteAttempt.Connect(ctx)
		remoteDone <- struct {
			connected *iceconnect.ConnectedSession
			err       error
		}{connected, err}
	}()
	localConnected, localErr := localAttempt.Connect(ctx)
	remoteResult := <-remoteDone
	if localErr != nil || remoteResult.err != nil {
		_ = localAttempt.Close()
		_ = remoteAttempt.Close()
		return migrationRecord{}, nil, errors.Join(localErr, remoteResult.err)
	}
	observations := generation.Observations()
	if len(observations) != 1 {
		_ = localConnected.Close()
		_ = remoteResult.connected.Close()
		return migrationRecord{}, nil, errors.New("migration generation lacks one STUN observation")
	}
	keepLocal, keepRemote = true, true
	return migrationRecord{
		sessionID: exchange.SessionID, ufrag: local.LocalDescription().Credentials.Ufrag,
		password: local.LocalDescription().Credentials.Pwd, agent: local.Agent(),
		selectedLocal: localConnected.Result().LocalEndpoint, observation: observations[0],
	}, &migrationConnected{local: localConnected, remote: remoteResult.connected}, nil
}

func loadMigrationRecords(t *testing.T, mu *sync.Mutex, records map[networkstate.Generation][]migrationRecord, generation networkstate.Generation) []migrationRecord {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	record, ok := records[generation]
	if !ok {
		t.Fatalf("missing migration record %d", generation)
	}
	return append([]migrationRecord(nil), record...)
}

func loadMigrationTransport(t *testing.T, mu *sync.Mutex, transports map[networkstate.Generation]*migrationTransport, generation networkstate.Generation) *migrationTransport {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	transport, ok := transports[generation]
	if !ok {
		t.Fatalf("missing transport generation %d", generation)
	}
	return transport
}

func waitMigrationManagerEvent(t *testing.T, events <-chan transportgen.ManagerEvent, eventType transportgen.ManagerEventType, generation networkstate.Generation) {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("transport manager events closed before generation %d", generation)
			}
			if event.Type == transportgen.ManagerBuildFailed ||
				event.Type == transportgen.ManagerMigrationFailed ||
				event.Type == transportgen.ManagerMonitorFailed {
				t.Fatalf("transport generation %d failed: %v", event.Generation, event.Err)
			}
			if event.Type == eventType && event.Generation == generation {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for transport generation %d", generation)
		}
	}
}

func startMigrationSTUNServer(t *testing.T) *net.UDPConn {
	t.Helper()
	server, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.10:0")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	go func() {
		buffer := make([]byte, 2048)
		for {
			count, source, err := server.ReadFromUDPAddrPort(buffer)
			if err != nil {
				return
			}
			request := stun.NewWithOptions(stun.WithStrict(true))
			if request.UnmarshalBinary(buffer[:count]) != nil {
				continue
			}
			response := stun.MustBuild(stun.BindingSuccess)
			response.TransactionID = request.TransactionID
			response.WriteTransactionID()
			mapped := stun.XORMappedAddress{IP: net.IP(source.Addr().AsSlice()), Port: int(source.Port())}
			if mapped.AddTo(response) != nil {
				continue
			}
			_, _ = server.WriteToUDPAddrPort(response.Raw, source)
		}
	}()
	return server
}
