// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package ipforward

import (
	"context"
	"errors"
	"testing"
)

type fakeBackend struct {
	values map[string][]byte
	writes int
	failAt int
}

func (f *fakeBackend) Read(path string) ([]byte, error) {
	return append([]byte(nil), f.values[path]...), nil
}
func (f *fakeBackend) Write(path string, value []byte) error {
	f.writes++
	if f.failAt == f.writes {
		return errors.New("injected")
	}
	f.values[path] = append([]byte(nil), value...)
	return nil
}

func TestEnableRestoresExactValues(t *testing.T) {
	back := &fakeBackend{values: map[string][]byte{ipv4Path: []byte("0\n"), ipv6Path: []byte("1\n")}}
	manager, err := enable(context.Background(), Config{IPv4: true, IPv6: true}, back)
	if err != nil {
		t.Fatal(err)
	}
	if string(back.values[ipv4Path]) != "1" || string(back.values[ipv6Path]) != "1\n" {
		t.Fatalf("enabled=%q,%q", back.values[ipv4Path], back.values[ipv6Path])
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if string(back.values[ipv4Path]) != "0\n" || string(back.values[ipv6Path]) != "1\n" {
		t.Fatalf("restored=%q,%q", back.values[ipv4Path], back.values[ipv6Path])
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEnableRollsBackPartialFailure(t *testing.T) {
	back := &fakeBackend{values: map[string][]byte{ipv4Path: []byte("0"), ipv6Path: []byte("0")}, failAt: 2}
	if _, err := enable(context.Background(), Config{IPv4: true, IPv6: true}, back); err == nil {
		t.Fatal("expected failure")
	}
	if string(back.values[ipv4Path]) != "0" {
		t.Fatalf("IPv4 not restored: %q", back.values[ipv4Path])
	}
}
