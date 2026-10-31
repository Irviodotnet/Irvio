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
	"os"
	"slices"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
)

type exitDevicePolicySpy struct {
	events                *[]string
	grant                 *killswitch.WindowsExitControl
	publishErr, revokeErr error
}

func (p *exitDevicePolicySpy) UpdateExitControl(_ context.Context, grant *killswitch.WindowsExitControl) error {
	if grant == nil {
		*p.events = append(*p.events, "revoke-wfp")
		if p.revokeErr != nil {
			return p.revokeErr
		}
		p.grant = nil
		return nil
	}
	*p.events = append(*p.events, "grant-wfp")
	copy := *grant
	p.grant = &copy
	return p.publishErr
}

func TestWindowsExitDeviceOwnsProtectionGrant(t *testing.T) {
	for _, fault := range []string{"none", "publish", "revoke"} {
		t.Run(fault, func(t *testing.T) {
			var events []string
			tun := &exitDeviceTUNSpy{events: &events}
			control := &exitDeviceControlSpy{events: &events}
			policy := &exitDevicePolicySpy{events: &events}
			device, err := openWindowsExitDevice(context.Background(), exitDeviceConfig(), t.TempDir(), windowsExitDeviceFactory{
				tunnel:  func(tunnel.Config) (windowsExitTUN, error) { return tun, nil },
				control: func(string, uint64, netip.AddrPort, netip.Prefix) (windowsExitControl, error) { return control, nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			if fault == "publish" {
				policy.publishErr = os.ErrPermission
			}
			events = nil
			err = device.protectControl(context.Background(), policy)
			if (err != nil) != (fault == "publish") || device.policy != policy {
				t.Fatal("grant ownership lost", err)
			}
			if policy.grant == nil || policy.grant.Interface != 42 || policy.grant.Local != netip.MustParseAddrPort("10.253.245.1:7890") || policy.grant.Clients != exitDeviceConfig().IPv4Pool {
				t.Fatal("wrong grant", policy.grant)
			}
			if err := device.protectControl(context.Background(), policy); !errors.Is(err, ErrAlreadyStarted) {
				t.Fatal("grant owner overwritten", err)
			}
			if fault == "revoke" {
				policy.revokeErr = os.ErrPermission
				if err := device.Close(); err == nil || device.tun != tun || device.control != control || device.policy != policy {
					t.Fatal("failed revoke lost owner", err)
				}
				if !slices.Equal(events, []string{"grant-wfp", "revoke-wfp"}) {
					t.Fatal("adapter released before WFP grant", events)
				}
				policy.revokeErr = nil
			}
			if err := device.Close(); err != nil {
				t.Fatal(err)
			}
			if policy.grant != nil || device.policy != nil || device.tun != nil || device.control != nil {
				t.Fatal("incomplete cleanup")
			}
			if !slices.Equal(events[len(events)-3:], []string{"revoke-wfp", "close-control", "close-tun"}) {
				t.Fatal("wrong release order", events)
			}
			before := len(events)
			if err := device.Close(); err != nil || len(events) != before {
				t.Fatal("not idempotent", err)
			}
			if err := device.protectControl(context.Background(), policy); !errors.Is(err, ErrClosed) {
				t.Fatal("closed device accepted grant", err)
			}
		})
	}
}

type exitDeviceTUNSpy struct {
	tunnel.DeviceOwner
	events              *[]string
	sourceErr, closeErr error
}

func (*exitDeviceTUNSpy) LUID() uint64 { return 42 }
func (s *exitDeviceTUNSpy) SetAutomaticSource(_ netip.Prefix, enabled bool) error {
	if enabled {
		panic("Exit control may not be selected as an automatic user source")
	}
	*s.events = append(*s.events, "exclude-source")
	return s.sourceErr
}
func (s *exitDeviceTUNSpy) Close() error {
	*s.events = append(*s.events, "close-tun")
	return s.closeErr
}

type exitDeviceControlSpy struct {
	events               *[]string
	installErr, closeErr error
}

func (s *exitDeviceControlSpy) Install() error {
	*s.events = append(*s.events, "install-control")
	return s.installErr
}
func (s *exitDeviceControlSpy) Close() error {
	*s.events = append(*s.events, "close-control")
	return s.closeErr
}

func exitDeviceConfig() ExitServerConfig {
	return ExitServerConfig{TUNName: "IRVIO-Exit", TUNAddresses: []netip.Prefix{netip.MustParsePrefix("10.253.245.1/24")},
		IPv4Pool: netip.MustParsePrefix("10.253.245.0/24"), M12Enabled: true, SessionControlPort: 7890}
}

func TestWindowsExitDeviceOwnership(t *testing.T) {
	for _, fault := range []string{"none", "source", "open-control", "install", "cleanup", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			var events []string
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tun := &exitDeviceTUNSpy{events: &events}
			control := &exitDeviceControlSpy{events: &events}
			if fault == "source" {
				tun.sourceErr = os.ErrPermission
			}
			if fault == "install" || fault == "cleanup" {
				control.installErr = os.ErrPermission
			}
			if fault == "cleanup" {
				control.closeErr = os.ErrPermission
			}
			factory := windowsExitDeviceFactory{
				tunnel: func(c tunnel.Config) (windowsExitTUN, error) {
					if c.MTU != tunnel.DefaultMTU || c.Name != "IRVIO-Exit" {
						t.Fatal("configuration changed")
					}
					events = append(events, "create-tun")
					return tun, nil
				},
				control: func(_ string, luid uint64, endpoint netip.AddrPort, pool netip.Prefix) (windowsExitControl, error) {
					if luid != 42 || endpoint != netip.MustParseAddrPort("10.253.245.1:7890") || pool != exitDeviceConfig().IPv4Pool {
						t.Fatal("scope changed")
					}
					events = append(events, "open-control")
					if fault == "cancel" {
						cancel()
					}
					if fault == "open-control" {
						return nil, os.ErrPermission
					}
					return control, nil
				},
			}
			d, err := openWindowsExitDevice(ctx, exitDeviceConfig(), t.TempDir(), factory)
			if fault == "none" {
				if err != nil || d == nil {
					t.Fatal(err)
				}
				if err := d.Close(); err != nil {
					t.Fatal(err)
				}
				if err := d.Close(); err != nil {
					t.Fatal(err)
				}
				want := []string{"create-tun", "exclude-source", "open-control", "install-control", "close-control", "close-tun"}
				if !slices.Equal(events, want) {
					t.Fatal("shutdown ordering", events)
				}
				return
			}
			if err == nil {
				t.Fatal("failure hidden")
			}
			if fault == "cleanup" {
				if d == nil || slices.Contains(events, "close-tun") {
					t.Fatal("failed owner discarded", events)
				}
				control.closeErr = nil
				if err := d.Close(); err != nil {
					t.Fatal("cleanup retry", err)
				}
			} else if d != nil {
				t.Fatal("successful rollback returned live owner")
			}
			if events[len(events)-1] != "close-tun" {
				t.Fatal("adapter leaked", events)
			}
			if fault == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation hidden", err)
			}
		})
	}
}

