# HYDRA Linux GUI field UX (P2.1)

## Implementation contract

P2.1 is a product-layer increment over the completed P2 Linux GUI. HYDRA Core
v1 remains frozen: the GUI still talks only to the local Unix-domain control
API, and the daemon remains the sole owner of discovery, selection, ICE, AWG,
TunnelLease, ProtectedMode, generations, routing, and recovery.

The working baseline is `36557d1a1c528bdf84bd5e7fac4425e0defbc951`.
The historical P2 baseline `ff2e6046b116efcd4a03fda18311a59c78bd5ac9`
remains in its ancestry. The post-P2 public-endpoint and tunnel-DNS field fixes
are retained.

## Change boundary

The central Connect/Disconnect action and its presentation are Flutter-only.
Authoritative connection uptime uses the existing
`ConnectionInfo.connected_since_unix_ms` wire field and a daemon-owned product
projection; it does not add a Core generation or lifecycle.

Logs and bootstrap workflows require an additive Local API minor extension.
API v1.1 retains the v1 Unix socket, `SO_PEERCRED`, four-byte big-endian frame,
protobuf envelope, 64 KiB hard frame limit, bounded clients, and authoritative
`GET_STATUS`. API v1.0 clients continue to use status, Connect, Disconnect, and
status events. A v1.1 client negotiated down to v1.0 keeps those functions and
shows logs/bootstrap/share as unavailable.

The only planned frozen-Core change is a separately tested correction to the
existing bootstrap connector. A non-empty set succeeds when at least one
unique expected PeerID is reached and authenticated, rather than requiring
every configured peer to be live. Attempts have bounded concurrency,
per-peer timeout, and overall deadline. An empty set remains a valid isolated
startup. No Kademlia or discovery semantics change.

## Local API v1.1 surface

The additive methods are:

- `GET_RECENT_LOGS`
- `SUBSCRIBE_LOGS`
- `GET_BOOTSTRAP_STATE`
- `IMPORT_BOOTSTRAP_BUNDLE`
- `GENERATE_BOOTSTRAP_BUNDLE`

Bootstrap state is fetched after import and after each GUI reconnect; it does
not require a push event. Logs use a second local API connection so log volume
or a slow log view cannot delay status events. Log tail responses are bounded
both by record count and serialized byte budget. Subscription starts after an
explicit process-local cursor and reports a gap if that cursor has fallen out
of the ring. A random daemon incarnation distinguishes sequence reuse after a
daemon restart.

The daemon attaches a bounded product-facing handler to its existing
structured `slog` logger. It exports only timestamp, severity, a bounded and
sanitized message, and allowlisted scalar fields. Sanitization applies to
message and error text as well as attributes. Keys, passwords, ICE
credentials, nonces, signatures, payloads, and arbitrary binary data are never
part of the product log stream. Slow subscribers are disconnected; Core never
waits for a GUI.

## Logical connection uptime

A newly accepted logical Connect intent arms a fresh uptime lifecycle. The
timestamp is set on its first successful `CONNECTED` promotion. It survives
same-peer recovery, Exit failover/reselection, network migration, transport
replacement, and lease renewal. It resets only on explicit Disconnect or a
new non-idempotent Connect lifecycle. Before the first successful connection
the value is absent. Flutter formats the daemon timestamp as `HH:MM:SS` and may
tick locally between snapshots.

## BootstrapBundle v1

The product format is independent of the Local API version:

```text
irvio-bootstrap:v1:<base64url protobuf>
```

The bounded payload contains a format version, informational generation time,
and literal-IP TCP endpoints grouped by expected libp2p PeerID. It contains no
DNS name and no hard expiry. Duplicate PeerIDs and endpoints are collapsed.
The persisted daemon-owned store holds at most 32 peer records and is replaced
with a crash-safe temp/write/fsync/rename sequence under the daemon data
directory. Corruption degrades bootstrap state but does not prevent the daemon
or Local API from starting; a valid import replaces the damaged store.

