// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/networkstate"
)

const (
	MaxCandidateBatch = 10
	MaxCriticalPeers  = 3
)

// CountryIntent preserves explicit user choice across selection failures.
type CountryIntent string

// UptimeClass is a coarse provider hint rather than global reputation.
type UptimeClass uint8

const (
	UptimeUnknown UptimeClass = iota
	UptimeNew
	UptimeMature
)

// CandidateTransport describes establishment, not the eventual direct ICE/AWG path.
type CandidateTransport uint8

const (
	CandidateTransportDirect CandidateTransport = iota
	CandidateTransportBrokered
)

// Candidate contains bounded transient control metadata for one Exit identity.
type Candidate struct {
	PeerID                 peer.ID
	Country                string
	ControlEndpoint        netip.AddrPort
	ProtocolVersion        uint32
	Capabilities           uint64
	Accepting              bool
	AvailableBandwidthMbps uint32
	Uptime                 UptimeClass
	NetworkEpoch           uint64
	GeneratedAt            time.Time
	ExpiresAt              time.Time
	Transport              CandidateTransport
	DescriptorID           [32]byte
	DescriptorIncarnation  uint64
	DescriptorSequence     uint64
	// SignalingRouteEpoch fences broker metadata; newer routes immediately clear route-failure suppression.
	SignalingRouteEpoch uint64
}

type CandidateRequest struct {
	Country        CountryIntent
	Limit          int
	ExcludePeerIDs []peer.ID
}

type CandidateBatch struct {
	Country            CountryIntent
	AvailablePeerCount int
	GeneratedAt        time.Time
	Candidates         []Candidate
}

// CandidateProvider hides discovery implementation details from selection.
type CandidateProvider interface {
	GetCandidates(context.Context, CandidateRequest) (CandidateBatch, error)
}

type CandidateProviderFunc func(context.Context, CandidateRequest) (CandidateBatch, error)

func (f CandidateProviderFunc) GetCandidates(ctx context.Context, request CandidateRequest) (CandidateBatch, error) {
	return f(ctx, request)
}

type ProbeResult struct {
	Reachable    bool
	Latency      time.Duration
	ObservedAt   time.Time
	NetworkEpoch uint64
	Kind         ProbeKind
}

// ProbeKind separates direct RTT from broker freshness; zero retains direct M9 semantics.
type ProbeKind uint8

const (
	ProbeDirectControl ProbeKind = iota
	ProbeBrokerMetadata
)

// CandidateProbe is a bounded non-tunneling readiness observation.
type CandidateProbe interface {
	Probe(context.Context, Candidate) (ProbeResult, error)
}

type CandidateProbeFunc func(context.Context, Candidate) (ProbeResult, error)

func (f CandidateProbeFunc) Probe(ctx context.Context, candidate Candidate) (ProbeResult, error) {
	return f(ctx, candidate)
}

type CandidateEvaluation struct {
	Candidate Candidate
	Probe     ProbeResult
}

type Selection struct {
	Active  *CandidateEvaluation
	Reserve *CandidateEvaluation
	Cold    *CandidateEvaluation
}

// UnderlayState is derived from an authoritative networkstate snapshot.
type UnderlayState struct {
	NetworkGeneration networkstate.Generation
	IPv4Usable        bool
	IPv6Usable        bool
}

func (s UnderlayState) Usable() bool { return s.IPv4Usable || s.IPv6Usable }

type ConnectOutcome uint8

const (
	OutcomeUnknown ConnectOutcome = iota
	OutcomeConnected
	OutcomeBusy
	OutcomeUnavailable
	OutcomeIncompatible
	OutcomeAuthFailed
	OutcomeFailed
	// OutcomeRouteFailed scopes failure to the candidate's broker route set.
	OutcomeRouteFailed
	// OutcomeLocalPromotionFailed means the remote path succeeded but local activation failed.
	OutcomeLocalPromotionFailed
)

type ConnectionEventType uint8

const (
	ConnectionDisconnected ConnectionEventType = iota + 1
	ConnectionRecovered
	ConnectionFailed
	ConnectionUpdated
)

type ConnectionEvent struct {
	Type ConnectionEventType
	At   time.Time
}

// Connection owns the authenticated path and resources required before ACTIVE promotion.
type Connection interface {
	Events() <-chan ConnectionEvent
	Close() error
}

// PromotableConnection stages resources; only Selector's fenced event loop may publish them.
type PromotableConnection interface {
	Connection
	Promote(context.Context) error
}

// PromotionTimeoutConnection bounds the staged Data Plane promotion phase.
type PromotionTimeoutConnection interface {
	PromotableConnection
	PromotionTimeout() time.Duration
}

