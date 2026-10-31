// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winroute

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sync"

	"golang.org/x/sys/windows"
)

var (
	ErrRouteConflict      = errors.New("tunnel route state conflicts with its owner")
	ErrRouteInterfaceGone = errors.New("tunnel route interface no longer exists")
)

type routeRecord struct {
	Version   uint32
	Interface windows.GUID
	Prefixes  []netip.Prefix
}

type routeJournal interface {
	Read() (routeRecord, error)
	Write(routeRecord) error
	Close() error
}

type routeBackend interface {
	Resolve(windows.GUID) (uint64, error)
	Ready(uint64) error
	Lookup(uint64, netip.Prefix) (present, exact bool, err error)
	Add(uint64, netip.Prefix) error
	Delete(uint64, netip.Prefix) error
}

// TunnelRoutes owns split-default or pre-BIND host routes on a caller-owned Wintun; WFP protection is required.
type TunnelRoutes struct {
	mu       sync.Mutex
	id       windows.GUID
	back     routeBackend
	journal  routeJournal
	state    routeRecord
	dirty    bool
	closed   bool
	closeErr error
}

func OpenTunnelRoutes(luid uint64, directory string) (*TunnelRoutes, error) {
	id, err := routeGUID(luid)
	if err != nil {
		return nil, err
	}
	b := nativeRouteBackend{}
	if _, err := b.Resolve(id); err != nil {
		return nil, err
	}
	j, err := openRouteJournal(directory)
	if err != nil {
		return nil, err
	}
	m, err := newTunnelRoutes(id, b, j)
	if err != nil {
		return nil, errors.Join(err, j.Close())
	}
	return m, nil
}

func newTunnelRoutes(id windows.GUID, b routeBackend, j routeJournal) (*TunnelRoutes, error) {
	if id == (windows.GUID{}) || b == nil || j == nil {
		return nil, fmt.Errorf("invalid tunnel route owner")
	}
	r, err := j.Read()
	if err != nil {
		return nil, err
	}
	if err := validateRouteRecord(r); err != nil {
		return nil, err
	}
	if len(r.Prefixes) != 0 && r.Interface != id {
		return nil, ErrRouteConflict
	}
	return &TunnelRoutes{id: id, back: b, journal: j, state: r}, nil
}

func splitPrefixes(ipv4, ipv6 bool) []netip.Prefix {
	var out []netip.Prefix
	if ipv4 {
		out = append(out, netip.MustParsePrefix("0.0.0.0/1"), netip.MustParsePrefix("128.0.0.0/1"))
	}
	if ipv6 {
		out = append(out, netip.MustParsePrefix("::/1"), netip.MustParsePrefix("8000::/1"))
	}
	return out
}

func validateRouteRecord(r routeRecord) error {
	if (r.Version != 1 && r.Version != 2) || len(r.Prefixes) > 4 || (r.Version == 2 && (len(r.Prefixes) == 0 || len(r.Prefixes) > 2)) {
		return fmt.Errorf("invalid tunnel route journal")
	}
	if len(r.Prefixes) == 0 {
		if r.Interface != (windows.GUID{}) {
			return fmt.Errorf("inactive route journal has an interface")
		}
		return nil
	}
	if r.Interface == (windows.GUID{}) {
		return fmt.Errorf("route journal identity missing")
	}
	allowed := splitPrefixes(true, true)
	seen := map[netip.Prefix]bool{}
	for _, p := range r.Prefixes {
		valid := slices.Contains(allowed, p)
		if r.Version == 2 {
			valid = p.IsValid() && p.Bits() == p.Addr().BitLen() && p.Addr().IsGlobalUnicast() && !p.Addr().Is4In6()
		}
		if !valid || seen[p] {
			return fmt.Errorf("invalid or duplicate journal route")
		}
		seen[p] = true
	}
	return nil
}

