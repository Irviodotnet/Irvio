// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux || windows

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/Irviodotnet/Irvio/internal/rendezvous"
	irvioruntime "github.com/Irviodotnet/Irvio/internal/runtime"
)

type warmIdleProbe struct {
	idleOwnerProbe
	warm *rendezvous.WarmDiscovery
}

func (p *warmIdleProbe) WarmDiscovery() *rendezvous.WarmDiscovery { return p.warm }

func TestProductWarmDiscoveryOnlyAfterCleanIdleProbe(t *testing.T) {
	for _, stage := range []string{"success", "query failure", "close failure"} {
		t.Run(stage, func(t *testing.T) {
			d := idleOwnerController(t)
			p := &warmIdleProbe{warm: new(rendezvous.WarmDiscovery)}
			p.query = func(context.Context) (rendezvous.AvailableCountries, error) {
				if stage == "query failure" {
					return rendezvous.AvailableCountries{}, errors.New("query failed")
				}
				return rendezvous.AvailableCountries{}, nil
			}
			if stage == "close failure" {
				p.close = func(context.Context) error { return errors.New("close failed") }
			}
			_, _ = d.queryIdleCountries(context.Background(), p, "bootstrap-A")
			if (d.warmDiscovery == p.warm) != (stage == "success") {
				t.Fatal("unclean probe exported warm hints")
			}
			if stage == "success" && (d.idleProbe != nil || !d.idleWithoutIntent()) {
				t.Fatal("warm cache kept active runtime")
			}
			p.close = nil
			if err := d.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProductWarmDiscoveryBoundToBootstrap(t *testing.T) {
	warm := new(rendezvous.WarmDiscovery)
	d := &desktopController{warmDiscovery: warm, warmBootstrap: []string{"A", "B"}}
	for _, test := range []struct {
		addresses []string
		want      bool
	}{
		{[]string{"A", "B"}, true}, {[]string{"A"}, false}, {[]string{"C", "B"}, false}, {nil, false},
	} {
		config := irvioruntime.DaemonConfig{Bootstrap: test.addresses}
		d.applyWarmDiscovery(&config)
		if (config.M12.WarmDiscovery == warm) != test.want {
			t.Fatal("wrong bootstrap cache binding", test.addresses)
		}
		if config.Exit || config.Production != nil || config.ProtectedMode || config.ProtectAtStart {
			t.Fatal("warm hints changed runtime capabilities")
		}
	}
}
