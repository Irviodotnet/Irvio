// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type IncomingSignalHandler interface {
	HandleSignal(context.Context, peer.ID, []byte) ([]byte, error)
}

type RegistrationClientOptions struct {
	Host             host.Host
	Server           peer.ID
	SignedDescriptor []byte
	Config           Config
	Handler          IncomingSignalHandler
	Random           io.Reader
}

type RegistrationClient struct {
	host    host.Host
	server  peer.ID
	config  Config
	handler IncomingSignalHandler
	random  io.Reader
	stream  network.Stream
	id      RegistrationID

	context context.Context
	cancel  context.CancelFunc
	write   sync.Mutex
	once    sync.Once
	wg      sync.WaitGroup

	mu                sync.Mutex
	serverIncarnation uint64
	receipt           SignedReceipt
	heartbeatSequence uint64
	lastAck           time.Time
	update            *registrationUpdate
	closed            bool
	closeErr          error
}

type registrationUpdate struct {
	descriptorID [32]byte
	version      DescriptorVersion
	done         chan error
}

type RegistrationClientSnapshot struct {
	Server              peer.ID
	ServerIncarnation   uint64
	RegistrationID      RegistrationID
	HeartbeatSequence   uint64
	LastAcknowledgement time.Time
	ReceiptExpiresAt    time.Time
	ReceiptSequence     uint64
	Closed              bool
}

func OpenRegistration(ctx context.Context, options RegistrationClientOptions) (*RegistrationClient, error) {
	if ctx == nil || options.Host == nil || options.Server == "" || options.Server == options.Host.ID() ||
		len(options.SignedDescriptor) == 0 || len(options.SignedDescriptor) > MaxDescriptorSize || options.Handler == nil {
		return nil, fmt.Errorf("%w: registration client", ErrInvalid)
	}
	if err := options.Config.validate(); err != nil {
		return nil, err
	}
	random := options.Random
	if random == nil {
		random = rand.Reader
	}
	id, err := NewRegistrationID(random)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(options.Config.PendingTimeout)
	if caller, ok := ctx.Deadline(); ok && caller.Before(deadline) {
		deadline = caller
	}
	stream, err := options.Host.NewStream(ctx, options.Server, libprotocol.ID(ProtocolID))
	if err != nil {
		return nil, err
	}
	fail := func(cause error) (*RegistrationClient, error) {
		_ = stream.Reset()
		return nil, cause
	}
	if stream.Conn().RemotePeer() != options.Server {
		return fail(ErrWrongPeer)
	}
	_ = stream.SetDeadline(deadline)
	if err := WriteFrame(stream, &irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_Register{
		Register: &irviov1.RendezvousRegister{
			ProtocolVersion: ProtocolVersion, RegistrationId: append([]byte(nil), id[:]...),
			SignedAnnouncement: append([]byte(nil), options.SignedDescriptor...),
		},
	}}); err != nil {
		return fail(err)
	}
	frame, err := ReadFrame(stream)
	if err != nil {
		return fail(err)
	}
	if failure, ok := frame.Message.(*irviov1.RendezvousFrame_Error); ok {
		return fail(errorFromWire(failure.Error))
	}
	registered, ok := frame.Message.(*irviov1.RendezvousFrame_Registered)
	if !ok || registered.Registered.GetProtocolVersion() != ProtocolVersion ||
		!equalRegistrationID(registered.Registered.GetRegistrationId(), id) ||
		registered.Registered.GetServerIncarnation() == 0 {
		return fail(ErrInvalid)
	}
	receipt, err := VerifyReceipt(registered.Registered.GetSignedReceipt(), options.Server, options.Config.Now())
	if err != nil {
		return fail(err)
	}
	if receipt.Receipt.SubjectPeer != options.Host.ID() || receipt.Receipt.RegistrationID != id ||
		receipt.Receipt.ServerIncarnation != registered.Registered.ServerIncarnation {
		return fail(ErrWrongPeer)
	}
	runContext, cancel := context.WithCancel(context.Background())
	registration := &RegistrationClient{
		host: options.Host, server: options.Server, config: options.Config, handler: options.Handler,
		random: random, stream: stream, id: id, context: runContext, cancel: cancel,
		serverIncarnation: registered.Registered.ServerIncarnation, receipt: receipt,
	}
	_ = stream.SetDeadline(time.Time{})
	registration.wg.Add(2)
	go registration.readLoop()
	go registration.heartbeatLoop()
	return registration, nil
}

