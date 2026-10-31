//go:build linux

// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

// hydra-test-internet serves deterministic lab HTTP/UDP/DNS, never production traffic.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Irviodotnet/Irvio/internal/controlsocket"
)

const (
	ioTimeout       = 5 * time.Second
	maxDNSPacket    = 4096
	maxObservations = 4096
)

type options struct {
	expectedEgress                       string
	mode, kind, target, listen4, listen6 string
	source                               string
	ready, release, observations, result string
	bindDevice                           string
	tcpPorts, udpPorts                   string
	httpPort, udpPort, dnsPort           int
	payloadBytes                         int
	socketMark                           uint
	probeTimeout                         time.Duration
}

type observation struct {
	Kind           string `json:"kind"`
	Family         string `json:"family"`
	ObservedSource string `json:"observed_source"`
	Destination    string `json:"destination"`
	AtUnixNano     int64  `json:"at_unix_nano"`
}

type probeResult struct {
	Kind           string `json:"kind"`
	Target         string `json:"target"`
	LocalEndpoint  string `json:"local_endpoint"`
	ObservedSource string `json:"observed_source,omitempty"`
	DNSAnswer      string `json:"dns_answer,omitempty"`
	Success        bool   `json:"success"`
}

type recorder struct {
	mu    sync.Mutex
	file  *os.File
	count int
}

func main() {
	var value options
	flag.StringVar(&value.mode, "mode", "", "server, probe, stream-probe, or fragment")
	flag.StringVar(&value.expectedEgress, "expected-egress", "", "required egress IP for the bounded ordinary UDP stream")
	flag.StringVar(&value.kind, "kind", "", "http, udp, dns-udp, or dns-tcp")
	flag.StringVar(&value.target, "target", "", "probe target host:port")
	flag.StringVar(&value.source, "source", "", "synthetic source address for raw fragment mode")
	flag.StringVar(&value.listen4, "listen4", "", "concrete IPv4 listen address")
	flag.StringVar(&value.listen6, "listen6", "", "concrete IPv6 listen address")
	flag.IntVar(&value.httpPort, "http-port", 18080, "HTTP port")
	flag.IntVar(&value.udpPort, "udp-port", 18081, "UDP echo port")
	flag.IntVar(&value.dnsPort, "dns-port", 1053, "DNS UDP/TCP port")
	flag.StringVar(&value.tcpPorts, "tcp-ports", "", "additional comma-separated TCP echo ports")
	flag.StringVar(&value.udpPorts, "udp-ports", "", "additional comma-separated UDP echo ports")
	flag.IntVar(&value.payloadBytes, "payload-bytes", 32, "UDP probe payload bytes")
	flag.StringVar(&value.ready, "ready", "", "server readiness file")
	flag.StringVar(&value.release, "release", "", "server release file")
	flag.StringVar(&value.observations, "observations", "", "bounded JSONL observation file")
	flag.StringVar(&value.result, "result", "", "probe JSON result path")
	flag.StringVar(&value.bindDevice, "bind-device", "", "test-only physical output interface")
	flag.UintVar(&value.socketMark, "socket-mark", 0, "Irvio-owned SO_MARK for a control probe")
	flag.DurationVar(&value.probeTimeout, "probe-timeout", ioTimeout, "bounded probe timeout")
	flag.Parse()
	var err error
	switch value.mode {
	case "server":
		err = serve(value)
	case "probe":
		err = probe(value)
	case "stream-probe":
		err = streamProbe(value)
	case "fragment":
		err = sendFragments(value)
	default:
		err = errors.New("mode must be server or probe")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func sendFragments(config options) error {
	source, err := netip.ParseAddr(config.source)
	if err != nil || !source.IsValid() || source.Is4In6() {
		return errors.New("fragment mode requires a concrete source")
	}
	target, err := netip.ParseAddrPort(config.target)
	if err != nil || target.Addr().Is4In6() || target.Addr().Is4() != source.Is4() {
		return errors.New("fragment source and target families differ")
	}
	var protocol byte
	switch config.kind {
	case "tcp":
		protocol = unix.IPPROTO_TCP
	case "udp":
		protocol = unix.IPPROTO_UDP
	default:
		return errors.New("fragment kind must be tcp or udp")
	}
	segment := fragmentTransportSegment(source, target, protocol)
	const firstLength = 16
	fragments := [][]byte{segment[:firstLength], segment[firstLength:]}
	if source.Is4() {
		fd, err := unix.Socket(unix.AF_INET, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.IPPROTO_RAW)
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		if err := unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_HDRINCL, 1); err != nil {
			return err
		}
		destination := target.Addr().As4()
		sockaddr := &unix.SockaddrInet4{Addr: destination}
		for index := len(fragments) - 1; index >= 0; index-- {
			packet := ipv4Fragment(source.As4(), destination, protocol, 0x4844, index*firstLength, index == 0, fragments[index])
			if err := unix.Sendto(fd, packet, 0, sockaddr); err != nil {
				return fmt.Errorf("send IPv4 fragment %d: %w", index, err)
			}
		}
	} else {
		fd, err := unix.Socket(unix.AF_INET6, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.IPPROTO_RAW)
		if err != nil {
			return err
		}
		defer unix.Close(fd)
		if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_HDRINCL, 1); err != nil {
			return err
		}
		destination := target.Addr().As16()
		sockaddr := &unix.SockaddrInet6{Addr: destination}
		for index := len(fragments) - 1; index >= 0; index-- {
			packet := ipv6Fragment(source.As16(), destination, protocol, 0x48445241, index*firstLength, index == 0, fragments[index])
			if err := unix.Sendto(fd, packet, 0, sockaddr); err != nil {
				return fmt.Errorf("send IPv6 fragment %d: %w", index, err)
			}
		}
	}
	if config.result != "" {
		return writeAtomic(config.result, []byte("sent\n"))
	}
	return nil
}

