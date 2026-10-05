// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package underlay

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/networkstate"
)

type Reachability uint8

const (
	Unknown Reachability = iota
	Reachable
	Unreachable
)

type Target struct {
	PeerID   peer.ID
	Endpoint netip.AddrPort
}

type TargetProvider interface {
	Targets(context.Context, networkstate.Family, int) ([]Target, error)
}

type TargetProviderFunc func(context.Context, networkstate.Family, int) ([]Target, error)

func (f TargetProviderFunc) Targets(ctx context.Context, family networkstate.Family, limit int) ([]Target, error) {
	return f(ctx, family, limit)
}

type Prober interface {
	Probe(context.Context, Target) error
}

type ProberFunc func(context.Context, Target) error

func (f ProberFunc) Probe(ctx context.Context, target Target) error { return f(ctx, target) }

type FamilyEvidence struct {
	State      Reachability
	ObservedAt time.Time
	Target     Target
	Failures   uint32
}

type State struct {
	NetworkGeneration networkstate.Generation
	IPv4              FamilyEvidence
	IPv6              FamilyEvidence
}

func (s State) AnyReachable() bool {
	return s.IPv4.State == Reachable || s.IPv6.State == Reachable
}

type Config struct {
	Targets          TargetProvider
	Prober           Prober
	Now              func() time.Time
	ProbeTimeout     time.Duration
	TargetLimit      int
	Parallelism      int
	FailureThreshold uint32
}

func DefaultConfig(targets TargetProvider, prober Prober) Config {
	return Config{
		Targets: targets, Prober: prober, Now: time.Now,
		ProbeTimeout: 750 * time.Millisecond, TargetLimit: 3,
		Parallelism: 3, FailureThreshold: 2,
	}
}

var (
	ErrInvalidConfig     = errors.New("invalid underlay oracle configuration")
	ErrInvalidTarget     = errors.New("invalid underlay probe target")
	ErrChallengeMismatch = errors.New("underlay challenge response mismatch")
	ErrClosed            = errors.New("underlay reachability oracle is closed")
)
