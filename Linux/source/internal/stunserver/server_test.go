// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunserver

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/pion/stun/v3"
)

func TestBasicBindingAndClose(t *testing.T) {
	server, err := Listen(netip.MustParseAddr("127.0.0.1"), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(server.Addr()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	request, err := stun.Build(stun.TransactionID, stun.BindingRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(request.Raw); err != nil {
		t.Fatal(err)
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 512)
	count, err := client.Read(buffer)
	if err != nil {
		t.Fatal(err)
	}
	response := stun.NewWithOptions(stun.WithStrict(true))
	if err := response.UnmarshalBinary(buffer[:count]); err != nil {
		t.Fatal(err)
	}
	if response.Type != stun.BindingSuccess || response.TransactionID != request.TransactionID {
		t.Fatalf("unexpected STUN response type=%v transaction=%v", response.Type, response.TransactionID)
	}
	var mapped stun.XORMappedAddress
	if err := mapped.GetFrom(response); err != nil {
		t.Fatal(err)
	}
	if mapped.Port != client.LocalAddr().(*net.UDPAddr).Port {
		t.Fatalf("mapped port = %d", mapped.Port)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
}