func fragmentTransportSegment(source netip.Addr, target netip.AddrPort, protocol byte) []byte {
	payload := []byte("hydra-safety-fragment-regression")
	headerLength := 8
	if protocol == unix.IPPROTO_TCP {
		headerLength = 20
	}
	segment := make([]byte, headerLength+len(payload))
	binary.BigEndian.PutUint16(segment[0:2], 40000)
	binary.BigEndian.PutUint16(segment[2:4], target.Port())
	if protocol == unix.IPPROTO_TCP {
		binary.BigEndian.PutUint32(segment[4:8], 1)
		segment[12] = 5 << 4
		segment[13] = 0x02
		binary.BigEndian.PutUint16(segment[14:16], 65535)
	} else {
		binary.BigEndian.PutUint16(segment[4:6], uint16(len(segment)))
	}
	copy(segment[headerLength:], payload)
	checksumOffset := 6
	if protocol == unix.IPPROTO_TCP {
		checksumOffset = 16
	}
	binary.BigEndian.PutUint16(segment[checksumOffset:checksumOffset+2], transportChecksum(source, target.Addr(), protocol, segment))
	return segment
}

func transportChecksum(source, destination netip.Addr, protocol byte, segment []byte) uint16 {
	var pseudo []byte
	if source.Is4() {
		src, dst := source.As4(), destination.As4()
		pseudo = append(pseudo, src[:]...)
		pseudo = append(pseudo, dst[:]...)
		pseudo = append(pseudo, 0, protocol)
		pseudo = binary.BigEndian.AppendUint16(pseudo, uint16(len(segment)))
	} else {
		src, dst := source.As16(), destination.As16()
		pseudo = append(pseudo, src[:]...)
		pseudo = append(pseudo, dst[:]...)
		pseudo = binary.BigEndian.AppendUint32(pseudo, uint32(len(segment)))
		pseudo = append(pseudo, 0, 0, 0, protocol)
	}
	return checksum(append(pseudo, segment...))
}

func ipv4Fragment(source, destination [4]byte, protocol byte, id uint16, offset int, more bool, payload []byte) []byte {
	packet := make([]byte, 20+len(payload))
	packet[0] = 0x45
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	binary.BigEndian.PutUint16(packet[4:6], id)
	fragmentField := uint16(offset / 8)
	if more {
		fragmentField |= 0x2000
	}
	binary.BigEndian.PutUint16(packet[6:8], fragmentField)
	packet[8], packet[9] = 64, protocol
	copy(packet[12:16], source[:])
	copy(packet[16:20], destination[:])
	binary.BigEndian.PutUint16(packet[10:12], checksum(packet[:20]))
	copy(packet[20:], payload)
	return packet
}

