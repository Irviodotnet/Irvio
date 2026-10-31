// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"slices"
	"sync"

	"github.com/Irviodotnet/Irvio/internal/safetypolicy"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"golang.org/x/sys/windows"
)

// WindowsBypass describes an authorized control endpoint, never an ordinary user route.
type WindowsBypass struct {
	Interface uint64
	Source    netip.Addr
	Remote    netip.AddrPort
	Protocol  uint8
	LocalPort uint16
}

// WindowsTunnelControl authorizes only daemon TCP control before TunnelLease BIND succeeds.
type WindowsTunnelControl struct {
	Interface uint64
	Source    netip.Addr
	Remote    netip.AddrPort
}

// WindowsExitControl allows only replies from the owned Exit control listener to its inner pool.
type WindowsExitControl struct {
	Interface uint64
	Local     netip.AddrPort
	Clients   netip.Prefix
}

// WindowsControlListener permits replies only while its exclusive TCP listener is owned.
type WindowsControlListener struct {
	Interface uint64
	Local     netip.AddrPort
}

// WindowsDatagramSocket authorizes only an exclusively reserved physical STUN/ICE/AWG socket.
type WindowsDatagramSocket struct {
	Interface uint64
	Local     netip.AddrPort
}

// WindowsNetwork is supplied by privileged orchestration; zero TunnelLUID keeps all ordinary WAN blocked.
type WindowsNetwork struct {
	Application      wfp.ApplicationID
	TunnelLUID       uint64
	Underlay         []WindowsBypass
	TunnelControl    []WindowsTunnelControl
	ExitControl      *WindowsExitControl
	ControlListeners []WindowsControlListener
	DatagramSockets  []WindowsDatagramSocket
}

type WindowsBackendConfig struct {
	Policy  Policy
	Owner   wfp.Config
	Network WindowsNetwork
}

type windowsPolicyStore interface {
	Replace(context.Context, []wfp.Rule) error
	ReplaceInstalled(context.Context, []wfp.Rule) error
	Inspect(context.Context) (wfp.Snapshot, error)
	Remove(context.Context) error
}

// WindowsBackend preserves the existing Controller lifecycle with independently persistent WFP protection.
type WindowsBackend struct {
	mu            sync.Mutex
	policy        Policy
	owner         wfp.Config
	rules         []wfp.Rule
	idleRules     []wfp.Rule
	network       WindowsNetwork
	store         windowsPolicyStore
	epoch         uint64
	underlayOwner *WindowsUnderlayOwner
}

func NewWindowsBackend(c WindowsBackendConfig) (*WindowsBackend, error) {
	s, err := wfp.NewStore(c.Owner)
	if err != nil {
		return nil, err
	}
	return newWindowsBackend(c, s)
}

func newWindowsBackend(c WindowsBackendConfig, s windowsPolicyStore) (*WindowsBackend, error) {
	p, err := normalizePolicy(c.Policy)
	if err != nil || s == nil {
		return nil, ErrInvalidConfig
	}
	rules, err := compileWindowsPolicy(c.Owner, c.Network)
	if err != nil {
		return nil, err
	}
	idle, err := compileWindowsPolicy(c.Owner, WindowsNetwork{})
	if err != nil {
		return nil, err
	}
	return &WindowsBackend{policy: p, owner: c.Owner, rules: rules, idleRules: idle, network: cloneWindowsNetwork(c.Network), store: s}, nil
}

func cloneWindowsNetwork(n WindowsNetwork) WindowsNetwork {
	n.Underlay = slices.Clone(n.Underlay)
	n.TunnelControl = slices.Clone(n.TunnelControl)
	n.ControlListeners = slices.Clone(n.ControlListeners)
	n.DatagramSockets = slices.Clone(n.DatagramSockets)
	if n.ExitControl != nil {
		value := *n.ExitControl
		n.ExitControl = &value
	}
	return n
}

