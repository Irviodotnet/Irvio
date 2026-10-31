// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	"github.com/Irviodotnet/Irvio/internal/productprofile"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
)

func TestPrivateEnrollmentThroughRegisteredOwner(t *testing.T) {
	owner, ownerKey := newRendezvousHost(t)
	exit, exitKey := newRendezvousHost(t)
	clientHost, clientKey := newRendezvousHost(t)
	connectRendezvousHosts(t, exit, owner)
	connectRendezvousHosts(t, clientHost, owner)
	dir := filepath.Join(t.TempDir(), "profile")
	_, err := productprofile.Open(dir)
	privateMust(t, err)
	store, err := privateaccess.Open(dir, exit.ID())
	privateMust(t, err)
	defer store.Close()
	state, err := store.Snapshot()
	privateMust(t, err)
	locatorBytes, err := hex.DecodeString(state.Locator)
	privateMust(t, err)
	var locator [32]byte
	copy(locator[:], locatorBytes)
	invite, err := store.CreateInvitation(time.Now(), time.Hour)
	privateMust(t, err)
	token := privateaccess.InvitationToken{Host: exit.ID(), Locator: locator, Invitation: invite}
	endpoint, err := privateaccess.NewEnrollmentServer(store, exitKey)
	privateMust(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	managed, err := NewPrivateEnrollmentEndpoint(exit, endpoint.Serve)
	privateMust(t, err)
	defer managed.Close()
	privateMust(t, managed.SetOwners([]peer.ID{owner.ID()}))
	config := DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	config.Admission.GETInterval = time.Millisecond
	config.Admission.SignalInterval = time.Millisecond
	registry, err := NewPrivateRegistry(RegistryOptions{Config: config, ServerPeer: owner.ID(), ServerIncarnation: 1, Signer: ownerKey, Resolver: fixedCountryResolver{}, ControlAddresses: []string{hostControlAddress(t, owner)}})
	privateMust(t, err)
	service, err := NewPrivateService(ServiceOptions{Host: owner, Registry: registry})
	privateMust(t, err)
	defer service.Close()
	registration, err := OpenPrivateRegistration(ctx, RegistrationClientOptions{Host: exit, Server: owner.ID(), Config: config, SignedDescriptor: signedPrivateExit(t, exitKey, exit.ID(), time.Now(), 1), Handler: &echoSignalHandler{}}, locator)
	privateMust(t, err)
	defer registration.Close(ctx)
	client, err := NewClient(clientHost, time.Second)
	privateMust(t, err)
	candidate, err := client.LookupPrivate(ctx, owner.ID(), exit.ID(), locator)
	privateMust(t, err)
	for _, poll := range []bool{false, true} {
		conn, err := client.OpenPrivateEnrollment(ctx, exit.ID(), locator, candidate.Routes[0])
		privateMust(t, err)
		status, err := privateaccess.Enroll(ctx, conn, clientKey, token, poll)
		privateMust(t, err)
		if status != irviov1.PrivateEnrollmentResponse_PENDING {
			t.Fatal("relayed enrollment bypassed approval")
		}
	}
	state, err = store.Snapshot()
	privateMust(t, err)
	if state.Invitations[0].Client != clientHost.ID().String() || state.Authorized(owner.ID()) {
		t.Fatal("broker identity substituted for end client")
	}
	privateMust(t, store.Approve(invite.ID, "trusted client", time.Now()))
	conn, err := client.OpenPrivateEnrollment(ctx, exit.ID(), locator, candidate.Routes[0])
	privateMust(t, err)
	status, err := privateaccess.Enroll(ctx, conn, clientKey, token, true)
	privateMust(t, err)
	if status != irviov1.PrivateEnrollmentResponse_APPROVED {
		t.Fatal("relayed approval missing")
	}
	clientDir := filepath.Join(t.TempDir(), "profile")
	_, err = productprofile.Open(clientDir)
	privateMust(t, err)
	clientStore, err := privateaccess.Open(clientDir, clientHost.ID())
	privateMust(t, err)
	defer clientStore.Close()
	second, err := store.CreateInvitation(time.Now(), time.Hour)
	privateMust(t, err)
	secondToken := privateaccess.InvitationToken{Host: exit.ID(), Locator: locator, Invitation: second}
	link, err := privateaccess.EncodeInvitation(exitKey, secondToken, time.Now())
	privateMust(t, err)
	privateMust(t, clientStore.ImportInvitation(link, "private host", time.Now()))
	unavailable, _ := newRendezvousHost(t)
	connectRendezvousHosts(t, clientHost, unavailable)
	dial, err := client.PrivateEnrollmentDialer(privateFinderFunc(func(context.Context, [32]byte) ([]peer.ID, error) {
		return []peer.ID{unavailable.ID(), owner.ID()}, nil
	}))
	privateMust(t, err)
	status, err = clientStore.ResumeEnrollment(ctx, exit.ID(), clientKey, dial)
	privateMust(t, err)
	if status != irviov1.PrivateEnrollmentResponse_PENDING {
		t.Fatal("discovered enrollment skipped approval")
	}
	privateMust(t, store.Approve(second.ID, "trusted client", time.Now()))
	status, err = clientStore.ResumeEnrollment(ctx, exit.ID(), clientKey, dial)
	privateMust(t, err)
	if status != irviov1.PrivateEnrollmentResponse_APPROVED {
		t.Fatal("discovered enrollment failed")
	}
	clientState, err := clientStore.Snapshot()
	privateMust(t, err)
	if len(clientState.Targets) != 1 || clientState.Targets[0].PeerID != exit.ID().String() || len(clientState.Enrollments) != 0 {
		t.Fatal("discovered approved target not saved")
	}
	if conn, err := client.OpenPrivateEnrollment(ctx, exit.ID(), [32]byte{99}, candidate.Routes[0]); err == nil {
		conn.Close()
		t.Fatal("wrong locator opened relay")
	}
	route := candidate.Routes[0]
	route.RegistrationID[0] ^= 1
	if conn, err := client.OpenPrivateEnrollment(ctx, exit.ID(), locator, route); err == nil {
		conn.Close()
		t.Fatal("stale registration route opened relay")
	}
	privateMust(t, managed.Close())
	received := make(chan int64, 1)
	exit.SetStreamHandler(libprotocol.ID(PrivateEnrollmentEndpointProtocol), func(stream network.Stream) {
		defer stream.Close()
		n, _ := io.Copy(io.Discard, stream)
		received <- n
	})
	bounded, err := client.OpenPrivateEnrollment(ctx, exit.ID(), locator, candidate.Routes[0])
	privateMust(t, err)
	_, _ = bounded.Write(bytes.Repeat([]byte{1}, privateEnrollmentByteLimit+1024))
	select {
	case n := <-received:
		if n > privateEnrollmentByteLimit {
			t.Fatal("relay forwarded beyond enrollment byte budget")
		}
	case <-ctx.Done():
		t.Fatal("oversized relay did not terminate")
	}
	bounded.Close()
}

func TestPrivateEnrollmentSetupDeadlineDoesNotCancelEstablishedCarrier(t *testing.T) {
	owner, _ := newRendezvousHost(t)
	clientHost, _ := newRendezvousHost(t)
	_, target := testIdentity(t)
	connectRendezvousHosts(t, clientHost, owner)
	client, err := NewClient(clientHost, time.Second)
	privateMust(t, err)
	route := OwnerRoute{ServerPeer: owner.ID(), ServerIncarnation: 1, RegistrationID: RegistrationID{1}, ControlAddresses: []string{hostControlAddress(t, owner)}}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	owner.SetStreamHandler(libprotocol.ID(PrivateEnrollmentRelayProtocol), func(stream network.Stream) {
		defer stream.Close()
		_, _ = io.Copy(io.Discard, stream)
	})
	started := time.Now()
	if conn, err := client.openPrivateEnrollment(ctx, target, [32]byte{1}, route, 30*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("stalled setup exceeded its budget")
	}
	if time.Since(started) > time.Second {
		t.Fatal("route setup ignored bounded timeout")
	}
	owner.SetStreamHandler(libprotocol.ID(PrivateEnrollmentRelayProtocol), func(stream network.Stream) {
		defer stream.Close()
		request := new(irviov1.PrivateEnrollmentRelayRequest)
		if readProtoFrameLimit(stream, request, PrivateMaxFrameSize) != nil {
			return
		}
		if _, err := stream.Write([]byte{1}); err != nil {
			return
		}
		var probe [1]byte
		if _, err := io.ReadFull(stream, probe[:]); err == nil {
			_, _ = stream.Write(probe[:])
		}
	})
	conn, err := client.openPrivateEnrollment(ctx, target, [32]byte{1}, route, 100*time.Millisecond)
	privateMust(t, err)
	defer conn.Close()
	time.Sleep(150 * time.Millisecond)
	_, err = conn.Write([]byte{7})
	privateMust(t, err)
	var response [1]byte
	_, err = io.ReadFull(conn, response[:])
	privateMust(t, err)
	if response[0] != 7 {
		t.Fatal("carrier corrupted after setup deadline")
	}
}
