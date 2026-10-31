// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package transportgen

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"time"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

// Lifecycle is the one-way state of a transport generation.
type Lifecycle uint8

const (
	LifecyclePreparing Lifecycle = iota
	LifecycleReady
	LifecycleStale
	LifecycleClosing
	LifecycleClosed
)

// BindPlan selects at most one concrete address per family.
type BindPlan struct {
	IPv4 netip.Addr
	IPv6 netip.Addr
}

// BindPlanner maps an immutable network snapshot to concrete bind addresses.
type BindPlanner interface {
	Plan(networkstate.Snapshot) (BindPlan, error)
}

// BindPlannerFunc adapts a function to BindPlanner.
type BindPlannerFunc func(networkstate.Snapshot) (BindPlan, error)

// Plan invokes f.
func (f BindPlannerFunc) Plan(snapshot networkstate.Snapshot) (BindPlan, error) { return f(snapshot) }

// DataPlaneFactory borrows the mux for one generation and must never own it.
type DataPlaneFactory func(context.Context, *socketmux.Mux, networkstate.Generation) (io.Closer, error)

// Observation binds one M3b result to the exact transport that produced it.
type Observation struct {
	NetworkGeneration networkstate.Generation
	MuxPort           uint16
	Family            socketmux.AddressFamily
	LocalEndpoint     netip.AddrPort
	MappedEndpoint    netip.AddrPort
	ObservedAt        time.Time
}

var (
	ErrStale            = errors.New("transport generation is stale")
	ErrClosed           = errors.New("transport generation is closed")
	ErrInvalidPlan      = errors.New("invalid transport bind plan")
	ErrDataPlaneExists  = errors.New("transport generation already has a data plane")
	ErrInvalidDataPlane = errors.New("invalid transport generation data plane")
)
