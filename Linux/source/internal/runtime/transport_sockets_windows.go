// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sync"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/transportgen"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

// WindowsMuxAuthorizer returns ownership even when a native permission may have committed before an error.
type WindowsMuxAuthorizer func(context.Context, networkstate.Generation, winroute.Binding, netip.AddrPort) (io.Closer, error)

// WindowsTransportSockets retains exclusive reservations until failed permission cleanup can be retried.
type WindowsTransportSockets struct {
	mu      sync.Mutex
	closing bool
	entries map[*windowsTransportSocket]struct{}
}

type windowsTransportSocket struct {
	mu     sync.Mutex
	parent *WindowsTransportSockets
	mux    *socketmux.Mux
	grants []io.Closer
	closed bool
}

func (o *WindowsTransportSockets) Factory(authorize WindowsMuxAuthorizer) (transportgen.MuxFactory, error) {
	if o == nil || authorize == nil {
		return nil, fmt.Errorf("Windows UDP socket owner and authorizer required")
	}
	return func(ctx context.Context, state networkstate.State, config socketmux.Config) (*socketmux.Mux, io.Closer, error) {
		if ctx == nil || state.Generation == 0 {
			return nil, nil, fmt.Errorf("Windows UDP generation required")
		}
		o.mu.Lock()
		if o.closing {
			o.mu.Unlock()
			return nil, nil, fmt.Errorf("Windows UDP socket owner closing")
		}
		if len(o.entries) >= 4 {
			o.mu.Unlock()
			return nil, nil, fmt.Errorf("Windows UDP generation capacity exhausted")
		}
		mux, err := socketmux.New(ctx, config)
		if err != nil {
			o.mu.Unlock()
			return nil, nil, err
		}
		entry := &windowsTransportSocket{parent: o, mux: mux}
		if o.entries == nil {
			o.entries = make(map[*windowsTransportSocket]struct{})
		}
		o.entries[entry] = struct{}{}
		for _, family := range []socketmux.AddressFamily{socketmux.IPv4, socketmux.IPv6} {
			local, ok := mux.LocalAddr(family)
			if !ok {
				continue
			}
			binding, bindErr := winroute.ForSource(local.Addr())
			if bindErr != nil {
				err = bindErr
				break
			}
			if !transportSocketMatchesSnapshot(state.Snapshot, binding) {
				err = fmt.Errorf("Windows UDP source/interface no longer matches generation %d", state.Generation)
				break
			}
			grant, grantErr := authorize(ctx, state.Generation, binding, local)
			if grant != nil {
				entry.grants = append(entry.grants, grant)
			}
			if grantErr == nil && grant == nil {
				grantErr = fmt.Errorf("Windows UDP permission owner missing")
			}
			if grantErr != nil {
				err = grantErr
				break
			}
		}
		if err == nil {
			err = ctx.Err()
		}
		o.mu.Unlock()
		if err != nil {
			return nil, nil, errors.Join(err, entry.Close())
		}
		return mux, entry, nil
	}, nil
}

func transportSocketMatchesSnapshot(snapshot networkstate.Snapshot, binding winroute.Binding) bool {
	for _, link := range snapshot.Links {
		if !link.Up || link.Index != int(binding.Index) || link.NativeID != binding.LUID {
			continue
		}
		for _, address := range link.Addresses {
			if address.Addr() == binding.Source {
				return true
			}
		}
	}
	return false
}

// Close is called after generation consumers stop; a failed revoke must not release its reserved UDP port.
func (e *windowsTransportSocket) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	var failure error
	for _, grant := range e.grants {
		failure = errors.Join(failure, grant.Close())
	}
	if failure != nil {
		return failure
	}
	if err := e.mux.Close(); err != nil {
		return err
	}
	e.closed = true
	e.parent.mu.Lock()
	delete(e.parent.entries, e)
	e.parent.mu.Unlock()
	return nil
}

// Close fences new generations and retries every retained socket without dropping uncertain ownership.
func (o *WindowsTransportSockets) Close() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	o.closing = true
	entries := make([]*windowsTransportSocket, 0, len(o.entries))
	for entry := range o.entries {
		entries = append(entries, entry)
	}
	o.mu.Unlock()
	var failure error
	for _, entry := range entries {
		failure = errors.Join(failure, entry.Close())
	}
	return failure
}
