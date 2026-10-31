// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package localapi

import "testing"

func TestWindowsCredentialPolicyRequiresSingleAccountSID(t *testing.T) {
	const account = "S-1-5-21-123-456-789-1001"
	policy, err := NewWindowsCredentialPolicy(account)
	if err != nil || !policy.permits(account) || !policy.permits("S-1-5-18") || policy.permits(account+"0") {
		t.Fatalf("account authorization changed: %+v %v", policy, err)
	}
	for _, sid := range []string{"", "S-1-1-0", "S-1-5-18", "S-1-5-32-544", account + ";D:(A;;GA;;;WD)", account + "-1"} {
		if _, err := NewWindowsCredentialPolicy(sid); err == nil {
			t.Fatalf("unsafe account accepted: %q", sid)
		}
	}
}
