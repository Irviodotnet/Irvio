//go:build linux

// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/awg"
	"github.com/Irviodotnet/Irvio/internal/exitfirewall"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/icesignal"
	"github.com/Irviodotnet/Irvio/internal/ipforward"
	"github.com/Irviodotnet/Irvio/internal/linuxroute"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
)

type m7MultiPeerSpec struct {
	PeerID     string `json:"peer_id"`
	KeySeed    int    `json:"key_seed"`
	AllowedIPs string `json:"allowed_ips"`
}

type m7MultiPeerFile struct {
	Peers []m7MultiPeerSpec `json:"peers"`
}

type m7MultiConnection struct {
	accepted  icesignal.AcceptedSession
	attempt   *iceconnect.Attempt
	connected *iceconnect.ConnectedSession
}

type m7MultiOutcome struct {
	index     int
	connected *iceconnect.ConnectedSession
	err       error
}

func runM7MultiResponder(
	ctx context.Context,
	value *report,
	parsed options,
	transportHost host.Host,
	sessionStack *stack,
	signalConfig icesignal.Config,
) error {
	if parsed.m7Role != "exit" || !parsed.awg || !parsed.awgRealTUN {
		return errors.New("multi-peer responder requires the M7 Exit real-TUN data plane")
	}
	service, err := icesignal.NewService(
		transportHost,
		transportHost.ID(),
		icesignal.SessionFactoryFunc(func(ctx context.Context, _ peer.ID, _ icesession.Description) (*icesession.PreparedSession, error) {
			return sessionStack.engine.Prepare(ctx)
		}),
		icesignal.AuthorizerFunc(func(context.Context, peer.ID, icesession.SessionPurpose) error { return nil }),
		signalConfig,
	)
	if err != nil {
		return err
	}
	defer service.Close()
	if err := writeJSON(parsed.ready, *value); err != nil {
		return err
	}
	connections := make([]m7MultiConnection, 0, parsed.m7MultiCount)
	defer func() {
		for index := len(connections) - 1; index >= 0; index-- {
			if connections[index].connected != nil {
				_ = connections[index].connected.Close()
			} else if connections[index].attempt != nil {
				_ = connections[index].attempt.Close()
			} else if connections[index].accepted.Local != nil {
				_ = connections[index].accepted.Local.Close()
			}
		}
	}()
	outcomes := make(chan m7MultiOutcome, parsed.m7MultiCount)
	for index := 0; index < parsed.m7MultiCount; index++ {
		accepted, acceptErr := service.NextAccepted(ctx)
		if acceptErr != nil {
			return acceptErr
		}
		attempt, attemptErr := responderAttempt(accepted, transportHost.ID(), signalConfig.Validation)
		if attemptErr != nil {
			_ = accepted.Local.Close()
			return attemptErr
		}
		connections = append(connections, m7MultiConnection{accepted: accepted, attempt: attempt})
		if loadErr := attempt.LoadRemoteCandidates(ctx); loadErr != nil {
			return loadErr
		}
		go func(outcomeIndex int, candidate *iceconnect.Attempt) {
			connected, connectErr := candidate.Connect(ctx)
			outcomes <- m7MultiOutcome{index: outcomeIndex, connected: connected, err: connectErr}
		}(index, attempt)
	}
	for range parsed.m7MultiCount {
		outcome := <-outcomes
		if outcome.err != nil {
			return outcome.err
		}
		connections[outcome.index].connected = outcome.connected
	}
	if len(connections) == 0 {
		return errors.New("multi-peer responder connected no peers")
	}
	fillAccepted(value, connections[0].accepted)
	if err := fillConnected(value, connections[0].connected.Result(), sessionStack.mux.Port()); err != nil {
		return err
	}
	value.MultiPeers = len(connections)
	dataPlane, err := runM7MultiAWG(ctx, value, parsed, sessionStack, connections)
	if err != nil {
		return err
	}
	defer dataPlane.Close()
	return holdConnected(ctx, parsed.release, parsed.result, *value)
}

