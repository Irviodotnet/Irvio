// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestClassifierRequestRouting(t *testing.T) {
	registry, err := NewRegistry(DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.registerUfrag("localA", socketmux.IPv4); err != nil {
		t.Fatal(err)
	}
	classifier := NewClassifier(registry)
	source := netip.MustParseAddrPort("192.0.2.50:5000")
	tests := []struct {
		name    string
		message *stun.Message
		want    socketmux.PacketClass
	}{
		{"known-ufrag-unknown-source", mustBindingRequest(t, "localA:remoteA"), socketmux.Control},
		{"unknown-ufrag", mustBindingRequest(t, "otherA:remoteA"), socketmux.Data},
		{"malformed-username", mustBindingRequest(t, "localA"), socketmux.Data},
		{"no-username", stun.MustBuild(stun.TransactionID, stun.BindingRequest), socketmux.Data},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifier.Classify(test.message.Raw, source, socketmux.IPv4); got != test.want {
				t.Fatalf("Classify() = %v, want %v", got, test.want)
			}
		})
	}

	wrongMethod := mustBindingRequest(t, "localA:remoteA")
	wrongMethod.Type = stun.NewType(stun.MethodAllocate, stun.ClassRequest)
	wrongMethod.Encode()
	if got := classifier.Classify(wrongMethod.Raw, source, socketmux.IPv4); got != socketmux.Data {
		t.Fatalf("wrong method classified as %v", got)
	}

	multiple := stun.MustBuild(stun.TransactionID, stun.BindingRequest, stun.NewUsername("localA:remoteA"), stun.NewUsername("localA:remoteA"))
	if got := classifier.Classify(multiple.Raw, source, socketmux.IPv4); got != socketmux.Data {
		t.Fatalf("multiple USERNAME attributes classified as %v", got)
	}
}

func TestClassifierResponseRoutingAndQuarantine(t *testing.T) {
	registry, err := NewRegistry(DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.registerUfrag("localA", socketmux.IPv4); err != nil {
		t.Fatal(err)
	}
	request := mustBindingRequest(t, "remoteA:localA")
	server := netip.MustParseAddrPort("192.0.2.1:3478")
	id := transactionID(request.TransactionID)
	if err := registry.registerTransaction(id, "localA", server, socketmux.IPv4, time.Now()); err != nil {
		t.Fatal(err)
	}
	response := mustBindingResponse(t, request)
	classifier := NewClassifier(registry)
	if got := classifier.Classify(response.Raw, server, socketmux.IPv4); got != socketmux.Control {
		t.Fatalf("active response classified as %v", got)
	}
	if got := classifier.Classify(response.Raw, netip.MustParseAddrPort("192.0.2.2:3478"), socketmux.IPv4); got != socketmux.Data {
		t.Fatalf("wrong source classified as %v", got)
	}
	if got := classifier.Classify(response.Raw, server, socketmux.IPv6); got != socketmux.Data {
		t.Fatalf("wrong family classified as %v", got)
	}
	unknown := stun.MustBuild(stun.TransactionID, stun.BindingSuccess)
	if got := classifier.Classify(unknown.Raw, server, socketmux.IPv4); got != socketmux.Data {
		t.Fatalf("unknown transaction classified as %v", got)
	}
	registry.retireTransaction(id, time.Now())
	if got := classifier.Classify(response.Raw, server, socketmux.IPv4); got != socketmux.Drop {
		t.Fatalf("retired response classified as %v", got)
	}
}

func TestClassifierMalformedAndOrdinaryTrafficRemainData(t *testing.T) {
	registry, err := NewRegistry(DefaultRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	classifier := NewClassifier(registry)
	source := netip.MustParseAddrPort("192.0.2.1:3478")
	if got := classifier.Classify([]byte("future-amneziawg-data"), source, socketmux.IPv4); got != socketmux.Data {
		t.Fatalf("ordinary payload classified as %v", got)
	}
	malformed := append([]byte(nil), stun.MustBuild(stun.TransactionID, stun.BindingSuccess).Raw...)
	binary.BigEndian.PutUint16(malformed[2:4], uint16(len(malformed)))
	if got := classifier.Classify(malformed, source, socketmux.IPv4); got != socketmux.Data {
		t.Fatalf("malformed STUN-like payload classified as %v", got)
	}
}

func TestRegistryBoundsExpiryAndRemoval(t *testing.T) {
	config := DefaultRegistryConfig()
	config.MaxUfrags = 1
	config.MaxTransactions = 1
	config.MaxRetired = 1
	config.TransactionTTL = time.Millisecond
	config.RetiredTTL = time.Second
	registry, err := NewRegistry(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.registerUfrag("localA", socketmux.IPv4); err != nil {
		t.Fatal(err)
	}
	if err := registry.registerUfrag("localB", socketmux.IPv4); err != ErrTooManyUfrags {
		t.Fatalf("second ufrag error = %v", err)
	}
	now := time.Now()
	first := transactionID{1}
	server := netip.MustParseAddrPort("192.0.2.1:3478")
	if err := registry.registerTransaction(first, "localA", server, socketmux.IPv4, now); err != nil {
		t.Fatal(err)
	}
	if err := registry.registerTransaction(transactionID{2}, "localA", server, socketmux.IPv4, now); err != ErrTooManyTransactions {
		t.Fatalf("second transaction error = %v", err)
	}
	if got := registry.classifyResponse(first, server, socketmux.IPv4, now.Add(2*time.Millisecond)); got != responseRetired {
		t.Fatalf("expired transaction disposition = %v", got)
	}
	registry.removeUfrag("localA", now)
	ufrags, active, retired := registry.sizes()
	if ufrags != 0 || active != 0 || retired > 1 {
		t.Fatalf("registry sizes = %d/%d/%d", ufrags, active, retired)
	}
}
