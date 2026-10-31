// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/netip"
	"os"
	"strconv"

	"github.com/Irviodotnet/Irvio/internal/controlbypass"
	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/linuxroute"
	"github.com/Irviodotnet/Irvio/internal/stunserver"
	"github.com/multiformats/go-multiaddr"
)

type DaemonPlatformConfig struct{}
type daemonPlatform struct{ controlBypass *controlbypass.Ownership }

func validateDaemonPlatform(DaemonConfig) error { return nil }

func (d *Daemon) disconnectProtection(ctx context.Context) error { return d.protection.Disconnect(ctx) }

func (d *Daemon) newPlatformProtection(context.Context) (*killswitch.Controller, error) {
	return killswitch.New(killswitch.Config{
		Backend: killswitch.NFTBackend{},
		Policy:  killswitch.Policy{TUNName: "hydra0", BypassMark: linuxroute.IrvioBypassMark, TunnelControlMark: linuxroute.IrvioTunnelControlMark},
	})
}

func (d *Daemon) preparePlatform(ctx context.Context) (uint32, error) {
	if d.config.PublicSTUN {
		bind := d.config.PublicSTUNBind
		if !bind.IsValid() {
			bind = netip.IPv4Unspecified()
		}
		server, err := stunserver.Listen(bind, stunserver.DefaultPort)
		if err != nil {
			return 0, err
		}
		d.stunServer = server
		d.config.Logger.Info("PUBLIC_STUN_READY", "bind", server.Addr())
	}
	controlMark := uint32(0)
	runtimeConfigured := d.config.Connector != nil || d.config.Production != nil &&
		(!d.config.Exit || d.config.Production.ConnectCountry != "" || d.config.Production.PrivateTarget != "")
	if d.config.ProtectedMode && (!runtimeConfigured || d.config.ProtectAtStart) {
		generation, err := d.protection.Protect(ctx)
		if err != nil {
			return 0, err
		}
		d.config.Logger.Info("HYDRAD_PROTECTED_MODE", "generation", generation, "adopted", d.protection.Snapshot().Adopted)
		controlMark = linuxroute.IrvioBypassMark
	}
	if d.config.Connector != nil || d.config.Production != nil || d.config.M12.Enabled {
		controlMark = linuxroute.IrvioBypassMark
	}
	return controlMark, nil
}

func (d *Daemon) configurePlatformDiscovery(context.Context, *discovery.Config) error { return nil }
func (d *Daemon) platformNodeReady(ctx context.Context, node *discovery.Node) error {
	if needsAcceptedControlBypass(d.config) {
		ports, err := acceptedControlListenPorts(node.ListenAddresses())
		if err != nil {
			return fmt.Errorf("resolve accepted-control listeners: %w", err)
		}
		digest := sha256.Sum256([]byte(d.config.DataDir))
		suffix := fmt.Sprintf("%x", digest[:8])
		owner, err := controlbypass.Install(controlbypass.Config{
			Name: "hydrad-" + suffix, TableName: "hydra_ctl_" + suffix,
			Mark: linuxroute.IrvioBypassMark, PID: os.Getpid(), ListenPorts: ports,
		})
		if err != nil {
			return fmt.Errorf("install accepted-control bypass ownership: %w", err)
		}
		d.platform.controlBypass = owner
	}

	if d.config.Production != nil && node.ControlMark() != linuxroute.IrvioBypassMark {
		return fmt.Errorf("production libp2p transport is not Irvio-marked: %w", ErrInvalidConfig)
	}
	return nil
}
func (d *Daemon) newDaemonTransport() (TransportLifecycle, error) {
	return newTransportOwner(*d.config.Production, d.config.Logger)
}

// Mark accepted control traffic so protected Exits and verifiers can answer without user-WAN bypass.
func needsAcceptedControlBypass(config DaemonConfig) bool {
	return config.ProtectedMode && (config.Exit || config.PublicRole.Requested)
}

func acceptedControlListenPorts(addresses []string) ([]uint16, error) {
	seen := make(map[uint16]struct{})
	ports := make([]uint16, 0, 2)
	for _, address := range addresses {
		value, err := multiaddr.NewMultiaddr(address)
		if err != nil {
			return nil, err
		}
		tcpPort, err := value.ValueForProtocol(multiaddr.P_TCP)
		if err != nil {
			continue
		}
		port, err := strconv.ParseUint(tcpPort, 10, 16)
		if err != nil || port == 0 {
			return nil, fmt.Errorf("invalid control TCP listener port %q", tcpPort)
		}
		if _, ok := seen[uint16(port)]; !ok {
			seen[uint16(port)] = struct{}{}
			ports = append(ports, uint16(port))
		}
	}
	if len(ports) == 0 {
		return nil, fmt.Errorf("no control TCP listener")
	}
	return ports, nil
}

func (d *Daemon) ControlBypassStatus() (controlbypass.Snapshot, error) {
	d.mu.RLock()
	owner := d.platform.controlBypass
	d.mu.RUnlock()
	if owner == nil {
		return controlbypass.Snapshot{}, ErrNotStarted
	}
	return owner.Snapshot()
}

func (d *Daemon) closeResources(ctx context.Context) error {
	d.mu.Lock()
	d.privateAdminExit = nil
	d.mu.Unlock()
	if d.exitServer != nil {
		d.exitServer.stopAdmissions()
	}
	if d.privateHosting != nil {
		d.privateHosting.policy.Close()
	}
	if d.m12 != nil {
		if err := d.m12.stopExitRegistrations(ctx); err != nil {
			return err
		}
	}
	if d.runtime != nil {
		if err := d.runtime.Close(ctx); err != nil {
			return err
		}
		d.runtime, d.network, d.oracle, d.protection = nil, nil, nil, nil
		d.transport, d.clientCore = nil, nil
	}
	if d.m12 != nil {
		if err := d.m12.Close(ctx); err != nil {
			return err
		}
		d.m12 = nil
	}
	if d.publicRole != nil {
		if err := d.publicRole.Close(); err != nil {
			return err
		}
		d.publicRole = nil
	}
	if d.exitServer != nil {
		if err := d.exitServer.Close(ctx); err != nil {
			return err
		}
		d.exitServer = nil
	}
	if d.exitPump != nil {
		if err := d.exitPump.Close(); err != nil {
			return err
		}
		d.exitPump, d.transport = nil, nil
	}
	if d.clientCore != nil {
		if err := d.clientCore.Close(); err != nil {
			return err
		}
		d.clientCore = nil
	}
	if d.transport != nil {
		if err := d.transport.Close(); err != nil {
			return err
		}
		d.transport = nil
	}
	if d.protection != nil {
		if d.protection.Snapshot().State.Protected() {
			if err := d.protection.Disconnect(ctx); err != nil {
				return err
			}
		}
		if err := d.protection.Close(ctx); err != nil {
			return err
		}
		d.protection = nil
	}
	if d.network != nil {
		if err := d.network.Close(); err != nil {
			return err
		}
		d.network = nil
	}
	if d.underlayChallenge != nil {
		if err := d.underlayChallenge.Close(); err != nil {
			return err
		}
		d.underlayChallenge = nil
	}
	if d.node != nil {
		if err := d.node.Close(); err != nil {
			return err
		}
		d.node = nil
	}
	if d.platform.controlBypass != nil {
		if err := d.platform.controlBypass.Close(); err != nil {
			return err
		}
		d.platform.controlBypass = nil
	}
	if d.stunServer != nil {
		if err := d.stunServer.Close(); err != nil {
			return err
		}
		d.stunServer = nil
	}
	return nil
}
