// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessionestablish

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func transcriptIdentity(t *testing.T) (crypto.PrivKey, peer.ID) {
	t.Helper()
	key, _, err := crypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, id
}

func preparePayload(client, exit peer.ID, now time.Time, runtime uint64) *irviov1.SessionEstablishPayload {
	return &irviov1.SessionEstablishPayload{
		ProtocolVersion: ProtocolVersion,
		Kind:            irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_PREPARE,
		SenderPeerId:    []byte(client), RecipientPeerId: []byte(exit),
		AttemptId:          bytes.Repeat([]byte{0x21}, AttemptIDSize),
		OwnerToken:         bytes.Repeat([]byte{0x22}, OwnerTokenSize),
		CreatedAtUnixMilli: now.UnixMilli(), ExpiresAtUnixMilli: now.Add(time.Second).UnixMilli(),
		Body: &irviov1.SessionEstablishPayload_Prepare{Prepare: &irviov1.SessionPrepare{
			CountryIntent: "DE", DescriptorId: bytes.Repeat([]byte{0x23}, HashSize),
			DescriptorIncarnation: 4, DescriptorSequence: 5,
			Purpose:           irviov1.IceSessionPurpose_ICE_SESSION_PURPOSE_EXIT,
			NetworkGeneration: 7, RuntimeGeneration: runtime,
		}},
	}
}

func TestSignedTranscriptAuthenticatesExactBytesAndChain(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	clientKey, client := transcriptIdentity(t)
	exitKey, exit := transcriptIdentity(t)
	policy := DefaultPolicy()
	prepareWire, err := Seal(clientKey, preparePayload(client, exit, now, 9), now, policy)
	if err != nil {
		t.Fatal(err)
	}
	prepare, err := Verify(prepareWire, client, exit, now, policy)
	if err != nil {
		t.Fatal(err)
	}

	readyPayload := &irviov1.SessionEstablishPayload{
		ProtocolVersion: ProtocolVersion,
		Kind:            irviov1.SessionEstablishKind_SESSION_ESTABLISH_KIND_READY,
		SenderPeerId:    []byte(exit), RecipientPeerId: []byte(client),
		AttemptId:          append([]byte(nil), prepare.Payload.AttemptId...),
		OwnerToken:         append([]byte(nil), prepare.Payload.OwnerToken...),
		PredecessorHash:    append([]byte(nil), prepare.Hash[:]...),
		CreatedAtUnixMilli: now.UnixMilli(), ExpiresAtUnixMilli: now.Add(time.Second).UnixMilli(),
		Body: &irviov1.SessionEstablishPayload_Ready{Ready: &irviov1.SessionReady{
			PrepareHash:        append([]byte(nil), prepare.Hash[:]...),
			SignedAnnouncement: []byte("signed-exit-descriptor"),
			ExitChallenge:      bytes.Repeat([]byte{0x31}, ChallengeSize),
			NetworkGeneration:  8, AwgKeyEpoch: 2,
		}},
	}
	readyWire, err := Seal(exitKey, readyPayload, now, policy)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := Verify(readyWire, exit, client, now, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := RequirePredecessor(ready, prepare); err != nil {
		t.Fatal(err)
	}

	mutated := append([]byte(nil), readyWire...)
	mutated[len(mutated)-1] ^= 1
	if _, err := Verify(mutated, exit, client, now, policy); err == nil {
		t.Fatal("mutated transcript accepted")
	}
	if _, err := Verify(readyWire, client, client, now, policy); !errors.Is(err, ErrWrongPeer) {
		t.Fatalf("wrong signer error=%v", err)
	}
}

func TestReplayGuardCoalescesExactDuplicateAndRejectsMutation(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	clientKey, client := transcriptIdentity(t)
	_, exit := transcriptIdentity(t)
	policy := DefaultPolicy()
	wire, err := Seal(clientKey, preparePayload(client, exit, now, 1), now, policy)
	if err != nil {
		t.Fatal(err)
	}
	message, err := Verify(wire, client, exit, now, policy)
	if err != nil {
		t.Fatal(err)
	}
	guard, _ := NewReplayGuard(4, time.Minute, func() time.Time { return now })
	var calls atomic.Uint64
	started := make(chan struct{})
	release := make(chan struct{})
	operation := func(context.Context) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []byte("same-response"), nil
	}
	var wait sync.WaitGroup
	results := make(chan []byte, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, err := guard.Execute(context.Background(), message, operation)
			if err != nil {
				t.Error(err)
				return
			}
			results <- value
		}()
	}
	<-started
	close(release)
	wait.Wait()
	close(results)
	if calls.Load() != 1 {
		t.Fatalf("operation calls=%d", calls.Load())
	}
	for result := range results {
		if string(result) != "same-response" {
			t.Fatalf("response=%q", result)
		}
	}

	changedWire, err := Seal(clientKey, preparePayload(client, exit, now, 2), now, policy)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := Verify(changedWire, client, exit, now, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Execute(context.Background(), changed, operation); !errors.Is(err, ErrConflict) {
		t.Fatalf("altered duplicate error=%v", err)
	}
}
