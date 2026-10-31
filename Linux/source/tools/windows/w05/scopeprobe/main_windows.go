// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Irviodotnet/Irvio/internal/wfp"
	"github.com/Irviodotnet/Irvio/internal/winroute"
	"golang.org/x/sys/windows"
)

const target = "192.168.23.2"

type result struct {
	Stage    string
	TCP, UDP bool
}

func emit(r any) error { return json.NewEncoder(os.Stdout).Encode(r) }

func dial(proto string) (net.Conn, error) {
	port := "49494"
	if proto == "udp" {
		port = "49495"
	}
	d := net.Dialer{Timeout: 1500 * time.Millisecond}
	if proto == "udp" && os.Getenv("IRVIO_SCOPE_PORT") != "" {
		p, err := strconv.ParseUint(os.Getenv("IRVIO_SCOPE_PORT"), 10, 16)
		if err != nil || p == 0 {
			return nil, fmt.Errorf("invalid probe UDP port")
		}
		d.LocalAddr = &net.UDPAddr{Port: int(p)}
	}
	return d.Dial(proto+"4", net.JoinHostPort(target, port))
}

func echo(c net.Conn, stage string) bool {
	if c == nil {
		return false
	}
	_ = c.SetDeadline(time.Now().Add(1500 * time.Millisecond))
	k, err := windows.GenerateGUID()
	if err != nil {
		return false
	}
	msg := "irvio-nat-wfp-" + stage + "-" + k.String() + "\n"
	if _, err := io.WriteString(c, msg); err != nil {
		return false
	}
	buffer := make([]byte, len(msg))
	_, err = io.ReadFull(c, buffer)
	return err == nil && string(buffer) == msg
}

func fresh(stage string) result {
	r := result{Stage: stage}
	for _, proto := range []string{"tcp", "udp"} {
		c, err := dial(proto)
		ok := false
		if err == nil {
			ok = echo(c, stage)
			c.Close()
		}
		if proto == "tcp" {
			r.TCP = ok
		} else {
			r.UDP = ok
		}
	}
	return r
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) == 3 && os.Args[1] == "echo" {
		return emit(fresh(os.Args[2]))
	}
	if len(os.Args) == 2 && os.Args[1] == "held" {
		tcp, err := dial("tcp")
		if err != nil {
			return err
		}
		defer tcp.Close()
		udp, err := dial("udp")
		if err != nil {
			return err
		}
		defer udp.Close()
		if err := emit(result{Stage: "held-baseline", TCP: echo(tcp, "held-baseline"), UDP: echo(udp, "held-baseline")}); err != nil {
			return err
		}
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			return fmt.Errorf("missing held-flow probe command")
		}
		return emit(result{Stage: "held-protected", TCP: echo(tcp, "held-protected"), UDP: echo(udp, "held-protected")})
	}
	if len(os.Args) != 3 || os.Args[1] != "run" {
		return fmt.Errorf("run <second-executable> | echo <stage> | held")
	}
	return check(os.Args[2])
}

