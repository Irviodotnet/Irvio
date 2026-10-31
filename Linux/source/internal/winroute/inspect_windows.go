// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

package winroute

import (
	"fmt"

	"github.com/Irviodotnet/Irvio/internal/winfile"
)

// CheckInactiveJournal rejects live or unresolved route ownership without performing recovery.
func CheckInactiveJournal(directory string) error {
	return winfile.InspectLockedDirectory(directory, "routes.lock", func(d *winfile.Directory) error {
		r, err := (&routeFileJournal{dir: d}).Read()
		if err != nil {
			return err
		}
		if len(r.Prefixes) != 0 {
			return fmt.Errorf("route journal still owns network state")
		}
		return nil
	})
}
