// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux

package underlay

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"
	"time"

	"hydra.local/hydra/internal/controlsocket"
	"hydra.local/hydra/internal/linuxroute"
)

const challengeSize = 32

var challengeMagic = [4]byte{'H', 'Y', 'U', '1'}

// MarkedChallengeProbe verifies bidirectional literal-IP reachability through M8 control bypass.
type MarkedChallengeProbe struct {
	Mark   uint32
	Random io.Reader
	Now    func() time.Time
}

func NewMarkedChallengeProbe(mark uint32) (*MarkedChallengeProbe, error) {
	if mark != linuxroute.IrvioBypassMark {
		return nil, ErrInvalidConfig
	}
	return &MarkedChallengeProbe{Mark: mark, Random: rand.Reader, Now: time.Now}, nil
}

func (p *MarkedChallengeProbe) Probe(ctx context.Context, target Target) error {
	if ctx == nil || !target.Endpoint.IsValid() || target.Endpoint.Port() == 0 {
		return ErrInvalidTarget
	}
	address := target.Endpoint.Addr()
	if address.IsUnspecified() || address.IsMulticast() || address.IsLinkLocalUnicast() || address.Is4In6() {
		return ErrInvalidTarget
	}
	request := make([]byte, challengeSize)
	copy(request, challengeMagic[:])
	if _, err := io.ReadFull(p.Random, request[len(challengeMagic):]); err != nil {
		return fmt.Errorf("create underlay challenge: %w", err)
	}
	network := "tcp6"
	if address.Is4() {
		network = "tcp4"
	}
	dialer := net.Dialer{Control: controlsocket.Control(p.Mark)}
	connection, err := dialer.DialContext(ctx, network, target.Endpoint.String())
	if err != nil {
		return err
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return err
		}
	}
	if err := writeAll(connection, request); err != nil {
		return err
	}
	response := make([]byte, len(request))
	if _, err := io.ReadFull(connection, response); err != nil {
		return err
	}
	if !bytes.Equal(response, request) {
		return ErrChallengeMismatch
	}
	return nil
}

type ChallengeServiceConfig struct {
	Listen        string
	Mark          uint32
	Timeout       time.Duration
	MaxConcurrent int
}

// ChallengeService echoes bounded equal-size nonces without amplification.
type ChallengeService struct {
	listener net.Listener
	config   ChallengeServiceConfig
	sem      chan struct{}
	done     chan struct{}
	once     sync.Once
	wait     sync.WaitGroup
}

func NewChallengeService(ctx context.Context, config ChallengeServiceConfig) (*ChallengeService, error) {
	if ctx == nil || config.Mark != linuxroute.IrvioBypassMark ||
		config.Timeout <= 0 || config.MaxConcurrent < 1 || config.MaxConcurrent > 64 {
		return nil, ErrInvalidConfig
	}
	listen := net.ListenConfig{Control: controlsocket.Control(config.Mark)}
	listener, err := listen.Listen(ctx, "tcp", config.Listen)
	if err != nil {
		return nil, fmt.Errorf("listen for underlay challenges: %w", err)
	}
	service := &ChallengeService{
		listener: listener, config: config, sem: make(chan struct{}, config.MaxConcurrent), done: make(chan struct{}),
	}
	service.wait.Add(1)
	go service.serve()
	return service, nil
}

func (s *ChallengeService) Addr() net.Addr { return s.listener.Addr() }

func (s *ChallengeService) serve() {
	defer s.wait.Done()
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				continue
			}
		}
		select {
		case s.sem <- struct{}{}:
			s.wait.Add(1)
			go s.handle(connection)
		default:
			_ = connection.Close()
		}
	}
}

func (s *ChallengeService) handle(connection net.Conn) {
	defer s.wait.Done()
	defer func() { <-s.sem }()
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(s.config.Timeout))
	request := make([]byte, challengeSize)
	if _, err := io.ReadFull(connection, request); err != nil {
		return
	}
	if !bytes.Equal(request[:len(challengeMagic)], challengeMagic[:]) {
		return
	}
	_ = writeAll(connection, request)
}

func (s *ChallengeService) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	s.once.Do(func() {
		close(s.done)
		closeErr = s.listener.Close()
		s.wait.Wait()
	})
	return closeErr
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		n, err := writer.Write(payload)
		if n <= 0 || n > len(payload) {
			return io.ErrShortWrite
		}
		payload = payload[n:]
		if err != nil {
			return err
		}
	}
	return nil
}

var _ func(string, string, syscall.RawConn) error = controlsocket.Control(linuxroute.IrvioBypassMark)
