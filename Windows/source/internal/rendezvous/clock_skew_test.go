// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"bytes"
	"context"
	"testing"
	"time"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func TestPrivateBrokerClockSkewAndLocalTimeout(t *testing.T) {
	for _, offset := range []time.Duration{-2 * time.Second, 2 * time.Second} {
		t.Run(offset.String(), func(t *testing.T) {
			server, key := newRendezvousHost(t)
			exit, exitKey := newRendezvousHost(t)
			caller, _ := newRendezvousHost(t)
			connectRendezvousHosts(t, exit, server)
			connectRendezvousHosts(t, caller, server)
			config := DefaultConfig()
			config.ObservedIPPolicy.AllowLoopback = true
			config.Now = func() time.Time { return time.Now().Add(offset) }
			registry, err := NewPrivateRegistry(RegistryOptions{Config: config, ServerPeer: server.ID(), ServerIncarnation: 1,
				Signer: key, Resolver: fixedCountryResolver{}, ControlAddresses: []string{hostControlAddress(t, server)}})
			privateMust(t, err)
			service, err := NewPrivateService(ServiceOptions{Host: server, Registry: registry})
			privateMust(t, err)
			defer service.Close()
			exitConfig := config
			exitConfig.Now = func() time.Time { return time.Now().Add(-offset) }
			handler := &echoSignalHandler{}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			registration, err := OpenPrivateRegistration(ctx, RegistrationClientOptions{Host: exit, Server: server.ID(),
				SignedDescriptor: signedPrivateExit(t, exitKey, exit.ID(), time.Now(), 1), Config: exitConfig, Handler: handler}, [32]byte{1})
			privateMust(t, err)
			defer registration.Close(ctx)
			client, err := NewClient(caller, time.Second)
			privateMust(t, err)
			candidate, err := client.LookupPrivate(ctx, server.ID(), exit.ID(), [32]byte{1})
			privateMust(t, err)
			wire, err := PrivateCandidateToProto(candidate)
			privateMust(t, err)
			request, err := NewRequestID(nil)
			privateMust(t, err)
			signal := &irviov1.RendezvousSignal{ProtocolVersion: ProtocolVersion, RequestHandle: request[:],
				TargetPeerId: []byte(exit.ID()), OwnerRoute: wire.OwnerRoutes[0], SignedMessage: []byte("opaque"),
				DeadlineUnixMilli: time.Now().Add(1500 * time.Millisecond).UnixMilli()}
			response, err := client.SignalPrivate(ctx, candidate.Routes[0], signal)
			privateMust(t, err)
			if !bytes.Equal(response.SignedMessage, []byte("response:opaque")) {
				t.Fatal("owner channel not reached")
			}
			handler.delay.Store(int64(2 * time.Second))
			request, err = NewRequestID(nil)
			privateMust(t, err)
			signal.RequestHandle = request[:]
			signal.DeadlineUnixMilli = time.Now().Add(1500 * time.Millisecond).UnixMilli()
			bounded, stop := context.WithTimeout(ctx, 100*time.Millisecond)
			defer stop()
			started := time.Now()
			if _, err := client.SignalPrivate(bounded, candidate.Routes[0], signal); err == nil {
				t.Fatal("local timeout ignored")
			}
			if time.Since(started) > time.Second {
				t.Fatal("clock tolerance extended caller budget")
			}
		})
	}
}
