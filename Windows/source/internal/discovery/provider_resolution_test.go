// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package discovery

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func TestProcessProviderResults(t *testing.T) {
	address := ma.StringCast("/ip4/198.18.4.2/tcp/46054")
	s := peer.ID("provider-s")
	u := peer.ID("provider-t")
	notFound := errors.New("routing: not found")
	tests := []struct {
		name        string
		providers   []peer.AddrInfo
		stored      map[peer.ID][]ma.Multiaddr
		resolved    map[peer.ID]peer.AddrInfo
		resolveErr  error
		want        []peer.ID
		wantLookups map[peer.ID]int
	}{
		{
			name: "empty first then direct address",
			providers: []peer.AddrInfo{
				{ID: s}, {ID: s, Addrs: []ma.Multiaddr{address}},
			},
			resolveErr: notFound, want: []peer.ID{s},
			wantLookups: map[peer.ID]int{s: 1},
		},
		{
			name:      "peerstore address",
			providers: []peer.AddrInfo{{ID: s}},
			stored:    map[peer.ID][]ma.Multiaddr{s: {address}},
			want:      []peer.ID{s}, wantLookups: map[peer.ID]int{},
		},
		{
			name:      "find peer address",
			providers: []peer.AddrInfo{{ID: s}},
			resolved:  map[peer.ID]peer.AddrInfo{s: {ID: s, Addrs: []ma.Multiaddr{address}}},
			want:      []peer.ID{s}, wantLookups: map[peer.ID]int{s: 1},
		},
		{
			name:       "unresolved everywhere",
			providers:  []peer.AddrInfo{{ID: s}, {ID: s}},
			resolveErr: notFound, wantLookups: map[peer.ID]int{s: 1},
		},
		{
			name:       "unresolved peer does not hide another",
			providers:  []peer.AddrInfo{{ID: s}, {ID: u, Addrs: []ma.Multiaddr{address}}},
			resolveErr: notFound, want: []peer.ID{u},
			wantLookups: map[peer.ID]int{s: 1},
		},
		{
			name:       "failed find peer followed by direct address",
			providers:  []peer.AddrInfo{{ID: s}, {ID: s, Addrs: []ma.Multiaddr{address}}},
			resolveErr: notFound, want: []peer.ID{s},
			wantLookups: map[peer.ID]int{s: 1},
		},
		{
			name:       "successful peer is yielded once",
			providers:  []peer.AddrInfo{{ID: s}, {ID: s, Addrs: []ma.Multiaddr{address}}, {ID: s, Addrs: []ma.Multiaddr{address}}},
			resolveErr: notFound, want: []peer.ID{s},
			wantLookups: map[peer.ID]int{s: 1},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stream := make(chan peer.AddrInfo, len(tc.providers))
			for _, provider := range tc.providers {
				stream <- provider
			}
			close(stream)
			lookups := make(map[peer.ID]int)
			var got []peer.ID
			err := processProviderResults(context.Background(), peer.ID("self"), stream,
				func(id peer.ID) []ma.Multiaddr { return tc.stored[id] },
				func(_ context.Context, id peer.ID) (peer.AddrInfo, error) {
					lookups[id]++
					if tc.resolveErr != nil {
						return peer.AddrInfo{}, tc.resolveErr
					}
					return tc.resolved[id], nil
				},
				func(candidate peer.AddrInfo) (bool, error) {
					if len(candidate.Addrs) == 0 {
						t.Fatal("empty AddrInfo reached the dial callback")
					}
					got = append(got, candidate.ID)
					return true, nil
				})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) || !reflect.DeepEqual(lookups, tc.wantLookups) {
				t.Fatalf("got candidates %v, lookups %v; want %v, %v", got, lookups, tc.want, tc.wantLookups)
			}
		})
	}
}

func TestProviderResolutionDoesNotConsumeWholeAcquisition(t *testing.T) {
	address := ma.StringCast("/ip4/198.18.4.2/tcp/46054")
	stream := make(chan peer.AddrInfo, 2)
	stream <- peer.AddrInfo{ID: peer.ID("unresolved")}
	stream <- peer.AddrInfo{ID: peer.ID("healthy"), Addrs: []ma.Multiaddr{address}}
	close(stream)
	started := time.Now()
	var got []peer.ID
	err := processProviderResults(context.Background(), peer.ID("self"), stream,
		func(peer.ID) []ma.Multiaddr { return nil },
		func(ctx context.Context, _ peer.ID) (peer.AddrInfo, error) {
			<-ctx.Done()
			return peer.AddrInfo{}, ctx.Err()
		},
		func(candidate peer.AddrInfo) (bool, error) {
			got = append(got, candidate.ID)
			return true, nil
		})
	if err != nil || !reflect.DeepEqual(got, []peer.ID{peer.ID("healthy")}) || time.Since(started) > time.Second {
		t.Fatalf("bounded resolution failed: candidates %v, error %v, elapsed %s", got, err, time.Since(started))
	}
}
