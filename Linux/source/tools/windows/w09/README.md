# W09 — UI Local API transport gate

The Windows UI uses the same Dart framing/negotiation/commands as Linux. Its
platform adapter launches `irvio-ui-bridge.exe` from the UI executable directory,
never from PATH or a shell. Package the helper beside the UI in a protected
Program Files directory and build it with `go build -ldflags=-H=windowsgui
./cmd/irvio-ui-bridge`. This is an ordinary-user child process, not another
privileged service. It accepts no command-line overrides.

The helper opens only the installed `IRVIO` SCM service with START, QUERY_CONFIG
and QUERY_STATUS access. The installer must grant those rights to the selected
desktop account, not STOP, CHANGE_CONFIG or WRITE_DAC. The service must be an
isolated LocalSystem process. Activation waits boundedly, then authenticates
the SYSTEM-owned Named Pipe and verifies its server PID against the running
SCM service before forwarding any API bytes.

The helper forwards opaque bytes with bounded buffers; it has no second protobuf
parser or VPN state machine. EOF in either direction exits the helper process
and releases its remaining blocked stdio handles. Dart closes stdin first and
terminates only this child after a bounded grace period if necessary. Neither
path sends DISCONNECT/SHUTDOWN. The daemon continues after GUI death; intentional
shutdown still uses the existing Local API command and controller polling fence.

Run `bridge-check.ps1 -Source <checkout> -Evidence <new-directory>` through the
disposable Windows VM SYSTEM agent. It refuses an existing `IRVIO` service and
creates two temporary non-admin accounts with random, process-local credentials.
The gate covers SCM state/timeouts, opaque relay, locked Flutter tests, a compiled
Dart smoke client, real standard-user activation/IPC, repeated GUI EOF, excess
service-rights denial, other-user rejection, foreign pipe ownership/PID rejection
before disclosure, and removal of the temporary service/accounts/processes.

The SCM fixture's CONNECT is deliberately in-memory: this gate proves transport
and lifecycle isolation, not network connectivity. `tools/windows/w08/product-idle.ps1`
accepts optional `-UISmoke <windows-local-api-smoke.exe>` to exercise the real
zero-argument product service through Dart before its existing idle lifecycle
and host-state cleanup gates. That invocation is SYSTEM, not the ordinary-user
oracle; the separate two-account fixture supplies that oracle.

The Flutter application's platform startup guard remains until the Windows
runner and desktop integrations are ready. These checks are not full W09 or
Windows-port acceptance.

## Packaged UI runtime loading

Run `ui-payload-load.ps1 -DisposableVM -Payload <verified-payload>
-ManifestSHA256 <trusted-hash> -Evidence <new-directory>` in an elevated
interactive session on a disposable Windows VM. It requires a stopped, disabled
IRVIO service and no existing UI/daemon processes; it does not alter services.
The server fixture's service ACL may prevent a non-elevated CIM preflight, so
this gate is not the ordinary-user activation test described above.

The gate verifies the payload, copies only UI files to a new evidence directory,
limits the child's PATH to Windows directories and checks actual loaded module
paths for all three packaged Visual C++ runtime DLLs and Flutter. An interactive
run must create a window. It terminates only its own UI child and checks helper
exit and unchanged service state. No system runtime is removed or replaced.

Session 0 can establish library loading but cannot establish rendering. This
test does not substitute for a clean-machine installation, standard-user launch,
visual inspection or the complete UI acceptance matrix. Export only the result,
module and log files; the copied `ui` directory contains the executable payload.

## Installed UI with an ordinary interactive token

`installed-user-launch.ps1 -DisposableVM -Evidence <new-directory>` runs inside
the authorized desktop user's interactive session, without elevation. An elevated
coordinator first installs the verified desktop EXE on an unused disposable
fixture and records stopped service state. The helper verifies the installed
shortcut, launches the installed UI with a Windows-only PATH, checks app-local
runtime modules and records its token/session/window evidence. It does not query
privileged server CIM state or loosen service permissions.

After `ui-ready.json`, the coordinator must independently verify that the UI
activated the daemon and that its API remains DISCONNECTED with empty bootstrap.
It then writes `coordinator-release` in the helper's evidence directory. The
helper waits ten seconds, captures the guest desktop, kills only its own UI and
checks helper exit. The coordinator checks that the same daemon PID survives,
performs API Shutdown, verifies native cleanup and restores the lab fixture.
Both sides have bounded waits; a missing marker is not permission to restart a
still-running job. The helper's PASS is not a rendered-screen or connectivity
verdict: screenshots and daemon evidence require independent review.

Wake the VM display before visual assessment, without changing the workstation
display/proxy/network settings. In the Pro fixture, screen-off guest captures
showed a white window while QEMU's framebuffer was black; after waking the guest,
the unchanged installed UI rendered the bootstrap-import page normally. Preserve
those captures rather than claiming a renderer fix. This gate does not replace
an actual non-admin-account matrix, clean Home/Pro validation, signing or full UX.
