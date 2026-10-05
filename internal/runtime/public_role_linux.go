// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"

	"github.com/Irviodotnet/Irvio/internal/bootstrap"
	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/linuxroute"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/reachability"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type PublicRoleConfig struct {
	Requested          bool
	DiscoverVerifiers  bool
	VerifierAddresses  []string
	VerifierBootstrap  []string
	ExternalPortIPv4   uint16
	ExternalPortIPv6   uint16
	ProofTTL           time.Duration
	ReverifyLead       time.Duration
	RetryDelay         time.Duration
	AllowTestAddresses bool
	OperationTimeout   time.Duration
	Logger             *slog.Logger
	// Challenger is test-only; production always uses a fresh marked libp2p dialer.
	Challenger reachability.FreshChallenger
}

type PublicRoleStatus struct {
	Requested bool
	IPv4      reachability.Snapshot
	IPv6      reachability.Snapshot
	Epoch     uint64
}

type publicRole struct {
	config     PublicRoleConfig
	node       *discovery.Node
	client     *reachability.Client
	service    *reachability.Service
	verifiers  []peer.AddrInfo
	requester  *reachability.FamilyRequester
	verifierMu sync.Mutex

	mu          sync.Mutex
	publishMu   sync.Mutex
	controllers map[irviov1.ReachabilityFamily]*reachability.Controller
	proofs      map[irviov1.ReachabilityFamily]reachability.Proof
	epoch       uint64
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
	wait        sync.WaitGroup
	closeOnce   sync.Once
	closeErr    error
	watching    bool
	observer    func()
}

func newPublicRole(parent context.Context, node *discovery.Node, config PublicRoleConfig) (_ *publicRole, resultErr error) {
	if parent == nil || node == nil || config.OperationTimeout <= 0 || config.OperationTimeout > time.Minute {
		return nil, ErrInvalidConfig
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.ProofTTL == 0 {
		config.ProofTTL = reachability.DefaultProofTTL
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = time.Minute
	}
	var verifiers []peer.AddrInfo
	if config.Requested {
		var err error
		verifiers, err = bootstrap.Parse(config.VerifierAddresses)
		if err != nil || len(verifiers) < reachability.DefaultQuorum || len(verifiers) > reachability.DefaultVerifierLimit {
			return nil, fmt.Errorf("public role requires 2-3 literal-IP verifier peers: %w", ErrInvalidConfig)
		}
		seen := make(map[peer.ID]struct{}, len(verifiers))
		for _, verifier := range verifiers {
			if verifier.ID == node.PeerID() {
				return nil, ErrInvalidConfig
			}
			if _, duplicate := seen[verifier.ID]; duplicate {
				return nil, ErrInvalidConfig
			}
			seen[verifier.ID] = struct{}{}
		}
		if config.ExternalPortIPv4 == 0 && verifiersSupportFamily(verifiers, irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4) {
			config.ExternalPortIPv4 = externalPort(node, irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4)
		}
		if config.ExternalPortIPv6 == 0 && verifiersSupportFamily(verifiers, irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6) {
			config.ExternalPortIPv6 = externalPort(node, irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6)
		}
		if config.ExternalPortIPv4 == 0 && config.ExternalPortIPv6 == 0 {
			return nil, fmt.Errorf("public role has no advertised external control port: %w", ErrInvalidConfig)
		}
	}
	challenger := config.Challenger
	if challenger == nil {
		fresh, err := reachability.NewFreshDialer(node.IdentityPrivateKey(), linuxroute.IrvioBypassMark)
		if err != nil {
			return nil, err
		}
		challenger = fresh
	}
	service, err := reachability.NewService(reachability.ServiceConfig{
		Host: node.Host(), PrivateKey: node.IdentityPrivateKey(), Challenger: challenger,
		ObservedIP: reachability.ObservedIPPolicy{
			AllowDocumentation: config.AllowTestAddresses, AllowPrivate: config.AllowTestAddresses,
			AllowLoopback: config.AllowTestAddresses,
		},
		ProofTTL: config.ProofTTL, DialTimeout: config.OperationTimeout,
	})
	if err != nil {
		return nil, err
	}
	clientConfig := reachability.ClientConfig{Host: node.Host(), MaxPending: 2 * reachability.DefaultVerifierLimit}
	var requester *reachability.FamilyRequester
	if config.Requested {
		requester, err = reachability.NewFamilyRequester(node.IdentityPrivateKey(), node.ControlMark(), verifiers)
		if err != nil {
			_ = service.Close()
			return nil, err
		}
		clientConfig.Requester = requester
	}
	client, err := reachability.NewClient(clientConfig)
	if err != nil {
		_ = service.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	role := &publicRole{
		config: config, node: node, client: client, service: service,
		controllers: make(map[irviov1.ReachabilityFamily]*reachability.Controller, 2),
		proofs:      make(map[irviov1.ReachabilityFamily]reachability.Proof, 2),
		verifiers:   verifiers, ctx: ctx, cancel: cancel,
		requester: requester,
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, role.Close())
		}
	}()
	stop := context.AfterFunc(parent, cancel)
	role.wait.Add(1)
	go func() {
		defer role.wait.Done()
		defer stop()
		<-ctx.Done()
	}()
	return role, nil
}

