// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package runtime

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/discovery"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/privateaccess"
	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/peer"
)

type privateDistributorEcho struct{}

func (privateDistributorEcho) HandleSignal(_ context.Context, _ peer.ID, value []byte) ([]byte, error) {
	return append([]byte(nil), value...), nil
}

func TestPrivateDistributorRuntimeReadinessAndWithdrawal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	start := func(config discovery.Config) *discovery.Node {
		config.DataDir, config.DHTMode, config.Logger = t.TempDir(), "server", logger
		node, err := discovery.Start(ctx, config)
		must(err)
		t.Cleanup(func() { node.Close() })
		return node
	}
	first, second := start(discovery.Config{}), start(discovery.Config{})
	a, err := first.IPv4LoopbackAddress()
	must(err)
	b, err := second.IPv4LoopbackAddress()
	must(err)
	subject := start(discovery.Config{Bootstrap: []string{a, b}})
	base := PublicRoleConfig{OperationTimeout: 4 * time.Second, ProofTTL: 30 * time.Second,
		ReverifyLead: 5 * time.Second, RetryDelay: time.Second, AllowTestAddresses: true, Logger: logger}
	for _, node := range []*discovery.Node{first, second} {
		config := base
		config.Challenger = testInboundChallenge{key: node.IdentityPrivateKey()}
		role, err := newPublicRole(ctx, node, config)
		must(err)
		defer role.Close()
	}
	base.Requested, base.VerifierAddresses = true, []string{a, b}
	base.Challenger = testInboundChallenge{key: subject.IdentityPrivateKey()}
	role, err := newPublicRole(ctx, subject, base)
	must(err)
	defer role.Close()
	servicePolicy := rendezvous.DefaultConfig()
	servicePolicy.Admission.RegisterInterval = time.Millisecond
	runtime, err := newM12Runtime(ctx, subject, role, M12Config{Enabled: true, Serve: true, AllowTestAddresses: true, Policy: servicePolicy}, logger)
	must(err)
	defer runtime.Close(context.Background())
	runtime.mu.Lock()
	premature := runtime.service != nil || runtime.privateService != nil
	runtime.mu.Unlock()
	if premature {
		t.Fatal("handlers started before M11 readiness")
	}
	must(role.Enable(ctx, networkstate.State{Generation: 1, ObservedAt: time.Now()}))
	wait := func(check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal("runtime readiness transition timed out")
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
	wait(func() bool {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		return runtime.service != nil && runtime.privateService != nil
	})
	announcement := subject.LocalAnnouncement()
	found := false
	for _, feature := range announcement.SessionFeatures {
		found = found || feature == discovery.PrivateRendezvousFeature
	}
	if !found {
		t.Fatal("ready private handler not advertised")
	}
	address, err := subject.IPv4LoopbackAddress()
	must(err)
	exit := start(discovery.Config{Bootstrap: []string{address}, Exit: true, DeferExitAdvertisement: true,
		AWGPublicKey: bytes.Repeat([]byte{7}, 32), AWGKeyEpoch: 1})
	must(exit.EnableSessionEstablishment())
	must(exit.SetPrivateExitReady(true))
	if status := (&Daemon{node: exit}).Status(); status.State != StateHosting || status.ExitContributing {
		t.Fatalf("private host presented as idle or public contributor: %+v", status)
	}
	config := rendezvous.DefaultConfig()
	config.ObservedIPPolicy.AllowLoopback = true
	locator := [32]byte{41}
	registration, err := rendezvous.OpenPrivateRegistration(ctx, rendezvous.RegistrationClientOptions{
		Host: exit.Host(), Server: subject.PeerID(), Config: config, SignedDescriptor: exit.LocalSignedAnnouncement(), Handler: privateDistributorEcho{},
	}, locator)
	must(err)
	defer registration.Close(context.Background())
	client, err := rendezvous.NewClient(exit.Host(), 2*time.Second)
	must(err)
	candidate, err := client.LookupPrivate(ctx, subject.PeerID(), exit.PeerID(), locator)
	must(err)
	if candidate.Descriptor.Subject != exit.PeerID() {
		t.Fatal("private target lost")
	}
	countries, err := client.ListLocalAvailableCountries(ctx, subject.PeerID())
	must(err)
	if len(countries) != 0 {
		t.Fatal("private registration entered public country index")
	}
	runtime.mu.Lock()
	publicCount, privateCount := runtime.registry.Snapshot().Registrations, runtime.privateRegistry.Snapshot().Registrations
	runtime.mu.Unlock()
	if publicCount != 0 || privateCount != 1 {
		t.Fatalf("registries not isolated: public=%d private=%d", publicCount, privateCount)
	}
	must(registration.Close(ctx))
	exercisePrivateHostingRuntime(t, ctx, exit, subject, logger)
	must(exit.SetPrivateExitReady(true))
	registration, err = rendezvous.OpenPrivateRegistration(ctx, rendezvous.RegistrationClientOptions{
		Host: exit.Host(), Server: subject.PeerID(), Config: config, SignedDescriptor: exit.LocalSignedAnnouncement(), Handler: privateDistributorEcho{},
	}, locator)
	must(err)
	defer registration.Close(context.Background())
	must(role.Disable(ctx))
	wait(func() bool {
		runtime.mu.Lock()
		defer runtime.mu.Unlock()
		return runtime.service == nil && runtime.privateService == nil
	})
	select {
	case <-registration.Done():
	case <-ctx.Done():
		t.Fatal("private registration survived readiness loss")
	}
	for _, feature := range subject.LocalAnnouncement().SessionFeatures {
		if feature == discovery.PrivateRendezvousFeature {
			t.Fatal("private support remained advertised")
		}
	}
	if _, err := client.LookupPrivate(ctx, subject.PeerID(), exit.PeerID(), locator); err == nil {
		t.Fatal("withdrawn runtime still serves private lookup")
	}
}

