// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package rendezvous

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/Irviodotnet/Irvio/internal/privateaccess"
)

// PrivateEnrollmentDialer discovers current owners and never routes to unverified introduction hints.
func (c *Client) PrivateEnrollmentDialer(finder PrivateOwnerFinder) (privateaccess.EnrollmentDialer, error) {
	if c == nil || finder == nil {
		return nil, ErrInvalid
	}
	return func(ctx context.Context, token privateaccess.InvitationToken) (net.Conn, error) {
		lookup, err := NewPrivateLookup(finder, c, token.Host, token.Locator)
		if err != nil {
			return nil, err
		}
		result, err := lookup.Lookup(ctx)
		if err != nil {
			return nil, err
		}
		var failures []error
		for _, route := range result.Candidate.Routes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			conn, err := c.openPrivateEnrollment(ctx, token.Host, token.Locator, route, 3*time.Second)
			if err == nil {
				return conn, nil
			}
			failures = append(failures, err)
		}
		return nil, errors.Join(append([]error{ErrRouteFailed}, failures...)...)
	}, nil
}
