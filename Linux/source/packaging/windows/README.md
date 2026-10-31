# Windows packaging — work in progress

## Headless server installation (development gate)

The development EXE accepts the same product role/bundle intent as the daemon:

```powershell
.\irvio-setup-development.exe --role server --bootstrap 'irvio-bootstrap:v1:<FULL_BUNDLE>'
```

The existing Go bootstrap validator canonicalizes the complete bundle before
elevation; the IPv4 server path requires an eligible public bootstrap/STUN seed.
The installer does not weaken M11 or add verifier/STUN endpoint overrides.
The elevated backend installs daemon, signed driver dependencies, real MMDB and
licenses, but no UI, desktop authorization file or Start Menu shortcut. It creates
the SYSTEM service `IRVIO`, with a literal `irviod.exe --role server --bootstrap`
command, automatic startup and failure restart. `IRVIO-Recovery` is demand-start
for this role; server startup owns its journal recovery under the product lock.
The bootstrap file and its hash-bound installation record are administrator-only.
Critical executable/driver/GeoIP files have explicitly protected ACLs matching
the native loader contract. TCP/4000 and UDP/3478,51820 must be free before a fresh install.

The EXE delegates to `install-or-update-server.ps1`: a fresh installation uses
the fresh backend, while a verified existing server uses role-aware update and
recovery. It refuses a different bootstrap bundle and any desktop installation;
there is no implicit role conversion or adoption of unknown partial files. Default desktop maintenance and
uninstall calls still refuse server records. Failure retains partial owned state and
protected diagnostics instead of recursively deleting a potentially live network
installation. Do not delete runtime journals/protection to force a retry.

The installer waits up to 60 seconds for SCM Running, **not** for a complete
M11/GeoIP/selectable Exit proof. Its completion message makes this distinction.
Inspect `Get-Service IRVIO,IRVIO-Recovery` and protected
`C:\ProgramData\IRVIO\logs\daemon-*.jsonl`. A real second-client traffic proof,
public/NAT readiness matrix, initial copy-journal creation recovery and release signing
remain separate unfinished gates. This is not a supported server release yet.

### Completing interrupted server service setup

Before copying payload files, the fresh/reinstall backend commits a protected
`server-copy.json` receipt binding the package, bootstrap and retained-profile
metadata. Files are written to a reserved `.irvio-install-part` name, verified,
flushed and renamed into place. A retry completes missing/partial files without
overwriting a completed file whose hash differs. Read-only source attributes do
not prevent flushing the owned temporary copy; source payload files are unchanged.
Unknown files/directories, reparse points, writable ownership and existing services
prevent copy re-entry. It neither erases unknown trees nor touches profile data.

After all payload files have been copied and verified, the backend writes a
protected `setup_pending` installation record before creating services.
Repeating the same EXE server command with the same payload and bootstrap can
complete missing SCM registrations and finish startup. It validates every installed
file and every existing service before making changes; it does not overwrite a
damaged payload, adopt a foreign service, change bootstrap or switch package versions
mid-setup. An already-running owned server is completed without restarting it.

The pending record also binds retained uninstall metadata when reinstalling a kept
profile. Those receipts are consumed only after verified startup. A leftover
`installation.json.new` from the final record write is preserved as a protected
`setup-journal-interrupted-<id>.json`, not silently discarded. Identity/bootstrap
and runtime protection are not deleted. Normal server maintenance/uninstall refuses
pending setup until it is finished. The completed record retains its previous schema
and has no setup-only fields.

