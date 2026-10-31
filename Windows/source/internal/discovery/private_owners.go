// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package discovery

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	irvioprotocol "github.com/Irviodotnet/Irvio/internal/protocol"
	"github.com/Irviodotnet/Irvio/internal/serviceindex"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/peer"
	"google.golang.org/protobuf/proto"
)

const PrivateRendezvousFeature = "private-rendezvous-v1"
const privateOwnerLimit = 8
const privateOwnerParallelism = 3
const privateOwnerTimeout = 10 * time.Second

var ErrPrivateLookupIncomplete = errors.New("private owner lookup incomplete")

// VerifyPrivateOwner requires current signed M11-backed capability before private registration.
func (n *Node) VerifyPrivateOwner(ctx context.Context, owner peer.ID) error {
	announcement, _, err := n.fetchAndVerify(ctx, owner)
	if err != nil {
		return err
	}
	if announcement.Capabilities&irvioprotocol.CapRendezvous == 0 ||
		announcement.Capabilities&(irvioprotocol.CapPublicControlV4|irvioprotocol.CapPublicControlV6) == 0 ||
		!containsFeature(announcement.SessionFeatures, PrivateRendezvousFeature) {
		return fmt.Errorf("private Rendezvous capability unavailable")
	}
	return nil
}

// SetPrivateRendezvousReady advertises private support only after its handler is ready.
func (n *Node) SetPrivateRendezvousReady(ready bool) error {
	n.announcementMu.Lock()
	defer n.announcementMu.Unlock()
	if ready && n.announcement.Capabilities&irvioprotocol.CapRendezvous == 0 {
		return fmt.Errorf("private Rendezvous requires validated public Rendezvous")
	}
	if containsFeature(n.announcement.SessionFeatures, PrivateRendezvousFeature) == ready {
		return nil
	}
	if n.announcement.Sequence == ^uint64(0) {
		return fmt.Errorf("local announcement sequence exhausted")
	}
	cloned := proto.Clone(n.announcement).(*irviov1.NodeAnnouncement)
	cloned.SessionFeatures = removeFeature(cloned.SessionFeatures, PrivateRendezvousFeature)
	if ready {
		cloned.SessionFeatures = append(cloned.SessionFeatures, PrivateRendezvousFeature)
	}
	now := time.Now()
	cloned.Sequence++
	cloned.CreatedAtUnix = now.Unix()
	cloned.ExpiresAtUnix = now.Add(n.announcementTTL).Unix()
	signed, err := irvioprotocol.SealAnnouncement(n.privateKey, cloned, now)
	if err != nil {
		return err
	}
	if err := n.server.SetEnvelope(signed); err != nil {
		return err
	}
	n.announcement, n.signedEnvelope = cloned, append([]byte(nil), signed...)
	return nil
}

// ProvidePrivateOwner publishes an opaque locator only while its caller owns a live registration.
func (n *Node) ProvidePrivateOwner(ctx context.Context, locator [32]byte) error {
	value, err := serviceindex.PrivateOwnerCID(locator[:])
	if err != nil {
		return err
	}
	announcement, err := irvioprotocol.VerifySignedAnnouncement(n.LocalSignedAnnouncement(), n.PeerID(), time.Now())
	if err != nil || announcement.Capabilities&irvioprotocol.CapRendezvous == 0 || !containsFeature(announcement.SessionFeatures, PrivateRendezvousFeature) {
		return fmt.Errorf("private owner publication requires current validated readiness")
	}
	ctx, cancel := context.WithTimeout(ctx, privateOwnerTimeout)
	defer cancel()
	if n.dht.RoutingTable().Size() == 0 && len(n.bootstrapPeers) > 0 {
		if err := n.EnsureRoutingPeers(ctx); err != nil {
			return err
		}
	}
	return n.dht.Provide(ctx, value, true)
}

// FindPrivateOwners authenticates bounded DHT hints; missing results never prove target absence.
func (n *Node) FindPrivateOwners(ctx context.Context, locator [32]byte) ([]peer.ID, error) {
	value, err := serviceindex.PrivateOwnerCID(locator[:])
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, privateOwnerTimeout)
	defer cancel()
	if n.dht.RoutingTable().Size() == 0 && len(n.bootstrapPeers) > 0 {
		if err := n.EnsureRoutingPeers(ctx); err != nil {
			return nil, errors.Join(ErrPrivateLookupIncomplete, err)
		}
	}
	resolved := make(chan peer.AddrInfo)
	done := make(chan error, 1)
	var incomplete atomic.Bool
	go func() {
		defer close(resolved)
		done <- processProviderResults(ctx, n.host.ID(), n.dht.FindProvidersAsync(ctx, value, privateOwnerLimit),
			n.host.Peerstore().Addrs, n.dht.FindPeer, func(info peer.AddrInfo) (bool, error) {
				select {
				case resolved <- info:
					return true, nil
				case <-ctx.Done():
					return false, ctx.Err()
				}
			})
	}()
	owners, verifyErr := verifyProviders(ctx, n.host.ID(), resolved, privateOwnerParallelism,
		func(peerCtx context.Context, info peer.AddrInfo) (bool, error) {
			if err := n.host.Connect(peerCtx, info); err != nil {
				incomplete.Store(true)
				return false, nil
			}
			announcement, _, err := n.fetchAndVerify(peerCtx, info.ID)
			if err != nil {
				incomplete.Store(true)
				return false, nil
			}
			return announcement.Capabilities&irvioprotocol.CapRendezvous != 0 &&
				announcement.Capabilities&(irvioprotocol.CapPublicControlV4|irvioprotocol.CapPublicControlV6) != 0 &&
				containsFeature(announcement.SessionFeatures, PrivateRendezvousFeature), nil
		})
	resolutionErr := <-done
	if incomplete.Load() || verifyErr != nil || resolutionErr != nil || len(owners) == 0 {
		return owners, errors.Join(ErrPrivateLookupIncomplete, verifyErr, resolutionErr)
	}
	return owners, nil
}