func ipv6Fragment(source, destination [16]byte, protocol byte, id uint32, offset int, more bool, payload []byte) []byte {
	packet := make([]byte, 48+len(payload))
	packet[0] = 0x60
	binary.BigEndian.PutUint16(packet[4:6], uint16(8+len(payload)))
	packet[6], packet[7] = 44, 64
	copy(packet[8:24], source[:])
	copy(packet[24:40], destination[:])
	packet[40] = protocol
	fragmentField := uint16(offset/8) << 3
	if more {
		fragmentField |= 1
	}
	binary.BigEndian.PutUint16(packet[42:44], fragmentField)
	binary.BigEndian.PutUint32(packet[44:48], id)
	copy(packet[48:], payload)
	return packet
}

func checksum(payload []byte) uint16 {
	var sum uint32
	for index := 0; index+1 < len(payload); index += 2 {
		sum += uint32(binary.BigEndian.Uint16(payload[index : index+2]))
	}
	if len(payload)%2 != 0 {
		sum += uint32(payload[len(payload)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	value := ^uint16(sum)
	if value == 0 {
		return 0xffff
	}
	return value
}

func serve(config options) error {
	if config.ready == "" || config.release == "" || config.observations == "" || config.httpPort < 1 || config.httpPort > 65535 || config.udpPort < 1 || config.udpPort > 65535 || config.dnsPort < 1 || config.dnsPort > 65535 {
		return errors.New("invalid server configuration")
	}
	if config.listen4 == "" && config.listen6 == "" {
		return errors.New("at least one listen address is required")
	}
	tcpPorts, err := parsePortList(config.tcpPorts)
	if err != nil {
		return fmt.Errorf("TCP ports: %w", err)
	}
	udpPorts, err := parsePortList(config.udpPorts)
	if err != nil {
		return fmt.Errorf("UDP ports: %w", err)
	}
	file, err := os.OpenFile(config.observations, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	record := &recorder{file: file}
	defer file.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var closers []io.Closer
	var workers sync.WaitGroup
	for _, item := range []struct {
		network string
		address string
	}{
		{"4", config.listen4}, {"6", config.listen6},
	} {
		if item.address == "" {
			continue
		}
		address, parseErr := netip.ParseAddr(item.address)
		if parseErr != nil || !address.IsValid() || address.IsUnspecified() || address.IsMulticast() || address.Is4In6() || address.Is4() != (item.network == "4") {
			return fmt.Errorf("invalid IPv%s listen address", item.network)
		}
		httpListener, listenErr := net.Listen("tcp"+item.network, net.JoinHostPort(address.String(), fmt.Sprint(config.httpPort)))
		if listenErr != nil {
			closeAll(closers)
			return listenErr
		}
		udpListener, listenErr := net.ListenUDP("udp"+item.network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(address, uint16(config.udpPort))))
		if listenErr != nil {
			_ = httpListener.Close()
			closeAll(closers)
			return listenErr
		}
		dnsUDP, listenErr := net.ListenUDP("udp"+item.network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(address, uint16(config.dnsPort))))
		if listenErr != nil {
			_ = httpListener.Close()
			_ = udpListener.Close()
			closeAll(closers)
			return listenErr
		}
		dnsTCP, listenErr := net.Listen("tcp"+item.network, net.JoinHostPort(address.String(), fmt.Sprint(config.dnsPort)))
		if listenErr != nil {
			_ = httpListener.Close()
			_ = udpListener.Close()
			_ = dnsUDP.Close()
			closeAll(closers)
			return listenErr
		}
		closers = append(closers, httpListener, udpListener, dnsUDP, dnsTCP)
		workers.Add(4)
		go serveHTTP(ctx, &workers, httpListener, item.network, record)
		go serveUDP(ctx, &workers, udpListener, item.network, record)
		go serveDNSUDP(ctx, &workers, dnsUDP, item.network, record)
		go serveDNSTCP(ctx, &workers, dnsTCP, item.network, record)
		for _, port := range tcpPorts {
			listener, listenErr := net.Listen("tcp"+item.network, net.JoinHostPort(address.String(), fmt.Sprint(port)))
			if listenErr != nil {
				cancel()
				closeAll(closers)
				workers.Wait()
				return listenErr
			}
			closers = append(closers, listener)
			workers.Add(1)
			go serveTCP(ctx, &workers, listener, item.network, record)
		}
		for _, port := range udpPorts {
			listener, listenErr := net.ListenUDP("udp"+item.network, net.UDPAddrFromAddrPort(netip.AddrPortFrom(address, port)))
			if listenErr != nil {
				cancel()
				closeAll(closers)
				workers.Wait()
				return listenErr
			}
			closers = append(closers, listener)
			workers.Add(1)
			go serveUDP(ctx, &workers, listener, item.network, record)
		}
	}
	if err := writeAtomic(config.ready, []byte("ready\n")); err != nil {
		cancel()
		closeAll(closers)
		workers.Wait()
		return err
	}
	for {
		if _, err := os.Stat(config.release); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	closeAll(closers)
	workers.Wait()
	return nil
}

func (r *recorder) add(kind, family string, remote, local net.Addr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count >= maxObservations {
		return
	}
	entry := observation{Kind: kind, Family: "udp" + family, ObservedSource: remote.String(), Destination: local.String(), AtUnixNano: time.Now().UTC().UnixNano()}
	if kind == "tcp" || strings.HasPrefix(kind, "http") || kind == "dns-tcp" {
		entry.Family = "tcp" + family
	}
	encoded, _ := json.Marshal(entry)
	_, _ = r.file.Write(append(encoded, '\n'))
	_ = r.file.Sync()
	r.count++
}

func serveHTTP(ctx context.Context, workers *sync.WaitGroup, listener net.Listener, family string, record *recorder) {
	defer workers.Done()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		_ = connection.SetDeadline(time.Now().Add(ioTimeout))
		request, err := bufio.NewReader(io.LimitReader(connection, 4096)).ReadString('\n')
		if err == nil && strings.HasPrefix(request, "GET ") {
			record.add("http", family, connection.RemoteAddr(), connection.LocalAddr())
			body, _ := json.Marshal(probeResult{Kind: "http", ObservedSource: connection.RemoteAddr().String(), Success: true})
			_, _ = fmt.Fprintf(connection, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
		}
		_ = connection.Close()
	}
}

func serveTCP(ctx context.Context, workers *sync.WaitGroup, listener net.Listener, family string, record *recorder) {
	defer workers.Done()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		_ = connection.SetDeadline(time.Now().Add(ioTimeout))
		var token [32]byte
		if _, err := io.ReadFull(connection, token[:]); err == nil {
			record.add("tcp", family, connection.RemoteAddr(), connection.LocalAddr())
			body, _ := json.Marshal(probeResult{Kind: "tcp", ObservedSource: connection.RemoteAddr().String(), Success: true})
			_, _ = connection.Write(body)
		}
		_ = connection.Close()
	}
}

func serveUDP(ctx context.Context, workers *sync.WaitGroup, connection *net.UDPConn, family string, record *recorder) {
	defer workers.Done()
	buffer := make([]byte, 65535)
	for {
		n, source, err := connection.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		if n < 1 {
			continue
		}
		record.add("udp", family, source, connection.LocalAddr())
		body, _ := json.Marshal(probeResult{Kind: "udp", ObservedSource: source.String(), Success: true})
		_, _ = connection.WriteToUDP(body, source)
	}
}

func serveDNSUDP(ctx context.Context, workers *sync.WaitGroup, connection *net.UDPConn, family string, record *recorder) {
	defer workers.Done()
	buffer := make([]byte, maxDNSPacket)
	for {
		n, source, err := connection.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		response, err := dnsResponse(buffer[:n])
		if err != nil {
			continue
		}
		record.add("dns-udp", family, source, connection.LocalAddr())
		_, _ = connection.WriteToUDP(response, source)
	}
}

func serveDNSTCP(ctx context.Context, workers *sync.WaitGroup, listener net.Listener, family string, record *recorder) {
	defer workers.Done()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		_ = connection.SetDeadline(time.Now().Add(ioTimeout))
		var length [2]byte
		if _, err = io.ReadFull(connection, length[:]); err == nil {
			size := int(binary.BigEndian.Uint16(length[:]))
			if size >= 12 && size <= maxDNSPacket {
				request := make([]byte, size)
				if _, err = io.ReadFull(connection, request); err == nil {
					if response, responseErr := dnsResponse(request); responseErr == nil {
						record.add("dns-tcp", family, connection.RemoteAddr(), connection.LocalAddr())
						binary.BigEndian.PutUint16(length[:], uint16(len(response)))
						_, _ = connection.Write(append(length[:], response...))
					}
				}
			}
		}
		_ = connection.Close()
	}
}

func probe(config options) error {
	if config.result == "" || config.target == "" || config.probeTimeout <= 0 || config.payloadBytes < 1 || config.payloadBytes > 60000 {
		return errors.New("probe requires target and result")
	}
	ctx, cancel := context.WithTimeout(context.Background(), config.probeTimeout)
	defer cancel()
	var result probeResult
	var err error
	switch config.kind {
	case "http":
		result, err = probeHTTP(ctx, config.target, probeDialer(config))
	case "tcp":
		result, err = probeTCP(ctx, config.target, probeDialer(config))
	case "udp":
		result, err = probeUDP(ctx, config.target, config.payloadBytes, probeDialer(config))
	case "dns-udp", "dns-tcp":
		result, err = probeDNS(ctx, config.kind, config.target, probeDialer(config))
	default:
		err = errors.New("unsupported probe kind")
	}
	if err != nil {
		return err
	}
	result.Target = config.target
	result.Success = true
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(config.result, append(encoded, '\n'))
}

func probeTCP(ctx context.Context, target string, dialer *net.Dialer) (probeResult, error) {
	connection, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return probeResult{}, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Until(deadline(ctx))))
	var token [32]byte
	if _, err = rand.Read(token[:]); err != nil {
		return probeResult{}, err
	}
	if _, err = connection.Write(token[:]); err != nil {
		return probeResult{}, err
	}
	var remote probeResult
	if err := json.NewDecoder(io.LimitReader(connection, 4096)).Decode(&remote); err != nil {
		return probeResult{}, err
	}
	return probeResult{Kind: "tcp", LocalEndpoint: connection.LocalAddr().String(), ObservedSource: remote.ObservedSource}, nil
}

