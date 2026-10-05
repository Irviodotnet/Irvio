// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package reachability

import (
	"bytes"
	"context"
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"hydra.local/hydra/internal/controlsocket"
	irviov1 "hydra.local/hydra/proto/hydra/v1"
)

type FreshDialer struct {
	privateKey crypto.PrivKey
	mark       uint32
}

func NewFreshDialer(privateKey crypto.PrivKey, mark uint32) (*FreshDialer, error) {
	if privateKey == nil || mark == 0 {
		return nil, ErrInvalidConfig
	}
	return &FreshDialer{privateKey: privateKey, mark: mark}, nil
}

func (d *FreshDialer) Challenge(ctx context.Context, target peer.AddrInfo, challenge *irviov1.ReachabilityChallenge) error {
	if ctx == nil || target.ID == "" || len(target.Addrs) == 0 || challenge == nil {
		return ErrInvalidConfig
	}
	dialer, err := controlsocket.DialerForMark(d.mark)
	if err != nil {
		return err
	}
	owner, err := libp2p.New(
		libp2p.Identity(d.privateKey), libp2p.NoListenAddrs, libp2p.NoTransports,
		libp2p.Transport(tcptransport.NewTCPTransport, tcptransport.WithDialerForAddr(dialer)),
		libp2p.DisableRelay(),
	)
	if err != nil {
		return err
	}
	defer owner.Close()
	if err := owner.Connect(ctx, target); err != nil {
		return ErrUnreachable
	}
	connections := owner.Network().ConnsToPeer(target.ID)
	if len(connections) != 1 || connections[0].RemotePeer() != target.ID {
		return ErrIdentityMismatch
	}
	stream, err := owner.NewStream(ctx, target.ID, ChallengeProtocolID)
	if err != nil {
		return ErrUnreachable
	}
	defer stream.Close()
	stop := context.AfterFunc(ctx, func() { _ = stream.Reset() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = stream.SetDeadline(deadline)
	} else {
		_ = stream.SetDeadline(time.Now().Add(DefaultAttemptTTL))
	}
	if err := writeFrame(stream, challenge); err != nil {
		return err
	}
	response := new(irviov1.ReachabilityChallengeResponse)
	if err := readFrame(stream, response); err != nil {
		return ErrUnreachable
	}
	if response.ProtocolVersion != ProtocolVersion || !bytes.Equal(response.AttemptId, challenge.AttemptId) ||
		!bytes.Equal(response.Nonce, challenge.Nonce) {
		return ErrIdentityMismatch
	}
	return nil
}

var _ FreshChallenger = (*FreshDialer)(nil)
