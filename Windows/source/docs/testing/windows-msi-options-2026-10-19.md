# Windows MSI installation choices — 2026-10-19

The Windows MSI now offers the standard destination directory page, followed
by independent Desktop and Start menu shortcut checkboxes (both on by default).
Default application path: `C:\Program Files\IRVIO`.

The application directory is recorded in protected installation metadata and
used by install, maintenance, uninstall and rollback. Driver, installer cache
and ProgramData locations are unchanged. A custom location must be on a local
fixed NTFS drive under protected parents; user-writable parents and reparse
points are rejected. Legacy records retain the former default path/shortcut.

The daemon's Windows maintenance ownership check was adjusted to read that
protected directory setting rather than assume Program Files. Network
protocols and connection behavior were not changed by this task.

## Validation on IRVIO-Win11-Peer

- Windows Go regressions: custom directory parsing and server ownership PASS.
- PowerShell parser checks PASS.
- Four shortcut combinations, legacy defaults, safe paths and explicit action
  aliases: PASS.
- MSI table checks: destination property and both shortcut choices are secure,
  the shortcut dialog is reachable, backend receives the selections, deferred
  SYSTEM/rollback ordering is retained, action strings fit MSI columns: PASS.
- Existing MSI 1.1.1014 removed through Windows Installer, exit 0.
- Final MSI installed silently into `C:\Program Files\IRVIO Custom Test` with
  Desktop enabled and Start menu unchecked, exit 0.
- Actual protected installation record, service command and shortcut target
  match the chosen directory; no Start menu shortcut was created.
- Starting the daemon from the custom path yielded Local API DISCONNECTED.
  No VPN Connect was requested.
- Final MSI removed through Windows Installer, exit 0; app, driver, installer
  cache, shortcuts and services absent afterwards.
- Retained ProgramData profile and IRVIO UI preferences explicitly erased for
  the user's fresh-install test. No app/service/MSI registration remains.

A first candidate failed before backend installation because `-D` became
ambiguous after adding DesktopOption. An explicit alias and regression check
fixed it. The MSI-formatted commit command was also changed to avoid literal
braces being interpreted by Windows Installer. That candidate was not handed
off. The final tested package is listed below.

WiX reports the existing ICE60 informational warning for the bundled Material
Icons font. No ICE03 action-length warning remains. This is still an unsigned
preview MSI; publisher signing and a broad release campaign are out of scope.
The interactive wizard is left for the user's manual verification; package
tables and an actual unattended install/remove were checked here.

## Handoff

`C:\Users\IrvioLab\Desktop\IRVIO\irvio-1.1.1019-preview-x64.msi`

SHA-256:
`b7538c02615831e1ae4948e2382c862090ef9379a72aa0efd6e35b04b7fed6f1`

The new MSI is not installed at handoff. The old desktop MSI and its checksum
were removed to avoid confusing the two versions. The lab build/evidence
directory remains outside the installation.
