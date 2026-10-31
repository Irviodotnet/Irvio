// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winroute

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

var testRouteID = windows.GUID{Data1: 17}
var errRouteInjected = errors.New("injected route failure")

type memoryRouteJournal struct {
	r                      routeRecord
	writes, failAt, closed int
	trace                  *[]string
}

func (j *memoryRouteJournal) Read() (routeRecord, error) { return j.r, nil }
func (j *memoryRouteJournal) Write(r routeRecord) error {
	j.writes++
	*j.trace = append(*j.trace, "journal")
	if j.writes == j.failAt {
		return errRouteInjected
	}
	j.r = r
	return nil
}
func (j *memoryRouteJournal) Close() error { j.closed++; return nil }

type memoryRouteBackend struct {
	routes                    map[netip.Prefix]bool
	trace                     *[]string
	adds, deletes, failAdd    int
	failDelete, gone, collide bool
	down                      bool
	afterAdd                  func()
}

func (b *memoryRouteBackend) Ready(luid uint64) error {
	if b.down {
		return errRouteInjected
	}
	return nil
}

func (b *memoryRouteBackend) Resolve(id windows.GUID) (uint64, error) {
	if id != testRouteID {
		return 0, ErrRouteConflict
	}
	if b.gone {
		return 0, ErrRouteInterfaceGone
	}
	return 777, nil
}
func (b *memoryRouteBackend) Lookup(luid uint64, p netip.Prefix) (bool, bool, error) {
	if luid != 777 {
		return false, false, ErrRouteConflict
	}
	exact, present := b.routes[p]
	return present, exact, nil
}
func (b *memoryRouteBackend) Add(luid uint64, p netip.Prefix) error {
	*b.trace = append(*b.trace, "add")
	b.adds++
	if b.adds == b.failAdd {
		if b.collide {
			b.routes[p] = true
		}
		return errRouteInjected
	}
	b.routes[p] = true
	if b.afterAdd != nil {
		b.afterAdd()
	}
	return nil
}
func (b *memoryRouteBackend) Delete(luid uint64, p netip.Prefix) error {
	*b.trace = append(*b.trace, "delete")
	b.deletes++
	if b.failDelete {
		return errRouteInjected
	}
	delete(b.routes, p)
	return nil
}
func fakeRoutes(t *testing.T) (*TunnelRoutes, *memoryRouteBackend, *memoryRouteJournal) {
	t.Helper()
	trace := []string{}
	b := &memoryRouteBackend{routes: map[netip.Prefix]bool{}, trace: &trace}
	j := &memoryRouteJournal{r: routeRecord{Version: 1}, trace: &trace}
	m, err := newTunnelRoutes(testRouteID, b, j)
	if err != nil {
		t.Fatal(err)
	}
	return m, b, j
}

func TestOwnedRouteLifecycle(t *testing.T) {
	m, b, j := fakeRoutes(t)
	ctx := context.Background()
	if err := m.Ensure(ctx, true, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*b.trace, []string{"journal", "add", "add", "add", "add"}) {
		t.Fatal(*b.trace)
	}
	if err := m.Ensure(ctx, true, true); err != nil || b.adds != 4 {
		t.Fatal("non-idempotent Ensure", err)
	}
	if err := m.Ensure(ctx, true, false); !errors.Is(err, ErrRouteConflict) {
		t.Fatal("unfenced family update", err)
	}
	if err := m.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if len(b.routes) != 0 || len(j.r.Prefixes) != 0 {
		t.Fatal("route residue")
	}
	if err := m.Ensure(ctx, true, false); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil || j.closed != 1 {
		t.Fatal("Close not idempotent")
	}
	if err := m.Ensure(ctx, true, true); err == nil {
		t.Fatal("closed owner reused")
	}
}

func TestOwnedRoutePreflightAndJournalFailure(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		m, b, j := fakeRoutes(t)
		if foreign {
			b.routes[splitPrefixes(true, false)[1]] = true
		} else {
			j.failAt = 1
		}
		if err := m.Ensure(context.Background(), true, true); err == nil {
			t.Fatal("failure ignored")
		}
		if b.adds != 0 || b.deletes != 0 {
			t.Fatal("mutated despite failed preflight/journal")
		}
	}
}

func TestOwnedRoutePartialRollback(t *testing.T) {
	for _, at := range []int{1, 2, 3, 4} {
		m, b, j := fakeRoutes(t)
		b.failAdd = at
		if err := m.Ensure(context.Background(), true, true); !errors.Is(err, errRouteInjected) {
			t.Fatal(err)
		}
		if len(b.routes) != 0 || len(j.r.Prefixes) != 0 || b.deletes != at-1 {
			t.Fatal("partial rollback failed", at)
		}
	}
}