func (b *WindowsBackend) check(ctx context.Context, p Policy) error {
	if ctx == nil {
		return ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := normalizePolicy(p)
	if err != nil || p != b.policy {
		return ErrInvalidConfig
	}
	return nil
}

func (b *WindowsBackend) Ensure(ctx context.Context, p Policy) (Installation, EnsureResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.check(ctx, p); err != nil {
		return nil, 0, err
	}
	if b.underlayOwner != nil {
		return nil, 0, ErrWindowsUnderlayBusy
	}
	before, err := b.store.Inspect(ctx)
	if err != nil {
		return nil, 0, err
	}
	result := EnsureInstalled
	if before.Installed {
		result = EnsureReconciled
	}
	if b.complete(before) {
		result = EnsureAdopted
	} else {
		if err := b.store.Replace(ctx, b.rules); err != nil {
			return nil, 0, err
		}
	}
	if err := b.verify(ctx); err != nil {
		return nil, 0, err
	}
	b.epoch++
	return &windowsInstallation{backend: b, epoch: b.epoch}, result, nil
}

func (b *WindowsBackend) Inspect(ctx context.Context, p Policy) (KernelSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.check(ctx, p); err != nil {
		return KernelSnapshot{}, err
	}
	return b.snapshot(ctx)
}

// WindowsNetworkUpdater is fenced to one installation, not a reusable authority across Disconnect/Connect.
type WindowsNetworkUpdater struct {
	backend *WindowsBackend
	epoch   uint64
}

func (b *WindowsBackend) NetworkUpdater(ctx context.Context) (*WindowsNetworkUpdater, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.check(ctx, b.policy); err != nil {
		return nil, err
	}
	s, err := b.store.Inspect(ctx)
	if err != nil {
		return nil, err
	}
	if b.epoch == 0 || !s.Installed {
		return nil, ErrNotProtected
	}
	return &WindowsNetworkUpdater{backend: b, epoch: b.epoch}, nil
}

// Update atomically replaces a live policy; callers additionally retain their existing NetworkGeneration fencing.
func (u *WindowsNetworkUpdater) Update(ctx context.Context, n WindowsNetwork) error {
	return u.update(ctx, func(WindowsNetwork) WindowsNetwork { return n }, true)
}

// UpdateTunnel preserves the latest independently reconciled underlay permissions under the same policy lock.
func (u *WindowsNetworkUpdater) UpdateTunnel(ctx context.Context, luid uint64, control []WindowsTunnelControl) error {
	return u.update(ctx, func(n WindowsNetwork) WindowsNetwork {
		n.TunnelLUID, n.TunnelControl = luid, control
		return n
	}, false)
}

// UpdateExitControl preserves independently owned upstream and physical-path permissions.
func (u *WindowsNetworkUpdater) UpdateExitControl(ctx context.Context, control *WindowsExitControl) error {
	return u.update(ctx, func(n WindowsNetwork) WindowsNetwork {
		n.ExitControl = control
		return n
	}, false)
}

// UpdateUnderlay cannot resurrect or overwrite a tunnel permission from an older network snapshot.
func (u *WindowsNetworkUpdater) UpdateUnderlay(ctx context.Context, app wfp.ApplicationID, underlay []WindowsBypass) error {
	return u.update(ctx, func(n WindowsNetwork) WindowsNetwork {
		n.Application, n.Underlay = app, underlay
		return n
	}, true)
}

func (u *WindowsNetworkUpdater) update(ctx context.Context, change func(WindowsNetwork) WindowsNetwork, ownsUnderlay bool) error {
	if u == nil || u.backend == nil {
		return ErrInvalidConfig
	}
	b := u.backend
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.check(ctx, b.policy); err != nil {
		return err
	}
	if u.epoch != b.epoch {
		return ErrStaleGeneration
	}
	if o := b.underlayOwner; o != nil {
		if ownsUnderlay || o.dirty || o.draining || o.closing {
			return ErrWindowsUnderlayBusy
		}
	}
	n := cloneWindowsNetwork(change(b.network))
	return b.replaceNetwork(ctx, n)
}

// The caller holds b.mu through validation, replacement and post-commit inspection.
func (b *WindowsBackend) replaceNetwork(ctx context.Context, n WindowsNetwork) error {
	rules, err := compileWindowsPolicy(b.owner, n)
	if err != nil {
		return err
	}
	if err := b.store.ReplaceInstalled(ctx, rules); err != nil {
		if errors.Is(err, wfp.ErrNotInstalled) {
			return errors.Join(ErrNotProtected, err)
		}
		return err
	}
	b.rules = rules
	b.network = n
	return b.verify(ctx)
}

func (b *WindowsBackend) verify(ctx context.Context) error {
	s, err := b.store.Inspect(ctx)
	if err != nil {
		return fmt.Errorf("read Windows protection: %w", err)
	}
	if !b.complete(s) {
		return fmt.Errorf("Windows protection is absent, inactive or differs from the required rules")
	}
	return nil
}

func (b *WindowsBackend) complete(s wfp.Snapshot) bool {
	if !s.Active || !s.Installed || s.SublayerWeight != b.owner.Weight || len(s.Rules) != len(b.rules) {
		return false
	}
	seen := make(map[windows.GUID]bool, len(s.Rules))
	for _, r := range s.Rules {
		if seen[r.Key] || !slices.Contains(b.rules, r) {
			return false
		}
		seen[r.Key] = true
	}
	return true
}

func (b *WindowsBackend) snapshot(ctx context.Context) (KernelSnapshot, error) {
	s, err := b.store.Inspect(ctx)
	if err != nil {
		return KernelSnapshot{}, err
	}
	return KernelSnapshot{TableName: b.policy.TableName, Version: PolicyVersion, Installed: s.Installed, Complete: b.complete(s)}, nil
}

type windowsInstallation struct {
	backend *WindowsBackend
	epoch   uint64
}

func (i *windowsInstallation) Snapshot(ctx context.Context) (KernelSnapshot, error) {
	b := i.backend
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.check(ctx, b.policy); err != nil {
		return KernelSnapshot{}, err
	}
	if i.epoch != b.epoch {
		return KernelSnapshot{}, ErrStaleGeneration
	}
	return b.snapshot(ctx)
}

func (i *windowsInstallation) Reconcile(ctx context.Context) error {
	b := i.backend
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.check(ctx, b.policy); err != nil {
		return err
	}
	if i.epoch != b.epoch {
		return ErrStaleGeneration
	}
	if o := b.underlayOwner; o != nil && (o.dirty || o.draining || o.closing) {
		return ErrWindowsUnderlayBusy
	}
	s, err := b.store.Inspect(ctx)
	if err != nil {
		return err
	}
	if b.complete(s) {
		return nil
	}
	if err := b.store.Replace(ctx, b.rules); err != nil {
		return err
	}
	return b.verify(ctx)
}

func (i *windowsInstallation) Remove(ctx context.Context) error {
	b := i.backend
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.check(ctx, b.policy); err != nil {
		return err
	}
	if i.epoch != b.epoch {
		return ErrStaleGeneration
	}
	if err := b.store.Remove(ctx); err != nil {
		return err
	}
	after, err := b.store.Inspect(ctx)
	if err != nil {
		return err
	}
	if after.Installed {
		return fmt.Errorf("Windows protection removal left owned objects")
	}
	// A later Connect must not reuse a removed tunnel's LUID or stale underlay permissions.
	b.rules = slices.Clone(b.idleRules)
	b.network = WindowsNetwork{}
	if b.underlayOwner != nil {
		b.underlayOwner.closed = true
		b.underlayOwner.leases = nil
		b.underlayOwner.listeners = nil
		b.underlayOwner.datagrams = nil
		b.underlayOwner = nil
	}
	b.epoch++
	return nil
}

func compileWindowsPolicy(owner wfp.Config, n WindowsNetwork) ([]wfp.Rule, error) {
	if _, err := wfp.NewStore(owner); err != nil {
		return nil, err
	}
	if len(n.Underlay) > 128 {
		return nil, fmt.Errorf("too many Windows control-plane grants")
	}
	if len(n.TunnelControl) > 2 || (len(n.TunnelControl) != 0 && n.TunnelLUID != 0) {
		return nil, fmt.Errorf("invalid Windows pre-BIND tunnel grants")
	}
	controlSeen := make(map[WindowsTunnelControl]bool)
	var controlInterface uint64
	for _, e := range n.TunnelControl {
		if e.Interface == 0 || (controlInterface != 0 && controlInterface != e.Interface) ||
			!e.Source.IsGlobalUnicast() || !e.Remote.Addr().IsGlobalUnicast() || e.Source == e.Remote.Addr() ||
			e.Source.Is4In6() || e.Remote.Addr().Is4In6() || e.Source.Zone() != "" || e.Remote.Addr().Zone() != "" ||
			e.Source.Is4() != e.Remote.Addr().Is4() || e.Remote.Port() == 0 || n.Application == "" || controlSeen[e] {
			return nil, fmt.Errorf("invalid Windows pre-BIND tunnel endpoint")
		}
		controlInterface = e.Interface
		controlSeen[e] = true
	}
	if e := n.ExitControl; e != nil {
		if e.Interface == 0 || !e.Local.Addr().Is4() || !e.Local.Addr().IsPrivate() || e.Local.Port() == 0 ||
			!e.Clients.IsValid() || !e.Clients.Addr().Is4() || e.Clients != e.Clients.Masked() ||
			e.Clients.Bits() < 16 || e.Clients.Bits() > 30 || !e.Clients.Contains(e.Local.Addr()) ||
			n.Application == "" || n.TunnelLUID != 0 && n.TunnelLUID != e.Interface ||
			controlInterface != 0 && controlInterface != e.Interface {
			return nil, fmt.Errorf("invalid Windows Exit control reply grant")
		}
	}
	dhcpApp, err := windowsDHCPApplication()
	if err != nil {
		return nil, err
	}
	if len(n.ControlListeners) > 8 {
		return nil, fmt.Errorf("too many Windows control listeners")
	}
	listeners := make(map[WindowsControlListener]bool)
	for _, e := range n.ControlListeners {
		if e.Interface == 0 || e.Interface == n.TunnelLUID || e.Interface == controlInterface || n.ExitControl != nil && e.Interface == n.ExitControl.Interface ||
			!e.Local.Addr().IsGlobalUnicast() || e.Local.Addr().Is4In6() || e.Local.Addr().Zone() != "" || e.Local.Port() == 0 || n.Application == "" || listeners[e] {
			return nil, fmt.Errorf("invalid Windows control listener reply grant")
		}
		listeners[e] = true
	}
	if len(n.DatagramSockets) > 4 {
		return nil, fmt.Errorf("too many Windows physical datagram sockets")
	}
	datagrams := make(map[WindowsDatagramSocket]bool)
	for _, e := range n.DatagramSockets {
		if e.Interface == 0 || e.Interface == n.TunnelLUID || e.Interface == controlInterface || n.ExitControl != nil && e.Interface == n.ExitControl.Interface ||
			!e.Local.Addr().IsGlobalUnicast() || e.Local.Addr().Is4In6() || e.Local.Addr().Zone() != "" || e.Local.Port() == 0 || n.Application == "" || datagrams[e] {
			return nil, fmt.Errorf("invalid Windows physical datagram socket grant")
		}
		datagrams[e] = true
	}
	seen := make(map[WindowsBypass]bool)
	for _, e := range n.Underlay {
		if e.Interface == 0 || e.Interface == n.TunnelLUID || e.Interface == controlInterface || n.ExitControl != nil && e.Interface == n.ExitControl.Interface || !e.Source.IsGlobalUnicast() || !e.Remote.Addr().IsGlobalUnicast() ||
			e.Source.Is4In6() || e.Remote.Addr().Is4In6() || e.Source.Zone() != "" || e.Remote.Addr().Zone() != "" ||
			e.Source.Is4() != e.Remote.Addr().Is4() || e.Remote.Port() == 0 || (e.Protocol != 6 && e.Protocol != 17) || n.Application == "" {
			return nil, fmt.Errorf("invalid Windows control-plane grant")
		}
		if seen[e] {
			return nil, fmt.Errorf("duplicate Windows control-plane grant")
		}
		seen[e] = true
	}
	var rules []wfp.Rule
	add := func(label string, r wfp.Rule) {
		d := sha256.Sum256([]byte("IRVIO-WFP-PROTECTED-V1\x00" + owner.Provider.String() + "\x00" + label))
		r.Key = windows.GUID{Data1: binary.BigEndian.Uint32(d[:4]), Data2: binary.BigEndian.Uint16(d[4:6]), Data3: binary.BigEndian.Uint16(d[6:8])}
		copy(r.Key.Data4[:], d[8:16])
		rules = append(rules, r)
	}
	for li, layer := range []windows.GUID{wfp.ConnectV4, wfp.ConnectV6, wfp.TransportV4, wfp.TransportV6} {
		is4 := layer == wfp.ConnectV4 || layer == wfp.TransportV4
		label := fmt.Sprintf("layer-%d/", li)
		add(label+"deny-default", wfp.Rule{Layer: layer, Action: wfp.Block})
		add(label+"loopback", wfp.Rule{Layer: layer, Action: wfp.Permit, Priority: 500, LoopbackOnly: true})
		if n.TunnelLUID != 0 {
			add(label+"tunnel", wfp.Rule{Layer: layer, Action: wfp.Permit, Priority: 200, LocalInterface: n.TunnelLUID})
		}
		for _, transport := range []safetypolicy.Transport{safetypolicy.TransportTCP, safetypolicy.TransportUDP} {
			proto := uint8(6)
			if transport == safetypolicy.TransportUDP {
				proto = 17
			}
			for _, port := range safetypolicy.Ports(safetypolicy.V0, transport) {
				add(fmt.Sprintf("%ssafety-%d-%d", label, proto, port), wfp.Rule{Layer: layer, Action: wfp.Block, Priority: 300, Protocol: proto, RemotePort: port})
			}
		}
		local, remote := uint16(68), uint16(67)
		if !is4 {
			local, remote = 546, 547
		}
		dhcp := wfp.Rule{Layer: layer, Action: wfp.Permit, Priority: 100, Protocol: 17, LocalPort: local, RemotePort: remote}
		if layer == wfp.ConnectV4 || layer == wfp.ConnectV6 {
			dhcp.User, dhcp.Application = wfp.DHCPService, dhcpApp
		}
		add(label+"dhcp", dhcp)
		if !is4 {
			for _, kind := range []uint8{133, 134, 135, 136} {
				add(fmt.Sprintf("%sneighbor-%d", label, kind), wfp.Rule{Layer: layer, Action: wfp.Permit, Priority: 100, Protocol: 58, ICMP: true, ICMPType: kind})
			}
		}
		for ei, e := range n.Underlay {
			if e.Source.Is4() != is4 {
				continue
			}
			r := wfp.Rule{Layer: layer, Action: wfp.Permit, Priority: 400, LocalInterface: e.Interface, LocalAddress: e.Source, LocalPort: e.LocalPort, Remote: netip.PrefixFrom(e.Remote.Addr(), e.Remote.Addr().BitLen()), RemotePort: e.Remote.Port(), Protocol: e.Protocol}
			if layer == wfp.ConnectV4 || layer == wfp.ConnectV6 {
				r.Application = n.Application
				r.User = wfp.SystemOrAdministrator
			}
			add(fmt.Sprintf("%sunderlay-%d", label, ei), r)
		}
		for ei, e := range n.DatagramSockets {
			if e.Local.Addr().Is4() != is4 {
				continue
			}
			r := wfp.Rule{Layer: layer, Action: wfp.Permit, Priority: 400, LocalInterface: e.Interface, LocalAddress: e.Local.Addr(), LocalPort: e.Local.Port(), Protocol: 17}
			if layer == wfp.ConnectV4 || layer == wfp.ConnectV6 {
				r.Application, r.User = n.Application, wfp.SystemOrAdministrator
			}
			add(fmt.Sprintf("%sdatagram-%d", label, ei), r)
		}
		for ei, e := range n.TunnelControl {
			if e.Source.Is4() != is4 {
				continue
			}
			r := wfp.Rule{Layer: layer, Action: wfp.Permit, Priority: 400, LocalInterface: e.Interface, LocalAddress: e.Source,
				Remote: netip.PrefixFrom(e.Remote.Addr(), e.Remote.Addr().BitLen()), RemotePort: e.Remote.Port(), Protocol: 6}
			if layer == wfp.ConnectV4 || layer == wfp.ConnectV6 {
				r.Application, r.User = n.Application, wfp.SystemOrAdministrator
			}
			add(fmt.Sprintf("%stunnel-control-%d", label, ei), r)
		}
		if layer == wfp.TransportV4 || layer == wfp.TransportV6 {
			for ei, e := range n.ControlListeners {
				if e.Local.Addr().Is4() != is4 {
					continue
				}
				add(fmt.Sprintf("%slistener-reply-%d", label, ei), wfp.Rule{Layer: layer, Action: wfp.Permit, Priority: 400,
					LocalInterface: e.Interface, LocalAddress: e.Local.Addr(), LocalPort: e.Local.Port(), Protocol: 6})
			}
		}
		if e := n.ExitControl; e != nil && is4 {
			r := wfp.Rule{Layer: layer, Action: wfp.Permit, Priority: 400, LocalInterface: e.Interface,
				LocalAddress: e.Local.Addr(), LocalPort: e.Local.Port(), Remote: e.Clients, Protocol: 6}
			if layer == wfp.ConnectV4 {
				r.Application, r.User = n.Application, wfp.SystemOrAdministrator
			}
			add(label+"exit-control", r)
		}
	}
	for ei, e := range n.ControlListeners {
		layer := wfp.AcceptV4
		if e.Local.Addr().Is6() {
			layer = wfp.AcceptV6
		}
		r := wfp.Rule{Layer: layer, Action: wfp.Block, Priority: 390, LocalInterface: e.Interface, LocalAddress: e.Local.Addr(), LocalPort: e.Local.Port(), Protocol: 6}
		add(fmt.Sprintf("listener-%d/deny-foreign", ei), r)
		r.Action, r.Priority, r.Application, r.User = wfp.Permit, 400, n.Application, wfp.SystemOrAdministrator
		add(fmt.Sprintf("listener-%d/allow-owner", ei), r)
	}
	for ei, e := range n.DatagramSockets {
		layer := wfp.AcceptV4
		if e.Local.Addr().Is6() {
			layer = wfp.AcceptV6
		}
		r := wfp.Rule{Layer: layer, Action: wfp.Block, Priority: 390, LocalInterface: e.Interface, LocalAddress: e.Local.Addr(), LocalPort: e.Local.Port(), Protocol: 17}
		add(fmt.Sprintf("datagram-%d/deny-foreign", ei), r)
		r.Action, r.Priority, r.Application, r.User = wfp.Permit, 400, n.Application, wfp.SystemOrAdministrator
		add(fmt.Sprintf("datagram-%d/allow-owner", ei), r)
	}
	for _, r := range rules {
		if err := r.Validate(); err != nil {
			return nil, err
		}
	}
	return rules, nil
}

var windowsDHCPApplication = sync.OnceValues(func() (wfp.ApplicationID, error) {
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return "", err
	}
	return wfp.ApplicationIDForPath(filepath.Join(dir, "svchost.exe"))
})
