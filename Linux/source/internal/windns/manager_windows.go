// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package windns

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

var ErrConflict = errors.New("Windows DNS settings changed outside this owner")

type record struct {
	Version           uint32
	Active            bool
	Original          Snapshot
	Previous, Desired [2]string
}

type journal interface {
	Read() (record, error)
	Write(record) error
	Close() error
}

// Manager mutates only server lists on its explicit interface; suffix/profile and other interfaces remain untouched.
type Manager struct {
	mu       sync.Mutex
	id       windows.GUID
	back     backend
	journal  journal
	state    record
	closed   bool
	closeErr error
}

func Open(luid uint64, directory string) (*Manager, error) {
	id, err := interfaceGUID(luid)
	if err != nil {
		return nil, err
	}
	j, err := openJournal(directory)
	if err != nil {
		return nil, err
	}
	m, err := newManager(id, nativeBackend{}, j)
	if err != nil {
		return nil, errors.Join(err, j.Close())
	}
	return m, nil
}

func newManager(id windows.GUID, b backend, j journal) (*Manager, error) {
	if id == (windows.GUID{}) || b == nil || j == nil {
		return nil, fmt.Errorf("invalid DNS manager")
	}
	r, err := j.Read()
	if err != nil {
		return nil, err
	}
	if err := validateRecord(r); err != nil {
		return nil, err
	}
	if r.Active && r.Original.Interface != id {
		return nil, ErrConflict
	}
	return &Manager{id: id, back: b, journal: j, state: r}, nil
}

func validateRecord(r record) error {
	if r.Version != 1 {
		return fmt.Errorf("unsupported DNS recovery version")
	}
	if !r.Active {
		if r != (record{Version: 1}) {
			return fmt.Errorf("invalid inactive DNS recovery record")
		}
		return nil
	}
	if r.Original.Interface == (windows.GUID{}) {
		return fmt.Errorf("DNS recovery identity missing")
	}
	for i, s := range []Settings{r.Original.IPv4, r.Original.IPv6} {
		if s.ProfileServers != "" || s.EncryptedServerProperties {
			return fmt.Errorf("unowned advanced DNS in recovery record")
		}
		for _, v := range []string{s.Servers, r.Previous[i], r.Desired[i]} {
			n, err := normalizeServers(v, i == 1)
			if err != nil || n != v {
				return fmt.Errorf("invalid DNS recovery server list")
			}
		}
	}
	return nil
}

func desiredServers(servers []netip.Addr) ([2]string, error) {
	var out [2]string
	if len(servers) == 0 || len(servers) > 8 {
		return out, fmt.Errorf("one to eight tunnel DNS servers required")
	}
	var grouped [2][]string
	seen := map[netip.Addr]bool{}
	for _, a := range servers {
		if !a.IsGlobalUnicast() || a.Is4In6() || a.Zone() != "" {
			return out, fmt.Errorf("invalid tunnel DNS server")
		}
		if seen[a] {
			continue
		}
		seen[a] = true
		family := 0
		if a.Is6() {
			family = 1
		}
		grouped[family] = append(grouped[family], a.String())
	}
	for i := range out {
		out[i] = strings.Join(grouped[i], ",")
	}
	return out, nil
}

func serverLists(s Snapshot) [2]string { return [2]string{s.IPv4.Servers, s.IPv6.Servers} }

// Apply journals before mutation and rejects unrelated changes rather than silently overriding them.
func (m *Manager) Apply(ctx context.Context, servers []netip.Addr) error {
	if ctx == nil {
		return fmt.Errorf("nil DNS context")
	}
	desired, err := desiredServers(servers)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("DNS manager closed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := m.back.Read(m.id)
	if err != nil {
		return err
	}
	if current.Interface != m.id {
		return ErrConflict
	}
	if current.IPv4.ProfileServers != "" || current.IPv6.ProfileServers != "" || current.IPv4.EncryptedServerProperties || current.IPv6.EncryptedServerProperties {
		return fmt.Errorf("profile or encrypted interface DNS overrides require explicit resolution")
	}
	original := current
	if m.state.Active {
		if serverLists(current) != m.state.Desired {
			return ErrConflict
		}
		original = m.state.Original
	}
	next := record{Version: 1, Active: true, Original: original, Previous: serverLists(current), Desired: desired}
	if err := m.journal.Write(next); err != nil {
		return err
	}
	m.state = next
	for i, value := range desired {
		if err = ctx.Err(); err == nil {
			err = m.back.SetServers(m.id, i == 1, value)
		}
		if err != nil {
			return errors.Join(err, m.restore(context.Background()))
		}
	}
	after, err := m.back.Read(m.id)
	if err != nil {
		return err
	}
	if after.Interface != m.id || serverLists(after) != desired {
		return ErrConflict
	}
	return nil
}

func (m *Manager) Restore(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("nil DNS context")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("DNS manager closed")
	}
	return m.restore(ctx)
}

func (m *Manager) restore(ctx context.Context) error {
	if !m.state.Active {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := m.back.Read(m.id)
	if errors.Is(err, ErrInterfaceGone) {
		clean := record{Version: 1}
		if err := m.journal.Write(clean); err != nil {
			return err
		}
		m.state = clean
		return nil
	}
	if err != nil {
		return err
	}
	if current.Interface != m.id {
		return ErrConflict
	}
	original := serverLists(m.state.Original)
	var result error
	for i, value := range serverLists(current) {
		if value == original[i] {
			continue
		}
		settings := current.IPv4
		if i == 1 {
			settings = current.IPv6
		}
		if settings.EncryptedServerProperties || settings.ProfileServers != "" {
			result = errors.Join(result, ErrConflict)
			continue
		}
		if value != m.state.Previous[i] && value != m.state.Desired[i] {
			result = errors.Join(result, ErrConflict)
			continue
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(result, err)
		}
		if err := m.back.SetServers(m.id, i == 1, original[i]); err != nil {
			result = errors.Join(result, err)
		}
	}
	if result != nil {
		return result
	}
	after, err := m.back.Read(m.id)
	if err != nil {
		return err
	}
	if after.Interface != m.id || serverLists(after) != original {
		return ErrConflict
	}
	clean := record{Version: 1}
	if err := m.journal.Write(clean); err != nil {
		return err
	}
	m.state = clean
	return nil
}

// Close releases ownership even on failed cleanup; the durable journal remains available to recovery.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return m.closeErr
	}
	err := m.restore(context.Background())
	m.closed = true
	m.closeErr = errors.Join(err, m.journal.Close())
	return m.closeErr
}

// Recover replays only the recorded GUID, never a potentially recycled interface index or LUID.
func Recover(ctx context.Context, directory string) (resultErr error) {
	if ctx == nil {
		return fmt.Errorf("nil DNS recovery context")
	}
	j, err := openJournal(directory)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, j.Close()) }()
	r, err := j.Read()
	if err != nil {
		return err
	}
	if !r.Active {
		return nil
	}
	m := Manager{id: r.Original.Interface, back: nativeBackend{}, journal: j, state: r}
	return m.restore(ctx)
}
