// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"google.golang.org/protobuf/proto"

	localv1 "hydra.local/hydra/proto/hydra/local/v1"
)

const MaxFrameSize = 64 << 10

var (
	ErrZeroLengthFrame = errors.New("local API frame has zero length")
	ErrFrameTooLarge   = errors.New("local API frame exceeds maximum length")
	ErrMalformedFrame  = errors.New("local API frame contains malformed protobuf")
)

func ReadFrame(reader io.Reader) (*localv1.Envelope, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	return readFrameBody(reader, header)
}

func readFrameWithDeadline(conn net.Conn, idleTimeout, frameTimeout time.Duration) (*localv1.Envelope, error) {
	var header [4]byte
	if err := conn.SetReadDeadline(time.Now().Add(idleTimeout)); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(conn, header[:1]); err != nil {
		return nil, err
	}
	if err := conn.SetReadDeadline(time.Now().Add(frameTimeout)); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(conn, header[1:]); err != nil {
		return nil, err
	}
	return readFrameBody(conn, header)
}

func readFrameBody(reader io.Reader, header [4]byte) (*localv1.Envelope, error) {
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 {
		return nil, ErrZeroLengthFrame
	}
	if length > MaxFrameSize {
		return nil, fmt.Errorf("%w: %d", ErrFrameTooLarge, length)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	envelope := new(localv1.Envelope)
	if err := proto.Unmarshal(payload, envelope); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedFrame, err)
	}
	return envelope, nil
}

func WriteFrame(writer io.Writer, envelope *localv1.Envelope) error {
	if envelope == nil {
		return ErrMalformedFrame
	}
	payload, err := proto.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("marshal local API frame: %w", err)
	}
	if len(payload) == 0 {
		return ErrZeroLengthFrame
	}
	if len(payload) > MaxFrameSize {
		return fmt.Errorf("%w: %d", ErrFrameTooLarge, len(payload))
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeFull(writer, header[:]); err != nil {
		return err
	}
	return writeFull(writer, payload)
}

func writeFull(writer io.Writer, payload []byte) error {
	for len(payload) != 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(payload) {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}
