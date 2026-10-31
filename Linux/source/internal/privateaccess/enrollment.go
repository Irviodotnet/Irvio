// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package privateaccess

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"time"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	libtls "github.com/libp2p/go-libp2p/p2p/security/tls"
	"google.golang.org/protobuf/proto"
)

const enrollmentTransportID = "/irvio/private-enrollment/1.0.0"
const enrollmentFrameLimit = 4096

type EnrollmentServer struct {
	store     *Store
	transport *libtls.Transport
	slots     chan struct{}
}

func NewEnrollmentServer(store *Store, key crypto.PrivKey) (*EnrollmentServer, error) {
	if store == nil || key == nil || key.Type() != crypto.Ed25519 {
		return nil, ErrInvalid
	}
	state, err := store.Snapshot()
	if err != nil {
		return nil, err
	}
	transport, err := libtls.New(enrollmentTransportID, key, nil)
	if err != nil {
		return nil, ErrInvalid
	}
	host, err := peer.IDFromPrivateKey(key)
	if err != nil || host.String() != state.HostPeerID {
		return nil, ErrDenied
	}
	return &EnrollmentServer{store: store, transport: transport, slots: make(chan struct{}, 8)}, nil
}

// Serve authenticates the end-client; only explicitly preauthorized invitations grant access.
func (s *EnrollmentServer) Serve(ctx context.Context, conn net.Conn) error {
	if ctx == nil || conn == nil {
		return ErrInvalid
	}
	defer conn.Close()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return ErrCapacity
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	secure, err := s.transport.SecureInbound(ctx, conn, "")
	if err != nil {
		return ErrDenied
	}
	defer secure.Close()
	request := new(irviov1.PrivateEnrollmentRequest)
	if err := readEnrollment(secure, request); err != nil {
		return err
	}
	if request.ProtocolVersion != 1 || len(request.InvitationId) != 16 || (!request.StatusOnly && len(request.EnrollmentSecret) != 32) || (request.StatusOnly && len(request.EnrollmentSecret) != 0) {
		return ErrInvalid
	}
	id := hex.EncodeToString(request.InvitationId)
	client := secure.RemotePeer()
	status := irviov1.PrivateEnrollmentResponse_DENIED
	if !request.StatusOnly {
		if err := s.store.Reserve(id, hex.EncodeToString(request.EnrollmentSecret), client, time.Now()); err == nil {
			status = irviov1.PrivateEnrollmentResponse_PENDING
		}
	}
	if request.StatusOnly || status == irviov1.PrivateEnrollmentResponse_PENDING {
		state, err := s.store.Snapshot()
		if err != nil {
			return err
		}
		for _, invite := range state.Invitations {
			if invite.ID != id || invite.Client != client.String() {
				continue
			}
			if invite.State == "pending" && time.Now().Unix() >= invite.CreatedAt && time.Now().Unix() < invite.ExpiresAt {
				status = irviov1.PrivateEnrollmentResponse_PENDING
			}
			if invite.State == "consumed" && state.Authorized(client) {
				status = irviov1.PrivateEnrollmentResponse_APPROVED
			}
			break
		}
	}
	return writeEnrollment(secure, &irviov1.PrivateEnrollmentResponse{ProtocolVersion: 1, Status: status})
}

// Enroll verifies the host's identity before revealing the invitation secret inside TLS.
func Enroll(ctx context.Context, conn net.Conn, key crypto.PrivKey, token InvitationToken, statusOnly bool) (irviov1.PrivateEnrollmentResponse_Status, error) {
	denied := irviov1.PrivateEnrollmentResponse_DENIED
	if ctx == nil || conn == nil || key == nil || key.Type() != crypto.Ed25519 {
		return denied, ErrInvalid
	}
	defer conn.Close()
	if err := validateInvitationToken(token, time.Now()); err != nil && !(statusOnly && errors.Is(err, ErrExpired)) {
		return denied, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return denied, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	transport, err := libtls.New(enrollmentTransportID, key, nil)
	if err != nil {
		return denied, ErrInvalid
	}
	secure, err := transport.SecureOutbound(ctx, conn, token.Host)
	if err != nil {
		return denied, ErrDenied
	}
	defer secure.Close()
	id, _ := hex.DecodeString(token.Invitation.ID)
	request := &irviov1.PrivateEnrollmentRequest{ProtocolVersion: 1, InvitationId: id, StatusOnly: statusOnly}
	if !statusOnly {
		request.EnrollmentSecret, _ = hex.DecodeString(token.Invitation.Secret)
	}
	if err := writeEnrollment(secure, request); err != nil {
		return denied, err
	}
	response := new(irviov1.PrivateEnrollmentResponse)
	if err := readEnrollment(secure, response); err != nil {
		return denied, err
	}
	if response.ProtocolVersion != 1 || response.Status < denied || response.Status > irviov1.PrivateEnrollmentResponse_APPROVED {
		return denied, ErrInvalid
	}
	return response.Status, nil
}

func writeEnrollment(w io.Writer, message proto.Message) error {
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil || len(data) == 0 || len(data) > enrollmentFrameLimit {
		return ErrInvalid
	}
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame, uint32(len(data)))
	copy(frame[4:], data)
	n, err := w.Write(frame)
	if err == nil && n != len(frame) {
		return io.ErrShortWrite
	}
	return err
}

func readEnrollment(r io.Reader, message proto.Message) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > enrollmentFrameLimit {
		return ErrInvalid
	}
	data := make([]byte, int(size))
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	if err := (proto.UnmarshalOptions{RecursionLimit: 4}).Unmarshal(data, message); err != nil {
		return ErrInvalid
	}
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil || len(message.ProtoReflect().GetUnknown()) != 0 || !bytes.Equal(data, canonical) {
		return ErrInvalid
	}
	return nil
}
