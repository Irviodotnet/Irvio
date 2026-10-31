// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package stunprobe

import (
	"errors"
	"fmt"

	"github.com/pion/stun/v3"
)

const maximumErrorReasonLength = 256

var (
	// ErrClosed indicates that the STUN client has stopped.
	ErrClosed = errors.New("STUN probe client is closed")
	// ErrTransportClosed indicates that IrvioSocketMux stopped first.
	ErrTransportClosed = errors.New("STUN probe transport is closed")
	// ErrInFlightLimit indicates that the active transaction bound was reached.
	ErrInFlightLimit = errors.New("STUN in-flight transaction limit reached")
	// ErrDuplicateTransaction indicates an active or quarantined transaction ID collision.
	ErrDuplicateTransaction = errors.New("duplicate STUN transaction ID")
	// ErrTimeout indicates that all configured request attempts expired.
	ErrTimeout = errors.New("STUN Binding probe timed out")
	// ErrInvalidServer indicates an unusable or ambiguous server endpoint.
	ErrInvalidServer = errors.New("invalid STUN server endpoint")
	// ErrInvalidResponse indicates a matching response with invalid Binding semantics.
	ErrInvalidResponse = errors.New("invalid STUN Binding response")
	// ErrInvalidSourcePolicy indicates an invalid or unbounded response-source policy.
	ErrInvalidSourcePolicy = errors.New("invalid STUN response-source policy")
)

// BindingError reports an ERROR-CODE returned by a STUN Binding server.
type BindingError struct {
	Code   stun.ErrorCode
	Reason string
}

func (e *BindingError) Error() string {
	return fmt.Sprintf("STUN Binding error %d: %s", e.Code, e.Reason)
}
