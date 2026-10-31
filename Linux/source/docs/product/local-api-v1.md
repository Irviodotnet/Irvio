# HYDRA Local Control API v1

The HYDRA Local Control API is a product-facing adapter over the frozen HYDRA
Core v1 runtime. It accepts local user intent and returns a deliberately small
status model. It does not own VPN state, select an Exit, perform recovery,
configure AWG, manage a TunnelLease, or change Linux routing and firewall
state.

## Transport and ownership

Linux uses a Unix domain stream socket. The production service path is:

```text
/run/irvio/control.sock
```

`hydrad` enables the endpoint with
`--local-api-socket /run/irvio/control.sock`. Keeping the flag explicit avoids
collisions between the many independent `hydrad` processes used by the
network-namespace laboratory. The flag requires `--runtime` or `--connect`.
A configured Local API starts only after the production daemon has initialized.
Failure to create the socket is logged as
`LOCAL_API_START_FAILED`; it does not stop or disconnect an existing Core
runtime.

For a root service, `--local-api-group hydra` selects the service group. A
newly created directory is owned by `root:hydra` with mode `0750`, and the
socket is `root:hydra` with mode `0660`. Existing directories must already
have the expected ownership and no permissions for other users. Development
and tests may use an absolute private temporary path owned by the current
user.

The server authorizes every accepted socket with Linux `SO_PEERCRED`. The
effective UID, effective GID, or a kernel-reported supplementary group must
match the configured policy. Supplementary membership is read from the
credential PID's `/proc` status and accepted only when its UID still matches
the socket credential. No protobuf field is
accepted as an identity or authorization claim. The protocol uses no TLS,
Hydra PeerID, or Noise because filesystem permissions and kernel peer
credentials are its local security boundary.

At most eight clients are accepted by default. Each client has bounded frame,
request, idle, read, and write deadlines. The per-subscriber outbound queue is
64 events by default. A client that cannot consume its queue is disconnected;
Core never waits for a GUI.

## Framing and versioning

The schema is `proto/irvio/local/v1/control.proto`. Each message is framed as:

```text
4-byte unsigned big-endian length
protobuf Envelope
```

The maximum frame is 64 KiB, checked before allocation. Zero-length,
oversized, truncated, and malformed frames are rejected. There is no
compression.

The current Local API version is `1.4`. The first request must be `HELLO`. A
different major version returns `UNSUPPORTED_API` and closes the connection. Within
major 1, the negotiated minor is the lower of the client and server minor;
new fields and methods must remain additive. Local API versions are unrelated
to Rendezvous, ICE signaling, session-control, or AWG key epochs.

`HELLO` returns the server API version, negotiated minor, product version, and
`core_version = "v1"`. Request IDs are nonzero and only correlate requests
with responses. Events use request ID zero.

The v1.4 daemon remains compatible with v1.0–v1.3 GUIs. A v1.4 GUI
negotiated down to an older minor continues to use the methods available in
that minor and disables newer controls. Logs/bootstrap/share require v1.1;
profile deletion requires v1.2; dynamic available countries require v1.3;
daemon-owned Shutdown requires v1.4.

## Commands

The v1 surface supports these method identifiers:

| Method | Behavior |
|---|---|
| `HELLO` | Negotiate Local API version; must be first. |
| `GET_STATUS` | Return the complete current product snapshot. |
| `CONNECT` | Validate/normalize an alpha-2 country and submit it to the existing runtime. |
| `DISCONNECT` | Submit the existing runtime disconnect intent. |
| `GET_CONTRIBUTION` | Returns `UNSUPPORTED` in Core v1. |
| `SET_CONTRIBUTION` | Returns `UNSUPPORTED` in Core v1. |
| `SUBSCRIBE_EVENTS` | Subscribe the connection to bounded full-status events. |
| `GET_RECENT_LOGS` | v1.1: return a count- and byte-bounded sanitized daemon log tail. |
| `SUBSCRIBE_LOGS` | v1.1: follow product log records after an incarnation/sequence cursor. |
| `GET_BOOTSTRAP_STATE` | v1.1: return daemon-owned onboarding state and bounded counters. |
| `IMPORT_BOOTSTRAP_BUNDLE` | v1.1: validate, persist, and hot-join from an introduction bundle. |
| `GENERATE_BOOTSTRAP_BUNDLE` | v1.1: return a bounded Share Hydra bundle of eligible public introducers. |
| `DELETE_PROFILE` | v1.2: disconnect, delete the current daemon-owned profile, and restart hydrad. |
| `GET_AVAILABLE_COUNTRIES` | v1.3: return the cached or explicitly refreshed bounded union of live country presence. |
| `SHUTDOWN` | v1.4: complete safe Disconnect/Exit teardown, acknowledge, then exit the daemon normally. |