func runM7MultiAWG(ctx context.Context, value *report, parsed options, sessionStack *stack, connections []m7MultiConnection) (_ *awgDataPlane, resultErr error) {
	specs, err := waitM7MultiPeerFile(ctx, parsed.m7MultiConfig, len(connections))
	if err != nil {
		return nil, err
	}
	addresses, err := parsePrefixList(parsed.awgTUNAddresses)
	if err != nil {
		return nil, err
	}
	owner, err := tunnel.NewLinux(tunnel.Config{Name: parsed.awgTUNName, MTU: tunnel.DefaultMTU, Addresses: addresses})
	if err != nil {
		return nil, err
	}
	var transport *awg.Transport
	var forwarding *ipforward.Manager
	var firewall exitfirewall.Installation
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, closeOptional(transport), closeOptional(firewall), closeOptional(forwarding), owner.Close())
		}
	}()
	borrowed, err := owner.Borrow()
	if err != nil {
		return nil, err
	}
	forwarding, err = ipforward.Enable(ctx, ipforward.Config{IPv4: parsed.m7ClientPool4 != "", IPv6: parsed.m7ClientPool6 != ""})
	if err != nil {
		return nil, err
	}
	pool4, err := parseOptionalPrefix(parsed.m7ClientPool4)
	if err != nil {
		return nil, err
	}
	pool6, err := parseOptionalPrefix(parsed.m7ClientPool6)
	if err != nil {
		return nil, err
	}
	var management []netip.Prefix
	if parsed.m7Management != "" {
		management, err = parsePrefixList(parsed.m7Management)
		if err != nil {
			return nil, err
		}
	}
	firewall, err = (exitfirewall.NFTBackend{}).Apply(ctx, exitfirewall.Policy{
		TUNName: owner.Name(), WANName: parsed.m7WANName,
		ClientIPv4: pool4, ClientIPv6: pool6, BlockedDestinations: management,
	})
	if err != nil {
		return nil, err
	}
	privateKey, publicKey, err := awgFixtureKey(byte(parsed.awgLocalKeySeed))
	if err != nil {
		return nil, err
	}
	peerConfigs := make([]awg.PeerConfig, 0, len(connections))
	publicKeys := make([]awg.Key, 0, len(connections))
	seen := make(map[peer.ID]struct{}, len(connections))
	networkGeneration := networkstate.Generation(connections[0].connected.Result().TransportGeneration)
	for _, connection := range connections {
		remote := connection.accepted.RemotePeer
		spec, exists := specs[remote]
		if !exists {
			return nil, fmt.Errorf("missing multi-peer AWG configuration for %s", remote)
		}
		if _, duplicate := seen[remote]; duplicate {
			return nil, errors.New("duplicate connected multi-peer identity")
		}
		seen[remote] = struct{}{}
		allowed, parseErr := parseAllowedPrefixList(spec.AllowedIPs)
		if parseErr != nil {
			return nil, parseErr
		}
		_, remotePublic, keyErr := awgFixtureKey(byte(spec.KeySeed))
		if keyErr != nil {
			return nil, keyErr
		}
		iceResult := connection.connected.Result()
		if networkstate.Generation(iceResult.TransportGeneration) != networkGeneration {
			return nil, errors.New("multi-peer sessions crossed transport generations")
		}
		selected, selectedErr := awg.SelectedEndpointFromICE(
			iceResult,
			networkGeneration,
			1,
			iceResult.RemotePeer,
		)
		if selectedErr != nil {
			return nil, selectedErr
		}
		publicKeys = append(publicKeys, remotePublic)
		peerConfigs = append(peerConfigs, awg.PeerConfig{
			RemotePeer: remote, PublicKey: remotePublic, Endpoint: selected, AllowedIPs: allowed,
		})
	}
	if len(seen) != len(specs) {
		return nil, errors.New("multi-peer AWG configuration contains an unconnected peer")
	}
	transport, err = awg.NewTransport(awg.Config{
		Mux: sessionStack.mux, NetworkGeneration: networkGeneration, TUN: borrowed,
		PrivateKey: privateKey, Obfuscation: awgFixtureObfuscation(),
		MaxPeers: len(peerConfigs), Peers: peerConfigs,
	})
	if err != nil {
		return nil, err
	}
	if err := transport.Bind().SetMark(linuxroute.DefaultOuterMark); err != nil {
		return nil, err
	}
	value.AWGPublicFingerprint = publicKey.Fingerprint()
	value.AWGTUNName = owner.Name()
	value.AWGTUNIfIndex = owner.IfIndex()
	value.AWGSocketMark = sessionStack.mux.Mark()
	value.AWGSocketInode, err = udpSocketInode(sessionStack.mux.Port(), "/proc/net/udp")
	if err != nil {
		return nil, err
	}
	snapshot, err := firewall.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	value.ExitFirewallTable = snapshot.TableName
	value.ExitFirewallRules = snapshot.Rules
	if err := waitForConnectStart(ctx, parsed.awgReady, parsed.awgStart, *value); err != nil {
		return nil, err
	}
	for index, public := range publicKeys {
		if _, err := transport.WaitHandshake(ctx, public); err != nil {
			return nil, fmt.Errorf("wait for multi-peer AWG handshake %d: %w", index, err)
		}
	}
	for _, connection := range connections {
		current, currentErr := connection.connected.CurrentResult()
		if currentErr != nil {
			return nil, currentErr
		}
		if current.ApplicationPacketsSent != 0 || current.ApplicationPacketsRecv != 0 || current.ApplicationBytesSent != 0 || current.ApplicationBytesRecv != 0 {
			return nil, errors.New("multi-peer AWG application traffic leaked through ice.Conn")
		}
	}
	value.AWGHandshake = true
	bindStats := transport.Bind().Stats()
	value.AWGSendPackets = bindStats.SendPackets
	value.AWGReceivePackets = bindStats.ReceivePackets
	result := &awgDataPlane{transport: transport, tun: owner, firewall: firewall, forwarding: forwarding}
	transport, owner, firewall, forwarding = nil, nil, nil, nil
	return result, nil
}

func waitM7MultiPeerFile(ctx context.Context, path string, count int) (map[peer.ID]m7MultiPeerSpec, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			var file m7MultiPeerFile
			if err := json.Unmarshal(data, &file); err != nil {
				return nil, err
			}
			if len(file.Peers) != count || count < 2 || count > 8 {
				return nil, errors.New("invalid multi-peer AWG configuration size")
			}
			result := make(map[peer.ID]m7MultiPeerSpec, count)
			seeds := make(map[int]struct{}, count)
			for _, spec := range file.Peers {
				id, decodeErr := peer.Decode(spec.PeerID)
				if decodeErr != nil || spec.KeySeed < 1 || spec.KeySeed > 255 || spec.KeySeed == 91 || spec.AllowedIPs == "" {
					return nil, errors.New("invalid multi-peer AWG entry")
				}
				if _, exists := result[id]; exists {
					return nil, errors.New("duplicate multi-peer identity")
				}
				if _, exists := seeds[spec.KeySeed]; exists {
					return nil, errors.New("duplicate multi-peer key seed")
				}
				result[id] = spec
				seeds[spec.KeySeed] = struct{}{}
			}
			return result, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}
