// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package tunnel

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/rwcancel"
	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/vishvananda/netlink"
)

type fakeDevice struct {
	file      *os.File
	closed    atomic.Bool
	events    chan awgtun.Event
	closeOnce sync.Once
}

type fakeLinkBackend struct {
	addressCalls int
	failAddress  int
	linkUp       bool
	deleted      int
}

func (*fakeLinkBackend) LinkByName(name string) (netlink.Link, error) {
	return &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: name, Index: 7}}, nil
}
func (*fakeLinkBackend) LinkSetMTU(netlink.Link, int) error { return nil }
func (f *fakeLinkBackend) AddrReplace(netlink.Link, *netlink.Addr) error {
	f.addressCalls++
	if f.addressCalls == f.failAddress {
		return errors.New("injected address installation failure")
	}
	return nil
}
func (f *fakeLinkBackend) AddrDel(netlink.Link, *netlink.Addr) error {
	f.deleted++
	return nil
}
func (f *fakeLinkBackend) LinkSetUp(netlink.Link) error {
	f.linkUp = true
	return nil
}

func TestNewLinuxPropagatesTUNCreationFailure(t *testing.T) {
	injected := errors.New("injected TUN creation failure")
	config := Config{Name: "hydra0", MTU: 1280, Addresses: []netip.Prefix{netip.MustParsePrefix("10.77.0.1/32")}}
	owner, err := newLinux(config, func(string, int) (awgtun.Device, error) {
		return nil, injected
	}, &fakeLinkBackend{})
	if owner != nil || !errors.Is(err, injected) {
		t.Fatalf("newLinux owner=%v error=%v", owner, err)
	}
}

func TestNewLinuxClosesTUNAfterPartialAddressFailure(t *testing.T) {
	device := &fakeDevice{events: make(chan awgtun.Event)}
	links := &fakeLinkBackend{failAddress: 2}
	config := Config{Name: "hydra0", MTU: 1280, Addresses: []netip.Prefix{
		netip.MustParsePrefix("10.77.0.1/32"), netip.MustParsePrefix("fd77::1/128"),
	}}
	owner, err := newLinux(config, func(string, int) (awgtun.Device, error) {
		return device, nil
	}, links)
	if owner != nil || err == nil {
		t.Fatalf("newLinux owner=%v error=%v", owner, err)
	}
	if links.addressCalls != 2 || links.linkUp {
		t.Fatalf("addressCalls=%d linkUp=%t", links.addressCalls, links.linkUp)
	}
	if !device.closed.Load() {
		t.Fatal("partial address setup left TUN device open")
	}
}

func (f *fakeDevice) File() *os.File                       { return f.file }
func (*fakeDevice) Read([][]byte, []int, int) (int, error) { return 0, os.ErrClosed }
func (*fakeDevice) Write([][]byte, int) (int, error)       { return 0, nil }
func (*fakeDevice) MTU() (int, error)                      { return 1280, nil }
func (*fakeDevice) Name() (string, error)                  { return "hydra0", nil }
func (f *fakeDevice) Events() <-chan awgtun.Event          { return f.events }
func (*fakeDevice) BatchSize() int                         { return 1 }
func (f *fakeDevice) Close() error {
	var result error
	f.closeOnce.Do(func() {
		f.closed.Store(true)
		close(f.events)
		if f.file != nil {
			result = f.file.Close()
		}
	})
	return result
}

func TestLinuxCloseDrainsSaturatedOwnerEvents(t *testing.T) {
	device := &fakeDevice{events: make(chan awgtun.Event, 1)}
	owner, err := newLinux(Config{
		Name: "hydra0", MTU: 1280,
		Addresses: []netip.Prefix{netip.MustParsePrefix("10.77.0.1/32")},
	}, func(string, int) (awgtun.Device, error) { return device, nil }, &fakeLinkBackend{})
	if err != nil {
		t.Fatal(err)
	}

	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		for range 32 {
			device.events <- awgtun.EventUp
		}
	}()
	select {
	case <-producerDone:
	case <-time.After(time.Second):
		t.Fatal("owner event producer blocked on an unconsumed channel")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-owner.eventsDone:
	default:
		t.Fatal("owner event drain did not terminate")
	}
}

func TestLinuxRepeatedCreateCloseDoesNotLeakEventDrains(t *testing.T) {
	baseline := runtime.NumGoroutine()
	for range 500 {
		device := &fakeDevice{events: make(chan awgtun.Event, 1)}
		owner, err := newLinux(Config{
			Name: "hydra0", MTU: 1280,
			Addresses: []netip.Prefix{netip.MustParsePrefix("10.77.0.1/32")},
		}, func(string, int) (awgtun.Device, error) { return device, nil }, &fakeLinkBackend{})
		if err != nil {
			t.Fatal(err)
		}
		device.events <- awgtun.EventUp
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
	}
	runtime.Gosched()
	if got := runtime.NumGoroutine(); got > baseline+2 {
		t.Fatalf("event-drain goroutines grew from %d to %d", baseline, got)
	}
}