// ConnectionDiagnostics is a redacted immutable snapshot for daemon status.
type ConnectionDiagnostics struct {
	NetworkGeneration     networkstate.Generation
	SessionGeneration     uint64
	TunnelLeaseID         string
	TunnelLeaseValidUntil time.Time
	AWGKeyEpoch           uint64
	SelectedEndpoint      netip.AddrPort
}

// DiagnosableConnection exposes no credentials or mutable transport objects.
type DiagnosableConnection interface {
	Connection
	Diagnostics() ConnectionDiagnostics
}

type ConnectResult struct {
	Outcome    ConnectOutcome
	Connection Connection
}

type Connector interface {
	Connect(context.Context, Candidate) (ConnectResult, error)
}

type ConnectorFunc func(context.Context, Candidate) (ConnectResult, error)

func (f ConnectorFunc) Connect(ctx context.Context, candidate Candidate) (ConnectResult, error) {
	return f(ctx, candidate)
}

// ProtectedModeCheck forbids selection outside M8 fail-closed protection.
type ProtectedModeCheck func() bool

type State uint8

const (
	StateIdle State = iota
	StateSelecting
	StateConnectingInitial
	StateConnected
	StateSamePeerRecovery
	StateWaitingForUnderlay
	StateConnectingReserve
	StateConnectingCold
	StateRefreshingAfterExhaustion
	StateNoExit
	StateClosing
	StateClosed
	StateLocalPromotionFailed
)

type EventType uint8

const (
	EventInitialSelectionStarted EventType = iota + 1
	EventCandidateBatchReceived
	EventCandidateProbeStarted
	EventCandidateProbeCompleted
	EventActivePromoted
	EventActiveDisconnected
	EventUnderlayLost
	EventUnderlayRestored
	EventSamePeerRecoveryStarted
	EventSamePeerRecovered
	EventSamePeerRecoveryExpired
	EventReserveConnectStarted
	EventReserveUnavailable
	EventReserveBusy
	EventColdConnectStarted
	EventCandidateSetExhausted
	EventFreshBatchRequested
	EventProviderUnavailable
	EventProviderRecovered
	EventNoExitAvailable
	EventExitFailoverCompleted
	EventBackgroundRefreshStarted
	EventBackgroundRefreshCompleted
	EventCountryIntentChanged
	EventConnectionUpdated
	EventClosed
	EventLocalPromotionFailed
)

type Event struct {
	Type                EventType
	State               State
	Country             CountryIntent
	PeerID              peer.ID
	NetworkGeneration   networkstate.Generation
	LifecycleGeneration uint64
	Outcome             ConnectOutcome
	Duration            time.Duration
	At                  time.Time
	Err                 error
}

type Snapshot struct {
	State               State
	Country             CountryIntent
	Underlay            UnderlayState
	LifecycleGeneration uint64
	Active              *CandidateEvaluation
	Reserve             *CandidateEvaluation
	Cold                *CandidateEvaluation
	ProviderAvailable   bool
	RecoveryRemaining   time.Duration
	Connection          ConnectionDiagnostics
	Closed              bool
}

type Stats struct {
	ProviderRequests        uint64
	ProviderFailures        uint64
	BatchesReceived         uint64
	CandidatesReceived      uint64
	ProbesStarted           uint64
	ProbesCompleted         uint64
	MaximumConcurrentProbes uint64
	ConnectAttempts         uint64
	Connected               uint64
	Busy                    uint64
	Unavailable             uint64
	Failed                  uint64
	LocalPromotionFailures  uint64
	SamePeerRecoveries      uint64
	Failovers               uint64
	FreshBatchRequests      uint64
	UnderlayPauses          uint64
	StaleResultsDiscarded   uint64
	ProbeLatencyP50         time.Duration
	ProbeLatencyP95         time.Duration
	ConnectLatencyP50       time.Duration
	ConnectLatencyP95       time.Duration
}

// OutcomeError preserves structured connection-failure classification.
type OutcomeError struct {
	Outcome ConnectOutcome
	Err     error
}

func (e *OutcomeError) Error() string {
	if e == nil || e.Err == nil {
		return "Exit connection failed"
	}
	return e.Err.Error()
}

func (e *OutcomeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func classifyOutcome(result ConnectResult, err error) ConnectOutcome {
	var outcomeError *OutcomeError
	if errors.As(err, &outcomeError) && outcomeError.Outcome != OutcomeUnknown {
		return outcomeError.Outcome
	}
	if err != nil {
		if result.Outcome != OutcomeUnknown && result.Outcome != OutcomeConnected {
			return result.Outcome
		}
		return OutcomeFailed
	}
	if result.Outcome != OutcomeUnknown {
		return result.Outcome
	}
	return OutcomeFailed
}
