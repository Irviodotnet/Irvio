# IRVIO desktop MSI preview

This is a native x64 Windows Installer package for Windows 11 desktop clients.
It reuses the existing validated payload, ACL/service installation and bounded
maintenance/uninstall scripts. It is not an EXE renamed to MSI and does not
start a VPN connection during installation.

The MSI owns a protected installer cache under `Program Files\IRVIO Installer`.
Deferred, non-impersonating actions install/remove the runtime through the
existing backend. The backend owns the selected application directory, driver, services
and protected state; MSI and the backend do not both own the same files.
Rollback actions undo completed runtime installation/removal. Interrupted
backend operations retain existing diagnostics/fencing and are not silently
declared successful. Do not erase protection or transaction records to retry.

## Current scope

- Unsigned development preview, local Windows account, desktop role only.
- Fresh installation, removal retaining profile, reinstallation.
- No in-place conversion of an EXE/manual installation; uninstall it first.
- No automatic MSI upgrades or repair in this version; remove and reinstall.
- Close IRVIO using Shutdown before uninstall. A live UI causes safe refusal.
- Removal does not uninstall unrelated system runtimes or erase user profiles.
- Publisher signing and broad release/upgrade acceptance remain separate work.

## Installation choices

The wizard shows the license, destination folder, shortcut choices and final
confirmation before installing. The default destination is `C:\Program Files\IRVIO`.
Users can choose another dedicated folder on a local fixed NTFS drive.
The installer creates missing parent folders with protected ACLs. Existing
application folders, network drives, user-writable parents, reparse points and
system-data directories are refused; it never weakens service binary protection.

Desktop and Start menu shortcuts are independent checkboxes, both selected by
default. They are machine-wide shortcuts; access to IRVIO's network controls
still belongs to the selected desktop user. Unchecking both is supported;
launch `<selected directory>\ui\irvio.exe` directly in that case.

The protected installation record persists the actual directory and shortcut
choices for maintenance, removal and rollback. Old default-path installations
remain readable. Driver, installer cache and ProgramData state stay at their
existing system locations; the folder choice moves the application payload.

## Build on Windows

Prepare the payload using `stage-payload.ps1` as documented in `README.md`.
Use the matching Visual Studio VC redist files and their notices, not System32.
The current payload still requires valid signatures for third-party drivers
and MSVC DLLs even with `-DevelopmentUnsigned`.

Use the official WiX 3.14.1 binary distribution for this preview builder:
https://github.com/wixtoolset/wix3/releases/tag/wix3141rtm

Verified archive `wix314-binaries.zip` SHA-256:
`6ac824e1642d6f7277d0ed7ea09411a508f6116ba6fae0aa5f2c7daa2ff43d31`.
The compiler is a build dependency, not a client dependency. Its version is
pinned for reproducibility; this does not establish ongoing toolchain support.

```powershell
$hash = (Get-FileHash C:\build\payload\payload-manifest.json -Algorithm SHA256).Hash
.\packaging\windows\build-msi.ps1 `
  -Payload C:\build\payload -ManifestSHA256 $hash `
  -WixDirectory C:\Tools\wix314 `
  -Output C:\build\new-msi -DevelopmentUnsigned
```

Run the resulting `.msi` from Explorer as the desktop user and accept UAC.
For unattended installation from SYSTEM, supply that user's explicit SID:

```powershell
$sid = (Get-LocalUser IrvioLab).SID.Value
msiexec.exe /i .\irvio-1.1.1019-preview-x64.msi /qn IRVIODESKTOPSID=$sid /L*v install.log
```

Uninstall through Installed apps or:

```powershell
msiexec.exe /x .\irvio-1.1.1019-preview-x64.msi /L*v uninstall.log
```

Unattended installs can set `INSTALLFOLDER="D:\Applications\IRVIO"`,
`DESKTOPSHORTCUT=1` and `STARTMENUSHORTCUT=1`. An empty property disables its
shortcut, matching an unchecked box in the wizard.

The MSI's numeric `1.1.1019` version is distinct from the embedded application
version/source revision recorded by `payload-manifest.json`.
