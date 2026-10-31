// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"github.com/multiformats/go-multiaddr"
	"golang.org/x/sys/windows"
)

func admissionProtection(t *testing.T, ctx context.Context) *killswitch.WindowsUnderlayOwner {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	app, err := wfp.ApplicationIDForPath(exe)
	if err != nil {
		t.Fatal(err)
	}
	c := killswitch.WindowsBackendConfig{Policy: killswitch.Policy{TUNName: "irvio0", BypassMark: 1}, Owner: wfp.Config{
		Provider: windows.GUID{Data1: 0xd6e22314, Data2: 0xe698, Data3: 0x4ed9, Data4: [8]byte{0x90, 0x35, 0x83, 0x15, 0x3a, 0x1a, 0x66, 0x17}},
		Sublayer: windows.GUID{Data1: 0xab6e4d92, Data2: 0x1176, Data3: 0x4edf, Data4: [8]byte{0x9a, 0x5c, 0x76, 0xde, 0x38, 0x2e, 0x1c, 0x53}},
		Name:     "IRVIO W05 full protection lab", Marker: "irvio-w05-protection-v1", ServiceName: "IRVIO-W05-Owner", Weight: 49200}, Network: killswitch.WindowsNetwork{Application: app}}
	b, err := killswitch.NewWindowsBackend(c)
	if err != nil {
		t.Fatal(err)
	}
	i, _, err := b.Ensure(ctx, c.Policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := i.Remove(context.Background()); err != nil {
			t.Error(err)
		}
	})
	u, err := b.NetworkUpdater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	o, err := u.OwnUnderlay(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := o.Close(); err != nil {
			t.Error(err)
		}
	})
	return o
}

func TestWindowsNativeSocketAdmission(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_SOCKET_ADMISSION_TEST") != "1" {
		t.Skip("requires isolated forwarded VM and independent mini-PC sender")
	}
	for _, mode := range []WindowsSocketMode{WindowsSocketsHosting, WindowsSocketsProtected} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			path, err := winroute.ResolveUnderlay(netip.MustParseAddr("192.168.23.2"), 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			b := path.Binding
			var underlay *killswitch.WindowsUnderlayOwner
			if mode == WindowsSocketsProtected {
				underlay = admissionProtection(t, ctx)
			}
			blocked := func() {
				t.Helper()
				u, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(b.Source, 49507)))
				if err != nil {
					t.Fatal(err)
				}
				defer u.Close()
				l, err := net.ListenTCP("tcp4", net.TCPAddrFromAddrPort(netip.AddrPortFrom(b.Source, 49508)))
				if err != nil {
					t.Fatal(err)
				}
				defer l.Close()
				u.SetReadDeadline(time.Now().Add(time.Second))
				if _, _, err := u.ReadFromUDP(make([]byte, 128)); err == nil {
					t.Fatal("UDP admitted without permission")
				} else if e, ok := err.(net.Error); !ok || !e.Timeout() {
					t.Fatal(err)
				}
				l.SetDeadline(time.Now().Add(time.Second))
				if c, err := l.AcceptTCP(); err == nil {
					c.Close()
					t.Fatal("TCP admitted without permission")
				} else if e, ok := err.(net.Error); !ok || !e.Timeout() {
					t.Fatal(err)
				}
			}
			blocked()
			m, err := OpenWindowsSocketPermissions(WindowsSocketPermissionsConfig{Directory: filepath.Join(t.TempDir(), "permissions"), Generation: 1, Mode: mode, Underlay: underlay})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := m.Close(); err != nil {
					t.Error(err)
				}
			})
			udpOwner := &WindowsTransportSockets{}
			t.Cleanup(func() {
				if err := udpOwner.Close(); err != nil {
					t.Error(err)
				}
			})
			udpFactory, err := udpOwner.Factory(m.Datagram)
			if err != nil {
				t.Fatal(err)
			}
			config := socketmux.DefaultConfig()
			config.IPv4Bind = b.Source
			config.IPv6Policy = socketmux.FamilyDisabled
			config.Port = 49507
			state := networkstate.State{Generation: 1, Snapshot: networkstate.Snapshot{Links: []networkstate.Link{{Index: int(b.Index), NativeID: b.LUID, Up: true, Addresses: []netip.Prefix{netip.PrefixFrom(b.Source, 32)}}}}}
			udp, udpLease, err := udpFactory(ctx, state, config)
			if err != nil {
				t.Fatal(err)
			}
			tcpOwner := &controlsocket.WindowsListeners{}
			t.Cleanup(func() {
				if err := tcpOwner.Close(); err != nil {
					t.Error(err)
				}
			})
			tcpFactory, err := tcpOwner.Factory(ctx, func(context.Context, bool) (winroute.Binding, error) { return b, nil }, func(ctx context.Context, b winroute.Binding, local netip.AddrPort) (io.Closer, error) {
				return m.Listener(ctx, 1, b, local)
			})
			if err != nil {
				t.Fatal(err)
			}
			address, err := multiaddr.NewMultiaddr("/ip4/0.0.0.0/tcp/49508")
			if err != nil {
				t.Fatal(err)
			}
			listener, err := tcpFactory(address)
			if err != nil {
				t.Fatal(err)
			}
			receive, stop := context.WithTimeout(ctx, 8*time.Second)
			packet, err := udp.Data().Read(receive)
			stop()
			if err != nil {
				t.Fatal("inbound UDP", err)
			}
			if _, err := udp.Data().WriteTo(ctx, packet.Payload, packet.Source); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				for {
					c, err := listener.Accept()
					if err != nil {
						done <- err
						return
					}
					c.SetDeadline(time.Now().Add(400 * time.Millisecond))
					_, err = c.Write([]byte("IRVIO-INBOUND-OK\n"))
					ack := make([]byte, 2)
					if err == nil {
						_, err = io.ReadFull(c, ack)
					}
					c.Close()
					if err == nil && string(ack) == "OK" {
						done <- nil
						return
					}
				}
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal("inbound TCP", err)
				}
			case <-ctx.Done():
				listener.Close()
				<-done
				t.Fatal("incoming TCP deadline")
			}
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			if err := udpLease.Close(); err != nil {
				t.Fatal(err)
			}
			if len(m.entries) != 0 {
				t.Fatal("permissions survived socket close")
			}
			blocked()
			if underlay != nil {
				if err := underlay.Advance(ctx, 1); err != nil {
					t.Fatal("protection removed with socket", err)
				}
			}
			t.Log("production TCP/UDP factories: admitted only while owned; cleanup and protection lifetime PASS")
		})
	}
}
