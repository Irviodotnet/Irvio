// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winfirewall

import (
	"fmt"

	"github.com/Irviodotnet/Irvio/internal/winfile"
)

// CheckInactiveJournal rejects live or unresolved firewall ownership without performing recovery.
func CheckInactiveJournal(directory string) error {
	return winfile.InspectLockedDirectory(directory, "control-firewall.lock", func(d *winfile.Directory) error {
		r, err := (&controlFileJournal{dir: d}).Read()
		if err != nil {
			return err
		}
		if r.Rule != nil {
			return fmt.Errorf("firewall journal still owns network state")
		}
		return nil
	})
}
