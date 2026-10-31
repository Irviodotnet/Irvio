// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import (
	"context"
	"fmt"
	"net/netip"
)

// Endpoint is a logical view over a mux-owned physical socket pair.
type Endpoint struct {
	mux     *Mux
	receive <-chan Datagram
}

// Read waits for the next datagram classified for this endpoint.
func (e *Endpoint) Read(ctx context.Context) (Datagram, error) {
	if ctx == nil {
		return Datagram{}, fmt.Errorf("read datagram: nil context")
	}
	if err := ctx.Err(); err != nil {
		return Datagram{}, err
	}
	if e.mux.closed.Load() {
		return Datagram{}, ErrClosed
	}
	select {
	case <-ctx.Done():
		return Datagram{}, ctx.Err()
	case <-e.mux.done:
		return Datagram{}, ErrClosed
	case datagram, ok := <-e.receive:
		if !ok || e.mux.closed.Load() {
			return Datagram{}, ErrClosed
		}
		return datagram, nil
	}
}

// WriteTo sends one datagram through the mux-owned socket selected by destination family.
func (e *Endpoint) WriteTo(ctx context.Context, payload []byte, destination netip.AddrPort) (int, error) {
	if ctx == nil {
		return 0, fmt.Errorf("write datagram: nil context")
	}
	return e.mux.writeTo(ctx, payload, destination)
}

// Port returns the shared physical UDP port owned by the parent mux.
func (e *Endpoint) Port() uint16 { return e.mux.Port() }

// LocalAddr returns the parent mux local endpoint for an address family.
func (e *Endpoint) LocalAddr(family AddressFamily) (netip.AddrPort, bool) {
	return e.mux.LocalAddr(family)
}
