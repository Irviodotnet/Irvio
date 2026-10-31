// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"google.golang.org/protobuf/proto"

	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

type byteReader struct{ io.Reader }

func (reader byteReader) Read(payload []byte) (int, error) {
	if len(payload) > 1 {
		payload = payload[:1]
	}
	return reader.Reader.Read(payload)
}

type byteWriter struct{ bytes.Buffer }

func (writer *byteWriter) Write(payload []byte) (int, error) {
	if len(payload) > 1 {
		payload = payload[:1]
	}
	return writer.Buffer.Write(payload)
}

func TestFrameRoundTripHandlesPartialIO(t *testing.T) {
	wire := new(byteWriter)
	want := &localv1.Envelope{
		ApiMajor: 1, ApiMinor: 7, RequestId: 91,
		Payload: &localv1.Envelope_Request{Request: &localv1.Request{Method: localv1.Method_METHOD_GET_STATUS}},
	}
	if err := WriteFrame(wire, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(byteReader{Reader: bytes.NewReader(wire.Bytes())})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got, want) {
		t.Fatalf("frame mismatch: got %v want %v", got, want)
	}
}

func TestReadFrameBoundsAndMalformedInput(t *testing.T) {
	frame := func(length uint32, payload []byte) []byte {
		result := make([]byte, 4, 4+len(payload))
		binary.BigEndian.PutUint32(result, length)
		return append(result, payload...)
	}
	tests := []struct {
		name string
		wire []byte
		want error
	}{
		{name: "zero", wire: frame(0, nil), want: ErrZeroLengthFrame},
		{name: "oversized", wire: frame(MaxFrameSize+1, nil), want: ErrFrameTooLarge},
		{name: "truncated header", wire: []byte{0, 0}, want: io.ErrUnexpectedEOF},
		{name: "closed mid frame", wire: frame(10, []byte{1}), want: io.ErrUnexpectedEOF},
		{name: "malformed protobuf", wire: frame(1, []byte{0xff}), want: ErrMalformedFrame},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ReadFrame(bytes.NewReader(test.wire))
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}

func TestReadFrameAcceptsUnknownProtobufFields(t *testing.T) {
	payload, err := proto.Marshal(&localv1.Envelope{
		ApiMajor: 1, RequestId: 3,
		Payload: &localv1.Envelope_Request{Request: &localv1.Request{Method: localv1.Method_METHOD_GET_STATUS}},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, 0x98, 0x06, 0x01) // Unknown field 99, varint 1.
	wire := make([]byte, 4, 4+len(payload))
	binary.BigEndian.PutUint32(wire, uint32(len(payload)))
	wire = append(wire, payload...)
	got, err := ReadFrame(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	if got.GetRequest().GetMethod() != localv1.Method_METHOD_GET_STATUS {
		t.Fatalf("known fields were not preserved: %v", got)
	}
}

func TestWriteFrameRejectsNilAndOversizedEnvelope(t *testing.T) {
	if err := WriteFrame(io.Discard, nil); !errors.Is(err, ErrMalformedFrame) {
		t.Fatalf("nil envelope: %v", err)
	}
	large := &localv1.Envelope{
		ApiMajor: 1,
		Payload:  &localv1.Envelope_Response{Response: &localv1.Response{UserMessage: string(make([]byte, MaxFrameSize))}},
	}
	if err := WriteFrame(io.Discard, large); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("oversized envelope: %v", err)
	}
}
