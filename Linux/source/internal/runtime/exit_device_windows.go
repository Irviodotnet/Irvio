// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"sync"

	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/winfirewall"
)

type windowsExitTUN interface {
	tunnel.DeviceOwner
	LUID() uint64
	SetAutomaticSource(netip.Prefix, bool) error
}

type windowsExitControl interface {
	Install() error
	Close() error
}

type windowsExitControlPolicy interface {
	UpdateExitControl(context.Context, *killswitch.WindowsExitControl) error
}

// windowsExitDevice owns the long-lived adapter/control rule, not generation-local AWG or leases.
type windowsExitDevice struct {
	tun     windowsExitTUN
	control windowsExitControl
	policy  windowsExitControlPolicy
	grant   killswitch.WindowsExitControl
	closing bool
	mu      sync.Mutex
}

type windowsExitDeviceFactory struct {
	tunnel  func(tunnel.Config) (windowsExitTUN, error)
	control func(string, uint64, netip.AddrPort, netip.Prefix) (windowsExitControl, error)
}

func newWindowsExitDevice(ctx context.Context, config ExitServerConfig, journal string) (*windowsExitDevice, error) {
	return openWindowsExitDevice(ctx, config, journal, windowsExitDeviceFactory{
		tunnel: func(c tunnel.Config) (windowsExitTUN, error) {
			w, err := tunnel.NewWindows(c)
			if err != nil {
				return nil, err
			}
			return w, nil
		},
		control: func(path string, luid uint64, endpoint netip.AddrPort, pool netip.Prefix) (windowsExitControl, error) {
			c, err := winfirewall.OpenOwnedControl(path, luid, endpoint, pool)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
	})
}

// openWindowsExitDevice returns a partial owner on cleanup failure; callers must retain it for retry.
func openWindowsExitDevice(ctx context.Context, config ExitServerConfig, journal string, factory windowsExitDeviceFactory) (*windowsExitDevice, error) {
	if ctx == nil || !filepath.IsAbs(journal) || factory.tunnel == nil || factory.control == nil ||
		!config.M12Enabled || config.SessionControlPort == 0 || config.TUNName == "" ||
		len(config.TUNAddresses) != 1 || config.IPv6Pool.IsValid() || !config.IPv4Pool.IsValid() ||
		!config.IPv4Pool.Addr().Is4() || !config.IPv4Pool.Addr().IsPrivate() ||
		config.IPv4Pool != config.IPv4Pool.Masked() || config.IPv4Pool.Bits() < 16 || config.IPv4Pool.Bits() > 30 {
		return nil, ErrInvalidConfig
	}
	address := config.TUNAddresses[0]
	if address.Masked() != config.IPv4Pool || address.Addr() == config.IPv4Pool.Addr() || !config.IPv4Pool.Contains(address.Addr().Next()) {
		return nil, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d := &windowsExitDevice{grant: killswitch.WindowsExitControl{Local: netip.AddrPortFrom(address.Addr(), config.SessionControlPort), Clients: config.IPv4Pool}}
	fail := func(cause error) (*windowsExitDevice, error) {
		if err := d.Close(); err != nil {
			return d, errors.Join(cause, err)
		}
		return nil, cause
	}
	var err error
	d.tun, err = factory.tunnel(tunnel.Config{Name: config.TUNName, MTU: tunnel.DefaultMTU, Addresses: config.TUNAddresses})
	if err != nil || d.tun == nil {
		return fail(errors.Join(err, ErrInvalidConfig))
	}
	if err := d.tun.SetAutomaticSource(address, false); err != nil {
		return fail(err)
	}
	d.grant.Interface = d.tun.LUID()
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	d.control, err = factory.control(journal, d.tun.LUID(), netip.AddrPortFrom(address.Addr(), config.SessionControlPort), config.IPv4Pool)
	if err != nil || d.control == nil {
		return fail(errors.Join(err, ErrInvalidConfig))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := d.control.Install(); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return d, nil
}

// protectControl is armed only after ProtectedMode exists; failed publication retains cleanup ownership.
func (d *windowsExitDevice) protectControl(ctx context.Context, policy windowsExitControlPolicy) error {
	if d == nil || ctx == nil || policy == nil {
		return ErrInvalidConfig
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closing || d.tun == nil {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if d.policy != nil {
		return ErrAlreadyStarted
	}
	d.policy = policy
	grant := d.grant
	return policy.UpdateExitControl(ctx, &grant)
}

// Close retains the adapter while its inbound control firewall ownership cannot be safely released.
func (d *windowsExitDevice) Close() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closing = true
	if d.policy != nil {
		if err := d.policy.UpdateExitControl(context.Background(), nil); err != nil {
			return err
		}
		d.policy = nil
	}
	if d.control != nil {
		if err := d.control.Close(); err != nil {
			return err
		}
		d.control = nil
	}
	if d.tun != nil {
		if err := d.tun.Close(); err != nil {
			return err
		}
		d.tun = nil
	}
	return nil
}
