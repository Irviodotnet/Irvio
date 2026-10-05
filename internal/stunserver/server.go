// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunserver

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"

	"github.com/pion/stun/v3"
)

const DefaultPort uint16 = 3478

// Server serves Basic Binding separately; peer STUN/ICE/AWG remain on IrvioSocketMux.
type Server struct {
	conn *net.UDPConn
	done chan struct{}
	once sync.Once
	err  error
}

// Listen serves Basic Binding; public UDP/3478 must reach this socket even when bound to a wildcard.
func Listen(bind netip.Addr, port uint16) (*Server, error) {
	if !bind.IsValid() || !bind.Is4() {
		return nil, fmt.Errorf("STUN bind must be an IPv4 address")
	}
	conn, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(bind, port)))
	if err != nil {
		return nil, fmt.Errorf("listen public STUN UDP/%d: %w", port, err)
	}
	server := &Server{conn: conn, done: make(chan struct{})}
	go server.serve()
	return server, nil
}

func (s *Server) Addr() netip.AddrPort { return s.conn.LocalAddr().(*net.UDPAddr).AddrPort() }

func (s *Server) serve() {
	defer close(s.done)
	buffer := make([]byte, 512)
	for {
		count, source, err := s.conn.ReadFromUDPAddrPort(buffer)
		if err != nil {
			return
		}
		if count < 20 || count == len(buffer) {
			continue
		}
		request := stun.NewWithOptions(stun.WithStrict(true))
		if request.UnmarshalBinary(buffer[:count]) != nil || request.Type != stun.BindingRequest {
			continue
		}
		response, err := stun.Build(stun.BindingSuccess)
		if err != nil {
			continue
		}
		response.TransactionID = request.TransactionID
		response.WriteTransactionID()
		mapped := stun.XORMappedAddress{IP: net.IP(source.Addr().AsSlice()), Port: int(source.Port())}
		if mapped.AddTo(response) != nil {
			continue
		}
		_, _ = s.conn.WriteToUDPAddrPort(response.Raw, source)
	}
}

func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.err = s.conn.Close()
		<-s.done
	})
	if errors.Is(s.err, net.ErrClosed) {
		return nil
	}
	return s.err
}