The bundle is an untrusted locator hint, not a signed authority. Its security
boundary is the authenticated libp2p dial: the remote identity must equal the
expected PeerID from the bundle. A bundle containing dead locators remains
useful when any one expected peer is live.

At startup, persisted and command-line records are merged and deduplicated.
At runtime, import validates and persists the bundle, performs bounded
authenticated dials, and makes the resulting connection available to the
existing DHT without restarting `hydrad`. Import does not change CountryIntent,
the active Exit, tunnel ownership, generations, or routing.

Product bootstrap state is deliberately small: `EMPTY`, `CONNECTING`, `READY`,
`DEGRADED`, or `FAILED`, plus configured and currently reachable counts and a
stable product error code. It exposes no Kademlia buckets, routes, peer
database, or generation values.

## Share Hydra

`GENERATE_BOOTSTRAP_BUNDLE` is a bounded, side-effect-free product discovery
operation. It considers known-good/configured introducers, current
authenticated public peers, and finally bounded existing DHT Rendezvous
discovery. It stops at three to five usable nodes and returns a partial bundle
when only one or two exist.

Each exported node must have an authenticated expected PeerID, a literal
global-unicast public control endpoint, a suitable DHT/public-control
capability, a valid current M11 reachability proof where required, and a fresh
bounded authenticated check. Arbitrary Exits and merely connected peers are
not eligible. The v1.1 selector deliberately excludes the local node; it
shares only third-party nodes that passed a fresh authenticated probe. A
future self-advertisement path may include the local node only after applying
the same verified public-bootstrap predicate.

The generated payload contains no private identity, AWG material, ICE
credentials, TunnelLease, Exit list, application traffic, or destination
history. The exact returned text is used both by Copy and QR generation.
Image import extracts that text from PNG or JPEG and sends it to the same
daemon parser as pasted text. QR is an introduction mechanism only; normal DHT
discovery continues after the first authenticated connection.

## GUI behavior

The sidebar order is Home, Settings, Diagnostics, Logs, Share Hydra, About.
The central circle is the only connection action: it Connects from an idle
intent, Disconnects/cancels while connecting, connected, or recovering, and is
disabled while disconnecting. The GUI never implements failover.

With no bootstrap configuration, Home presents Join Hydra with paste, QR-file
import, and an advanced PeerID plus literal IP/port form. Logs provide bounded
severity filtering, auto-scroll, pause, copy, and local-only Clear. Pause never
creates daemon backlog; resume continues from a valid cursor or obtains a new
bounded tail and displays any gap.

Daemon loss leaves the GUI alive. Reconnect creates exactly one status and one
logs connection, negotiates HELLO, refreshes status/bootstrap/log tail, and
resubscribes. Closing the GUI never invokes Disconnect and never changes the
VPN.

## Settings profile deletion

Settings exposes a dedicated danger-zone `Delete profile` action when the
negotiated daemon supports Local API v1.2. A v1.1 daemon renders it disabled
rather than attempting a fictitious local deletion. Confirmation warns that
the active VPN is disconnected and the persistent PeerID, AWG identity,
bootstrap store, and peer database are permanently removed.

After the daemon acknowledges the operation, the GUI treats the Local API
disconnect as an expected restart, stays alive, and reconnects through the
normal HELLO/GET_STATUS/bootstrap/log synchronization path. The resulting
empty bootstrap state returns the user to onboarding. Flutter never accesses
the data directory or service manager directly.

## Verification boundary

Unit, race, Flutter, and non-privileged regression tests may run from the
source tree. Production `hydrad`, namespace faults, diagnostics, QR A-to-B
bootstrap, Internet, leak, and host-integrity proofs run only on the dedicated
mini-PC/isolated machines. A failed expected-success production cycle keeps
P2.1 incomplete and is classified before any timeout or policy change.

P2.1 does not add contribution semantics, bandwidth controls, traffic
accounting, GeoIP delivery, installers, updaters, additional platforms, TURN,
Sybil resistance, or a new distributed trust layer.
