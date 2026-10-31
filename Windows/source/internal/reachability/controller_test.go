// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package reachability

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type fakeAttestor struct {
	mu      sync.Mutex
	keys    map[peer.ID]crypto.PrivKey
	subject peer.ID
	now     func() time.Time
	block   <-chan struct{}
	started chan<- peer.ID
	fail    bool
	calls   int
}

func (a *fakeAttestor) Request(ctx context.Context, verifier peer.ID, family irviov1.ReachabilityFamily, port uint16, network, epoch uint64) (*irviov1.SignedReachabilityAttestation, error) {
	a.mu.Lock()
	a.calls++
	fail, block, started := a.fail, a.block, a.started
	a.mu.Unlock()
	if started != nil {
		select {
		case started <- verifier:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if fail {
		return nil, ErrUnreachable
	}
	now := a.now().UTC().Truncate(time.Second)
	id := bytes.Repeat([]byte{byte(verifier[len(verifier)-1])}, AttemptIDSize)
	return signAttestation(a.keys[verifier], &irviov1.ReachabilityAttestation{
		ProtocolVersion: ProtocolVersion, SubjectPeerId: []byte(a.subject), VerifierPeerId: []byte(verifier),
		Family: family, ObservedIp: netip.MustParseAddr("203.0.113.10").AsSlice(), ExternalControlPort: uint32(port),
		NetworkGeneration: network, PublicEpoch: epoch, AttemptId: id,
		VerifiedAtUnix: now.Unix(), ExpiresAtUnix: now.Add(time.Minute).Unix(),
	})
}

func controllerFixture(t *testing.T) (*Controller, *fakeAttestor, VerificationRequest) {
	t.Helper()
	subjectKey, _, _ := crypto.GenerateEd25519Key(nil)
	subject, _ := peer.IDFromPrivateKey(subjectKey)
	attestor := &fakeAttestor{keys: make(map[peer.ID]crypto.PrivKey), subject: subject, now: time.Now}
	request := VerificationRequest{
		Family: irviov1.ReachabilityFamily_REACHABILITY_FAMILY_IPV4, ExternalPort: 45001,
		NetworkGeneration: 1, PublicEpoch: 1,
	}
	for index := 0; index < 3; index++ {
		key, _, _ := crypto.GenerateEd25519Key(nil)
		id, _ := peer.IDFromPrivateKey(key)
		attestor.keys[id] = key
		request.Verifiers = append(request.Verifiers, id)
	}
	controller, err := NewController(ControllerConfig{Subject: subject, Attestor: attestor, RetryDelay: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = controller.Close() })
	return controller, attestor, request
}

func TestControllerQuorumNetworkInvalidationAndDisable(t *testing.T) {
	controller, _, request := controllerFixture(t)
	if err := controller.Enable(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	first := controller.Snapshot()
	if first.State != StateVerified || !first.Requested || len(first.Proof.Attestations) != DefaultQuorum {
		t.Fatalf("verified snapshot=%+v", first)
	}
	if err := controller.InvalidateNetwork(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	invalid := controller.Snapshot()
	if invalid.State != StateUnverified || !invalid.Requested || len(invalid.Proof.Attestations) != 0 || invalid.Generation <= first.Generation {
		t.Fatalf("invalidated snapshot=%+v", invalid)
	}
	if err := controller.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	disabled := controller.Snapshot()
	if disabled.State != StateDisabled || disabled.Requested || disabled.Generation <= invalid.Generation {
		t.Fatalf("disabled snapshot=%+v", disabled)
	}
}

func TestControllerSnapshotIsImmutable(t *testing.T) {
	controller, _, request := controllerFixture(t)
	if err := controller.Enable(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	first := controller.Snapshot()
	if len(first.Proof.Attestations) == 0 || len(first.Proof.Attestations[0].Signature) == 0 {
		t.Fatal("verified snapshot has no signed attestation")
	}
	original := first.Proof.Attestations[0].Signature[0]
	first.Proof.Attestations[0].Signature[0] ^= 0xff
	second := controller.Snapshot()
	if second.Proof.Attestations[0].Signature[0] != original {
		t.Fatal("caller mutation changed controller-owned proof")
	}
}

func TestControllerDisableFencesLateQuorum(t *testing.T) {
	controller, attestor, request := controllerFixture(t)
	block := make(chan struct{})
	started := make(chan peer.ID, 3)
	attestor.block, attestor.started = block, started
	done := make(chan error, 1)
	go func() { done <- controller.Enable(context.Background(), request) }()
	for index := 0; index < 3; index++ {
		<-started
	}
	if err := controller.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(block)
	if err := <-done; !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("late Enable error=%v", err)
	}
	if snapshot := controller.Snapshot(); snapshot.State != StateDisabled || snapshot.Requested {
		t.Fatalf("late result resurrected state=%+v", snapshot)
	}
}

func TestControllerSerializesProofPublicationBeforeDisable(t *testing.T) {
	controller, _, request := controllerFixture(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var mu sync.Mutex
	var publications []bool
	controller.config.Publish = func(_ context.Context, proof *Proof) error {
		if proof != nil {
			entered <- struct{}{}
			<-release
		}
		mu.Lock()
		publications = append(publications, proof != nil)
		mu.Unlock()
		return nil
	}
	enabled := make(chan error, 1)
	go func() { enabled <- controller.Enable(context.Background(), request) }()
	<-entered
	disabled := make(chan error, 1)
	go func() { disabled <- controller.Disable(context.Background()) }()
	select {
	case err := <-disabled:
		t.Fatalf("Disable overtook proof publication: %v", err)
	default:
	}
	close(release)
	if err := <-enabled; err != nil {
		t.Fatal(err)
	}
	if err := <-disabled; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(publications) != 2 || !publications[0] || publications[1] {
		t.Fatalf("publication order=%v", publications)
	}
}

func TestControllerOneDeadVerifierStillMeetsTwoOfThree(t *testing.T) {
	controller, attestor, request := controllerFixture(t)
	dead := request.Verifiers[2]
	delete(attestor.keys, dead)
	base := attestor
	controller.config.Attestor = AttestorFunc(func(ctx context.Context, verifier peer.ID, family irviov1.ReachabilityFamily, port uint16, network, epoch uint64) (*irviov1.SignedReachabilityAttestation, error) {
		if verifier == dead {
			return nil, ErrUnreachable
		}
		return base.Request(ctx, verifier, family, port, network, epoch)
	})
	if err := controller.Enable(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if got := len(controller.Snapshot().Proof.Attestations); got != 2 {
		t.Fatalf("attestations=%d", got)
	}
}

func TestControllerInsufficientQuorumRemainsUnverified(t *testing.T) {
	controller, attestor, request := controllerFixture(t)
	passing := request.Verifiers[0]
	base := attestor
	controller.config.Attestor = AttestorFunc(func(ctx context.Context, verifier peer.ID, family irviov1.ReachabilityFamily, port uint16, network, epoch uint64) (*irviov1.SignedReachabilityAttestation, error) {
		if verifier != passing {
			return nil, ErrUnreachable
		}
		return base.Request(ctx, verifier, family, port, network, epoch)
	})
	if err := controller.Enable(context.Background(), request); !errors.Is(err, ErrInsufficient) {
		t.Fatalf("Enable error=%v want insufficient quorum", err)
	}
	if snapshot := controller.Snapshot(); snapshot.State != StateUnverified || !snapshot.Requested || len(snapshot.Proof.Attestations) != 0 {
		t.Fatalf("insufficient quorum published capability: %+v", snapshot)
	}
}

func TestControllerQuorumDoesNotWaitForThirdVerifier(t *testing.T) {
	controller, attestor, request := controllerFixture(t)
	blocked := request.Verifiers[2]
	release := make(chan struct{})
	finished := make(chan struct{})
	base := attestor
	controller.config.Attestor = AttestorFunc(func(ctx context.Context, verifier peer.ID, family irviov1.ReachabilityFamily, port uint16, network, epoch uint64) (*irviov1.SignedReachabilityAttestation, error) {
		if verifier == blocked {
			defer close(finished)
			select {
			case <-release:
				return nil, ErrUnreachable
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return base.Request(ctx, verifier, family, port, network, epoch)
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err := controller.Enable(ctx, request)
	cancel()
	if err != nil {
		close(release)
		t.Fatalf("quorum waited for an unnecessary third verifier: %v", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("quorum returned before the canceled verifier worker was joined")
	}
	close(release)
	if got := len(controller.Snapshot().Proof.Attestations); got != 2 {
		t.Fatalf("attestations=%d", got)
	}
}

type AttestorFunc func(context.Context, peer.ID, irviov1.ReachabilityFamily, uint16, uint64, uint64) (*irviov1.SignedReachabilityAttestation, error)

func (f AttestorFunc) Request(ctx context.Context, remote peer.ID, family irviov1.ReachabilityFamily, port uint16, network, epoch uint64) (*irviov1.SignedReachabilityAttestation, error) {
	return f(ctx, remote, family, port, network, epoch)
}
