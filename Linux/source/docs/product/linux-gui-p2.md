# HYDRA Linux GUI MVP (P2)

## Boundary

P2 is a Flutter desktop client for the frozen HYDRA Core v1. It does not own
VPN state and does not call networking packages. All daemon interaction goes
through `HydraClient`; the Linux implementation connects only to the
configurable Unix domain socket (default `/run/irvio/control.sock`) and speaks
HYDRA Local API v1.0.

The wire implementation uses the checked-in Dart code generated from
`proto/irvio/local/v1/control.proto`. Each protobuf envelope is prefixed by a
four-byte unsigned big-endian length. Empty frames and frames above 64 KiB are
rejected before payload allocation. The client allows at most 16 outstanding
requests and serializes writes to the socket.

Widgets depend on the platform-neutral `HydraClient` interface. They never
open Unix sockets and never access Kademlia, Rendezvous, ICE, AWG, leases,
routes, nftables, netlink, SQLite, or Core generations.

## Startup and synchronization

The application follows this order:

1. Connect to the Unix socket and complete `HELLO` for API major version 1.
2. Call `GET_STATUS` and render the authoritative product snapshot.
3. Query contribution support.
4. Call `SUBSCRIBE_EVENTS` for responsive full-status updates.

If the daemon is absent or the stream closes, the window remains usable and
reconnects with bounded backoff. Every successful reconnect repeats
`GET_STATUS`; event sequence numbers are not treated as persistent history.
Starting the GUI while a tunnel is active therefore restores `CONNECTED`
without changing the Core session.

The current on-demand product behavior supersedes this P2 window-lifecycle
description: the close button and tray Shutdown action request Local API v1.4
`SHUTDOWN` after confirmation. The daemon completes teardown before the UI
exits. An unexpected GUI crash still leaves an active tunnel daemon-owned.
See [on-demand desktop lifecycle](on-demand-desktop-lifecycle.md).

## Product surface

The UI maps only the Local API product states:

| Local API state | Presentation |
| --- | --- |
| `DISCONNECTED` | Blue Connect action |
| `CONNECTING` | Blue progress, Connecting |
| `CONNECTED` | Green confirmation and Disconnect |
| `RECOVERING` | Amber progress, Reconnecting |
| `NO_NETWORK` | Local network unavailable |
| `NO_EXIT` | No peers available for the selected country |
| `DISCONNECTING` | Teardown progress |
| `ERROR` | Red, safe API-provided user message |

`CONNECT(country)` and `DISCONNECT` return command acceptance only. The GUI
does not predict completion or run recovery. Repeated button activation is
bounded in the UI, while command idempotence remains a daemon property.

The country selector uses a small product-layer catalog because API v1.0 has
no country-list method. Selection while disconnected is local intent; status
from the daemon is authoritative once a lifecycle starts.

API v1.0 reports contribution as unsupported. The `Help Hydra network` card is
therefore visible but disabled and labelled `Coming later`; P2 never stores or
sends a pretend contribution value. Start-at-login and notifications are also
shown as future GUI integration points, not working toggles.

P2 does not fabricate bandwidth, NAT type, public-node capability, latency, or
connection uptime. Optional latency/uptime are displayed only when the daemon
actually includes them. Diagnostics export, log-folder opening, and system
information actions are omitted because Local API v1.0 exposes no safe data
source for them.

## Linux desktop behavior

The production window is approximately 492 by 620 logical pixels with a
custom title bar, navigation for Home, Settings, Diagnostics, and About, and a
dark theme by default. System, dark, and light themes are GUI-local settings.
The selected country and theme are stored through Flutter's preferences
plugin; no VPN state is persisted by the GUI.

The optional AppIndicator tray exposes status, Open Hydra, Disconnect VPN,
Settings, About, and (in the original P2 design) the explicitly labelled
`Quit UI · VPN keeps running`. The current on-demand product replaces this
entry with `Shutdown Hydra`; see the lifecycle document above.
On desktops without a tray host, the main window remains functional and its
close action exits only the UI.

## Build and test

Flutter's Linux desktop prerequisites and a current stable Flutter SDK are
required. From the repository root:

```bash
make FLUTTER=/path/to/flutter DART=/path/to/dart test-p2
```

Run directly against the production socket:

```bash
cd ui
flutter run -d linux
```

For a test socket:

```bash
HYDRA_CONTROL_SOCKET=/tmp/hydra/control.sock flutter run -d linux
```

`make test-p2-netns` builds a headless Dart client from the same
`LinuxHydraClient` implementation and runs it inside the existing privileged
M12 topology. It proves that a Dart Local API disconnect/connect cycle reaches
the production `hydrad` runtime and restores independently observed Internet
traffic. Only prebuilt helpers run with the repository's existing privileged
test discipline.

Widget tests use a fake `HydraClient` and cover product rendering, command
delegation, authoritative resynchronization, daemon loss, unsupported
contribution, GUI close/restart, and recovery states. Transport tests use a
real Unix socket fixture with the exact generated protobuf contract and cover
partial, malformed, truncated, and oversized framing.

## Deferred work

P2 does not add contribution bandwidth controls, client limits, a dynamic
country catalog, bootstrap sharing, updater behavior, remote APIs, mobile or
other desktop platforms, or any Core networking behavior. Those require a
future product/API increment rather than inference from Core internals.

P2.1 is the additive field-UX successor. It retains this frozen boundary while
adding daemon-backed uptime, product logs, bootstrap onboarding, and Share
Hydra through Local API v1.1. See
[`linux-gui-p2.1.md`](linux-gui-p2.1.md).
