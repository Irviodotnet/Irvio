// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package windns

import (
	"fmt"

	"github.com/Irviodotnet/Irvio/internal/winfile"
)

// CheckInactiveJournal rejects live or unresolved DNS ownership without performing recovery.
func CheckInactiveJournal(directory string) error {
	return winfile.InspectLockedDirectory(directory, "dns.lock", func(d *winfile.Directory) error {
		r, err := (&fileJournal{dir: d}).Read()
		if err != nil {
			return err
		}
		if r.Active {
			return fmt.Errorf("DNS journal still owns network state")
		}
		return nil
	})
}