func check(other string) (err error) {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	app, err := wfp.ApplicationIDForPath(self)
	if err != nil {
		return err
	}
	otherApp, err := wfp.ApplicationIDForPath(other)
	if err != nil || app == otherApp {
		return fmt.Errorf("second executable must have a distinct native application identity: %v", err)
	}
	path, err := winroute.ResolveUnderlay(netip.MustParseAddr(target), 0, nil)
	if err != nil {
		return err
	}
	loop := windows.MibIfRow2{InterfaceIndex: 1}
	if err := windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &loop); err != nil {
		return err
	}
	if loop.InterfaceLuid == path.Binding.LUID {
		return fmt.Errorf("negative control must use a different interface")
	}
	s, err := wfp.NewStore(wfp.Config{Provider: windows.GUID{Data1: 0xa24ffa91, Data2: 0xaf90, Data3: 0x4782, Data4: [8]byte{0x92, 0x89, 0xee, 0x19, 0x62, 0x53, 0x2a, 0x71}}, Sublayer: windows.GUID{Data1: 0xba2c934e, Data2: 0xb9f1, Data3: 0x4a42, Data4: [8]byte{0xa1, 0x4e, 0x2e, 0x10, 0xf3, 0xf6, 0x24, 0x4a}}, Name: "IRVIO W05 scoped oracle", Marker: "irvio-w05-scope-v1", ServiceName: "IRVIO-W05-Owner", Weight: 49100})
	if err != nil {
		return err
	}
	ctx := context.Background()
	old, err := s.Inspect(ctx)
	if err != nil || old.Installed {
		return fmt.Errorf("existing scoped fixture: %v", err)
	}
	defer func() {
		if e := s.Remove(ctx); e != nil {
			err = fmt.Errorf("cleanup: %v; run: %v", e, err)
			return
		}
		after, e := s.Inspect(ctx)
		if e != nil || after.Installed {
			err = fmt.Errorf("cleanup residue: %+v %v", after, e)
			return
		}
		_ = emit(map[string]any{"cleanup": true})
	}()
	probe := func(exe, stage string, want bool) error {
		cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		command := exec.CommandContext(cctx, exe, "echo", stage)
		command.Env = append(os.Environ(), "IRVIO_SCOPE_PORT=49596")
		b, e := command.Output()
		if e != nil {
			return fmt.Errorf("%s subprocess: %w", stage, e)
		}
		var r result
		if e = json.Unmarshal(b, &r); e != nil {
			return e
		}
		_ = emit(r)
		if r.TCP != want || r.UDP != want {
			return fmt.Errorf("%s: unexpected TCP/UDP result %+v", stage, r)
		}
		return nil
	}
	if err := probe(self, "baseline-self", true); err != nil {
		return err
	}
	if err := probe(other, "baseline-other", true); err != nil {
		return err
	}
	heldctx, cancelHeld := context.WithTimeout(ctx, 45*time.Second)
	defer cancelHeld()
	held := exec.CommandContext(heldctx, other, "held")
	held.Stderr = os.Stderr
	out, err := held.StdoutPipe()
	if err != nil {
		return err
	}
	in, err := held.StdinPipe()
	if err != nil {
		return err
	}
	if err := held.Start(); err != nil {
		return err
	}
	defer func() { in.Close(); _ = held.Process.Kill(); _ = held.Wait() }()
	decoder := json.NewDecoder(out)
	var r result
	if err := decoder.Decode(&r); err != nil || !r.TCP || !r.UDP {
		return fmt.Errorf("held baseline: %+v %v", r, err)
	}
	_ = emit(r)
	var rules []wfp.Rule
	for _, layer := range []windows.GUID{wfp.ConnectV4, wfp.TransportV4} {
		for i, proto := range []uint8{6, 17} {
			key, _ := windows.GenerateGUID()
			block := wfp.Rule{Key: key, Layer: layer, Action: wfp.Block, Priority: 10, Remote: netip.MustParsePrefix(target + "/32"), Protocol: proto, RemotePort: uint16(49494 + i)}
			rules = append(rules, block)
			allow := block
			allow.Key, _ = windows.GenerateGUID()
			allow.Action = wfp.Permit
			allow.Priority = 20
			allow.LocalInterface = path.Binding.LUID
			allow.LocalAddress = path.Binding.Source
			if proto == 17 {
				allow.LocalPort = 49596
			}
			if layer == wfp.ConnectV4 {
				allow.Application = app
			}
			rules = append(rules, allow)
		}
	}
	install := func(luid uint64) error {
		for i := range rules {
			if rules[i].Action == wfp.Permit {
				rules[i].LocalInterface = luid
			}
		}
		if err := s.Replace(ctx, rules); err != nil {
			return err
		}
		v, e := s.Inspect(ctx)
		if e != nil || !v.Active || len(v.Rules) != len(rules) {
			return fmt.Errorf("scoped policy readback: %v", e)
		}
		_ = emit(map[string]any{"active": true, "luid": luid, "rules": len(v.Rules)})
		return nil
	}
	if err := install(path.Binding.LUID); err != nil {
		return err
	}
	if _, err := io.WriteString(in, "probe\n"); err != nil {
		return err
	}
	if err := decoder.Decode(&r); err != nil {
		return err
	}
	_ = emit(r)
	if r.TCP || r.UDP {
		return fmt.Errorf("previously open foreign flow escaped scoped policy")
	}
	if err := probe(self, "allowed-self", true); err != nil {
		return err
	}
	if err := probe(other, "blocked-other", false); err != nil {
		return err
	}
	for i := range rules {
		if rules[i].Action == wfp.Permit {
			rules[i].LocalAddress = netip.MustParseAddr("192.0.2.253")
		}
	}
	if err := install(path.Binding.LUID); err != nil {
		return err
	}
	if err := probe(self, "wrong-local-address", false); err != nil {
		return err
	}
	for i := range rules {
		if rules[i].Action == wfp.Permit {
			rules[i].LocalAddress = path.Binding.Source
			rules[i].LocalPort = 1
		}
	}
	if err := install(path.Binding.LUID); err != nil {
		return err
	}
	if err := probe(self, "wrong-local-port", false); err != nil {
		return err
	}
	for i := range rules {
		if rules[i].Action == wfp.Permit {
			rules[i].LocalPort = 0
			if rules[i].Protocol == 17 {
				rules[i].LocalPort = 49596
			}
		}
	}
	if err := install(loop.InterfaceLuid); err != nil {
		return err
	}
	if err := probe(self, "wrong-interface", false); err != nil {
		return err
	}
	if err := install(path.Binding.LUID); err != nil {
		return err
	}
	if err := probe(self, "restored-interface", true); err != nil {
		return err
	}
	if err := s.Remove(ctx); err != nil {
		return err
	}
	if err := probe(other, "restored-other", true); err != nil {
		return err
	}
	return emit(map[string]any{"result": "PASS", "application_paths_distinct": !strings.EqualFold(self, other)})
}