func probeHTTP(ctx context.Context, target string, dialer *net.Dialer) (probeResult, error) {
	connection, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return probeResult{}, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(ioTimeout))
	if _, err = fmt.Fprintf(connection, "GET /hydra HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", target); err != nil {
		return probeResult{}, err
	}
	reader := bufio.NewReader(io.LimitReader(connection, 8192))
	status, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(status, " 200 ") {
		return probeResult{}, errors.New("HTTP probe did not receive 200")
	}
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return probeResult{}, readErr
		}
		if line == "\r\n" {
			break
		}
	}
	var remote probeResult
	if err := json.NewDecoder(reader).Decode(&remote); err != nil {
		return probeResult{}, err
	}
	return probeResult{Kind: "http", LocalEndpoint: connection.LocalAddr().String(), ObservedSource: remote.ObservedSource}, nil
}

func probeUDP(ctx context.Context, target string, payloadBytes int, dialer *net.Dialer) (probeResult, error) {
	connection, err := dialer.DialContext(ctx, "udp", target)
	if err != nil {
		return probeResult{}, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(time.Until(deadline(ctx))))
	payload := make([]byte, payloadBytes)
	if _, err = rand.Read(payload); err != nil {
		return probeResult{}, err
	}
	if _, err = connection.Write(payload); err != nil {
		return probeResult{}, err
	}
	var buffer [1024]byte
	n, err := connection.Read(buffer[:])
	if err != nil {
		return probeResult{}, err
	}
	var remote probeResult
	if err := json.Unmarshal(buffer[:n], &remote); err != nil {
		return probeResult{}, err
	}
	return probeResult{Kind: "udp", LocalEndpoint: connection.LocalAddr().String(), ObservedSource: remote.ObservedSource}, nil
}

