// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfirewall

import (
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/windows"
)

type controlScope struct {
	Owner, Tunnel          windows.GUID
	Application, Interface string
	Endpoint               netip.AddrPort
	Pool                   netip.Prefix
	Physical               bool   `json:",omitempty"`
	Protocol               uint32 `json:",omitempty"`
}

type controlRecord struct {
	Version uint32
	Rule    *controlScope `json:",omitempty"`
}

type controlJournal interface {
	Read() (controlRecord, error)
	Write(controlRecord) error
	Close() error
}

type controlOps interface {
	Install() error
	Close() error
}

// OwnedControl journals before mutation and retains ownership on failed cleanup for a safe retry.
type OwnedControl struct {
	mu             sync.Mutex
	rule           controlOps
	scope          controlScope
	journal        controlJournal
	active, closed bool
	closeErr       error
}

// OpenOwnedControl reclaims only recorded scope, then captures the current ready tunnel generation.
func OpenOwnedControl(directory string, luid uint64, endpoint netip.AddrPort, pool netip.Prefix) (*OwnedControl, error) {
	return openOwnedRule(directory, func(id windows.GUID) (*Control, error) {
		return NewControl(id, luid, endpoint, pool)
	})
}

func openOwnedRule(directory string, construct func(windows.GUID) (*Control, error)) (*OwnedControl, error) {
	j, err := openControlJournal(directory)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*OwnedControl, error) { return nil, errors.Join(err, j.Close()) }
	if err := recoverControl(j); err != nil {
		return fail(err)
	}
	id, err := windows.GenerateGUID()
	if err != nil {
		return fail(err)
	}
	c, err := construct(id)
	if err != nil {
		return fail(err)
	}
	s := controlScope{Owner: c.owner, Tunnel: c.guid, Application: c.application, Interface: c.iface, Endpoint: c.endpoint, Pool: c.pool, Physical: c.physical, Protocol: c.protocol}
	return &OwnedControl{rule: c, scope: s, journal: j}, nil
}

func (m *OwnedControl) Install() error {
	if m == nil {
		return fmt.Errorf("control firewall owner required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("control firewall owner closed")
	}
	if !m.active {
		if err := m.journal.Write(controlRecord{Version: 1, Rule: &m.scope}); err != nil {
			return err
		}
		m.active = true
	}
	return m.rule.Install()
}

func (m *OwnedControl) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return m.closeErr
	}
	if m.active {
		if err := m.rule.Close(); err != nil {
			return err
		}
		if err := m.journal.Write(controlRecord{Version: 1}); err != nil {
			return err
		}
		m.active = false
	}
	m.closeErr = m.journal.Close()
	m.closed = true
	return m.closeErr
}

// RecoverControl removes unchanged recorded rules even when the old adapter or executable is gone.
func RecoverControl(directory string) (result error) {
	j, err := openControlJournal(directory)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, j.Close()) }()
	return recoverControl(j)
}

func recoverControl(j controlJournal) error {
	r, err := j.Read()
	if err != nil {
		return err
	}
	if err := validateControlRecord(r); err != nil {
		return err
	}
	if r.Rule == nil {
		return nil
	}
	if err := r.Rule.control().Close(); err != nil {
		return err
	}
	return j.Write(controlRecord{Version: 1})
}

func (s controlScope) control() *Control {
	if s.Physical {
		return &Control{owner: s.Owner, name: "IRVIO.Inbound." + s.Owner.String(), application: s.Application, iface: s.Interface,
			guid: s.Tunnel, endpoint: s.Endpoint, physical: true, protocol: s.Protocol, description: "IRVIO physical inbound v1; interface=" + s.Tunnel.String()}
	}
	return &Control{owner: s.Owner, name: "IRVIO.Control." + s.Owner.String(), application: s.Application, iface: s.Interface,
		guid: s.Tunnel, endpoint: s.Endpoint, pool: s.Pool, description: "IRVIO TCP control v1; tunnel=" + s.Tunnel.String()}
}

func validateControlRecord(r controlRecord) error {
	if r.Version != 1 {
		return fmt.Errorf("unsupported control firewall journal version")
	}
	if r.Rule == nil {
		return nil
	}
	s := r.Rule
	if s.Owner == (windows.GUID{}) || s.Tunnel == (windows.GUID{}) || !filepath.IsAbs(s.Application) ||
		len(s.Application) > 4096 || len(s.Interface) == 0 || len(s.Interface) > 256 || strings.ContainsAny(s.Application+s.Interface, "\x00\r\n") {
		return fmt.Errorf("invalid firewall journal identity")
	}
	if s.Physical {
		if !validInboundEndpoint(s.Endpoint, s.Protocol) || s.Pool.IsValid() {
			return fmt.Errorf("invalid physical firewall journal scope")
		}
		return nil
	}
	if s.Protocol != 0 || !s.Endpoint.Addr().Is4() || !s.Endpoint.Addr().IsPrivate() || s.Endpoint.Port() == 0 ||
		!s.Pool.IsValid() || !s.Pool.Addr().Is4() || !s.Pool.Addr().IsPrivate() || s.Pool != s.Pool.Masked() || s.Pool.Bits() < 16 || !s.Pool.Contains(s.Endpoint.Addr()) {
		return fmt.Errorf("invalid control firewall journal scope")
	}
	return nil
}
