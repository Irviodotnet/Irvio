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
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
	"github.com/Irviodotnet/Irvio/internal/socketpolicy"
	"github.com/Irviodotnet/Irvio/internal/tunnel"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun"
)

const oracle = "192.168.23.2:49495"

type observed struct {
	Source          string `json:"source"`
	Destination     string `json:"destination"`
	SourcePort      uint16 `json:"source_port"`
	DestinationPort uint16 `json:"destination_port"`
}

type probe struct {
	device   tun.Device
	physical winroute.Binding
	tunIndex uint32
	mu       sync.Mutex
	packets  map[string][]observed
	readErr  error
	readDone chan struct{}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	path, err := winroute.ResolveUnderlay(netip.MustParseAddr("192.168.23.2"), 0, nil)
	if err != nil {
		return err
	}
	owner, err := tunnel.NewWindows(tunnel.Config{Name: "IRVIO-NATDUAL", Addresses: []netip.Prefix{
		netip.MustParsePrefix("10.253.240.1/24"), netip.MustParsePrefix("10.253.241.2/24"),
	}})
	if err != nil {
		return err
	}
	defer owner.Close()
	device, err := owner.Borrow()
	if err != nil {
		return err
	}
	defer device.Close()
	p := &probe{device: device, physical: path.Binding, tunIndex: uint32(owner.IfIndex()), packets: make(map[string][]observed), readDone: make(chan struct{})}
	go p.read()
	defer func() { device.Close(); <-p.readDone }()
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(map[string]any{"stage": "ready", "ifindex": owner.IfIndex(), "luid": owner.LUID(), "physical": path}); err != nil {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		command := scanner.Text()
		switch command {
		case "forward-inject":
			result := p.injectThrough(p.physical.Index, command)
			if err = enc.Encode(result); err != nil {
				return err
			}
		case "forward-invalid":
			if err = enc.Encode(p.injectThrough(0x7ffffffe, command)); err != nil {
				return err
			}
		case "forward-tun":
			if err = enc.Encode(p.injectThrough(p.tunIndex, command)); err != nil {
				return err
			}
		case "forward-nat":
			if err = enc.Encode(p.injectNAT()); err != nil {
				return err
			}
		case "forward-tcp":
			if err = enc.Encode(p.injectTCP()); err != nil {
				return err
			}
		case "forward-stress":
			if err = enc.Encode(p.stressNAT()); err != nil {
				return err
			}
		case "baseline", "routed", "restored":
			base := uint16(49000)
			if command == "routed" {
				base = 49100
			}
			if command == "restored" {
				base = 49200
			}
			result, err := p.forward(command, base)
			if err != nil {
				return err
			}
			if err = enc.Encode(result); err != nil {
				return err
			}
		case "physical", "physical-repin", "physical-packet", "physical-mux", "ordinary":
			token := fmt.Sprintf("irvio-nat-%s-%d", command, time.Now().UnixNano())
			option, success, err := p.host(command, token)
			result := map[string]any{"stage": command, "success": success, "socket_interface": option, "packets": p.snapshot(token)}
			if err != nil {
				result["error"] = err.Error()
			}
			if err = enc.Encode(result); err != nil {
				return err
			}
		case "quit":
			return nil
		default:
			return fmt.Errorf("unknown lab command %q", command)
		}
	}
	return scanner.Err()
}

func (p *probe) read() {
	defer close(p.readDone)
	buf := make([]byte, 65535)
	sizes := []int{0}
	for {
		_, err := p.device.Read([][]byte{buf}, sizes, 0)
		if err != nil {
			p.mu.Lock()
			p.readErr = err
			p.mu.Unlock()
			return
		}
		packet := buf[:sizes[0]]
		if len(packet) >= 40 && packet[0] == 0x45 && packet[9] == 6 {
			key := fmt.Sprintf("irvio-tcp-source-port-%d", binary.BigEndian.Uint16(packet[20:22]))
			p.mu.Lock()
			p.packets[key] = append(p.packets[key], observed{Source: net.IP(packet[12:16]).String(), Destination: net.IP(packet[16:20]).String(), SourcePort: binary.BigEndian.Uint16(packet[20:22]), DestinationPort: binary.BigEndian.Uint16(packet[22:24])})
			p.mu.Unlock()
			continue
		}
		if len(packet) < 28 || packet[0] != 0x45 || packet[9] != 17 {
			continue
		}
		token := string(packet[28:])
		if !strings.HasPrefix(token, "irvio-nat-") {
			continue
		}
		entry := observed{Source: net.IP(packet[12:16]).String(), Destination: net.IP(packet[16:20]).String(), SourcePort: binary.BigEndian.Uint16(packet[20:22]), DestinationPort: binary.BigEndian.Uint16(packet[22:24])}
		p.mu.Lock()
		p.packets[token] = append(p.packets[token], entry)
		p.mu.Unlock()
	}
}

func (p *probe) snapshot(token string) []observed {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]observed{}, p.packets[token]...)
}

