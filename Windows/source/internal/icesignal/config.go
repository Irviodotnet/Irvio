// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesignal

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/icesession"
)

const (
	defaultStreamTimeout       = 5 * time.Second
	defaultDescriptionTTL      = 2 * time.Minute
	defaultClockSkew           = 30 * time.Second
	defaultReplayTTL           = 5 * time.Minute
	defaultMaxConcurrentOffers = 4
	defaultMaxSessionsPerPeer  = 2
	defaultMaxRecentSessions   = 1024
	defaultAcceptedQueue       = 4
	maximumLimit               = 65536
)

// Config bounds signaling time, memory, replay, and responder concurrency.
type Config struct {
	StreamTimeout       time.Duration
	DescriptionTTL      time.Duration
	ClockSkew           time.Duration
	ReplayTTL           time.Duration
	MaxConcurrentOffers int
	MaxSessionsPerPeer  int
	MaxRecentSessions   int
	AcceptedQueue       int
	Validation          icesession.ValidationConfig
	Logger              *slog.Logger
}

// DefaultConfig returns conservative one-shot signaling limits.
func DefaultConfig() Config {
	return Config{
		StreamTimeout: defaultStreamTimeout, DescriptionTTL: defaultDescriptionTTL,
		ClockSkew: defaultClockSkew, ReplayTTL: defaultReplayTTL,
		MaxConcurrentOffers: defaultMaxConcurrentOffers,
		MaxSessionsPerPeer:  defaultMaxSessionsPerPeer,
		MaxRecentSessions:   defaultMaxRecentSessions, AcceptedQueue: defaultAcceptedQueue,
		Validation: icesession.DefaultValidationConfig(),
	}
}

func (c Config) validate() error {
	if c.StreamTimeout <= 0 || c.DescriptionTTL <= 0 || c.DescriptionTTL > 10*time.Minute ||
		c.ClockSkew < 0 || c.ClockSkew > time.Minute || c.ReplayTTL <= 0 || c.ReplayTTL > time.Hour {
		return fmt.Errorf("invalid ICE signaling duration")
	}
	limits := []int{c.MaxConcurrentOffers, c.MaxSessionsPerPeer, c.MaxRecentSessions, c.AcceptedQueue}
	for _, limit := range limits {
		if limit <= 0 || limit > maximumLimit {
			return fmt.Errorf("ICE signaling limits must be within 1..%d", maximumLimit)
		}
	}
	if c.MaxSessionsPerPeer > c.AcceptedQueue {
		return fmt.Errorf("sessions per peer exceed accepted queue")
	}
	if err := c.Validation.Validate(); err != nil {
		return fmt.Errorf("invalid ICE description validation: %w", err)
	}
	return nil
}

// Authorizer controls whether a remote peer may request an EXIT ICE session.
type Authorizer interface {
	AuthorizeICE(ctx context.Context, remote peer.ID, purpose icesession.SessionPurpose) error
}

// AuthorizerFunc adapts a function to Authorizer.
type AuthorizerFunc func(context.Context, peer.ID, icesession.SessionPurpose) error

// AuthorizeICE invokes f.
func (f AuthorizerFunc) AuthorizeICE(ctx context.Context, remote peer.ID, purpose icesession.SessionPurpose) error {
	return f(ctx, remote, purpose)
}

// SessionFactory creates a responder PreparedSession after offer validation.
type SessionFactory interface {
	Prepare(ctx context.Context, remote peer.ID, description icesession.Description) (*icesession.PreparedSession, error)
}

// SessionPreparer creates an initiator PreparedSession without signaling.
type SessionPreparer interface {
	Prepare(context.Context) (*icesession.PreparedSession, error)
}

// SessionFactoryFunc adapts a function to SessionFactory.
type SessionFactoryFunc func(context.Context, peer.ID, icesession.Description) (*icesession.PreparedSession, error)

// Prepare invokes f.
func (f SessionFactoryFunc) Prepare(ctx context.Context, remote peer.ID, description icesession.Description) (*icesession.PreparedSession, error) {
	return f(ctx, remote, description)
}
