// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package ipforward

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

const (
	ipv4Path = "/proc/sys/net/ipv4/ip_forward"
	ipv6Path = "/proc/sys/net/ipv6/conf/all/forwarding"
)

// Config selects only the forwarding families Irvio is authorized to enable.
type Config struct {
	IPv4 bool
	IPv6 bool
}

type sysctlBackend interface {
	Read(string) ([]byte, error)
	Write(string, []byte) error
}

type osBackend struct{}

func (osBackend) Read(path string) ([]byte, error)      { return os.ReadFile(path) }
func (osBackend) Write(path string, value []byte) error { return os.WriteFile(path, value, 0) }

type ownedValue struct {
	path     string
	previous []byte
	changed  bool
}

// Manager restores every changed sysctl to its exact prior value.
type Manager struct {
	back sysctlBackend

	mu        sync.Mutex
	values    []ownedValue
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

// Enable transactionally enables selected forwarding families.
func Enable(ctx context.Context, config Config) (*Manager, error) {
	return enable(ctx, config, osBackend{})
}

func enable(ctx context.Context, config Config, back sysctlBackend) (_ *Manager, resultErr error) {
	if ctx == nil || (!config.IPv4 && !config.IPv6) {
		return nil, errors.New("invalid forwarding configuration")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m := &Manager{back: back}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, m.restore())
		}
	}()
	for _, item := range []struct {
		enabled bool
		path    string
	}{{config.IPv4, ipv4Path}, {config.IPv6, ipv6Path}} {
		if !item.enabled {
			continue
		}
		previous, err := back.Read(item.path)
		if err != nil {
			return nil, fmt.Errorf("read forwarding sysctl %s: %w", item.path, err)
		}
		value := ownedValue{path: item.path, previous: append([]byte(nil), previous...)}
		if strings.TrimSpace(string(previous)) != "1" {
			if err := back.Write(item.path, []byte("1")); err != nil {
				return nil, fmt.Errorf("enable forwarding sysctl %s: %w", item.path, err)
			}
			value.changed = true
		}
		m.values = append(m.values, value)
	}
	return m, nil
}

// Close restores changed values and is idempotent.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		m.closeErr = m.restore()
	})
	return m.closeErr
}

func (m *Manager) restore() error {
	var result error
	for index := len(m.values) - 1; index >= 0; index-- {
		value := m.values[index]
		if value.changed {
			result = errors.Join(result, m.back.Write(value.path, value.previous))
		}
	}
	m.values = nil
	return result
}