An interrupted copy can be resumed with the same payload/bootstrap, including a
partial initial `installation.json.new`; preserved journal fragments remain private.
An interruption before `server-copy.json` itself was committed is still a refusal
requiring inspection. Unknown initial directory state is not adopted automatically.
The backend-only `-DevelopmentStopAfter` hook requires an unsigned development
payload and is not a public EXE option. `tests/server-setup-pending-test.ps1` uses
isolated ownership fixtures. `tests/server-setup-resume-test.ps1` is destructive to
a disposable stopped server and tests six interruption points, retained identity,
fixed sockets, repeat completion and graceful cleanup. See the validation report
for executed gates; these checks do not prove arbitrary power-loss recovery.
Its optional `-Phases` includes five copy checkpoints. `tests/server-copy-test.ps1`
checks isolated partial-file/inventory/record invariants.
`tests/server-copy-processkill-test.ps1` pauses the real installer's first daemon
copy through a harness-only Copy-Item interception, forcibly terminates that exact
installer child, and resumes using the EXE. This is process-crash coverage, not
disk/power-loss durability proof.
`tests/server-clean-reinstall-test.ps1` retains the disposable server profile
outside canonical paths, tests an actual fresh EXE installation, erases only that
new test profile, then reinstalls the original retained identity. Never export
its private retained-state directories with public test evidence.
`tests/setup-server-resume-test.ps1` checks completion through the actual embedded
EXE backend after service creation and after daemon startup, including stable PID,
retained identity/bootstrap, repeated setup and native cleanup. Its unattended
development child invocation does not replace interactive UAC acceptance.

`tests/server-installation-test.ps1` checks role isolation, headless file mapping,
ACL/command/driver/bootstrap tampering and busy/free port preflight using disposable
fixtures. `tests/setup-server-test.ps1` is destructive to a disposable desktop
installation: it retains old state outside canonical paths, installs the actual
EXE as server, checks fixed sockets, restart/identity and graceful cleanup. Its
`-FreshOnly` path requires already unused canonical paths. It disables server
autostart at final teardown so the lab does not restart unexpectedly. Never run
the test against a user's installation; its retained state contains private data
and must not be included in exported evidence.

### Server maintenance prerequisite

Elevated installer code can import `server-installation.ps1` and explicitly use
`Stop-IRVIOForMaintenance -Role server`,
`Assert-IRVIOMaintenanceClean -Role server -ProbePath <trusted-daemon> -ProbeSHA256 <digest>`,
then `Restore-IRVIOMaintenanceStartModes -Role server`. The stop records original
start modes, disables/stops the daemon, then disables/stops recovery. Desktop
recovery stays automatic throughout the daemon's WFP teardown; a failed stop
must not disable recovery or authorize payload replacement. Restoration
does not start the server. An interrupted stop retains its record; inspect it
before restoring. This is an internal prerequisite, not the server update UI.

Server maintenance schema 2 binds the manifest and bootstrap digests; desktop
schema 1 stays unchanged. Default maintenance calls still select desktop and
refuse server installations. The native probe accepts the exact server command
only after validating protected server metadata and its canonical full bootstrap.
Neither a running service nor residual protection/journals are accepted. No
force-kill, WFP cleanup bypass, profile erasure or automatic reconnect is added.
Use `tests/server-maintenance-test.ps1` only on a disposable installed server;
`tests/maintenance-record-test.ps1` covers both role-specific record formats.
`tests/maintenance-stop-order-test.ps1` uses isolated SCM fixtures to enforce
the recovery ordering and failure retention. `tests/connected-update-test.ps1`
requires a disposable installed desktop, trusted distinct payload, API helper
and bootstrap file. It connects, probes traffic/protection, updates while
connected, verifies native cleanup and preserved identity, then checks IDLE
startup and an explicit reconnect. It does not exercise downstream leases or
continuously capture packets during teardown; preserve failed state for diagnosis.
`tests/hosting-update-test.ps1` pairs with
`tools/windows/w07/hosting-maintenance-client.ps1` on a second disposable VM.
The client imports the host's private invitation and completes a real transfer;
only then may the coordinator create `client-probe-ready` in the host evidence
directory. The host executes the embedded development EXE while a downstream
lease is active. After its host verdict succeeds, the coordinator creates
`host-update-complete` in the client evidence directory. Both wait phases are
bounded to five minutes. The client checks fail-closed behavior and explicit
Disconnect cleanup. Export neither invitation files nor retained profiles;
both sides' results are required, not only the host verdict.

