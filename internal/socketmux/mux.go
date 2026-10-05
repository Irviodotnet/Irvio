// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package socketmux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
)

const (
	initialReadErrorBackoff = 10 * time.Millisecond
	maximumReadErrorBackoff = time.Second
)

type physicalSocket struct {
	family  AddressFamily
	conn    *net.UDPConn
	local   netip.AddrPort
	writeMu sync.Mutex
}

type socketListener func(context.Context, AddressFamily, netip.Addr, uint16) (*net.UDPConn, error)

// Mux owns physical UDP sockets and two isolated logical receive paths.
type Mux struct {
	config     Config
	classifier Classifier
	logger     *slog.Logger
	ipv4       *physicalSocket
	ipv6       *physicalSocket
	port       uint16
	listen     socketListener

	controlQueue chan Datagram
	dataQueue    chan Datagram
	control      *Endpoint
	data         *Endpoint
	done         chan struct{}

	stats        counters
	closed       atomic.Bool
	closeOnce    sync.Once
	closeErr     error
	readers      sync.WaitGroup
	stateMu      sync.Mutex
	writesDone   *sync.Cond
	activeWrites int
	markMu       sync.Mutex
	mark         uint32
	setMark      func(*net.UDPConn, uint32) error
}

// New atomically binds the configured physical sockets and starts one reader per socket.
func New(ctx context.Context, config Config) (*Mux, error) {
	return newWithListener(ctx, config, listenPhysical)
}

func newWithListener(ctx context.Context, config Config, listener socketListener) (*Mux, error) {
	if ctx == nil {
		return nil, fmt.Errorf("create socket mux: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("validate socket mux config: %w", err)
	}
	classifier := config.Classifier
	if classifier == nil {
		classifier = dataClassifier{}
	}
	logger := config.Logger
	if logger == nil {
		logger = slog.Default()
	}
	mux := &Mux{
		config: config, classifier: classifier, logger: logger,
		listen:       listener,
		controlQueue: make(chan Datagram, config.ControlQueueCapacity),
		dataQueue:    make(chan Datagram, config.DataQueueCapacity),
		done:         make(chan struct{}),
		setMark:      setUDPMark,
	}
	mux.writesDone = sync.NewCond(&mux.stateMu)
	mux.control = &Endpoint{mux: mux, receive: mux.controlQueue}
	mux.data = &Endpoint{mux: mux, receive: mux.dataQueue}
	if err := mux.bind(ctx); err != nil {
		return nil, err
	}
	if mux.ipv4 != nil {
		mux.startReader(mux.ipv4)
	}
	if mux.ipv6 != nil {
		mux.startReader(mux.ipv6)
	}
	logger.Info("SOCKETMUX_STARTED", "port", mux.port, "ipv4", mux.ipv4 != nil, "ipv6", mux.ipv6 != nil)
	return mux, nil
}

func (m *Mux) bind(ctx context.Context) error {
	requestedPort := uint16(m.config.Port)
	if requestedPort != 0 {
		return m.bindAttempt(ctx, requestedPort, false)
	}
	var lastErr error
	for attempt := 1; attempt <= portBindAttempts; attempt++ {
		err := m.bindAttempt(ctx, 0, attempt < portBindAttempts)
		if err == nil {
			return nil
		}
		lastErr = err
		if !errors.Is(err, unix.EADDRINUSE) {
			return err
		}
	}
	return fmt.Errorf("bind common ephemeral UDP port after %d attempts: %w", portBindAttempts, lastErr)
}

func (m *Mux) bindAttempt(ctx context.Context, port uint16, retryCollision bool) error {
	m.closeBoundSockets()
	selectedPort := port
	if m.config.IPv4Policy != FamilyDisabled {
		socket, err := m.openSocket(ctx, IPv4, m.config.IPv4Bind, selectedPort)
		if err != nil {
			if m.config.IPv4Policy == FamilyRequired {
				return fmt.Errorf("bind required IPv4 socket: %w", err)
			}
			m.logger.Warn("SOCKETMUX_FAMILY_UNAVAILABLE", "family", IPv4.String(), "reason", err)
		} else {
			m.ipv4 = socket
			selectedPort = socket.local.Port()
		}
	}
	if m.config.IPv6Policy != FamilyDisabled {
		socket, err := m.openSocket(ctx, IPv6, m.config.IPv6Bind, selectedPort)
		if err != nil {
			if errors.Is(err, unix.EADDRINUSE) && port == 0 && retryCollision && m.ipv4 != nil {
				m.closeBoundSockets()
				return fmt.Errorf("bind IPv6 to selected ephemeral port: %w", err)
			}
			if m.config.IPv6Policy == FamilyRequired {
				m.closeBoundSockets()
				return fmt.Errorf("bind required IPv6 socket: %w", err)
			}
			m.logger.Warn("SOCKETMUX_FAMILY_UNAVAILABLE", "family", IPv6.String(), "reason", err)
		} else {
			m.ipv6 = socket
			if selectedPort == 0 {
				selectedPort = socket.local.Port()
			}
		}
	}
	if m.ipv4 == nil && m.ipv6 == nil {
		return fmt.Errorf("bind socket mux: no address family is available")
	}
	if m.ipv4 != nil && m.ipv6 != nil && m.ipv4.local.Port() != m.ipv6.local.Port() {
		m.closeBoundSockets()
		return fmt.Errorf("dual-stack port mismatch: IPv4=%d IPv6=%d", m.ipv4.local.Port(), m.ipv6.local.Port())
	}
	if err := ctx.Err(); err != nil {
		m.closeBoundSockets()
		return err
	}
	if m.ipv4 != nil {
		m.port = m.ipv4.local.Port()
	} else {
		m.port = m.ipv6.local.Port()
	}
	return nil
}

func (m *Mux) openSocket(ctx context.Context, family AddressFamily, bind netip.Addr, port uint16) (*physicalSocket, error) {
	conn, err := m.listen(ctx, family, bind, port)
	if err != nil {
		return nil, err
	}
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		conn.Close()
		return nil, fmt.Errorf("unexpected local address type %T", conn.LocalAddr())
	}
	address := local.AddrPort()
	if !address.IsValid() || address.Port() == 0 {
		conn.Close()
		return nil, fmt.Errorf("invalid bound local endpoint %s", address)
	}
	return &physicalSocket{family: family, conn: conn, local: address}, nil
}