An `OK` response with `accepted = true` means only that Core accepted the
command. `CONNECT` does not wait for discovery, ICE, AWG, or Internet. Repeated
`CONNECT` for the current country and repeated `DISCONNECT` are idempotent at
the adapter boundary and do not introduce a Local API generation. A different
country while Core is busy is passed to the current runtime and currently
returns `BUSY`; P1 does not invent a country-switch algorithm.

Core v1 has no unified product-level contribution intent. The two contribution
method IDs are reserved so a later product layer can add the capability
without replacing the protocol, but P1 truthfully returns `UNSUPPORTED` and
does not synthesize an Exit/DHT/public/Rendezvous state machine.

## Product status

`GET_STATUS` is the source of truth. The stable state mapping is centralized
in `internal/localapi/mapper.go`:

| Core runtime state | Product state |
|---|---|
| stopped, idle, normally closed | `DISCONNECTED` |
| starting, protecting, discovering, connecting | `CONNECTING` |
| connected, lease renewal | `CONNECTED` |
| same-peer recovery, network migration, Exit reselection | `RECOVERING` |
| no Exit and both underlay families unreachable | `NO_NETWORK` |
| no Exit with any non-definitively-unreachable underlay | `NO_EXIT` |
| disconnecting, stopping | `DISCONNECTING` |
| failed close or unknown state | `ERROR` |

Status exposes country intent, contribution state, and a small connection
summary. `connected_since_unix_ms` is owned by the daemon product adapter: it
is set on the first `CONNECTED` promotion of a logical Connect lifecycle,
survives recovery/reselection/migration, and resets on Disconnect or a new
lifecycle. Latency remains absent unless Core has actually observed it.

The stable contract never returns Exit/Rendezvous PeerIDs, session or lease
IDs, ICE secrets or candidates, AWG keys, generation counters, selected-pair
internals, nftables marks, or route table numbers.

## Events and reconnect

`STATUS_CHANGED` carries a complete new `Status`, not a patch. Its sequence is
monotonic only for the current daemon process and may restart after daemon
restart. The adapter samples the single authoritative runtime snapshot and
emits only product-visible changes; it neither consumes nor competes with the
Core diagnostic event stream.

Closing or crashing a GUI only closes that Unix connection. It does not call
`Disconnect` and cannot release the lease, close AWG, remove routes, disable
ProtectedMode, or stop contribution roles. After reconnect, a GUI must call
`GET_STATUS` and use that full snapshot before relying on later events.

## Product logs

The daemon fans its existing structured `slog` records into a 512-record
product ring. It does not create another logging authority. One exported
message is at most 512 bytes and contains only timestamp, severity, sanitized
message text, and up to 12 allowlisted scalar fields. Sensitive key names and
text fragments for private keys, passwords, ICE credentials, nonces,
signatures, tokens, envelopes, secrets, and raw payloads are redacted or
discarded server-side.

`GET_RECENT_LOGS` returns at most 200 records and stops before a 48 KiB
serialized `LogTail` budget, leaving headroom below the 64 KiB frame limit.
`SUBSCRIBE_LOGS` atomically installs a separate bounded subscription after a
process-local daemon incarnation and sequence cursor. A missing ring segment
is reported with `gap=true`; daemon restart changes the random incarnation.
The subscriber queue is 64 records. A slow subscriber is closed without
blocking logging, status events, or Core. GUI Pause closes this stream and
later obtains a fresh bounded tail; Clear view never deletes daemon logs.

## Bootstrap onboarding and Share Hydra

`BootstrapBundle` format version 1 is encoded as:

