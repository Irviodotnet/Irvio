// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"time"

	"github.com/Irviodotnet/Irvio/internal/wfp"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

const serviceName = "IRVIO-W05-Owner"

type ownerService struct{}

func (ownerService) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for r := range requests {
		switch r.Cmd {
		case svc.Stop, svc.Shutdown:
			return false, 0
		case svc.Interrogate:
			status <- r.CurrentStatus
		}
	}
	return false, 0
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) == 4 && os.Args[1] == "remove-fixture" {
		p, err := windows.GUIDFromString(os.Args[2])
		if err != nil {
			return err
		}
		sl, err := windows.GUIDFromString(os.Args[3])
		if err != nil {
			return err
		}
		s, err := wfp.NewStore(wfp.Config{Provider: p, Sublayer: sl, Name: "IRVIO WFP test", Marker: "irvio-wfp-test-v1", ServiceName: serviceName, Weight: 49000})
		if err != nil {
			return err
		}
		return s.Remove(context.Background())
	}
	if len(os.Args) != 2 {
		return fmt.Errorf("install-hold | inspect | remove | service")
	}
	if os.Args[1] == "service" {
		return svc.Run(serviceName, ownerService{})
	}
	s, err := wfp.NewStore(wfp.Config{
		Provider: windows.GUID{Data1: 0x477ca9c4, Data2: 0xc196, Data3: 0x43e6, Data4: [8]byte{0x91, 0x8a, 0xdd, 0x23, 0xa4, 0xe5, 0xfb, 0x71}},
		Sublayer: windows.GUID{Data1: 0xff48e16a, Data2: 0xf3d9, Data3: 0x4689, Data4: [8]byte{0x9c, 0x1f, 0xc9, 0x90, 0xa8, 0x14, 0x0d, 0xfe}},
		Name:     "IRVIO W05 isolated oracle policy", Marker: "irvio-w05-oracle-v1", ServiceName: serviceName, Weight: 49000,
	})
	if err != nil {
		return err
	}
	ctx := context.Background()
	switch os.Args[1] {
	case "remove":
		return s.Remove(ctx)
	case "inspect":
		v, err := s.Inspect(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(v)
	case "install-hold":
		old, err := s.Inspect(ctx)
		if err != nil {
			return err
		}
		if old.Installed {
			return fmt.Errorf("existing fixture policy; refusing overwrite")
		}
		var rules []wfp.Rule
		for i, layer := range []windows.GUID{wfp.ConnectV4, wfp.TransportV4} {
			for j, protocol := range []uint8{6, 17} {
				k := windows.GUID{Data1: 0xca4cd680 + uint32(i*2+j), Data2: 0xdf48, Data3: 0x4a65, Data4: [8]byte{0xa3, 0x74, 0x46, 0x77, 0xa4, 0x81, 0xee, 0xdd}}
				rules = append(rules, wfp.Rule{Key: k, Layer: layer, Priority: 10, Action: wfp.Block, Remote: netip.MustParsePrefix("192.168.23.2/32"), Protocol: protocol, RemotePort: uint16(49494 + j)})
			}
		}
		if err := s.Replace(ctx, rules); err != nil {
			return err
		}
		v, err := s.Inspect(ctx)
		if err != nil {
			return err
		}
		if !v.Active || len(v.Rules) != 4 {
			return fmt.Errorf("native readback not ready")
		}
		if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
			return err
		}
		for {
			time.Sleep(time.Hour)
		}
	default:
		return fmt.Errorf("unknown command")
	}
}
