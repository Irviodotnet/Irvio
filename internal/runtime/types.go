// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/exitselector"
	"hydra.local/hydra/internal/killswitch"
	"hydra.local/hydra/internal/networkstate"
	"hydra.local/hydra/internal/reachability"
	"hydra.local/hydra/internal/underlay"
)

type State uint8

const (
	StateStopped State = iota
	StateStarting
	StateIdle
	StateProtecting
	StateDiscovering
	StateConnecting
	StateConnected
	StateSamePeerRecovery
	StateNetworkMigration
	StateExitReselection
	StateNoExit
	StateLeaseRenewal
	StateDisconnecting
	StateStopping
	StateClosed
	StateError
)

type Reason uint8

const (
	ReasonStart Reason = iota + 1
	ReasonConnect
	ReasonProtected
	ReasonActivePromoted
	ReasonActiveDisconnected
	ReasonUnderlayChanged
	ReasonNetworkChanged
	ReasonNoExit
	ReasonLeaseRenewal
	ReasonDisconnect
	ReasonShutdown
	ReasonChildFailure
	ReasonGenerationStale
)

type Status struct {
	State               State
	RuntimeGeneration   uint64
	Country             exitselector.CountryIntent
	ActivePeer          peer.ID
	ReservePeer         peer.ID
	ColdPeer            peer.ID
	NetworkGeneration   networkstate.Generation
	SessionGeneration   uint64
	TransportGeneration networkstate.Generation
	TunnelLeaseID       string
	TunnelLeaseExpiry   time.Time
	AWGKeyEpoch         uint64
	SelectedEndpoint    netip.AddrPort
	MuxPort             uint16
	Underlay            underlay.State
	ProtectedMode       killswitch.State
	ProtectionAdopted   bool
	ExitContributing    bool
	LastReason          Reason
	PublicNodeRequested bool
	PublicControlIPv4   reachability.State
	PublicControlIPv6   reachability.State
	PublicEndpointIPv4  netip.AddrPort
	PublicEndpointIPv6  netip.AddrPort
	UpdatedAt           time.Time
}

type Event struct {
	Status Status
	Err    error
}

type NetworkSource interface {
	Current() networkstate.State
	Changes() <-chan networkstate.State
	Errors() <-chan error
	Close() error
}

type UnderlayOracle interface {
	Evaluate(context.Context, networkstate.State) (underlay.State, error)
	Current() underlay.State
	Close() error
}

// NetworkReconciler updates logical OS state before new-generation transport work.
type NetworkReconciler interface {
	ReconcileNetwork(context.Context, networkstate.State) error
}

type Protection interface {
	Protect(context.Context) (uint64, error)
	Transition(uint64, killswitch.State) error
	Disconnect(context.Context) error
	Snapshot() killswitch.Snapshot
	Close(context.Context) error
}

type Selector interface {
	Start(context.Context) error
	UpdateUnderlay(exitselector.UnderlayState) error
	Current() exitselector.Snapshot
	Events() <-chan exitselector.Event
	Close() error
}

type SelectorFactory func(exitselector.CountryIntent, exitselector.UnderlayState) (Selector, error)

type Config struct {
	Core             io.Closer
	Network          NetworkSource
	Underlay         UnderlayOracle
	Transport        TransportLifecycle
	Protection       Protection
	NewSelector      SelectorFactory
	Now              func() time.Time
	OperationTimeout time.Duration
	UnderlayInterval time.Duration
	EventCapacity    int
	OnNetworkChange  func(context.Context, networkstate.State) error
}

var (
	ErrInvalidConfig    = errors.New("invalid Irvio runtime configuration")
	ErrInvalidState     = errors.New("invalid Irvio runtime state")
	ErrAlreadyStarted   = errors.New("Irvio runtime already started")
	ErrNotStarted       = errors.New("Irvio runtime not started")
	ErrAlreadyConnected = errors.New("Irvio runtime already connecting or connected")
	ErrNotConnected     = errors.New("Irvio runtime is not protected")
	ErrClosed           = errors.New("Irvio runtime closed")
)