```text
irvio-bootstrap:v1:<base64url protobuf>
```

It holds an informational generation time and literal-IP TCP endpoints grouped
by expected libp2p PeerID. It is an untrusted locator hint, not a signed
authority: the authenticated libp2p connection must still produce exactly the
expected PeerID. DNS names, private material, AWG data, ICE credentials,
leases, Exit lists, and traffic metadata are absent.

The daemon merges CLI and persisted records, deduplicates by PeerID, and keeps
at most 32 records with at most four endpoints per peer. The daemon-owned
`bootstrap-v1.pb` is written under its data directory using a private
temporary file, `fsync`, atomic rename, directory `fsync`, and mode `0600`.
Malformed stored data yields `DEGRADED` while hydrad and the Local API remain
available for replacement import. Import performs a bounded authenticated hot
join and never changes the active VPN lifecycle.

Share returns one to five currently usable public introducers (normally four).
Each record is selected only after signed-announcement validation, a current
M11 public-control proof, discovery capability, literal public endpoint,
support for the Hydra Kademlia protocol, and a bounded authenticated probe.
Generation is read/discovery-only and cannot change CountryIntent, selector
slots, leases, generations, routing, or user traffic.

## Available countries

`GET_AVAILABLE_COUNTRIES` returns only sorted, deduplicated ISO 3166-1
alpha-2 codes and a product status: `READY`, `PARTIAL`, `EMPTY`, or
`UNAVAILABLE`. `EMPTY` means at least one current Rendezvous answered with an
empty local set; `UNAVAILABLE` means none produced a valid response. A partial
result remains usable.

The daemon queries at most ten usable Rendezvous peers, with at most five
requests in flight, per-peer and overall deadlines, and a 45-second product
cache. Each Rendezvous returns only its own local country set; the operation
never recursively queries another server. No PeerID, endpoint, registration,
count, or Exit record crosses the Local API boundary.

This list is discovery metadata, not a VPN lifecycle input. It cannot change
CountryIntent, disconnect a healthy tunnel, trigger failover, or alter
ACTIVE/RESERVE/COLD selection. A v1.3 GUI connected to an older daemon keeps
the older supported controls and reports locations as unavailable instead of
falling back to a static selectable list.

## Profile deletion

`DELETE_PROFILE` is a destructive product operation and requires an explicit
`confirm=true` request on a negotiated v1.2 connection. The GUI presents a
separate confirmation dialog. Flutter never reads or removes daemon files.

The daemon first completes the existing authoritative Disconnect path. It
writes the accepted response before committing deletion, closes the Local API,
bootstrap manager, and Core runtime, and refuses deletion if shutdown fails.
The data directory is claimed at startup with a private ownership marker and
device/inode fence. Deletion revalidates both and removes only entries inside
that exact directory, preserving the service-managed directory itself. Legacy
profiles are claimed only when their existing entries match the bounded Hydra
profile layout. Any unrecognized entry makes deletion fail closed and is left
untouched.

The daemon then self-executes the same hydrad binary and arguments; no systemd
restart policy is required. The new process creates fresh Hydra/AWG identities,
an empty database, and empty bootstrap state. The operation is irreversible and
does not retain the old PeerID.

## Errors

Stable decisions use the enum:

```text
OK
INVALID_ARGUMENT
UNSUPPORTED_API
UNAUTHORIZED
BUSY
NOT_READY
UNSUPPORTED
INTERNAL
```

`user_message` is displayable diagnostic text, not a machine-stable value.
Raw Go errors are never part of the wire contract.

## Verification

Focused tests:

```bash
go test ./internal/localapi/...
go test ./internal/productbootstrap/... ./internal/productprofile/...
go test -race ./internal/localapi/...
```

Full process-level proof uses the existing isolated M12 topology:

```bash
make test-p1
make test-p1-netns
make test-p1-soak
make demo-p1
```

The privileged runner starts the prebuilt normal-user `hydrad` binary. It
proves Local API Connect/Disconnect reaches the production M12 runtime,
Internet resumes through the selected Exit, closing/reopening the local client
does not change the session, Core failover is merely observed as
`RECOVERING`, ProtectedMode leakage remains zero, a stale crash socket is
safely adopted, and host state is restored.