func deadline(ctx context.Context) time.Time {
	value, ok := ctx.Deadline()
	if !ok {
		return time.Now().Add(ioTimeout)
	}
	return value
}

func parsePortList(raw string) ([]uint16, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	seen := make(map[uint16]struct{})
	var ports []uint16
	for _, value := range strings.Split(raw, ",") {
		parsed, err := netip.ParseAddrPort("127.0.0.1:" + strings.TrimSpace(value))
		if err != nil || parsed.Port() == 0 {
			return nil, fmt.Errorf("invalid port %q", value)
		}
		if _, exists := seen[parsed.Port()]; exists {
			continue
		}
		seen[parsed.Port()] = struct{}{}
		ports = append(ports, parsed.Port())
	}
	return ports, nil
}

func probeDNS(ctx context.Context, kind, target string, dialer *net.Dialer) (probeResult, error) {
	network := "udp"
	if kind == "dns-tcp" {
		network = "tcp"
	}
	connection, err := dialer.DialContext(ctx, network, target)
	if err != nil {
		return probeResult{}, err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(ioTimeout))
	request := dnsQuery(0x4844)
	if network == "tcp" {
		var length [2]byte
		binary.BigEndian.PutUint16(length[:], uint16(len(request)))
		if _, err = connection.Write(append(length[:], request...)); err != nil {
			return probeResult{}, err
		}
		if _, err = io.ReadFull(connection, length[:]); err != nil {
			return probeResult{}, err
		}
		response := make([]byte, int(binary.BigEndian.Uint16(length[:])))
		if len(response) > maxDNSPacket {
			return probeResult{}, errors.New("oversized DNS response")
		}
		if _, err = io.ReadFull(connection, response); err != nil {
			return probeResult{}, err
		}
		return validateDNSProbe(kind, connection.LocalAddr().String(), response)
	}
	if _, err = connection.Write(request); err != nil {
		return probeResult{}, err
	}
	var response [maxDNSPacket]byte
	n, err := connection.Read(response[:])
	if err != nil {
		return probeResult{}, err
	}
	return validateDNSProbe(kind, connection.LocalAddr().String(), response[:n])
}

