// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package natprobe

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/stunprobe"
)

func TestTopologyValidation(t *testing.T) {
	primary := netip.MustParseAddrPort("192.0.2.1:3478")
	other := netip.MustParseAddrPort("192.0.2.2:3479")
	base := stunprobe.BindingResponse{
		Destination: primary, ResponseSource: primary, Family: socketmux.IPv4,
		ResponseOrigin: stunprobe.OptionalAddrPort{AddrPort: primary, Present: true},
		OtherAddress:   stunprobe.OptionalAddrPort{AddrPort: other, Present: true},
	}
	topology, err := topologyFromBaseline(primary, base)
	if err != nil {
		t.Fatal(err)
	}
	if topology.AlternateIPPrimaryPort != netip.MustParseAddrPort("192.0.2.2:3478") ||
		topology.PrimaryIPAlternatePort != netip.MustParseAddrPort("192.0.2.1:3479") || topology.AlternateIPAlternatePort != other {
		t.Fatalf("unexpected topology: %+v", topology)
	}

	tests := map[string]stunprobe.BindingResponse{
		"response family mismatch": func() stunprobe.BindingResponse {
			response := base
			response.Family = socketmux.IPv6
			return response
		}(),
		"same IP": func() stunprobe.BindingResponse {
			response := base
			response.OtherAddress.AddrPort = netip.MustParseAddrPort("192.0.2.1:3479")
			return response
		}(),
		"same port": func() stunprobe.BindingResponse {
			response := base
			response.OtherAddress.AddrPort = netip.MustParseAddrPort("192.0.2.2:3478")
			return response
		}(),
		"mixed family": func() stunprobe.BindingResponse {
			response := base
			response.OtherAddress.AddrPort = netip.MustParseAddrPort("[2001:db8::2]:3479")
			return response
		}(),
		"unspecified": func() stunprobe.BindingResponse {
			response := base
			response.OtherAddress.AddrPort = netip.MustParseAddrPort("0.0.0.0:3479")
			return response
		}(),
		"multicast": func() stunprobe.BindingResponse {
			response := base
			response.OtherAddress.AddrPort = netip.MustParseAddrPort("224.0.0.1:3479")
			return response
		}(),
		"mapped IPv6": func() stunprobe.BindingResponse {
			response := base
			response.OtherAddress.AddrPort = netip.MustParseAddrPort("[::ffff:192.0.2.2]:3479")
			return response
		}(),
		"wrong origin": func() stunprobe.BindingResponse {
			response := base
			response.ResponseOrigin.AddrPort = other
			return response
		}(),
		"actual source mismatch": func() stunprobe.BindingResponse {
			response := base
			response.ResponseSource = other
			return response
		}(),
	}
	for name, response := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := topologyFromBaseline(primary, response); !errors.Is(err, ErrInvalidTopology) {
				t.Fatalf("topology error = %v", err)
			}
		})
	}
	missingOther := base
	missingOther.OtherAddress = stunprobe.OptionalAddrPort{}
	if _, err := topologyFromBaseline(primary, missingOther); !errors.Is(err, ErrServerUnsupported) {
		t.Fatalf("missing OTHER-ADDRESS error = %v", err)
	}
	missingOrigin := base
	missingOrigin.ResponseOrigin = stunprobe.OptionalAddrPort{}
	if _, err := topologyFromBaseline(primary, missingOrigin); !errors.Is(err, ErrServerUnsupported) {
		t.Fatalf("missing RESPONSE-ORIGIN error = %v", err)
	}
}

func TestTranslationState(t *testing.T) {
	concrete := netip.MustParseAddrPort("10.0.0.2:41000")
	if got := translationState(concrete, concrete); got != TranslationAbsent {
		t.Fatalf("equal concrete endpoints = %v", got)
	}
	if got := translationState(concrete, netip.MustParseAddrPort("198.51.100.8:51000")); got != TranslationPresent {
		t.Fatalf("different endpoints = %v", got)
	}
	if got := translationState(netip.MustParseAddrPort("0.0.0.0:41000"), concrete); got != TranslationUnknown {
		t.Fatalf("wildcard local endpoint = %v", got)
	}
}

func TestConfigValidation(t *testing.T) {
	if config := DefaultConfig(); config.MinimumTransactionInterval != 100_000_000 || config.MaxObservations != defaultObservationLimit {
		t.Fatalf("unexpected default config: %+v", config)
	}
	if _, err := New(nil, DefaultConfig()); err == nil {
		t.Fatal("nil exchanger accepted")
	}
	script := newIPv4Script(scriptOptions{})
	for _, config := range []Config{
		{MinimumTransactionInterval: -1, MaxObservations: minimumObservationLimit},
		{MaxObservations: minimumObservationLimit - 1},
		{MaxObservations: maximumObservationLimit + 1},
	} {
		if _, err := New(script, config); err == nil {
			t.Fatalf("invalid config accepted: %+v", config)
		}
	}
}
