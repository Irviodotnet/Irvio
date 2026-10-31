//go:build linux

// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Command hydra-test-icesignal tests M5b in disposable namespaces without reporting ICE passwords.
package main

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun/tuntest"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
	"github.com/pion/ice/v4"
	"github.com/vishvananda/netlink"
	"golang.org/x/crypto/curve25519"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/connsupervisor"
	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	"github.com/Irviodotnet/Irvio/internal/exitfirewall"
	"github.com/Irviodotnet/Irvio/internal/iceadapter"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/iceengine"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/icesignal"
	"github.com/Irviodotnet/Irvio/internal/ipforward"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/linuxroute"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/stunprobe"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
)

type candidateReport struct {
	Type           string `json:"type"`
	Network        string `json:"network"`
	Address        string `json:"address"`
	Port           uint16 `json:"port"`
	RelatedAddress string `json:"related_address,omitempty"`
	RelatedPort    uint16 `json:"related_port,omitempty"`
	Foundation     string `json:"foundation"`
	Priority       uint32 `json:"priority"`
	Component      uint16 `json:"component"`
}

type report struct {
	Type                      string            `json:"type"`
	Mode                      string            `json:"mode"`
	PID                       int               `json:"pid"`
	PeerID                    string            `json:"peer_id"`
	ListenAddresses           []string          `json:"listen_addresses"`
	MuxPort                   uint16            `json:"mux_port"`
	LocalIPv4                 string            `json:"local_ipv4,omitempty"`
	LocalIPv6                 string            `json:"local_ipv6,omitempty"`
	SessionID                 string            `json:"session_id,omitempty"`
	RemotePeerID              string            `json:"remote_peer_id,omitempty"`
	LocalUfrag                string            `json:"local_ufrag,omitempty"`
	RemoteUfrag               string            `json:"remote_ufrag,omitempty"`
	ObservedSrflxIPv4         string            `json:"observed_srflx_ipv4,omitempty"`
	UDP4SocketInodeBeforeSTUN string            `json:"udp4_socket_inode_before_stun,omitempty"`
	UDP4SocketInodeReady      string            `json:"udp4_socket_inode_ready,omitempty"`
	UDP4SocketInodeAfter      string            `json:"udp4_socket_inode_after,omitempty"`
	UDP6SocketInodeBeforeSTUN string            `json:"udp6_socket_inode_before_stun,omitempty"`
	UDP6SocketInodeReady      string            `json:"udp6_socket_inode_ready,omitempty"`
	UDP6SocketInodeAfter      string            `json:"udp6_socket_inode_after,omitempty"`
	LocalCandidates           []candidateReport `json:"local_candidates,omitempty"`
	RemoteCandidates          []candidateReport `json:"remote_candidates,omitempty"`
	LearnedCandidates         []candidateReport `json:"learned_candidates,omitempty"`
	ConnectivityRequests      uint64            `json:"connectivity_requests"`
	Connected                 bool              `json:"connected"`
	Role                      uint8             `json:"role,omitempty"`
	SelectedNetwork           uint8             `json:"selected_network,omitempty"`
	SelectedLocal             string            `json:"selected_local,omitempty"`
	SelectedRemote            string            `json:"selected_remote,omitempty"`
	SelectedLocalType         uint8             `json:"selected_local_type,omitempty"`
	SelectedRemoteType        uint8             `json:"selected_remote_type,omitempty"`
	Nominated                 bool              `json:"nominated"`
	PairID                    uint64            `json:"pair_id,omitempty"`
	RequestsSent              uint64            `json:"requests_sent,omitempty"`
	ResponsesReceived         uint64            `json:"responses_received,omitempty"`
	ApplicationPackets        uint32            `json:"application_packets"`
	ApplicationBytes          uint64            `json:"application_bytes"`
	AdapterBefore             uint64            `json:"adapter_before"`
	AdapterAfter              uint64            `json:"adapter_after"`
	PostCloseAdapterClean     bool              `json:"post_close_adapter_clean"`
	PostCloseDataWrite        bool              `json:"post_close_data_write"`
	PostCloseSTUNLease        bool              `json:"post_close_stun_lease"`
	NetworkGeneration         uint64            `json:"network_generation,omitempty"`
	SessionGeneration         uint64            `json:"session_generation,omitempty"`
	PreviousSessionID         string            `json:"previous_session_id,omitempty"`
	PreviousLocalUfrag        string            `json:"previous_local_ufrag,omitempty"`
	CredentialsRotated        bool              `json:"credentials_rotated,omitempty"`
	PionAgentReplaced         bool              `json:"pion_agent_replaced,omitempty"`
	DisconnectedObserved      bool              `json:"disconnected_observed,omitempty"`
	FailedObserved            bool              `json:"failed_observed,omitempty"`
	AWGHandshake              bool              `json:"awg_handshake,omitempty"`
	AWGInnerPacket            bool              `json:"awg_inner_packet,omitempty"`
	AWGSelectedRemote         string            `json:"awg_selected_remote,omitempty"`
	AWGSocketInode            string            `json:"awg_socket_inode,omitempty"`
	AWGSendPackets            uint64            `json:"awg_send_packets,omitempty"`
	AWGReceivePackets         uint64            `json:"awg_receive_packets,omitempty"`
	AWGPublicFingerprint      string            `json:"awg_public_fingerprint,omitempty"`
	AWGTUNName                string            `json:"awg_tun_name,omitempty"`
	AWGTUNIfIndex             int               `json:"awg_tun_ifindex,omitempty"`
	AWGTUNAddresses           []string          `json:"awg_tun_addresses,omitempty"`
	AWGSocketMark             uint32            `json:"awg_socket_mark,omitempty"`
	RoutingOutTable           int               `json:"routing_out_table,omitempty"`
	RoutingWANTable           int               `json:"routing_wan_table,omitempty"`
	RoutingRules              int               `json:"routing_rules,omitempty"`
	RoutingRoutes             int               `json:"routing_routes,omitempty"`
	ExitFirewallTable         string            `json:"exit_firewall_table,omitempty"`
	ExitFirewallRules         int               `json:"exit_firewall_rules,omitempty"`
	ProtectedMode             bool              `json:"protected_mode,omitempty"`
	ProtectionAdopted         bool              `json:"protection_adopted,omitempty"`
	MultiPeers                int               `json:"multi_peers,omitempty"`
	Error                     string            `json:"error,omitempty"`
}

type options struct {
	mode, bind4Text, bind6Text        string
	tcp4Text, tcp6Text                string
	remote, stunServer4               string
	srflx4, srflx6                    string
	srflx4Override                    string
	ready, result, release            string
	connectReady, connectStart        string
	disconnected, failed              string
	port                              int
	timeout                           time.Duration
	disconnectedTimeout               time.Duration
	failedTimeout                     time.Duration
	keepaliveInterval                 time.Duration
	checkInterval                     time.Duration
	verifyPostClose                   bool
	transientRecovery                 bool
	familyFailover                    bool
	awg                               bool
	awgReady, awgStart                string
	awgInnerLocal, awgInnerRemote     string
	awgTUNName, awgTUNAddresses       string
	awgAllowedIPs                     string
	awgRealTUN                        bool
	m7Role                            string
	m7WANName                         string
	m7WAN4Source, m7WAN4Gateway       string
	m7WAN4Connected                   string
	m7WAN6Source, m7WAN6Gateway       string
	m7WAN6Connected                   string
	m7Bypass                          string
	m7Management                      string
	m7Protect4, m7Protect6            bool
	m7Reject4, m7Reject6              bool
	m7ClientPool4, m7ClientPool6      string
	m7MultiConfig                     string
	m7MultiCount                      int
	m8Protect                         bool
	awgLocalKeySeed, awgRemoteKeySeed int
}

type stack struct {
	mux                 *socketmux.Mux
	engine              *iceengine.Engine
	observedSrflxIPv4   netip.AddrPort
	phases              *icesession.PhaseManager
	probeServer         netip.AddrPort
	udp4InodeBeforeSTUN string
	udp6InodeBeforeSTUN string
}

