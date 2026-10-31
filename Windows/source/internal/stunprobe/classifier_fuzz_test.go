// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunprobe

import (
	"net/netip"
	"testing"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

func FuzzClassifierUnknownTrafficRemainsData(f *testing.F) {
	registry, err := NewRegistry(RegistryConfig{MaxInFlight: 4, MaxRetired: 4, RetiredGrace: time.Second})
	if err != nil {
		f.Fatal(err)
	}
	classifier := NewClassifier(registry)
	validUnknown := stun.MustBuild(stun.TransactionID, stun.BindingSuccess)
	f.Add([]byte(nil))
	f.Add([]byte{0, 1, 0, 0, 0x21, 0x12, 0xa4, 0x42})
	f.Add(validUnknown.Raw)
	f.Fuzz(func(t *testing.T, payload []byte) {
		class := classifier.Classify(payload, netip.MustParseAddrPort("127.0.0.1:3478"), socketmux.IPv4)
		if class != socketmux.Data {
			t.Fatalf("unregistered packet class = %v, want Data", class)
		}
	})
}
