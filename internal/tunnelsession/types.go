// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnelsession

import (
	"context"
	"io"
	"net/netip"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"hydra.local/hydra/internal/networkstate"
	"hydra.local/hydra/internal/socketmux"
	"hydra.local/hydra/internal/tunnel"
	"hydra.local/hydra/internal/tunnellease"
)

// DNSPolicy describes tunnel resolvers; production applies per-link DNS after promotion.
type DNSPolicy struct {
	Servers []netip.AddrPort
}

// TUNFactory creates the one kernel TUN owned by a logical session.
type TUNFactory func(context.Context) (tunnel.DeviceOwner, error)

// LeaseReleaser releases the authenticated Exit-side lease on final close.
type LeaseReleaser func(context.Context, tunnellease.ID) error

// Config creates the logical client session; ActivateGeneration installs routing and AWG later.
type Config struct {
	LocalPeer    peer.ID
	RemoteExit   peer.ID
	Lease        tunnellease.Lease
	DNS          DNSPolicy
	NewTUN       TUNFactory
	Release      LeaseReleaser
	OuterMark    uint32
	CloseTimeout time.Duration
}

// Generation transfers transport ownership to Session even on stale rejection.
type Generation struct {
	ID        networkstate.Generation
	Mux       *socketmux.Mux
	Routing   io.Closer
	DataPlane io.Closer
}

// Snapshot is a redacted immutable logical tunnel status.
type Snapshot struct {
	LocalPeer         peer.ID
	RemoteExit        peer.ID
	Lease             tunnellease.Lease
	TUNName           string
	TUNIfIndex        int
	NetworkGeneration networkstate.Generation
	DNS               DNSPolicy
	Closed            bool
}
