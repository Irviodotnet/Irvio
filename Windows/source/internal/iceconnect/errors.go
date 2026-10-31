// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package iceconnect

import "errors"

var (
	// ErrInvalidBinding indicates inconsistent M5b session identity or roles.
	ErrInvalidBinding = errors.New("invalid ICE session binding")
	// ErrInvalidState indicates an operation is not valid in the current one-shot state.
	ErrInvalidState = errors.New("invalid ICE connectivity state")
	// ErrAlreadyStarted indicates connectivity has already started or terminated.
	ErrAlreadyStarted = errors.New("ICE connectivity already started")
	// ErrRemoteNotLoaded indicates Connect was called before candidate loading completed.
	ErrRemoteNotLoaded = errors.New("remote ICE candidates are not loaded")
	// ErrCandidateLoad indicates remote candidate loading or verification failed.
	ErrCandidateLoad = errors.New("load remote ICE candidates")
	// ErrNoUsableFamily indicates local and remote candidates share no UDP family.
	ErrNoUsableFamily = errors.New("no common ICE UDP address family")
	// ErrConnectivityFailed indicates Pion did not establish a valid selected pair.
	ErrConnectivityFailed = errors.New("ICE connectivity failed")
	// ErrSelectedPair indicates Pion selected a pair outside the immutable descriptions.
	ErrSelectedPair = errors.New("invalid selected ICE candidate pair")
	// ErrClosed indicates the one-shot connectivity lifecycle is closed.
	ErrClosed = errors.New("ICE connectivity attempt is closed")
)
