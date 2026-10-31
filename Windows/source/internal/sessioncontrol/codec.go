// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package sessioncontrol

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"io"

	"google.golang.org/protobuf/proto"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

type RequestID [RequestIDSize]byte

func NewRequestID() (RequestID, error) {
	var id RequestID
	if _, err := io.ReadFull(rand.Reader, id[:]); err != nil {
		return id, err
	}
	if id == (RequestID{}) {
		id[len(id)-1] = 1
	}
	return id, nil
}

func writeFrame(writer io.Writer, frame *irviov1.SessionControlFrame) error {
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(frame)
	if err != nil || len(payload) == 0 || len(payload) > MaxFrameSize {
		return ErrInvalid
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

func readFrame(reader io.Reader) (*irviov1.SessionControlFrame, []byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > MaxFrameSize {
		return nil, nil, ErrInvalid
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, nil, err
	}
	frame := new(irviov1.SessionControlFrame)
	if err := (proto.UnmarshalOptions{RecursionLimit: 16}).Unmarshal(payload, frame); err != nil {
		return nil, nil, ErrInvalid
	}
	return frame, payload, nil
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) != 0 {
		written, err := writer.Write(payload)
		if written <= 0 || written > len(payload) {
			return io.ErrShortWrite
		}
		payload = payload[written:]
		if err != nil {
			return err
		}
	}
	return nil
}

func requestDigest(payload []byte) [sha256.Size]byte {
	return sha256.Sum256(payload)
}