func (p *probe) forward(stage string, base uint16) (map[string]any, error) {
	for id := byte(2); id <= 6; id++ {
		token := fmt.Sprintf("irvio-nat-%s-%d", stage, id)
		packet := udpPacket([]byte(token), [4]byte{10, 253, 240, id}, [4]byte{192, 168, 23, 2}, base+uint16(id), 49495)
		if _, err := p.device.Write([][]byte{packet}, 0); err != nil {
			return nil, err
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		seen := 0
		for id := byte(2); id <= 6; id++ {
			if len(p.snapshot(fmt.Sprintf("irvio-nat-%s-%d", stage, id))) > 0 {
				seen++
			}
		}
		if seen == 5 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	replies, captured := 0, 0
	packets := make(map[string][]observed)
	for id := byte(2); id <= 6; id++ {
		token := fmt.Sprintf("irvio-nat-%s-%d", stage, id)
		entries := p.snapshot(token)
		packets[token] = entries
		for _, entry := range entries {
			if entry.Source == "192.168.23.2" && entry.Destination == fmt.Sprintf("10.253.240.%d", id) && entry.SourcePort == 49495 && entry.DestinationPort == base+uint16(id) {
				replies++
				break
			}
		}
		for _, entry := range entries {
			if entry.Destination == "192.168.23.2" && entry.DestinationPort == 49495 {
				captured++
				break
			}
		}
	}
	p.mu.Lock()
	readErr := p.readErr
	p.mu.Unlock()
	if readErr != nil {
		return nil, readErr
	}
	return map[string]any{"stage": stage, "responses": replies, "upstream_captures": captured, "packets": packets}, nil
}

func (p *probe) host(stage, token string) (uint32, bool, error) {
	if stage == "physical-mux" {
		config := socketmux.DefaultConfig()
		config.IPv4Bind = p.physical.Source
		config.IPv6Policy = socketmux.FamilyDisabled
		mux, err := socketmux.New(context.Background(), config)
		if err != nil {
			return 0, false, err
		}
		defer mux.Close()
		if err = mux.SetMark(socketpolicy.Underlay); err != nil {
			return 0, false, err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		dst := netip.MustParseAddrPort(oracle)
		if _, err = mux.Data().WriteTo(ctx, []byte(token), dst); err != nil {
			return 0, false, err
		}
		reply, err := mux.Data().Read(ctx)
		if err != nil {
			return 0, false, err
		}
		return 0, string(reply.Payload) == token && reply.Source == dst, nil
	}
	if stage == "physical-packet" {
		lc := net.ListenConfig{Control: p.physical.Control}
		conn, err := lc.ListenPacket(context.Background(), "udp4", net.JoinHostPort(p.physical.Source.String(), "0"))
		if err != nil {
			return 0, false, err
		}
		defer conn.Close()
		raw, err := conn.(*net.UDPConn).SyscallConn()
		if err != nil {
			return 0, false, err
		}
		option, err := winroute.SocketInterface(raw, true)
		if err != nil {
			return option, false, err
		}
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err = conn.WriteTo([]byte(token), net.UDPAddrFromAddrPort(netip.MustParseAddrPort(oracle))); err != nil {
			return option, false, err
		}
		buf := make([]byte, 256)
		n, from, err := conn.ReadFrom(buf)
		return option, err == nil && string(buf[:n]) == token && from.String() == oracle, err
	}
	dialer := net.Dialer{Timeout: 2 * time.Second}
	if strings.HasPrefix(stage, "physical") {
		dialer.LocalAddr = &net.UDPAddr{IP: net.IP(p.physical.Source.AsSlice())}
		dialer.Control = p.physical.Control
	} else {
		dialer.LocalAddr = &net.UDPAddr{IP: net.ParseIP("10.253.241.2")}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "udp4", oracle)
	if err != nil {
		return 0, false, err
	}
	defer conn.Close()
	raw, err := conn.(*net.UDPConn).SyscallConn()
	if err != nil {
		return 0, false, err
	}
	if stage == "physical-repin" {
		if err = p.physical.Control("udp4", oracle, raw); err != nil {
			return 0, false, err
		}
	}
	option, err := winroute.SocketInterface(raw, true)
	if err != nil {
		return option, false, err
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = conn.Write([]byte(token)); err != nil {
		return option, false, err
	}
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	return option, err == nil && string(buf[:n]) == token, err
}

func udpPacket(payload []byte, source, dest [4]byte, sourcePort, destPort uint16) []byte {
	p := make([]byte, 28+len(payload))
	p[0], p[8], p[9] = 0x45, 64, 17
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	copy(p[12:16], source[:])
	copy(p[16:20], dest[:])
	var sum uint32
	for i := 0; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(p[i : i+2]))
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	binary.BigEndian.PutUint16(p[10:12], ^uint16(sum))
	binary.BigEndian.PutUint16(p[20:22], sourcePort)
	binary.BigEndian.PutUint16(p[22:24], destPort)
	binary.BigEndian.PutUint16(p[24:26], uint16(8+len(payload)))
	copy(p[28:], payload)
	return p
}
