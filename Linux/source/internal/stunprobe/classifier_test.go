// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunprobe

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func TestClassifierRoutesOnlyExpectedBindingResponses(t *testing.T) {
	registry, err := NewRegistry(testRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	classifier := NewClassifier(registry)
	server := netip.MustParseAddrPort("127.0.0.1:3478")
	other := netip.MustParseAddrPort("127.0.0.1:3479")
	policy := testResponsePolicy(server)
	id := transactionID{1, 2, 3}
	response, err := buildResponse(id, server, false, false)
	if err != nil {
		t.Fatal(err)
	}

	if got := classifier.Classify([]byte("ordinary data"), server, socketmux.IPv4); got != socketmux.Data {
		t.Fatalf("ordinary classification = %v", got)
	}
	unknown := append([]byte(nil), response.Raw...)
	unknown[19] ^= 1
	if got := classifier.Classify(unknown, server, socketmux.IPv4); got != socketmux.Data {
		t.Fatalf("unknown transaction classification = %v", got)
	}
	tx, err := registry.register(id, policy, socketmux.IPv4, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := classifier.Classify(response.Raw, other, socketmux.IPv4); got != socketmux.Data {
		t.Fatalf("wrong-source classification = %v", got)
	}
	if got := classifier.Classify(response.Raw, server, socketmux.IPv6); got != socketmux.Data {
		t.Fatalf("wrong-family classification = %v", got)
	}
	if got := classifier.Classify(response.Raw, server, socketmux.IPv4); got != socketmux.Control {
		t.Fatalf("active classification = %v", got)
	}
	message, status := decodeResponse(response.Raw)
	if status != decodeBindingResponse || !registry.complete(id, server, socketmux.IPv4, message, time.Now()) {
		t.Fatal("could not complete active transaction")
	}
	if event := <-tx.delivery; event.message == nil {
		t.Fatal("completion was not delivered")
	}
	if got := classifier.Classify(response.Raw, server, socketmux.IPv4); got != socketmux.Drop {
		t.Fatalf("retired classification = %v", got)
	}
	if registry.stats.unknownTransaction.Load() != 1 || registry.stats.wrongSource.Load() != 2 || registry.stats.retiredLate.Load() != 1 {
		t.Fatalf("unexpected classifier stats: unknown=%d wrong=%d retired=%d", registry.stats.unknownTransaction.Load(), registry.stats.wrongSource.Load(), registry.stats.retiredLate.Load())
	}
}

func TestClassifierMalformedAndOtherSTUNRemainData(t *testing.T) {
	registry, err := NewRegistry(testRegistryConfig())
	if err != nil {
		t.Fatal(err)
	}
	classifier := NewClassifier(registry)
	source := netip.MustParseAddrPort("127.0.0.1:3478")
	id := transactionID{9}
	malformed := malformedAttributeResponse(id)
	if got := classifier.Classify(malformed, source, socketmux.IPv4); got != socketmux.Data {
		t.Fatalf("malformed attribute classification = %v", got)
	}
	badLength := stun.MustBuild(stun.BindingSuccess).Raw
	badLength = append([]byte(nil), badLength...)
	binary.BigEndian.PutUint16(badLength[2:4], 4)
	if got := classifier.Classify(badLength, source, socketmux.IPv4); got != socketmux.Data {
		t.Fatalf("bad framing classification = %v", got)
	}
	badType := stun.MustBuild(stun.BindingSuccess).Raw
	badType = append([]byte(nil), badType...)
	badType[0] |= 0xc0
	if got := classifier.Classify(badType, source, socketmux.IPv4); got != socketmux.Data {
		t.Fatalf("bad type framing classification = %v", got)
	}
	request := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	if got := classifier.Classify(request.Raw, source, socketmux.IPv4); got != socketmux.Data {
		t.Fatalf("Binding request classification = %v", got)
	}
	if got := registry.stats.malformedSTUNLike.Load(); got != 3 {
		t.Fatalf("malformed count = %d, want 3", got)
	}
}

func TestRegistryBoundsAndQuarantineExpiry(t *testing.T) {
	config := RegistryConfig{MaxInFlight: 1, MaxRetired: 2, RetiredGrace: time.Second}
	registry, err := NewRegistry(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	server := netip.MustParseAddrPort("127.0.0.1:3478")
	policy := testResponsePolicy(server)
	tx, err := registry.register(transactionID{1}, policy, socketmux.IPv4, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.register(transactionID{2}, policy, socketmux.IPv4, now); err != ErrInFlightLimit {
		t.Fatalf("in-flight error = %v", err)
	}
	registry.retire(tx, nil, now)
	if _, err := registry.register(transactionID{1}, policy, socketmux.IPv4, now); err != ErrDuplicateTransaction {
		t.Fatalf("quarantined duplicate error = %v", err)
	}
	for value := byte(2); value < 6; value++ {
		tx, registerErr := registry.register(transactionID{value}, policy, socketmux.IPv4, now)
		if registerErr != nil {
			t.Fatal(registerErr)
		}
		registry.retire(tx, nil, now)
	}
	registry.mu.Lock()
	if len(registry.retired) != config.MaxRetired || registry.retiredOrder.Len() != config.MaxRetired {
		t.Fatalf("retired bounds map=%d list=%d", len(registry.retired), registry.retiredOrder.Len())
	}
	registry.mu.Unlock()
	if got := registry.classify(transactionID{5}, server, socketmux.IPv4, stun.ClassSuccessResponse, now.Add(2*time.Second)); got != socketmux.Data {
		t.Fatalf("expired classification = %v", got)
	}
}

func testResponsePolicy(server netip.AddrPort) responseSourcePolicy {
	return responseSourcePolicy{success: server, errors: [MaxResponseSources]netip.AddrPort{server}, errorCount: 1}
}

func TestRegistryConfigValidation(t *testing.T) {
	tests := []RegistryConfig{
		{},
		{MaxInFlight: -1, MaxRetired: 1, RetiredGrace: time.Second},
		{MaxInFlight: 1, MaxRetired: -1, RetiredGrace: time.Second},
		{MaxInFlight: maximumRegistrySize + 1, MaxRetired: 1, RetiredGrace: time.Second},
		{MaxInFlight: 1, MaxRetired: maximumRegistrySize + 1, RetiredGrace: time.Second},
	}
	for _, config := range tests {
		if _, err := NewRegistry(config); err == nil {
			t.Fatalf("invalid config accepted: %+v", config)
		}
	}
}