// Ensure journals before native mutation; callers keep WFP installed throughout partial application and rollback.
func (m *TunnelRoutes) Ensure(ctx context.Context, ipv4, ipv6 bool) error {
	if ctx == nil {
		return fmt.Errorf("nil route context")
	}
	desired := splitPrefixes(ipv4, ipv6)
	if len(desired) == 0 {
		return fmt.Errorf("at least one tunnel route family required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return routeOperation(m.back, func() error { return m.ensure(ctx, desired, 1) })
}

// EnsureControl installs only exact authenticated control hosts; callers separately restrict TCP through WFP.
func (m *TunnelRoutes) EnsureControl(ctx context.Context, addresses []netip.Addr) error {
	if ctx == nil || len(addresses) == 0 || len(addresses) > 2 {
		return fmt.Errorf("invalid tunnel control routes")
	}
	desired := make([]netip.Prefix, 0, len(addresses))
	for _, a := range addresses {
		if a.Zone() != "" {
			return fmt.Errorf("zoned tunnel control address")
		}
		desired = append(desired, netip.PrefixFrom(a, a.BitLen()))
	}
	if err := validateRouteRecord(routeRecord{Version: 2, Interface: m.id, Prefixes: desired}); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return routeOperation(m.back, func() error { return m.ensure(ctx, desired, 2) })
}

func (m *TunnelRoutes) ensure(ctx context.Context, desired []netip.Prefix, version uint32) error {
	if m.closed {
		return fmt.Errorf("tunnel routes closed")
	}
	if m.dirty {
		return ErrRouteConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	luid, err := m.back.Resolve(m.id)
	if err != nil {
		return err
	}
	if err := m.back.Ready(luid); err != nil {
		return err
	}
	if len(m.state.Prefixes) != 0 {
		if m.state.Version != version || !slices.Equal(m.state.Prefixes, desired) {
			return ErrRouteConflict
		}
		for _, p := range desired {
			present, exact, err := m.back.Lookup(luid, p)
			if err != nil {
				return err
			}
			if !present || !exact {
				return ErrRouteConflict
			}
		}
		return m.back.Ready(luid)
	}
	for _, p := range desired {
		present, _, err := m.back.Lookup(luid, p)
		if err != nil {
			return err
		}
		if present {
			return ErrRouteConflict
		}
	}
	next := routeRecord{Version: version, Interface: m.id, Prefixes: desired}
	if err := m.journal.Write(next); err != nil {
		return err
	}
	m.state = next
	for i, p := range desired {
		if err = ctx.Err(); err == nil {
			err = m.back.Add(luid, p)
		}
		if err != nil {
			// Failed/unattempted additions are not owned, even if a competing native add won after preflight.
			owned := routeRecord{Version: 1}
			if i != 0 {
				owned.Version = version
				owned.Interface = m.id
				owned.Prefixes = append([]netip.Prefix(nil), desired[:i]...)
			}
			m.state = owned
			m.dirty = true
			if writeErr := m.journal.Write(owned); writeErr != nil {
				return errors.Join(err, writeErr)
			}
			m.dirty = false
			return errors.Join(err, m.remove(context.Background()))
		}
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, m.remove(context.Background()))
	}
	for _, p := range desired {
		present, exact, err := m.back.Lookup(luid, p)
		if err != nil {
			return err
		}
		if !present || !exact {
			return ErrRouteConflict
		}
	}
	return m.back.Ready(luid)
}

func (m *TunnelRoutes) Remove(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("nil route context")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("tunnel routes closed")
	}
	return routeOperation(m.back, func() error { return m.remove(ctx) })
}

func (m *TunnelRoutes) remove(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.dirty {
		if err := m.journal.Write(m.state); err != nil {
			return err
		}
		m.dirty = false
	}
	if len(m.state.Prefixes) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	luid, err := m.back.Resolve(m.id)
	if err != nil && !errors.Is(err, ErrRouteInterfaceGone) {
		return err
	}
	if err == nil {
		var result error
		for i := len(m.state.Prefixes) - 1; i >= 0; i-- {
			p := m.state.Prefixes[i]
			if err := ctx.Err(); err != nil {
				return errors.Join(result, err)
			}
			present, exact, err := m.back.Lookup(luid, p)
			if err != nil {
				result = errors.Join(result, err)
				continue
			}
			if !present {
				continue
			}
			if !exact {
				result = errors.Join(result, ErrRouteConflict)
				continue
			}
			if err := m.back.Delete(luid, p); err != nil {
				result = errors.Join(result, err)
				continue
			}
			present, _, err = m.back.Lookup(luid, p)
			if err != nil {
				result = errors.Join(result, err)
			} else if present {
				result = errors.Join(result, ErrRouteConflict)
			}
		}
		if result != nil {
			return result
		}
	}
	clean := routeRecord{Version: 1}
	if err := m.journal.Write(clean); err != nil {
		return err
	}
	m.state = clean
	return nil
}

func (m *TunnelRoutes) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return m.closeErr
	}
	m.closeErr = errors.Join(routeOperation(m.back, func() error { return m.remove(context.Background()) }), m.journal.Close())
	m.closed = true
	return m.closeErr
}

// RecoverTunnelRoutes cleans the recorded GUID only; it never guesses ownership from a route prefix alone.
func RecoverTunnelRoutes(ctx context.Context, directory string) (result error) {
	if ctx == nil {
		return fmt.Errorf("nil route context")
	}
	j, err := openRouteJournal(directory)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, j.Close()) }()
	r, err := j.Read()
	if err != nil {
		return err
	}
	if len(r.Prefixes) == 0 {
		return nil
	}
	m := TunnelRoutes{id: r.Interface, back: nativeRouteBackend{}, journal: j, state: r}
	return routeOperation(m.back, func() error { return m.remove(ctx) })
}
