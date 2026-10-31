// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package transportgen

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

type muxCloseFunc func() error

func (f muxCloseFunc) Close() error { return f() }

func TestGenerationMuxFactoryOwnership(t *testing.T) {
	b := loopbackBuilder(t, nil)
	closed := 0
	var gotGeneration networkstate.Generation
	b.config.MuxFactory = func(ctx context.Context, state networkstate.State, c socketmux.Config) (*socketmux.Mux, io.Closer, error) {
		gotGeneration = state.Generation
		m, err := socketmux.New(ctx, c)
		if err != nil {
			return nil, nil, err
		}
		return m, muxCloseFunc(func() error { closed++; return m.Close() }), nil
	}
	g, err := b.Build(context.Background(), loopbackState(7))
	if err != nil {
		t.Fatal(err)
	}
	if gotGeneration != 7 || closed != 0 {
		t.Fatal("factory lifecycle", gotGeneration, closed)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if err := g.Close(); err != nil || closed != 1 {
		t.Fatal("owner closed more than once", closed, err)
	}
}

func TestGenerationMuxFactoryFailureCleanup(t *testing.T) {
	for _, stage := range []string{"factory", "stun", "missing-owner"} {
		t.Run(stage, func(t *testing.T) {
			b := loopbackBuilder(t, nil)
			closed := 0
			var local netip.AddrPort
			cleanupErr := errors.New("retained platform cleanup")
			b.config.MuxFactory = func(ctx context.Context, _ networkstate.State, c socketmux.Config) (*socketmux.Mux, io.Closer, error) {
				m, err := socketmux.New(ctx, c)
				if err != nil {
					return nil, nil, err
				}
				local, _ = m.LocalAddr(socketmux.IPv4)
				owner := muxCloseFunc(func() error { closed++; return errors.Join(m.Close(), cleanupErr) })
				if stage == "factory" {
					return m, owner, errors.New("injected authorization failure")
				}
				if stage == "missing-owner" {
					return m, nil, nil
				}
				return m, owner, nil
			}
			if stage == "stun" {
				b.config.STUNServers = map[socketmux.AddressFamily]netip.AddrPort{socketmux.IPv4: netip.MustParseAddrPort("[::1]:9")}
			}
			g, err := b.Build(context.Background(), loopbackState(8))
			if err == nil || g != nil {
				t.Fatal("failed build accepted")
			}
			if stage != "missing-owner" && (!errors.Is(err, cleanupErr) || closed != 1) {
				t.Fatal("cleanup error or owner lost", closed, err)
			}
			c := socketmux.DefaultConfig()
			c.IPv4Bind = local.Addr()
			c.IPv6Policy = socketmux.FamilyDisabled
			c.Port = int(local.Port())
			m, err := socketmux.New(context.Background(), c)
			if err != nil {
				t.Fatal("failed factory retained an unowned socket", err)
			}
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