func exercisePrivateHostingRuntime(t *testing.T, ctx context.Context, exit, distributor *discovery.Node, logger *slog.Logger) {
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	directory := t.TempDir()
	must(os.Chmod(directory, 0700))
	store, err := privateaccess.Open(directory, exit.PeerID())
	must(err)
	defer store.Close()
	state, err := store.Snapshot()
	must(err)
	decoded, err := hex.DecodeString(state.Locator)
	must(err)
	var locator [32]byte
	copy(locator[:], decoded)
	enrollment, err := privateaccess.NewEnrollmentServer(store, exit.IdentityPrivateKey())
	must(err)
	endpoint, err := rendezvous.NewPrivateEnrollmentEndpoint(exit.Host(), enrollment.Serve)
	must(err)
	defer endpoint.Close()
	role, err := newPublicRole(ctx, exit, PublicRoleConfig{Logger: logger, AllowTestAddresses: true, OperationTimeout: 2 * time.Second})
	must(err)
	defer role.Close()
	address, err := distributor.IPv4LoopbackAddress()
	must(err)
	runtime, err := newM12Runtime(ctx, exit, role, M12Config{Enabled: true, AllowTestAddresses: true, RendezvousPeers: []string{address}}, logger)
	must(err)
	defer runtime.Close(context.Background())
	if err := runtime.StartExitRegistrations(privateDistributorEcho{}); err == nil {
		t.Fatal("public manager started while private hosting was active")
	}
	must(runtime.StartPrivateExitRegistrations(privateDistributorEcho{}, locator, endpoint))
	if err := runtime.StartPrivateExitRegistrations(privateDistributorEcho{}, locator, endpoint); err == nil {
		t.Fatal("duplicate manager start accepted")
	}
	for {
		runtime.mu.Lock()
		active := runtime.registration.Snapshot().Active
		runtime.mu.Unlock()
		if active == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("private runtime registration never became active")
		case <-time.After(5 * time.Millisecond):
		}
	}
	friend, err := discovery.Start(ctx, discovery.Config{DataDir: t.TempDir(), Bootstrap: []string{address}, Logger: logger})
	must(err)
	defer friend.Close()
	provider, err := runtime.privateCandidates(friend.PeerID(), locator)
	must(err)
	again, err := runtime.privateCandidates(friend.PeerID(), locator)
	must(err)
	if provider != again {
		t.Fatal("private selector and connector received separate route caches")
	}
	if _, err := runtime.privateCandidates(distributor.PeerID(), locator); err == nil {
		t.Fatal("runtime changed pinned private peer")
	}
	if _, err := runtime.privateCandidates(friend.PeerID(), [32]byte{99}); err == nil {
		t.Fatal("runtime changed pinned private locator")
	}
	client, err := rendezvous.NewClient(friend.Host(), 2*time.Second)
	must(err)
	candidate, err := client.LookupPrivate(ctx, distributor.PeerID(), exit.PeerID(), locator)
	must(err)
	invite, err := store.CreateInvitation(time.Now(), time.Hour)
	must(err)
	token := privateaccess.InvitationToken{Host: exit.PeerID(), Locator: locator, Invitation: invite}
	conn, err := client.OpenPrivateEnrollment(ctx, exit.PeerID(), locator, candidate.Routes[0])
	must(err)
	status, err := privateaccess.Enroll(ctx, conn, friend.IdentityPrivateKey(), token, false)
	must(err)
	if status != irviov1.PrivateEnrollmentResponse_PENDING {
		t.Fatal("enrollment bypassed explicit approval")
	}
	must(store.Approve(invite.ID, "friend", time.Now()))
	conn, err = client.OpenPrivateEnrollment(ctx, exit.PeerID(), locator, candidate.Routes[0])
	must(err)
	status, err = privateaccess.Enroll(ctx, conn, friend.IdentityPrivateKey(), token, true)
	must(err)
	if status != irviov1.PrivateEnrollmentResponse_APPROVED {
		t.Fatal("approved enrollment not observable")
	}
	must(runtime.stopExitRegistrations(ctx))
	if exit.PrivateExitActive() {
		t.Fatal("private advertisement survived stop")
	}
	if err := endpoint.SetOwners([]peer.ID{distributor.PeerID()}); err == nil {
		t.Fatal("enrollment endpoint survived stop")
	}
	withdrawal, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		_, err := client.LookupPrivate(withdrawal, distributor.PeerID(), exit.PeerID(), locator)
		if errors.Is(err, rendezvous.ErrNotFound) {
			break
		}
		if err != nil {
			t.Fatalf("withdrawal lookup failed without confirming absence: %v", err)
		}
		select {
		case <-withdrawal.Done():
			t.Fatal("private registration survived bounded withdrawal")
		case <-time.After(25 * time.Millisecond):
		}
	}
	must(runtime.stopExitRegistrations(ctx))
	if err := runtime.StartPrivateExitRegistrations(privateDistributorEcho{}, locator, endpoint); err == nil {
		t.Fatal("private manager restarted after readiness withdrawal")
	}
}
