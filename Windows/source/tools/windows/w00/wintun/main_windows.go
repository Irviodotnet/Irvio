// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
)

// This lab probe exercises the pinned AWG TUN implementation, not a replacement driver.
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 2 || (len(os.Args) == 2 && os.Args[1] != "nat") {
		return fmt.Errorf("usage: wintun-probe.exe [nat]")
	}
	device, err := tun.CreateTUN("IRVIO-W00", 1280)
	if err != nil {
		return err
	}
	defer device.Close()
	go func() {
		for range device.Events() {
		}
	}()
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"stage": "adapter_ready", "name": "IRVIO-W00", "pid": os.Getpid(),
		"luid": device.(*tun.NativeTun).LUID(), "mtu": 1280,
	}); err != nil {
		return err
	}
	// The harness configures the test address before sending this stdin barrier.
	var ready string
	if _, err := fmt.Fscanln(os.Stdin, &ready); err != nil || ready != "ready" {
		return fmt.Errorf("configuration barrier: %q, %v", ready, err)
	}
	if len(os.Args) == 2 && os.Args[1] == "nat" {
		return probeNAT(device)
	}
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(10, 253, 240, 1), Port: 49001})
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := listener.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	payload := []byte("irvio-w00-wintun-io")
	readResult := make(chan error, 1)
	go func() {
		buffer := make([]byte, 65535)
		sizes := []int{0}
		for {
			_, err := device.Read([][]byte{buffer}, sizes, 0)
			if err != nil {
				readResult <- err
				return
			}
			packet := buffer[:sizes[0]]
			if len(packet) >= 28 && packet[0]>>4 == 4 && packet[9] == 17 && bytes.Equal(packet[28:], payload) {
				readResult <- nil
				return
			}
		}
	}()
	if _, err := listener.WriteToUDP(payload, &net.UDPAddr{IP: net.IPv4(10, 253, 240, 2), Port: 49000}); err != nil {
		return err
	}
	select {
	case err := <-readResult:
		if err != nil {
			return fmt.Errorf("kernel-to-userspace read: %w", err)
		}
	case <-time.After(10 * time.Second):
		return fmt.Errorf("kernel-to-userspace read timeout")
	}
	packet := udpPacket(payload)
	if _, err := device.Write([][]byte{packet}, 0); err != nil {
		return fmt.Errorf("userspace-to-kernel write: %w", err)
	}
	buffer := make([]byte, 256)
	n, source, err := listener.ReadFromUDP(buffer)
	if err != nil {
		return fmt.Errorf("injected UDP receive: %w", err)
	}
	if !bytes.Equal(buffer[:n], payload) || !source.IP.Equal(net.IPv4(10, 253, 240, 2)) || source.Port != 49000 {
		return fmt.Errorf("injected UDP mismatch: source=%v bytes=%d", source, n)
	}
	if err := device.Close(); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"stage": "packet_io", "result": "PASS", "read": true, "write": true, "close": true,
	})
}

func udpPacket(payload []byte) []byte {
	return ipv4UDP(payload, [4]byte{10, 253, 240, 2}, [4]byte{10, 253, 240, 1}, 49000, 49001)
}

func ipv4UDP(payload []byte, source, destination [4]byte, sourcePort, destinationPort uint16) []byte {
	packet := make([]byte, 28+len(payload))
	packet[0], packet[8], packet[9] = 0x45, 64, 17
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	copy(packet[12:16], source[:])
	copy(packet[16:20], destination[:])
	var sum uint32
	for index := 0; index < 20; index += 2 {
		sum += uint32(binary.BigEndian.Uint16(packet[index : index+2]))
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + sum>>16
	}
	binary.BigEndian.PutUint16(packet[10:12], ^uint16(sum))
	binary.BigEndian.PutUint16(packet[20:22], sourcePort)
	binary.BigEndian.PutUint16(packet[22:24], destinationPort)
	binary.BigEndian.PutUint16(packet[24:26], uint16(8+len(payload)))
	copy(packet[28:], payload)
	return packet
}

func probeNAT(device tun.Device) error {
	result := make(chan error, 1)
	go func() {
		buffer := make([]byte, 65535)
		sizes := []int{0}
		seen := make(map[byte]bool)
		for len(seen) < 5 {
			if _, err := device.Read([][]byte{buffer}, sizes, 0); err != nil {
				result <- err
				return
			}
			p := buffer[:sizes[0]]
			if len(p) < 28 || p[0] != 0x45 || p[9] != 17 || !bytes.Equal(p[12:16], []byte{192, 168, 23, 2}) {
				continue
			}
			id := p[19]
			if id < 2 || id > 6 || !bytes.Equal(p[16:19], []byte{10, 253, 240}) || binary.BigEndian.Uint16(p[20:22]) != 49495 || binary.BigEndian.Uint16(p[22:24]) != uint16(49000)+uint16(id) {
				continue
			}
			if string(p[28:]) == fmt.Sprintf("irvio-nat-%d", id) {
				seen[id] = true
			}
		}
		result <- nil
	}()
	for id := byte(2); id <= 6; id++ {
		payload := []byte(fmt.Sprintf("irvio-nat-%d", id))
		packet := ipv4UDP(payload, [4]byte{10, 253, 240, id}, [4]byte{192, 168, 23, 2}, 49000+uint16(id), 49495)
		if _, err := device.Write([][]byte{packet}, 0); err != nil {
			return err
		}
	}
	select {
	case err := <-result:
		if err != nil {
			return err
		}
	case <-time.After(15 * time.Second):
		return fmt.Errorf("NAT: did not receive all five translated UDP responses")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"stage": "nat_udp", "result": "PASS", "inner_sources": 5})
}