func TestWindowsExitDeviceValidationBeforeMutation(t *testing.T) {
	for _, mutate := range []func(*ExitServerConfig){
		func(c *ExitServerConfig) { c.M12Enabled = false },
		func(c *ExitServerConfig) { c.SessionControlPort = 0 },
		func(c *ExitServerConfig) { c.IPv6Pool = netip.MustParsePrefix("fd00::/64") },
		func(c *ExitServerConfig) { c.IPv4Pool = netip.MustParsePrefix("0.0.0.0/0") },
		func(c *ExitServerConfig) { c.TUNAddresses[0] = netip.MustParsePrefix("10.253.245.0/24") },
		func(c *ExitServerConfig) { c.TUNAddresses[0] = netip.MustParsePrefix("10.253.245.255/24") },
		func(c *ExitServerConfig) { c.TUNAddresses[0] = netip.MustParsePrefix("10.253.246.1/24") },
	} {
		c := exitDeviceConfig()
		mutate(&c)
		f := windowsExitDeviceFactory{tunnel: func(tunnel.Config) (windowsExitTUN, error) { t.Fatal("invalid input mutated OS"); return nil, nil },
			control: func(string, uint64, netip.AddrPort, netip.Prefix) (windowsExitControl, error) {
				t.Fatal("invalid input mutated firewall")
				return nil, nil
			}}
		if d, err := openWindowsExitDevice(context.Background(), c, t.TempDir(), f); d != nil || !errors.Is(err, ErrInvalidConfig) {
			t.Fatal("invalid input accepted", err)
		}
	}
}

func TestWindowsExitDeviceRetainsFailedAdapterClose(t *testing.T) {
	var events []string
	tun := &exitDeviceTUNSpy{events: &events, closeErr: os.ErrPermission}
	d := &windowsExitDevice{tun: tun, control: &exitDeviceControlSpy{events: &events}}
	if err := d.Close(); !errors.Is(err, os.ErrPermission) || d.tun == nil || d.control != nil {
		t.Fatal("failed adapter owner lost", err)
	}
	tun.closeErr = nil
	if err := d.Close(); err != nil || d.tun != nil {
		t.Fatal("adapter cleanup retry failed", err)
	}
	if !slices.Equal(events, []string{"close-control", "close-tun", "close-tun"}) {
		t.Fatal("cleanup retried already released control", events)
	}
}