func (r *RegistrationClient) Update(ctx context.Context, signedDescriptor []byte) error {
	if len(signedDescriptor) == 0 || len(signedDescriptor) > MaxDescriptorSize {
		return ErrInvalid
	}
	if ctx == nil {
		return ErrInvalid
	}
	descriptor, err := VerifyDescriptor(signedDescriptor, r.host.ID(), r.config.Now())
	if err != nil {
		return err
	}
	update := &registrationUpdate{descriptorID: descriptor.ID, version: descriptor.Version, done: make(chan error, 1)}
	r.mu.Lock()
	if r.closed || r.update != nil {
		r.mu.Unlock()
		if r.closed {
			return ErrClosed
		}
		return ErrBusy
	}
	serverIncarnation := r.serverIncarnation
	r.update = update
	r.mu.Unlock()
	if err := r.writeFrame(&irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_Update{
		Update: &irviov1.RendezvousUpdate{
			ProtocolVersion: ProtocolVersion, ServerIncarnation: serverIncarnation,
			RegistrationId:     append([]byte(nil), r.id[:]...),
			SignedAnnouncement: append([]byte(nil), signedDescriptor...),
		},
	}}); err != nil {
		r.finishUpdate(update, err)
		return err
	}
	deadline := time.NewTimer(r.config.PendingTimeout)
	defer deadline.Stop()
	select {
	case err := <-update.done:
		return err
	case <-ctx.Done():
		r.finishUpdate(update, ctx.Err())
		return ctx.Err()
	case <-r.context.Done():
		r.mu.Lock()
		err := r.closeErr
		r.mu.Unlock()
		if err == nil {
			err = ErrClosed
		}
		return err
	case <-deadline.C:
		r.finishUpdate(update, context.DeadlineExceeded)
		return context.DeadlineExceeded
	}
}

func (r *RegistrationClient) Snapshot() RegistrationClientSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RegistrationClientSnapshot{
		Server: r.server, ServerIncarnation: r.serverIncarnation, RegistrationID: r.id,
		HeartbeatSequence: r.heartbeatSequence, LastAcknowledgement: r.lastAck,
		ReceiptExpiresAt: r.receipt.Receipt.ExpiresAt, ReceiptSequence: r.receipt.Receipt.Sequence, Closed: r.closed,
	}
}

// Done closes when the owning registration stream becomes unusable.
func (r *RegistrationClient) Done() <-chan struct{} { return r.context.Done() }

func (r *RegistrationClient) readLoop() {
	defer r.wg.Done()
	for {
		frame, err := ReadFrame(r.stream)
		if err != nil {
			r.setTerminal(err)
			return
		}
		switch value := frame.Message.(type) {
		case *irviov1.RendezvousFrame_HeartbeatAck:
			ack := value.HeartbeatAck
			r.mu.Lock()
			valid := ack.GetProtocolVersion() == ProtocolVersion &&
				ack.GetServerIncarnation() == r.serverIncarnation &&
				equalRegistrationID(ack.GetRegistrationId(), r.id) && ack.Sequence != 0 &&
				ack.Sequence <= r.heartbeatSequence
			currentReceipt := r.receipt
			r.mu.Unlock()
			if !valid {
				r.setTerminal(ErrInvalid)
				return
			}
			if len(ack.GetSignedReceipt()) != 0 {
				renewed, err := VerifyReceipt(ack.GetSignedReceipt(), r.server, r.config.Now())
				if err != nil || renewed.Receipt.SubjectPeer != r.host.ID() ||
					renewed.Receipt.ServerIncarnation != r.serverIncarnation || renewed.Receipt.RegistrationID != r.id ||
					renewed.Receipt.DescriptorID != currentReceipt.Receipt.DescriptorID ||
					renewed.Receipt.DescriptorVersion != currentReceipt.Receipt.DescriptorVersion ||
					renewed.Receipt.Sequence < currentReceipt.Receipt.Sequence ||
					renewed.Receipt.Sequence == currentReceipt.Receipt.Sequence &&
						!bytes.Equal(renewed.SignedEnvelope, currentReceipt.SignedEnvelope) {
					r.setTerminal(ErrInvalid)
					return
				}
				r.mu.Lock()
				if renewed.Receipt.Sequence >= r.receipt.Receipt.Sequence {
					r.receipt = renewed
				}
				r.mu.Unlock()
			}
			r.mu.Lock()
			r.lastAck = time.Unix(ack.AcceptedAtUnix, 0).UTC()
			r.mu.Unlock()
		case *irviov1.RendezvousFrame_Registered:
			registered := value.Registered
			receipt, err := VerifyReceipt(registered.GetSignedReceipt(), r.server, r.config.Now())
			if err != nil || registered.GetServerIncarnation() == 0 ||
				!equalRegistrationID(registered.GetRegistrationId(), r.id) ||
				receipt.Receipt.SubjectPeer != r.host.ID() ||
				receipt.Receipt.ServerIncarnation != registered.ServerIncarnation {
				r.setTerminal(ErrInvalid)
				return
			}
			r.mu.Lock()
			if registered.ServerIncarnation != r.serverIncarnation {
				r.mu.Unlock()
				r.setTerminal(ErrStale)
				return
			}
			update := r.update
			if update != nil && (receipt.Receipt.DescriptorID != update.descriptorID || receipt.Receipt.DescriptorVersion != update.version) {
				r.mu.Unlock()
				r.setTerminal(ErrConflict)
				return
			}
			r.receipt = receipt
			if update != nil {
				r.update = nil
				update.done <- nil
				close(update.done)
			}
			r.mu.Unlock()
		case *irviov1.RendezvousFrame_Signal:
			r.handleIncoming(value.Signal)
		case *irviov1.RendezvousFrame_Error:
			err := errorFromWire(value.Error)
			r.mu.Lock()
			update := r.update
			if update != nil {
				r.update = nil
				update.done <- err
				close(update.done)
			}
			r.mu.Unlock()
			if !errorsIsRecoverable(err) {
				r.setTerminal(err)
				return
			}
		default:
			r.setTerminal(ErrInvalid)
			return
		}
	}
}