The client helper's optional `-DNSForensic` records cache observations around
Disconnect without flushing DNS. Probe and Disconnect durations use a monotonic
stopwatch; UTC markers are diagnostic only. If the first post-Disconnect probe
fails, a separately labelled direct-DNS/followup probe does not convert that
failure into PASS. Packet captures are external lab evidence, not implied by
this helper's verdict. Keep guest and host clock domains separate.

### Role-aware software replacement backend

The internal `update-desktop.ps1` and `recover-update.ps1` backends also accept
explicit `-Role server`; their historical filenames and desktop default remain
compatible. Both require a trusted payload/manifest and the existing ownership
checks. Server replacement skips UI files, retains the full bootstrap record,
identity/profile and unchanged driver payload, and keeps staged server files
protected. It uses the same durable update, rollback and recovery transaction.
Neither backend automatically starts a stopped server after restoring SCM modes.
The EXE wrapper additionally starts a verified automatic-start server after update
or an idempotent setup invocation. Existing Manual/Disabled policy is preserved
without starting the service; an already running same-package server is not
restarted. Identity and exact bootstrap ownership cannot be changed by an update.
`tests/setup-server-update-test.ps1` covers this EXE workflow; see the validation
report for completed versus pending native gates.

The update, interruption and recovery-reentry harnesses accept `-Role server`.
Use only on a disposable installation: they intentionally interrupt their own
installer children at named points, preserve rollback archives and verify exact
profile/ownership/mode retention. Process-interruption coverage is not power-loss
coverage. `tests/update-record-test.ps1` checks unchanged desktop ownership and
server bootstrap binding without operating on installed services.

## Profile-aware uninstall backend (development gate)

The same internal backend accepts explicit `-Role server` for a headless
installation. Server journal schema 2 binds its existing bootstrap/manifest
record and does not claim a desktop shortcut or UI files. The default remains
desktop/schema 1 and refuses server state; `-DesktopSID` is invalid with server.
Both roles use the same native cleanup fence and exact-file/service removal.
Server bootstrap configuration remains protected when the application/profile
is removed. The development EXE exposes server removal through:

```powershell
.\irvio-setup-development.exe --role server --uninstall
```

It asks whether to retain or erase the profile before elevation, then confirms
server shutdown/removal. The administrator-only internal child preserves that
choice; no bootstrap or desktop SID is supplied for removal. Repeating removal
must use the same recorded profile choice.

Reinstall with the normal server role/bootstrap command accepts a completed
protected server receipt only with the same deployment bootstrap. It validates
the saved manifest and absence of unfinished maintenance, services or application
objects, and refuses mixed desktop authorization or profile resurrection after
recorded erasure. It preserves retained identity, or creates a new identity when
the erased server starts. Reinstallation starts the server with automatic startup,
as a fresh installation does; it is not an update preserving a Disabled mode.
Initial copy-journal creation repair remains unfinished; committed server copy/setup can
resume as described above. Keep diagnostics and ownership records rather than
deleting them to force a retry.

`tests/uninstall-role-test.ps1` checks role/schema isolation, component projection
and fenced exact server command ownership with disposable fixtures.
`tests/server-uninstall-test.ps1` is destructive to a disposable stopped server:
it tests keep/erase intent through four removal interruptions and idempotent
completion, then restores a protected lab backup. This restoration is not a
product reinstall proof. Its `private-backup` and `private-retained-state`
directories contain private identities/configuration and must never be exported.
`tests/server-preserved-test.ps1` covers retained-receipt refusal cases without
touching installed services. `tests/setup-server-uninstall-test.ps1` runs the
actual development EXE against a disposable server, erases its identity and
verifies that reinstallation generates a new one. It checks fixed sockets and
native cleanup, not public M11 or second-client connectivity.

`uninstall-desktop.ps1 -Payload <verified-package> -ManifestSHA256 <trusted-digest>
-DevelopmentUnsigned` removes a canonical owned desktop installation after its
UI is closed. It preserves protected ProgramData profile, configuration and
diagnostic/update history. The single-EXE frontend exposes this through
`--uninstall`; Apps & Features registration and repair remain unfinished.

