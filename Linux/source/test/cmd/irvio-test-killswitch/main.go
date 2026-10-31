//go:build linux

// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// hydra-test-killswitch is a bounded M8 lab helper, never a production daemon.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
	tcptransport "github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
	"golang.org/x/sys/unix"

	"github.com/Irviodotnet/Irvio/internal/controlsocket"
	"github.com/Irviodotnet/Irvio/internal/exitfirewall"
	"github.com/Irviodotnet/Irvio/internal/exitselector"
	"github.com/Irviodotnet/Irvio/internal/killswitch"
	"github.com/Irviodotnet/Irvio/internal/linuxroute"
	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

const recoveryProtocol libprotocol.ID = "/hydra/m8/recovery/1.0.0"

type options struct {
	mode, bind4, bind6, remote, ready, result, release string
	tunName, tableName                                 string
	bindDevice, checkpoint, continueFile               string
	wanName                                            string
	port, cycles                                       int
	mark                                               uint
}

type report struct {
	Mode             string                    `json:"mode"`
	PID              int                       `json:"pid"`
	Ensure           killswitch.EnsureResult   `json:"ensure_result,omitempty"`
	Protected        killswitch.Snapshot       `json:"protected_mode,omitempty"`
	Kernel           killswitch.KernelSnapshot `json:"kernel,omitempty"`
	PeerID           string                    `json:"peer_id,omitempty"`
	ListenAddresses  []string                  `json:"listen_addresses,omitempty"`
	Authenticated    bool                      `json:"authenticated,omitempty"`
	MuxPort          uint16                    `json:"mux_port,omitempty"`
	SocketMark       uint32                    `json:"socket_mark,omitempty"`
	AttemptedPackets uint32                    `json:"attempted_packets,omitempty"`
	Remote           string                    `json:"remote,omitempty"`
	CompletedAtNanos int64                     `json:"completed_at_unix_nano"`
	ProbeReachable   bool                      `json:"probe_reachable,omitempty"`
	ProbeLatency     int64                     `json:"probe_latency_nanos,omitempty"`
	Cycles           int                       `json:"cycles,omitempty"`
	FDBefore         int                       `json:"fd_before,omitempty"`
	FDAfter          int                       `json:"fd_after,omitempty"`
	NetlinkBefore    int                       `json:"netlink_before,omitempty"`
	NetlinkAfter     int                       `json:"netlink_after,omitempty"`
	GoroutinesBefore int                       `json:"goroutines_before,omitempty"`
	GoroutinesAfter  int                       `json:"goroutines_after,omitempty"`
}

