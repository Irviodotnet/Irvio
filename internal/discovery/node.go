// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// Package discovery owns a IRVIO node's libp2p and Kademlia lifecycle.
package discovery

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/netip"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
	"google.golang.org/protobuf/proto"

	"github.com/Irviodotnet/Irvio/internal/bootstrap"
	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	"github.com/Irviodotnet/Irvio/internal/identity"
	"github.com/Irviodotnet/Irvio/internal/peerstore"
	irvioprotocol "github.com/Irviodotnet/Irvio/internal/protocol"
	"github.com/Irviodotnet/Irvio/internal/reachability"
	"github.com/Irviodotnet/Irvio/internal/serviceindex"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

const (
	dhtProtocolPrefix          libprotocol.ID = "/hydra"
	defaultAnnouncementTTL                    = 10 * time.Minute
	defaultAnnouncementRefresh                = 5 * time.Minute
	defaultProviderRefresh                    = 12 * time.Hour
	maxProviderResults                        = 32
)

// Config configures one independent IRVIO node.
type Config struct {
	DataDir         string
	ListenPort      int
	Bootstrap       []string
	Exit            bool
	Country         string
	Logger          *slog.Logger
	ProviderRefresh time.Duration
	// AnnouncementTTL and AnnouncementRefresh allow accelerated tests within M12 bounds.
	AnnouncementTTL     time.Duration
	AnnouncementRefresh time.Duration
	DHTMode             string
	// DeferExitAdvertisement withholds CAP_EXIT and DHT publication until ActivateExit.
	DeferExitAdvertisement bool
	// ControlMark enables M8 WAN bypass for libp2p; zero leaves sockets unmarked.
	ControlMark uint32
	// AWGPublicKey and AWGKeyEpoch publish the separate transport identity in signed announcements.
	AWGPublicKey []byte
	AWGKeyEpoch  uint64
}

