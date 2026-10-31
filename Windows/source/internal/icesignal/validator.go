// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package icesignal

import (
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Irviodotnet/Irvio/internal/icesession"
	irviov1 "github.com/Irviodotnet/Irvio/proto/irvio/v1"
)

func validateOfferWireVersion(message *irviov1.IceOffer) error {
	if message == nil || message.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("%w: unsupported protocol version", ErrInvalidMessage)
	}
	return nil
}

func validateAnswerWireVersion(message *irviov1.IceAnswer) error {
	if message == nil || message.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("%w: unsupported protocol version", ErrInvalidMessage)
	}
	return nil
}

func validateOffer(offer Offer, authenticatedRemote, local peer.ID, now time.Time, config Config) error {
	if offer.Initiator == "" || offer.Responder == "" || offer.Initiator == offer.Responder {
		return fmt.Errorf("%w: invalid peer pair", ErrWrongPeer)
	}
	if offer.Initiator != authenticatedRemote || offer.Responder != local {
		return fmt.Errorf("%w: authenticated offer identity mismatch", ErrWrongPeer)
	}
	if offer.Purpose != icesession.PurposeExit || offer.Role != icesession.RoleControlling {
		return fmt.Errorf("%w: invalid offer purpose or role", ErrInvalidMessage)
	}
	if err := validateTimes(offer.CreatedAt, offer.ExpiresAt, now, config); err != nil {
		return err
	}
	_, err := icesession.ValidateDescription(offer.Description, config.Validation)
	return err
}

// ValidateOffer applies existing checks using the signed M12 end-peer identity.
func ValidateOffer(offer Offer, authenticatedRemote, local peer.ID, now time.Time, config Config) error {
	return validateOffer(offer, authenticatedRemote, local, now, config)
}

func validateAnswer(answer Answer, offer Offer, authenticatedRemote, local peer.ID, now time.Time, config Config) error {
	if answer.SessionID != offer.SessionID || answer.Initiator != offer.Initiator ||
		answer.Responder != offer.Responder || answer.Purpose != offer.Purpose {
		return fmt.Errorf("%w: answer changed session binding", ErrInvalidMessage)
	}
	if answer.Initiator != local || answer.Responder != authenticatedRemote || answer.Initiator == answer.Responder {
		return fmt.Errorf("%w: authenticated answer identity mismatch", ErrWrongPeer)
	}
	if answer.Role != icesession.RoleControlled {
		return fmt.Errorf("%w: invalid answer role", ErrInvalidMessage)
	}
	if err := validateTimes(answer.CreatedAt, answer.ExpiresAt, now, config); err != nil {
		return err
	}
	_, err := icesession.ValidateDescription(answer.Description, config.Validation)
	return err
}

// ValidateAnswer applies existing checks using the signed M12 end-peer identity.
func ValidateAnswer(answer Answer, offer Offer, authenticatedRemote, local peer.ID, now time.Time, config Config) error {
	return validateAnswer(answer, offer, authenticatedRemote, local, now, config)
}

func validateTimes(created, expires, now time.Time, config Config) error {
	if created.Unix() <= 0 || expires.Unix() <= 0 || !expires.After(created) {
		return fmt.Errorf("%w: invalid description timestamps", ErrInvalidMessage)
	}
	if expires.Sub(created) > config.DescriptionTTL || created.After(now.Add(config.ClockSkew)) {
		return fmt.Errorf("%w: description lifetime is invalid", ErrInvalidMessage)
	}
	if !expires.After(now) {
		return fmt.Errorf("%w: description expired", ErrInvalidMessage)
	}
	return nil
}

func errorCodeToProto(code ErrorCode) irviov1.IceSignalErrorCode {
	switch code {
	case ErrorInvalidRequest:
		return irviov1.IceSignalErrorCode_ICE_SIGNAL_ERROR_CODE_INVALID_REQUEST
	case ErrorUnsupportedVersion:
		return irviov1.IceSignalErrorCode_ICE_SIGNAL_ERROR_CODE_UNSUPPORTED_VERSION
	case ErrorWrongPeer:
		return irviov1.IceSignalErrorCode_ICE_SIGNAL_ERROR_CODE_WRONG_PEER
	case ErrorUnauthorized:
		return irviov1.IceSignalErrorCode_ICE_SIGNAL_ERROR_CODE_UNAUTHORIZED
	case ErrorBusy:
		return irviov1.IceSignalErrorCode_ICE_SIGNAL_ERROR_CODE_BUSY
	case ErrorSessionConflict:
		return irviov1.IceSignalErrorCode_ICE_SIGNAL_ERROR_CODE_SESSION_CONFLICT
	default:
		return irviov1.IceSignalErrorCode_ICE_SIGNAL_ERROR_CODE_INTERNAL_ERROR
	}
}

func errorCodeFromProto(code irviov1.IceSignalErrorCode) ErrorCode {
	switch code {
	case irviov1.IceSignalErrorCode_ICE_SIGNAL_ERROR_CODE_INVALID_REQUEST:
		return ErrorInvalidRequest
	case irviov1.IceSignalErrorCode_ICE_SIGNAL_ERROR_CODE_UNSUPPORTED_VERSION:
		return ErrorUnsupportedVersion
	case irviov1.IceSignalErrorCode_ICE_SIGNAL_ERROR_CODE_WRONG_PEER:
		return ErrorWrongPeer
	case irviov1.IceSignalErrorCode_ICE_SIGNAL_ERROR_CODE_UNAUTHORIZED:
		return ErrorUnauthorized
	case irviov1.IceSignalErrorCode_ICE_SIGNAL_ERROR_CODE_BUSY:
		return ErrorBusy
	case irviov1.IceSignalErrorCode_ICE_SIGNAL_ERROR_CODE_SESSION_CONFLICT:
		return ErrorSessionConflict
	default:
		return ErrorInternal
	}
}
