// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package reachability

import (
	"context"
	"fmt"
	"io"
	"sync"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

// FamilyRequester uses a fresh authenticated host per request to prevent cross-family proof reuse.
type FamilyRequester struct {
	privateKey crypto.PrivKey
	mark       uint32
	mu         sync.RWMutex
	targets    map[peer.ID]peer.AddrInfo
}

func NewFamilyRequester(privateKey crypto.PrivKey, mark uint32, targets []peer.AddrInfo) (*FamilyRequester, error) {
	if privateKey == nil || len(targets) == 0 || len(targets) > DefaultVerifierLimit {
		return nil, ErrInvalidConfig
	}
	result := &FamilyRequester{privateKey: privateKey, mark: mark}
	if err := result.SetTargets(targets); err != nil {
		return nil, err
	}
	return result, nil
}

// SetTargets replaces destinations atomically; in-flight requests retain their original fences.
func (r *FamilyRequester) SetTargets(targets []peer.AddrInfo) error {
	if len(targets) == 0 || len(targets) > DefaultVerifierLimit {
		return ErrInvalidConfig
	}
	next := make(map[peer.ID]peer.AddrInfo, len(targets))
	for _, target := range targets {
		if target.ID == "" || len(target.Addrs) == 0 {
			return ErrInvalidConfig
		}
		copy := peer.AddrInfo{ID: target.ID, Addrs: append([]ma.Multiaddr(nil), target.Addrs...)}
		next[target.ID] = copy
	}
	r.mu.Lock()
	r.targets = next
	r.mu.Unlock()
	return nil
}

func (r *FamilyRequester) Open(ctx context.Context, remote peer.ID, family irviov1.ReachabilityFamily) (network.Stream, io.Closer, error) {
	if ctx == nil || remote == "" || family != irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4 &&
		family != irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV6 {
		return nil, nil, ErrInvalidConfig
	}
	r.mu.RLock()
	target, exists := r.targets[remote]
	r.mu.RUnlock()
	if !exists {
		return nil, nil, ErrWrongPeer
	}
	protocolCode := ma.P_IP6
	if family == irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4 {
		protocolCode = ma.P_IP4
	}
	filtered := target.Addrs[:0:0]
	for _, address := range target.Addrs {
		if _, err := address.ValueForProtocol(protocolCode); err == nil {
			filtered = append(filtered, address)
		}
	}
	if len(filtered) == 0 {
		return nil, nil, ErrUnreachable
	}
	target.Addrs = filtered
	transportOption := libp2p.Transport(tcptransport.NewTCPTransport)
	if r.mark != 0 {
		dialer, err := controlsocket.DialerForMark(r.mark)
		if err != nil {
			return nil, nil, err
		}
		transportOption = libp2p.Transport(tcptransport.NewTCPTransport, tcptransport.WithDialerForAddr(dialer))
	}
	owner, err := libp2p.New(
		libp2p.Identity(r.privateKey), libp2p.NoListenAddrs, libp2p.NoTransports,
		transportOption, libp2p.DisableRelay(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create fresh reachability requester: %w", err)
	}
	if err := owner.Connect(ctx, target); err != nil {
		_ = owner.Close()
		return nil, nil, ErrUnreachable
	}
	stream, err := owner.NewStream(ctx, remote, libprotocol.ID(ProtocolID))
	if err != nil {
		_ = owner.Close()
		return nil, nil, err
	}
	return stream, owner, nil
}

var _ RequestStreamOpener = (*FamilyRequester)(nil)