func (r *publicRole) Enable(ctx context.Context, state networkstate.State) error {
	if r == nil || ctx == nil || state.Generation == 0 {
		return ErrInvalidConfig
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClosed
	}
	if !r.config.Requested {
		r.mu.Unlock()
		return nil
	}
	r.epoch++
	epoch := r.epoch
	r.mu.Unlock()
	return r.enableEpoch(ctx, state, epoch)
}

func (r *publicRole) enableEpoch(ctx context.Context, state networkstate.State, epoch uint64) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClosed
	}
	if !r.config.Requested || epoch != r.epoch {
		r.mu.Unlock()
		return reachability.ErrStaleGeneration
	}
	ids := make([]peer.ID, len(r.verifiers))
	for index, verifier := range r.verifiers {
		ids[index] = verifier.ID
	}
	r.mu.Unlock()
	var result error
	for _, item := range []struct {
		family irviov1.ReachabilityFamily
		port   uint16
	}{
		{irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4, r.config.ExternalPortIPv4},
		{irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6, r.config.ExternalPortIPv6},
	} {
		if item.port == 0 {
			continue
		}
		controller, err := r.controller(item.family, epoch)
		if err != nil {
			return errors.Join(result, err)
		}
		attempt, cancel := context.WithTimeout(ctx, r.config.OperationTimeout)
		err = controller.Enable(attempt, reachability.VerificationRequest{
			Family: item.family, ExternalPort: item.port, NetworkGeneration: uint64(state.Generation),
			PublicEpoch: epoch, Verifiers: ids,
		})
		cancel()
		if err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}

func (r *publicRole) controller(family irviov1.ReachabilityFamily, epoch uint64) (*reachability.Controller, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrClosed
	}
	if !r.config.Requested || epoch != r.epoch {
		return nil, reachability.ErrStaleGeneration
	}
	if existing := r.controllers[family]; existing != nil {
		return existing, nil
	}
	var refresh func(context.Context) ([]peer.ID, error)
	if r.config.DiscoverVerifiers {
		refresh = r.refreshVerifiers
	}
	controller, err := reachability.NewController(reachability.ControllerConfig{
		Subject: r.node.PeerID(), Attestor: r.client, ReverifyLead: r.config.ReverifyLead,
		RetryDelay:       r.config.RetryDelay,
		RefreshVerifiers: refresh,
		Publish: func(ctx context.Context, proof *reachability.Proof) error {
			return r.publish(ctx, family, proof)
		},
	})
	if err != nil {
		return nil, err
	}
	r.controllers[family] = controller
	return controller, nil
}

