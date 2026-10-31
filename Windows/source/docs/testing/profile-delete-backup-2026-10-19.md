# Profile deletion blocked by a lab backup — 2026-10-19

## Cause

The mini-PC profile contained `bootstrap-v1.before-FI-20261013.pb`, a backup left by earlier lab work. This is deliberately not an owned profile filename. The deletion allowlist correctly refused it, but only after DELETE_PROFILE had been acknowledged and the runtime stopped. The daemon exited with an error and systemd restarted the unchanged profile, restoring available countries.

There was one daemon and one GUI process; this was not a duplicate-instance or country-cache issue.

## Fix

- Validate profile ownership and contents before acknowledging deletion or disconnecting the runtime.
- Cancel reset preparation on validation failure so the user can correct the problem and retry.
- Return a bounded actionable API error without disclosing arbitrary filenames.
- Retain the final destructive-operation checks; preflight does not replace them or guarantee against later filesystem changes.
- Move the mini-PC backup outside the profile to `/var/backups/irvio-profile-fix-20261019/`; do not broaden the deletion allowlist.

Linux preflight reuses the existing directory identity, marker, regular-file and filename checks. Windows preflight checks directory identity, marker and filenames without locking active SQLite files; its existing final exclusive-removal checks remain unchanged.

## Validation

Tests were compiled on the workstation and executed on the mini-PC, not on the workstation:

- Full productprofile suite with race instrumentation: PASS.
- Full localapi suite with race instrumentation: PASS.
- ProfileReset controller tests, including validation failure/retry: PASS.
- Socket-activated desktop deletion integration: PASS, including rejection while preserving a responsive API followed by successful retry.
- Windows daemon cross-compilation: PASS; Windows runtime tests were not run for this Linux task.
- Live mini-PC with the original unexpected file: API rejects deletion, PID unchanged, zero service restarts.
- After relocating the backup: PROFILE_DELETE_COMPLETE, clean socket reactivation, empty bootstrap, empty countries, DISCONNECTED.
- Reimported three bootstrap entries, observed a populated country response, then repeated deletion successfully.

Installed mini-PC daemon SHA-256: `62ee664ed073bde560916cd09969d39af760bc08ea4547cf9f966c310b137220`.

The GUI was not rebuilt: its existing API deletion path is unchanged. A remote screenshot attempt under Wayland produced a black image, so visual verification is not claimed. The real Local API and daemon lifecycle were verified. The mini-PC is left without a profile; the bootstrap text/QR on its Desktop are retained for reimport. No release archive was rebuilt and no commit was created in this task.