func probeDialer(config options) *net.Dialer {
	if config.bindDevice == "" && config.socketMark == 0 {
		return &net.Dialer{}
	}
	markControl := controlsocket.Control(uint32(config.socketMark))
	return &net.Dialer{Control: func(network, address string, raw syscall.RawConn) error {
		if config.bindDevice != "" {
			var bindErr error
			if err := raw.Control(func(fd uintptr) {
				bindErr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, config.bindDevice)
			}); err != nil {
				return err
			}
			if bindErr != nil {
				return bindErr
			}
		}
		if config.socketMark != 0 {
			return markControl(network, address, raw)
		}
		return nil
	}}
}

func dnsQuery(id uint16) []byte {
	message := make([]byte, 12)
	binary.BigEndian.PutUint16(message[0:2], id)
	binary.BigEndian.PutUint16(message[2:4], 0x0100)
	binary.BigEndian.PutUint16(message[4:6], 1)
	for _, label := range []string{"hydra", "test"} {
		message = append(message, byte(len(label)))
		message = append(message, label...)
	}
	message = append(message, 0, 0, 1, 0, 1)
	return message
}

func dnsResponse(request []byte) ([]byte, error) {
	if len(request) < 17 || binary.BigEndian.Uint16(request[4:6]) != 1 {
		return nil, errors.New("invalid DNS question")
	}
	questionEnd := 12
	for {
		if questionEnd >= len(request) {
			return nil, errors.New("truncated DNS name")
		}
		length := int(request[questionEnd])
		questionEnd++
		if length == 0 {
			break
		}
		if length > 63 || questionEnd+length > len(request) {
			return nil, errors.New("invalid DNS label")
		}
		questionEnd += length
	}
	if questionEnd+4 > len(request) {
		return nil, errors.New("truncated DNS question")
	}
	questionEnd += 4
	response := append([]byte(nil), request[:questionEnd]...)
	binary.BigEndian.PutUint16(response[2:4], 0x8180)
	binary.BigEndian.PutUint16(response[6:8], 1)
	response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 30, 0, 4, 198, 51, 100, 77)
	return response, nil
}

func validateDNSProbe(kind, local string, response []byte) (probeResult, error) {
	if len(response) < 12 || binary.BigEndian.Uint16(response[0:2]) != 0x4844 || binary.BigEndian.Uint16(response[2:4])&0x000f != 0 || binary.BigEndian.Uint16(response[6:8]) != 1 {
		return probeResult{}, errors.New("invalid DNS response")
	}
	return probeResult{Kind: kind, LocalEndpoint: local, DNSAnswer: "198.51.100.77"}, nil
}

func closeAll(closers []io.Closer) {
	for index := len(closers) - 1; index >= 0; index-- {
		_ = closers[index].Close()
	}
}

func writeAtomic(path string, contents []byte) error {
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, contents, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
