// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"encoding/binary"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func WriteFrame(writer io.Writer, frame *irviov1.RendezvousFrame) error {
	if writer == nil || frame == nil || frame.Message == nil {
		return ErrInvalid
	}
	return writeProtoFrame(writer, frame)
}

func writeAvailableCountriesFrame(writer io.Writer, frame *irviov1.AvailableCountriesFrame) error {
	if writer == nil || frame == nil || frame.Message == nil {
		return ErrInvalid
	}
	return writeProtoFrame(writer, frame)
}

func writeProtoFrame(writer io.Writer, frame proto.Message) error {
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(frame)
	if err != nil {
		return fmt.Errorf("marshal Rendezvous frame: %w", err)
	}
	if len(payload) == 0 || len(payload) > MaxFrameSize {
		return fmt.Errorf("%w: frame size %d", ErrInvalid, len(payload))
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	if err := writeAll(writer, header[:]); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

func ReadFrame(reader io.Reader) (*irviov1.RendezvousFrame, error) {
	if reader == nil {
		return nil, ErrInvalid
	}
	frame := new(irviov1.RendezvousFrame)
	if err := readProtoFrame(reader, frame); err != nil {
		return nil, err
	}
	if frame.Message == nil {
		return nil, fmt.Errorf("%w: empty frame", ErrInvalid)
	}
	return frame, nil
}

func readAvailableCountriesFrame(reader io.Reader) (*irviov1.AvailableCountriesFrame, error) {
	if reader == nil {
		return nil, ErrInvalid
	}
	frame := new(irviov1.AvailableCountriesFrame)
	if err := readProtoFrame(reader, frame); err != nil {
		return nil, err
	}
	if frame.Message == nil {
		return nil, fmt.Errorf("%w: empty frame", ErrInvalid)
	}
	return frame, nil
}

func readProtoFrame(reader io.Reader, frame proto.Message) error {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > MaxFrameSize {
		return fmt.Errorf("%w: frame size %d", ErrInvalid, length)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return err
	}
	if err := (proto.UnmarshalOptions{RecursionLimit: 24, DiscardUnknown: false}).Unmarshal(payload, frame); err != nil {
		return fmt.Errorf("%w: frame protobuf", ErrInvalid)
	}
	return nil
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
