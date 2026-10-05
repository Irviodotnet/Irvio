// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package awg

import (
	"crypto/sha256"
	"encoding/binary"
)

const obfuscationProfileDomain = "hydra/awg-profile/v1\x00"

// ObfuscationProfileHash canonically binds wire-significant AWG parameters.
func ObfuscationProfileHash(config ObfuscationConfig) ([sha256.Size]byte, error) {
	if err := config.validate(); err != nil {
		return [sha256.Size]byte{}, err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte(obfuscationProfileDomain))
	var word [4]byte
	for _, value := range []uint32{
		config.JunkPacketCount, config.JunkMin, config.JunkMax,
		uint32(config.InitPadding), uint32(config.ResponsePadding), uint32(config.CookiePadding), uint32(config.TransportPadding),
		config.InitHeader, config.ResponseHeader, config.CookieHeader, config.TransportHeader,
	} {
		binary.BigEndian.PutUint32(word[:], value)
		_, _ = hash.Write(word[:])
	}
	for _, signature := range config.SignaturePackets {
		binary.BigEndian.PutUint32(word[:], uint32(len(signature)))
		_, _ = hash.Write(word[:])
		_, _ = hash.Write([]byte(signature))
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}
