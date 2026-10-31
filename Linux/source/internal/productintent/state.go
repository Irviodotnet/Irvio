// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

//go:build linux || windows

package productintent

import (
	"errors"

	"github.com/libp2p/go-libp2p/core/peer"
)

const maxStateBytes = 512

type State struct {
	Version         int    `json:"version"`
	ConnectIntent   bool   `json:"connect_intent"`
	Country         string `json:"country,omitempty"`
	TeardownPending bool   `json:"teardown_pending,omitempty"`
	PrivateTarget   string `json:"private_target,omitempty"`
	PrivateHosting  bool   `json:"private_hosting,omitempty"`
}

func (state State) valid() bool {
	if state.Version != 1 && state.Version != 2 {
		return false
	}
	if state.Version == 1 && (state.PrivateTarget != "" || state.PrivateHosting) {
		return false
	}
	if state.TeardownPending {
		return !state.ConnectIntent && state.Country == "" && state.PrivateTarget == "" && !state.PrivateHosting
	}
	if state.PrivateTarget != "" || state.PrivateHosting {
		if state.Country != "" || state.ConnectIntent != (state.PrivateTarget != "") {
			return false
		}
		if state.PrivateTarget != "" {
			id, err := peer.Decode(state.PrivateTarget)
			if err != nil || id.String() != state.PrivateTarget {
				return false
			}
		}
		return true
	}
	return state.ConnectIntent && validCountry(state.Country)
}

// WantPrivate persists only identity and intent; authorization stays in the trust store.
func (s Store) WantPrivate(target peer.ID, hosting bool) error {
	state := State{Version: 2, ConnectIntent: target != "", PrivateHosting: hosting}
	if target != "" {
		if _, err := peer.IDFromBytes([]byte(target)); err != nil {
			return errors.New("invalid IRVIO private target")
		}
		state.PrivateTarget = target.String()
	}
	if !state.valid() {
		return errors.New("invalid IRVIO private intent")
	}
	return s.write(state)
}

func (s Store) Want(country string) error {
	if !validCountry(country) {
		return errors.New("invalid IRVIO country intent")
	}
	return s.write(State{Version: 1, ConnectIntent: true, Country: country})
}

func (s Store) BeginTeardown() error {
	return s.write(State{Version: 1, TeardownPending: true})
}

func validCountry(country string) bool {
	if len(country) != 2 {
		return false
	}
	for _, value := range country {
		if value < 'A' || value > 'Z' {
			return false
		}
	}
	return true
}