func main() {
	value, resultPath, err := run()
	if err != nil {
		value.Error = err.Error()
	}
	if resultPath != "" {
		if writeErr := writeJSON(resultPath, value); writeErr != nil && err == nil {
			err = writeErr
		}
	}
	_ = json.NewEncoder(os.Stdout).Encode(value)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (value report, resultPath string, runErr error) {
	value = report{Type: "result", PID: os.Getpid()}
	parsed, err := parseOptions()
	if err != nil {
		return value, "", err
	}
	value.Mode = parsed.mode
	resultPath = parsed.result
	ctx, cancel := context.WithTimeout(context.Background(), parsed.timeout)
	defer cancel()
	bind4, err := parseOptionalAddr(parsed.bind4Text)
	if err != nil {
		return value, resultPath, err
	}
	bind6, err := parseOptionalAddr(parsed.bind6Text)
	if err != nil {
		return value, resultPath, err
	}
	var protection *killswitch.Controller
	var protectionGeneration uint64
	var controlMark uint32
	if parsed.m7Role != "" {
		controlMark = linuxroute.DefaultOuterMark
	}
	if parsed.m8Protect {
		protection, err = killswitch.New(killswitch.Config{
			Backend: killswitch.NFTBackend{},
			Policy: killswitch.Policy{
				TUNName:    parsed.awgTUNName,
				BypassMark: linuxroute.DefaultOuterMark,
			},
		})
		if err != nil {
			return value, resultPath, err
		}
		protectionGeneration, err = protection.Protect(ctx)
		if err != nil {
			return value, resultPath, err
		}
		value.ProtectedMode = protection.IsCurrent(protectionGeneration)
		value.ProtectionAdopted = protection.Snapshot().Adopted
		defer func() {
			disconnectContext, disconnectCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer disconnectCancel()
			runErr = errors.Join(runErr, protection.Disconnect(disconnectContext))
		}()
	}
	transport, err := newHost(parsed.tcp4Text, parsed.tcp6Text, controlMark)
	if err != nil {
		return value, resultPath, err
	}
	defer func() { runErr = errors.Join(runErr, transport.Close()) }()
	value.PeerID = transport.ID().String()
	for _, address := range transport.Addrs() {
		value.ListenAddresses = append(value.ListenAddresses, address.Encapsulate(multiaddr.StringCast("/p2p/"+transport.ID().String())).String())
	}
	if parsed.mode == "migration-initiator" {
		if err := runMigrationInitiator(ctx, &value, parsed, transport); err != nil {
			return value, resultPath, err
		}
		return value, resultPath, nil
	}
	var outerMark uint32
	if parsed.m7Role != "" {
		outerMark = linuxroute.DefaultOuterMark
	}
	sessionStack, err := newStack(ctx, bind4, bind6, parsed.port, outerMark, parsed.stunServer4, parsed.srflx4, parsed.srflx6, parsed.srflx4Override, icesession.AgentTiming{
		DisconnectedTimeout: parsed.disconnectedTimeout, FailedTimeout: parsed.failedTimeout,
		KeepaliveInterval: parsed.keepaliveInterval, CheckInterval: parsed.checkInterval,
	})
	if err != nil {
		return value, resultPath, err
	}
	defer func() { runErr = errors.Join(runErr, sessionStack.mux.Close()) }()
	defer func() { runErr = errors.Join(runErr, sessionStack.engine.Close()) }()
	value.MuxPort = sessionStack.mux.Port()
	value.UDP4SocketInodeBeforeSTUN = sessionStack.udp4InodeBeforeSTUN
	value.UDP6SocketInodeBeforeSTUN = sessionStack.udp6InodeBeforeSTUN
	if local, ok := sessionStack.mux.LocalAddr(socketmux.IPv4); ok {
		value.LocalIPv4 = local.String()
	}
	if local, ok := sessionStack.mux.LocalAddr(socketmux.IPv6); ok {
		value.LocalIPv6 = local.String()
	}
	if sessionStack.observedSrflxIPv4.IsValid() {
		value.ObservedSrflxIPv4 = sessionStack.observedSrflxIPv4.String()
	}
	if bind4.IsValid() {
		value.UDP4SocketInodeReady, err = udpSocketInode(sessionStack.mux.Port(), "/proc/net/udp")
		if err != nil {
			return value, resultPath, err
		}
	}
	if bind6.IsValid() {
		value.UDP6SocketInodeReady, err = udpSocketInode(sessionStack.mux.Port(), "/proc/net/udp6")
		if err != nil {
			return value, resultPath, err
		}
	}
	value.Type = "ready"

	config := icesignal.DefaultConfig()
	config.Validation.AllowLoopback = bind4.IsLoopback() || bind6.IsLoopback()
	config.StreamTimeout = min(5*time.Second, parsed.timeout)
	switch parsed.mode {
	case "connect-multi-responder":
		if err := runM7MultiResponder(ctx, &value, parsed, transport, sessionStack, config); err != nil {
			return value, resultPath, err
		}
	case "migration-responder":
		if err := runMigrationResponder(ctx, &value, parsed, transport, sessionStack, config); err != nil {
			return value, resultPath, err
		}
	case "reconnect-responder":
		if err := runReconnectResponder(ctx, &value, parsed, transport, sessionStack, config); err != nil {
			return value, resultPath, err
		}
	case "reconnect-initiator":
		if err := runReconnectInitiator(ctx, &value, parsed, transport, sessionStack, config); err != nil {
			return value, resultPath, err
		}
	case "responder", "connect-responder":
		service, err := icesignal.NewService(
			transport, transport.ID(),
			icesignal.SessionFactoryFunc(func(ctx context.Context, _ peer.ID, _ icesession.Description) (*icesession.PreparedSession, error) {
				return sessionStack.engine.Prepare(ctx)
			}),
			icesignal.AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }),
			config,
		)
		if err != nil {
			return value, resultPath, err
		}
		defer service.Close()
		if err := writeJSON(parsed.ready, value); err != nil {
			return value, resultPath, err
		}
		value.Type = "result"
		accepted, err := service.NextAccepted(ctx)
		if err != nil {
			return value, resultPath, err
		}
		defer accepted.Local.Close()
		if parsed.mode == "responder" {
			fillAccepted(&value, accepted)
			break
		}
		fillAccepted(&value, accepted)
		connectConfig := iceconnect.DefaultConfig()
		connectConfig.Validation = config.Validation
		attempt, err := iceconnect.New(
			accepted.Local,
			iceconnect.Binding{
				SessionID: accepted.SessionID, Initiator: accepted.Initiator, Responder: accepted.Responder,
				LocalPeer: transport.ID(), RemotePeer: accepted.RemotePeer, Purpose: accepted.Purpose,
				LocalRole: accepted.LocalRole, RemoteRole: accepted.RemoteRole,
			},
			iceconnect.RemoteState{
				SessionID: accepted.SessionID, PeerID: accepted.RemotePeer,
				Role: accepted.RemoteRole, Description: accepted.RemoteDescription,
			},
			connectConfig,
		)
		if err != nil {
			return value, resultPath, err
		}
		defer attempt.Close()
		if err := attempt.LoadRemoteCandidates(ctx); err != nil {
			return value, resultPath, err
		}
		if err := waitForConnectStart(ctx, parsed.connectReady, parsed.connectStart, value); err != nil {
			return value, resultPath, err
		}
		connected, err := attempt.Connect(ctx)
		if err != nil {
			if parsed.verifyPostClose {
				err = errors.Join(err, verifyPostClose(sessionStack, attempt, accepted.Local, &value))
			}
			return value, resultPath, err
		}
		defer connected.Close()
		connectedResult := connected.Result()
		if err := fillConnected(&value, connectedResult, sessionStack.mux.Port()); err != nil {
			return value, resultPath, err
		}
		if parsed.awg {
			var awgTransport *awgDataPlane
			if err := runAWGDataPlane(ctx, &value, parsed, sessionStack.mux, connected, connectedResult, false, &awgTransport); err != nil {
				return value, resultPath, err
			}
			defer awgTransport.Close()
		}
		if err := holdConnected(ctx, parsed.release, resultPath, value); err != nil {
			return value, resultPath, err
		}
		if parsed.verifyPostClose {
			if err := connected.Close(); err != nil {
				return value, resultPath, err
			}
			if err := verifyPostClose(sessionStack, attempt, accepted.Local, &value); err != nil {
				return value, resultPath, err
			}
		}
	case "initiator", "connect-initiator":
		if err := writeJSON(parsed.ready, value); err != nil {
			return value, resultPath, err
		}
		value.Type = "result"
		remoteAddress, err := multiaddr.NewMultiaddr(parsed.remote)
		if err != nil {
			return value, resultPath, err
		}
		remoteInfo, err := peer.AddrInfoFromP2pAddr(remoteAddress)
		if err != nil {
			return value, resultPath, err
		}
		if err := transport.Connect(ctx, *remoteInfo); err != nil {
			return value, resultPath, err
		}
		prepared, err := sessionStack.engine.Prepare(ctx)
		if err != nil {
			return value, resultPath, err
		}
		if parsed.mode == "initiator" {
			defer prepared.Close()
		}
		client, err := icesignal.NewClient(transport, config)
		if err != nil {
			return value, resultPath, err
		}
		defer client.Close()
		result, err := client.Exchange(ctx, remoteInfo.ID, prepared)
		if err != nil {
			return value, resultPath, err
		}
		fillExchange(&value, prepared, result)
		if parsed.mode == "initiator" {
			break
		}
		connectConfig := iceconnect.DefaultConfig()
		connectConfig.Validation = config.Validation
		attempt, err := iceconnect.New(
			prepared,
			iceconnect.Binding{
				SessionID: result.SessionID, Initiator: result.Initiator, Responder: result.Responder,
				LocalPeer: transport.ID(), RemotePeer: result.RemotePeer, Purpose: result.Purpose,
				LocalRole: result.LocalRole, RemoteRole: result.RemoteRole,
			},
			iceconnect.RemoteState{
				SessionID: result.SessionID, PeerID: result.RemotePeer,
				Role: result.RemoteRole, Description: result.RemoteDescription,
			},
			connectConfig,
		)
		if err != nil {
			_ = prepared.Close()
			return value, resultPath, err
		}
		defer attempt.Close()
		if err := attempt.LoadRemoteCandidates(ctx); err != nil {
			return value, resultPath, err
		}
		if err := waitForConnectStart(ctx, parsed.connectReady, parsed.connectStart, value); err != nil {
			return value, resultPath, err
		}
		connected, err := attempt.Connect(ctx)
		if err != nil {
			if parsed.verifyPostClose {
				err = errors.Join(err, verifyPostClose(sessionStack, attempt, prepared, &value))
			}
			return value, resultPath, err
		}
		defer connected.Close()
		connectedResult := connected.Result()
		if err := fillConnected(&value, connectedResult, sessionStack.mux.Port()); err != nil {
			return value, resultPath, err
		}
		if parsed.awg {
			var awgTransport *awgDataPlane
			if err := runAWGDataPlane(ctx, &value, parsed, sessionStack.mux, connected, connectedResult, true, &awgTransport); err != nil {
				return value, resultPath, err
			}
			defer awgTransport.Close()
		}
		if err := holdConnected(ctx, parsed.release, resultPath, value); err != nil {
			return value, resultPath, err
		}
		if parsed.verifyPostClose {
			if err := connected.Close(); err != nil {
				return value, resultPath, err
			}
			if err := verifyPostClose(sessionStack, attempt, prepared, &value); err != nil {
				return value, resultPath, err
			}
		}
	default:
		return value, resultPath, fmt.Errorf("unsupported mode %q", parsed.mode)
	}
	return value, resultPath, nil
}

type reconnectRecord struct {
	report    report
	password  string
	agent     *ice.Agent
	attempt   *iceconnect.Attempt
	connected *iceconnect.ConnectedSession
}

type migrationTransport struct {
	generation *transportgen.Generation
	mu         sync.Mutex
	record     reconnectRecord
}

func (t *migrationTransport) ID() networkstate.Generation { return t.generation.ID() }
func (t *migrationTransport) MarkStale() bool             { return t.generation.MarkStale() }
func (t *migrationTransport) setRecord(record reconnectRecord) {
	t.mu.Lock()
	t.record = record
	t.mu.Unlock()
}
func (t *migrationTransport) getRecord() reconnectRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.record
}
func (t *migrationTransport) Close() error {
	t.mu.Lock()
	connected := t.record.connected
	t.record.connected = nil
	t.mu.Unlock()
	if connected == nil {
		return t.generation.Close()
	}
	return errors.Join(connected.Close(), t.generation.Close())
}

func runMigrationInitiator(ctx context.Context, value *report, parsed options, transport host.Host) error {
	remoteAddress, err := multiaddr.NewMultiaddr(parsed.remote)
	if err != nil {
		return err
	}
	remoteInfo, err := peer.AddrInfoFromP2pAddr(remoteAddress)
	if err != nil {
		return err
	}
	if err := transport.Connect(ctx, *remoteInfo); err != nil {
		return err
	}
	clientConfig := icesignal.DefaultConfig()
	client, err := icesignal.NewClient(transport, clientConfig)
	if err != nil {
		return err
	}
	defer client.Close()
	monitor, err := networkstate.NewMonitor(networkstate.Config{Debounce: 25 * time.Millisecond})
	if err != nil {
		return err
	}
	builderConfig := transportgen.DefaultConfig(transportgen.PreferredBindPlanner{})
	builderConfig.Port = parsed.port
	if parsed.m7Role != "" {
		builderConfig.SocketMark = linuxroute.DefaultOuterMark
	}
	if parsed.stunServer4 != "" {
		stunServer, parseErr := netip.ParseAddrPort(parsed.stunServer4)
		if parseErr != nil || !stunServer.Addr().Is4() {
			_ = monitor.Close()
			return errors.New("invalid migration IPv4 STUN endpoint")
		}
		builderConfig.STUNServers = map[socketmux.AddressFamily]netip.AddrPort{socketmux.IPv4: stunServer}
	}
	builderConfig.Timing = icesession.AgentTiming{
		DisconnectedTimeout: parsed.disconnectedTimeout, FailedTimeout: parsed.failedTimeout,
		KeepaliveInterval: parsed.keepaliveInterval, CheckInterval: parsed.checkInterval,
	}
	builderConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	builder, err := transportgen.NewBuilder(builderConfig)
	if err != nil {
		_ = monitor.Close()
		return err
	}
	base := *value
	migrator := transportgen.MigratorFunc(func(migrateContext context.Context, candidate transportgen.Transport) error {
		owned := candidate.(*migrationTransport)
		record, err := connectMigrationInitiator(migrateContext, base, owned.generation, transport, *remoteInfo, client, clientConfig.Validation)
		if err != nil {
			return err
		}
		owned.setRecord(record)
		return nil
	})
	factory := transportgen.FactoryFunc(func(buildContext context.Context, state networkstate.State) (transportgen.Transport, error) {
		generation, err := builder.Build(buildContext, state)
		if err != nil {
			return nil, err
		}
		return &migrationTransport{generation: generation}, nil
	})
	manager, err := transportgen.NewManager(transportgen.ManagerConfig{
		Source: monitor, Factory: factory, Migrator: migrator,
		EventCapacity: 32, BuildTimeout: min(12*time.Second, parsed.timeout), MaxConcurrentBuilds: 2,
	})
	if err != nil {
		_ = monitor.Close()
		return err
	}
	defer manager.Close()
	if err := manager.Start(ctx); err != nil {
		return err
	}
	var awgTransport *awgDataPlane
	defer func() {
		if awgTransport != nil {
			_ = awgTransport.Close()
		}
	}()
	var first reconnectRecord
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-manager.Events():
			if !ok {
				return errors.New("transport migration manager stopped before generation two")
			}
			switch event.Type {
			case transportgen.ManagerBuildFailed, transportgen.ManagerMigrationFailed, transportgen.ManagerMonitorFailed:
				return fmt.Errorf("transport generation %d failed: %w", event.Generation, event.Err)
			case transportgen.ManagerActivated:
				current, ok := manager.Current().(*migrationTransport)
				if !ok || current.ID() != event.Generation {
					return errors.New("migration manager published the wrong current transport")
				}
				record := current.getRecord()
				if record.connected == nil {
					return errors.New("activated transport lacks a connected ICE session")
				}
				if event.Generation == 1 {
					first = record
					if err := writeJSON(parsed.ready, record.report); err != nil {
						return err
					}
					if parsed.awg {
						if err := runAWGDataPlane(ctx, &record.report, parsed, current.generation.Mux(), record.connected, record.connected.Result(), true, &awgTransport); err != nil {
							return err
						}
						first.report = record.report
					}
					if err := writeJSON(parsed.result+".generation-1", record.report); err != nil {
						return err
					}
					continue
				}
				record.report.PreviousSessionID = first.report.SessionID
				record.report.PreviousLocalUfrag = first.report.LocalUfrag
				record.report.CredentialsRotated = first.report.SessionID != record.report.SessionID &&
					first.report.LocalUfrag != record.report.LocalUfrag && first.password != record.password
				record.report.PionAgentReplaced = first.agent != record.agent
				if parsed.awg {
					if err := migrateAWGDataPlane(ctx, &record.report, parsed, awgTransport, current.generation.Mux(), record.connected, record.connected.Result()); err != nil {
						return err
					}
				}
				*value = record.report
				return holdConnected(ctx, parsed.release, parsed.result, record.report)
			}
		}
	}
}