// cancelRaceDevice models the pinned NativeTun listener closing RWCancel before Cancel writes.
type cancelRaceDevice struct {
	*fakeDevice
	leaveFileOpen     bool
	hideFile          bool
	readerClosedFirst bool
	returnedErr       error
}

func (d *cancelRaceDevice) File() *os.File {
	if d.hideFile {
		return nil
	}
	return d.file
}

func (d *cancelRaceDevice) Close() error {
	cancel, err := rwcancel.NewRWCancel(int(d.file.Fd()))
	if err != nil {
		return err
	}
	cancel.Close()
	err = cancel.Cancel()
	if d.readerClosedFirst {
		reader, writer, pipeErr := os.Pipe()
		if pipeErr != nil {
			return pipeErr
		}
		_ = reader.Close()
		_, err = writer.Write([]byte{0})
		_ = writer.Close()
	}
	close(d.events)
	if !d.leaveFileOpen {
		if closeErr := d.file.Close(); closeErr != nil {
			return errors.Join(err, closeErr)
		}
	}
	if d.returnedErr != nil {
		return d.returnedErr
	}
	return err
}

func TestLinuxCloseHandlesCompletedCancelPipeRace(t *testing.T) {
	for _, borrowedDevice := range []bool{false, true} {
		for _, kind := range []string{"completed", "file-still-open", "broken-pipe-completed", "broken-pipe-file-open", "missing-file", "other-error", "joined-error", "bare-closed", "read-closed"} {
			t.Run(fmt.Sprintf("borrowed=%t/%s", borrowedDevice, kind), func(t *testing.T) {
				file, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = file.Close(); _ = writer.Close() })
				injected := errors.New("unrelated close failure")
				device := &cancelRaceDevice{fakeDevice: &fakeDevice{file: file, events: make(chan awgtun.Event)},
					leaveFileOpen: kind == "file-still-open" || kind == "broken-pipe-file-open", hideFile: kind == "missing-file",
					readerClosedFirst: kind == "broken-pipe-completed" || kind == "broken-pipe-file-open"}
				if kind == "other-error" {
					device.returnedErr = injected
				}
				if kind == "joined-error" {
					device.returnedErr = errors.Join(&os.PathError{Op: "write", Path: "|1", Err: os.ErrClosed}, injected)
				}
				if kind == "bare-closed" {
					device.returnedErr = os.ErrClosed
				}
				if kind == "read-closed" {
					device.returnedErr = &os.PathError{Op: "read", Path: "|0", Err: os.ErrClosed}
				}
				var closeDevice func() error
				if borrowedDevice {
					owner := &Linux{}
					value := &borrowed{owner: owner, device: device}
					owner.borrower = value
					closeDevice = value.Close
				} else {
					owner := &Linux{device: device, eventsDone: drainDeviceEvents(device)}
					closeDevice = owner.Close
				}
				err = closeDevice()
				completed := kind == "completed" || kind == "broken-pipe-completed"
				if completed && err != nil {
					t.Fatalf("completed device close reported cancellation race: %v", err)
				}
				if !completed && err == nil {
					t.Fatal("unsafe or unrelated close error was suppressed")
				}
				if (kind == "other-error" || kind == "joined-error") && !errors.Is(err, injected) {
					t.Fatalf("lost unrelated close failure: %v", err)
				}
				if got := closeDevice(); (got == nil) != (err == nil) {
					t.Fatalf("Close is not idempotent: first=%v second=%v", err, got)
				}
			})
		}
	}
}

func TestNormalizeConfigRejectsUnsafeAddresses(t *testing.T) {
	for _, prefix := range []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/0"),
		netip.MustParsePrefix("::/0"),
		netip.MustParsePrefix("224.0.0.1/32"),
		netip.MustParsePrefix("::ffff:192.0.2.1/128"),
	} {
		if _, err := normalizeConfig(Config{Addresses: []netip.Prefix{prefix}}); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("normalizeConfig(%s) error = %v", prefix, err)
		}
	}
}

