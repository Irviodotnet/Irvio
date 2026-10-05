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
	"net"
	"net/netip"
	"os"
	"slices"
	"sync"
	"time"

	awgtun "github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type borrowFactory func(*os.File, int) (awgtun.Device, error)
type createFactory func(string, int) (awgtun.Device, error)

const ownerEventDrainTimeout = 5 * time.Second

type linkBackend interface {
	LinkByName(string) (netlink.Link, error)
	LinkSetMTU(netlink.Link, int) error
	AddrReplace(netlink.Link, *netlink.Addr) error
	AddrDel(netlink.Link, *netlink.Addr) error
	LinkSetUp(netlink.Link) error
}

type netlinkBackend struct{}

func (netlinkBackend) LinkByName(name string) (netlink.Link, error) {
	return netlink.LinkByName(name)
}
func (netlinkBackend) LinkSetMTU(link netlink.Link, mtu int) error {
	return netlink.LinkSetMTU(link, mtu)
}
func (netlinkBackend) AddrReplace(link netlink.Link, address *netlink.Addr) error {
	return netlink.AddrReplace(link, address)
}
func (netlinkBackend) AddrDel(link netlink.Link, address *netlink.Addr) error {
	return netlink.AddrDel(link, address)
}
func (netlinkBackend) LinkSetUp(link netlink.Link) error { return netlink.LinkSetUp(link) }

// Linux keeps the TUN owner descriptor alive while AWG closes only borrowed duplicates.
type Linux struct {
	device     awgtun.Device
	eventsDone <-chan struct{}
	name       string
	ifindex    int
	mtu        int
	addresses  []netip.Prefix
	borrower   *borrowed
	factory    borrowFactory
	links      linkBackend
	dynamic    map[netip.Prefix]struct{}

	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

var _ DeviceOwner = (*Linux)(nil)

// NewLinux configures the pinned native TUN through netlink, never shell commands.
func NewLinux(config Config) (*Linux, error) {
	return newLinux(config, awgtun.CreateTUN, netlinkBackend{})
}

func newLinux(config Config, create createFactory, links linkBackend) (*Linux, error) {
	config, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	device, err := create(config.Name, config.MTU)
	if err != nil {
		return nil, fmt.Errorf("create Linux TUN %s: %w", config.Name, err)
	}
	owner := &Linux{
		device: device, name: config.Name, mtu: config.MTU,
		addresses: append([]netip.Prefix(nil), config.Addresses...),
		factory:   awgtun.CreateTUNFromFile, links: links, dynamic: make(map[netip.Prefix]struct{}),
	}
	owner.eventsDone = drainDeviceEvents(device)
	if err := owner.configure(links); err != nil {
		_ = owner.Close()
		return nil, err
	}
	return owner, nil
}

func normalizeConfig(config Config) (Config, error) {
	if config.Name == "" {
		config.Name = DefaultName
	}
	if config.MTU == 0 {
		config.MTU = DefaultMTU
	}
	if len(config.Name) > 15 || config.MTU < 576 || config.MTU > 65535 || len(config.Addresses) == 0 || len(config.Addresses) > 4 {
		return Config{}, ErrInvalidConfig
	}
	seen := make(map[string]struct{}, len(config.Addresses))
	for index, prefix := range config.Addresses {
		if !prefix.IsValid() || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() || prefix.Addr().Is4In6() {
			return Config{}, ErrInvalidConfig
		}
		config.Addresses[index] = prefix
		if _, exists := seen[prefix.String()]; exists {
			return Config{}, ErrInvalidConfig
		}
		seen[prefix.String()] = struct{}{}
	}
	return config, nil
}

func (l *Linux) configure(links linkBackend) error {
	link, err := links.LinkByName(l.name)
	if err != nil {
		return fmt.Errorf("find Linux TUN %s: %w", l.name, err)
	}
	l.ifindex = link.Attrs().Index
	if err := links.LinkSetMTU(link, l.mtu); err != nil {
		return fmt.Errorf("set Linux TUN %s MTU: %w", l.name, err)
	}
	for _, prefix := range l.addresses {
		bits := 128
		if prefix.Addr().Is4() {
			bits = 32
		}
		address := &netlink.Addr{IPNet: &net.IPNet{
			IP: net.IP(prefix.Addr().AsSlice()), Mask: net.CIDRMask(prefix.Bits(), bits),
		}}
		if err := links.AddrReplace(link, address); err != nil {
			return fmt.Errorf("add %s to Linux TUN %s: %w", prefix, l.name, err)
		}
	}
	if err := links.LinkSetUp(link); err != nil {
		return fmt.Errorf("bring Linux TUN %s up: %w", l.name, err)
	}
	return nil
}

// Borrow permits one generation-local reader so AWG devices cannot race for TUN packets.
func (l *Linux) Borrow() (awgtun.Device, error) {
	if l == nil {
		return nil, ErrClosed
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, ErrClosed
	}
	if l.borrower != nil {
		return nil, ErrAlreadyBorrowed
	}
	file := l.device.File()
	if file == nil {
		return nil, fmt.Errorf("borrow Linux TUN: missing owner descriptor: %w", ErrInvalidConfig)
	}
	fd, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("duplicate Linux TUN descriptor: %w", err)
	}
	duplicate := os.NewFile(uintptr(fd), l.name+"-awg")
	device, err := l.factory(duplicate, l.mtu)
	if err != nil {
		_ = duplicate.Close()
		return nil, fmt.Errorf("create borrowed Linux TUN: %w", err)
	}
	value := &borrowed{owner: l, device: device}
	l.borrower = value
	return value, nil
}

