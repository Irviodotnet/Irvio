// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceadapter

import (
	"encoding/binary"
	"net/netip"
	"strings"
	"time"

	"github.com/pion/stun/v3"

	"github.com/Irviodotnet/Irvio/internal/socketmux"
)

const stunHeaderSize = 20

// Classifier claims only STUN packets associated with active ICE routing state.
type Classifier struct {
	registry *Registry
}

// NewClassifier creates a classifier backed by registry.
func NewClassifier(registry *Registry) *Classifier {
	return &Classifier{registry: registry}
}

// Classify implements socketmux.Classifier without retaining payload.
func (c *Classifier) Classify(payload []byte, source netip.AddrPort, family socketmux.AddressFamily) socketmux.PacketClass {
	message, status := decodeSTUN(payload)
	if status == decodeNotSTUN {
		return socketmux.Data
	}
	if status == decodeMalformed {
		if c != nil && c.registry != nil {
			c.registry.stats.malformedSTUNLikePackets.Add(1)
		}
		return socketmux.Data
	}
	if c == nil || c.registry == nil || message.Type.Method != stun.MethodBinding {
		return socketmux.Data
	}
	switch message.Type.Class {
	case stun.ClassRequest:
		local, _, ok := usernameParts(message)
		if !ok || !c.registry.hasUfrag(local, family) {
			c.registry.stats.unknownUfragPackets.Add(1)
			return socketmux.Data
		}
		return socketmux.Control
	case stun.ClassSuccessResponse, stun.ClassErrorResponse:
		switch c.registry.classifyResponse(transactionID(message.TransactionID), source, family, time.Now()) {
		case responseActive:
			return socketmux.Control
		case responseRetired:
			c.registry.stats.retiredResponsesDropped.Add(1)
			return socketmux.Drop
		case responseWrongSource:
			c.registry.stats.wrongSourceResponses.Add(1)
		case responseWrongFamily:
			c.registry.stats.wrongFamilyResponses.Add(1)
		default:
			c.registry.stats.unknownTransactions.Add(1)
		}
	}
	return socketmux.Data
}

type decodeStatus uint8

const (
	decodeNotSTUN decodeStatus = iota
	decodeMalformed
	decodeValid
)

func decodeSTUN(payload []byte) (*stun.Message, decodeStatus) {
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
	return message, decodeValid
}

func usernameParts(message *stun.Message) (local, remote string, ok bool) {
	var raw []byte
	count := 0
	for _, attribute := range message.Attributes {
		if attribute.Type == stun.AttrUsername {
			count++
			raw = attribute.Value
		}
	}
	if count != 1 || len(raw) == 0 || len(raw) > 2*maxUfragLength+1 {
		return "", "", false
	}
	username := string(raw)
	if strings.Count(username, ":") != 1 {
		return "", "", false
	}
	local, remote, _ = strings.Cut(username, ":")
	return local, remote, validUfrag(local) && validUfrag(remote)
}

func validUfrag(ufrag string) bool {
	if len(ufrag) < minUfragLength || len(ufrag) > maxUfragLength {
		return false
	}
	for index := 0; index < len(ufrag); index++ {
		character := ufrag[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '+' || character == '/' {
			continue
		}
		return false
	}
	return true
}