func TestOwnedRouteRacePreservesFailedAdd(t *testing.T) {
	m, b, j := fakeRoutes(t)
	b.failAdd = 2
	b.collide = true
	j.failAt = 2
	if err := m.Ensure(context.Background(), true, false); err == nil {
		t.Fatal("failed write ignored")
	}
	if !m.dirty || len(b.routes) != 2 || b.deletes != 0 {
		t.Fatal("unsafe cleanup before durable ownership narrowing")
	}
	if err := m.Ensure(context.Background(), true, false); !errors.Is(err, ErrRouteConflict) {
		t.Fatal("dirty owner reused")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if len(b.routes) != 1 || !b.routes[splitPrefixes(true, false)[1]] || len(j.r.Prefixes) != 0 {
		t.Fatal("foreign winning add removed")
	}
}

func TestOwnedRouteExternalEditAndFailedCleanup(t *testing.T) {
	m, b, j := fakeRoutes(t)
	ctx := context.Background()
	if err := m.Ensure(ctx, true, true); err != nil {
		t.Fatal(err)
	}
	p := splitPrefixes(true, true)[1]
	b.routes[p] = false
	if err := m.Remove(ctx); !errors.Is(err, ErrRouteConflict) {
		t.Fatal(err)
	}
	if len(b.routes) != 1 || len(j.r.Prefixes) != 4 {
		t.Fatal("foreign edit overwritten or journal discarded")
	}
	delete(b.routes, p)
	j.failAt = j.writes + 1
	if err := m.Remove(ctx); err == nil || len(m.state.Prefixes) == 0 {
		t.Fatal("failed cleanup journal cleared")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if len(j.r.Prefixes) != 0 {
		t.Fatal("journal residue")
	}
}

func TestOwnedRouteCrashRecoveryAndAbsentGUID(t *testing.T) {
	for _, count := range []int{0, 1, 4} {
		m, b, j := fakeRoutes(t)
		prefixes := splitPrefixes(true, true)
		j.r = routeRecord{Version: 1, Interface: testRouteID, Prefixes: prefixes}
		for _, p := range prefixes[:count] {
			b.routes[p] = true
		}
		var err error
		m, err = newTunnelRoutes(testRouteID, b, j)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Remove(context.Background()); err != nil || len(b.routes) != 0 || len(j.r.Prefixes) != 0 {
			t.Fatal("crash recovery", count, err)
		}
	}
	m, b, j := fakeRoutes(t)
	if err := m.Ensure(context.Background(), true, true); err != nil {
		t.Fatal(err)
	}
	b.gone = true
	if err := m.Remove(context.Background()); err != nil || b.deletes != 0 || len(j.r.Prefixes) != 0 {
		t.Fatal("recycled interface targeted", err)
	}
}

func TestOwnedRouteRejectsBadJournalAndContext(t *testing.T) {
	m, b, j := fakeRoutes(t)
	for _, r := range []routeRecord{
		{Version: 2}, {Version: 1, Interface: testRouteID},
		{Version: 1, Prefixes: splitPrefixes(true, false)},
		{Version: 1, Interface: testRouteID, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}},
		{Version: 1, Interface: testRouteID, Prefixes: []netip.Prefix{netip.MustParsePrefix("::/1"), netip.MustParsePrefix("::/1")}},
	} {
		if err := validateRouteRecord(r); err == nil {
			t.Fatal("bad route journal accepted", r)
		}
	}
	j.r = routeRecord{Version: 1, Interface: windows.GUID{Data1: 99}, Prefixes: splitPrefixes(true, false)}
	if _, err := newTunnelRoutes(testRouteID, b, j); !errors.Is(err, ErrRouteConflict) {
		t.Fatal("identity substitution")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Ensure(ctx, true, true); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := m.Ensure(nil, true, true); err == nil {
		t.Fatal("nil context")
	}
	if err := m.Ensure(context.Background(), false, false); err == nil {
		t.Fatal("empty family set")
	}
	if b.adds != 0 {
		t.Fatal("invalid request mutated routes")
	}
}

func TestOwnedRouteCancellationAndDownInterface(t *testing.T) {
	for _, at := range []int{1, 4} {
		m, b, j := fakeRoutes(t)
		ctx, cancel := context.WithCancel(context.Background())
		b.afterAdd = func() {
			if b.adds == at {
				cancel()
			}
		}
		if err := m.Ensure(ctx, true, true); !errors.Is(err, context.Canceled) {
			t.Fatal("cancel ignored", err)
		}
		if len(b.routes) != 0 || len(j.r.Prefixes) != 0 {
			t.Fatal("cancellation residue")
		}
	}
	m, b, _ := fakeRoutes(t)
	b.down = true
	if err := m.Ensure(context.Background(), true, true); err == nil || b.adds != 0 {
		t.Fatal("down interface reported ready")
	}
}

func TestOwnedRouteCloseFailureRetainsRecovery(t *testing.T) {
	m, b, j := fakeRoutes(t)
	if err := m.Ensure(context.Background(), true, true); err != nil {
		t.Fatal(err)
	}
	b.failDelete = true
	if err := m.Close(); !errors.Is(err, errRouteInjected) {
		t.Fatal(err)
	}
	if err := m.Close(); !errors.Is(err, errRouteInjected) || j.closed != 1 {
		t.Fatal("lost Close failure")
	}
	if len(j.r.Prefixes) != 4 || len(b.routes) != 4 {
		t.Fatal("failed cleanup lost recovery state")
	}
}