func (m *Mux) closeBoundSockets() {
	if m.ipv4 != nil {
		_ = m.ipv4.conn.Close()
		m.ipv4 = nil
	}
	if m.ipv6 != nil {
		_ = m.ipv6.conn.Close()
		m.ipv6 = nil
	}
}

func (m *Mux) startReader(socket *physicalSocket) {
	m.readers.Add(1)
	go m.receiveLoop(socket)
}

func (m *Mux) receiveLoop(socket *physicalSocket) {
	defer m.readers.Done()
	buffer := make([]byte, m.config.MaxDatagramSize+1)
	backoff := initialReadErrorBackoff
	for {
		n, _, flags, source, err := socket.conn.ReadMsgUDPAddrPort(buffer, nil)
		if err != nil {
			if m.closed.Load() || errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) {
				return
			}
			m.stats.family(socket.family).readErrors.Add(1)
			m.logger.Warn("SOCKETMUX_READ_ERROR", "family", socket.family.String(), "reason", err)
			if !m.waitReadRetry(backoff) {
				return
			}
			backoff = min(backoff*2, maximumReadErrorBackoff)
			continue
		}
		backoff = initialReadErrorBackoff
		familyStats := m.stats.family(socket.family)
		familyStats.receivedPackets.Add(1)
		familyStats.receivedBytes.Add(uint64(n))
		if flags&unix.MSG_TRUNC != 0 || n > m.config.MaxDatagramSize {
			m.stats.oversizedDrops.Add(1)
			continue
		}
		if !validSource(source, socket.family) {
			m.stats.classifierDrops.Add(1)
			continue
		}
		payload := append([]byte(nil), buffer[:n]...)
		datagram := Datagram{Payload: payload, Source: source, Family: socket.family}
		switch m.classifier.Classify(payload, source, socket.family) {
		case Control:
			select {
			case m.controlQueue <- datagram:
				m.stats.controlEnqueued.Add(1)
			default:
				if m.stats.controlOverflowDrops.Add(1) == 1 {
					m.logger.Warn("SOCKETMUX_QUEUE_DROP", "queue", "control", "family", socket.family.String())
				}
			}
		case Data:
			select {
			case m.dataQueue <- datagram:
				m.stats.dataEnqueued.Add(1)
			default:
				if m.stats.dataOverflowDrops.Add(1) == 1 {
					m.logger.Warn("SOCKETMUX_QUEUE_DROP", "queue", "data", "family", socket.family.String())
				}
			}
		case Drop:
			m.stats.classifierDrops.Add(1)
		default:
			m.stats.classifierDrops.Add(1)
		}
	}
}

func validSource(source netip.AddrPort, family AddressFamily) bool {
	if !source.IsValid() || source.Addr().IsUnspecified() || source.Addr().Is4In6() {
		return false
	}
	if family == IPv4 {
		return source.Addr().Is4()
	}
	return source.Addr().Is6()
}

func (m *Mux) waitReadRetry(delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-m.done:
		return false
	case <-timer.C:
		return true
	}
}