func (r *publicRole) refreshVerifiers(ctx context.Context) ([]peer.ID, error) {
	r.verifierMu.Lock()
	defer r.verifierMu.Unlock()
	addresses, err := discoverPublicVerifiers(ctx, r.node, r.config.VerifierBootstrap, r.config.AllowTestAddresses)
	if err != nil {
		return nil, err
	}
	peers, err := bootstrap.Parse(addresses)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || !r.config.Requested {
		return nil, ErrClosed
	}
	if err := r.requester.SetTargets(peers); err != nil {
		return nil, err
	}
	r.verifiers = peers
	ids := make([]peer.ID, len(peers))
	for i, info := range peers {
		ids[i] = info.ID
	}
	r.config.Logger.Info("PUBLIC_VERIFIERS_REFRESHED", "count", len(ids))
	return ids, nil
}

func (r *publicRole) publish(_ context.Context, family irviov1.ReachabilityFamily, proof *reachability.Proof) error {
	r.publishMu.Lock()
	defer r.publishMu.Unlock()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClosed
	}
	if proof == nil {
		delete(r.proofs, family)
	} else {
		if proof.Endpoint.PublicEpoch != r.epoch {
			r.mu.Unlock()
			return reachability.ErrStaleGeneration
		}
		r.proofs[family] = *proof
	}
	controller := r.controllers[family]
	proofs := make([]reachability.Proof, 0, len(r.proofs))
	for _, current := range r.proofs {
		proofs = append(proofs, current)
	}
	r.mu.Unlock()
	if err := r.node.UpdatePublicReachability(proofs); err != nil {
		return err
	}
	r.notifyObserver()
	if proof == nil {
		var reason error
		if controller != nil {
			reason = controller.Snapshot().LastError
		}
		r.config.Logger.Info("PUBLIC_REACHABILITY_UNVERIFIED", "family", family, "reason", reason)
	} else {
		r.config.Logger.Info("PUBLIC_REACHABILITY_VERIFIED", "family", family,
			"endpoint", proof.Endpoint.Address, "network_generation", proof.Endpoint.NetworkGeneration,
			"public_epoch", proof.Endpoint.PublicEpoch, "expires_at", proof.ExpiresAt)
	}
	return nil
}

func (r *publicRole) NetworkChanged(state networkstate.State) {
	if r == nil || state.Generation == 0 {
		return
	}
	epoch, requested, err := r.invalidateNetwork(state)
	if err != nil {
		r.config.Logger.Error("PUBLIC_REACHABILITY_INVALIDATION_FAILED", "network_generation", state.Generation, "reason", err)
		return
	}
	if requested {
		r.reverifyNetwork(state, epoch)
	}
}

// invalidateNetwork revokes old capabilities before migration exposes a new endpoint.
func (r *publicRole) invalidateNetwork(state networkstate.State) (uint64, bool, error) {
	if r == nil || state.Generation == 0 {
		return 0, false, ErrInvalidConfig
	}
	r.mu.Lock()
	if r.closed || !r.config.Requested {
		r.mu.Unlock()
		return 0, false, nil
	}
	controllers := make([]*reachability.Controller, 0, len(r.controllers))
	for _, controller := range r.controllers {
		controllers = append(controllers, controller)
	}
	r.epoch++
	epoch := r.epoch
	r.proofs = make(map[irviov1.ReachabilityFamily]reachability.Proof, 2)
	r.mu.Unlock()
	var result error
	for _, controller := range controllers {
		result = errors.Join(result, controller.InvalidateNetwork(context.Background(), uint64(state.Generation)))
	}
	r.publishMu.Lock()
	result = errors.Join(result, r.node.UpdatePublicReachability(nil))
	r.publishMu.Unlock()
	r.notifyObserver()
	return epoch, true, result
}

func (r *publicRole) reverifyNetwork(state networkstate.State, epoch uint64) {
	r.mu.Lock()
	if r.closed || !r.config.Requested || epoch != r.epoch {
		r.mu.Unlock()
		return
	}
	r.wait.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wait.Done()
		ctx, cancel := context.WithTimeout(r.ctx, r.config.OperationTimeout)
		defer cancel()
		if err := r.enableEpoch(ctx, state, epoch); err != nil && !errors.Is(err, context.Canceled) &&
			!errors.Is(err, reachability.ErrStaleGeneration) {
			r.config.Logger.Warn("PUBLIC_REACHABILITY_UNVERIFIED", "network_generation", state.Generation, "reason", err)
		}
	}()
}