func connectMigrationInitiator(
	ctx context.Context,
	base report,
	generation *transportgen.Generation,
	transport host.Host,
	remote peer.AddrInfo,
	client *icesignal.Client,
	validation icesession.ValidationConfig,
) (reconnectRecord, error) {
	prepared, err := generation.NewPreparedSession(ctx)
	if err != nil {
		return reconnectRecord{}, err
	}
	exchange, err := client.Exchange(ctx, remote.ID, prepared)
	if err != nil {
		_ = prepared.Close()
		return reconnectRecord{}, err
	}
	attempt, err := initiatorAttempt(prepared, exchange, transport.ID(), validation)
	if err != nil {
		_ = prepared.Close()
		return reconnectRecord{}, err
	}
	if err := attempt.LoadRemoteCandidates(ctx); err != nil {
		_ = attempt.Close()
		return reconnectRecord{}, err
	}
	connected, err := attempt.Connect(ctx)
	if err != nil {
		return reconnectRecord{}, err
	}
	current := base
	current.MuxPort = generation.Mux().Port()
	if local, ok := generation.Mux().LocalAddr(socketmux.IPv4); ok {
		current.LocalIPv4 = local.String()
		current.UDP4SocketInodeReady, err = udpSocketInode(generation.Mux().Port(), "/proc/net/udp")
		if err != nil {
			_ = connected.Close()
			return reconnectRecord{}, err
		}
	}
	if local, ok := generation.Mux().LocalAddr(socketmux.IPv6); ok {
		current.LocalIPv6 = local.String()
		current.UDP6SocketInodeReady, err = udpSocketInode(generation.Mux().Port(), "/proc/net/udp6")
		if err != nil {
			_ = connected.Close()
			return reconnectRecord{}, err
		}
	}
	current.NetworkGeneration = uint64(generation.ID())
	current.SessionGeneration = uint64(generation.ID())
	fillExchange(&current, prepared, exchange)
	if observations := generation.Observations(); len(observations) == 1 {
		current.ObservedSrflxIPv4 = observations[0].MappedEndpoint.String()
	} else if len(observations) != 0 {
		_ = connected.Close()
		return reconnectRecord{}, errors.New("migration transport has an unexpected STUN observation count")
	}
	if err := fillConnected(&current, connected.Result(), generation.Mux().Port()); err != nil {
		_ = connected.Close()
		return reconnectRecord{}, err
	}
	return reconnectRecord{
		report: current, password: prepared.LocalDescription().Credentials.Pwd,
		agent: prepared.Agent(), attempt: attempt, connected: connected,
	}, nil
}

func runMigrationResponder(ctx context.Context, value *report, parsed options, transport host.Host, sessionStack *stack, config icesignal.Config) error {
	service, err := icesignal.NewService(
		transport, transport.ID(),
		icesignal.SessionFactoryFunc(func(ctx context.Context, _ peer.ID, _ icesession.Description) (*icesession.PreparedSession, error) {
			return sessionStack.engine.Prepare(ctx)
		}),
		icesignal.AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }),
		config,
	)
	if err != nil {
		return err
	}
	defer service.Close()
	var awgTransport *awgDataPlane
	defer func() {
		if awgTransport != nil {
			_ = awgTransport.Close()
		}
	}()
	if err := writeJSON(parsed.ready, *value); err != nil {
		return err
	}
	connectedSessions := make([]*iceconnect.ConnectedSession, 0, 2)
	defer func() {
		for _, connected := range connectedSessions {
			_ = connected.Close()
		}
	}()
	var first reconnectRecord
	for generation := 1; generation <= 2; generation++ {
		accepted, err := service.NextAccepted(ctx)
		if err != nil {
			return err
		}
		attempt, err := responderAttempt(accepted, transport.ID(), config.Validation)
		if err != nil {
			_ = accepted.Local.Close()
			return err
		}
		if err := attempt.LoadRemoteCandidates(ctx); err != nil {
			_ = attempt.Close()
			return err
		}
		connected, err := attempt.Connect(ctx)
		if err != nil {
			return err
		}
		connectedSessions = append(connectedSessions, connected)
		current := *value
		fillAccepted(&current, accepted)
		current.NetworkGeneration = 1
		current.SessionGeneration = uint64(generation)
		if err := fillConnected(&current, connected.Result(), sessionStack.mux.Port()); err != nil {
			return err
		}
		if parsed.awg {
			if generation == 1 {
				if err := runAWGDataPlane(ctx, &current, parsed, sessionStack.mux, connected, connected.Result(), false, &awgTransport); err != nil {
					return err
				}
			} else if err := refreshAWGDataPlane(ctx, &current, parsed, awgTransport, connected, connected.Result()); err != nil {
				return err
			}
		}
		record := reconnectRecord{
			report: current, password: accepted.Local.LocalDescription().Credentials.Pwd,
			agent: accepted.Local.Agent(), attempt: attempt, connected: connected,
		}
		if generation == 1 {
			first = record
			if err := writeJSON(parsed.result+".generation-1", current); err != nil {
				return err
			}
			continue
		}
		current.PreviousSessionID = first.report.SessionID
		current.PreviousLocalUfrag = first.report.LocalUfrag
		current.CredentialsRotated = first.report.SessionID != current.SessionID && first.report.LocalUfrag != current.LocalUfrag && first.password != record.password
		current.PionAgentReplaced = first.agent != record.agent
		*value = current
		return holdConnected(ctx, parsed.release, parsed.result, current)
	}
	return errors.New("migration responder ended unexpectedly")
}

type reconnectSession struct {
	metadata  connsupervisor.SessionMetadata
	states    <-chan ice.ConnectionState
	connected *iceconnect.ConnectedSession
	closeOnce sync.Once
	closeErr  error
}

func (s *reconnectSession) Metadata() connsupervisor.SessionMetadata { return s.metadata }
func (s *reconnectSession) StateChanges() <-chan ice.ConnectionState { return s.states }
func (s *reconnectSession) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.connected.Close() })
	return s.closeErr
}

func runReconnectResponder(ctx context.Context, value *report, parsed options, transport host.Host, sessionStack *stack, config icesignal.Config) error {
	service, err := icesignal.NewService(
		transport, transport.ID(),
		icesignal.SessionFactoryFunc(func(ctx context.Context, _ peer.ID, _ icesession.Description) (*icesession.PreparedSession, error) {
			return sessionStack.engine.Prepare(ctx)
		}),
		icesignal.AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }),
		config,
	)
	if err != nil {
		return err
	}
	defer service.Close()
	var awgTransport *awgDataPlane
	defer func() {
		if awgTransport != nil {
			_ = awgTransport.Close()
		}
	}()
	if err := writeJSON(parsed.ready, *value); err != nil {
		return err
	}
	var first reconnectRecord
	for generation := 1; generation <= 2; generation++ {
		accepted, err := service.NextAccepted(ctx)
		if err != nil {
			return err
		}
		attempt, err := responderAttempt(accepted, transport.ID(), config.Validation)
		if err != nil {
			_ = accepted.Local.Close()
			return err
		}
		if err := attempt.LoadRemoteCandidates(ctx); err != nil {
			_ = attempt.Close()
			return err
		}
		current := *value
		fillAccepted(&current, accepted)
		current.NetworkGeneration = 1
		current.SessionGeneration = uint64(generation)
		if err := waitForConnectStart(ctx, cycleReadyPath(parsed.connectReady, generation), cycleStartPath(parsed.connectStart, generation), current); err != nil {
			_ = attempt.Close()
			return err
		}
		connected, err := attempt.Connect(ctx)
		if err != nil {
			return err
		}
		if err := fillConnected(&current, connected.Result(), sessionStack.mux.Port()); err != nil {
			_ = connected.Close()
			return err
		}
		if parsed.awg {
			if generation == 1 {
				if err := runAWGDataPlane(ctx, &current, parsed, sessionStack.mux, connected, connected.Result(), false, &awgTransport); err != nil {
					_ = connected.Close()
					return err
				}
			} else if err := refreshAWGDataPlane(ctx, &current, parsed, awgTransport, connected, connected.Result()); err != nil {
				_ = connected.Close()
				return err
			}
		}
		record := reconnectRecord{
			report: current, password: accepted.Local.LocalDescription().Credentials.Pwd,
			agent: accepted.Local.Agent(), attempt: attempt, connected: connected,
		}
		if generation == 1 {
			first = record
			if err := writeJSON(parsed.result+".generation-1", current); err != nil {
				return err
			}
			if parsed.transientRecovery || parsed.familyFailover {
				disconnected, recovered, err := waitForPionRecoveryOrFailure(ctx, attempt.ConnectionStateChanges(), func() error {
					current.DisconnectedObserved = true
					return writeJSON(parsed.disconnected, current)
				})
				if err != nil {
					return err
				}
				current.DisconnectedObserved = disconnected
				if recovered {
					if err := refreshConnected(&current, connected, sessionStack.mux.Port()); err != nil {
						return err
					}
					*value = current
					return holdConnected(ctx, parsed.release, parsed.result, current)
				}
				if parsed.transientRecovery {
					return errors.New("Pion entered Failed during transient recovery")
				}
				current.FailedObserved = true
				if err := writeJSON(parsed.failed, current); err != nil {
					return err
				}
				if err := connected.Close(); err != nil {
					return err
				}
				continue
			}
			disconnected, err := waitForPionFailure(ctx, attempt.ConnectionStateChanges())
			current.DisconnectedObserved = disconnected
			current.FailedObserved = err == nil
			if err != nil {
				return err
			}
			if err := writeJSON(parsed.failed, current); err != nil {
				return err
			}
			if err := connected.Close(); err != nil {
				return err
			}
			continue
		}
		current.PreviousSessionID = first.report.SessionID
		current.PreviousLocalUfrag = first.report.LocalUfrag
		current.CredentialsRotated = first.report.SessionID != current.SessionID && first.report.LocalUfrag != current.LocalUfrag && first.password != record.password
		current.PionAgentReplaced = first.agent != record.agent
		current.DisconnectedObserved = true
		current.FailedObserved = true
		*value = current
		return holdConnected(ctx, parsed.release, parsed.result, current)
	}
	return errors.New("responder reconnect loop ended unexpectedly")
}

