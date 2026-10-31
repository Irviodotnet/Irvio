// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
)

type TCPProbe struct {
	dial tcptransport.DialerForAddr
	now  func() time.Time
}

func NewTCPProbe(dial tcptransport.DialerForAddr) (*TCPProbe, error) {
	if dial == nil {
		return nil, ErrProbeUnavailable
	}
	return &TCPProbe{dial: dial, now: time.Now}, nil
}

// Probe measures the existing candidate reachability contract through explicit platform policy.
func (p *TCPProbe) Probe(ctx context.Context, candidate Candidate) (ProbeResult, error) {
	if p == nil || p.dial == nil {
		return ProbeResult{}, ErrProbeUnavailable
	}
	return probeTCP(ctx, candidate, p.now, func(ctx context.Context, endpoint netip.AddrPort) (net.Conn, error) {
		return controlsocket.DialEndpoint(ctx, p.dial, endpoint)
	})
}

func probeTCP(ctx context.Context, candidate Candidate, now func() time.Time, dial func(context.Context, netip.AddrPort) (net.Conn, error)) (ProbeResult, error) {
	if ctx == nil || now == nil || dial == nil {
		return ProbeResult{}, ErrProbeUnavailable
	}
	if !candidate.ControlEndpoint.IsValid() || candidate.ControlEndpoint.Port() == 0 {
		return ProbeResult{}, ErrInvalidCandidate
	}
	a := candidate.ControlEndpoint.Addr()
	if a.IsUnspecified() || a.IsMulticast() || a.IsLoopback() || a.Is4In6() || a.IsLinkLocalUnicast() || a.Zone() != "" {
		return ProbeResult{}, ErrInvalidCandidate
	}
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}
	started := now()
	connection, err := dial(ctx, candidate.ControlEndpoint)
	result := ProbeResult{Latency: now().Sub(started), ObservedAt: now().UTC(), NetworkEpoch: candidate.NetworkEpoch}
	if err != nil {
		return result, err
	}
	if connection == nil {
		return result, ErrProbeUnavailable
	}
	result.Reachable = true
	if err := connection.Close(); err != nil {
		return result, fmt.Errorf("close CandidateProbe socket: %w", err)
	}
	return result, nil
}