func main() {
	var config options
	flag.StringVar(&config.mode, "mode", "", "policy, policy-cycles, exit-policy, exit-policy-cycles, libp2p-server, libp2p-client, mux-probe, candidate-probe, marked-tcp-connect, or leak-probe")
	flag.StringVar(&config.bind4, "bind4", "", "concrete IPv4 address")
	flag.StringVar(&config.bind6, "bind6", "", "concrete IPv6 address")
	flag.StringVar(&config.remote, "remote", "", "literal remote endpoint or peer multiaddr")
	flag.StringVar(&config.ready, "ready", "", "readiness JSON path")
	flag.StringVar(&config.result, "result", "", "result JSON path")
	flag.StringVar(&config.release, "release", "", "graceful release path")
	flag.StringVar(&config.tunName, "tun-name", "hydra0", "protected TUN name")
	flag.StringVar(&config.tableName, "table-name", killswitch.DefaultTableName, "owned nftables table")
	flag.StringVar(&config.bindDevice, "bind-device", "", "test-only ordinary physical output interface")
	flag.StringVar(&config.wanName, "wan-name", "wan0", "test-only Exit WAN interface name")
	flag.StringVar(&config.checkpoint, "checkpoint", "", "first leak-probe phase completion path")
	flag.StringVar(&config.continueFile, "continue", "", "leak-probe second-phase barrier")
	flag.IntVar(&config.port, "port", 0, "listen/source port")
	flag.IntVar(&config.cycles, "cycles", 100, "bounded policy lifecycle cycles")
	flag.UintVar(&config.mark, "mark", uint(linuxroute.DefaultOuterMark), "Irvio bypass mark")
	flag.Parse()

	var err error
	switch config.mode {
	case "policy":
		err = runPolicy(config)
	case "policy-cycles":
		err = runPolicyCycles(config)
	case "exit-policy":
		err = runExitPolicy(config)
	case "exit-policy-cycles":
		err = runExitPolicyCycles(config)
	case "libp2p-server":
		err = runLibp2pServer(config)
	case "libp2p-client":
		err = runLibp2pClient(config)
	case "mux-probe":
		err = runMuxProbe(config)
	case "leak-probe":
		err = runLeakProbe(config)
	case "candidate-probe":
		err = runCandidateProbe(config)
	case "marked-tcp-connect":
		err = runMarkedTCPConnect(config)
	default:
		err = errors.New("unsupported mode")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type resourceCounts struct {
	fd, netlink, goroutines int
}

func runPolicyCycles(config options) error {
	if config.result == "" || config.cycles < 1 || config.cycles > 1000 || config.mark == 0 {
		return errors.New("policy-cycles requires result, bounded cycles, and nonzero mark")
	}
	before, err := currentResources()
	if err != nil {
		return err
	}
	for cycle := 0; cycle < config.cycles; cycle++ {
		controller, err := killswitch.New(killswitch.Config{
			Backend: killswitch.NFTBackend{},
			Policy:  killswitch.Policy{TableName: config.tableName, TUNName: config.tunName, BypassMark: uint32(config.mark)},
		})
		if err != nil {
			return err
		}
		if _, err := controller.Protect(context.Background()); err != nil {
			return fmt.Errorf("client policy cycle %d install: %w", cycle, err)
		}
		if snapshot := controller.Snapshot(); !snapshot.Kernel.Complete {
			return fmt.Errorf("client policy cycle %d incomplete", cycle)
		}
		if err := controller.Disconnect(context.Background()); err != nil {
			return fmt.Errorf("client policy cycle %d remove: %w", cycle, err)
		}
		if err := controller.Close(context.Background()); err != nil {
			return fmt.Errorf("client policy cycle %d close: %w", cycle, err)
		}
	}
	after, err := currentResources()
	if err != nil {
		return err
	}
	return writeResourceReport(config, before, after)
}

func runExitPolicy(config options) error {
	if config.ready == "" || config.release == "" {
		return errors.New("exit-policy requires ready and release")
	}
	installation, err := applyTestExitPolicy(config)
	if err != nil {
		return err
	}
	if err := writeReport(config.ready, report{Mode: config.mode, PID: os.Getpid()}); err != nil {
		return errors.Join(err, installation.Close())
	}
	if err := waitForFile(config.release); err != nil {
		return errors.Join(err, installation.Close())
	}
	return installation.Close()
}

func runExitPolicyCycles(config options) error {
	if config.result == "" || config.cycles < 1 || config.cycles > 1000 {
		return errors.New("exit-policy-cycles requires result and bounded cycles")
	}
	before, err := currentResources()
	if err != nil {
		return err
	}
	for cycle := 0; cycle < config.cycles; cycle++ {
		installation, err := applyTestExitPolicy(config)
		if err != nil {
			return fmt.Errorf("Exit policy cycle %d install: %w", cycle, err)
		}
		snapshot, err := installation.Snapshot(context.Background())
		if err != nil || snapshot.TableName != exitfirewall.DefaultTableName {
			return errors.Join(fmt.Errorf("Exit policy cycle %d snapshot", cycle), err, installation.Close())
		}
		if err := installation.Close(); err != nil {
			return fmt.Errorf("Exit policy cycle %d remove: %w", cycle, err)
		}
		if err := installation.Close(); err != nil {
			return fmt.Errorf("Exit policy cycle %d idempotent close: %w", cycle, err)
		}
	}
	after, err := currentResources()
	if err != nil {
		return err
	}
	return writeResourceReport(config, before, after)
}

func applyTestExitPolicy(config options) (exitfirewall.Installation, error) {
	return (exitfirewall.NFTBackend{}).Apply(context.Background(), exitfirewall.Policy{
		TUNName: config.tunName, WANName: config.wanName,
		ClientIPv4: netip.MustParsePrefix("10.77.0.0/24"),
		ClientIPv6: netip.MustParsePrefix("fd77::/64"),
	})
}

func writeResourceReport(config options, before, after resourceCounts) error {
	return writeReport(config.result, report{
		Mode: config.mode, PID: os.Getpid(), Cycles: config.cycles,
		FDBefore: before.fd, FDAfter: after.fd, NetlinkBefore: before.netlink, NetlinkAfter: after.netlink,
		GoroutinesBefore: before.goroutines, GoroutinesAfter: after.goroutines,
	})
}

func currentResources() (resourceCounts, error) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return resourceCounts{}, err
	}
	ownedSockets := make(map[string]struct{})
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err == nil && strings.HasPrefix(target, "socket:[") && strings.HasSuffix(target, "]") {
			ownedSockets[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] = struct{}{}
		}
	}
	netlink := 0
	raw, err := os.ReadFile("/proc/net/netlink")
	if err != nil {
		return resourceCounts{}, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 10 {
			if _, owned := ownedSockets[fields[9]]; owned {
				netlink++
			}
		}
	}
	return resourceCounts{fd: len(entries), netlink: netlink, goroutines: runtime.NumGoroutine()}, nil
}

