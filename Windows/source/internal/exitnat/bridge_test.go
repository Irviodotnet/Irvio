// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitnat

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/icmpv4"
	"github.com/Irviodotnet/Irvio/internal/tunnellease"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun/tuntest"
)

func TestBridgedICMPErrorDelivery(t *testing.T) {
	for _, kind := range [][2]byte{{3, 4}, {3, 3}, {11, 0}, {12, 0}} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			ctx := context.Background()
			host := tuntest.NewChannelTUN()
			var f *backendFixture
			r, err := NewBridgedResources(BridgedConfig{TUN: host.TUN(), Control: netip.MustParseAddrPort("10.17.0.1:7890"), Limit: 8,
				Backend: func(deliver Emit) (*Backend, error) { f = newBackendFixture(t, deliver); return f.b, nil },
				Install: func(context.Context, tunnellease.Lease) error { return nil },
				Remove:  func(context.Context, tunnellease.Lease) error { return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			read := make(chan []byte, 8)
			readerDone := make(chan struct{})
			go func() {
				defer close(readerDone)
				for {
					buffer, sizes := make([]byte, 2048), []int{0}
					_, err := r.Bridge().Read([][]byte{buffer}, sizes, 0)
					if err != nil {
						return
					}
					read <- buffer[:sizes[0]]
				}
			}()
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				if err := r.Close(cleanup); err != nil {
					t.Error(err)
				}
				select {
				case <-readerDone:
				case <-cleanup.Done():
					t.Error("bridge reader retained")
				}
			})
			v := resourceLease(2)
			if err := r.ActivateLease(ctx, v); err != nil {
				t.Fatal(err)
			}
			request := wire(UDP, v.IPv4.Addr(), remote, 30000, 33434, 0, make([]byte, 1172))
			if _, err := r.Bridge().Write([][]byte{request}, 0); err != nil {
				t.Fatal(err)
			}
			quoted := f.sender.packets[0][:28]
			errorPacket := errorWire(quoted, kind[0], kind[1])
			token := f.b.engine.reverse[external{UDP, f.ports[0].port}].reply
			if err := f.b.engine.Inbound(ctx, f.path, token, errorPacket, r.deliver); err != nil {
				t.Fatal("validated NAT error dropped by bridge", err)
			}
			select {
			case packet := <-read:
				p, err := icmpv4.ParseError(packet)
				if err != nil || p.Recipient != v.IPv4.Addr() || p.OriginalSource != v.IPv4.Addr() || p.SourcePort != 30000 || p.DestinationPort != 33434 || p.OriginalDestination != remote {
					t.Fatal("wrong ICMP delivery", p, err)
				}
				if kind == [2]byte{3, 4} && binary.BigEndian.Uint16(packet[26:28]) != 1280 {
					t.Fatal("PMTU changed in bridge")
				}
				packet[22] ^= 1
				if err := r.deliver(ctx, f.path, packet); err == nil {
					t.Fatal("malformed ICMP accepted by bridge")
				}
			case <-time.After(time.Second):
				t.Fatal("ICMP error did not reach the AWG reader")
			}
			if err := r.RevokeLease(ctx, v, tunnellease.RevokeExplicitRelease); err != nil {
				t.Fatal(err)
			}
			if err := f.b.engine.Inbound(ctx, f.path, token, errorPacket, r.deliver); !errors.Is(err, ErrStale) {
				t.Fatal("ICMP delivered after revoke", err)
			}
		})
	}
}

func TestBridgedActivationFailureAndCleanupRetry(t *testing.T) {
	ctx := context.Background()
	host := tuntest.NewChannelTUN()
	failure := errors.New("injected peer failure")
	installFails, removeFails := true, true
	var r *BridgedResources
	var f *backendFixture
	var err error
	r, err = NewBridgedResources(BridgedConfig{TUN: host.TUN(), Control: netip.MustParseAddrPort("10.17.0.1:7890"), Limit: 8,
		Backend: func(deliver Emit) (*Backend, error) { f = newBackendFixture(t, deliver); return f.b, nil },
		Install: func(_ context.Context, v tunnellease.Lease) error {
			packet := wire(UDP, v.IPv4.Addr(), remote, 30000, 443, 0, nil)
			if _, err := r.Bridge().Write([][]byte{packet}, 0); err != nil {
				return err
			}
			if f.b.Stats().Flows != 0 {
				return errors.New("forwarding before activation completed")
			}
			if installFails {
				return failure
			}
			return nil
		},
		Remove: func(context.Context, tunnellease.Lease) error {
			if removeFails {
				return failure
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		removeFails = false
		if err := r.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	v := resourceLease(2)
	if err := r.ActivateLease(ctx, v); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if len(r.bySource) != 1 || f.b.Stats().Leases != 0 {
		t.Fatal("lost ownership of failed cleanup")
	}
	if err := r.ActivateLease(ctx, v); !errors.Is(err, ErrStale) {
		t.Fatal("reactivated failed cleanup", err)
	}
	removeFails = false
	if err := r.RevokeLease(ctx, v, tunnellease.RevokeExplicitRelease); err != nil {
		t.Fatal(err)
	}
	if len(r.bySource) != 0 {
		t.Fatal("stale bridge attachment")
	}
	installFails = false
	v.ID[0] = 3
	if err := r.ActivateLease(ctx, v); err != nil {
		t.Fatal(err)
	}
	packet := wire(UDP, v.IPv4.Addr(), remote, 30000, 443, 0, nil)
	if _, err := r.Bridge().Write([][]byte{packet}, 0); err != nil {
		t.Fatal(err)
	}
	if f.b.Stats().Flows != 1 {
		t.Fatal("active lease not forwarded")
	}
	removeFails = true
	if err := r.Close(ctx); !errors.Is(err, failure) {
		t.Fatal("cleanup failure hidden", err)
	}
	if len(r.bySource) != 1 {
		t.Fatal("cleanup retry token discarded")
	}
	removeFails = false
	if err := r.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.bySource) != 0 || r.Stats() != (Stats{}) {
		t.Fatal("cleanup residue")
	}
}

func TestBridgedControlCollisionAndFactoryRollback(t *testing.T) {
	t.Run("control-collision", func(t *testing.T) {
		h := tuntest.NewChannelTUN()
		installed := false
		r, err := NewBridgedResources(BridgedConfig{TUN: h.TUN(), Control: netip.MustParseAddrPort("10.17.0.1:7890"), Limit: 8,
			Backend: func(deliver Emit) (*Backend, error) { return newBackendFixture(t, deliver).b, nil },
			Install: func(context.Context, tunnellease.Lease) error { installed = true; return nil },
			Remove:  func(context.Context, tunnellease.Lease) error { return nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := r.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
		if err := r.ActivateLease(context.Background(), resourceLease(1)); err == nil || installed {
			t.Fatal("control address leased", err)
		}
		if len(r.bySource) != 0 || r.Stats() != (Stats{}) {
			t.Fatal("collision rollback residue")
		}
	})
	t.Run("factory-rollback", func(t *testing.T) {
		h := tuntest.NewChannelTUN()
		r, err := NewBridgedResources(BridgedConfig{TUN: h.TUN(), Control: netip.MustParseAddrPort("10.17.0.1:7890"), Limit: 8,
			Backend: func(Emit) (*Backend, error) { return nil, ErrConfig },
			Install: func(context.Context, tunnellease.Lease) error { return nil }, Remove: func(context.Context, tunnellease.Lease) error { return nil },
		})
		if r != nil || !errors.Is(err, ErrConfig) {
			t.Fatal(r, err)
		}
		for range h.TUN().Events() {
		}
	})
}