func TestBorrowedCloseDoesNotCloseOwnerDescriptor(t *testing.T) {
	ownerFile, duplicateSource, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer duplicateSource.Close()
	ownerDevice := &fakeDevice{file: ownerFile, events: make(chan awgtun.Event)}
	owner := &Linux{
		device: ownerDevice, name: "hydra0", ifindex: 7, mtu: 1280,
		addresses: []netip.Prefix{netip.MustParsePrefix("10.77.0.1/32")},
		links:     &fakeLinkBackend{}, dynamic: make(map[netip.Prefix]struct{}), factory: func(file *os.File, _ int) (awgtun.Device, error) {
			return &fakeDevice{file: file, events: make(chan awgtun.Event)}, nil
		},
	}
	first, err := owner.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Borrow(); !errors.Is(err, ErrAlreadyBorrowed) {
		t.Fatalf("second Borrow error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if ownerDevice.closed.Load() {
		t.Fatal("borrowed Close closed the owner TUN")
	}
	second, err := owner.Borrow()
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if !ownerDevice.closed.Load() {
		t.Fatal("owner Close left the kernel owner open")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Borrow(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Borrow after Close error = %v", err)
	}
}

func TestLinuxCloseRacesWithBorrow(t *testing.T) {
	type borrowResult struct {
		device awgtun.Device
		err    error
	}
	for cycle := range 500 {
		ownerFile, duplicateSource, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		ownerDevice := &fakeDevice{file: ownerFile, events: make(chan awgtun.Event)}
		owner := &Linux{
			device: ownerDevice, name: "hydra0", ifindex: 7, mtu: 1280,
			addresses: []netip.Prefix{netip.MustParsePrefix("10.77.0.1/32")},
			links:     &fakeLinkBackend{}, dynamic: make(map[netip.Prefix]struct{}), factory: func(file *os.File, _ int) (awgtun.Device, error) {
				return &fakeDevice{file: file, events: make(chan awgtun.Event)}, nil
			},
		}
		start := make(chan struct{})
		borrowed := make(chan borrowResult, 1)
		closed := make(chan error, 1)
		go func() {
			<-start
			device, err := owner.Borrow()
			borrowed <- borrowResult{device: device, err: err}
		}()
		go func() {
			<-start
			closed <- owner.Close()
		}()
		close(start)
		result := <-borrowed
		if result.err != nil && !errors.Is(result.err, ErrClosed) {
			t.Fatalf("cycle %d Borrow error = %v", cycle+1, result.err)
		}
		if err := <-closed; err != nil {
			t.Fatalf("cycle %d Close error = %v", cycle+1, err)
		}
		if result.device != nil {
			if err := result.device.Close(); err != nil {
				t.Fatalf("cycle %d borrowed Close error = %v", cycle+1, err)
			}
		}
		if err := owner.Close(); err != nil {
			t.Fatalf("cycle %d repeated owner Close error = %v", cycle+1, err)
		}
		if !ownerDevice.closed.Load() {
			t.Fatalf("cycle %d left owner device open", cycle+1)
		}
		if err := duplicateSource.Close(); err != nil {
			t.Fatalf("cycle %d close pipe source: %v", cycle+1, err)
		}
	}
}

func TestDynamicDualRoleAddressesAreOwnedAndRemoved(t *testing.T) {
	links := &fakeLinkBackend{}
	owner := &Linux{name: "hydra0", ifindex: 7, links: links, dynamic: make(map[netip.Prefix]struct{})}
	prefixes := []netip.Prefix{netip.MustParsePrefix("10.77.0.9/32"), netip.MustParsePrefix("fd77::9/128")}
	if err := owner.AddAddresses(prefixes); err != nil {
		t.Fatal(err)
	}
	if len(owner.dynamic) != 2 || links.addressCalls != 2 {
		t.Fatalf("dynamic=%d address calls=%d", len(owner.dynamic), links.addressCalls)
	}
	if err := owner.RemoveAddresses(prefixes); err != nil {
		t.Fatal(err)
	}
	if len(owner.dynamic) != 0 || links.deleted != 2 {
		t.Fatalf("dynamic=%d deleted=%d", len(owner.dynamic), links.deleted)
	}
}

func TestDynamicDualRoleAddressesRejectStaticOverlapAndNonHostPrefix(t *testing.T) {
	links := &fakeLinkBackend{}
	static := netip.MustParsePrefix("10.77.0.1/32")
	owner := &Linux{
		name: "hydra0", ifindex: 7, links: links,
		addresses: []netip.Prefix{static}, dynamic: make(map[netip.Prefix]struct{}),
	}
	for _, prefix := range []netip.Prefix{static, netip.MustParsePrefix("10.77.0.9/24"), netip.MustParsePrefix("fd77::9/64")} {
		if err := owner.AddAddresses([]netip.Prefix{prefix}); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("AddAddresses(%s) error=%v", prefix, err)
		}
	}
	if links.addressCalls != 0 || len(owner.dynamic) != 0 {
		t.Fatalf("rejected addresses mutated TUN: calls=%d dynamic=%v", links.addressCalls, owner.dynamic)
	}
}
