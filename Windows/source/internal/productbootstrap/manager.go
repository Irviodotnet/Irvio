// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package productbootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"path/filepath"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libp2ppeerstore "github.com/libp2p/go-libp2p/core/peerstore"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
	"google.golang.org/protobuf/proto"

	"github.com/Irviodotnet/Irvio/internal/bootstrap"
	"github.com/Irviodotnet/Irvio/internal/discovery"
	irvioprotocol "github.com/Irviodotnet/Irvio/internal/protocol"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

const (
	DefaultShareNodes    = 4
	MaxShareNodes        = 5
	generateOverallLimit = 6 * time.Second
	generateLookupLimit  = 2 * time.Second
	generatePeerLimit    = 1200 * time.Millisecond
	generateConcurrency  = 4
	irvioDHTProtocol     = libprotocol.ID("/hydra/kad/1.0.0")
)

var (
	ErrNotAttached          = errors.New("bootstrap manager is not attached")
	ErrNoReachableBootstrap = errors.New("no reachable bootstrap nodes")
)

type Config struct {
	DataDir            string
	CLI                []string
	Logger             *slog.Logger
	DialPolicy         bootstrap.ConnectPolicy
	AllowTestAddresses bool
}

type Manager struct {
	config Config
	path   string

	mu         sync.Mutex
	cli        *localv1.BootstrapBundle
	persisted  *localv1.BootstrapBundle
	combined   *localv1.BootstrapBundle
	loadErr    error
	lastErr    error
	joining    bool
	generation uint64
	node       *discovery.Node
	ctx        context.Context
	cancel     context.CancelFunc
	joinCancel context.CancelFunc
	joinWait   sync.WaitGroup
	closed     bool
}

func New(config Config) (*Manager, error) {
	if config.DataDir == "" || config.Logger == nil {
		return nil, fmt.Errorf("invalid bootstrap manager configuration")
	}
	if config.DialPolicy.MaxConcurrency == 0 {
		config.DialPolicy = bootstrap.DefaultConnectPolicy()
	}
	manager := &Manager{config: config, path: filepath.Join(config.DataDir, StoreFilename)}
	if len(config.CLI) != 0 {
		cli, err := FromMultiaddrs(config.CLI, 0)
		if err != nil {
			return nil, fmt.Errorf("parse command-line bootstrap records: %w", err)
		}
		manager.cli = cli
	}
	persisted, err := loadStore(manager.path)
	if err != nil {
		manager.loadErr = err
		config.Logger.Warn("BOOTSTRAP_STORE_DEGRADED")
	} else {
		manager.persisted = persisted
	}
	manager.combined, err = mergeBundles(manager.cli, manager.persisted)
	if err != nil {
		return nil, err
	}
	return manager, nil
}

func (manager *Manager) StartupAddresses() ([]string, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.combined == nil {
		return nil, nil
	}
	return Multiaddrs(manager.combined)
}

func (manager *Manager) Attach(parent context.Context, node *discovery.Node) error {
	if parent == nil || node == nil {
		return ErrNotAttached
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return ErrNotAttached
	}
	if manager.node != nil {
		return nil
	}
	manager.ctx, manager.cancel = context.WithCancel(parent)
	manager.node = node
	manager.installAddressesLocked(manager.combined)
	return nil
}

func (manager *Manager) State() *localv1.BootstrapState {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.stateLocked()
}

func (manager *Manager) Import(code string) (*localv1.BootstrapState, error) {
	bundle, err := Decode(code)
	if err != nil {
		return nil, err
	}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil, ErrNotAttached
	}
	persisted, err := mergeBundles(manager.persisted, bundle)
	if err != nil {
		manager.mu.Unlock()
		return nil, err
	}
	if proto.Equal(persisted, manager.persisted) {
		state := manager.stateLocked()
		manager.mu.Unlock()
		return state, nil
	}
	if err := writeStore(manager.path, persisted); err != nil {
		manager.mu.Unlock()
		return nil, err
	}
	combined, err := mergeBundles(manager.cli, persisted)
	if err != nil {
		manager.mu.Unlock()
		return nil, err
	}
	manager.persisted = persisted
	manager.combined = combined
	manager.loadErr = nil
	manager.lastErr = nil
	manager.installAddressesLocked(combined)
	if manager.node == nil || manager.ctx == nil {
		state := manager.stateLocked()
		manager.mu.Unlock()
		return state, nil
	}
	if manager.joinCancel != nil {
		manager.joinCancel()
	}
	manager.generation++
	generation := manager.generation
	joinContext, cancel := context.WithCancel(manager.ctx)
	manager.joinCancel = cancel
	manager.joining = true
	addresses, _ := Multiaddrs(combined)
	node := manager.node
	policy := manager.config.DialPolicy
	manager.joinWait.Add(1)
	go manager.join(joinContext, generation, node, addresses, policy)
	state := manager.stateLocked()
	manager.mu.Unlock()
	return state, nil
}

