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
	"net/netip"
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
	return probeTCP(ctx, candidate, p.Now, func(ctx context.Context, endpoint netip.AddrPort) (net.Conn, error) {
		network := "tcp6"
		if endpoint.Addr().Is4() {
			network = "tcp4"
		}
		dialer := net.Dialer{Control: controlsocket.Control(p.Mark)}
		return dialer.DialContext(ctx, network, endpoint.String())
	})
}
