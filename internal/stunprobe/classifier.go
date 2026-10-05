// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunprobe

import (
	"encoding/binary"
	"net/netip"
	"time"

	"github.com/pion/stun/v3"

	"hydra.local/hydra/internal/socketmux"
)

const stunHeaderSize = 20

// Classifier routes only valid responses for registered transactions to Control.
type Classifier struct {
	registry *Registry
}

// NewClassifier creates a transaction-aware socket mux classifier.
func NewClassifier(registry *Registry) *Classifier {
	return &Classifier{registry: registry}
}

// Classify implements socketmux.Classifier without retaining payload.
func (c *Classifier) Classify(payload []byte, source netip.AddrPort, family socketmux.AddressFamily) socketmux.PacketClass {
	message, status := decodeResponse(payload)
	if status == decodeNotSTUN {
		return socketmux.Data
	}
	if status == decodeMalformed {
		if c != nil && c.registry != nil {
			c.registry.stats.malformedSTUNLike.Add(1)
		}
		return socketmux.Data
	}
	if status == decodeOtherSTUN || c == nil || c.registry == nil {
		return socketmux.Data
	}
	return c.registry.classify(transactionID(message.TransactionID), source, family, message.Type.Class, time.Now())
}

type decodeStatus uint8

const (
	decodeNotSTUN decodeStatus = iota
	decodeMalformed
	decodeOtherSTUN
	decodeBindingResponse
)

func decodeResponse(payload []byte) (*stun.Message, decodeStatus) {
	if !stun.IsMessage(payload) {
		return nil, decodeNotSTUN
	}
	if len(payload) < stunHeaderSize {
		return nil, decodeMalformed
	}
	declared := int(binary.BigEndian.Uint16(payload[2:4]))
	if declared%4 != 0 || stunHeaderSize+declared != len(payload) {
		return nil, decodeMalformed
	}
	message := stun.NewWithOptions(stun.WithStrict(true))
	if err := message.UnmarshalBinary(payload); err != nil {
		return nil, decodeMalformed
	}
	if binary.BigEndian.Uint16(payload[:2]) != message.Type.Value() {
		return nil, decodeMalformed
	}
	if message.Type.Method != stun.MethodBinding || (message.Type.Class != stun.ClassSuccessResponse && message.Type.Class != stun.ClassErrorResponse) {
		return message, decodeOtherSTUN
	}
	return message, decodeBindingResponse
}
