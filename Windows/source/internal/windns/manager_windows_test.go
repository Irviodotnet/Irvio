// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package windns

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

type fakeJournal struct {
	r      record
	fail   bool
	closed bool
}

func (j *fakeJournal) Read() (record, error) { return j.r, nil }
func (j *fakeJournal) Write(r record) error {
	if j.fail {
		return errors.New("journal failure")
	}
	j.r = r
	return nil
}
func (j *fakeJournal) Close() error { j.closed = true; return nil }

type fakeBackend struct {
	s      Snapshot
	j      *fakeJournal
	sets   int
	failAt int
	absent bool
	t      *testing.T
}

func (b *fakeBackend) Read(id windows.GUID) (Snapshot, error) {
	if b.absent {
		return Snapshot{}, ErrInterfaceGone
	}
	return b.s, nil
}
func (b *fakeBackend) SetServers(id windows.GUID, v6 bool, value string) error {
	if !b.j.r.Active {
		b.t.Fatal("write without durable original")
	}
	b.sets++
	if b.sets == b.failAt {
		return errors.New("native write failed")
	}
	if v6 {
		b.s.IPv6.Servers = value
	} else {
		b.s.IPv4.Servers = value
	}
	return nil
}
func fixture(t *testing.T) (*Manager, *fakeBackend, *fakeJournal) {
	t.Helper()
	j := &fakeJournal{r: record{Version: 1}}
	b := &fakeBackend{t: t, j: j, s: Snapshot{Interface: windows.GUID{Data1: 123}, IPv4: Settings{Servers: "192.0.2.53", Domain: "example.invalid", SearchList: "one.invalid,two.invalid", RegistrationEnabled: 1}, IPv6: Settings{Servers: "2001:db8::53"}}}
	m, err := newManager(b.s.Interface, b, j)
	if err != nil {
		t.Fatal(err)
	}
	return m, b, j
}
func servers() []netip.Addr {
	return []netip.Addr{netip.MustParseAddr("10.253.245.53"), netip.MustParseAddr("fd77:245::53")}
}

func TestSDKLayout(t *testing.T) {
	n := nativeSettings{}
	if unsafe.Sizeof(n) != 64 || unsafe.Offsetof(n.Flags) != 8 || unsafe.Offsetof(n.Domain) != 16 || unsafe.Offsetof(n.NameServer) != 24 || unsafe.Offsetof(n.SearchList) != 32 || unsafe.Offsetof(n.RegistrationEnabled) != 40 || unsafe.Offsetof(n.ProfileNameServer) != 56 {
		t.Fatal("DNS ABI differs from SDK x64")
	}
	v3 := nativeSettings3{}
	if unsafe.Sizeof(v3) != 112 || unsafe.Offsetof(v3.SupplementalSearchList) != 72 || unsafe.Offsetof(v3.ServerPropertyCount) != 80 || unsafe.Offsetof(v3.ServerProperties) != 88 || unsafe.Offsetof(v3.ProfilePropertyCount) != 96 || unsafe.Offsetof(v3.ProfileProperties) != 104 {
		t.Fatal("DNS v3 ABI differs from SDK x64")
	}
}

func TestDNSRejectsUnownedEncryptedSettings(t *testing.T) {
	m, b, _ := fixture(t)
	b.s.IPv4.EncryptedServerProperties = true
	if m.Apply(context.Background(), servers()) == nil || b.sets != 0 {
		t.Fatal("encrypted interface settings overwritten")
	}
}

