// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package protocol

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	libprotocol "github.com/libp2p/go-libp2p/core/protocol"
)

const (
	NodeProtocolID      libprotocol.ID = "/hydra/node/1.0.0"
	getAnnouncementByte                = byte(1)
	StreamTimeout                      = 10 * time.Second
)

// AnnouncementServer serves the current signed announcement over libp2p.
type AnnouncementServer struct {
	logger   *slog.Logger
	mu       sync.RWMutex
	envelope []byte
}

// RegisterAnnouncementServer installs the IRVIO node protocol handler.
func RegisterAnnouncementServer(h host.Host, logger *slog.Logger) *AnnouncementServer {
	server := &AnnouncementServer{logger: logger}
	h.SetStreamHandler(NodeProtocolID, server.handle)
	return server
}

// SetEnvelope atomically replaces the announcement served to peers.
func (s *AnnouncementServer) SetEnvelope(envelope []byte) error {
	if len(envelope) == 0 || len(envelope) > MaxSignedEnvelopeSize {
		return fmt.Errorf("signed announcement size %d is invalid", len(envelope))
	}
	s.mu.Lock()
	s.envelope = append(s.envelope[:0], envelope...)
	s.mu.Unlock()
	return nil
}

func (s *AnnouncementServer) handle(stream network.Stream) {
	remotePeer := stream.Conn().RemotePeer()
	if err := stream.SetDeadline(time.Now().Add(StreamTimeout)); err != nil {
		s.reject(stream, remotePeer, fmt.Errorf("set stream deadline: %w", err))
		return
	}
	request := []byte{0}
	if _, err := io.ReadFull(stream, request); err != nil {
		s.reject(stream, remotePeer, fmt.Errorf("read request: %w", err))
		return
	}
	if request[0] != getAnnouncementByte {
		s.reject(stream, remotePeer, fmt.Errorf("unknown request %d", request[0]))
		return
	}
	s.mu.RLock()
	envelope := append([]byte(nil), s.envelope...)
	s.mu.RUnlock()
	if len(envelope) == 0 {
		s.reject(stream, remotePeer, fmt.Errorf("announcement is unavailable"))
		return
	}
	length := binary.AppendUvarint(nil, uint64(len(envelope)))
	if _, err := stream.Write(length); err != nil {
		s.reject(stream, remotePeer, fmt.Errorf("write response length: %w", err))
		return
	}
	if _, err := stream.Write(envelope); err != nil {
		s.reject(stream, remotePeer, fmt.Errorf("write response: %w", err))
		return
	}
	if err := stream.Close(); err != nil {
		s.logger.Debug("NODE_ANNOUNCEMENT_STREAM_CLOSE", "remote_peer_id", remotePeer, "reason", err)
	}
}

func (s *AnnouncementServer) reject(stream network.Stream, remotePeer peer.ID, err error) {
	s.logger.Warn("NODE_ANNOUNCEMENT_REJECTED", "remote_peer_id", remotePeer, "reason", err)
	_ = stream.Reset()
}

// FetchSignedAnnouncement directly retrieves a peer's bounded signed envelope.
func FetchSignedAnnouncement(ctx context.Context, h host.Host, remotePeer peer.ID) ([]byte, error) {
	stream, err := h.NewStream(ctx, remotePeer, NodeProtocolID)
	if err != nil {
		return nil, fmt.Errorf("open %s stream: %w", NodeProtocolID, err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = stream.Reset()
		}
	}()
	stopCancel := context.AfterFunc(ctx, func() { _ = stream.Reset() })
	defer stopCancel()
	deadline := time.Now().Add(StreamTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := stream.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("set stream deadline: %w", err)
	}
	if _, err := stream.Write([]byte{getAnnouncementByte}); err != nil {
		return nil, fmt.Errorf("write GET_ANNOUNCEMENT: %w", err)
	}
	reader := bufio.NewReaderSize(stream, binary.MaxVarintLen64)
	length, err := binary.ReadUvarint(reader)
	if err != nil {
		return nil, fmt.Errorf("read response length: %w", err)
	}
	if length == 0 || length > MaxSignedEnvelopeSize {
		return nil, fmt.Errorf("response length %d is invalid", length)
	}
	envelope := make([]byte, int(length))
	if _, err := io.ReadFull(reader, envelope); err != nil {
		return nil, fmt.Errorf("read signed envelope: %w", err)
	}
	if err := stream.Close(); err != nil {
		return nil, fmt.Errorf("close stream: %w", err)
	}
	closed = true
	return envelope, nil
}
