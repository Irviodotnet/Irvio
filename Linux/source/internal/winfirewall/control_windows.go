// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfirewall

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

var ErrConflict = errors.New("Windows Firewall control rule ownership mismatch")
var policyMu sync.Mutex

// Control owns one exact inbound exception; the caller must retain it until Close succeeds.
type Control struct {
	name, application, iface, description string
	luid                                  uint64
	guid                                  windows.GUID
	owner                                 windows.GUID
	endpoint                              netip.AddrPort
	pool                                  netip.Prefix
	physical                              bool
	protocol                              uint32
}

// NewControl captures a ready tunnel; owner must be unique and retained by the runtime recovery journal.
func NewControl(owner windows.GUID, luid uint64, endpoint netip.AddrPort, pool netip.Prefix) (*Control, error) {
	if runtime.GOARCH != "amd64" {
		return nil, fmt.Errorf("Windows Firewall control backend requires amd64")
	}
	if owner == (windows.GUID{}) || luid == 0 || !endpoint.Addr().Is4() || !endpoint.Addr().IsPrivate() || endpoint.Port() == 0 ||
		!pool.IsValid() || !pool.Addr().Is4() || !pool.Addr().IsPrivate() || pool != pool.Masked() || pool.Bits() < 16 || !pool.Contains(endpoint.Addr()) {
		return nil, fmt.Errorf("control firewall requires an owner, TUN, private IPv4 endpoint and bounded private pool")
	}
	row := windows.MibIfRow2{InterfaceLuid: luid}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return nil, err
	}
	c := &Control{name: "IRVIO.Control." + owner.String(), application: exe, luid: luid, guid: row.InterfaceGuid, owner: owner,
		iface: windows.UTF16ToString(row.Alias[:]), endpoint: endpoint, pool: pool,
		description: "IRVIO TCP control v1; tunnel=" + row.InterfaceGuid.String()}
	if err := c.validateTunnel(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Control) validateTunnel() error {
	row := windows.MibIfRow2{InterfaceLuid: c.luid}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row); err != nil {
		return err
	}
	validType := row.Type == 53
	if c.physical {
		validType = row.Type != 53 && row.Type != 24
	}
	if row.InterfaceGuid != c.guid || c.guid == (windows.GUID{}) || !validType || row.OperStatus != windows.IfOperStatusUp ||
		c.iface == "" || windows.UTF16ToString(row.Alias[:]) != c.iface {
		return fmt.Errorf("firewall interface replaced, renamed, down or wrong type")
	}
	binding, err := winroute.ForSource(c.endpoint.Addr())
	if err != nil {
		return err
	}
	if binding.LUID != c.luid {
		return fmt.Errorf("control firewall address belongs to another interface")
	}
	return nil
}

// Install preserves explicit administrator blocks and refuses to overwrite a foreign same-name rule.
func (c *Control) Install() error {
	if c == nil {
		return fmt.Errorf("control firewall owner required")
	}
	return withRules(func(rules *object) error {
		if err := c.validateTunnel(); err != nil {
			return err
		}
		old, err := findRule(rules, c.name)
		if err != nil {
			return err
		}
		if old != nil {
			defer old.release()
			return c.verify(old)
		}
		rule, err := create(&classRule, &iidRule)
		if err != nil {
			return err
		}
		defer rule.release()
		if err := c.configure(rule); err != nil {
			return err
		}
		if err := rules.call(8, uintptr(unsafe.Pointer(rule))); err != nil {
			return fmt.Errorf("add control firewall rule: %w", err)
		}
		stored, err := findRule(rules, c.name)
		if err != nil {
			return err
		}
		if stored == nil {
			return fmt.Errorf("control firewall rule not persisted")
		}
		defer stored.release()
		return c.verify(stored)
	})
}

// Close removes only unchanged owned scope; absent rules are already clean.
func (c *Control) Close() error {
	if c == nil {
		return nil
	}
	return withRules(func(rules *object) error {
		rule, err := findRule(rules, c.name)
		if err != nil || rule == nil {
			return err
		}
		defer rule.release()
		if err := c.verify(rule); err != nil {
			return err
		}
		return rules.putString(9, c.name)
	})
}

func withRules(fn func(*object) error) error {
	if runtime.GOARCH != "amd64" {
		return fmt.Errorf("Windows Firewall COM requires amd64")
	}
	policyMu.Lock()
	defer policyMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := ole.NewProc("CoInitializeEx").Call(0, 0)
	if err := checked(hr); err != nil {
		return err
	}
	defer windows.CoUninitialize()
	policy, err := create(&classPolicy, &iidPolicy)
	if err != nil {
		return err
	}
	defer policy.release()
	var rules *object
	if err := policy.call(18, uintptr(unsafe.Pointer(&rules))); err != nil {
		return err
	}
	defer rules.release()
	return fn(rules)
}

