// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awg

import (
	"errors"
	"net/netip"
	"testing"

	libcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/ice/v4"

	"github.com/Irviodotnet/Irvio/internal/connsupervisor"
	"github.com/Irviodotnet/Irvio/internal/iceconnect"
	"github.com/Irviodotnet/Irvio/internal/icesession"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
)

func TestSelectedEndpointFromICEFencesGenerationPeerAndCandidate(t *testing.T) {
	remote := testPeerID(t)
	endpoint := netip.MustParseAddrPort("198.51.100.20:52012")
	result := iceconnect.Result{
		TransportGeneration: 7,
		RemotePeer:          remote,
		RemoteEndpoint:      endpoint,
		SelectedRemoteCandidate: iceconnect.CandidateSnapshot{
			Type: ice.CandidateTypePeerReflexive, Network: icesession.NetworkUDP4,
			Address: endpoint.Addr(), Port: endpoint.Port(), Component: 1,
		},
	}
	selected, err := SelectedEndpointFromICE(result, networkstate.Generation(7), connsupervisor.SessionGeneration(9), remote)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Address != endpoint || selected.Candidate.Type != ice.CandidateTypePeerReflexive {
		t.Fatalf("selected endpoint = %+v", selected)
	}
	if _, err := SelectedEndpointFromICE(result, 8, 9, remote); !errors.Is(err, ErrWrongGeneration) {
		t.Fatalf("wrong-generation error = %v", err)
	}
	other := testPeerID(t)
	if _, err := SelectedEndpointFromICE(result, 7, 9, other); !errors.Is(err, ErrWrongPeer) {
		t.Fatalf("wrong-peer error = %v", err)
	}
	result.SelectedRemoteCandidate.Port++
	if _, err := SelectedEndpointFromICE(result, 7, 9, remote); !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatalf("candidate mismatch error = %v", err)
	}
	result.SelectedRemoteCandidate.Port--
	result.SelectedRemoteCandidate.Network = icesession.NetworkUDP6
	if _, err := SelectedEndpointFromICE(result, 7, 9, remote); !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatalf("candidate family mismatch error = %v", err)
	}
	result.SelectedRemoteCandidate.Network = icesession.NetworkUDP4
	result.SelectedRemoteCandidate.Component = 2
	if _, err := SelectedEndpointFromICE(result, 7, 9, remote); !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatalf("candidate component mismatch error = %v", err)
	}
}

func testPeerID(t *testing.T) peer.ID {
	t.Helper()
	privateKey, _, err := libcrypto.GenerateEd25519Key(nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := peer.IDFromPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