func (r *publicRole) WatchNetwork(source NetworkSource) error {
	if r == nil || source == nil {
		return ErrInvalidConfig
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClosed
	}
	if r.watching {
		r.mu.Unlock()
		return ErrAlreadyStarted
	}
	r.watching = true
	r.wait.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wait.Done()
		changes, failures := source.Changes(), source.Errors()
		for changes != nil || failures != nil {
			select {
			case <-r.ctx.Done():
				return
			case state, ok := <-changes:
				if !ok {
					changes = nil
					continue
				}
				r.NetworkChanged(state)
			case err, ok := <-failures:
				if !ok {
					failures = nil
					continue
				}
				if err != nil {
					r.config.Logger.Error("PUBLIC_REACHABILITY_NETWORK_MONITOR_FAILED", "reason", err)
				}
			}
		}
	}()
	return nil
}

func (r *publicRole) Status() PublicRoleStatus {
	if r == nil {
		return PublicRoleStatus{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	status := PublicRoleStatus{Requested: r.config.Requested, Epoch: r.epoch}
	if controller := r.controllers[irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4]; controller != nil {
		status.IPv4 = controller.Snapshot()
	}
	if controller := r.controllers[irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6]; controller != nil {
		status.IPv6 = controller.Snapshot()
	}
	return status
}

func (r *publicRole) Disable(ctx context.Context) error {
	if r == nil || ctx == nil {
		return ErrInvalidConfig
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrClosed
	}
	r.config.Requested = false
	r.epoch++
	r.proofs = make(map[irviov1.ReachabilityFamily]reachability.Proof, 2)
	controllers := make([]*reachability.Controller, 0, len(r.controllers))
	for _, controller := range r.controllers {
		controllers = append(controllers, controller)
	}
	r.mu.Unlock()
	var result error
	for _, controller := range controllers {
		result = errors.Join(result, controller.Disable(ctx))
	}
	r.publishMu.Lock()
	result = errors.Join(result, r.node.UpdatePublicReachability(nil))
	r.publishMu.Unlock()
	r.notifyObserver()
	return result
}

func (r *publicRole) setObserver(observer func()) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.observer = observer
	r.mu.Unlock()
}

func (r *publicRole) notifyObserver() {
	r.mu.Lock()
	observer := r.observer
	r.mu.Unlock()
	if observer != nil {
		observer()
	}
}

func (r *publicRole) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.cancel()
		controllers := r.controllers
		r.controllers = nil
		r.proofs = nil
		r.mu.Unlock()
		for _, controller := range controllers {
			r.closeErr = errors.Join(r.closeErr, controller.Close())
		}
		r.wait.Wait()
		r.closeErr = errors.Join(r.closeErr, r.client.Close(), r.service.Close())
	})
	return r.closeErr
}

func verifiersSupportFamily(verifiers []peer.AddrInfo, family irviov1.ReachabilityFamily) bool {
	protocolCode := ma.P_IP6
	if family == irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4 {
		protocolCode = ma.P_IP4
	}
	for _, verifier := range verifiers {
		for _, address := range verifier.Addrs {
			if _, err := address.ValueForProtocol(protocolCode); err == nil {
				return true
			}
		}
	}
	return false
}

func externalPort(owner *discovery.Node, family irviov1.ReachabilityFamily) uint16 {
	protocolCode := ma.P_IP6
	if family == irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4 {
		protocolCode = ma.P_IP4
	}
	for _, address := range owner.Host().Addrs() {
		if _, err := address.ValueForProtocol(protocolCode); err != nil {
			continue
		}
		value, err := address.ValueForProtocol(ma.P_TCP)
		if err != nil {
			continue
		}
		port, err := strconv.ParseUint(value, 10, 16)
		if err == nil {
			return uint16(port)
		}
	}
	return 0
}