func runMarkedTCPConnect(config options) error {
	if config.remote == "" || config.result == "" || config.mark == 0 {
		return errors.New("marked-tcp-connect mode requires remote, result, and nonzero mark")
	}
	remote, err := netip.ParseAddrPort(config.remote)
	if err != nil {
		return fmt.Errorf("parse marked TCP endpoint: %w", err)
	}
	networkName := "tcp6"
	if remote.Addr().Is4() {
		networkName = "tcp4"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dialer := net.Dialer{Control: controlsocket.Control(uint32(config.mark))}
	connection, err := dialer.DialContext(ctx, networkName, remote.String())
	if err != nil {
		return fmt.Errorf("connect marked TCP socket: %w", err)
	}
	if err := connection.Close(); err != nil {
		return fmt.Errorf("close marked TCP socket: %w", err)
	}
	return writeReport(config.result, report{
		Mode: config.mode, Remote: remote.String(), ProbeReachable: true, SocketMark: uint32(config.mark),
	})
}

func runCandidateProbe(config options) error {
	if config.remote == "" || config.result == "" || config.mark == 0 {
		return errors.New("candidate-probe mode requires remote, result, and nonzero mark")
	}
	remote, err := netip.ParseAddrPort(config.remote)
	if err != nil {
		return fmt.Errorf("parse CandidateProbe endpoint: %w", err)
	}
	probe, err := exitselector.NewMarkedTCPProbe(uint32(config.mark))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := probe.Probe(ctx, exitselector.Candidate{ControlEndpoint: remote, NetworkEpoch: 1})
	if err != nil {
		return fmt.Errorf("run marked CandidateProbe: %w", err)
	}
	return writeReport(config.result, report{
		Mode: config.mode, Remote: remote.String(), ProbeReachable: result.Reachable,
		ProbeLatency: result.Latency.Nanoseconds(), SocketMark: uint32(config.mark),
	})
}

func runPolicy(config options) error {
	if config.ready == "" || config.release == "" || config.mark == 0 {
		return errors.New("policy mode requires ready, release, and nonzero mark")
	}
	controller, err := killswitch.New(killswitch.Config{
		Backend: killswitch.NFTBackend{},
		Policy:  killswitch.Policy{TableName: config.tableName, TUNName: config.tunName, BypassMark: uint32(config.mark)},
	})
	if err != nil {
		return err
	}
	generation, err := controller.Protect(context.Background())
	if err != nil {
		return err
	}
	snapshot := controller.Snapshot()
	if err := writeReport(config.ready, report{Mode: config.mode, PID: os.Getpid(), Protected: snapshot, Kernel: snapshot.Kernel}); err != nil {
		return err
	}
	if err := waitForFile(config.release); err != nil {
		return err
	}
	if !controller.IsCurrent(generation) {
		return killswitch.ErrStaleGeneration
	}
	if err := controller.Disconnect(context.Background()); err != nil {
		return err
	}
	if config.result != "" {
		return writeReport(config.result, report{Mode: config.mode, PID: os.Getpid(), Protected: controller.Snapshot()})
	}
	return nil
}

func runLibp2pServer(config options) error {
	if config.ready == "" || config.release == "" || config.port < 0 || config.port > 65535 || config.bind4 == "" && config.bind6 == "" {
		return errors.New("invalid libp2p server configuration")
	}
	addresses := make([]string, 0, 2)
	if config.bind4 != "" {
		addresses = append(addresses, "/ip4/"+config.bind4+"/tcp/"+strconv.Itoa(config.port))
	}
	if config.bind6 != "" {
		addresses = append(addresses, "/ip6/"+config.bind6+"/tcp/"+strconv.Itoa(config.port))
	}
	h, err := libp2p.New(libp2p.NoTransports, libp2p.Transport(tcptransport.NewTCPTransport), libp2p.DisableRelay(), libp2p.ListenAddrStrings(addresses...))
	if err != nil {
		return err
	}
	defer h.Close()
	h.SetStreamHandler(recoveryProtocol, func(stream network.Stream) {
		defer stream.Close()
		_ = stream.SetDeadline(time.Now().Add(5 * time.Second))
		line, err := bufio.NewReader(io.LimitReader(stream, 64)).ReadString('\n')
		if err == nil && line == "hydra-m8\n" {
			_, _ = stream.Write([]byte("authenticated\n"))
		}
	})
	listen := make([]string, 0, len(h.Addrs()))
	for _, address := range h.Addrs() {
		listen = append(listen, address.Encapsulate(multiaddr.StringCast("/p2p/"+h.ID().String())).String())
	}
	if err := writeReport(config.ready, report{Mode: config.mode, PID: os.Getpid(), PeerID: h.ID().String(), ListenAddresses: listen}); err != nil {
		return err
	}
	return waitForFile(config.release)
}

func runLibp2pClient(config options) error {
	if config.remote == "" || config.result == "" || config.mark == 0 {
		return errors.New("libp2p client requires literal remote, result, and mark")
	}
	dialer, err := controlsocket.DialerForMark(uint32(config.mark))
	if err != nil {
		return err
	}
	h, err := libp2p.New(libp2p.NoListenAddrs, libp2p.NoTransports, libp2p.Transport(tcptransport.NewTCPTransport, tcptransport.WithDialerForAddr(dialer)), libp2p.DisableRelay())
	if err != nil {
		return err
	}
	defer h.Close()
	address, err := multiaddr.NewMultiaddr(config.remote)
	if err != nil {
		return err
	}
	info, err := peer.AddrInfoFromP2pAddr(address)
	if err != nil {
		return err
	}
	h.Peerstore().AddAddrs(info.ID, info.Addrs, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := h.Connect(ctx, *info); err != nil {
		return err
	}
	stream, err := h.NewStream(ctx, info.ID, recoveryProtocol)
	if err != nil {
		return err
	}
	defer stream.Close()
	_ = stream.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := stream.Write([]byte("hydra-m8\n")); err != nil {
		return err
	}
	line, err := bufio.NewReader(io.LimitReader(stream, 64)).ReadString('\n')
	if err != nil || line != "authenticated\n" || stream.Conn().RemotePeer() != info.ID {
		return errors.New("authenticated literal-IP libp2p exchange failed")
	}
	return writeReport(config.result, report{Mode: config.mode, PID: os.Getpid(), PeerID: h.ID().String(), Authenticated: true, Remote: info.ID.String(), SocketMark: uint32(config.mark)})
}

func runMuxProbe(config options) error {
	if config.remote == "" || config.result == "" || config.mark == 0 || config.bind4 == "" && config.bind6 == "" {
		return errors.New("invalid mux probe configuration")
	}
	remote, err := netip.ParseAddrPort(config.remote)
	if err != nil {
		return err
	}
	muxConfig := socketmux.DefaultConfig()
	muxConfig.Port = config.port
	muxConfig.IPv4Policy = socketmux.FamilyDisabled
	muxConfig.IPv6Policy = socketmux.FamilyDisabled
	if config.bind4 != "" {
		muxConfig.IPv4Bind = netip.MustParseAddr(config.bind4)
		muxConfig.IPv4Policy = socketmux.FamilyRequired
	}
	if config.bind6 != "" {
		muxConfig.IPv6Bind = netip.MustParseAddr(config.bind6)
		muxConfig.IPv6Policy = socketmux.FamilyRequired
	}
	mux, err := socketmux.New(context.Background(), muxConfig)
	if err != nil {
		return err
	}
	defer mux.Close()
	if err := mux.SetMark(uint32(config.mark)); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := mux.Control().WriteTo(ctx, []byte("hydra-m8-control"), remote); err != nil {
		return err
	}
	reply, err := mux.Data().Read(ctx)
	if err != nil {
		return fmt.Errorf("receive marked IrvioSocketMux reply: %w", err)
	}
	if reply.Source != remote || len(reply.Payload) == 0 {
		return errors.New("marked IrvioSocketMux reply did not match the literal endpoint")
	}
	return writeReport(config.result, report{Mode: config.mode, PID: os.Getpid(), MuxPort: mux.Port(), SocketMark: uint32(config.mark), Remote: remote.String()})
}

func runLeakProbe(config options) error {
	if config.remote == "" || config.bindDevice == "" || config.ready == "" || config.checkpoint == "" || config.continueFile == "" || config.result == "" {
		return errors.New("invalid leak probe configuration")
	}
	remote, err := netip.ParseAddrPort(config.remote)
	if err != nil || remote.Addr().Is4In6() {
		return errors.New("invalid leak probe endpoint")
	}
	network := "udp6"
	bind := &net.UDPAddr{IP: net.IPv6unspecified}
	if remote.Addr().Is4() {
		network = "udp4"
		bind = &net.UDPAddr{IP: net.IPv4zero}
	}
	connection, err := net.ListenUDP(network, bind)
	if err != nil {
		return err
	}
	defer connection.Close()
	raw, err := connection.SyscallConn()
	if err != nil {
		return err
	}
	var bindErr error
	if err := raw.Control(func(fd uintptr) {
		bindErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, config.bindDevice)
	}); err != nil {
		return err
	}
	if bindErr != nil {
		return bindErr
	}
	if err := writeReport(config.ready, report{Mode: config.mode, PID: os.Getpid(), Remote: remote.String()}); err != nil {
		return err
	}
	attempted := uint32(0)
	send := func() {
		for range 32 {
			_, _ = connection.WriteToUDPAddrPort([]byte("ordinary-m8-leak-probe"), remote)
			attempted++
		}
	}
	send()
	if err := writeReport(config.checkpoint, report{Mode: config.mode, PID: os.Getpid(), AttemptedPackets: attempted}); err != nil {
		return err
	}
	if err := waitForFile(config.continueFile); err != nil {
		return err
	}
	send()
	return writeReport(config.result, report{Mode: config.mode, PID: os.Getpid(), AttemptedPackets: attempted, Remote: remote.String()})
}

func waitForFile(path string) error {
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func writeReport(path string, value report) error {
	value.CompletedAtNanos = time.Now().UTC().UnixNano()
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