// Node owns all runtime resources of one IRVIO node.
type Node struct {
	directory       *peerstore.Directory
	host            host.Host
	dht             *dht.IpfsDHT
	logger          *slog.Logger
	country         string
	dbPath          string
	privateKey      crypto.PrivKey
	bootstrapPeers  []peer.AddrInfo
	rebootstrapMu   sync.Mutex
	nextRebootstrap time.Time
	publisher       *irvioprotocol.PublisherState
	controlMark     uint32
	server          *irvioprotocol.AnnouncementServer
	announcement    *irviov1.NodeAnnouncement
	signedEnvelope  []byte
	announcementMu  sync.Mutex

	exitConfigured      bool
	providerRefresh     time.Duration
	announcementTTL     time.Duration
	announcementRefresh time.Duration
	providerMu          sync.Mutex
	providerStarted     bool
	providerLoopStarted bool
	rendezvousStarted   bool
	rendezvousActive    bool
	rendezvousCountries map[string]struct{}
	rendezvousPublished map[string]time.Time
	rendezvousPublishMu sync.Mutex
	closed              bool

	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

// Start creates and bootstraps one node.
func Start(parent context.Context, config Config) (*Node, error) {
	if config.DataDir == "" {
		return nil, fmt.Errorf("data directory is required")
	}
	if config.ListenPort < 0 || config.ListenPort > 65535 {
		return nil, fmt.Errorf("listen port %d is invalid", config.ListenPort)
	}
	mode, err := parseDHTMode(config.DHTMode)
	if err != nil {
		return nil, err
	}
	country := ""
	if config.Country != "" {
		country, err = serviceindex.NormalizeCountry(config.Country)
		if err != nil {
			return nil, fmt.Errorf("invalid country: %w", err)
		}
	}
	if config.Exit && country == "" && !config.DeferExitAdvertisement {
		return nil, fmt.Errorf("exit capability requires --country")
	}
	if len(config.AWGPublicKey) != 0 && len(config.AWGPublicKey) != 32 || len(config.AWGPublicKey) == 0 && config.AWGKeyEpoch != 0 || len(config.AWGPublicKey) == 32 && config.AWGKeyEpoch == 0 {
		return nil, fmt.Errorf("AWG public key and positive epoch must be supplied together")
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if config.ProviderRefresh <= 0 {
		config.ProviderRefresh = defaultProviderRefresh
	}
	if config.AnnouncementTTL == 0 {
		config.AnnouncementTTL = defaultAnnouncementTTL
	}
	if config.AnnouncementRefresh == 0 {
		config.AnnouncementRefresh = defaultAnnouncementRefresh
	}
	if config.AnnouncementTTL <= 0 || config.AnnouncementTTL > defaultAnnouncementTTL ||
		config.AnnouncementRefresh <= 0 || config.AnnouncementRefresh >= config.AnnouncementTTL {
		return nil, fmt.Errorf("invalid announcement timing policy")
	}

	nodeIdentity, err := identity.LoadOrCreate(config.DataDir)
	if err != nil {
		return nil, fmt.Errorf("load node identity: %w", err)
	}
	identityEvent := "IDENTITY_LOADED"
	if nodeIdentity.Created {
		identityEvent = "IDENTITY_CREATED"
	}
	logger.Info(identityEvent, "peer_id", nodeIdentity.PeerID)

	dbPath := filepath.Join(config.DataDir, "hydra.db")
	directory, err := peerstore.Open(parent, dbPath)
	if err != nil {
		return nil, err
	}
	h, ipv6, err := newHost(nodeIdentity, config.ListenPort, config.ControlMark)
	if err != nil {
		directory.Close()
		return nil, err
	}
	logger.Info("LIBP2P_STARTED", "peer_id", h.ID(), "ipv6_listen", ipv6, "listen_addrs", h.Addrs())

	kademlia, err := dht.New(h, dht.Mode(mode), dht.ProtocolPrefix(dhtProtocolPrefix))
	if err != nil {
		h.Close()
		directory.Close()
		return nil, fmt.Errorf("create Kademlia DHT: %w", err)
	}
	publisher, err := irvioprotocol.AcquirePublisherState(config.DataDir)
	if err != nil {
		_ = kademlia.Close()
		_ = h.Close()
		_ = directory.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	node := &Node{
		directory: directory, host: h, dht: kademlia,
		logger: logger, country: country, dbPath: dbPath, privateKey: nodeIdentity.PrivateKey,
		controlMark: config.ControlMark, exitConfigured: config.Exit, publisher: publisher,
		providerRefresh: config.ProviderRefresh, announcementTTL: config.AnnouncementTTL,
		announcementRefresh: config.AnnouncementRefresh, rendezvousCountries: make(map[string]struct{}),
		ctx: ctx, cancel: cancel,
	}
	fail := func(cause error) (*Node, error) {
		_ = node.Close()
		return nil, cause
	}

	server := irvioprotocol.RegisterAnnouncementServer(h, logger)
	epoch, err := irvioprotocol.NewSessionEpoch()
	if err != nil {
		return fail(err)
	}
	capabilities := irvioprotocol.CapDiscovery
	if config.Exit && !config.DeferExitAdvertisement {
		capabilities |= irvioprotocol.CapExit
	}
	now := time.Now()
	announcement := &irviov1.NodeAnnouncement{
		ProtocolVersion: irvioprotocol.ProtocolVersion,
		NodeId:          []byte(h.ID()),
		SessionEpoch:    epoch,
		Incarnation:     publisher.Incarnation(),
		Sequence:        1,
		Capabilities:    capabilities,
		Libp2PAddrs:     node.ListenAddresses(),
		Country:         country,
		AwgPublicKey:    append([]byte(nil), config.AWGPublicKey...),
		AwgKeyEpoch:     config.AWGKeyEpoch,
		CreatedAtUnix:   now.Unix(),
		ExpiresAtUnix:   now.Add(node.announcementTTL).Unix(),
	}
	signed, err := irvioprotocol.SealAnnouncement(nodeIdentity.PrivateKey, announcement, now)
	if err != nil {
		return fail(fmt.Errorf("create local announcement: %w", err))
	}
	if err := server.SetEnvelope(signed); err != nil {
		return fail(err)
	}
	node.server = server
	node.announcement = announcement
	node.signedEnvelope = append([]byte(nil), signed...)
	logger.Info("NODE_ANNOUNCEMENT_CREATED", "peer_id", h.ID(), "country", country, "sequence", 1)

	bootstrapPeers, err := bootstrap.Parse(config.Bootstrap)
	if err != nil {
		return fail(err)
	}
	node.bootstrapPeers = bootstrapPeers
	if err := bootstrap.Connect(ctx, h, bootstrapPeers, logger); err != nil {
		return fail(err)
	}
	if err := kademlia.Bootstrap(ctx); err != nil {
		return fail(fmt.Errorf("bootstrap Kademlia DHT: %w", err))
	}
	if len(bootstrapPeers) > 0 && !waitForRoutingPeer(ctx, kademlia, 5*time.Second) {
		return fail(fmt.Errorf("Kademlia routing table remained empty after bootstrap"))
	}
	logger.Info("DHT_BOOTSTRAP_COMPLETE", "peer_id", h.ID())

	if config.Exit && !config.DeferExitAdvertisement {
		if err := node.ActivateExit(ctx); err != nil {
			return fail(err)
		}
	}
	node.wg.Add(1)
	go node.announcementLoop(node.announcementRefresh)
	return node, nil
}

// EnsureRoutingPeers retries the full bootstrap bundle only when the DHT routing table is empty.
func (n *Node) EnsureRoutingPeers(ctx context.Context) error {
	if n == nil || ctx == nil {
		return fmt.Errorf("invalid Kademlia rebootstrap request")
	}
	if n.dht.RoutingTable().Size() > 0 {
		return nil
	}
	n.rebootstrapMu.Lock()
	defer n.rebootstrapMu.Unlock()
	if n.dht.RoutingTable().Size() > 0 {
		return nil
	}
	if len(n.bootstrapPeers) == 0 {
		return fmt.Errorf("Kademlia routing table is empty and no bootstrap peers are configured")
	}
	if time.Now().Before(n.nextRebootstrap) {
		return fmt.Errorf("Kademlia rebootstrap is rate limited")
	}
	n.nextRebootstrap = time.Now().Add(5 * time.Second)
	if err := bootstrap.Connect(ctx, n.host, n.bootstrapPeers, n.logger); err != nil {
		return fmt.Errorf("reconnect bootstrap peers: %w", err)
	}
	if err := n.dht.Bootstrap(ctx); err != nil {
		return fmt.Errorf("bootstrap Kademlia after peer loss: %w", err)
	}
	if !waitForRoutingPeer(ctx, n.dht, 2*time.Second) {
		// Validate evicted peers with a DHT request; existing connections may not emit Identify again.
		probeCtx, stop := context.WithTimeout(ctx, 3*time.Second)
		reconnect := false
		for _, info := range n.bootstrapPeers {
			if probeCtx.Err() != nil || n.dht.RoutingTable().Size() > 0 {
				break
			}
			peerCtx, cancel := context.WithTimeout(probeCtx, time.Second)
			pingErr := n.dht.Ping(peerCtx, info.ID)
			cancel()
			if pingErr == nil {
				// TryAddPeer still applies the DHT routing-table filters.
				_, _ = n.dht.RoutingTable().TryAddPeer(info.ID, true, true)
			} else if ctx.Err() == nil && len(n.host.Network().ConnsToPeer(info.ID)) > 0 {
				// Only a DHT-unresponsive bootstrap connection is reopened.
				_ = n.host.Network().ClosePeer(info.ID)
				reconnect = true
			}
		}
		stop()
		if reconnect && n.dht.RoutingTable().Size() == 0 {
			if err := bootstrap.Connect(ctx, n.host, n.bootstrapPeers, n.logger); err != nil {
				return fmt.Errorf("reconnect DHT-unresponsive bootstrap peers: %w", err)
			}
			if err := n.dht.Bootstrap(ctx); err != nil {
				return fmt.Errorf("bootstrap Kademlia after reconnect: %w", err)
			}
		}
		if !waitForRoutingPeer(ctx, n.dht, 2*time.Second) {
			return fmt.Errorf("Kademlia routing table remained empty after rebootstrap")
		}
	}
	n.logger.Info("DHT_REBOOTSTRAP_COMPLETE", "peer_id", n.host.ID(), "routing_peers", n.dht.RoutingTable().Size())
	return nil
}

func parseDHTMode(value string) (dht.ModeOpt, error) {
	switch value {
	case "", "server":
		return dht.ModeServer, nil
	case "auto":
		return dht.ModeAuto, nil
	default:
		return 0, fmt.Errorf("DHT mode %q is invalid; use server or auto", value)
	}
}

func newHost(nodeIdentity *identity.Identity, port int, controlMark uint32) (host.Host, bool, error) {
	ipv4 := fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", port)
	ipv6 := fmt.Sprintf("/ip6/::/tcp/%d", port)
	transportOption := libp2p.Transport(tcptransport.NewTCPTransport)
	if controlMark != 0 {
		dialer, err := controlsocket.DialerForMark(controlMark)
		if err != nil {
			return nil, false, err
		}
		transportOption = libp2p.Transport(tcptransport.NewTCPTransport, tcptransport.WithDialerForAddr(dialer))
	}
	h, err := libp2p.New(
		libp2p.Identity(nodeIdentity.PrivateKey),
		libp2p.NoTransports,
		transportOption,
		libp2p.DisableRelay(),
		libp2p.ListenAddrStrings(ipv4, ipv6),
	)
	if err == nil {
		return h, true, nil
	}
	h, ipv4Err := libp2p.New(
		libp2p.Identity(nodeIdentity.PrivateKey),
		libp2p.NoTransports,
		transportOption,
		libp2p.DisableRelay(),
		libp2p.ListenAddrStrings(ipv4),
	)
	if ipv4Err != nil {
		return nil, false, fmt.Errorf("create dual-stack host (%v), IPv4 fallback: %w", err, ipv4Err)
	}
	return h, false, nil
}

func waitForRoutingPeer(ctx context.Context, kademlia *dht.IpfsDHT, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if kademlia.RoutingTable().Size() > 0 {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
			if err := kademlia.Bootstrap(ctx); err != nil && ctx.Err() != nil {
				return false
			}
		}
	}
}

func (n *Node) provideExitWithRetry(ctx context.Context) error {
	serviceCID, service, err := serviceindex.ExitCID(n.country)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 8; attempt++ {
		if err := n.dht.Provide(ctx, serviceCID, true); err == nil {
			n.logger.Info("SERVICE_ADVERTISE", "peer_id", n.host.ID(), "country", n.country, "service", service)
			return nil
		} else {
			lastErr = err
		}
		delay := time.Duration(attempt+1) * 100 * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("advertise %s: %w", service, ctx.Err())
		case <-timer.C:
		}
	}
	return fmt.Errorf("advertise %s: %w", service, lastErr)
}

// ActivateExit publishes CAP_EXIT and providers only after Exit services are ready.
func (n *Node) ActivateExit(ctx context.Context) error {
	if n == nil || ctx == nil {
		return fmt.Errorf("invalid Exit activation")
	}
	n.providerMu.Lock()
	defer n.providerMu.Unlock()
	if n.closed {
		return fmt.Errorf("discovery node is closed")
	}
	if !n.exitConfigured || n.country == "" {
		return fmt.Errorf("Exit capability was not configured")
	}
	if n.providerStarted {
		return nil
	}
	if err := n.publishExitCapability(); err != nil {
		return err
	}
	deferred := n.dht.RoutingTable().Size() == 0
	if deferred {
		n.logger.Info("SERVICE_ADVERTISE_DEFERRED", "peer_id", n.host.ID(), "country", n.country)
	} else if err := n.provideExitWithRetry(ctx); err != nil {
		return err
	}
	n.providerStarted = true
	if !n.providerLoopStarted {
		n.providerLoopStarted = true
		n.wg.Add(1)
		go n.providerLoop(n.providerRefresh, deferred)
	}
	return nil
}

// SuspendExit withdraws CAP_EXIT on M11 readiness loss; stale DHT hints fail descriptor validation.
func (n *Node) SuspendExit() error {
	if n == nil {
		return fmt.Errorf("invalid Exit suspension")
	}
	n.providerMu.Lock()
	defer n.providerMu.Unlock()
	if n.closed {
		return fmt.Errorf("discovery node is closed")
	}
	if !n.providerStarted {
		return nil
	}
	if err := n.withdrawExitCapability(); err != nil {
		return err
	}
	n.providerStarted = false
	return nil
}

// ExitActive checks signed Exit advertisement, not client tunnel state.
func (n *Node) ExitActive() bool {
	if n == nil {
		return false
	}
	n.providerMu.Lock()
	defer n.providerMu.Unlock()
	return !n.closed && n.providerStarted
}

// SetExitCountry assigns observed GeoIP before publication, never while already advertised.
func (n *Node) SetExitCountry(country string) error {
	if n == nil {
		return fmt.Errorf("invalid Exit country update")
	}
	normalized, err := serviceindex.NormalizeCountry(country)
	if err != nil {
		return err
	}
	n.providerMu.Lock()
	defer n.providerMu.Unlock()
	if n.closed || !n.exitConfigured || n.providerStarted {
		return fmt.Errorf("Exit country cannot change after activation or close")
	}
	n.announcementMu.Lock()
	defer n.announcementMu.Unlock()
	if n.country == normalized {
		return nil
	}
	if n.announcement.Sequence == ^uint64(0) {
		return fmt.Errorf("local announcement sequence exhausted")
	}
	cloned, ok := proto.Clone(n.announcement).(*irviov1.NodeAnnouncement)
	if !ok {
		return fmt.Errorf("clone local announcement")
	}
	cloned.Sequence++
	cloned.Country = normalized
	now := time.Now()
	cloned.CreatedAtUnix = now.Unix()
	cloned.ExpiresAtUnix = now.Add(n.announcementTTL).Unix()
	signed, err := irvioprotocol.SealAnnouncement(n.privateKey, cloned, now)
	if err == nil {
		err = n.server.SetEnvelope(signed)
	}
	if err != nil {
		return err
	}
	n.country = normalized
	n.announcement = cloned
	n.signedEnvelope = append([]byte(nil), signed...)
	n.logger.Info("EXIT_COUNTRY_RESOLVED", "peer_id", n.host.ID(), "country", normalized)
	return nil
}

func (n *Node) publishExitCapability() error {
	n.announcementMu.Lock()
	defer n.announcementMu.Unlock()
	if n.announcement.Capabilities&irvioprotocol.CapExit != 0 {
		return nil
	}
	if n.announcement.Sequence == ^uint64(0) {
		return fmt.Errorf("local announcement sequence exhausted")
	}
	cloned, ok := proto.Clone(n.announcement).(*irviov1.NodeAnnouncement)
	if !ok {
		return fmt.Errorf("clone local announcement")
	}
	now := time.Now()
	cloned.Sequence++
	cloned.Capabilities |= irvioprotocol.CapExit
	cloned.AcceptingNewSessions = true
	cloned.CreatedAtUnix = now.Unix()
	cloned.ExpiresAtUnix = now.Add(n.announcementTTL).Unix()
	signed, err := irvioprotocol.SealAnnouncement(n.privateKey, cloned, now)
	if err != nil {
		return err
	}
	if err := n.server.SetEnvelope(signed); err != nil {
		return err
	}
	n.announcement = cloned
	n.signedEnvelope = append([]byte(nil), signed...)
	n.logger.Info("EXIT_CAPABILITY_READY", "peer_id", n.host.ID(), "country", n.country, "sequence", cloned.Sequence)
	return nil
}

func (n *Node) withdrawExitCapability() error {
	n.announcementMu.Lock()
	defer n.announcementMu.Unlock()
	if n.announcement.Capabilities&irvioprotocol.CapExit == 0 {
		return nil
	}
	if n.announcement.Sequence == ^uint64(0) {
		return fmt.Errorf("local announcement sequence exhausted")
	}
	cloned, ok := proto.Clone(n.announcement).(*irviov1.NodeAnnouncement)
	if !ok {
		return fmt.Errorf("clone local announcement")
	}
	now := time.Now()
	cloned.Sequence++
	cloned.Capabilities &^= irvioprotocol.CapExit
	cloned.AcceptingNewSessions = false
	cloned.CreatedAtUnix = now.Unix()
	cloned.ExpiresAtUnix = now.Add(n.announcementTTL).Unix()
	signed, err := irvioprotocol.SealAnnouncement(n.privateKey, cloned, now)
	if err != nil {
		return err
	}
	if err := n.server.SetEnvelope(signed); err != nil {
		return err
	}
	n.announcement = cloned
	n.signedEnvelope = append([]byte(nil), signed...)
	n.logger.Info("EXIT_CAPABILITY_SUSPENDED", "peer_id", n.host.ID(), "country", n.country, "sequence", cloned.Sequence)
	return nil
}

func (n *Node) announcementLoop(refresh time.Duration) {
	defer n.wg.Done()
	timer := time.NewTimer(announcementRefreshDelay(refresh))
	defer timer.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-timer.C:
			if err := n.renewAnnouncement(); err != nil && n.ctx.Err() == nil {
				n.logger.Warn("NODE_ANNOUNCEMENT_REJECTED", "peer_id", n.host.ID(), "reason", err)
			}
			timer.Reset(announcementRefreshDelay(refresh))
		}
	}
}

