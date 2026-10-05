// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package exitselector

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// Random provides unbiased sampling and jitter; shared implementations must be concurrency-safe.
type Random interface {
	Index(limit int) (int, error)
}

// JitteredRefresh chooses one delay from the inclusive configured interval.
func JitteredRefresh(minimum, maximum time.Duration, random Random) (time.Duration, error) {
	if minimum <= 0 || maximum < minimum || random == nil {
		return 0, ErrInvalidConfig
	}
	span := maximum - minimum
	if span == 0 {
		return minimum, nil
	}
	steps := int(span / time.Millisecond)
	index, err := random.Index(steps + 1)
	if err != nil {
		return 0, err
	}
	return minimum + time.Duration(index)*time.Millisecond, nil
}

type cryptoRandom struct{ reader io.Reader }

func NewCryptoRandom() Random { return &cryptoRandom{reader: rand.Reader} }

func (r *cryptoRandom) Index(limit int) (int, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("random limit must be positive")
	}
	bound := uint64(limit)
	threshold := -bound % bound
	for {
		var raw [8]byte
		if _, err := io.ReadFull(r.reader, raw[:]); err != nil {
			return 0, err
		}
		value := binary.LittleEndian.Uint64(raw[:])
		if value >= threshold {
			return int(value % bound), nil
		}
	}
}
