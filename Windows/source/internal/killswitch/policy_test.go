// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package killswitch

import (
	"errors"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/linuxroute"
)

func TestPolicyDefaultsArePlatformIndependent(t *testing.T) {
	input := Policy{TUNName: "irvio0", BypassMark: linuxroute.IrvioBypassMark}
	got, err := normalizePolicy(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.TableName != DefaultTableName || got.TunnelControlMark != linuxroute.IrvioTunnelControlMark || got.BypassMark != input.BypassMark {
		t.Fatalf("unexpected defaults: %+v", got)
	}
	explicit := Policy{TableName: "custom-policy", TUNName: "custom-tun", BypassMark: 7, TunnelControlMark: 8}
	if got, err := normalizePolicy(explicit); err != nil || got != explicit {
		t.Fatalf("explicit policy changed: %+v, %v", got, err)
	}
}

func TestPolicyRejectsAmbiguousOrUnsafeValues(t *testing.T) {
	for _, input := range []Policy{
		{TUNName: "irvio0"},
		{BypassMark: 1},
		{TableName: "bad name", TUNName: "irvio0", BypassMark: 1},
		{TUNName: "bad/name", BypassMark: 1},
		{TUNName: "abcdefghijklmnop", BypassMark: 1},
		{TUNName: "irvio0", BypassMark: 1, TunnelControlMark: 1},
		{TUNName: "irvio0", BypassMark: linuxroute.IrvioTunnelControlMark},
	} {
		if _, err := normalizePolicy(input); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("policy %+v accepted or wrong error: %v", input, err)
		}
	}
}