func announcementRefreshDelay(base time.Duration) time.Duration {
	span := base / 5
	if span <= 0 {
		return base
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(2*span)+1))
	if err != nil {
		return base
	}
	return base - span + time.Duration(value.Int64())
}

func (n *Node) renewAnnouncement() error {
	n.announcementMu.Lock()
	defer n.announcementMu.Unlock()
	if n.announcement.Sequence == ^uint64(0) {
		return fmt.Errorf("local announcement sequence exhausted")
	}
	cloned := proto.Clone(n.announcement)
	next, ok := cloned.(*irviov1.NodeAnnouncement)
	if !ok {
		return fmt.Errorf("clone local announcement: unexpected type %T", cloned)
	}
	now := time.Now()
	next.Sequence++
	next.Libp2PAddrs = n.ListenAddresses()
	next.CreatedAtUnix = now.Unix()
	next.ExpiresAtUnix = now.Add(n.announcementTTL).Unix()
	signed, err := irvioprotocol.SealAnnouncement(n.privateKey, next, now)
	if err != nil {
		return fmt.Errorf("renew local announcement: %w", err)
	}
	if err := n.server.SetEnvelope(signed); err != nil {
		return fmt.Errorf("publish renewed local announcement: %w", err)
	}
	n.announcement = next
	n.signedEnvelope = append([]byte(nil), signed...)
	n.logger.Info("NODE_ANNOUNCEMENT_CREATED", "peer_id", n.host.ID(), "country", n.country, "sequence", next.Sequence)
	return nil
}

