// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesession

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/pion/ice/v4"

	"hydra.local/hydra/internal/iceadapter"
)

const defaultGatherTimeout = 5 * time.Second

// AgentTiming uses pinned Pion defaults for zero values, keeping test timing out of production.
type AgentTiming struct {
	DisconnectedTimeout time.Duration
	FailedTimeout       time.Duration
	KeepaliveInterval   time.Duration
	CheckInterval       time.Duration
}

func (c AgentTiming) validate() error {
	values := []time.Duration{c.DisconnectedTimeout, c.FailedTimeout, c.KeepaliveInterval, c.CheckInterval}
	for _, value := range values {
		if value < 0 {
			return fmt.Errorf("ICE timing values must not be negative")
		}
	}
	if c.KeepaliveInterval > 0 && c.DisconnectedTimeout > 0 && c.KeepaliveInterval >= c.DisconnectedTimeout {
		return fmt.Errorf("ICE keepalive interval must be shorter than disconnected timeout")
	}
	return nil
}

// SessionConfig controls one Agent and immutable local description.
type SessionConfig struct {
	ListenAddresses   []netip.Addr
	SrflxObservations []SrflxObservation
	Validation        ValidationConfig
	GatherTimeout     time.Duration
	Timing            AgentTiming
	Logger            *slog.Logger
}

// DefaultSessionConfig returns production validation and Pion timing defaults.
func DefaultSessionConfig(addresses ...netip.Addr) SessionConfig {
	return SessionConfig{
		ListenAddresses: append([]netip.Addr(nil), addresses...),
		Validation:      DefaultValidationConfig(), GatherTimeout: defaultGatherTimeout,
	}
}

// Validate checks static session preparation limits.
func (c SessionConfig) Validate() error {
	if c.GatherTimeout <= 0 || len(c.ListenAddresses) == 0 || len(c.ListenAddresses) > 16 {
		return fmt.Errorf("invalid timeout or listen addresses")
	}
	if err := c.Validation.Validate(); err != nil {
		return err
	}
	return c.Timing.validate()
}

// PrepareWithSharedAdapter borrows the adapter; release removes only this session.
func PrepareWithSharedAdapter(ctx context.Context, adapter *iceadapter.Adapter, muxPort uint16, transportGeneration uint64, config SessionConfig, release func()) (_ *PreparedSession, resultErr error) {
	if ctx == nil || adapter == nil || muxPort == 0 || transportGeneration == 0 || release == nil {
		return nil, fmt.Errorf("prepare ICE session: nil or invalid dependency")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("prepare ICE session: %w", err)
	}
	var agent *ice.Agent
	defer func() {
		if resultErr != nil {
			if agent != nil {
				_ = agent.Close()
			}
			release()
		}
	}()

	options := []ice.AgentOption{
		ice.WithUDPMux(adapter),
		ice.WithCandidateTypes([]ice.CandidateType{ice.CandidateTypeHost}),
		ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6}),
		ice.WithMulticastDNSMode(ice.MulticastDNSModeDisabled),
		ice.WithContinualGatheringPolicy(ice.GatherOnce),
	}
	if config.Validation.AllowLoopback {
		options = append(options, ice.WithIncludeLoopback())
	}
	if config.Timing.DisconnectedTimeout > 0 {
		options = append(options, ice.WithDisconnectedTimeout(config.Timing.DisconnectedTimeout))
	}
	if config.Timing.FailedTimeout > 0 {
		options = append(options, ice.WithFailedTimeout(config.Timing.FailedTimeout))
	}
	if config.Timing.KeepaliveInterval > 0 {
		options = append(options, ice.WithKeepaliveInterval(config.Timing.KeepaliveInterval))
	}
	if config.Timing.CheckInterval > 0 {
		options = append(options, ice.WithCheckInterval(config.Timing.CheckInterval))
	}
	var err error
	agent, err = ice.NewAgentWithOptions(options...)
	if err != nil {
		return nil, fmt.Errorf("prepare Pion Agent: %w", err)
	}

	var candidatesMu sync.Mutex
	var gathered []ice.Candidate
	done := make(chan struct{})
	var doneOnce sync.Once
	if err := agent.OnCandidate(func(candidate ice.Candidate) {
		if candidate == nil {
			doneOnce.Do(func() { close(done) })
			return
		}
		candidatesMu.Lock()
		gathered = append(gathered, candidate)
		candidatesMu.Unlock()
	}); err != nil {
		return nil, fmt.Errorf("register Pion candidate callback: %w", err)
	}
	if err := agent.GatherCandidates(); err != nil {
		return nil, fmt.Errorf("gather Pion host candidates: %w", err)
	}
	gatherContext, cancel := context.WithTimeout(ctx, config.GatherTimeout)
	defer cancel()
	select {
	case <-gatherContext.Done():
		return nil, fmt.Errorf("wait for Pion host candidates: %w", gatherContext.Err())
	case <-done:
	}
	ufrag, password, err := agent.GetLocalUserCredentials()
	if err != nil {
		return nil, fmt.Errorf("get Pion local credentials: %w", err)
	}
	candidatesMu.Lock()
	pionCandidates := append([]ice.Candidate(nil), gathered...)
	candidatesMu.Unlock()
	hosts := make([]Candidate, 0, len(pionCandidates))
	for _, pionCandidate := range pionCandidates {
		candidate, err := CandidateFromPion(pionCandidate, config.Validation)
		if err != nil {
			return nil, fmt.Errorf("convert Pion host candidate: %w", err)
		}
		if candidate.Type != CandidateTypeHost || candidate.Port != muxPort {
			return nil, fmt.Errorf("Pion host candidate violates Irvio mux ownership")
		}
		hosts = append(hosts, candidate)
	}
	all := append([]Candidate(nil), hosts...)
	for _, observation := range config.SrflxObservations {
		if observation.LocalEndpoint.Port() != muxPort {
			return nil, fmt.Errorf("srflx local port %d differs from mux port %d", observation.LocalEndpoint.Port(), muxPort)
		}
		candidate, redundant, err := candidateFromObservation(observation, hosts, config.Validation)
		if err != nil {
			return nil, err
		}
		if !redundant {
			all = append(all, candidate)
		}
	}
	description, err := ValidateDescription(Description{
		Credentials: Credentials{Ufrag: ufrag, Pwd: password}, Candidates: all,
	}, config.Validation)
	if err != nil {
		return nil, err
	}
	if config.Logger != nil {
		config.Logger.Info("ICE_SESSION_PREPARED", "candidate_count", len(description.Candidates))
	}
	return &PreparedSession{
		agent: agent, adapter: adapter, release: release, description: description,
		transportGeneration: transportGeneration,
	}, nil
}

// NewSessionID returns a nonzero cryptographically random 128-bit ID.
func NewSessionID() (SessionID, error) {
	for attempt := 0; attempt < 2; attempt++ {
		var id SessionID
		if _, err := io.ReadFull(rand.Reader, id[:]); err != nil {
			return SessionID{}, fmt.Errorf("generate ICE session ID: %w", err)
		}
		if id != (SessionID{}) {
			return id, nil
		}
	}
	return SessionID{}, errors.New("generated all-zero ICE session ID twice")
}
