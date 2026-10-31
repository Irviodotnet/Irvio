# HYDRA Alpha runtime configuration inventory (baseline)

Baseline: branch `feat/egress-safety-policy-v0`, HEAD `e2046c8a3e29273c2719e30710e3a70a315688ca`, Go `go1.27.1`, current `bin/hydrad` SHA-256 `918cc5b2b3825bee8563f54353c1bb1e4c04e0ace1f484754b14e56ae4532dc5`. The tracked tree was clean before this task. This document is an **inventory and intended source mapping**, not a claim that every source is implemented.

`cmd/irviod/main.go` defined 48 startup flags at the baseline. The legacy engineering flags remain usable by existing netns and forensic suites. Alpha product intent is `hydrad` (desktop) or `hydrad --role server --bootstrap irvio-bootstrap:v1:<bundle>` (server). The implementation adds `--role` on top of this inventory.

| Existing flag | Current purpose | Intended product source | Public CLI? |
| --- | --- | --- | --- |
| `--data-dir` | identity, SQLite, bootstrap store | product default + persistent profile | no |
| `--listen-port` | libp2p TCP listener | product default / OS allocation | no |
| `--bootstrap` | repeatable literal-IP `/p2p` multiaddr | imported persistent profile (desktop); P2.1 bundle (server) | server only |
| `--exit` | Exit capability | product role / desktop Connect intent | no |
| `--country` | signed Exit country | observed public egress + GeoIP; synthetic test override | no |
| `--find-exits` | CLI discovery probe | engineering override only | no |
| `--find-once` | terminate after discovery probe | engineering override only | no |
| `--find-timeout` | discovery probe deadline | engineering override only | no |
| `--dht-mode` | Kademlia mode | product role (`server` or desktop policy) | no |
| `--print-node-info` | machine-readable identity/listener output | engineering override only | no |
| `--print-runtime-events` | lifecycle diagnostics | engineering override only | no |
| `--protected-mode` | fail-closed host policy | Connect intent / existing crash adoption | no |
| `--rotate-awg-key` | one-shot key rotation | engineering/maintenance override | no |
| `--public-node` | M11 public control request | server role; desktop contributor is not a public-control server | no |
| `--public-control-port4` | IPv4 advertised control port | listener/network observation | no |
| `--public-control-port6` | IPv6 advertised control port | listener/network observation | no |
| `--public-proof-ttl` | M11 proof lifetime | product default | no |
| `--public-reverify-lead` | M11 renewal lead | product default | no |
| `--public-retry` | M11 retry | product default | no |
| `--allow-test-public-ip` | private/documentation reachability lab mode | engineering override only | no |
| `--reachability-verifier` | M11 independent verifier peers | verified network discovery / bootstrap seeds; lab override | no |
| `--rendezvous-v1` | brokered M12 client/server path | product default | no |
| `--rendezvous-service` | serve M12 after M11 proof | server role | no |
| `--rendezvous-peer` | static Rendezvous peer | DHT ServiceCID discovery; lab override | no |
| `--geoip-db` | local country MMDB | canonical package path; lab override | no |
| `--rendezvous-descriptor-ttl` | M12 descriptor lifetime | product default | no |
| `--rendezvous-receipt-ttl` | M12 registration lifetime | product default | no |
| `--session-control-port` | inner authenticated control TCP | product default | no |
| `--local-api-socket` | Unix Local API path | product default `/run/irvio/control.sock` | no |
| `--local-api-group` | API socket authorization | package-created `irvio` group | no |
| `--runtime` | production M0–M12 runtime switch | product role | no |
| `--connect` | startup country intent | UI persistent/current intent; lab override | no |
| `--udp-port` | shared HydraSocketMux bind port | product default / OS allocation | no |
| `--stun4` | IPv4 STUN seed | every eligible direct public IPv4 bootstrap peer, on UDP/3478 | no |
| `--stun6` | IPv6 STUN seed | engineering-only in the current IPv4 bootstrap-derived STUN product path | no |
| `--tun-name` | logical tunnel name | Linux product default `hydra0` | no |
| `--exit-wan` | physical Exit WAN link | Linux kernel route lookup | no |
| `--exit-ipv4-pool` | downstream IPv4 pool | conflict-checked persistent selection | no |
| `--exit-ipv6-pool` | downstream IPv6 pool | engineering-only; v1 product pool auto-selection currently covers IPv4 | no |
| `--lease-ttl` | authenticated lease lifetime | product default | no |
| `--lease-sweep` | lease expiry reconciliation | product default | no |
| `--lease-renew-lead` | lease renewal lead | product default | no |
| `--lease-renew-retry` | lease renewal retry | product default | no |
| `--underlay-target` | marked physical-path challenge | bootstrap/network-discovered infrastructure | no |
| `--bypass-prefix` | control/LAN physical bypass | OS connected/control routes; lab override | no |
| `--tunnel-dns` | DNS through tunnel | product default / optional static config | no |
| `--exit-tun-address` | Exit hydra0 address | derived from persistent Exit pool | no |
| `--exit-management-prefix` | Exit-local destination isolation | OS connected/management routes; lab override | no |

## Baseline code boundaries that the implementation had to address

- `productbootstrap.Decode` validates `irvio-bootstrap:v1:` and `Multiaddrs` returns the entire normalized bundle. Product startup expands it before `Manager.New`; legacy multiaddrs remain accepted.
- `runtime.NewDaemon` constructs Exit service at Start. Desktop Connect therefore constructs a dual-role daemon; explicit Disconnect closes the whole daemon, withdrawing M12 registrations, revoking leases and leaving DHT. The idle Local API/bootstrap owner remains alive.
- M11 still requires independent verifiers and the existing two-attestation quorum. Product server startup selects signed public Rendezvous peers first and then direct public IPv4 bootstrap peers as needed, with distinct PeerIDs and IPs. Verifier challenge/attestation still establishes reachability; candidate selection alone is not proof. If fewer than two are available, startup fails rather than weakening M11. Exit advertisement is M11/Rendezvous-gated and withdrawn on public-proof loss; an empty DHT routing table is repaired from the same bootstrap bundle before re-advertisement.
- Local Exit country is resolved from a live STUN-observed public IPv4 endpoint through the existing CountryResolver and DB-IP MMDB at `/var/lib/irvio/geo/country.mmdb`. Missing/unknown country prevents Exit advertisement; no country is taken from the local private source or product CLI.
- Linux FIB lookup resolves underlay WAN; a per-profile randomized, conflict-checked persistent private pool supplies Exit TUN addresses. This avoids the deterministic same-pool assignment on every clean profile but does not guarantee global uniqueness without shared allocation. An ambiguous or absent route is an error.
- Product desktop defaults to `/var/lib/irvio/profile` and `/run/irvio/control.sock`. Profile deletion retains the ownership marker safeguards.

## Resolution precedence target

Product default → strict optional `/etc/irvio/config.json` static role → persistent bootstrap/profile → explicit product CLI role/bootstrap → validated OS/network facts → existing runtime/Core. The current product CLI rejects most low-level explicit flags instead of resolving them as overrides. The separate legacy engineering CLI remains available without `--role`; override precedence inside the new product path is an outstanding limitation.

The existing netns CLI is an engineering interface, not the Alpha product contract. No legacy flag is removed by this inventory.