func runReconnectInitiator(ctx context.Context, value *report, parsed options, transport host.Host, sessionStack *stack, config icesignal.Config) error {
	remoteAddress, err := multiaddr.NewMultiaddr(parsed.remote)
	if err != nil {
		return err
	}
	remoteInfo, err := peer.AddrInfoFromP2pAddr(remoteAddress)
	if err != nil {
		return err
	}
	if err := transport.Connect(ctx, *remoteInfo); err != nil {
		return err
	}
	client, err := icesignal.NewClient(transport, config)
	if err != nil {
		return err
	}
	defer client.Close()
	var awgTransport *awgDataPlane
	defer func() {
		if awgTransport != nil {
			_ = awgTransport.Close()
		}
	}()
	if err := writeJSON(parsed.ready, *value); err != nil {
		return err
	}
	var recordsMu sync.Mutex
	records := make(map[connsupervisor.SessionGeneration]reconnectRecord, 2)
	connector := connsupervisor.ConnectorFunc(func(connectContext context.Context, request connsupervisor.Request) (connsupervisor.Session, error) {
		record, session, err := connectReconnectInitiator(connectContext, request, *value, parsed, transport, *remoteInfo, sessionStack, client, config.Validation)
		if err != nil {
			return nil, err
		}
		recordsMu.Lock()
		records[request.SessionGeneration] = record
		recordsMu.Unlock()
		return session, nil
	})
	supervisorConfig := connsupervisor.DefaultConfig(connsupervisor.Intent{
		LocalPeer: transport.ID(), RemotePeer: remoteInfo.ID, Purpose: icesession.PurposeExit, Role: icesession.RoleControlling,
	}, networkstate.Generation(1), connector)
	supervisorConfig.DisconnectedGrace = 5 * time.Second
	supervisorConfig.RetryInitial = 100 * time.Millisecond
	supervisorConfig.RetryMaximum = 500 * time.Millisecond
	supervisor, err := connsupervisor.New(supervisorConfig)
	if err != nil {
		return err
	}
	defer supervisor.Close()
	if err := supervisor.Start(ctx); err != nil {
		return err
	}
	var first reconnectRecord
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-supervisor.Events():
			if !ok {
				return errors.New("connection supervisor stopped before reconnect")
			}
			switch event.Type {
			case connsupervisor.EventConnected:
				first = reconnectRecordForGeneration(&recordsMu, records, event.SessionGeneration)
				if first.connected == nil {
					return errors.New("missing first reconnect record")
				}
				if parsed.awg {
					if err := runAWGDataPlane(ctx, &first.report, parsed, sessionStack.mux, first.connected, first.connected.Result(), true, &awgTransport); err != nil {
						return err
					}
				}
				if err := writeJSON(parsed.result+".generation-1", first.report); err != nil {
					return err
				}
			case connsupervisor.EventDisconnected:
				first.report.DisconnectedObserved = true
				if parsed.disconnected != "" {
					if err := writeJSON(parsed.disconnected, first.report); err != nil {
						return err
					}
				}
			case connsupervisor.EventRecovered:
				if !parsed.transientRecovery && !parsed.familyFailover {
					continue
				}
				first.report.DisconnectedObserved = true
				if err := refreshConnected(&first.report, first.connected, sessionStack.mux.Port()); err != nil {
					return err
				}
				*value = first.report
				return holdConnected(ctx, parsed.release, parsed.result, *value)
			case connsupervisor.EventRestartStarted:
				diagnostics := first.attempt.Diagnostics()
				if !containsConnectionState(diagnostics.ConnectionStates, ice.ConnectionStateFailed) {
					return errors.New("supervisor restarted before Pion entered Failed")
				}
				first.report.FailedObserved = true
				if err := writeJSON(parsed.failed, first.report); err != nil {
					return err
				}
			case connsupervisor.EventRestartSucceeded:
				second := reconnectRecordForGeneration(&recordsMu, records, event.SessionGeneration)
				if second.connected == nil {
					return errors.New("missing second reconnect record")
				}
				second.report.PreviousSessionID = first.report.SessionID
				second.report.PreviousLocalUfrag = first.report.LocalUfrag
				second.report.CredentialsRotated = first.report.SessionID != second.report.SessionID && first.report.LocalUfrag != second.report.LocalUfrag && first.password != second.password
				second.report.PionAgentReplaced = first.agent != second.agent
				second.report.DisconnectedObserved = first.report.DisconnectedObserved
				second.report.FailedObserved = first.report.FailedObserved
				if parsed.awg {
					if err := refreshAWGDataPlane(ctx, &second.report, parsed, awgTransport, second.connected, second.connected.Result()); err != nil {
						return err
					}
				}
				*value = second.report
				return holdConnected(ctx, parsed.release, parsed.result, *value)
			}
		}
	}
}

func connectReconnectInitiator(
	ctx context.Context,
	request connsupervisor.Request,
	base report,
	parsed options,
	transport host.Host,
	remote peer.AddrInfo,
	sessionStack *stack,
	client *icesignal.Client,
	validation icesession.ValidationConfig,
) (reconnectRecord, connsupervisor.Session, error) {
	prepared, err := sessionStack.engine.Prepare(ctx)
	if err != nil {
		return reconnectRecord{}, nil, err
	}
	exchange, err := client.Exchange(ctx, remote.ID, prepared)
	if err != nil {
		_ = prepared.Close()
		return reconnectRecord{}, nil, err
	}
	attempt, err := initiatorAttempt(prepared, exchange, transport.ID(), validation)
	if err != nil {
		_ = prepared.Close()
		return reconnectRecord{}, nil, err
	}
	if err := attempt.LoadRemoteCandidates(ctx); err != nil {
		_ = attempt.Close()
		return reconnectRecord{}, nil, err
	}
	current := base
	fillExchange(&current, prepared, exchange)
	current.NetworkGeneration = uint64(request.NetworkGeneration)
	current.SessionGeneration = uint64(request.SessionGeneration)
	if err := waitForConnectStart(ctx, cycleReadyPath(parsed.connectReady, int(request.SessionGeneration)), cycleStartPath(parsed.connectStart, int(request.SessionGeneration)), current); err != nil {
		_ = attempt.Close()
		return reconnectRecord{}, nil, err
	}
	connected, err := attempt.Connect(ctx)
	if err != nil {
		return reconnectRecord{}, nil, err
	}
	if err := fillConnected(&current, connected.Result(), sessionStack.mux.Port()); err != nil {
		_ = connected.Close()
		return reconnectRecord{}, nil, err
	}
	session := &reconnectSession{
		metadata: connsupervisor.SessionMetadata{
			NetworkGeneration: request.NetworkGeneration, SessionGeneration: request.SessionGeneration,
			SessionID: exchange.SessionID, RemotePeer: remote.ID, Purpose: icesession.PurposeExit,
			LocalUfrag: prepared.LocalDescription().Credentials.Ufrag,
		},
		states: attempt.ConnectionStateChanges(), connected: connected,
	}
	return reconnectRecord{
		report: current, password: prepared.LocalDescription().Credentials.Pwd,
		agent: prepared.Agent(), attempt: attempt, connected: connected,
	}, session, nil
}

func responderAttempt(accepted icesignal.AcceptedSession, localPeer peer.ID, validation icesession.ValidationConfig) (*iceconnect.Attempt, error) {
	config := iceconnect.DefaultConfig()
	config.Validation = validation
	return iceconnect.New(accepted.Local, iceconnect.Binding{
		SessionID: accepted.SessionID, Initiator: accepted.Initiator, Responder: accepted.Responder,
		LocalPeer: localPeer, RemotePeer: accepted.RemotePeer, Purpose: accepted.Purpose,
		LocalRole: accepted.LocalRole, RemoteRole: accepted.RemoteRole,
	}, iceconnect.RemoteState{
		SessionID: accepted.SessionID, PeerID: accepted.RemotePeer,
		Role: accepted.RemoteRole, Description: accepted.RemoteDescription,
	}, config)
}

func initiatorAttempt(prepared *icesession.PreparedSession, exchange icesignal.ExchangeResult, localPeer peer.ID, validation icesession.ValidationConfig) (*iceconnect.Attempt, error) {
	config := iceconnect.DefaultConfig()
	config.Validation = validation
	return iceconnect.New(prepared, iceconnect.Binding{
		SessionID: exchange.SessionID, Initiator: exchange.Initiator, Responder: exchange.Responder,
		LocalPeer: localPeer, RemotePeer: exchange.RemotePeer, Purpose: exchange.Purpose,
		LocalRole: exchange.LocalRole, RemoteRole: exchange.RemoteRole,
	}, iceconnect.RemoteState{
		SessionID: exchange.SessionID, PeerID: exchange.RemotePeer,
		Role: exchange.RemoteRole, Description: exchange.RemoteDescription,
	}, config)
}

func waitForPionFailure(ctx context.Context, states <-chan ice.ConnectionState) (bool, error) {
	disconnected := false
	for {
		select {
		case <-ctx.Done():
			return disconnected, ctx.Err()
		case state := <-states:
			switch state {
			case ice.ConnectionStateDisconnected:
				disconnected = true
			case ice.ConnectionStateFailed:
				return disconnected, nil
			}
		}
	}
}

func waitForPionRecoveryOrFailure(ctx context.Context, states <-chan ice.ConnectionState, onDisconnected func() error) (bool, bool, error) {
	disconnected := false
	for {
		select {
		case <-ctx.Done():
			return disconnected, false, ctx.Err()
		case state, ok := <-states:
			if !ok {
				return disconnected, false, errors.New("ICE connection-state stream closed before recovery or failure")
			}
			switch state {
			case ice.ConnectionStateDisconnected:
				disconnected = true
				if err := onDisconnected(); err != nil {
					return disconnected, false, err
				}
			case ice.ConnectionStateConnected:
				if disconnected {
					return true, true, nil
				}
			case ice.ConnectionStateFailed:
				return disconnected, false, nil
			}
		}
	}
}

func refreshConnected(value *report, connected *iceconnect.ConnectedSession, muxPort uint16) error {
	result, err := connected.CurrentResult()
	if err != nil {
		return err
	}
	value.LearnedCandidates = nil
	return fillConnected(value, result, muxPort)
}

func reconnectRecordForGeneration(mu *sync.Mutex, records map[connsupervisor.SessionGeneration]reconnectRecord, generation connsupervisor.SessionGeneration) reconnectRecord {
	mu.Lock()
	defer mu.Unlock()
	return records[generation]
}

func containsConnectionState(states []ice.ConnectionState, wanted ice.ConnectionState) bool {
	for _, state := range states {
		if state == wanted {
			return true
		}
	}
	return false
}

func cycleReadyPath(prefix string, generation int) string {
	return fmt.Sprintf("%s-%d.json", prefix, generation)
}