func (r *RegistrationClient) handleIncoming(signal *irviov1.RendezvousSignal) {
	request, _, deadline, err := validateSignal(signal, r.config.Now())
	if err != nil || signal.Response {
		return
	}
	target, err := peer.IDFromBytes(signal.TargetPeerId)
	if err != nil || target != r.host.ID() {
		return
	}
	if caller, ok := r.context.Deadline(); ok && caller.Before(deadline) {
		deadline = caller
	}
	ctx, cancel := context.WithDeadline(r.context, deadline)
	signed, err := r.handler.HandleSignal(ctx, r.server, append([]byte(nil), signal.SignedMessage...))
	cancel()
	if err != nil || len(signed) == 0 || len(signed) > MaxSignalingBytes {
		return
	}
	response := &irviov1.RendezvousSignal{
		ProtocolVersion: ProtocolVersion, RequestHandle: append([]byte(nil), request[:]...),
		TargetPeerId: append([]byte(nil), signal.TargetPeerId...), OwnerRoute: signal.OwnerRoute,
		SignedMessage: signed, DeadlineUnixMilli: signal.DeadlineUnixMilli,
		Response: true, HopCount: signal.HopCount,
	}
	_ = r.writeFrame(&irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_Signal{Signal: response}})
}

func (r *RegistrationClient) heartbeatLoop() {
	defer r.wg.Done()
	timer := time.NewTimer(r.nextHeartbeat())
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			r.mu.Lock()
			if r.closed {
				r.mu.Unlock()
				return
			}
			r.heartbeatSequence++
			sequence := r.heartbeatSequence
			incarnation := r.serverIncarnation
			r.mu.Unlock()
			err := r.writeFrame(&irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_Heartbeat{
				Heartbeat: &irviov1.RendezvousHeartbeat{
					ProtocolVersion: ProtocolVersion, ServerIncarnation: incarnation,
					RegistrationId: append([]byte(nil), r.id[:]...), Sequence: sequence,
				},
			}})
			if err != nil {
				r.setTerminal(err)
				return
			}
			timer.Reset(r.nextHeartbeat())
		case <-r.context.Done():
			return
		}
	}
}

func (r *RegistrationClient) nextHeartbeat() time.Duration {
	base := r.config.HeartbeatInterval
	span := base / 5
	if span <= 0 {
		return base
	}
	value, err := rand.Int(r.random, big.NewInt(int64(2*span+1)))
	if err != nil {
		return base
	}
	return base - span + time.Duration(value.Int64())
}

func (r *RegistrationClient) writeFrame(frame *irviov1.RendezvousFrame) error {
	r.write.Lock()
	defer r.write.Unlock()
	return WriteFrame(r.stream, frame)
}

func (r *RegistrationClient) setTerminal(err error) {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		r.closeErr = err
	}
	if update := r.update; update != nil {
		r.update = nil
		update.done <- err
		close(update.done)
	}
	r.mu.Unlock()
	r.cancel()
	_ = r.stream.Reset()
}

func (r *RegistrationClient) finishUpdate(update *registrationUpdate, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.update != update {
		return
	}
	r.update = nil
	update.done <- err
	close(update.done)
}

func errorsIsRecoverable(err error) bool {
	return err == ErrStale || err == ErrRateLimited || err == ErrBusy
}

func (r *RegistrationClient) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.once.Do(func() {
		r.mu.Lock()
		r.closed = true
		incarnation := r.serverIncarnation
		if update := r.update; update != nil {
			r.update = nil
			update.done <- ErrClosed
			close(update.done)
		}
		r.mu.Unlock()
		_ = r.writeFrame(&irviov1.RendezvousFrame{Message: &irviov1.RendezvousFrame_Unregister{
			Unregister: &irviov1.RendezvousUnregister{
				ProtocolVersion: ProtocolVersion, ServerIncarnation: incarnation,
				RegistrationId:       append([]byte(nil), r.id[:]...),
				AcceptingNewSessions: false,
			},
		}})
		r.cancel()
		_ = r.stream.Close()
	})
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		_ = r.stream.Reset()
		<-done
		return ctx.Err()
	}
}