func (n *Node) providerLoop(refresh time.Duration, deferred bool) {
	defer n.wg.Done()
	const retry = 5 * time.Second
	delay := refresh
	if deferred {
		delay = retry
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-timer.C:
			n.providerMu.Lock()
			active := n.providerStarted
			n.providerMu.Unlock()
			if !active {
				timer.Reset(retry)
				continue
			}
			if n.dht.RoutingTable().Size() == 0 {
				if len(n.bootstrapPeers) == 0 {
					timer.Reset(retry)
					continue
				}
				if err := n.EnsureRoutingPeers(n.ctx); err != nil {
					if n.ctx.Err() == nil {
						n.logger.Warn("SERVICE_ADVERTISE_FAILED", "peer_id", n.host.ID(), "country", n.country, "reason", err)
					}
					timer.Reset(retry)
					continue
				}
			}
			if err := n.provideExitWithRetry(n.ctx); err != nil {
				if n.ctx.Err() == nil {
					n.logger.Warn("SERVICE_ADVERTISE_FAILED", "peer_id", n.host.ID(), "country", n.country, "reason", err)
				}
				timer.Reset(retry)
				continue
			}
			timer.Reset(refresh)
		}
	}
}

// FindExits discovers, directly authenticates, and persists exit providers.
func (n *Node) FindExits(ctx context.Context, country string) ([]peer.ID, error) {
	normalized, err := serviceindex.NormalizeCountry(country)
	if err != nil {
		return nil, err
	}
	serviceCID, service, err := serviceindex.ExitCID(normalized)
	if err != nil {
		return nil, err
	}
	n.logger.Info("SERVICE_DISCOVERY_START", "peer_id", n.host.ID(), "country", normalized, "service", service)
	verified := make([]peer.ID, 0)
	err = processProviderResults(ctx, n.host.ID(), n.dht.FindProvidersAsync(ctx, serviceCID, maxProviderResults),
		n.host.Peerstore().Addrs, n.dht.FindPeer, func(provider peer.AddrInfo) (bool, error) {
			if len(provider.Addrs) == 0 {
				return false, nil
			}
			n.logger.Info("SERVICE_PROVIDER_FOUND", "peer_id", n.host.ID(), "remote_peer_id", provider.ID, "service", service)
			if err := n.host.Connect(ctx, provider); err != nil {
				n.logger.Warn("NODE_ANNOUNCEMENT_REJECTED", "remote_peer_id", provider.ID, "reason", err)
				return false, nil
			}
			announcement, signed, err := n.fetchAndVerify(ctx, provider.ID)
			if err != nil {
				n.logger.Warn("NODE_ANNOUNCEMENT_REJECTED", "remote_peer_id", provider.ID, "reason", err)
				return false, nil
			}
			if announcement.Capabilities&irvioprotocol.CapExit == 0 || announcement.Country != normalized {
				n.logger.Warn("NODE_ANNOUNCEMENT_REJECTED", "remote_peer_id", provider.ID, "reason", "service claim mismatch")
				return false, nil
			}
			connectedAt := time.Now()
			storeErr := n.storeVerified(ctx, provider.ID, announcement, signed, connectedAt)
			if storeErr != nil && !errors.Is(storeErr, peerstore.ErrStaleAnnouncement) {
				return false, fmt.Errorf("persist peer %s: %w", provider.ID, storeErr)
			}
			if err := n.directory.RecordService(ctx, service, provider.ID.String(), connectedAt); err != nil {
				return false, err
			}
			n.logger.Info("PEER_PERSISTED", "peer_id", n.host.ID(), "remote_peer_id", provider.ID, "country", normalized)
			verified = append(verified, provider.ID)
			return true, nil
		})
	if err != nil {
		return verified, err
	}
	if err := ctx.Err(); err != nil {
		return verified, err
	}
	return verified, nil
}

