// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
)

func TestSocketMuxConfigValidation(t *testing.T) {
	valid := ipv4Config()
	tests := map[string]func(*Config){
		"no family": func(config *Config) {
			config.IPv4Policy = FamilyDisabled
		},
		"invalid policy": func(config *Config) {
			config.IPv4Policy = FamilyPolicy(99)
		},
		"negative port": func(config *Config) {
			config.Port = -1
		},
		"large port": func(config *Config) {
			config.Port = 65536
		},
		"zero control queue": func(config *Config) {
			config.ControlQueueCapacity = 0
		},
		"negative data queue": func(config *Config) {
			config.DataQueueCapacity = -1
		},
		"zero maximum": func(config *Config) {
			config.MaxDatagramSize = 0
		},
		"large maximum": func(config *Config) {
			config.MaxDatagramSize = MaxUDPPayloadSize + 1
		},
		"invalid IPv4 bind": func(config *Config) {
			config.IPv4Bind = netip.Addr{}
		},
		"IPv6 used for IPv4": func(config *Config) {
			config.IPv4Bind = netip.IPv6Loopback()
		},
		"IPv4-mapped IPv6 used for IPv4": func(config *Config) {
			config.IPv4Bind = netip.MustParseAddr("::ffff:127.0.0.1")
		},
		"IPv4 used for IPv6": func(config *Config) {
			config.IPv4Policy = FamilyDisabled
			config.IPv6Policy = FamilyRequired
			config.IPv6Bind = netip.MustParseAddr("127.0.0.1")
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			config := valid
			mutate(&config)
			if _, err := New(context.Background(), config); err == nil {
				t.Fatal("invalid config was accepted")
			}
		})
	}
	if _, err := New(nil, valid); err == nil {
		t.Fatal("nil context was accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(canceled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled construction error = %v", err)
	}
}

func TestSocketMuxNilClassifierDefaultsToData(t *testing.T) {
	config := ipv4Config()
	config.Classifier = nil
	mux := newTestMux(t, config)
	sender := listenTestUDP(t, IPv4)
	destination, _ := mux.LocalAddr(IPv4)
	if _, err := sender.WriteToUDPAddrPort([]byte("unknown"), destination); err != nil {
		t.Fatal(err)
	}
	datagram := readLogical(t, mux.Data())
	if string(datagram.Payload) != "unknown" {
		t.Fatalf("default-classified payload = %q", datagram.Payload)
	}
	eventually(t, func() bool {
		stats := mux.Stats()
		return stats.DataEnqueued == 1 && stats.ControlEnqueued == 0
	})
}

func TestSocketMuxRequiredFamilyUnavailable(t *testing.T) {
	occupied, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 0)))
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	config := ipv4Config()
	config.Port = int(localAddrPort(t, occupied).Port())
	if _, err := New(context.Background(), config); err == nil {
		t.Fatal("required occupied IPv4 family was accepted")
	}
}

func TestSocketMuxPartialConstructionFailureReleasesIPv4(t *testing.T) {
	requireIPv6(t)
	var lastErr error
	for range 32 {
		reserved4, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 0)))
		if err != nil {
			t.Fatal(err)
		}
		port := localAddrPort(t, reserved4).Port()
		occupied6, err := net.ListenUDP("udp6", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.IPv6Loopback(), port)))
		if err != nil {
			_ = reserved4.Close()
			lastErr = err
			continue
		}
		if err := reserved4.Close(); err != nil {
			_ = occupied6.Close()
			t.Fatal(err)
		}
		config := dualStackConfig()
		config.Port = int(port)
		if _, err := New(context.Background(), config); err == nil {
			_ = occupied6.Close()
			t.Fatal("construction succeeded with occupied required IPv6 port")
		}
		probe, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port)))
		_ = occupied6.Close()
		if err == nil {
			_ = probe.Close()
			return
		}
		lastErr = err
	}
	t.Fatalf("could not prove partially opened IPv4 socket was released after 32 reserved-port attempts: %v", lastErr)
}

func TestSocketMuxFixedPortBinding(t *testing.T) {
	probe, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), 0)))
	if err != nil {
		t.Fatal(err)
	}
	port := localAddrPort(t, probe).Port()
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	config := ipv4Config()
	config.Port = int(port)
	mux := newTestMux(t, config)
	if mux.Port() != port {
		t.Fatalf("bound port = %d, want %d", mux.Port(), port)
	}
}

func TestSocketMuxPortZeroRetriesCollision(t *testing.T) {
	requireIPv6(t)
	config := dualStackConfig()
	var ipv6Attempts atomic.Int32
	listener := func(ctx context.Context, family AddressFamily, bind netip.Addr, port uint16) (*net.UDPConn, error) {
		if family == IPv6 && ipv6Attempts.Add(1) <= 2 {
			return nil, errAddressInUse
		}
		return listenPhysical(ctx, family, bind, port)
	}
	mux, err := newWithListener(context.Background(), config, listener)
	if err != nil {
		t.Fatal(err)
	}
	defer mux.Close()
	if got := ipv6Attempts.Load(); got != 3 {
		t.Fatalf("IPv6 bind attempts = %d, want 3", got)
	}
	if mux.ipv4.local.Port() != mux.ipv6.local.Port() {
		t.Fatal("retry did not produce a common port")
	}
}

func TestSocketMuxPortZeroRetryIsBoundedAndLeakFree(t *testing.T) {
	requireIPv6(t)
	config := dualStackConfig()
	var ipv6Attempts int
	var openedIPv4 []*net.UDPConn
	listener := func(ctx context.Context, family AddressFamily, bind netip.Addr, port uint16) (*net.UDPConn, error) {
		if family == IPv6 {
			ipv6Attempts++
			return nil, errAddressInUse
		}
		conn, err := listenPhysical(ctx, family, bind, port)
		if err == nil {
			openedIPv4 = append(openedIPv4, conn)
		}
		return conn, err
	}
	if _, err := newWithListener(context.Background(), config, listener); !errors.Is(err, errAddressInUse) {
		t.Fatalf("construction error = %v", err)
	}
	if ipv6Attempts != portBindAttempts {
		t.Fatalf("IPv6 attempts = %d, want %d", ipv6Attempts, portBindAttempts)
	}
	for index, conn := range openedIPv4 {
		if _, err := conn.WriteToUDPAddrPort([]byte{0}, netip.MustParseAddrPort("127.0.0.1:9")); !errors.Is(err, net.ErrClosed) {
			t.Fatalf("retry IPv4 socket %d remains usable: %v", index, err)
		}
	}
}
