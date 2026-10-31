// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import (
	"net/netip"
	"testing"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func FuzzClassifier(f *testing.F) {
	registry, err := NewRegistry(DefaultRegistryConfig())
	if err != nil {
		f.Fatal(err)
	}
	if err := registry.registerUfrag("localA", socketmux.IPv4); err != nil {
		f.Fatal(err)
	}
	classifier := NewClassifier(registry)
	f.Add([]byte("future-data"), byte(4))
	f.Add(mustFuzzRequest("localA:remoteA"), byte(4))
	f.Add(mustFuzzRequest("unknown:remoteA"), byte(6))
	source4 := netip.MustParseAddrPort("192.0.2.1:3478")
	source6 := netip.MustParseAddrPort("[2001:db8::1]:3478")
	f.Fuzz(func(t *testing.T, payload []byte, familyByte byte) {
		family := socketmux.IPv4
		source := source4
		if familyByte&1 != 0 {
			family = socketmux.IPv6
			source = source6
		}
		class := classifier.Classify(payload, source, family)
		if class != socketmux.Control && class != socketmux.Data && class != socketmux.Drop {
			t.Fatalf("invalid packet class %d", class)
		}
	})
}

func mustFuzzRequest(username string) []byte {
	message := stun.MustBuild(stun.TransactionID, stun.BindingRequest, stun.NewUsername(username))
	return append([]byte(nil), message.Raw...)
}