// AuthenticatePeer refreshes an addressable peer before allocating responder resources.
func (n *Node) AuthenticatePeer(ctx context.Context, remote peer.ID) error {
	if n == nil || ctx == nil || remote == "" || remote == n.host.ID() {
		return fmt.Errorf("invalid authenticated peer refresh")
	}
	announcement, signed, err := n.fetchAndVerify(ctx, remote)
	if err != nil {
		return err
	}
	now := time.Now()
	err = n.storeVerified(ctx, remote, announcement, signed, now)
	if errors.Is(err, peerstore.ErrStaleAnnouncement) {
		return nil
	}
	return err
}

func (n *Node) storeVerified(ctx context.Context, remote peer.ID, announcement *irviov1.NodeAnnouncement, signed []byte, observedAt time.Time) error {
	verified, err := irvioprotocol.VerifySignedAnnouncementRecord(signed, remote, observedAt)
	if err != nil {
		return err
	}
	return n.directory.StoreVerified(ctx, peerstore.Peer{
		NodeID: remote.String(), ProtocolVersion: announcement.ProtocolVersion,
		Capabilities: announcement.Capabilities, Country: announcement.Country,
		FirstSeen: observedAt, LastSeen: observedAt, LastConnected: &observedAt,
		ExpiresAt:    time.Unix(announcement.ExpiresAtUnix, 0),
		AWGPublicKey: append([]byte(nil), announcement.AwgPublicKey...),
		AWGKeyEpoch:  announcement.AwgKeyEpoch,
	}, peerstore.Announcement{
		NodeID: remote.String(), Incarnation: announcement.Incarnation,
		DescriptorID: append([]byte(nil), verified.DescriptorID[:]...),
		Epoch:        append([]byte(nil), announcement.SessionEpoch...),
		Sequence:     announcement.Sequence, SignedEnvelope: append([]byte(nil), signed...),
		CreatedAt: time.Unix(announcement.CreatedAtUnix, 0),
		ExpiresAt: time.Unix(announcement.ExpiresAtUnix, 0),
	})
}

func (n *Node) fetchAndVerify(ctx context.Context, remotePeer peer.ID) (*irviov1.NodeAnnouncement, []byte, error) {
	n.logger.Info("NODE_ANNOUNCEMENT_RX", "peer_id", n.host.ID(), "remote_peer_id", remotePeer)
	signed, err := irvioprotocol.FetchSignedAnnouncement(ctx, n.host, remotePeer)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch node announcement from peer %s: %w", remotePeer, err)
	}
	announcement, err := irvioprotocol.VerifySignedAnnouncement(signed, remotePeer, time.Now())
	if err != nil {
		return nil, nil, fmt.Errorf("verify node announcement from peer %s: %w", remotePeer, err)
	}
	n.logger.Info("NODE_ANNOUNCEMENT_VALID", "peer_id", n.host.ID(), "remote_peer_id", remotePeer)
	return announcement, signed, nil
}

// VerifyDirect fetches and verifies a known peer without using the DHT.
func (n *Node) VerifyDirect(ctx context.Context, remotePeer peer.ID) error {
	_, _, err := n.fetchAndVerify(ctx, remotePeer)
	return err
}

// Host lends the authenticated host; callers must not close it.
func (n *Node) Host() host.Host { return n.host }

// PeerID returns this node's stable libp2p PeerID.
func (n *Node) PeerID() peer.ID { return n.host.ID() }

// ControlMark reports the mark guaranteed by the node-owned outbound TCP transport.
func (n *Node) ControlMark() uint32 { return n.controlMark }

// IdentityPrivateKey lends the signing key; never log or persist it elsewhere.
func (n *Node) IdentityPrivateKey() crypto.PrivKey { return n.privateKey }

// LocalAnnouncement returns an immutable copy of the current signed payload.
func (n *Node) LocalAnnouncement() *irviov1.NodeAnnouncement {
	n.announcementMu.Lock()
	defer n.announcementMu.Unlock()
	cloned, _ := proto.Clone(n.announcement).(*irviov1.NodeAnnouncement)
	return cloned
}

// VerifiedAnnouncement fetches and authenticates one remote announcement.
func (n *Node) VerifiedAnnouncement(ctx context.Context, remote peer.ID) (*irviov1.NodeAnnouncement, error) {
	announcement, _, err := n.fetchAndVerify(ctx, remote)
	if err != nil {
		return nil, err
	}
	cloned, _ := proto.Clone(announcement).(*irviov1.NodeAnnouncement)
	return cloned, nil
}

// LocalSignedAnnouncement returns the exact currently served signed bytes.
func (n *Node) LocalSignedAnnouncement() []byte {
	n.announcementMu.Lock()
	defer n.announcementMu.Unlock()
	return append([]byte(nil), n.signedEnvelope...)
}

