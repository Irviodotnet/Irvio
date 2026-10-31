// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build !linux

package exitselector

import "context"

type MarkedTCPProbe struct{}

func NewMarkedTCPProbe(uint32) (*MarkedTCPProbe, error) { return nil, ErrProbeUnavailable }
func (*MarkedTCPProbe) Probe(context.Context, Candidate) (ProbeResult, error) {
	return ProbeResult{}, ErrProbeUnavailable
}
