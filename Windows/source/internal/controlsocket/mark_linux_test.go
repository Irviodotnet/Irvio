// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package controlsocket

import (
	"errors"
	"testing"
)

func TestDialerForMarkRejectsZero(t *testing.T) {
	if _, err := DialerForMark(0); !errors.Is(err, ErrInvalidMark) {
		t.Fatalf("error=%v", err)
	}
}

func TestDialerForMarkProducesDedicatedDialer(t *testing.T) {
	factory, err := DialerForMark(0x48594452)
	if err != nil {
		t.Fatal(err)
	}
	first, err := factory(nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := factory(nil)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || second == nil || first == second {
		t.Fatalf("dialers first=%p second=%p", first, second)
	}
}

func TestControlOnDeviceRejectsEmptyDeviceBeforeSocketAccess(t *testing.T) {
	control := ControlOnDevice(0x48595443, "")
	if err := control("tcp", "127.0.0.1:1", nil); !errors.Is(err, ErrInvalidDevice) {
		t.Fatalf("error=%v", err)
	}
}