// EnableSessionEstablishment publishes supported M12 protocols without changing role intent.
func (n *Node) EnableSessionEstablishment() error {
	if n == nil {
		return fmt.Errorf("invalid session establishment activation")
	}
	n.providerMu.Lock()
	closed := n.closed
	n.providerMu.Unlock()
	if closed {
		return fmt.Errorf("discovery node is closed")
	}
	n.announcementMu.Lock()
	defer n.announcementMu.Unlock()
	if containsFeature(n.announcement.SessionFeatures, "session-establish-v1") &&
		containsFeature(n.announcement.SessionFeatures, "session-control-v1") {
		return nil
	}
	if n.announcement.Sequence == ^uint64(0) {
		return fmt.Errorf("local announcement sequence exhausted")
	}
	cloned, _ := proto.Clone(n.announcement).(*irviov1.NodeAnnouncement)
	cloned.Sequence++
	for _, feature := range []string{"session-establish-v1", "session-control-v1"} {
		if !containsFeature(cloned.SessionFeatures, feature) {
			cloned.SessionFeatures = append(cloned.SessionFeatures, feature)
		}
	}
	now := time.Now()
	cloned.CreatedAtUnix = now.Unix()
	cloned.ExpiresAtUnix = now.Add(n.announcementTTL).Unix()
	signed, err := irvioprotocol.SealAnnouncement(n.privateKey, cloned, now)
	if err == nil {
		err = n.server.SetEnvelope(signed)
	}
	if err != nil {
		return err
	}
	n.announcement = cloned
	n.signedEnvelope = append([]byte(nil), signed...)
	return nil
}

// ActivateRendezvousCapability requires M11 public readiness; DHT hints alone do not prove liveness.
func (n *Node) ActivateRendezvousCapability(ctx context.Context, countries []string) error {
	if n == nil || ctx == nil {
		return fmt.Errorf("invalid Rendezvous activation")
	}
	n.providerMu.Lock()
	closed := n.closed
	n.providerMu.Unlock()
	if closed {
		return fmt.Errorf("discovery node is closed")
	}
	n.announcementMu.Lock()
	if n.announcement.Capabilities&(irvioprotocol.CapPublicControlV4|irvioprotocol.CapPublicControlV6) == 0 {
		n.announcementMu.Unlock()
		return fmt.Errorf("Rendezvous requires verified public control")
	}
	if n.announcement.Capabilities&irvioprotocol.CapRendezvous == 0 {
		if n.announcement.Sequence == ^uint64(0) {
			n.announcementMu.Unlock()
			return fmt.Errorf("local announcement sequence exhausted")
		}
		cloned, _ := proto.Clone(n.announcement).(*irviov1.NodeAnnouncement)
		cloned.Sequence++
		cloned.Capabilities |= irvioprotocol.CapRendezvous
		if !containsFeature(cloned.SessionFeatures, "rendezvous-v1") {
			cloned.SessionFeatures = append(cloned.SessionFeatures, "rendezvous-v1")
		}
		now := time.Now()
		cloned.CreatedAtUnix = now.Unix()
		cloned.ExpiresAtUnix = now.Add(n.announcementTTL).Unix()
		signed, err := irvioprotocol.SealAnnouncement(n.privateKey, cloned, now)
		if err == nil {
			err = n.server.SetEnvelope(signed)
		}
		if err != nil {
			n.announcementMu.Unlock()
			return err
		}
		n.announcement = cloned
		n.signedEnvelope = append([]byte(nil), signed...)
	}
	n.announcementMu.Unlock()
	_, general, _ := serviceindex.RendezvousCID("")
	if err := n.ProvideService(ctx, general); err != nil {
		return err
	}
	if err := n.UpdateRendezvousCountries(ctx, countries); err != nil {
		return err
	}
	n.providerMu.Lock()
	n.rendezvousActive = true
	if !n.rendezvousStarted {
		n.rendezvousStarted = true
		n.wg.Add(1)
		go n.rendezvousProviderLoop(n.providerRefresh)
	}
	n.providerMu.Unlock()
	return nil
}

// UpdateRendezvousCountries updates fresh pools for reprovide, not DHT liveness claims.
func (n *Node) UpdateRendezvousCountries(ctx context.Context, countries []string) error {
	if n == nil || ctx == nil {
		return fmt.Errorf("invalid Rendezvous country update")
	}
	n.rendezvousPublishMu.Lock()
	defer n.rendezvousPublishMu.Unlock()
	next := make(map[string]struct{}, len(countries))
	for _, country := range countries {
		normalized, err := serviceindex.NormalizeCountry(country)
		if err != nil {
			return err
		}
		next[normalized] = struct{}{}
	}
	n.providerMu.Lock()
	if n.closed {
		n.providerMu.Unlock()
		return fmt.Errorf("discovery node is closed")
	}
	n.rendezvousCountries = next
	if n.rendezvousPublished == nil {
		n.rendezvousPublished = make(map[string]time.Time)
	}
	for country := range n.rendezvousPublished {
		if _, exists := next[country]; !exists {
			delete(n.rendezvousPublished, country)
		}
	}
	n.providerMu.Unlock()
	return n.provideRendezvousCountries(ctx, next)
}

// Caller holds rendezvousPublishMu; failed publications remain due for retry.
func (n *Node) provideRendezvousCountries(ctx context.Context, next map[string]struct{}) error {
	for country := range next {
		if published := n.rendezvousPublished[country]; !published.IsZero() && time.Since(published) < n.providerRefresh {
			continue
		}
		_, service, _ := serviceindex.RendezvousCID(country)
		if err := n.ProvideService(ctx, service); err != nil {
			return err
		}
		n.rendezvousPublished[country] = time.Now()
	}
	return nil
}

func (n *Node) refreshRendezvousCountries(ctx context.Context) error {
	n.rendezvousPublishMu.Lock()
	defer n.rendezvousPublishMu.Unlock()
	n.providerMu.Lock()
	active := n.rendezvousActive && !n.closed
	countries := make(map[string]struct{}, len(n.rendezvousCountries))
	for country := range n.rendezvousCountries {
		countries[country] = struct{}{}
	}
	n.providerMu.Unlock()
	if !active {
		return nil
	}
	return n.provideRendezvousCountries(ctx, countries)
}

