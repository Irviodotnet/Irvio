// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package uibridge

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestRelayOpaqueDuplexAndGUIEOF(t *testing.T) {
	conn, server := net.Pipe()
	defer server.Close()
	input, uiWrite := io.Pipe()
	defer input.Close()
	defer uiWrite.Close()
	uiRead, output := io.Pipe()
	defer uiRead.Close()
	defer output.Close()
	done := make(chan error, 1)
	go func() { done <- Relay(conn, input, output) }()
	payload := bytes.Repeat([]byte{0, 255, 1, 127}, 20000)
	sent := make(chan error, 1)
	go func() { _, err := uiWrite.Write(payload); sent <- err }()
	got := make([]byte, len(payload))
	server.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(server, got); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("request mismatch: %v", err)
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	go func() { _, err := server.Write(payload); sent <- err }()
	if _, err := io.ReadFull(uiRead, got); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("response mismatch: %v", err)
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	uiWrite.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("GUI EOF did not close relay")
	}
	if n, err := server.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Fatalf("EOF sent extra API command: n=%d err=%v", n, err)
	}
}

func TestRelayServiceEOFWakesOwner(t *testing.T) {
	conn, server := net.Pipe()
	input, uiWrite := io.Pipe()
	defer input.Close()
	defer uiWrite.Close()
	done := make(chan error, 1)
	go func() { done <- Relay(conn, input, io.Discard) }()
	server.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("service EOF did not wake helper owner")
	}
}
