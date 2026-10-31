// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
)

type privateOwnerAuthorizeFunc func(context.Context, peer.ID) error

func (f privateOwnerAuthorizeFunc) VerifyPrivateOwner(ctx context.Context, id peer.ID) error {
	return f(ctx, id)
}

func TestPrivateRegistrationManagerOwnsEnrollmentAdmission(t *testing.T) {
	server, key := newRendezvousHost(t)
	exit, exitKey := newRendezvousHost(t)
	connectRendezvousHosts(t, exit, server)
	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	options := RegistryOptions{Config: config, ServerPeer: server.ID(), ServerIncarnation: 1, Signer: key, Resolver: fixedCountryResolver{}, ControlAddresses: []string{hostControlAddress(t, server)}}
	publicRegistry, err := NewRegistry(options)
	privateMust(t, err)
	publicService, err := NewService(ServiceOptions{Host: server, Registry: publicRegistry})
	privateMust(t, err)
	defer publicService.Close()
	registry, err := NewPrivateRegistry(options)
	privateMust(t, err)
	service, err := NewPrivateService(ServiceOptions{Host: server, Registry: registry})
	privateMust(t, err)
	defer service.Close()
	endpoint, err := NewPrivateEnrollmentEndpoint(exit, func(context.Context, net.Conn) error { return nil })
	privateMust(t, err)
	defer endpoint.Close()
	var allowed atomic.Bool
	var checks atomic.Int32
	authorize := privateOwnerAuthorizeFunc(func(_ context.Context, id peer.ID) error {
		checks.Add(1)
		if id != server.ID() || !allowed.Load() {
			return ErrUnauthorized
		}
		return nil
	})
	signed := signedPrivateExit(t, exitKey, exit.ID(), time.Now(), 1)
	manager, err := NewPrivateRegistrationManager(RegistrationManagerOptions{Host: exit, Servers: partialServerProvider{[]peer.ID{server.ID()}}, SignedDescriptor: func() []byte { return signed }, Handler: &echoSignalHandler{}, Config: config, OperationTimeout: time.Second}, [32]byte{31}, endpoint, authorize)
	privateMust(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer manager.Close(ctx)
	privateMust(t, manager.Start(ctx))
	wait := func(ready func() bool) {
		for !ready() {
			select {
			case <-ctx.Done():
				t.Fatal("manager transition timed out")
			case <-time.After(time.Millisecond):
			}
		}
	}
	wait(func() bool { return checks.Load() > 0 })
	if manager.Snapshot().Active != 0 || registry.Snapshot().Registrations != 0 {
		t.Fatal("unverified owner received registration")
	}
	allowed.Store(true)
	manager.DescriptorChanged()
	wait(func() bool { return manager.Snapshot().Active == 1 })
	endpoint.mu.Lock()
	_, admitted := endpoint.owners[server.ID()]
	endpoint.mu.Unlock()
	if !admitted || publicRegistry.Snapshot().Registrations != 0 {
		t.Fatal("private admission missing or public fallback occurred")
	}
	privateMust(t, service.Close())
	wait(func() bool {
		endpoint.mu.Lock()
		defer endpoint.mu.Unlock()
		return len(endpoint.owners) == 0
	})
	privateMust(t, manager.Close(ctx))
	if manager.Snapshot().Active != 0 || !manager.Snapshot().Closed {
		t.Fatal("manager did not fully close")
	}
}
