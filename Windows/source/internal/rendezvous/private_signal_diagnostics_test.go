// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func TestPrivateSignalDiagnosticsPreserveRejection(t *testing.T) {
	_, server := testIdentity(t)
	id, registration := RequestID{1}, RegistrationID{1}
	owner := OwnerRoute{ServerPeer: server, ServerIncarnation: 1, RegistrationID: registration}
	signal := &irviov1.RendezvousSignal{
		ProtocolVersion: ProtocolVersion, RequestHandle: id[:], TargetPeerId: []byte(server),
		SignedMessage: []byte("sensitive-test-payload"), DeadlineUnixMilli: time.Now().Add(-time.Minute).UnixMilli(),
		OwnerRoute: &irviov1.OwnerRoute{ServerPeerId: []byte(server), ServerIncarnation: 1, RegistrationId: registration[:],
			ControlAddresses: []string{"/ip4/203.0.113.1/tcp/4000/p2p/" + server.String()}},
	}
	client := &Client{}
	_, err := client.SignalPrivate(context.Background(), owner, signal)
	if !errors.Is(err, ErrInvalid) || errors.Is(err, ErrTimeout) || !strings.Contains(err.Error(), "private signal validation") || !strings.Contains(err.Error(), ErrTimeout.Error()) {
		t.Fatalf("validation diagnostic changed rejection classification: %v", err)
	}
	signal.DeadlineUnixMilli = time.Now().Add(time.Second).UnixMilli()
	signal.Response = true
	_, err = client.SignalPrivate(context.Background(), owner, signal)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "private signal owner mismatch") || strings.Contains(err.Error(), string(signal.SignedMessage)) {
		t.Fatalf("owner diagnostic changed rejection or disclosed payload: %v", err)
	}
	if _, err := client.SignalPrivate(context.Background(), owner, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil signal no longer rejected: %v", err)
	}
}

func TestPrivateSignalBrokerDiagnosticBounded(t *testing.T) {
	for _, reason := range []string{"invalid signal", "invalid target", "secret\nforged-log", strings.Repeat("x", MaxErrorMessageBytes+1)} {
		failure := &irviov1.RendezvousError{ProtocolVersion: ProtocolVersion,
			Code: irviov1.RendezvousErrorCode_RENDEZVOUS_ERROR_CODE_INVALID_REQUEST, Message: reason}
		err := privateSignalBrokerError(failure)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("classification changed: %v", err)
		}
		want := "unspecified"
		if reason == "invalid signal" || reason == "invalid target" {
			want = reason
		}
		if err.Error() != "private signal broker response ("+want+"): "+ErrInvalid.Error() {
			t.Fatalf("unexpected diagnostic: %v", err)
		}
	}
	if !errors.Is(privateSignalBrokerError(nil), ErrInvalid) {
		t.Fatal("nil response classification changed")
	}
}

func TestSignalDeadlineClockSkew(t *testing.T) {
	_, server := testIdentity(t)
	id, registration := RequestID{1}, RegistrationID{1}
	now := time.Now().Truncate(time.Millisecond)
	for _, offset := range []time.Duration{-2 * time.Second, 0, 2 * time.Second} {
		signal := &irviov1.RendezvousSignal{
			ProtocolVersion: ProtocolVersion, RequestHandle: id[:], TargetPeerId: []byte(server),
			SignedMessage: []byte("opaque"), DeadlineUnixMilli: now.Add(offset + 1500*time.Millisecond).UnixMilli(),
			OwnerRoute: &irviov1.OwnerRoute{ServerPeerId: []byte(server), ServerIncarnation: 1, RegistrationId: registration[:],
				ControlAddresses: []string{"/ip4/203.0.113.1/tcp/4000/p2p/" + server.String()}},
		}
		_, _, deadline, err := validateSignal(signal, now)
		if err != nil || !deadline.After(now) || deadline.After(now.Add(20*time.Second)) {
			t.Fatalf("offset=%s deadline=%v error=%v", offset, deadline, err)
		}
		signal.DeadlineUnixMilli = now.Add(-30 * time.Second).UnixMilli()
		if _, _, _, err := validateSignal(signal, now); !errors.Is(err, ErrTimeout) {
			t.Fatal("expired envelope accepted", err)
		}
		signal.DeadlineUnixMilli = now.Add(time.Minute).UnixMilli()
		if _, _, _, err := validateSignal(signal, now); !errors.Is(err, ErrTimeout) {
			t.Fatal("unbounded envelope accepted", err)
		}
	}
}
