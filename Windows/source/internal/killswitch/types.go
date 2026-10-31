// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"context"
	"io"
	"time"
)

const (
	DefaultTableName = "hydra_protect"
	PolicyVersion    = uint32(1)
	OwnerMarker      = "hydra-protected-mode-v1"
)

// Policy protects new physical interfaces without needing interface-specific rewrites.
type Policy struct {
	TableName         string
	TUNName           string
	BypassMark        uint32
	TunnelControlMark uint32
}

// EnsureResult describes how kernel protection was obtained.
type EnsureResult uint8

const (
	EnsureInstalled EnsureResult = iota + 1
	EnsureAdopted
	EnsureReconciled
)

// Counter is an independently read kernel rule counter.
type Counter struct {
	Packets uint64
	Bytes   uint64
}

// KernelSnapshot is a redacted view of the persistent protection object.
type KernelSnapshot struct {
	TableName               string
	Version                 uint32
	Installed               bool
	Complete                bool
	SourceValidationEnabled bool
	Counters                map[string]Counter
}

// Installation has no finalizer: process crashes must leave kernel protection installed.
type Installation interface {
	Snapshot(context.Context) (KernelSnapshot, error)
	Reconcile(context.Context) error
	Remove(context.Context) error
}

// Backend atomically installs, adopts, or reconciles Irvio protection.
type Backend interface {
	Ensure(context.Context, Policy) (Installation, EnsureResult, error)
	Inspect(context.Context, Policy) (KernelSnapshot, error)
}

// State is the explicit logical ProtectedMode lifecycle.
type State uint8

const (
	StateVPNOff State = iota
	StateProtecting
	StateProtectedConnecting
	StateConnected
	StateSamePeerRecovery
	StateNetworkMigration
	StateNoExit
	StateUnprotecting
	StateClosed
)

func (s State) Protected() bool {
	return s >= StateProtectedConnecting && s <= StateUnprotecting
}

// Snapshot is safe to expose and contains no transport credentials.
type Snapshot struct {
	State      State
	Generation uint64
	Kernel     KernelSnapshot
	Adopted    bool
	UpdatedAt  time.Time
}

// ResourceBuilder creates generation-fenced tunnel resources after protection commits.
type ResourceBuilder func(context.Context) (io.Closer, error)

// Config constructs one lifecycle controller.
type Config struct {
	Backend Backend
	Policy  Policy
	Now     func() time.Time
}
