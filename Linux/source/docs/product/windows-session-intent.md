# Windows session intent — W08 foundation

Status: implemented storage primitive, not complete product lifecycle acceptance.

The Linux State and v1/v2 validation move unchanged into `productintent/state.go`.
Linux keeps `/run/irvio/session-state`, existing JSON bytes and file operations.
Windows uses Known Folder ProgramData + `IRVIO/runtime/session-state`, with a
versioned envelope around that same State and private native file operations.
The installer must provision protected ancestors; the store creates only its
final runtime directory. No credentials or private authorization are added.

## Session boundary

An actual VM restart demonstrated that the GUID from
`SystemBootEnvironmentInformation` remained unchanged while uptime reset. It
identifies the selected boot environment, not a sufficient per-boot fence. A
volatile registry key alone is also insufficient for hybrid startup. Neither is
used here, and neither wall-clock-minus-uptime nor formatted boot dates are used.

The Windows fence is the service-manager process instance: PID plus its exact
creation FILETIME from an open native process handle. Identification checks the
image path against the native system directory's `services.exe`, LocalSystem
token, and session 0. Wrong-path same-name processes are ignored; missing,
ambiguous or unqueryable trusted identity is an error, not permission to resume.
The caller runs as LocalSystem. No shell or WMI parsing is involved.

SCM starts at system boot. The fingerprint is expected to survive daemon crash,
GUI death, suspend and ordinary hibernation, but change when the OS creates a new
service-manager instance. This uses process-instance metadata, not the current
clock or a guessed boot timestamp. Creation time is not an authentication secret;
file ACLs establish trust. Administrators replacing OS executables, restoring
whole-machine snapshots or deliberately rewriting trusted state are outside the
ordinary-user threat model. Native process-kill, real restart/cold boot and
Hibernate resume have component-level evidence. Real Fast Startup and sleep
still require evidence; a requested hybrid shutdown is not proof of resume.

References: [SCM startup](https://learn.microsoft.com/en-us/windows/win32/services/service-control-manager),
[process creation metadata](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-getprocesstimes),
[Fast Startup versus hibernation](https://learn.microsoft.com/en-us/windows-hardware/drivers/kernel/distinguishing-fast-startup-from-wake-from-hibernation).

## Persistence and lifecycle contract

- Read/write/Clear hold an operation-scoped native exclusive lock in a pinned
  private directory. No process-lifetime handle is needed while IDLE.
- Writes use flushed, handle-relative atomic replacement; unsafe ACLs, links,
  malformed state, occupied/read-only targets and unknown versions fail without
  replacing prior state. The record has a strict byte bound.
- Same-session restart exposes the saved Global country or exact private
  target/hosting intent. A saved teardown marker never becomes CONNECT.
- A different session returns `ErrPreviousBoot`, an empty State and `present=false`;
  it neither resumes nor automatically discards previous protection evidence.
- Replacing old-session intent is also refused. Product boot recovery must clean
  owned resources, then explicitly Clear before accepting a new CONNECT.
- The existing lifecycle controller still serializes user intents and teardown.
  The storage lock is not a replacement for controller generation fencing.

This module does not reconnect, remove WFP filters, modify DNS/routes or start
network participation. Wiring it into the production service, crash adoption,
boot cleanup, Private hosting recovery and traffic leak oracles remains required.
