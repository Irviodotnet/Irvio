// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package tunnelsignal

import (
	"context"
	"encoding/binary"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func TestAuthenticatedLeaseAcquireAndRelease(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exit, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer exit.Close()
	clientHost, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer clientHost.Close()
	clientHost.Peerstore().AddAddrs(exit.ID(), exit.Addrs(), time.Minute)
	if err := clientHost.Connect(ctx, peer.AddrInfo{ID: exit.ID(), Addrs: exit.Addrs()}); err != nil {
		t.Fatal(err)
	}
	config := tunnellease.DefaultConfig(exit.ID())
	config.IPv4Pool = netip.MustParsePrefix("10.88.0.0/29")
	config.IPv6Pool = netip.MustParsePrefix("fd88::/124")
	allocator, err := tunnellease.New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer allocator.Close()
	service, err := NewService(exit, allocator, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	client, err := NewClient(clientHost, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := client.Acquire(ctx, exit.ID())
	if err != nil {
		t.Fatal(err)
	}
	if lease.ExitPeer != exit.ID() || lease.ClientPeer != clientHost.ID() || !lease.IPv4.IsValid() || !lease.IPv6.IsValid() || lease.Generation != 1 {
		t.Fatalf("invalid authenticated lease: %+v", lease)
	}
	renewed, err := client.Renew(ctx, lease)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.ID != lease.ID || renewed.IPv4 != lease.IPv4 || renewed.IPv6 != lease.IPv6 || renewed.Generation != 2 {
		t.Fatalf("invalid renewal: before=%+v after=%+v", lease, renewed)
	}
	lease = renewed
	superseding, err := client.Acquire(ctx, exit.ID())
	if err != nil {
		t.Fatalf("new authenticated session could not supersede stale ownership: %v", err)
	}
	if superseding != lease || len(allocator.Active()) != 1 {
		t.Fatal("same PeerID reconnect created duplicate lease state")
	}
	if err := client.Release(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if active := allocator.Active(); len(active) != 0 {
		t.Fatalf("active leases after release = %d", len(active))
	}
	if err := client.Release(ctx, lease); !errors.Is(err, ErrRemote) {
		t.Fatalf("replayed release error = %v", err)
	}
}

func TestAuthenticatedThreeClientLeasesRemainIsolated(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exit, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer exit.Close()
	config := tunnellease.DefaultConfig(exit.ID())
	config.IPv4Pool = netip.MustParsePrefix("10.89.0.0/29")
	config.IPv6Pool = netip.MustParsePrefix("fd89::/124")
	allocator, err := tunnellease.New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer allocator.Close()
	service, err := NewService(exit, allocator, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	hosts := make([]peer.ID, 0, 3)
	leases := make([]tunnellease.Lease, 0, 3)
	clients := make([]*Client, 0, 3)
	for range 3 {
		clientHost, hostErr := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
		if hostErr != nil {
			t.Fatal(hostErr)
		}
		t.Cleanup(func() { _ = clientHost.Close() })
		clientHost.Peerstore().AddAddrs(exit.ID(), exit.Addrs(), time.Minute)
		if hostErr := clientHost.Connect(ctx, peer.AddrInfo{ID: exit.ID(), Addrs: exit.Addrs()}); hostErr != nil {
			t.Fatal(hostErr)
		}
		client, clientErr := NewClient(clientHost, time.Second)
		if clientErr != nil {
			t.Fatal(clientErr)
		}
		lease, leaseErr := client.Acquire(ctx, exit.ID())
		if leaseErr != nil {
			t.Fatal(leaseErr)
		}
		hosts = append(hosts, clientHost.ID())
		clients = append(clients, client)
		leases = append(leases, lease)
	}
	for left := range leases {
		if leases[left].ClientPeer != hosts[left] {
			t.Fatalf("lease %d was bound to another authenticated peer", left)
		}
		for right := left + 1; right < len(leases); right++ {
			if leases[left].ID == leases[right].ID || leases[left].IPv4 == leases[right].IPv4 || leases[left].IPv6 == leases[right].IPv6 {
				t.Fatalf("leases %d and %d overlap", left, right)
			}
		}
	}
	if err := clients[0].Release(ctx, leases[0]); err != nil {
		t.Fatal(err)
	}
	active := allocator.Active()
	if len(active) != 2 {
		t.Fatalf("active leases after one client closed = %d", len(active))
	}
	for _, lease := range active {
		if lease.ClientPeer == hosts[0] {
			t.Fatal("released client lease remained active")
		}
	}
}

func TestRequestIdentityBindingAndFramingBounds(t *testing.T) {
	message := &irviov1.TunnelLeaseRequest{
		ProtocolVersion: ProtocolVersion,
		Operation:       irviov1.TunnelLeaseOperation_TUNNEL_LEASE_OPERATION_ACQUIRE,
		ExitPeerId:      []byte(peer.ID("exit")), ClientPeerId: []byte(peer.ID("forged")),
	}
	if _, _, _, err := validateRequest(message, peer.ID("actual"), peer.ID("exit")); !errors.Is(err, ErrWrongPeer) {
		t.Fatalf("forged client error = %v", err)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], MaxFrameSize+1)
	if err := readFrame(&fixedReader{payload: header[:]}, new(irviov1.TunnelLeaseRequest)); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("oversized frame error = %v", err)
	}
}

type fixedReader struct{ payload []byte }

func (r *fixedReader) Read(destination []byte) (int, error) {
	if len(r.payload) == 0 {
		return 0, errors.New("end")
	}
	n := copy(destination, r.payload)
	r.payload = r.payload[n:]
	return n, nil
}

func TestLifecycleProtocolFencesSessionTokenAndSupersedesSafely(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exit, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer exit.Close()
	clientHost, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatal(err)
	}
	defer clientHost.Close()
	clientHost.Peerstore().AddAddrs(exit.ID(), exit.Addrs(), time.Minute)
	if err := clientHost.Connect(ctx, peer.AddrInfo{ID: exit.ID(), Addrs: exit.Addrs()}); err != nil {
		t.Fatal(err)
	}
	config := tunnellease.DefaultConfig(exit.ID())
	config.IPv4Pool = netip.MustParsePrefix("10.90.0.0/29")
	config.TTL = time.Minute
	allocator, err := tunnellease.New(config)
	if err != nil {
		t.Fatal(err)
	}
	var activated, revoked int
	lifecycleConfig := tunnellease.DefaultLifecycleConfig(allocator, tunnellease.ResourceRevokerFunc(
		func(context.Context, tunnellease.Lease, tunnellease.RevocationReason) error {
			revoked++
			return nil
		},
	))
	lifecycleConfig.Activator = tunnellease.ResourceActivatorFunc(func(context.Context, tunnellease.Lease) error {
		activated++
		return nil
	})
	lifecycleConfig.SweepInterval = time.Hour
	lifecycle, err := tunnellease.NewLifecycle(lifecycleConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer lifecycle.Close(context.Background())
	service, err := NewLifecycleService(exit, lifecycle, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	client, err := NewClient(clientHost, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	firstToken := tunnellease.SessionToken{1}
	first, err := client.AcquireForSession(ctx, exit.ID(), firstToken)
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := client.Renew(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	secondToken := tunnellease.SessionToken{2}
	second, err := client.AcquireForSession(ctx, exit.ID(), secondToken)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.IPv4 != first.IPv4 || second.Generation != renewed.Generation+1 || second.Session != secondToken {
		t.Fatalf("invalid session supersession: first=%+v renewed=%+v second=%+v", first, renewed, second)
	}
	if _, err := client.Renew(ctx, renewed); !remoteCode(err, irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_STALE) {
		t.Fatalf("old session renewal error=%v", err)
	}
	if err := client.Release(ctx, renewed); !remoteCode(err, irviov1.TunnelLeaseErrorCode_TUNNEL_LEASE_ERROR_CODE_STALE) {
		t.Fatalf("old session release error=%v", err)
	}
	if err := client.Release(ctx, second); err != nil {
		t.Fatal(err)
	}
	if activated != 2 || revoked != 2 || len(allocator.Active()) != 0 {
		t.Fatalf("activated=%d revoked=%d active=%d", activated, revoked, len(allocator.Active()))
	}
}

func remoteCode(err error, code irviov1.TunnelLeaseErrorCode) bool {
	var remote *RemoteError
	return errors.As(err, &remote) && remote.Code == code
}
