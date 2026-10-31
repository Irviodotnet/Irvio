// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"sync/atomic"
	"time"

	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/socketpolicy"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
	"github.com/multiformats/go-multiaddr"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func probeHTTP(target netip.Addr, binding *winroute.Binding) error {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	var conn net.Conn
	var err error
	address := net.JoinHostPort(target.String(), "49494")
	if binding == nil {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp4", address)
	} else {
		factory, factoryErr := controlsocket.DialerForBindings(func(context.Context, netip.Addr) (winroute.Binding, error) { return *binding, nil })
		if factoryErr != nil {
			return factoryErr
		}
		dialer, factoryErr := factory(multiaddr.StringCast("/ip4/" + target.String() + "/tcp/49494"))
		if factoryErr != nil {
			return factoryErr
		}
		conn, err = dialer.DialContext(ctx, "tcp4", address)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(1500 * time.Millisecond))
	if _, err := fmt.Fprint(conn, "GET / HTTP/1.0\r\nHost: lab\r\n\r\n"); err != nil {
		return err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return err
	}
	if len(line) < 5 || line[:5] != "HTTP/" {
		return fmt.Errorf("invalid HTTP oracle reply")
	}
	return nil
}

func probeUDP(target netip.Addr, binding *winroute.Binding) error {
	listen := net.ListenConfig{}
	source := "0.0.0.0:0"
	if binding != nil {
		listen.Control = binding.Control
		source = net.JoinHostPort(binding.Source.String(), "0")
	}
	conn, err := listen.ListenPacket(context.Background(), "udp4", source)
	if err != nil {
		return err
	}
	defer conn.Close()
	return exchangeUDP(conn, target)
}

func exchangeUDP(conn net.PacketConn, target netip.Addr) error {
	conn.SetDeadline(time.Now().Add(1500 * time.Millisecond))
	token := fmt.Sprintf("irvio-nat-routepin-%d", time.Now().UnixNano())
	destination := net.UDPAddrFromAddrPort(netip.AddrPortFrom(target, 49495))
	if _, err := conn.WriteTo([]byte(token), destination); err != nil {
		return err
	}
	buffer := make([]byte, 128)
	n, from, err := conn.ReadFrom(buffer)
	if err != nil {
		return err
	}
	if string(buffer[:n]) != token || from.String() != destination.String() {
		return fmt.Errorf("UDP oracle reply mismatch")
	}
	return nil
}

func newMux(source netip.Addr) (*socketmux.Mux, error) {
	config := socketmux.DefaultConfig()
	config.IPv4Bind = source
	config.IPv6Policy = socketmux.FamilyDisabled
	mux, err := socketmux.New(context.Background(), config)
	if err != nil {
		return nil, err
	}
	if err := mux.SetMark(socketpolicy.Underlay); err != nil {
		mux.Close()
		return nil, err
	}
	return mux, nil
}

func exchangeMux(mux *socketmux.Mux, target netip.Addr) error {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	token := fmt.Sprintf("irvio-nat-mux-%d", time.Now().UnixNano())
	destination := netip.AddrPortFrom(target, 49495)
	if _, err := mux.Data().WriteTo(ctx, []byte(token), destination); err != nil {
		return err
	}
	packet, err := mux.Data().Read(ctx)
	if err != nil {
		return err
	}
	if string(packet.Payload) != token || packet.Source != destination {
		return fmt.Errorf("mux oracle reply mismatch")
	}
	return nil
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: routepin-probe.exe <oracle-ipv4>")
	}
	target, err := netip.ParseAddr(os.Args[1])
	if err != nil || !target.Is4() {
		return fmt.Errorf("invalid oracle IPv4")
	}
	baseline, err := winroute.ResolveUnderlay(target, 0, nil)
	if err != nil {
		return err
	}
	if err := probeHTTP(target, &baseline.Binding); err != nil {
		return fmt.Errorf("baseline oracle: %w", err)
	}
	device, err := tun.CreateTUN("IRVIO-ROUTE", 1280)
	if err != nil {
		return err
	}
	defer device.Close()
	var tunnelPackets atomic.Uint64
	captureDone := make(chan struct{})
	go func() {
		defer close(captureDone)
		buffer, sizes := make([]byte, 65535), []int{0}
		for {
			if _, err := device.Read([][]byte{buffer}, sizes, 0); err != nil {
				return
			}
			packet := buffer[:sizes[0]]
			if len(packet) < 24 || packet[0]>>4 != 4 {
				continue
			}
			header := int(packet[0]&15) * 4
			if header < 20 || len(packet) < header+4 {
				continue
			}
			if [4]byte(packet[16:20]) != target.As4() {
				continue
			}
			port := binary.BigEndian.Uint16(packet[header+2 : header+4])
			if (packet[9] == 6 && port == 49494) || (packet[9] == 17 && port == 49495) {
				tunnelPackets.Add(1)
			}
		}
	}()
	defer func() { device.Close(); <-captureDone }()
	heldConfig := net.ListenConfig{Control: baseline.Binding.Control}
	held, err := heldConfig.ListenPacket(context.Background(), "udp4", net.JoinHostPort(baseline.Binding.Source.String(), "0"))
	if err != nil {
		return err
	}
	defer held.Close()
	heldMux, err := newMux(baseline.Binding.Source)
	if err != nil {
		return err
	}
	defer heldMux.Close()
	go func() {
		for range device.Events() {
		}
	}()
	luid := uint64(device.(*tun.NativeTun).LUID())
	encoder := json.NewEncoder(os.Stdout)
	encoder.Encode(map[string]any{"stage": "ready", "baseline": baseline, "tun_luid": luid})
	input := bufio.NewScanner(os.Stdin)
	for input.Scan() {
		command := input.Text()
		var operationErr error
		var path winroute.Path
		start := time.Now()
		before := tunnelPackets.Load()
		switch command {
		case "physical":
			operationErr = probeHTTP(target, &baseline.Binding)
		case "ordinary":
			operationErr = probeHTTP(target, nil)
		case "physical-udp":
			operationErr = probeUDP(target, &baseline.Binding)
		case "ordinary-udp":
			operationErr = probeUDP(target, nil)
		case "held-udp":
			operationErr = exchangeUDP(held, target)
		case "held-mux":
			operationErr = exchangeMux(heldMux, target)
		case "new-mux":
			var mux *socketmux.Mux
			mux, operationErr = newMux(baseline.Binding.Source)
			if operationErr == nil {
				operationErr = exchangeMux(mux, target)
				mux.Close()
			}
		case "kernel":
			path, operationErr = winroute.ResolveUnderlay(target, 0, nil)
		case "exclude-tun":
			path, operationErr = winroute.ResolveUnderlay(target, 0, []uint64{luid})
		case "constrained":
			path, operationErr = winroute.ResolveUnderlay(target, baseline.Binding.LUID, []uint64{luid})
		case "close-adapter":
			operationErr = device.Close()
		case "quit":
			return nil
		default:
			return fmt.Errorf("unknown lab command")
		}
		message := ""
		if operationErr != nil {
			message = operationErr.Error()
		}
		if command == "physical" || command == "physical-udp" || command == "held-udp" || command == "held-mux" || command == "new-mux" {
			time.Sleep(250 * time.Millisecond)
		}
		if err := encoder.Encode(map[string]any{"command": command, "success": operationErr == nil, "error": message, "path": path, "duration_ms": time.Since(start).Milliseconds(), "tun_packets": tunnelPackets.Load() - before}); err != nil {
			return err
		}
	}
	return input.Err()
}
