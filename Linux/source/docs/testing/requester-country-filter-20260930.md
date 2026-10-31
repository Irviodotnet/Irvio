# Requester-aware available countries — 2026-09-30

Baseline: `main`, `124a8a1db72ccf4d2ff7ff48c24d15799419cc47` plus the current
uncommitted working-tree fixes. This is not a tagged or committed release.

## Fix

The available-country RPC previously returned the global Rendezvous country
index. A sole Exit therefore saw its own country even though candidate selection
already excludes the local PeerID.

`Registry.CountriesExcluding` reads the existing fresh/accepting index and omits
a country only when no indexed PeerID other than the requester remains.
The RPC uses the authenticated libp2p remote identity; the local runtime source
uses the node's identity. `Registry.Countries()` remains global for DHT country
advertisements. No protobuf, wire format, candidate acquisition, ExitSelector,
ICE, AWG or TunnelLease semantics change. Filtering is by identity, never by IP.

## Regression

The real-libp2p regression failed before the fix with
`sole Exit sees its own country: [NL]`, then passed after the fix. It verifies:

- an unrelated requester still sees NL;
- a sole registered Exit does not see its own NL;
- the global index still contains NL;
- a second identity on the same observed IP restores NL for the first Exit;
- an alternative no longer accepting sessions does not make NL available.

Passed: `gofmt`, `git diff --check`, `go vet ./...`, `go test ./...`, affected
Rendezvous/runtime/Local API/ExitSelector tests with `-race -shuffle=on` and with
`-shuffle=on -count=10`. Normal and CGO-disabled trimpath builds passed.
UI: 76 tests passed; `flutter analyze --no-pub` from `ui/` passed; isolated Linux
release build passed. An initial analyzer invocation at the repository root
incorrectly included historical lab UI copies; it is not the UI-package gate.

## Live deployment and proof

Updated Amsterdam, Helsinki, Prague and New York sequentially without changing
identities, bootstrap bundles or role arguments. New York SSH used an alternate
management route because direct SSH timed out. All four restarted, passed public
reachability verification, and restored three Rendezvous registrations.

The mini-PC runs the same new daemon and rebuilt UI, retaining its profile.
A real connection selected NL and an HTTPS IPv4 egress probe returned the
Amsterdam server IP. With the mini-PC contributing its sole RU Exit:

- its own API returned `CZ, DE, NL, US`, without RU;
- a temporary independent libp2p identity on the same mini-PC obtained RU in
  responses from Helsinki and Prague;
- thus RU remains globally available to another requester, not to itself.

The independent probe to the active Amsterdam Exit timed out while connected;
this probe is not the daemon's authenticated control path. The normal daemon
metadata query succeeded. No broad claim of every independent probe passing is
made. Existing GTK/appindicator startup warnings were observed, without UI death.
The mini-PC is returned to DISCONNECTED with its updated UI open after testing.
The workstation daemon/UI were not started or connected by this task.

## Build fingerprints

- Trimpath daemon: `608a04c47491094cf1957a1cdf88a51fef81634860dd04ca5ccabe1678ad583e`
- Normal daemon: `1dba0e92bd7a0bcf88d53617a2f0923964de3494ae81c96501ed9b6aad2f1725`
- UI `libapp.so`: `ed9b29a4643df9bf12ac0fda4ee7e2cab8034e75cc4c1a60b53b4c62e5fa9858`
- Ubuntu tarball: `570b3d65b39fc937d81c067e1ea11bc01e358f3e401431210d02b74c3ea600c3`

The installable tarball contains the latest tracked WAN/UI fixes as well as this
country-metadata fix. Its explicit-allowlist archive audit passed (288 files,
complete checksum inventory, no installed identities, profiles or user logs).
The separate source archive contains current tracked source contents plus these
validation reports, not `.git`, private files or living-network lab artifacts.

Limitation: a country indicates at least one fresh accepting different identity,
not a guarantee of NAT compatibility or successful subsequent connection.
