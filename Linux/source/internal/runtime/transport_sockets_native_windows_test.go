// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package runtime

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/networkstate"
	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winroute"
)

func TestWindowsNativeTransportSocketFactory(t *testing.T) {
	if os.Getenv("IRVIO_WINDOWS_TRANSPORT_SOCKET_TEST") != "1" {
		t.Skip("requires isolated Windows VM and mini-PC STUN oracle")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	target := netip.MustParseAddrPort("192.168.23.2:49505")
	path, err := winroute.ResolveUnderlay(target.Addr(), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	app, err := wfp.ApplicationIDForPath(self)
	if err != nil {
		t.Fatal(err)
	}
	// Use the native protection fixture identity so its independent cleanup tool remains applicable.
	c := killswitch.WindowsBackendConfig{
		Policy: killswitch.Policy{TUNName: "irvio0", BypassMark: 1},
		Owner: wfp.Config{
			Provider: windows.GUID{Data1: 0xd6e22314, Data2: 0xe698, Data3: 0x4ed9, Data4: [8]byte{0x90, 0x35, 0x83, 0x15, 0x3a, 0x1a, 0x66, 0x17}},
			Sublayer: windows.GUID{Data1: 0xab6e4d92, Data2: 0x1176, Data3: 0x4edf, Data4: [8]byte{0x9a, 0x5c, 0x76, 0xde, 0x38, 0x2e, 0x1c, 0x53}},
			Name:     "IRVIO W05 full protection lab", Marker: "irvio-w05-protection-v1", ServiceName: "IRVIO-W05-Owner", Weight: 49200,
		},
		Network: killswitch.WindowsNetwork{Application: app},
	}
	backend, err := killswitch.NewWindowsBackend(c)
	if err != nil {
		t.Fatal(err)
	}
	installation, _, err := backend.Ensure(ctx, c.Policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := installation.Remove(context.Background()); err != nil {
			t.Error(err)
		}
	})
	updater, err := backend.NetworkUpdater(ctx)
	if err != nil {
		t.Fatal(err)
	}
	grants, err := updater.OwnUnderlay(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := grants.Close(); err != nil {
			t.Error(err)
		}
	})
	sockets := &WindowsTransportSockets{}
	permissions, err := OpenWindowsSocketPermissions(WindowsSocketPermissionsConfig{Directory: filepath.Join(t.TempDir(), "permissions"), Generation: 1, Mode: WindowsSocketsProtected, Underlay: grants})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := permissions.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() {
		if err := sockets.Close(); err != nil {
			t.Error(err)
		}
	})
	factory, err := sockets.Factory(permissions.Datagram)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := newTransportOwner(ProductionConfig{MuxFactory: factory, STUNIPv4Pool: []netip.AddrPort{target}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	})
	binding := path.Binding
	snapshot := networkstate.Snapshot{
		Links:  []networkstate.Link{{Index: int(binding.Index), NativeID: binding.LUID, Up: true, Addresses: []netip.Prefix{netip.PrefixFrom(binding.Source, 32)}}},
		Routes: []networkstate.Route{{Family: networkstate.FamilyIPv4, LinkIndex: int(binding.Index), PreferredSource: binding.Source}},
	}
	for cycle := 1; cycle <= 10; cycle++ {
		if err := permissions.Advance(ctx, networkstate.Generation(cycle)); err != nil {
			t.Fatal(err)
		}
		if err := owner.Activate(ctx, networkstate.State{Generation: networkstate.Generation(cycle), Snapshot: snapshot}); err != nil {
			t.Fatal("build", cycle, err)
		}
		g := owner.Current()
		if err := owner.Commit(g.ID()); err != nil {
			t.Fatal("commit", cycle, err)
		}
		t.Cleanup(func() {
			if err := g.Close(); err != nil {
				t.Error(err)
			}
		})
		observed := g.Observations()
		if len(observed) != 1 || observed[0].NetworkGeneration != g.ID() || !observed[0].MappedEndpoint.IsValid() {
			t.Fatal("missing generation-scoped STUN observation", observed)
		}
		session, err := g.NewPreparedSession(ctx)
		if err != nil {
			t.Fatal("fresh Prepare", cycle, err)
		}
		if len(session.LocalDescription().Candidates) == 0 {
			t.Fatal("Prepare has no candidates")
		}
		if err := session.Close(); err != nil {
			t.Fatal(err)
		}
		if err := g.Close(); err != nil {
			t.Fatal(err)
		}
		if len(sockets.entries) != 0 || len(permissions.entries) != 0 {
			t.Fatal("closed generation retained UDP owner")
		}
		if err := grants.Advance(ctx, uint64(cycle)); err != nil {
			t.Fatal("protection lost", err)
		}
	}
	t.Log("10 production TransportOwner/STUN/fresh ICE Prepare/owned UDP teardown cycles PASS")
}
