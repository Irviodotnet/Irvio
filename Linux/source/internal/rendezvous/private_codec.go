// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"io"

	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func readPrivateRequest(reader io.Reader) (*irviov1.PrivateRendezvousRequest, error) {
	request := new(irviov1.PrivateRendezvousRequest)
	if err := readProtoFrameLimit(reader, request, PrivateMaxFrameSize); err != nil {
		return nil, err
	}
	if request.ProtocolVersion != ProtocolVersion || request.Message == nil || len(request.ProtoReflect().GetUnknown()) != 0 {
		return nil, ErrInvalid
	}
	return request, nil
}

func writePrivateRequest(writer io.Writer, request *irviov1.PrivateRendezvousRequest) error {
	if request == nil || request.ProtocolVersion != ProtocolVersion || request.Message == nil {
		return ErrInvalid
	}
	return writeProtoFrameLimit(writer, request, PrivateMaxFrameSize)
}

func readRegistrationFrame(reader io.Reader, private bool) (*irviov1.RendezvousFrame, error) {
	if !private {
		return ReadFrame(reader)
	}
	frame := new(irviov1.RendezvousFrame)
	if err := readProtoFrameLimit(reader, frame, PrivateMaxFrameSize); err != nil {
		return nil, err
	}
	if frame.Message == nil {
		return nil, ErrInvalid
	}
	return frame, nil
}

func frameLimit(private bool) int {
	if private {
		return PrivateMaxFrameSize
	}
	return MaxFrameSize
}

func privateLocator(raw []byte) ([32]byte, error) {
	var locator [32]byte
	if len(raw) != len(locator) {
		return locator, ErrInvalid
	}
	copy(locator[:], raw)
	if locator == ([32]byte{}) {
		return locator, ErrInvalid
	}
	return locator, nil
}
