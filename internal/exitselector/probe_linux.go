// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package exitselector

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	"github.com/Irviodotnet/Irvio/internal/linuxroute"
)

// MarkedTCPProbe checks literal-IP reachability through M8 bypass without reserving tunnel capacity.
type MarkedTCPProbe struct {
	Mark uint32
	Now  func() time.Time
}

func NewMarkedTCPProbe(mark uint32) (*MarkedTCPProbe, error) {
	if mark == 0 {
		mark = linuxroute.DefaultOuterMark
	}
	if mark != linuxroute.DefaultOuterMark {
		return nil, fmt.Errorf("create marked CandidateProbe: unexpected bypass mark %#x", mark)
	}
	return &MarkedTCPProbe{Mark: mark, Now: time.Now}, nil
}

func (p *MarkedTCPProbe) Probe(ctx context.Context, candidate Candidate) (ProbeResult, error) {
	if p == nil || p.Mark != linuxroute.DefaultOuterMark || p.Now == nil || ctx == nil {
		return ProbeResult{}, ErrProbeUnavailable
	}
	if !candidate.ControlEndpoint.IsValid() || candidate.ControlEndpoint.Port() == 0 {
		return ProbeResult{}, ErrInvalidCandidate
	}
	address := candidate.ControlEndpoint.Addr()
	if address.IsUnspecified() || address.IsMulticast() || address.IsLoopback() || address.Is4In6() || address.IsLinkLocalUnicast() || address.Zone() != "" {
		return ProbeResult{}, ErrInvalidCandidate
	}
	network := "tcp6"
	if candidate.ControlEndpoint.Addr().Is4() {
		network = "tcp4"
	}
	dialer := net.Dialer{Control: controlsocket.Control(p.Mark)}
	started := p.Now()
	connection, err := dialer.DialContext(ctx, network, candidate.ControlEndpoint.String())
	latency := p.Now().Sub(started)
	if err != nil {
		return ProbeResult{Latency: latency, ObservedAt: p.Now().UTC(), NetworkEpoch: candidate.NetworkEpoch}, err
	}
	closeErr := connection.Close()
	result := ProbeResult{Reachable: true, Latency: latency, ObservedAt: p.Now().UTC(), NetworkEpoch: candidate.NetworkEpoch}
	if closeErr != nil {
		return result, fmt.Errorf("close CandidateProbe socket: %w", closeErr)
	}
	return result, nil
}