func (m *Mux) writeTo(ctx context.Context, payload []byte, destination netip.AddrPort) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if m.closed.Load() {
		return 0, ErrClosed
	}
	if len(payload) > m.config.MaxDatagramSize {
		return 0, fmt.Errorf("%w: %d > %d", ErrPayloadTooLarge, len(payload), m.config.MaxDatagramSize)
	}
	socket, err := m.socketForDestination(destination)
	if err != nil {
		return 0, err
	}
	if !m.beginWrite() {
		return 0, ErrClosed
	}
	defer m.finishWrite()
	socket.writeMu.Lock()
	defer socket.writeMu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if m.closed.Load() {
		return 0, ErrClosed
	}

	cancelFinished := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		_ = socket.conn.SetWriteDeadline(time.Now())
		close(cancelFinished)
	})
	deadline := time.Time{}
	if contextDeadline, ok := ctx.Deadline(); ok {
		deadline = contextDeadline
	}
	if err := socket.conn.SetWriteDeadline(deadline); err != nil {
		stopAndWait(stopCancel, cancelFinished)
		return 0, m.normalizeWriteError(ctx, err)
	}
	n, writeErr := socket.conn.WriteToUDPAddrPort(payload, destination)
	if writeErr == nil && n != len(payload) {
		writeErr = io.ErrShortWrite
	}
	stopAndWait(stopCancel, cancelFinished)
	resetErr := socket.conn.SetWriteDeadline(time.Time{})
	if writeErr != nil {
		m.stats.family(socket.family).writeErrors.Add(1)
		return n, m.normalizeWriteError(ctx, writeErr)
	}
	if resetErr != nil && !m.closed.Load() {
		m.stats.family(socket.family).writeErrors.Add(1)
		return n, fmt.Errorf("reset UDP write deadline: %w", resetErr)
	}
	familyStats := m.stats.family(socket.family)
	familyStats.transmittedPackets.Add(1)
	familyStats.transmittedBytes.Add(uint64(n))
	return n, nil
}

func (m *Mux) beginWrite() bool {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if m.closed.Load() {
		return false
	}
	m.activeWrites++
	return true
}

func (m *Mux) finishWrite() {
	m.stateMu.Lock()
	m.activeWrites--
	if m.activeWrites == 0 {
		m.writesDone.Broadcast()
	}
	m.stateMu.Unlock()
}

func stopAndWait(stop func() bool, finished <-chan struct{}) {
	if !stop() {
		<-finished
	}
}

func (m *Mux) normalizeWriteError(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if m.closed.Load() || errors.Is(err, net.ErrClosed) || errors.Is(err, os.ErrClosed) {
		return ErrClosed
	}
	return fmt.Errorf("write UDP datagram: %w", err)
}

func (m *Mux) socketForDestination(destination netip.AddrPort) (*physicalSocket, error) {
	if !destination.IsValid() || destination.Port() == 0 || destination.Addr().IsUnspecified() || destination.Addr().Is4In6() {
		return nil, ErrInvalidDestination
	}
	if destination.Addr().Is4() {
		if m.ipv4 == nil {
			return nil, fmt.Errorf("%w: IPv4", ErrFamilyUnavailable)
		}
		return m.ipv4, nil
	}
	if destination.Addr().Is6() {
		if m.ipv6 == nil {
			return nil, fmt.Errorf("%w: IPv6", ErrFamilyUnavailable)
		}
		return m.ipv6, nil
	}
	return nil, ErrInvalidDestination
}

// Control returns the isolated logical control endpoint.
func (m *Mux) Control() *Endpoint { return m.control }

// Data returns the isolated logical data endpoint.
func (m *Mux) Data() *Endpoint { return m.data }

// Port returns the common physical UDP port.
func (m *Mux) Port() uint16 { return m.port }

// Mark returns the last mark successfully applied to every mux socket.
func (m *Mux) Mark() uint32 {
	if m == nil {
		return 0
	}
	m.markMu.Lock()
	defer m.markMu.Unlock()
	return m.mark
}

// LocalAddr returns the physical local endpoint for a family without exposing its socket.
func (m *Mux) LocalAddr(family AddressFamily) (netip.AddrPort, bool) {
	switch family {
	case IPv4:
		if m.ipv4 != nil {
			return m.ipv4.local, true
		}
	case IPv6:
		if m.ipv6 != nil {
			return m.ipv6.local, true
		}
	}
	return netip.AddrPort{}, false
}

// Stats returns a non-blocking atomic statistics snapshot.
func (m *Mux) Stats() Stats { return m.stats.snapshot() }

// Close shuts down sockets, readers, and logical receive paths. It is idempotent.
func (m *Mux) Close() error {
	m.closeOnce.Do(func() {
		m.stateMu.Lock()
		m.closed.Store(true)
		m.stateMu.Unlock()
		close(m.done)
		var closeErrors []error
		if m.ipv4 != nil {
			if err := m.ipv4.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				closeErrors = append(closeErrors, fmt.Errorf("close IPv4 socket: %w", err))
			}
		}
		if m.ipv6 != nil {
			if err := m.ipv6.conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				closeErrors = append(closeErrors, fmt.Errorf("close IPv6 socket: %w", err))
			}
		}
		m.stateMu.Lock()
		for m.activeWrites != 0 {
			m.writesDone.Wait()
		}
		m.stateMu.Unlock()
		m.readers.Wait()
		close(m.controlQueue)
		close(m.dataQueue)
		m.closeErr = errors.Join(closeErrors...)
		m.logger.Info("SOCKETMUX_CLOSED", "port", m.port)
	})
	return m.closeErr
}