func (manager *Manager) Generate(ctx context.Context, preferred int) (*localv1.GeneratedBootstrapBundle, error) {
	if ctx == nil {
		return nil, ErrNotAttached
	}
	if preferred == 0 {
		preferred = DefaultShareNodes
	}
	if preferred < 1 || preferred > MaxShareNodes {
		return nil, ErrInvalidBundle
	}
	manager.mu.Lock()
	if manager.closed || manager.node == nil {
		manager.mu.Unlock()
		return nil, ErrNotAttached
	}
	node := manager.node
	combined := cloneBundle(manager.combined)
	allowTest := manager.config.AllowTestAddresses
	manager.mu.Unlock()

	operation, cancel := context.WithTimeout(ctx, generateOverallLimit)
	defer cancel()
	ids := make([]peer.ID, 0, MaxBundleNodes)
	seen := map[peer.ID]struct{}{node.PeerID(): {}}
	add := func(id peer.ID) {
		if id == "" || len(ids) == MaxBundleNodes {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if combined != nil {
		for _, value := range combined.Nodes {
			id, err := peer.Decode(value.PeerId)
			if err == nil {
				add(id)
			}
		}
	}
	for _, id := range node.Host().Network().Peers() {
		add(id)
	}
	if len(ids) < preferred {
		lookupContext, lookupCancel := context.WithTimeout(operation, generateLookupLimit)
		values, _ := node.FindRendezvous(lookupContext, "")
		lookupCancel()
		for _, id := range values {
			add(id)
		}
	}

	selected := &localv1.BootstrapBundle{FormatVersion: BundleVersion, GeneratedAtUnixMs: uint64(time.Now().UTC().UnixMilli())}
	type probeResult struct {
		node *localv1.BootstrapNode
	}
	jobs := make(chan peer.ID)
	results := make(chan probeResult, generateConcurrency)
	var workers sync.WaitGroup
	for index := 0; index < generateConcurrency; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for id := range jobs {
				peerContext, peerCancel := context.WithTimeout(operation, generatePeerLimit)
				announcement, err := node.VerifiedAnnouncement(peerContext, id)
				if err == nil {
					stream, streamErr := node.Host().NewStream(peerContext, id, irvioDHTProtocol)
					if streamErr == nil {
						_ = stream.Close()
					}
					err = streamErr
				}
				peerCancel()
				var candidate *localv1.BootstrapNode
				if err == nil {
					candidate = eligibleNode(id, announcement, allowTest)
				}
				select {
				case results <- probeResult{node: candidate}:
				case <-operation.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, id := range ids {
			select {
			case jobs <- id:
			case <-operation.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()
	selectionComplete := false
	for result := range results {
		if !selectionComplete && result.node != nil {
			selected.Nodes = append(selected.Nodes, result.node)
			if len(selected.Nodes) == preferred {
				selectionComplete = true
				cancel()
			}
		}
	}
	if len(selected.Nodes) == 0 {
		return nil, ErrNoReachableBootstrap
	}
	normalized, err := Normalize(selected)
	if err != nil {
		return nil, err
	}
	code, err := Encode(normalized)
	if err != nil {
		return nil, err
	}
	return &localv1.GeneratedBootstrapBundle{
		Code: code, NodeCount: uint32(len(normalized.Nodes)), GeneratedAtUnixMs: normalized.GeneratedAtUnixMs,
	}, nil
}

func (manager *Manager) Close() {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return
	}
	manager.closed = true
	if manager.joinCancel != nil {
		manager.joinCancel()
	}
	if manager.cancel != nil {
		manager.cancel()
	}
	manager.mu.Unlock()
	manager.joinWait.Wait()
}

// Detach waits for in-flight joins and ends participation while retaining the bootstrap bundle and API.
func (manager *Manager) Detach() {
	manager.mu.Lock()
	if manager.joinCancel != nil {
		manager.joinCancel()
	}
	if manager.cancel != nil {
		manager.cancel()
	}
	manager.generation++
	manager.node = nil
	manager.ctx = nil
	manager.cancel = nil
	manager.joinCancel = nil
	manager.joining = false
	manager.lastErr = nil
	manager.mu.Unlock()
	manager.joinWait.Wait()
}

func (manager *Manager) join(ctx context.Context, generation uint64, node *discovery.Node, addresses []string, policy bootstrap.ConnectPolicy) {
	defer manager.joinWait.Done()
	infos, err := bootstrap.Parse(addresses)
	if err == nil {
		err = bootstrap.ConnectWithPolicy(ctx, node.Host(), infos, manager.config.Logger, policy)
	}
	if err == nil {
		lookupContext, cancel := context.WithTimeout(ctx, generateLookupLimit)
		_, _ = node.FindRendezvous(lookupContext, "")
		cancel()
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed || generation != manager.generation {
		return
	}
	manager.joining = false
	manager.lastErr = err
}

func (manager *Manager) installAddressesLocked(bundle *localv1.BootstrapBundle) {
	if manager.node == nil || bundle == nil {
		return
	}
	addresses, err := Multiaddrs(bundle)
	if err != nil {
		return
	}
	infos, err := bootstrap.Parse(addresses)
	if err != nil {
		return
	}
	for _, info := range infos {
		manager.node.Host().Peerstore().AddAddrs(info.ID, info.Addrs, libp2ppeerstore.PermanentAddrTTL)
	}
}

func (manager *Manager) stateLocked() *localv1.BootstrapState {
	configured := 0
	if manager.combined != nil {
		configured = len(manager.combined.Nodes)
	}
	reachable := 0
	if manager.node != nil && manager.combined != nil {
		for _, value := range manager.combined.Nodes {
			id, err := peer.Decode(value.PeerId)
			if err == nil && manager.node.Host().Network().Connectedness(id) == network.Connected {
				reachable++
			}
		}
	}
	state := &localv1.BootstrapState{
		ConfiguredCount: uint32(configured), CurrentlyReachableCount: uint32(reachable),
		LastErrorCode: localv1.ResultCode_RESULT_CODE_OK,
	}
	switch {
	case manager.joining:
		state.Phase = localv1.BootstrapPhase_BOOTSTRAP_PHASE_CONNECTING
	case manager.loadErr != nil:
		state.Phase = localv1.BootstrapPhase_BOOTSTRAP_PHASE_DEGRADED
		state.LastErrorCode = localv1.ResultCode_RESULT_CODE_INTERNAL
		state.UserMessage = "Saved bootstrap information is damaged; import a replacement."
	case configured == 0:
		state.Phase = localv1.BootstrapPhase_BOOTSTRAP_PHASE_EMPTY
	case reachable != 0:
		state.Phase = localv1.BootstrapPhase_BOOTSTRAP_PHASE_READY
	case manager.lastErr != nil:
		state.Phase = localv1.BootstrapPhase_BOOTSTRAP_PHASE_FAILED
		state.LastErrorCode = localv1.ResultCode_RESULT_CODE_NOT_READY
		state.UserMessage = "No configured introduction is currently reachable."
	default:
		state.Phase = localv1.BootstrapPhase_BOOTSTRAP_PHASE_DEGRADED
		state.LastErrorCode = localv1.ResultCode_RESULT_CODE_NOT_READY
		state.UserMessage = "Bootstrap peers are configured but not currently connected."
	}
	return state
}

func mergeBundles(bundles ...*localv1.BootstrapBundle) (*localv1.BootstrapBundle, error) {
	merged := &localv1.BootstrapBundle{FormatVersion: BundleVersion}
	for _, bundle := range bundles {
		if bundle == nil {
			continue
		}
		if bundle.GeneratedAtUnixMs > merged.GeneratedAtUnixMs {
			merged.GeneratedAtUnixMs = bundle.GeneratedAtUnixMs
		}
		for _, node := range bundle.Nodes {
			merged.Nodes = append(merged.Nodes, proto.Clone(node).(*localv1.BootstrapNode))
		}
	}
	if len(merged.Nodes) == 0 {
		return nil, nil
	}
	return Normalize(merged)
}

func cloneBundle(bundle *localv1.BootstrapBundle) *localv1.BootstrapBundle {
	if bundle == nil {
		return nil
	}
	return proto.Clone(bundle).(*localv1.BootstrapBundle)
}

func eligibleNode(id peer.ID, announcement *irviov1.NodeAnnouncement, allowTest bool) *localv1.BootstrapNode {
	if announcement == nil || announcement.Capabilities&irvioprotocol.CapDiscovery == 0 ||
		announcement.Capabilities&(irvioprotocol.CapPublicControlV4|irvioprotocol.CapPublicControlV6) == 0 {
		return nil
	}
	node := &localv1.BootstrapNode{PeerId: id.String()}
	seen := make(map[netip.AddrPort]struct{})
	for _, endpoint := range announcement.PublicControlEndpoints {
		if endpoint == nil || endpoint.ExternalControlPort == 0 || endpoint.ExternalControlPort > 65535 {
			continue
		}
		address, ok := netip.AddrFromSlice(endpoint.ObservedIp)
		if !ok || !shareableIP(address, allowTest) {
			continue
		}
		value := netip.AddrPortFrom(address, uint16(endpoint.ExternalControlPort))
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		node.Endpoints = append(node.Endpoints, &localv1.BootstrapEndpoint{
			Ip: append([]byte(nil), address.AsSlice()...), Port: endpoint.ExternalControlPort,
		})
	}
	if len(node.Endpoints) == 0 {
		return nil
	}
	return node
}

func shareableIP(address netip.Addr, allowTest bool) bool {
	if !address.IsValid() || address.Is4In6() || address.IsUnspecified() || address.IsLinkLocalUnicast() || address.IsMulticast() {
		return false
	}
	if address.IsLoopback() || address.IsPrivate() {
		return allowTest
	}
	testOnly := []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("2001:2::/48"), netip.MustParsePrefix("2001:db8::/32"),
	}
	for _, prefix := range testOnly {
		if prefix.Contains(address) {
			return allowTest
		}
	}
	nonPublic := []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.88.99.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("100::/64"),
		netip.MustParsePrefix("2001:10::/28"), netip.MustParsePrefix("2001:20::/28"),
	}
	for _, prefix := range nonPublic {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