func TestDNSOwnershipLifecycle(t *testing.T) {
	m, b, j := fixture(t)
	baseline := b.s
	ctx := context.Background()
	if err := m.Apply(ctx, servers()); err != nil {
		t.Fatal(err)
	}
	if b.s.IPv4.Domain != baseline.IPv4.Domain || b.s.IPv4.SearchList != baseline.IPv4.SearchList {
		t.Fatal("suffix changed")
	}
	if err := m.Apply(ctx, []netip.Addr{netip.MustParseAddr("10.253.245.54")}); err != nil {
		t.Fatal(err)
	}
	if j.r.Original != baseline {
		t.Fatal("baseline replaced on update")
	}
	if err := m.Restore(ctx); err != nil || b.s != baseline || j.r.Active {
		t.Fatalf("restore %v %+v", err, b.s)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if !j.closed {
		t.Fatal("journal not closed")
	}
	if m.Apply(ctx, servers()) == nil {
		t.Fatal("closed owner accepted")
	}
}

func TestDNSJournalAndPartialFailure(t *testing.T) {
	m, b, j := fixture(t)
	baseline := b.s
	j.fail = true
	if m.Apply(context.Background(), servers()) == nil || b.sets != 0 {
		t.Fatal("mutation without journal")
	}
	j.fail = false
	b.failAt = 2
	if m.Apply(context.Background(), servers()) == nil || b.s != baseline || j.r.Active {
		t.Fatal("partial apply not rolled back")
	}
}

func TestDNSPreservesExternalChanges(t *testing.T) {
	m, b, j := fixture(t)
	baseline := b.s
	if err := m.Apply(context.Background(), servers()); err != nil {
		t.Fatal(err)
	}
	b.s.IPv4.Servers = "192.0.2.99"
	b.s.IPv6.SearchList = "external.invalid"
	if !errors.Is(m.Restore(context.Background()), ErrConflict) {
		t.Fatal("external change not reported")
	}
	if b.s.IPv4.Servers != "192.0.2.99" || b.s.IPv6.Servers != baseline.IPv6.Servers || b.s.IPv6.SearchList != "external.invalid" || !j.r.Active {
		t.Fatal("external state overwritten or journal lost")
	}
	if !errors.Is(m.Close(), ErrConflict) || !errors.Is(m.Close(), ErrConflict) {
		t.Fatal("cleanup error not retained")
	}
}

func TestDNSRecoveryPreparedAndHalfApplied(t *testing.T) {
	for _, applied := range []int{0, 1, 2} {
		m, b, j := fixture(t)
		baseline := b.s
		desired, _ := desiredServers(servers())
		j.r = record{Version: 1, Active: true, Original: baseline, Previous: serverLists(baseline), Desired: desired}
		if applied > 0 {
			b.s.IPv4.Servers = desired[0]
		}
		if applied > 1 {
			b.s.IPv6.Servers = desired[1]
		}
		recovered, err := newManager(m.id, b, j)
		if err != nil {
			t.Fatal(err)
		}
		if err := recovered.Restore(context.Background()); err != nil || b.s != baseline {
			t.Fatalf("phase %d: %v", applied, err)
		}
	}
}

func TestDNSIdentityAbsenceAndValidation(t *testing.T) {
	m, b, j := fixture(t)
	if err := m.Apply(context.Background(), servers()); err != nil {
		t.Fatal(err)
	}
	if _, err := newManager(windows.GUID{Data1: 456}, b, j); !errors.Is(err, ErrConflict) {
		t.Fatal("identity reuse accepted")
	}
	b.absent = true
	if err := m.Restore(context.Background()); err != nil || j.r.Active {
		t.Fatal("missing interface recovery failed")
	}
	for _, bad := range [][]netip.Addr{nil, {netip.MustParseAddr("0.0.0.0")}, {netip.MustParseAddr("127.0.0.1")}, {netip.MustParseAddr("fe80::1%8")}} {
		if _, err := desiredServers(bad); err == nil {
			t.Fatal("invalid DNS target accepted")
		}
	}
	if _, err := normalizeServers("2001:db8::1", false); err == nil {
		t.Fatal("wrong family not rejected")
	}
	if _, err := normalizeServers("192.0.2.1", true); err == nil {
		t.Fatal("wrong family not rejected")
	}
}

func TestDNSExternalSuffixPreservedOnSuccess(t *testing.T) {
	m, b, _ := fixture(t)
	if err := m.Apply(context.Background(), servers()); err != nil {
		t.Fatal(err)
	}
	b.s.IPv4.Domain = "external.invalid"
	b.s.IPv6.SearchList = "search.external.invalid"
	if err := m.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.s.IPv4.Domain != "external.invalid" || b.s.IPv6.SearchList != "search.external.invalid" {
		t.Fatal("unowned settings restored")
	}
}
