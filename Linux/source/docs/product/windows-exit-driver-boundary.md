# Windows Exit driver trust boundary — staged component

`internal/windivert` validates and pins the official WinDivert 2.2.2-A x64
payload. Typed packet send/capture now exists, but is not connected to the
production runtime or `exitnat`. Together these are work toward full CLIENT+EXIT,
not a client-only substitute or Windows release acceptance.

## Package ownership and trust

The only public load path comes from the native Program Files Known Folder plus
`IRVIO Exit Driver`. No current-directory, PATH, environment or CLI DLL lookup is
accepted. The dedicated directory and both files require protected SYSTEM/admin
ownership and ACLs, no reparse traversal and no hardlinks. Files are opened with
read sharing only: existing writers block validation; later writers, rename,
replacement and deletion are denied while those handles remain alive.

Hashes are pinned to the official 2.2.2-A x64 payload:

| File | SHA-256 |
| --- | --- |
| `WinDivert.dll` | `c1e060ee19444a259b2162f8af0f3fe8c4428a1c6f694dce20de194ac8d7d9a2` |
| `WinDivert64.sys` | `8da085332782708d8767bcace5327a6ec7283c17cfb85e40b03cd2323a90ddc2` |

These derive from the already verified official archive
`63cb41763bb4b20f600b6de04e991a9c2be73279e317d4d82f237b150c5f3f15`.
The DLL is unsigned; its integrity is established by its pinned vendor hash.
The SYS additionally passes native WinVerifyTrust with no UI, cached-only trust
and chain revocation checking. Unknown/offline trust is an error, not a bypass.
Installer provisioning of a valid trust cache on clean Home/Pro machines remains
an acceptance gate. Kernel Code Integrity still decides whether the driver loads.

LoadLibraryEx searches only the verified DLL directory and System32. The loaded
module and payload handles remain pinned until process exit: vendor synchronous
I/O owns per-thread TLS events. Upgrades must stop the daemon before replacing
this package; there is no live DLL replacement or downloaded fallback.

## Service boundary

Runtime does not create, start, stop, delete or change the driver service. It
requires a running demand-start kernel service `WinDivert` whose binary path
matches the verified package. A missing, stopped or differently installed
service fails explicitly; another application's WinDivert is not commandeered.
The future installer owns installation/upgrade/uninstall and dependency startup.

The current `Probe` checks ABI compatibility by opening a send-only forward-layer
handle with filter `false` and **NO_INSTALL**, then closing it without sending
anything. It cannot capture user traffic. Each synchronous vendor call sequence
runs on a dedicated locked thread, excluding Go's startup `m0`. The caller waits
for OS-thread termination, including DLL detach, rather than just a Go channel
notification. A failed probe close retains
the handle and prevents additional opens until cleanup succeeds.

## Typed packet I/O

`OpenReplyCapture` selects only an explicit source IPv4/interface/protocol/port;
its caller must retain exclusive native ownership of that port. There is no
catch-all host capture or reinjection of unknown host packets. Each handle has
a 32-packet/65,535-byte/200-ms driver queue and one in-flight userspace operation.
`OpenForwardSender` creates a send-only forward-layer handle. It checks the
source and revalidates the caller-supplied interface binding; it does not replace
the upstream resolver, generation fence or NAT authorization policy.

Read/send use overlapped native I/O with pinned Go buffers, metadata and completion
records. The shorter of caller deadline and two seconds initiates cancellation.
No buffer is unpinned until kernel completion is confirmed. Close fences admission,
cancels pending I/O and waits for resource cleanup; a two-second close timeout
retains the handle for retry. Cancellation can race a successful send, so an
error does not prove that no packet left the interface. A malfunctioning kernel
driver that never completes cancellation cannot be made safe by freeing its
buffers: that outstanding operation remains owned, not silently abandoned.

Handle creation/vendor DLL thread teardown still uses synchronous native calls;
this is not a claim of hard deadlines under arbitrary kernel/loader hangs.
Interface-change/reuse races, driver/BFE loss and full runtime adoption remain
separate integration gates. Current packet tests prove literal IPv4 UDP physical
send and exact-port reply capture, not NAT, ProtectedMode or anti-multihop.

## Required next integration

- Owner-scoped installer service registration, conflict diagnostics and rollback;
  preserve third-party instances and include vendor LGPL/license notices.
- Connect capture/send to reservation ownership and generation fencing, then
  validate crash/driver/BFE recovery and physical interface identity under churn.
- Feed authenticated AWG plaintext through lease-owned `exitnat` checks before
  any forward injection, with no bypass of source/Safety/host/LAN restrictions.
- Complete packet support, real mixed-OS downstream Internet and CLIENT+EXIT,
  Private hosting, resource/performance and clean Home/Pro acceptance.

References: [official release](https://github.com/basil00/WinDivert/releases/tag/v2.2.2),
[vendor DLL implementation](https://github.com/basil00/WinDivert/blob/v2.2.2/dll/windivert.c),
[WinDivert API](https://reqrypt.org/windivert-doc.html),
[Windows trust flags](https://learn.microsoft.com/en-us/windows/win32/api/wintrust/ns-wintrust-wintrust_data).
