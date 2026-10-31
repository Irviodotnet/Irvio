// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build !linux

package underlay

import (
	"context"
)

type MarkedChallengeProbe struct{}
type ChallengeService struct{}
type ChallengeServiceConfig struct{}

func NewMarkedChallengeProbe(uint32) (*MarkedChallengeProbe, error) { return nil, ErrInvalidConfig }
func (*MarkedChallengeProbe) Probe(context.Context, Target) error   { return ErrInvalidConfig }
func NewChallengeService(context.Context, ChallengeServiceConfig) (*ChallengeService, error) {
	return nil, ErrInvalidConfig
}
func (*ChallengeService) Close() error { return nil }
