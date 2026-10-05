// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import (
	"strings"
	"time"

	"github.com/Irviodotnet/Irvio/internal/runtime"
	"github.com/Irviodotnet/Irvio/internal/underlay"
	localv1 "github.com/Irviodotnet/Irvio/proto/irvio/local/v1"
)

func MapProductStatus(status runtime.Status) *localv1.Status {
	return mapProductStatus(status, time.Time{})
}

func mapProductStatus(status runtime.Status, connectedSince time.Time) *localv1.Status {
	product := &localv1.Status{
		State:               mapConnectionState(status),
		CountryCode:         strings.ToUpper(string(status.Country)),
		ContributionEnabled: status.ExitContributing,
	}
	if product.State == localv1.ConnectionState_CONNECTION_STATE_CONNECTED ||
		product.State == localv1.ConnectionState_CONNECTION_STATE_RECOVERING {
		product.Connection = &localv1.ConnectionInfo{ExitCountryCode: product.CountryCode}
		if !connectedSince.IsZero() {
			product.Connection.ConnectedSinceUnixMs = uint64(connectedSince.UTC().UnixMilli())
		}
	}
	if product.State == localv1.ConnectionState_CONNECTION_STATE_ERROR {
		product.Error = &localv1.UserVisibleError{
			Code:        localv1.ResultCode_RESULT_CODE_INTERNAL,
			UserMessage: "Irvio stopped because an internal operation failed",
		}
	}
	return product
}

func mapConnectionState(status runtime.Status) localv1.ConnectionState {
	switch status.State {
	case runtime.StateStopped, runtime.StateIdle, runtime.StateClosed:
		if status.State == runtime.StateClosed && status.LastReason == runtime.ReasonChildFailure {
			return localv1.ConnectionState_CONNECTION_STATE_ERROR
		}
		return localv1.ConnectionState_CONNECTION_STATE_DISCONNECTED
	case runtime.StateStarting, runtime.StateProtecting, runtime.StateDiscovering, runtime.StateConnecting:
		return localv1.ConnectionState_CONNECTION_STATE_CONNECTING
	case runtime.StateConnected, runtime.StateLeaseRenewal:
		return localv1.ConnectionState_CONNECTION_STATE_CONNECTED
	case runtime.StateSamePeerRecovery, runtime.StateNetworkMigration, runtime.StateExitReselection:
		return localv1.ConnectionState_CONNECTION_STATE_RECOVERING
	case runtime.StateNoExit:
		if status.LastReason == runtime.ReasonUnderlayChanged && !status.Underlay.AnyReachable() ||
			status.Underlay.IPv4.State == underlay.Unreachable && status.Underlay.IPv6.State == underlay.Unreachable {
			return localv1.ConnectionState_CONNECTION_STATE_NO_NETWORK
		}
		return localv1.ConnectionState_CONNECTION_STATE_NO_EXIT
	case runtime.StateDisconnecting, runtime.StateStopping:
		return localv1.ConnectionState_CONNECTION_STATE_DISCONNECTING
	case runtime.StateError:
		return localv1.ConnectionState_CONNECTION_STATE_ERROR
	default:
		return localv1.ConnectionState_CONNECTION_STATE_ERROR
	}
}