func cycleStartPath(prefix string, generation int) string {
	return fmt.Sprintf("%s-%d", prefix, generation)
}

func newStack(ctx context.Context, bind4, bind6 netip.Addr, port int, outerMark uint32, stunServer4Text, srflx4Text, srflx6Text, srflx4Override string, timing icesession.AgentTiming) (*stack, error) {
	iceRegistry, err := iceadapter.NewRegistry(iceadapter.DefaultRegistryConfig())
	if err != nil {
		return nil, err
	}
	stunRegistry, err := stunprobe.NewRegistry(stunprobe.DefaultRegistryConfig())
	if err != nil {
		return nil, err
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.Port = port
	muxConfig.Classifier = socketmux.NewClassifierChain(
		stunprobe.NewClassifier(stunRegistry),
		iceadapter.NewClassifier(iceRegistry),
	)
	muxConfig.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	addresses := make([]netip.Addr, 0, 2)
	if bind4.IsValid() {
		muxConfig.IPv4Policy = socketmux.FamilyRequired
		muxConfig.IPv4Bind = bind4
		addresses = append(addresses, bind4)
	} else {
		muxConfig.IPv4Policy = socketmux.FamilyDisabled
	}
	if bind6.IsValid() {
		muxConfig.IPv6Policy = socketmux.FamilyRequired
		muxConfig.IPv6Bind = bind6
		addresses = append(addresses, bind6)
	} else {
		muxConfig.IPv6Policy = socketmux.FamilyDisabled
	}
	mux, err := socketmux.New(ctx, muxConfig)
	if err != nil {
		return nil, err
	}
	if outerMark != 0 {
		if err := mux.SetMark(outerMark); err != nil {
			_ = mux.Close()
			return nil, fmt.Errorf("mark Irvio transport sockets: %w", err)
		}
	}
	var udp4InodeBeforeSTUN, udp6InodeBeforeSTUN string
	if bind4.IsValid() {
		udp4InodeBeforeSTUN, err = udpSocketInode(mux.Port(), "/proc/net/udp")
		if err != nil {
			_ = mux.Close()
			return nil, err
		}
	}
	if bind6.IsValid() {
		udp6InodeBeforeSTUN, err = udpSocketInode(mux.Port(), "/proc/net/udp6")
		if err != nil {
			_ = mux.Close()
			return nil, err
		}
	}
	phases := icesession.NewPhaseManager()
	engineConfig := iceengine.DefaultConfig(addresses...)
	engineConfig.Session.Logger = muxConfig.Logger
	engineConfig.Logger = muxConfig.Logger
	engineConfig.Session.Validation.AllowLoopback = bind4.IsLoopback() || bind6.IsLoopback()
	engineConfig.Session.Timing = timing
	var observedSrflxIPv4 netip.AddrPort
	var probeServer netip.AddrPort
	if stunServer4Text != "" {
		server, err := netip.ParseAddrPort(stunServer4Text)
		if err != nil || !server.Addr().Is4() || server.Addr().Is4In6() || server.Port() == 0 {
			_ = mux.Close()
			return nil, fmt.Errorf("invalid IPv4 STUN server %q", stunServer4Text)
		}
		probeServer = server
		lease, err := phases.Acquire(ctx, icesession.ControlPhaseSTUN)
		if err != nil {
			_ = mux.Close()
			return nil, fmt.Errorf("acquire STUN phase: %w", err)
		}
		probeConfig := stunprobe.DefaultConfig()
		probeConfig.InitialRTO = 40 * time.Millisecond
		probeConfig.MaxRTO = 80 * time.Millisecond
		probeConfig.MaxAttempts = 3
		client, err := stunprobe.NewClient(mux.Control(), stunRegistry, probeConfig)
		if err != nil {
			lease.Release()
			_ = mux.Close()
			return nil, fmt.Errorf("create same-mux STUN client: %w", err)
		}
		observation, probeErr := client.Probe(ctx, server)
		closeErr := client.Close()
		lease.Release()
		if probeErr != nil || closeErr != nil {
			_ = mux.Close()
			return nil, errors.Join(probeErr, closeErr)
		}
		observedSrflxIPv4 = observation.Mapped
		mapped := observation.Mapped
		if srflx4Override != "" {
			mapped, err = netip.ParseAddrPort(srflx4Override)
			if err != nil || !mapped.Addr().Is4() || mapped.Addr().Is4In6() || mapped.Port() == 0 {
				_ = mux.Close()
				return nil, fmt.Errorf("invalid IPv4 srflx override %q", srflx4Override)
			}
		}
		engineConfig.Session.SrflxObservations = append(engineConfig.Session.SrflxObservations, icesession.SrflxObservation{
			Family: socketmux.IPv4, LocalEndpoint: observation.Local, MappedEndpoint: mapped,
		})
	}
	for _, item := range []struct {
		family socketmux.AddressFamily
		local  netip.Addr
		text   string
	}{
		{socketmux.IPv4, bind4, srflx4Text},
		{socketmux.IPv6, bind6, srflx6Text},
	} {
		if item.text == "" {
			continue
		}
		mapped, err := netip.ParseAddrPort(item.text)
		if err != nil {
			_ = mux.Close()
			return nil, err
		}
		engineConfig.Session.SrflxObservations = append(engineConfig.Session.SrflxObservations, icesession.SrflxObservation{
			Family:         item.family,
			LocalEndpoint:  netip.AddrPortFrom(item.local, mux.Port()),
			MappedEndpoint: mapped,
		})
	}
	lease, err := phases.Acquire(ctx, icesession.ControlPhaseICE)
	if err != nil {
		_ = mux.Close()
		return nil, err
	}
	engine, err := iceengine.New(mux, iceRegistry, lease, engineConfig)
	if err != nil {
		lease.Release()
		_ = mux.Close()
		return nil, err
	}
	return &stack{
		mux: mux, engine: engine, observedSrflxIPv4: observedSrflxIPv4, phases: phases, probeServer: probeServer,
		udp4InodeBeforeSTUN: udp4InodeBeforeSTUN, udp6InodeBeforeSTUN: udp6InodeBeforeSTUN,
	}, nil
}

func verifyPostClose(sessionStack *stack, attempt *iceconnect.Attempt, prepared *icesession.PreparedSession, value *report) error {
	if err := attempt.Close(); err != nil {
		return err
	}
	stats := prepared.Adapter().Stats()
	if stats.ActiveUfrags != 0 || stats.ActiveTransactions != 0 {
		return fmt.Errorf("ICE adapter retained active state: ufrags=%d transactions=%d", stats.ActiveUfrags, stats.ActiveTransactions)
	}
	value.PostCloseAdapterClean = true
	if err := sessionStack.engine.Close(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, err := sessionStack.phases.Acquire(ctx, icesession.ControlPhaseSTUN)
	if err != nil {
		return fmt.Errorf("reacquire STUN phase after ICE: %w", err)
	}
	value.PostCloseSTUNLease = true
	lease.Release()
	if !sessionStack.probeServer.IsValid() {
		return errors.New("post-close Data check requires an IPv4 STUN endpoint")
	}
	if _, err := sessionStack.mux.Data().WriteTo(ctx, []byte("hydra-m5c2-data-path-liveness"), sessionStack.probeServer); err != nil {
		return fmt.Errorf("write Mux.Data after ICE: %w", err)
	}
	value.PostCloseDataWrite = true
	return nil
}

func newHost(ipv4, ipv6 string, socketMark uint32) (host.Host, error) {
	listen := make([]string, 0, 2)
	if ipv4 != "" {
		listen = append(listen, "/ip4/"+ipv4+"/tcp/0")
	}
	if ipv6 != "" {
		listen = append(listen, "/ip6/"+ipv6+"/tcp/0")
	}
	if len(listen) == 0 {
		return nil, errors.New("at least one TCP listen address is required")
	}
	transportOption := libp2p.Transport(tcptransport.NewTCPTransport)
	if socketMark != 0 {
		markedDialer, err := controlsocket.DialerForMark(socketMark)
		if err != nil {
			return nil, err
		}
		transportOption = libp2p.Transport(tcptransport.NewTCPTransport, tcptransport.WithDialerForAddr(markedDialer))
	}
	return libp2p.New(
		libp2p.ListenAddrStrings(listen...),
		libp2p.NoTransports,
		transportOption,
	)
}

func fillAccepted(value *report, accepted icesignal.AcceptedSession) {
	value.SessionID = hex.EncodeToString(accepted.SessionID[:])
	value.RemotePeerID = accepted.RemotePeer.String()
	value.LocalUfrag = accepted.Local.LocalDescription().Credentials.Ufrag
	value.RemoteUfrag = accepted.RemoteDescription.Credentials.Ufrag
	value.LocalCandidates = candidateReports(accepted.Local.LocalDescription())
	value.RemoteCandidates = candidateReports(accepted.RemoteDescription)
	value.ConnectivityRequests = accepted.Local.Adapter().Stats().OutboundTransactionsRegistered
}

func fillExchange(value *report, prepared *icesession.PreparedSession, result icesignal.ExchangeResult) {
	value.SessionID = hex.EncodeToString(result.SessionID[:])
	value.RemotePeerID = result.RemotePeer.String()
	value.LocalUfrag = prepared.LocalDescription().Credentials.Ufrag
	value.RemoteUfrag = result.RemoteDescription.Credentials.Ufrag
	value.LocalCandidates = candidateReports(prepared.LocalDescription())
	value.RemoteCandidates = candidateReports(result.RemoteDescription)
	value.ConnectivityRequests = prepared.Adapter().Stats().OutboundTransactionsRegistered
}

func fillConnected(value *report, result iceconnect.Result, muxPort uint16) error {
	value.Connected = true
	value.Role = uint8(result.Role)
	value.SelectedNetwork = uint8(result.Network)
	value.SelectedLocal = result.LocalEndpoint.String()
	value.SelectedRemote = result.RemoteEndpoint.String()
	value.SelectedLocalType = uint8(result.SelectedLocalCandidate.Type)
	value.SelectedRemoteType = uint8(result.SelectedRemoteCandidate.Type)
	value.Nominated = result.Nominated
	value.PairID = result.PairID
	value.RequestsSent = result.RequestsSent
	value.ResponsesReceived = result.ResponsesReceived
	value.ApplicationPackets = result.ApplicationPacketsSent + result.ApplicationPacketsRecv
	value.ApplicationBytes = result.ApplicationBytesSent + result.ApplicationBytesRecv
	value.AdapterBefore = result.AdapterRequestsBefore
	value.AdapterAfter = result.AdapterRequestsAfter
	value.ConnectivityRequests = result.AdapterRequestsAfter
	for _, candidate := range result.RemoteCandidates {
		item := candidateReport{
			Type: candidate.Type.String(), Network: fmt.Sprint(candidate.Network), Address: candidate.Address.String(),
			Port: candidate.Port, Foundation: candidate.Foundation, Priority: candidate.Priority, Component: candidate.Component,
		}
		if candidate.RelatedAddress.IsValid() {
			item.RelatedAddress = candidate.RelatedAddress.String()
			item.RelatedPort = candidate.RelatedPort
		}
		value.LearnedCandidates = append(value.LearnedCandidates, item)
	}
	if value.LocalIPv4 != "" {
		inode, err := udpSocketInode(muxPort, "/proc/net/udp")
		if err != nil {
			return err
		}
		value.UDP4SocketInodeAfter = inode
	}
	if value.LocalIPv6 != "" {
		inode, err := udpSocketInode(muxPort, "/proc/net/udp6")
		if err != nil {
			return err
		}
		value.UDP6SocketInodeAfter = inode
	}
	return nil
}

func runAWGDataPlane(
	ctx context.Context,
	value *report,
	parsed options,
	mux *socketmux.Mux,
	connected *iceconnect.ConnectedSession,
	iceResult iceconnect.Result,
	initiator bool,
	opened **awgDataPlane,
) error {
	if opened == nil {
		return errors.New("nil AWG transport ownership target")
	}
	innerLocal, err := parseOptionalAddr(parsed.awgInnerLocal)
	if err != nil {
		return err
	}
	innerRemote, err := parseOptionalAddr(parsed.awgInnerRemote)
	if err != nil {
		return err
	}
	if !parsed.awgRealTUN && (!innerLocal.IsValid() || !innerRemote.IsValid() || innerLocal.Is4() != innerRemote.Is4()) {
		return errors.New("AWG inner addresses must be valid and from one family")
	}
	networkGeneration := networkstate.Generation(iceResult.TransportGeneration)
	sessionGeneration := connsupervisor.SessionGeneration(value.SessionGeneration)
	if sessionGeneration == 0 {
		sessionGeneration = 1
		value.SessionGeneration = 1
	}
	selected, err := awg.SelectedEndpointFromICE(
		iceResult, networkGeneration, sessionGeneration, iceResult.RemotePeer,
	)
	if err != nil {
		return err
	}
	privateKey, publicKey, err := awgFixtureKey(byte(parsed.awgLocalKeySeed))
	if err != nil {
		return err
	}
	_, remotePublicKey, err := awgFixtureKey(byte(parsed.awgRemoteKeySeed))
	if err != nil {
		return err
	}
	var tunDevice = tuntest.NewChannelTUN()
	var awgTUN = tunDevice.TUN()
	var tunOwner *tunnel.Linux
	var routing *linuxroute.Manager
	var firewall exitfirewall.Installation
	var forwarding *ipforward.Manager
	allowedIPs := []netip.Prefix{netip.PrefixFrom(innerRemote, innerRemote.BitLen())}
	if parsed.awgRealTUN {
		addresses, parseErr := parsePrefixList(parsed.awgTUNAddresses)
		if parseErr != nil {
			return parseErr
		}
		allowedIPs, parseErr = parseAllowedPrefixList(parsed.awgAllowedIPs)
		if parseErr != nil {
			return parseErr
		}
		tunOwner, err = tunnel.NewLinux(tunnel.Config{Name: parsed.awgTUNName, MTU: tunnel.DefaultMTU, Addresses: addresses})
		if err != nil {
			return err
		}
		defer func() {
			if tunOwner != nil {
				_ = tunOwner.Close()
			}
		}()
		awgTUN, err = tunOwner.Borrow()
		if err != nil {
			return err
		}
		value.AWGTUNName = tunOwner.Name()
		value.AWGTUNIfIndex = tunOwner.IfIndex()
		for _, prefix := range tunOwner.Addresses() {
			value.AWGTUNAddresses = append(value.AWGTUNAddresses, prefix.String())
		}
		if parsed.m7Role != "" {
			if err := mux.SetMark(linuxroute.DefaultOuterMark); err != nil {
				return fmt.Errorf("set M7 outer socket mark: %w", err)
			}
			value.AWGSocketMark = mux.Mark()
			switch parsed.m7Role {
			case "client":
				outer4, outer6 := innerOuterFamilies(parsed)
				routeConfig, configErr := m7RouteConfig(parsed, tunOwner, outer4, outer6)
				if configErr != nil {
					return configErr
				}
				routing, err = linuxroute.Apply(routeConfig)
				if err != nil {
					return err
				}
				state := routing.State()
				value.RoutingOutTable = state.OutTable
				value.RoutingWANTable = state.WANTable
				value.RoutingRules = state.Rules
				value.RoutingRoutes = state.Routes
			case "exit":
				forwarding, err = ipforward.Enable(ctx, ipforward.Config{IPv4: parsed.m7ClientPool4 != "", IPv6: parsed.m7ClientPool6 != ""})
				if err != nil {
					return err
				}
				pool4, parseErr := parseOptionalPrefix(parsed.m7ClientPool4)
				if parseErr != nil {
					return parseErr
				}
				pool6, parseErr := parseOptionalPrefix(parsed.m7ClientPool6)
				if parseErr != nil {
					return parseErr
				}
				var management []netip.Prefix
				if parsed.m7Management != "" {
					management, parseErr = parsePrefixList(parsed.m7Management)
					if parseErr != nil {
						return parseErr
					}
				}
				firewall, err = (exitfirewall.NFTBackend{}).Apply(ctx, exitfirewall.Policy{TUNName: tunOwner.Name(), WANName: parsed.m7WANName, ClientIPv4: pool4, ClientIPv6: pool6, BlockedDestinations: management})
				if err != nil {
					return err
				}
				snapshot, snapshotErr := firewall.Snapshot(ctx)
				if snapshotErr != nil {
					return snapshotErr
				}
				value.ExitFirewallTable = snapshot.TableName
				value.ExitFirewallRules = snapshot.Rules
			default:
				return errors.New("invalid M7 role")
			}
		}
	}
	defer func() {
		if routing != nil {
			_ = routing.Close()
		}
		if firewall != nil {
			_ = firewall.Close()
		}
		if forwarding != nil {
			_ = forwarding.Close()
		}
	}()
	transport, err := awg.NewTransport(awg.Config{
		Mux: mux, NetworkGeneration: networkGeneration, TUN: awgTUN, PrivateKey: privateKey,
		Obfuscation: awgFixtureObfuscation(),
		Peers: []awg.PeerConfig{{
			RemotePeer: iceResult.RemotePeer, PublicKey: remotePublicKey, Endpoint: selected,
			AllowedIPs: allowedIPs,
		}},
	})
	if err != nil {
		return err
	}
	if parsed.m7Role != "" {
		if err := transport.Bind().SetMark(linuxroute.DefaultOuterMark); err != nil {
			return fmt.Errorf("delegate M7 socket mark through IrvioBind: %w", err)
		}
	}
	keepTransport := false
	defer func() {
		if !keepTransport {
			_ = transport.Close()
		}
	}()
	value.AWGPublicFingerprint = publicKey.Fingerprint()
	if iceResult.Network == icesession.NetworkUDP4 {
		value.AWGSocketInode, err = udpSocketInode(mux.Port(), "/proc/net/udp")
	} else {
		value.AWGSocketInode, err = udpSocketInode(mux.Port(), "/proc/net/udp6")
	}
	if err != nil {
		return err
	}
	if value.AWGSocketInode != value.UDP4SocketInodeReady && value.AWGSocketInode != value.UDP6SocketInodeReady {
		return errors.New("AWG did not retain the Irvio UDP socket inode")
	}
	if err := waitForConnectStart(ctx, parsed.awgReady, parsed.awgStart, *value); err != nil {
		return err
	}
	if initiator {
		if err := transport.TriggerHandshake(remotePublicKey); err != nil {
			return err
		}
	}
	if _, err := transport.WaitHandshake(ctx, remotePublicKey); err != nil {
		return err
	}
	value.AWGHandshake = true
	if !parsed.awgRealTUN {
		packet := awgInnerPacket(innerLocal, innerRemote)
		for range 3 {
			select {
			case tunDevice.Outbound <- append([]byte(nil), packet...):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		select {
		case received := <-tunDevice.Inbound:
			if string(received) != string(awgInnerPacket(innerRemote, innerLocal)) {
				return errors.New("decrypted AWG inner packet mismatch")
			}
		case <-ctx.Done():
			return ctx.Err()
		}
		value.AWGInnerPacket = true
	}
	bindStats := transport.Bind().Stats()
	value.AWGSelectedRemote = bindStats.LastSend.String()
	value.AWGSendPackets = bindStats.SendPackets
	value.AWGReceivePackets = bindStats.ReceivePackets
	if bindStats.LastSend != iceResult.RemoteEndpoint || bindStats.SendPackets == 0 || bindStats.ReceivePackets == 0 {
		return errors.New("AWG traffic did not use the selected ICE endpoint through IrvioBind")
	}
	current, err := connected.CurrentResult()
	if err != nil {
		return err
	}
	if current.ApplicationPacketsSent != 0 || current.ApplicationPacketsRecv != 0 ||
		current.ApplicationBytesSent != 0 || current.ApplicationBytesRecv != 0 {
		return errors.New("AWG application traffic leaked through ice.Conn")
	}
	*opened = &awgDataPlane{transport: transport, tun: tunOwner, routing: routing, firewall: firewall, forwarding: forwarding}
	tunOwner = nil
	routing = nil
	firewall = nil
	forwarding = nil
	keepTransport = true
	return nil
}

type awgDataPlane struct {
	transport  *awg.Transport
	tun        *tunnel.Linux
	routing    *linuxroute.Manager
	firewall   exitfirewall.Installation
	forwarding *ipforward.Manager
	closeOnce  sync.Once
	closeErr   error
}

func refreshAWGDataPlane(
	ctx context.Context,
	value *report,
	parsed options,
	dataPlane *awgDataPlane,
	connected *iceconnect.ConnectedSession,
	iceResult iceconnect.Result,
) error {
	if dataPlane == nil || dataPlane.transport == nil || dataPlane.tun == nil {
		return errors.New("AWG data plane is not active")
	}
	networkGeneration := networkstate.Generation(iceResult.TransportGeneration)
	sessionGeneration := connsupervisor.SessionGeneration(value.SessionGeneration)
	selected, err := awg.SelectedEndpointFromICE(iceResult, networkGeneration, sessionGeneration, iceResult.RemotePeer)
	if err != nil {
		return err
	}
	_, remotePublicKey, err := awgFixtureKey(byte(parsed.awgRemoteKeySeed))
	if err != nil {
		return err
	}
	if err := dataPlane.transport.UpdatePeerEndpoint(remotePublicKey, selected); err != nil {
		return err
	}
	if parsed.mode == "reconnect-initiator" {
		if err := dataPlane.transport.TriggerHandshake(remotePublicKey); err != nil {
			return err
		}
	}
	if _, err := dataPlane.transport.WaitHandshake(ctx, remotePublicKey); err != nil {
		return err
	}
	stats, err := waitAWGSelectedSend(ctx, dataPlane.transport, iceResult.RemoteEndpoint)
	if err != nil {
		return err
	}
	value.AWGHandshake = true
	value.AWGSelectedRemote = stats.LastSend.String()
	value.AWGSendPackets = stats.SendPackets
	value.AWGReceivePackets = stats.ReceivePackets
	value.AWGTUNName = dataPlane.tun.Name()
	value.AWGTUNIfIndex = dataPlane.tun.IfIndex()
	value.AWGSocketMark = linuxroute.DefaultOuterMark
	if stats.LastSend != iceResult.RemoteEndpoint || stats.SendPackets == 0 || stats.ReceivePackets == 0 {
		return errors.New("restarted AWG transport did not use the fresh selected ICE endpoint")
	}
	current, err := connected.CurrentResult()
	if err != nil {
		return err
	}
	if current.ApplicationPacketsSent != 0 || current.ApplicationPacketsRecv != 0 ||
		current.ApplicationBytesSent != 0 || current.ApplicationBytesRecv != 0 {
		return errors.New("restarted AWG application traffic leaked through ice.Conn")
	}
	return nil
}

func migrateAWGDataPlane(
	ctx context.Context,
	value *report,
	parsed options,
	dataPlane *awgDataPlane,
	mux *socketmux.Mux,
	connected *iceconnect.ConnectedSession,
	iceResult iceconnect.Result,
) error {
	if dataPlane == nil || dataPlane.transport == nil || dataPlane.tun == nil || mux == nil {
		return errors.New("AWG data plane is not active for migration")
	}
	if err := dataPlane.transport.Close(); err != nil {
		return err
	}
	dataPlane.transport = nil
	if dataPlane.routing != nil {
		if err := dataPlane.routing.Close(); err != nil {
			return err
		}
		dataPlane.routing = nil
	}
	if local, ok := mux.LocalAddr(socketmux.IPv4); ok {
		parsed.m7WAN4Source = local.Addr().String()
	}
	if local, ok := mux.LocalAddr(socketmux.IPv6); ok {
		parsed.m7WAN6Source = local.Addr().String()
	}
	outer4, outer6 := innerOuterFamilies(parsed)
	routeConfig, err := m7RouteConfig(parsed, dataPlane.tun, outer4, outer6)
	if err != nil {
		return err
	}
	routing, err := linuxroute.Apply(routeConfig)
	if err != nil {
		return err
	}
	dataPlane.routing = routing
	borrowed, err := dataPlane.tun.Borrow()
	if err != nil {
		return err
	}
	privateKey, _, err := awgFixtureKey(byte(parsed.awgLocalKeySeed))
	if err != nil {
		_ = borrowed.Close()
		return err
	}
	_, remotePublicKey, err := awgFixtureKey(byte(parsed.awgRemoteKeySeed))
	if err != nil {
		_ = borrowed.Close()
		return err
	}
	allowedIPs, err := parseAllowedPrefixList(parsed.awgAllowedIPs)
	if err != nil {
		_ = borrowed.Close()
		return err
	}
	networkGeneration := networkstate.Generation(iceResult.TransportGeneration)
	sessionGeneration := connsupervisor.SessionGeneration(value.SessionGeneration)
	selected, err := awg.SelectedEndpointFromICE(iceResult, networkGeneration, sessionGeneration, iceResult.RemotePeer)
	if err != nil {
		_ = borrowed.Close()
		return err
	}
	replacement, err := awg.NewTransport(awg.Config{
		Mux: mux, NetworkGeneration: networkGeneration, TUN: borrowed, PrivateKey: privateKey,
		Obfuscation: awgFixtureObfuscation(),
		Peers:       []awg.PeerConfig{{RemotePeer: iceResult.RemotePeer, PublicKey: remotePublicKey, Endpoint: selected, AllowedIPs: allowedIPs}},
	})
	if err != nil {
		_ = borrowed.Close()
		return err
	}
	dataPlane.transport = replacement
	if err := replacement.Bind().SetMark(linuxroute.DefaultOuterMark); err != nil {
		return err
	}
	if parsed.mode == "migration-initiator" {
		if err := replacement.TriggerHandshake(remotePublicKey); err != nil {
			return err
		}
	}
	if _, err := replacement.WaitHandshake(ctx, remotePublicKey); err != nil {
		return err
	}
	value.AWGHandshake = true
	value.AWGTUNName = dataPlane.tun.Name()
	value.AWGTUNIfIndex = dataPlane.tun.IfIndex()
	value.AWGSocketMark = mux.Mark()
	if iceResult.Network == icesession.NetworkUDP4 {
		value.AWGSocketInode, err = udpSocketInode(mux.Port(), "/proc/net/udp")
	} else {
		value.AWGSocketInode, err = udpSocketInode(mux.Port(), "/proc/net/udp6")
	}
	if err != nil {
		return err
	}
	state := routing.State()
	value.RoutingOutTable = state.OutTable
	value.RoutingWANTable = state.WANTable
	value.RoutingRules = state.Rules
	value.RoutingRoutes = state.Routes
	stats, err := waitAWGSelectedSend(ctx, replacement, iceResult.RemoteEndpoint)
	if err != nil {
		return err
	}
	value.AWGSelectedRemote = stats.LastSend.String()
	value.AWGSendPackets = stats.SendPackets
	value.AWGReceivePackets = stats.ReceivePackets
	if stats.LastSend != iceResult.RemoteEndpoint || stats.SendPackets == 0 || stats.ReceivePackets == 0 {
		return errors.New("migrated AWG transport did not use the new selected ICE endpoint")
	}
	current, err := connected.CurrentResult()
	if err != nil {
		return err
	}
	if current.ApplicationPacketsSent != 0 || current.ApplicationPacketsRecv != 0 ||
		current.ApplicationBytesSent != 0 || current.ApplicationBytesRecv != 0 {
		return errors.New("migrated AWG application traffic leaked through ice.Conn")
	}
	return nil
}

func waitAWGSelectedSend(ctx context.Context, transport *awg.Transport, endpoint netip.AddrPort) (awg.BindStats, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		stats := transport.Bind().Stats()
		if stats.LastSend == endpoint && stats.SendPackets > 0 && stats.ReceivePackets > 0 {
			return stats, nil
		}
		select {
		case <-ctx.Done():
			return stats, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (p *awgDataPlane) Close() error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		p.closeErr = errors.Join(closeOptional(p.routing), closeOptional(p.firewall), closeOptional(p.transport), closeOptional(p.forwarding), p.tun.Close())
	})
	return p.closeErr
}

func closeOptional(value io.Closer) error {
	if value == nil {
		return nil
	}
	return value.Close()
}

func innerOuterFamilies(parsed options) (bool, bool) {
	return parsed.bind4Text != "", parsed.bind6Text != ""
}

func m7RouteConfig(parsed options, owner *tunnel.Linux, outer4, outer6 bool) (linuxroute.Config, error) {
	config := linuxroute.DefaultConfig()
	config.TUNIndex = owner.IfIndex()
	config.TUNName = owner.Name()
	config.ProtectIPv4 = parsed.m7Protect4
	config.ProtectIPv6 = parsed.m7Protect6
	config.RejectIPv4 = parsed.m7Reject4
	config.RejectIPv6 = parsed.m7Reject6
	config.OuterIPv4 = outer4
	config.OuterIPv6 = outer6
	var err error
	if outer4 {
		config.WANIPv4, err = parseWANPath(parsed.m7WANName, parsed.m7WAN4Source, parsed.m7WAN4Gateway, parsed.m7WAN4Connected)
		if err != nil {
			return linuxroute.Config{}, err
		}
	}
	if outer6 {
		config.WANIPv6, err = parseWANPath(parsed.m7WANName, parsed.m7WAN6Source, parsed.m7WAN6Gateway, parsed.m7WAN6Connected)
		if err != nil {
			return linuxroute.Config{}, err
		}
	}
	if parsed.m7Bypass != "" {
		config.Bypass, err = parsePrefixList(parsed.m7Bypass)
		if err != nil {
			return linuxroute.Config{}, err
		}
	}
	return config, nil
}

func parseWANPath(name, sourceText, gatewayText, connectedText string) (linuxroute.WANPath, error) {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return linuxroute.WANPath{}, fmt.Errorf("find M7 WAN %s: %w", name, err)
	}
	source, err := parseOptionalAddr(sourceText)
	if err != nil || !source.IsValid() {
		return linuxroute.WANPath{}, errors.New("invalid M7 WAN source")
	}
	gateway, err := parseOptionalAddr(gatewayText)
	if err != nil {
		return linuxroute.WANPath{}, err
	}
	connected, err := parseOptionalPrefix(connectedText)
	if err != nil {
		return linuxroute.WANPath{}, err
	}
	return linuxroute.WANPath{LinkIndex: link.Attrs().Index, Source: source, Gateway: gateway, Connected: connected}, nil
}

func parseOptionalPrefix(value string) (netip.Prefix, error) {
	if value == "" {
		return netip.Prefix{}, nil
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.IsValid() || prefix.Addr().Is4In6() || prefix.Bits() == 0 {
		return netip.Prefix{}, fmt.Errorf("invalid prefix %q", value)
	}
	return prefix, nil
}

func parsePrefixList(value string) ([]netip.Prefix, error) {
	return parsePrefixListWithDefaults(value, false)
}

func parseAllowedPrefixList(value string) ([]netip.Prefix, error) {
	return parsePrefixListWithDefaults(value, true)
}

func parsePrefixListWithDefaults(value string, allowDefaults bool) ([]netip.Prefix, error) {
	if value == "" {
		return nil, errors.New("empty prefix list")
	}
	parts := strings.Split(value, ",")
	if len(parts) > 16 {
		return nil, errors.New("too many prefixes")
	}
	result := make([]netip.Prefix, 0, len(parts))
	seen := make(map[netip.Prefix]struct{}, len(parts))
	for _, part := range parts {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil || !prefix.IsValid() || prefix.Addr().IsMulticast() || prefix.Addr().Is4In6() || prefix.Addr().IsUnspecified() && (!allowDefaults || prefix.Bits() != 0) {
			return nil, fmt.Errorf("invalid prefix %q", part)
		}
		if _, ok := seen[prefix]; ok {
			return nil, fmt.Errorf("duplicate prefix %s", prefix)
		}
		seen[prefix] = struct{}{}
		result = append(result, prefix)
	}
	return result, nil
}

func awgFixtureKey(seed byte) (awg.Key, awg.Key, error) {
	var private awg.Key
	for index := range private {
		private[index] = seed + byte(index)
	}
	encoded, err := curve25519.X25519(private[:], curve25519.Basepoint)
	if err != nil {
		return awg.Key{}, awg.Key{}, err
	}
	var public awg.Key
	copy(public[:], encoded)
	return private, public, nil
}

func awgFixtureObfuscation() awg.ObfuscationConfig {
	return awg.ObfuscationConfig{
		JunkPacketCount: 1, JunkMin: 24, JunkMax: 24,
		InitPadding: 16, ResponsePadding: 16, CookiePadding: 8,
		InitHeader: 101, ResponseHeader: 202, CookieHeader: 303, TransportHeader: 404,
		SignaturePackets: [5]string{"<b 0x4859445241>"},
	}
}

func awgInnerPacket(source, destination netip.Addr) []byte {
	if source.Is4() {
		packet := make([]byte, 28)
		packet[0] = 0x45
		binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
		packet[8], packet[9] = 64, 17
		copy(packet[12:16], source.AsSlice())
		copy(packet[16:20], destination.AsSlice())
		packet[27] = 0x61
		return packet
	}
	packet := make([]byte, 48)
	packet[0] = 0x60
	binary.BigEndian.PutUint16(packet[4:6], 8)
	packet[6], packet[7] = 17, 64
	copy(packet[8:24], source.AsSlice())
	copy(packet[24:40], destination.AsSlice())
	packet[47] = 0x61
	return packet
}

func waitForConnectStart(ctx context.Context, readyPath, startPath string, value report) error {
	if readyPath == "" && startPath == "" {
		return nil
	}
	if readyPath == "" || startPath == "" {
		return errors.New("connect readiness and start paths must be configured together")
	}
	if err := writeJSON(readyPath, value); err != nil {
		return err
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, err := os.Stat(startPath); err == nil && info.Mode().IsRegular() {
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

func udpSocketInode(port uint16, procPath string) (string, error) {
	owned := make(map[string]struct{})
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		target, err := os.Readlink("/proc/self/fd/" + entry.Name())
		if err == nil && strings.HasPrefix(target, "socket:[") && strings.HasSuffix(target, "]") {
			owned[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] = struct{}{}
		}
	}
	raw, err := os.ReadFile(procPath)
	if err != nil {
		return "", err
	}
	wantPort := strings.ToUpper(strconv.FormatUint(uint64(port), 16))
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		parts := strings.Split(fields[1], ":")
		if len(parts) != 2 || strings.TrimLeft(parts[1], "0") != strings.TrimLeft(wantPort, "0") {
			continue
		}
		if _, ok := owned[fields[9]]; ok {
			return fields[9], nil
		}
	}
	return "", fmt.Errorf("find owned UDP socket inode for port %d", port)
}

func holdConnected(ctx context.Context, releasePath, resultPath string, value report) error {
	if releasePath == "" {
		return nil
	}
	if err := writeJSON(resultPath, value); err != nil {
		return err
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, err := os.Stat(releasePath); err == nil && info.Mode().IsRegular() {
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

func candidateReports(description icesession.Description) []candidateReport {
	result := make([]candidateReport, 0, len(description.Candidates))
	for _, candidate := range description.Candidates {
		item := candidateReport{
			Type: fmt.Sprint(candidate.Type), Network: fmt.Sprint(candidate.Network),
			Address: candidate.Address.String(), Port: candidate.Port,
			Foundation: candidate.Foundation, Priority: candidate.Priority, Component: candidate.Component,
		}
		if candidate.RelatedAddress.IsValid() {
			item.RelatedAddress = candidate.RelatedAddress.String()
			item.RelatedPort = candidate.RelatedPort
		}
		result = append(result, item)
	}
	return result
}

func parseOptions() (options, error) {
	var value options
	flag.StringVar(&value.mode, "mode", "", "responder or initiator")
	flag.StringVar(&value.bind4Text, "bind4", "", "concrete UDP IPv4 bind address")
	flag.StringVar(&value.bind6Text, "bind6", "", "concrete UDP IPv6 bind address")
	flag.StringVar(&value.tcp4Text, "tcp4", "", "concrete TCP IPv4 listen address")
	flag.StringVar(&value.tcp6Text, "tcp6", "", "concrete TCP IPv6 listen address")
	flag.StringVar(&value.remote, "remote", "", "remote libp2p /p2p multiaddr")
	flag.StringVar(&value.stunServer4, "stun-server4", "", "IPv4 STUN server probed through this Irvio mux before ICE")
	flag.StringVar(&value.srflx4, "srflx4", "", "verified IPv4 mapped endpoint")
	flag.StringVar(&value.srflx6, "srflx6", "", "verified IPv6 mapped endpoint")
	flag.StringVar(&value.srflx4Override, "srflx4-override", "", "test-only replacement for an observed IPv4 mapped endpoint")
	flag.StringVar(&value.ready, "ready-file", "", "machine-readable readiness path")
	flag.StringVar(&value.result, "result-file", "", "machine-readable result path")
	flag.StringVar(&value.release, "release-file", "", "optional file that releases a connected helper")
	flag.StringVar(&value.connectReady, "connect-ready-file", "", "optional candidate-loaded readiness path")
	flag.StringVar(&value.connectStart, "connect-start-file", "", "optional connectivity start barrier path")
	flag.StringVar(&value.disconnected, "disconnected-file", "", "optional disconnected-state readiness path")
	flag.StringVar(&value.failed, "failed-file", "", "optional failed-state readiness path")
	flag.IntVar(&value.port, "port", 0, "Irvio shared UDP port")
	flag.DurationVar(&value.timeout, "timeout", 15*time.Second, "bounded helper lifetime")
	flag.DurationVar(&value.disconnectedTimeout, "ice-disconnected-timeout", 0, "test-only Pion disconnected timeout")
	flag.DurationVar(&value.failedTimeout, "ice-failed-timeout", 0, "test-only Pion failed timeout")
	flag.DurationVar(&value.keepaliveInterval, "ice-keepalive-interval", 0, "test-only Pion keepalive interval")
	flag.DurationVar(&value.checkInterval, "ice-check-interval", 0, "test-only Pion check interval")
	flag.BoolVar(&value.verifyPostClose, "verify-post-close", false, "verify ICE cleanup while preserving the Irvio mux")
	flag.BoolVar(&value.transientRecovery, "transient-recovery", false, "expect disconnected-to-connected recovery without restart")
	flag.BoolVar(&value.familyFailover, "family-failover", false, "accept natural selected-family recovery or a fresh ICE generation")
	flag.BoolVar(&value.awg, "awg", false, "run real AmneziaWG over the selected ICE endpoint")
	flag.StringVar(&value.awgReady, "awg-ready-file", "", "AWG configured readiness path")
	flag.StringVar(&value.awgStart, "awg-start-file", "", "AWG handshake start barrier path")
	flag.StringVar(&value.awgInnerLocal, "awg-inner-local", "", "local inner test address")
	flag.StringVar(&value.awgInnerRemote, "awg-inner-remote", "", "remote inner test address")
	flag.BoolVar(&value.awgRealTUN, "awg-real-tun", false, "use a real long-lived Linux TUN")
	flag.StringVar(&value.awgTUNName, "awg-tun-name", "hydra0", "real Linux TUN name")
	flag.StringVar(&value.awgTUNAddresses, "awg-tun-addresses", "", "comma-separated real TUN local prefixes")
	flag.StringVar(&value.awgAllowedIPs, "awg-allowed-ips", "", "comma-separated real AWG peer AllowedIPs")
	flag.StringVar(&value.m7Role, "m7-role", "", "M7 role: client or exit")
	flag.StringVar(&value.m7WANName, "m7-wan-name", "", "M7 physical WAN interface")
	flag.StringVar(&value.m7WAN4Source, "m7-wan4-source", "", "M7 WAN IPv4 source")
	flag.StringVar(&value.m7WAN4Gateway, "m7-wan4-gateway", "", "M7 WAN IPv4 gateway")
	flag.StringVar(&value.m7WAN4Connected, "m7-wan4-connected", "", "M7 WAN IPv4 connected prefix")
	flag.StringVar(&value.m7WAN6Source, "m7-wan6-source", "", "M7 WAN IPv6 source")
	flag.StringVar(&value.m7WAN6Gateway, "m7-wan6-gateway", "", "M7 WAN IPv6 gateway")
	flag.StringVar(&value.m7WAN6Connected, "m7-wan6-connected", "", "M7 WAN IPv6 connected prefix")
	flag.StringVar(&value.m7Bypass, "m7-bypass", "", "comma-separated main-table bypass prefixes")
	flag.StringVar(&value.m7Management, "m7-management", "", "comma-separated Exit management prefixes to isolate")
	flag.BoolVar(&value.m7Protect4, "m7-protect4", false, "route protected IPv4 through hydra0")
	flag.BoolVar(&value.m7Protect6, "m7-protect6", false, "route protected IPv6 through hydra0")
	flag.BoolVar(&value.m7Reject4, "m7-reject4", false, "explicitly reject unsupported IPv4")
	flag.BoolVar(&value.m7Reject6, "m7-reject6", false, "explicitly reject unsupported IPv6")
	flag.StringVar(&value.m7ClientPool4, "m7-client-pool4", "", "Exit downstream IPv4 pool")
	flag.StringVar(&value.m7ClientPool6, "m7-client-pool6", "", "Exit downstream IPv6 pool")
	flag.StringVar(&value.m7MultiConfig, "m7-multi-config", "", "bounded multi-peer AWG configuration file")
	flag.IntVar(&value.m7MultiCount, "m7-multi-count", 0, "number of concurrent M7 downstream peers")
	flag.BoolVar(&value.m8Protect, "m8-protect", false, "install fail-closed M8 client protection before transport setup")
	flag.IntVar(&value.awgLocalKeySeed, "awg-local-key-seed", 0, "non-secret deterministic AWG test key seed")
	flag.IntVar(&value.awgRemoteKeySeed, "awg-remote-key-seed", 0, "non-secret deterministic remote AWG test key seed")
	flag.Parse()
	validMode := value.mode == "initiator" || value.mode == "responder" ||
		value.mode == "connect-initiator" || value.mode == "connect-responder" ||
		value.mode == "connect-multi-responder" ||
		value.mode == "reconnect-initiator" || value.mode == "reconnect-responder" ||
		value.mode == "migration-initiator" || value.mode == "migration-responder"
	if !validMode || value.ready == "" || value.result == "" ||
		value.timeout <= 0 || value.port < 0 || value.port > 65535 {
		return options{}, errors.New("invalid mode, path, timeout, or port")
	}
	if value.bind4Text == "" && value.bind6Text == "" {
		return options{}, errors.New("at least one UDP bind address is required")
	}
	if (value.mode == "initiator" || value.mode == "connect-initiator" || value.mode == "reconnect-initiator" || value.mode == "migration-initiator") && value.remote == "" {
		return options{}, errors.New("remote multiaddr is required")
	}
	if value.stunServer4 != "" && value.srflx4 != "" {
		return options{}, errors.New("stun-server4 and srflx4 are mutually exclusive")
	}
	if value.srflx4Override != "" && value.stunServer4 == "" {
		return options{}, errors.New("srflx4-override requires a real stun-server4 observation")
	}
	if strings.HasPrefix(value.mode, "reconnect-") && (value.connectReady == "" || value.connectStart == "" ||
		value.disconnected == "" || (!value.transientRecovery && value.failed == "")) {
		return options{}, errors.New("reconnect mode requires connect-ready, connect-start, and lifecycle state paths")
	}
	if value.awg {
		multi := value.mode == "connect-multi-responder"
		reconnect := strings.HasPrefix(value.mode, "reconnect-")
		migration := strings.HasPrefix(value.mode, "migration-")
		if value.mode != "connect-initiator" && value.mode != "connect-responder" && !multi && !reconnect && !migration {
			return options{}, errors.New("AWG is supported only by connect, reconnect, and migration helper modes")
		}
		if value.awgReady == "" || value.awgStart == "" || (!value.awgRealTUN && (value.awgInnerLocal == "" || value.awgInnerRemote == "")) ||
			(value.awgRealTUN && (value.awgTUNName == "" || value.awgTUNAddresses == "" || !multi && value.awgAllowedIPs == "")) ||
			value.awgLocalKeySeed < 1 || value.awgLocalKeySeed > 255 ||
			(!multi && (value.awgRemoteKeySeed < 1 || value.awgRemoteKeySeed > 255 || value.awgLocalKeySeed == value.awgRemoteKeySeed)) ||
			(multi && (value.m7MultiCount < 2 || value.m7MultiCount > 8 || value.m7MultiConfig == "")) {
			return options{}, errors.New("invalid bounded AWG test configuration")
		}
	}
	if value.m7Role != "" && (!value.awgRealTUN || (value.m7Role != "client" && value.m7Role != "exit") || value.m7WANName == "") {
		return options{}, errors.New("invalid M7 real-TUN role configuration")
	}
	if value.m7Role == "client" && (!value.m7Protect4 && !value.m7Protect6 && !value.m7Reject4 && !value.m7Reject6) {
		return options{}, errors.New("M7 client must protect or reject at least one family")
	}
	if value.m7Role == "exit" && value.m7ClientPool4 == "" && value.m7ClientPool6 == "" {
		return options{}, errors.New("M7 Exit needs at least one client pool")
	}
	if value.m8Protect && value.m7Role != "client" {
		return options{}, errors.New("M8 protection requires the M7 client role")
	}
	return value, nil
}

func parseOptionalAddr(value string) (netip.Addr, error) {
	if value == "" {
		return netip.Addr{}, nil
	}
	address, err := netip.ParseAddr(value)
	if err != nil || address.IsUnspecified() || address.IsMulticast() || address.Is4In6() {
		return netip.Addr{}, fmt.Errorf("invalid concrete address %q", value)
	}
	return address, nil
}

func writeJSON(path string, value any) error {
	temporary := path + fmt.Sprintf(".tmp-%d-%d", os.Getpid(), time.Now().UnixNano())
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encodeErr := json.NewEncoder(file).Encode(value)
	closeErr := file.Close()
	if encodeErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		return errors.Join(encodeErr, closeErr)
	}
	return os.Rename(temporary, path)
}