// DeactivateRendezvousCapability withdraws signed capability, invalidating stale provider hints.
func (n *Node) DeactivateRendezvousCapability() error {
	if n == nil {
		return fmt.Errorf("invalid Rendezvous deactivation")
	}
	n.rendezvousPublishMu.Lock()
	defer n.rendezvousPublishMu.Unlock()
	n.providerMu.Lock()
	n.rendezvousActive = false
	n.rendezvousCountries = make(map[string]struct{})
	clear(n.rendezvousPublished)
	n.providerMu.Unlock()
	n.announcementMu.Lock()
	defer n.announcementMu.Unlock()
	if n.announcement.Capabilities&irvioprotocol.CapRendezvous == 0 {
		return nil
	}
	if n.announcement.Sequence == ^uint64(0) {
		return fmt.Errorf("local announcement sequence exhausted")
	}
	cloned, _ := proto.Clone(n.announcement).(*irviov1.NodeAnnouncement)
	cloned.Sequence++
	cloned.Capabilities &^= irvioprotocol.CapRendezvous
	cloned.SessionFeatures = removeFeature(cloned.SessionFeatures, "rendezvous-v1")
	now := time.Now()
	cloned.CreatedAtUnix = now.Unix()
	cloned.ExpiresAtUnix = now.Add(n.announcementTTL).Unix()
	signed, err := irvioprotocol.SealAnnouncement(n.privateKey, cloned, now)
	if err == nil {
		err = n.server.SetEnvelope(signed)
	}
	if err != nil {
		return err
	}
	n.announcement = cloned
	n.signedEnvelope = append([]byte(nil), signed...)
	return nil
}

func (n *Node) rendezvousProviderLoop(refresh time.Duration) {
	defer n.wg.Done()
	ticker := time.NewTicker(refresh)
	defer ticker.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			n.providerMu.Lock()
			active := n.rendezvousActive
			n.providerMu.Unlock()
			if !active {
				continue
			}
			_, general, _ := serviceindex.RendezvousCID("")
			if err := n.ProvideService(n.ctx, general); err != nil && n.ctx.Err() == nil {
				n.logger.Warn("SERVICE_ADVERTISE_FAILED", "service", general, "reason", err)
			}
			if err := n.refreshRendezvousCountries(n.ctx); err != nil && n.ctx.Err() == nil {
				n.logger.Warn("SERVICE_ADVERTISE_FAILED", "reason", err)
			}
		}
	}
}

// ProvideService publishes one exact versioned service key.
func (n *Node) ProvideService(ctx context.Context, service string) error {
	value, err := serviceindex.ServiceCID(service)
	if err != nil {
		return err
	}
	if n.dht.RoutingTable().Size() == 0 && len(n.bootstrapPeers) > 0 {
		if err := n.EnsureRoutingPeers(ctx); err != nil {
			return fmt.Errorf("advertise %s: %w", service, err)
		}
	}
	if err := n.dht.Provide(ctx, value, true); err != nil {
		return fmt.Errorf("advertise %s: %w", service, err)
	}
	return nil
}

// FindRendezvous authenticates current public capabilities behind provider hints.
func (n *Node) FindRendezvous(ctx context.Context, country string) ([]peer.ID, error) {
	value, service, err := serviceindex.RendezvousCID(country)
	if err != nil {
		return nil, err
	}
	if n.dht.RoutingTable().Size() == 0 && len(n.bootstrapPeers) > 0 {
		if err := n.EnsureRoutingPeers(ctx); err != nil {
			return nil, err
		}
	}
	resolutionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	resolved := make(chan peer.AddrInfo)
	resolutionDone := make(chan error, 1)
	go func() {
		defer close(resolved)
		resolutionDone <- processProviderResults(resolutionCtx, n.host.ID(), n.dht.FindProvidersAsync(resolutionCtx, value, maxProviderResults),
			n.host.Peerstore().Addrs, n.dht.FindPeer, func(info peer.AddrInfo) (bool, error) {
				select {
				case resolved <- info:
					return false, nil
				case <-resolutionCtx.Done():
					return false, resolutionCtx.Err()
				}
			})
	}()
	result, verifyErr := verifyRendezvousProviders(ctx, n.host.ID(), resolved,
		func(peerCtx context.Context, provider peer.AddrInfo) (bool, error) {
			if err := n.host.Connect(peerCtx, provider); err != nil {
				return false, nil
			}
			announcement, signed, err := n.fetchAndVerify(peerCtx, provider.ID)
			if err != nil || announcement.Capabilities&irvioprotocol.CapRendezvous == 0 ||
				announcement.Capabilities&(irvioprotocol.CapPublicControlV4|irvioprotocol.CapPublicControlV6) == 0 ||
				!containsFeature(announcement.SessionFeatures, "rendezvous-v1") {
				return false, nil
			}
			now := time.Now()
			if err := n.storeVerified(peerCtx, provider.ID, announcement, signed, now); err != nil &&
				!errors.Is(err, peerstore.ErrStaleAnnouncement) {
				return false, err
			}
			if err := n.directory.RecordService(peerCtx, service, provider.ID.String(), now); err != nil {
				return false, err
			}
			return true, nil
		})
	cancel()
	resolutionErr := <-resolutionDone
	if verifyErr != nil {
		return result, verifyErr
	}
	return result, resolutionErr
}