Explicit `-RemoveProfile` uses the candidate daemon's internal
`--maintenance-remove-profile` operation after native clean-state validation.
It erases owned profile contents (identity/AWG keys, stored peers/bootstrap and
private access state), not configuration, diagnostic/update history or UI
preferences. This is logical deletion, not a guarantee of physical secure erase
on storage. The default remains profile preservation. The selected choice is
recorded before mutation; a resumed or repeated operation cannot change it.
Reinstallation retains the authorized desktop SID in either mode and rejects
unexpected profile data appearing after recorded erasure.

The backend verifies manifest, ACLs, shortcut and service ownership; fences and
gracefully stops the services; and runs the independent native clean-state probe.
It removes only manifest-owned files and empty manifest-derived directories,
then deletes exactly the owned stopped service/driver registrations. Unknown
files/directories or changed binaries cause refusal, not recursive deletion.
SCM configuration/status is queried directly: WMI can omit a stopped driver
whose binary has already been removed. Start Menu shortcuts receive explicit
administrator/SYSTEM write access and Users read access when installed.

`uninstall.json` and the saved manifest allow resuming process interruption at
removing, first-file, files-removed and first-service checkpoints. Completion
leaves `uninstalled.json`; repeating removal checks that files and services remain
absent. Installing again accepts the retained profile only for its original
authorized desktop SID and keeps that profile unchanged. It does not connect a
VPN. Pending uninstall excludes update/recovery, and removal refuses pending
updates. Do not remove transaction records to bypass a refusal.

`tests/uninstall-test.ps1 -Scripts <backend-directory> -Payload <verified-package>
-ManifestSHA256 <digest> -Evidence <new-protected-directory>` exercises a stopped
disposable installation: unknown-file preservation, four interrupted removals,
reentry/idempotence, wrong-user refusal, same-user reinstallation and native
cleanup. Initial metadata-write interruption, real power loss, active sessions,
downstream leases, GUI file-holder races and deletion of all application data remain gates;
the four injected process exits do not prove those cases.

`tests/uninstall-ownership-test.ps1 -Helper <uninstall.ps1> -Evidence <new-path>`
checks only disposable file fixtures and service-name validation. It does not
modify canonical services or profile state, and does not replace the real
interrupted-removal gate.

`tests/maintenance-profile-test.ps1` is destructive only to the disposable
installed profile. It checks unfenced/runtime-busy/intent/unknown-file refusals,
then erases and repeats the operation. `tests/uninstall-profile-test.ps1` covers
explicit erase choice across interruption, removal and reinstallation, including
new identity generation. Its opt-in native fixture creates real identity/AWG
and SQLite files under the existing fenced desktop installation without joining
the network. Never run either test against a user's profile.

## Software update backend (development gate)

`update-desktop.ps1` updates a verified canonical desktop installation while
retaining the profile, desktop authorization and unchanged driver installation.
The single-EXE development frontend selects this backend for an existing owned
installation. Close the UI first; use only in the disposable Windows lab until
full installer/release acceptance is complete.

```powershell
.\packaging\windows\update-desktop.ps1 `
  -Payload C:\build\payload -ManifestSHA256 <trusted-manifest-sha256> `
  -DevelopmentUnsigned
```

The backend refuses modified/unowned installed files, changed driver payloads,
split-volume installations and existing update transactions. It stages and checks
the candidate, gracefully fences/stops the owned services, runs the read-only
native cleanup probe and retains the maintenance lock across directory swaps.
Program/GeoIP replacement and installation metadata have synchronous rollback;
normal completion restores prior service start modes without starting a VPN.
The candidate daemon must implement the internal maintenance-check command.

`ProgramData\IRVIO\software-update` retains the journal and replacement/backup
directories. Do not delete it to force another update. A separate elevated
`recover-update.ps1 -Payload <same-candidate> -ManifestSHA256 <trusted-digest>
-DevelopmentUnsigned` validates the pending journal and actual file placement.
Uncommitted replacements are rolled back; a verified committed replacement is
kept. Services must retain their maintenance fence before any replacement is
reversed. Recorded service modes are then restored without starting a VPN.