func (l *Linux) release(value *borrowed) {
	l.mu.Lock()
	if l.borrower == value {
		l.borrower = nil
	}
	l.mu.Unlock()
}

func (l *Linux) Name() string {
	if l == nil {
		return ""
	}
	return l.name
}

func (l *Linux) IfIndex() int {
	if l == nil {
		return 0
	}
	return l.ifindex
}

func (l *Linux) Addresses() []netip.Prefix {
	if l == nil {
		return nil
	}
	return append([]netip.Prefix(nil), l.addresses...)
}

// AddAddresses supports dual-role leases without transferring TUN ownership.
func (l *Linux) AddAddresses(prefixes []netip.Prefix) error {
	if l == nil || len(prefixes) == 0 {
		return ErrInvalidConfig
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrClosed
	}
	link, err := l.links.LinkByName(l.name)
	if err != nil {
		return err
	}
	var added []netip.Prefix
	for _, prefix := range prefixes {
		if !validDynamicPrefix(prefix) {
			err = ErrInvalidConfig
			break
		}
		prefix = prefix.Masked()
		if slices.Contains(l.addresses, prefix) {
			err = ErrInvalidConfig
			break
		}
		if _, exists := l.dynamic[prefix]; exists {
			continue
		}
		address := prefixAddress(prefix)
		if err = l.links.AddrReplace(link, &address); err != nil {
			break
		}
		l.dynamic[prefix] = struct{}{}
		added = append(added, prefix)
	}
	if err == nil {
		return nil
	}
	for index := len(added) - 1; index >= 0; index-- {
		address := prefixAddress(added[index])
		_ = l.links.AddrDel(link, &address)
		delete(l.dynamic, added[index])
	}
	return err
}

func (l *Linux) RemoveAddresses(prefixes []netip.Prefix) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	link, err := l.links.LinkByName(l.name)
	if err != nil {
		return err
	}
	var result error
	for _, prefix := range prefixes {
		prefix = prefix.Masked()
		if _, exists := l.dynamic[prefix]; !exists {
			continue
		}
		address := prefixAddress(prefix)
		if err := l.links.AddrDel(link, &address); err != nil && !errors.Is(err, unix.EADDRNOTAVAIL) {
			result = errors.Join(result, err)
			continue
		}
		delete(l.dynamic, prefix)
	}
	return result
}

func validDynamicPrefix(prefix netip.Prefix) bool {
	return prefix.IsValid() && prefix.Bits() == prefix.Addr().BitLen() &&
		!prefix.Addr().IsUnspecified() && !prefix.Addr().IsMulticast() && !prefix.Addr().Is4In6()
}

func prefixAddress(prefix netip.Prefix) netlink.Addr {
	bits := 128
	if prefix.Addr().Is4() {
		bits = 32
	}
	return netlink.Addr{IPNet: &net.IPNet{IP: net.IP(prefix.Addr().AsSlice()), Mask: net.CIDRMask(prefix.Bits(), bits)}}
}

// Close releases the borrower before the owner descriptor, deleting the TUN last.
func (l *Linux) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		borrower := l.borrower
		l.mu.Unlock()
		var borrowerErr error
		if borrower != nil {
			borrowerErr = borrower.Close()
		}
		l.closeErr = errors.Join(borrowerErr, l.device.Close())
		if l.eventsDone != nil {
			timer := time.NewTimer(ownerEventDrainTimeout)
			select {
			case <-l.eventsDone:
			case <-timer.C:
				l.closeErr = errors.Join(l.closeErr, ErrEventDrainTimeout)
			}
			timer.Stop()
		}
	})
	return l.closeErr
}

// drainDeviceEvents lets NativeTun reach netlink and RWCancel cleanup when no owner-event reader exists.
func drainDeviceEvents(device awgtun.Device) <-chan struct{} {
	done := make(chan struct{})
	events := device.Events()
	if events == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		for range events {
		}
	}()
	return done
}

type borrowed struct {
	owner     *Linux
	device    awgtun.Device
	closeOnce sync.Once
	closeErr  error
}

func (b *borrowed) File() *os.File { return b.device.File() }
func (b *borrowed) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	return b.device.Read(bufs, sizes, offset)
}
func (b *borrowed) Write(bufs [][]byte, offset int) (int, error) {
	return b.device.Write(bufs, offset)
}
func (b *borrowed) MTU() (int, error)           { return b.device.MTU() }
func (b *borrowed) Name() (string, error)       { return b.device.Name() }
func (b *borrowed) Events() <-chan awgtun.Event { return b.device.Events() }
func (b *borrowed) BatchSize() int              { return b.device.BatchSize() }
func (b *borrowed) Close() error {
	if b == nil {
		return nil
	}
	b.closeOnce.Do(func() {
		b.closeErr = b.device.Close()
		b.owner.release(b)
	})
	return b.closeErr
}

var _ awgtun.Device = (*borrowed)(nil)
