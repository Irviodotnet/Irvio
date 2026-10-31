// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"context"
	"testing"
	"time"

	"github.com/Irviodotnet/Irvio/internal/rendezvous"
)

type blockedCountries struct{ entered, release chan struct{} }

func (c *blockedCountries) AvailableCountries(ctx context.Context, _ bool) (rendezvous.AvailableCountries, error) {
	close(c.entered)
	select {
	case <-ctx.Done():
		return rendezvous.AvailableCountries{}, ctx.Err()
	case <-c.release:
		return rendezvous.AvailableCountries{Status: rendezvous.AvailableCountriesReady, Codes: []string{"NL"}}, nil
	}
}

func TestCountryLookupDoesNotBlockTrafficOnSameSocket(t *testing.T) {
	cfg := testServerConfig(t)
	country := &blockedCountries{make(chan struct{}), make(chan struct{})}
	cfg.Countries = country
	server, err := NewServer(cfg, &trafficController{})
	if err != nil {
		t.Fatal(err)
	}
	if err = server.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := Dial(context.Background(), cfg.SocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	defer func() {
		select {
		case <-country.release:
		default:
			close(country.release)
		}
	}()
	done := make(chan error, 1)
	go func() { _, err := client.GetAvailableCountries(context.Background(), true); done <- err }()
	select {
	case <-country.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("lookup did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.GetTrafficStats(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetStatus(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetAvailableCountries(ctx, true); err == nil {
		t.Fatal("duplicate country lookup was not bounded")
	}
	close(country.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDisconnectCancelsCountryLookupOnSameSocket(t *testing.T) {
	country := &blockedCountries{make(chan struct{}), make(chan struct{})}
	_, path := startTestServer(t, &fakeController{}, func(cfg *ServerConfig) { cfg.Countries = country })
	client := dialTestClient(t, path)
	done := make(chan error, 1)
	go func() { _, err := client.GetAvailableCountries(context.Background(), true); done <- err }()
	select {
	case <-country.entered:
	case <-time.After(time.Second):
		t.Fatal("lookup did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("lookup was not cancelled")
		}
	case <-ctx.Done():
		t.Fatal("lookup retained after Disconnect")
	}
}