Update and recovery share a private exclusive lock, including staging and
finalization. The recovery tool moves the entire terminal transaction to
`ProgramData\IRVIO\update-history\<transaction-id>` instead of deleting evidence.
Interrupted metadata fragments are preserved there too. Archive retention is
not yet bounded; do not ship this as a final maintenance UX. The current tool
refuses malformed/missing ownership journals, partial service-stop failures,
unknown filesystem states and changed ownership rather than guessing a repair.
Schema-changing updates, driver upgrades, active-session/downstream gates,
power-loss validation and frontend integration remain unfinished.
The lab-only `-InjectCrashAfterStep` terminates the updater process without
running its rollback/finally blocks; it does not simulate storage power loss.
Recovery has its own development-only interruption checkpoints. Re-running it
after finalization returns a verified no-op if no other maintenance is pending.

`tests/update-desktop-test.ps1` takes `-UpdateScript`, `-Payload`,
`-ManifestSHA256` and a new `-Evidence` directory. It requires a stopped,
disposable real installation and a development candidate containing a trusted
maintenance probe. It exercises preflight refusals, three replacement failures,
inventory/profile/start-mode preservation, successful replacement, pending
transaction refusal and the final independent native cleanup inspection.
Only its own unchanged sentinel file is removed; rollback trees are retained in
private evidence. It deliberately leaves the successful transaction in place.

`tests/update-recovery-test.ps1` takes `-Scripts`, `-Payload`,
`-ManifestSHA256` and a new `-Evidence` under an already protected lab directory.
It terminates child updaters at eight boundaries and runs recovery separately;
use only a disposable stopped desktop installation. It also checks concurrent
operation refusal, journal-path tampering, preserved metadata fragments,
profile/authorization/start-mode preservation and final native cleanup.
`tests/recovery-reentry-test.ps1` exercises six interruptions of recovery itself
with the same fixture arguments. Both tests leave the daemon stopped and retain
transaction history; neither simulates loss of unflushed storage writes.

## Payload staging

`stage-payload.ps1` assembles an audited input directory for the Windows installer.
It does **not** install services, drivers or firewall rules, or claim that the
Windows port/release acceptance is complete. Run packaging checks in a Windows
VM, not on the developer workstation.

