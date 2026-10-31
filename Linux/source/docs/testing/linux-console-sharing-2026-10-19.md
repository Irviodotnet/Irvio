# Linux console Private VPN sharing — 2026-10-19

Baseline: `windows-port`, `f00dbd12559ffcaf00adf8a9a69a63eafb1221d1`.
This is a working-tree feature, not a published release. Other pre-existing
runtime and Windows packaging edits were not part of this change.

## Implementation

`cmd/irvio` is an unprivileged Linux Local API client. `--mode client` starts
existing private hosting, not Global VPN. `--bootstrap` accepts the existing
network bundle, a UTF-8 text file, or PNG/JPEG QR. Existing bootstrap validation
and signed Private VPN invitations are reused; no protocol or Core changes.

The command requires the installed daemon/socket. It does not implement a second
daemon, installer or network stack. The Ubuntu launcher selects console mode
when flags are supplied; ordinary `irvio` still opens the GUI.

The invitation is created exclusively in the working directory, mode 0600,
one-hour redemption, first-device auto-approval. Console output contains the
file path, not the invitation secret. Existing active sessions are refused.
INT/TERM/HUP request normal DISCONNECT; profile and approvals are retained.
SIGKILL of the console does not stop the daemon; use UI DISCONNECT afterwards.
Avoid controlling the same session concurrently from the GUI and console.

## Validation

All tests ran on the mini-PC or Windows Peer, not on the workstation.
Go compilation and test executable preparation ran on the workstation.

| Check | Result |
| --- | --- |
| sharecli unit tests, race detector, three repetitions | PASS |
| Local API package tests, race detector | PASS |
| productbootstrap package tests, race detector | PASS |
| Literal network bundle, text file, real Desktop QR input | PASS |
| Malformed input, directory, missing file, oversized file, FIFO | Rejected |
| Active session refusal | PASS; existing sharing left intact |
| Invitation file mode and secret-free stdout | 0600, PASS |
| Start failure/ambiguous timeout/invalid invitation cleanup unit tests | PASS |
| Windows invitation enrollment through production network | PASS |
| Mini-PC downstream TunnelLease and member connected | PASS |
| Windows DNS and HTTPS through private connection | PASS |
| Windows route to 1.1.1.1 | irvio0, 0.0.0.0/1 |
| HTTPS download without proxy | HTTP 200, 1,048,576 bytes |
| SIGINT while Windows connected | Mini-PC DISCONNECTED, clients=0 |
| Windows after host stop | RECOVERING; explicit DISCONNECT returns idle |
| QR run terminated with SIGTERM | Exit 0, DISCONNECTED |
| Installed launcher plus final trimpath console binary | PASS, HOSTING |
| Windows reconnect using saved friend, no new invitation | PASS, CONNECTED + HTTPS 1 MiB |
| Final cleanup | Windows and mini-PC DISCONNECTED |

The Windows VM had no network bootstrap configured after its earlier clean
installation. Initial enrollment failed until the ordinary network bootstrap
was imported. No production fix or bypass was needed. Both machines share a
public address, so matching public IP alone was not used as traffic proof:
the route, active lease, member state and traffic counters were also checked.
The SSH management namespace on the mini-PC has no WAN route; WAN checks must
run in its actual host namespace, not that management namespace.

The KVM machines use the existing direct QEMU/systemd-user harness, not libvirt.
No workstation VPN connection was started. No server deployment was changed.

## Artifacts

Console SHA-256 (CGO_ENABLED=0, trimpath):
`812b1457a84e913db5fb2246c3dfae5623473f59159f9af10067dc8e303b933e`.

Mini-PC: `/opt/irvio/bin/irvio`; launcher: `/usr/local/bin/irvio`.
Previous launcher retained as `/usr/local/bin/irvio.before-console-20261019`.
Existing daemon and UI binaries were not replaced: the feature uses their
already-supported Local API. The Ubuntu packaging recipe includes the new
console binary and its dependency notices; a new full tarball was not built.
