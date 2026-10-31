// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package discovery

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/reachability"
	"github.com/Irviodotnet/Irvio/internal/serviceindex"
	dhtpb "github.com/libp2p/go-libp2p-kad-dht/pb"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-msgio/pbio"
)

func TestPrivateAddresslessOwnerUsesFindPeerAndAuthenticates(t *testing.T) {
	for _, name := range []string{"eligible", "missing-private-capability", "substituted-endpoint"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			bootstrap := privateDHTNode(t)
			address, err := bootstrap.IPv4LoopbackAddress()
			if err != nil {
				t.Fatal(err)
			}
			owner := privateDHTNode(t, address)
			if err := owner.UpdatePublicReachability([]reachability.Proof{privateTestProof(t, owner.PeerID())}); err != nil {
				t.Fatal(err)
			}
			if err := owner.ActivateRendezvousCapability(ctx, nil); err != nil {
				t.Fatal(err)
			}
			if name != "missing-private-capability" {
				if err := owner.SetPrivateRendezvousReady(true); err != nil {
					t.Fatal(err)
				}
			}
			locator := [32]byte{93}
			key, err := serviceindex.PrivateOwnerCID(locator[:])
			if err != nil {
				t.Fatal(err)
			}
			addresses := owner.Host().Addrs()
			if name == "substituted-endpoint" {
				addresses = bootstrap.Host().Addrs()
			}
			var providers, resolutions atomic.Int32
			var lookupPeer atomic.Value
			lookupPeer.Store(peer.ID(""))
			// Controlled DHT wire replies force an addressless hint without replacing the client lookup.
			bootstrap.Host().SetStreamHandler(dhtProtocolPrefix+"/kad/1.0.0", func(stream network.Stream) {
				defer stream.Close()
				_ = stream.SetDeadline(time.Now().Add(5 * time.Second))
				reader, writer := pbio.NewDelimitedReader(stream, 64<<10), pbio.NewDelimitedWriter(stream)
				for {
					var request dhtpb.Message
					if err := reader.ReadMsg(&request); err != nil {
						return
					}
					response := dhtpb.NewMessage(request.Type, request.Key, 0)
					switch request.Type {
					case dhtpb.Message_GET_PROVIDERS:
						if bytes.Equal(request.Key, key.Hash()) {
							if stream.Conn().RemotePeer() == lookupPeer.Load().(peer.ID) {
								providers.Add(1)
							}
							response.ProviderPeers = []*dhtpb.Message_Peer{{Id: []byte(owner.PeerID())}}
						}
					case dhtpb.Message_FIND_NODE:
						if bytes.Equal(request.Key, []byte(owner.PeerID())) && providers.Load() > 0 {
							if stream.Conn().RemotePeer() == lookupPeer.Load().(peer.ID) {
								resolutions.Add(1)
							}
							response.CloserPeers = dhtpb.RawPeerInfosToPBPeers([]peer.AddrInfo{{ID: owner.PeerID(), Addrs: addresses}})
						}
					}
					if err := writer.WriteMsg(response); err != nil {
						return
					}
				}
			})
			client := privateDHTNode(t, address)
			lookupPeer.Store(client.PeerID())
			if len(client.Host().Peerstore().Addrs(owner.PeerID())) != 0 || len(client.Host().Network().ConnsToPeer(owner.PeerID())) != 0 {
				t.Fatal("client already knew owner endpoint before addressless discovery")
			}
			owners, err := client.FindPrivateOwners(ctx, locator)
			if providers.Load() == 0 || resolutions.Load() == 0 {
				t.Fatalf("did not exercise addressless GET_PROVIDERS then FIND_NODE: providers=%d resolutions=%d err=%v", providers.Load(), resolutions.Load(), err)
			}
			if name == "eligible" {
				if err != nil || len(owners) != 1 || owners[0] != owner.PeerID() {
					t.Fatalf("resolved eligible owner lost: %v %v", owners, err)
				}
			} else if len(owners) != 0 || !errors.Is(err, ErrPrivateLookupIncomplete) {
				t.Fatalf("resolved address bypassed owner authentication/capability: %v %v", owners, err)
			}
		})
	}
}
