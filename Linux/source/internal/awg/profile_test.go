// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awg

import "testing"

func TestObfuscationProfileHashBindsEveryWireParameter(t *testing.T) {
	base := ObfuscationConfig{
		JunkPacketCount: 1, JunkMin: 24, JunkMax: 24,
		InitPadding: 16, ResponsePadding: 16, CookiePadding: 8,
		InitHeader: 101, ResponseHeader: 202, CookieHeader: 303, TransportHeader: 404,
		SignaturePackets: [5]string{"<b 0x4859445241>"},
	}
	want, err := ObfuscationProfileHash(base)
	if err != nil || want == ([32]byte{}) {
		t.Fatalf("profile hash=%x err=%v", want, err)
	}
	if repeat, err := ObfuscationProfileHash(base); err != nil || repeat != want {
		t.Fatalf("profile hash is not stable: %x %v", repeat, err)
	}
	changed := base
	changed.ResponseHeader++
	if got, err := ObfuscationProfileHash(changed); err != nil || got == want {
		t.Fatalf("changed profile hash=%x err=%v", got, err)
	}
}
