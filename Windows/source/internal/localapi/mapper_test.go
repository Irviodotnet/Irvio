// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"testing"

	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/runtime"
	"github.com/Irviodotnet/Irvio/internal/underlay"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
	"github.com/libp2p/go-libp2p/core/peer"
)

func TestPrivateStatusIncludesPinnedIntentWithoutCountryFallback(t *testing.T) {
	id, err := peer.Decode("12D3KooWN2M6B9pp1CW6rjeKca2hukYqrwgS55biswiGmM1R3j9x")
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []runtime.State{runtime.StateStarting, runtime.StateConnected, runtime.StateNoExit, runtime.StateExitReselection} {
		got := MapProductStatus(runtime.Status{State: state, Country: "DE", PrivateHostingWanted: true,
			Target: exitselector.Target{Kind: exitselector.TargetPrivatePeer, PeerID: id}})
		if got.PrivateTargetPeerId != id.String() || !got.PrivateHostingWanted || got.CountryCode != "" || got.GetConnection().GetExitCountryCode() != "" {
			t.Fatalf("lost or mislabelled private intent: %v", got)
		}
	}
	if got := MapProductStatus(runtime.Status{State: runtime.StateIdle}); got.PrivateTargetPeerId != "" || got.PrivateHostingWanted {
		t.Fatal("idle invented private intent")
	}
}

func TestMapProductStatus(t *testing.T) {
	tests := []struct {
		name  string
		state runtime.State
		want  localv1.ConnectionState
	}{
		{"idle", runtime.StateIdle, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED},
		{"protecting", runtime.StateProtecting, localv1.ConnectionState_CONNECTION_STATE_CONNECTING},
		{"discovering", runtime.StateDiscovering, localv1.ConnectionState_CONNECTION_STATE_CONNECTING},
		{"connecting", runtime.StateConnecting, localv1.ConnectionState_CONNECTION_STATE_CONNECTING},
		{"connected", runtime.StateConnected, localv1.ConnectionState_CONNECTION_STATE_CONNECTED},
		{"lease renewal", runtime.StateLeaseRenewal, localv1.ConnectionState_CONNECTION_STATE_CONNECTED},
		{"same peer", runtime.StateSamePeerRecovery, localv1.ConnectionState_CONNECTION_STATE_RECOVERING},
		{"migration", runtime.StateNetworkMigration, localv1.ConnectionState_CONNECTION_STATE_RECOVERING},
		{"reselection", runtime.StateExitReselection, localv1.ConnectionState_CONNECTION_STATE_RECOVERING},
		{"no exit", runtime.StateNoExit, localv1.ConnectionState_CONNECTION_STATE_NO_EXIT},
		{"disconnect", runtime.StateDisconnecting, localv1.ConnectionState_CONNECTION_STATE_DISCONNECTING},
		{"local promotion failure", runtime.StateError, localv1.ConnectionState_CONNECTION_STATE_ERROR},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := MapProductStatus(runtime.Status{State: test.state, Country: exitselector.CountryIntent("de")})
			if got.State != test.want || got.CountryCode != "DE" {
				t.Fatalf("got state=%v country=%q", got.State, got.CountryCode)
			}
			if test.want == localv1.ConnectionState_CONNECTION_STATE_CONNECTED && got.Connection == nil {
				t.Fatal("connected status lacks connection summary")
			}
		})
	}
}

func TestMapNoExitWithUnreachableUnderlay(t *testing.T) {
	status := runtime.Status{State: runtime.StateNoExit}
	status.Underlay.IPv4.State = underlay.Unreachable
	status.Underlay.IPv6.State = underlay.Unreachable
	if got := MapProductStatus(status).State; got != localv1.ConnectionState_CONNECTION_STATE_NO_NETWORK {
		t.Fatalf("got %v", got)
	}
}

func TestMapUnderlayWaitWithOneUnknownFamily(t *testing.T) {
	status := runtime.Status{State: runtime.StateNoExit, LastReason: runtime.ReasonUnderlayChanged}
	status.Underlay.IPv4.State = underlay.Unreachable
	status.Underlay.IPv6.State = underlay.Unknown
	if got := MapProductStatus(status).State; got != localv1.ConnectionState_CONNECTION_STATE_NO_NETWORK {
		t.Fatalf("got %v", got)
	}
}

func TestMapProductStatusDoesNotInventDiagnostics(t *testing.T) {
	status := runtime.Status{
		State: runtime.StateConnected, Country: "DE", TunnelLeaseID: "secret-lease",
		RuntimeGeneration: 9, SessionGeneration: 7, AWGKeyEpoch: 4,
	}
	got := MapProductStatus(status)
	if got.Connection.LatencyMs != nil || got.Connection.ConnectedSinceUnixMs != 0 || got.Error != nil {
		t.Fatalf("mapper invented or exposed diagnostic data: %v", got)
	}
}

func TestMapProductStatusReportsActualExitContribution(t *testing.T) {
	active := MapProductStatus(runtime.Status{State: runtime.StateConnected, ExitContributing: true})
	if !active.GetContributionEnabled() {
		t.Fatal("active Exit capability is not visible in product status")
	}
	idle := MapProductStatus(runtime.Status{State: runtime.StateIdle})
	if idle.GetContributionEnabled() {
		t.Fatal("IDLE desktop reported an active Exit contribution")
	}
}