func containsFeature(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func removeFeature(values []string, unwanted string) []string {
	result := values[:0]
	for _, value := range values {
		if value != unwanted {
			result = append(result, value)
		}
	}
	return result
}

// UpdateAWGBinding publishes a newer key epoch while old sessions may drain.
func (n *Node) UpdateAWGBinding(publicKey []byte, keyEpoch uint64) error {
	if len(publicKey) != 32 || keyEpoch == 0 {
		return fmt.Errorf("invalid AWG key binding")
	}
	n.announcementMu.Lock()
	defer n.announcementMu.Unlock()
	if keyEpoch <= n.announcement.AwgKeyEpoch {
		return fmt.Errorf("AWG key epoch %d does not supersede %d", keyEpoch, n.announcement.AwgKeyEpoch)
	}
	if n.announcement.Sequence == ^uint64(0) {
		return fmt.Errorf("local announcement sequence exhausted")
	}
	cloned, ok := proto.Clone(n.announcement).(*irviov1.NodeAnnouncement)
	if !ok {
		return fmt.Errorf("clone local announcement")
	}
	now := time.Now()
	cloned.Sequence++
	cloned.AwgPublicKey = append([]byte(nil), publicKey...)
	cloned.AwgKeyEpoch = keyEpoch
	cloned.CreatedAtUnix = now.Unix()
	cloned.ExpiresAtUnix = now.Add(n.announcementTTL).Unix()
	signed, err := irvioprotocol.SealAnnouncement(n.privateKey, cloned, now)
	if err != nil {
		return err
	}
	if err := n.server.SetEnvelope(signed); err != nil {
		return err
	}
	n.announcement = cloned
	n.signedEnvelope = append([]byte(nil), signed...)
	n.logger.Info("AWG_BINDING_ROTATED", "peer_id", n.host.ID(), "key_epoch", keyEpoch, "sequence", cloned.Sequence)
	return nil
}

// UpdatePublicReachability publishes current verified endpoints; empty proofs revoke public capability.
func (n *Node) UpdatePublicReachability(proofs []reachability.Proof) error {
	if len(proofs) > irvioprotocol.MaxPublicControlEndpoints {
		return fmt.Errorf("too many public reachability proofs")
	}
	n.announcementMu.Lock()
	defer n.announcementMu.Unlock()
	if n.announcement.Sequence == ^uint64(0) {
		return fmt.Errorf("local announcement sequence exhausted")
	}
	cloned, ok := proto.Clone(n.announcement).(*irviov1.NodeAnnouncement)
	if !ok {
		return fmt.Errorf("clone local announcement")
	}
	cloned.Capabilities &^= irvioprotocol.CapPublicControlV4 | irvioprotocol.CapPublicControlV6
	cloned.PublicControlEndpoints = nil
	now := time.Now()
	proofPolicy := reachability.DefaultProofPolicy()
	proofPolicy.Now = func() time.Time { return now }
	seen := make(map[irviov1.ReachabilityFamily]struct{}, len(proofs))
	for _, proof := range proofs {
		verified, err := reachability.BuildProof(n.host.ID(), proof.Attestations, proofPolicy)
		if err != nil || verified.Subject != proof.Subject || verified.Endpoint != proof.Endpoint ||
			!verified.ExpiresAt.Equal(proof.ExpiresAt) || !proof.ExpiresAt.After(now) {
			return fmt.Errorf("invalid local public reachability proof")
		}
		proof = verified
		if _, duplicate := seen[proof.Endpoint.Family]; duplicate {
			return fmt.Errorf("duplicate local public reachability family")
		}
		seen[proof.Endpoint.Family] = struct{}{}
		capability := irvioprotocol.CapPublicControlV6
		if proof.Endpoint.Family == irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4 {
			capability = irvioprotocol.CapPublicControlV4
		} else if proof.Endpoint.Family != irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6 {
			return fmt.Errorf("invalid local public reachability family")
		}
		cloned.Capabilities |= capability
		cloned.PublicControlEndpoints = append(cloned.PublicControlEndpoints, &irviov1.PublicControlEndpoint{
			Family: proof.Endpoint.Family, ObservedIp: append([]byte(nil), proof.Endpoint.Address.Addr().AsSlice()...),
			ExternalControlPort: uint32(proof.Endpoint.Address.Port()), NetworkGeneration: proof.Endpoint.NetworkGeneration,
			PublicEpoch: proof.Endpoint.PublicEpoch, ExpiresAtUnix: proof.ExpiresAt.Unix(), Proof: reachability.ProofMessage(proof),
		})
	}
	// Withdraw Rendezvous together with missing or expired public reachability.
	if cloned.Capabilities&(irvioprotocol.CapPublicControlV4|irvioprotocol.CapPublicControlV6) == 0 {
		cloned.Capabilities &^= irvioprotocol.CapRendezvous
		cloned.SessionFeatures = removeFeature(cloned.SessionFeatures, "rendezvous-v1")
	}
	cloned.Sequence++
	cloned.CreatedAtUnix = now.Unix()
	cloned.ExpiresAtUnix = now.Add(n.announcementTTL).Unix()
	signed, err := irvioprotocol.SealAnnouncement(n.privateKey, cloned, now)
	if err != nil {
		return err
	}
	if err := n.server.SetEnvelope(signed); err != nil {
		return err
	}
	n.announcement = cloned
	n.signedEnvelope = append([]byte(nil), signed...)
	n.logger.Info("PUBLIC_CONTROL_CAPABILITY_UPDATED", "peer_id", n.host.ID(), "endpoints", len(proofs), "sequence", cloned.Sequence)
	return nil
}

// ListenAddresses returns dialable addresses with this node's /p2p component.
func (n *Node) ListenAddresses() []string {
	addresses := n.host.Addrs()
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, fmt.Sprintf("%s/p2p/%s", address, n.host.ID()))
	}
	return result
}

// IPv4LoopbackAddress returns a deterministic local bootstrap address when available.
func (n *Node) IPv4LoopbackAddress() (string, error) {
	fallback := ""
	for _, address := range n.host.Addrs() {
		ipString, err := address.ValueForProtocol(multiaddr.P_IP4)
		if err != nil {
			continue
		}
		ip, err := netip.ParseAddr(ipString)
		if err != nil || !ip.Is4() {
			continue
		}
		port, err := address.ValueForProtocol(multiaddr.P_TCP)
		if err != nil {
			continue
		}
		candidate := fmt.Sprintf("/ip4/127.0.0.1/tcp/%s/p2p/%s", port, n.host.ID())
		if ip.IsLoopback() {
			return candidate, nil
		}
		fallback = candidate
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("node has no IPv4 listen address")
}

// DatabasePath returns this node's SQLite database path.
func (n *Node) DatabasePath() string { return n.dbPath }

// Directory returns the node-owned peer directory for local inspection.
func (n *Node) Directory() *peerstore.Directory { return n.directory }

// Close performs graceful shutdown in dependency order.
func (n *Node) Close() error {
	n.closeOnce.Do(func() {
		n.providerMu.Lock()
		n.closed = true
		n.cancel()
		n.providerMu.Unlock()
		n.wg.Wait()
		var errs []error
		if err := n.dht.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close DHT: %w", err))
		}
		if err := n.host.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close libp2p host: %w", err))
		}
		if err := n.directory.Close(); err != nil {
			errs = append(errs, err)
		}
		if err := n.publisher.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close announcement publisher: %w", err))
		}
		n.closeErr = errors.Join(errs...)
	})
	return n.closeErr
}