Supply a release Flutter Windows bundle (including its same-build UI bridge),
the daemon, Wintun 0.14.1 AMD64, WinDivert 2.2.2-A AMD64, DB-IP Country Lite MMDB
and attribution, and a collected license directory. The latter must contain
`AGPL-3.0-only.txt`, `WINTUN-LICENSE.txt`, `WINDIVERT-LICENSE.txt`, `GO-MODULES.txt`
`FLUTTER-LICENSE.txt` and `MSVC-LICENSE.txt`, plus the dependency notices. No dependency is downloaded
or silently selected from PATH by this script. Supply the appropriate MSVC
runtime DLLs in the UI release bundle with their redistribution notices.
The current AMD64 bundle requires `msvcp140.dll`, `vcruntime140.dll` and
`vcruntime140_1.dll`; staging and installation reject a manifest missing them.
Take these from the matching Visual Studio `VC/Redist/MSVC/<version>/x64/`
`Microsoft.VC143.CRT` directory, not arbitrary System32 copies. Staging checks
AMD64 and valid Microsoft Authenticode signatures even for a development build.
Supply the applicable Microsoft redistribution terms as `MSVC-LICENSE.txt`.
App-local deployment makes servicing these DLLs part of IRVIO package updates;
the installer must not delete or overwrite the shared system runtime. See
[Flutter deployment](https://docs.flutter.dev/platform-integration/windows/building)
and [Microsoft deployment guidance](https://learn.microsoft.com/cpp/windows/redistributing-visual-cpp-files).

```powershell
.\packaging\windows\stage-payload.ps1 `
  -Daemon C:\build\irviod.exe -UIBundle C:\build\ui-release `
  -Wintun C:\dependencies\wintun\bin\amd64\wintun.dll `
  -WinDivertDirectory C:\dependencies\WinDivert-2.2.2-A\x64 `
  -GeoIP C:\dependencies\country.mmdb `
  -GeoAttribution C:\dependencies\DB-IP-ATTRIBUTION.md `
  -Licenses C:\build\licenses -Version 1.1.0-alpha `
  -SourceRevision <full-commit> -Output C:\build\new-payload
```

The manifest records every copied file's size/hash and the **declared** source
revision: hashes do not prove that supplied binaries were built from that commit.
The release builder must supply reproducible build provenance separately. Output
must be new; an incomplete directory is retained for diagnosis and has no final
manifest. Inputs and parent paths must not traverse reparse points. Private
profiles, arbitrary build directories, PDBs and test executables are not inputs.

Pinned third-party driver hashes and valid Windows Authenticode verification are
mandatory even with `-DevelopmentUnsigned`. That explicit lab-only switch skips
only the signature requirement for our three EXEs and marks the manifest as
development-only. A release installer must refuse such a payload. SHA-256 is
integrity, not a substitute for publisher authentication. Installer signing,
service/ACL registration, upgrade/rollback, uninstall and their native gates are
still separate unfinished W11 work.

Native fixture checks (no service or network changes):

```powershell
.\packaging\windows\tests\stage-payload-test.ps1 `
  -StageScript .\packaging\windows\stage-payload.ps1 `
  -Daemon C:\build\unsigned-irviod.exe `
  -Wintun C:\dependencies\wintun\bin\amd64\wintun.dll `
  -DriverDirectory C:\dependencies\WinDivert-2.2.2-A\x64 `
  -MSVCRuntimeDirectory C:\dependencies\Microsoft.VC143.CRT `
  -Evidence C:\lab\new-payload-check
```

These deliberately synthetic UI/MMDB/license fixtures exercise packaging
validation only and must never be shipped. The real release build must validate
MMDB through the existing CountryResolver and collect actual dependency notices.

## Fresh desktop installation backend (development gate)

`install-desktop.ps1` is the elevated backend for the future single-EXE installer,
not the final end-user distribution. It currently supports **fresh desktop
installation only**. It refuses existing services/directories/profiles rather
than treating a manual installation as an upgrade. Server, repair, upgrade,
uninstall and interrupted-install recovery remain separate gates.

```powershell
.\packaging\windows\install-desktop.ps1 `
  -Payload C:\build\payload -ManifestSHA256 <trusted-manifest-sha256> `
  -DesktopSID <explicit-local-desktop-user-sid> -DevelopmentUnsigned
```

The final signed installer must embed the expected manifest hash and capture the
intended desktop user before elevation; never silently authorize the alternate
administrator supplied at a UAC prompt. The explicit development switch is for
the disposable lab. User-supplied hashes do not authenticate a download.
`payload.ps1` checks the manifest digest, signing mode, path syntax, duplicates,
required components and every copied file's size/hash. It copies no unmanifested
files. `tests/payload-test.ps1` exercises its rejection paths on a private copy.

Installed layout and ownership:

- `%ProgramFiles%\IRVIO\daemon`: daemon and Wintun; SYSTEM/Administrators only.
- `%ProgramFiles%\IRVIO\ui`: unprivileged Flutter application and bridge.
- `%ProgramFiles%\IRVIO Exit Driver`: pinned WinDivert DLL/driver; protected.
- `%ProgramData%\IRVIO`: explicit protected authorization, GeoIP and runtime state.
- Common Start Menu `IRVIO.lnk`: UI launch only, no VPN command or login autostart.

Directories are created with their final ACLs in the native create operation;
immutable private files also receive explicit protected ACLs, as required by
Core. IRVIO is configured disabled until payload/configuration is committed,
then demand-start with its WinDivert dependency and existing crash-restart
policy. The selected user can query/start it, but not stop/reconfigure it via
SCM; the authenticated API owns Shutdown. The independent automatic recovery
service uses `--boot-recover`. Installation starts neither the UI nor a VPN.
Caught pre-start failures roll back only objects created by that invocation;
if a service was started externally, the installation is retained for safe
teardown. Power-loss rollback and an authenticated repair path are not yet
implemented. App-local MSVC payload checks exist; a clean-host UI launch without
preinstalled Visual Studio/runtime, redistribution notice review and release
signing remain pending acceptance gates.

Native `TestWindowsInstalledDesktopActivation` is opt-in via
`IRVIO_INSTALLED_DESKTOP_TEST=1`, with disposable standard-account credentials
in `IRVIO_PIPE_ALLOWED_USER/PASSWORD`. It runs the installed bridge under that
non-admin token, verifies narrow SCM/file rights, demand activation, IDLE/EMPTY
and daemon-owned Shutdown. Do not set these fixture variables for normal use.

## Installed ownership inventory (read-only)

`installation.ps1` provides `Read-IRVIOInstallation` for the upcoming update and
uninstall backends. It checks protected installation metadata, its manifest
digest, every mapped installed file's hash/size and ACL, parent reparse points,
desktop authorization consistency, and the exact IRVIO/recovery/WinDivert service
paths, account and service types. It does not trust a matching service name alone.
Only the canonical installed desktop layout is accepted by the public helper;
the lower-level file validator also supports isolated native fixtures.

This inventory intentionally understands earlier schema-1 installations whose
manifest predates the mandatory app-local MSVC files. It is not validation of a
new candidate payload, which still goes through the stricter `payload.ps1`.
It does not stop services, remove files, repair a modified installation, inspect
live WFP state or prove that runtime teardown completed. Those operations and
their rollback gates remain required before an uninstall can be called safe.
Future removal must delete only verified owned objects, not recursively wipe
an installation directory because the current inventory passed.

Run `tests/installation-test.ps1 -Helper <installation.ps1> -Evidence <new-dir>`
in a disposable elevated Windows VM. `-VerifyInstalled` additionally inspects
the real installed desktop without changing it. Negative fixture tests cover
payload tampering, manifest mismatch, traversal/duplicates, authorization drift,
unprivileged write access, reparse points and foreign service/driver definitions.

## Single-EXE development frontend

`cmd/irvio-setup` wraps installation/update/removal in one native AMD64 executable.
The executable embeds a manifest-pinned payload archive and its thirteen backend scripts;
no adjacent scripts/archive or dependency download is required at installation.
It opens as the original desktop user (`asInvoker`), confirms that account, then
requests UAC elevation while carrying the same SID to the elevated backend.
Extraction uses private native Windows directories/files under a random
`ProgramData/IRVIO-Setup-*` path, not an executable script in a shared temp folder.
Archive names, entry kinds, duplicate paths, sizes and manifest digest are checked.
The dispatcher rechecks payload hashes and refuses changing the authorized
desktop SID of an existing installation. A missing installation record goes to
the fresh-install backend, which still refuses unowned/manual leftovers.
For an existing owned installation, a pending transaction is recovered using
the same candidate before updating. A different candidate for a pending update
is refused; retain the original installer for recovery. An already-installed
matching package is verified without restarting services or changing profiles.
Other pending maintenance and damaged ownership/inventory are refused; this
does not yet implement repair.
Success removes its staging directory; failure retains a protected `install.log`.
Neither successful installation nor the completion dialog starts a VPN session.

Run the same EXE as the original authorized desktop user with `--uninstall` to
remove IRVIO. Before elevation it asks whether to erase the connection profile
(default: keep), then separately confirms removal and the selected choice.
Cancel in the first dialog or No in the final confirmation exits successfully
without starting a transaction. The original user's SID and profile choice are
carried through UAC; the backend checks ownership under its transaction lock,
including resumed and completed removal. Do not launch as a different desktop
user merely to gain administration rights; use the elevation prompt instead.
The zero-argument action is still installation/update, not an action picker.
Apps & Features registration and a retained installed uninstaller are pending.

`tests/setup-uninstall-test.ps1` exercises the actual built development EXE:
wrong-owner refusal, keep/erase, choice fencing, idempotence, both reinstall
paths and native cleanup. It deliberately erases a disposable populated profile.
Its elevated unattended execution does not prove interactive alternate-account
UAC removal. The separate visible-dialog gate checks all three cancellation
paths without elevation or application/profile changes.

`tests/setup-update-test.ps1` exercises a built development EXE in an elevated
disposable VM: missing-program recovery, wrong-target SID refusal, completed
update, same-package no-op and native cleanup. It uses the explicit lab-only
unattended switch, not a claim of interactive UAC testing for every update path.

```powershell
.\packaging\windows\build-setup.ps1 `
  -Payload C:\build\payload -ManifestSHA256 <verified-manifest-sha256> `
  -Go C:\Tools\go\bin\go.exe `
  -Windres C:\Tools\llvm-mingw\bin\x86_64-w64-mingw32-windres.exe `
  -Output C:\build\new-setup -DevelopmentUnsigned
```

The builder uses temporary ignored `payload.zip`/COFF resource inputs inside
`cmd/irvio-setup`, refuses to overwrite them, and removes only its own inputs on
exit. It verifies the final PE's embedded manifest is `asInvoker/uiAccess=false`
and runs the embedded bundle integrity check. Use a private Windows build tree;
do not build concurrently in the same tree. A bare `go build ./cmd/irvio-setup`
without the archive/digest deliberately produces no usable installer.

**Development only:** release signing/provenance is not yet configured, so the
builder refuses an invocation without the explicit development switch. The lab
may use the development-only unattended internal entry point; this is not a
public server-install CLI. Actual interactive UAC with an alternate administrator
has passed on the Pro lab VM; see `windows-port-validation.md`. Clean-host
GUI/MSVC loading, server mode, active-session upgrade, repair, complete uninstall UX,
interrupted fresh-install recovery and signed release acceptance remain pending. The original
Microsoft runtime license document is bundled alongside its text extraction;
packaging a notice does not replace review of redistribution obligations.

## Maintenance stop prerequisite

`maintenance.ps1` provides `Stop-IRVIOForMaintenance` and
`Restore-IRVIOMaintenanceStartModes`. Both require the protected installed-file
and SCM ownership inventory to pass. An exclusive file handle serializes these
operations. Before disabling demand/boot activation, a flushed private
`maintenance.json` records the original start modes and installed identity.
An unfinished operation refuses another stop; restoration reads the durable
record, checks ownership again and restores only the recorded service modes.
It deliberately does not restart the daemon or reconnect the VPN.

Stop uses SCM's normal stop request and a bounded wait, not process termination.
A stop failure/timeout retains the record and does not remove protection, files,
profiles or drivers. The driver is left installed and untouched. Zero SCM exit
and no remaining installed daemon process are prerequisites, **not proof of
complete DNS/routes/WFP cleanup**. Independent native cleanup checks remain
required; this helper alone must not authorize deleting the runtime journals.
It also does not yet fence a GUI process holding installed files.

The stop and restoration primitives do not constitute transactional update,
repair, uninstall, or automatic interrupted-operation recovery. Those backends
must preserve/restore this journal across their file/service transactions.
Use `tests/maintenance-test.ps1` only against a stopped disposable Windows
desktop installation; it temporarily starts and stops its real service.

`irviod.exe --maintenance-check` is an internal read-only Windows probe, not a
cleanup command. It requires elevated access and canonical desktop services
that are both disabled and stopped, rejects remaining product intent/journal
ownership and native WFP roots, and refuses a remaining product tunnel. Linux
rejects the command without starting its runtime. Other CLI arguments cannot be
combined with this operation.

`Assert-IRVIOMaintenanceClean -ProbePath <trusted-daemon> -ProbeSHA256 <digest>`
pins and checks a trusted candidate daemon before invoking that probe, then
independently checks hidden adapters and active-policy IRVIO firewall groups.
The digest must come from the verified installer payload, not an arbitrary
end-user download. A probe timeout terminates only that read-only child, not the
installed daemon. The maintenance journal and existing network evidence remain
untouched on refusal. This supports checking older installed payloads using the
new installer's probe; it does not require executing an old daemon that lacks
the command.

Neither helper deletes files or authorizes a future unguarded operation. The
update/uninstall transaction must keep activation fenced and serialize its
mutations with the same maintenance lock. The current update/recovery backends
compose these helpers; profile-aware repair/uninstall and the remaining
interruption cases above are still pending.