func findRule(rules *object, name string) (*object, error) {
	b, err := newString(name)
	if err != nil {
		return nil, err
	}
	defer freeString.Call(b)
	var rule *object
	err = rules.call(10, b, uintptr(unsafe.Pointer(&rule)))
	if errors.Is(err, result(0x80070002)) {
		return nil, nil
	}
	return rule, err
}

func (c *Control) strings() []struct {
	get   int
	value string
} {
	remote, group := c.pool.String(), "IRVIO.Control.v1"
	if c.physical {
		remote, group = "*", "IRVIO.Inbound.v1"
	}
	return []struct {
		get   int
		value string
	}{
		{7, c.name}, {9, c.description}, {11, c.application}, {13, ""},
		{17, strconv.Itoa(int(c.endpoint.Port()))}, {19, "*"},
		{21, c.endpoint.Addr().String()}, {23, remote}, {31, "All"}, {35, group},
	}
}

var numbers = []struct {
	get   int
	value uint32
}{{15, 6}, {27, 1}, {33, 0xffff}, {37, 0x7fffffff}, {39, 0}, {41, 1}}

func (c *Control) ruleNumbers() []struct {
	get   int
	value uint32
} {
	result := append([]struct {
		get   int
		value uint32
	}(nil), numbers...)
	if c.physical {
		result[0].value = c.protocol
	}
	return result
}

func (c *Control) configure(rule *object) error {
	if err := rule.call(16, uintptr(c.ruleNumbers()[0].value)); err != nil {
		return err
	}
	for _, field := range c.strings() {
		if field.get == 13 {
			continue
		}
		if err := rule.putString(field.get+1, field.value); err != nil {
			return fmt.Errorf("control firewall property %d: %w", field.get, err)
		}
	}
	if err := rule.putInterface(c.iface); err != nil {
		return fmt.Errorf("control firewall interface: %w", err)
	}
	for _, field := range c.ruleNumbers() {
		if err := rule.call(field.get+1, uintptr(field.value)); err != nil {
			return err
		}
	}
	return nil
}

func (c *Control) verify(rule *object) error {
	for _, field := range c.strings() {
		got, err := rule.getString(field.get)
		if err != nil {
			return fmt.Errorf("read control firewall property %d: %w", field.get, err)
		}
		match := got == field.value
		if field.get == 11 {
			match = strings.EqualFold(got, field.value)
		}
		if field.get == 21 {
			match = samePrefix(got, netip.PrefixFrom(c.endpoint.Addr(), c.endpoint.Addr().BitLen()))
		}
		if field.get == 23 && !c.physical {
			match = samePrefix(got, c.pool)
		}
		if !match {
			return fmt.Errorf("%w: property %d", ErrConflict, field.get)
		}
	}
	for _, field := range c.ruleNumbers() {
		got, err := rule.getNumber(field.get)
		if err != nil {
			return err
		}
		if got != field.value {
			return fmt.Errorf("%w: property %d", ErrConflict, field.get)
		}
	}
	iface, err := rule.getInterface()
	if err != nil {
		return err
	}
	if !c.matchesInterface(iface) {
		return fmt.Errorf("%w: interface", ErrConflict)
	}
	return nil
}

// Firewall returns a GUID for removed adapters; a reused friendly name must not establish ownership.
func (c *Control) matchesInterface(value string) bool {
	if c.guid == (windows.GUID{}) {
		return false
	}
	if id, err := windows.GUIDFromString(value); err == nil {
		return id == c.guid
	}
	if value != c.iface {
		return false
	}
	iface, err := net.InterfaceByName(value)
	if err != nil {
		return false
	}
	row := windows.MibIfRow2{InterfaceIndex: uint32(iface.Index)}
	return windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row) == nil && row.InterfaceGuid == c.guid
}

func samePrefix(text string, expected netip.Prefix) bool {
	if p, err := netip.ParsePrefix(text); err == nil {
		return p == expected
	}
	if ip, err := netip.ParseAddr(text); err == nil {
		return expected.Bits() == ip.BitLen() && ip == expected.Addr()
	}
	ipText, maskText, ok := strings.Cut(text, "/")
	if !ok {
		return false
	}
	ip, err := netip.ParseAddr(ipText)
	mask, maskErr := netip.ParseAddr(maskText)
	if err != nil || maskErr != nil || !mask.Is4() {
		return false
	}
	ones, bits := net.IPMask(mask.AsSlice()).Size()
	return bits == 32 && netip.PrefixFrom(ip, ones) == expected
}
