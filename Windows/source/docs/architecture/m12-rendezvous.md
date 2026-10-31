# M12 Rendezvous v1

M12 adds a bounded, replaceable Rendezvous control plane to the production
`hydrad` runtime. Rendezvous is an introducer and signaling carrier. It is not
an overlay data path, a global membership database, or an authority over an
already promoted session.

The production path is:

```text
literal-IP bootstrap -> Kademlia service hint -> verified Rendezvous
  -> country sample -> M9 ExitSelector -> signed session establishment
  -> direct ICE/AWG -> in-tunnel session control
```

Application packets and active-session maintenance never traverse Rendezvous.
All production control endpoints are literal IP multiaddrs or `AddrPort`
values. The runtime has no telemetry dependency, and no DNS lookup is part of
bootstrap, public verification, registration, sampling, signaling, ICE, or
session maintenance. User DNS remains ordinary encrypted M7 Data Plane
traffic after the tunnel is connected.

## Runtime ownership

The existing serialized `runtime.Daemon` owns M12. It creates one Rendezvous
client and discovery provider, an optional proof-gated service, an optional
Exit registration manager, one M9 adapter, and the signed connector. Closing
the daemon fences selection and signaling first, closes registration streams,
removes the service handler, and then closes the older M0-M11 children in their
existing order. No second daemon or libp2p host is introduced.

A Rendezvous service starts only when all of the following are true:

- operator intent enables the service;
- M11 currently reports a verified public-control capability;
- the verified endpoint is present in the signed announcement;
- the local service can activate its signed `CAP_RENDEZVOUS` announcement.

Loss of that proof stops new service admission and advertisement. It does not
close healthy VPN sessions. `CAP_EXIT` remains independent, so a NATed node may
register as an Exit without being a public Rendezvous.

## Protocol and frames

The Hydra-specific protocol is `/hydra/rendezvous/1.0.0`. Every protobuf frame
has a four-byte big-endian length prefix. Length is checked before allocation;
the total frame limit is 64 KiB. The message families are:

```text
HELLO
REGISTER / REGISTERED / UPDATE
HEARTBEAT / HEARTBEAT_ACK
GET_CANDIDATES / CANDIDATES
SAMPLE_LOCAL / SAMPLE_RESULT
SIGNAL
UNREGISTER
ERROR
```

`SIGNAL` carries only signed PREPARE, READY, DECLINE, OFFER, ANSWER, or CANCEL.
It cannot name a callback IP, proxy arbitrary bytes, or create an arbitrary
outbound destination. Error text is capped at 128 bytes.

Implemented hard limits are:

| Resource | Limit |
|---|---:|
| Rendezvous frame | 64 KiB |
| preferred new descriptor | 8 KiB |
| compatible verified M11 signed announcement | 32 KiB payload / 64 KiB envelope |
| signed receipt | 2 KiB |
| candidates returned | 10 and frame budget |
| owner routes or receipts per candidate | 3 |
| excluded PeerIDs | 32 |
| control addresses per route | 4 / 1024 bytes total |
| signaling messages / bytes per attempt | 16 / 64 KiB |
| registrations | 10,000 |
| pending signaling contexts | 256 |
| outgoing SAMPLE operations | 32 |
| concurrent DHT lookups | 8 |
| client acquisition GET concurrency | 2 |

The M11 announcement compatibility ceiling is intentionally retained. When
ten records do not fit one frame, the server returns a smaller partial batch.

## Descriptor truth and durable ordering

`NodeAnnouncement` is the only signed Exit descriptor. Rendezvous forwards the
original signed envelope unchanged and derives:

```text
descriptor_id = SHA-256(exact authenticated RawPayload bytes)
version       = (incarnation, sequence)
```

Hydra signs the source payload once. Verification uses those original bytes;
protobuf reserialization is never used as a signature oracle. Local publisher
startup exclusively increments and persists `incarnation` before publication.
`sequence` increases for a material change or descriptor renewal inside that
incarnation. A heartbeat does not change descriptor version.

Versions are compared only for one PeerID. Lower lexicographic versions are
stale, identical version and bytes is an idempotent replay, and identical
version with different bytes is a conflict. AWG key epoch rollback protection
is additionally retained from M10. NetworkGeneration and timestamps do not
replace durable descriptor ordering.

Rolling a descriptor or allowing discovery metadata to expire never tears
down a healthy lease. Restoring an old disk snapshot or cloning the same Hydra
identity cannot be made rollback-proof without an external monotonic source;
M12 does not claim otherwise.

The production runtime binds the signed `NodeAnnouncement` lifetime to the
same M12 descriptor policy used by registries. `--rendezvous-descriptor-ttl`
defaults to ten minutes and also drives announcement refresh at half that
lifetime; `--rendezvous-receipt-ttl` defaults to ten minutes and its renewal
lead is half the configured lifetime. Both flags are bounded to one second
through ten minutes, and receipt lifetime cannot exceed descriptor lifetime.
Short values exist for deterministic lifecycle testing; they do not change
the signed schema or version comparison rules.

## Registration ownership and freshness

An Exit opens authenticated outbound libp2p streams to up to three distinct
verified Rendezvous PeerIDs. One live record per `(server, Exit PeerID)` is
owned by:

```text
Exit PeerID + server incarnation + registration ID + owning stream token
```

Replacement is atomic. A heartbeat, unregister, or close callback for an old
tuple cannot mutate its replacement. Closing the owning stream immediately
removes the record from fresh sampling. Server restart creates a new random
server incarnation and an empty RAM registry; persisted data is never restored
as online presence.

Default accepted-heartbeat ages are `FRESH < 90s`, `STALE 90s..120s`, and
expired after 120s. Heartbeats are sent every 30s with 20 percent jitter and a
strictly increasing sequence. Receipts/descriptors are valid for at most ten
minutes. With the default policy the server re-signs a receipt when its
remaining validity reaches the five-minute renewal lead and returns it in that
heartbeat ACK; ordinary heartbeats carry no new signature. The client
authenticates the renewed server, owner tuple, descriptor and receipt sequence
before replacing its cached receipt. Repair preserves healthy registrations
and repairs only missing ones:

| Active registrations | Repair delay |
|---:|---:|
| 2 | random 5-120s |
| 1 | random 1-15s |
| 0 | full jitter, initially 0-2s, exponential cap 60s |
| 3 | 5 minutes before re-evaluation |

One successful registration is useful; startup never waits for an impossible
three-server quorum. Each reconciliation randomizes the bounded working set,
uses at most six dial attempts, and shares one operation deadline across
discovery and all dials. Backoff counts failed reconciliation rounds rather
than multiplying once per dead hint. Planned shutdown performs bounded
best-effort unregister.

Significant signed-announcement changes, including a higher AWG key epoch,
wake the registration manager immediately. UPDATE remains subject to normal
admission limits and is not fire-and-forget: the registration is current only
after the owner returns a signed receipt for the exact descriptor ID/version.
Rate-limited or timed-out updates leave the manager's desired descriptor dirty
so a later bounded reconciliation retries it. Heartbeats do not create new
descriptor versions.

## Receipts and candidates

A `RegistrationReceipt` is signed by its observing server and binds server
identity/incarnation, subject, registration ID, receipt sequence, descriptor ID
and version, observed registration IP/family, country observation, issue/expiry,
and the server public-proof reference. Receipt sequence is local to one
`(server incarnation, registration ID)`.

A brokered candidate contains the original signed announcement, at least one
valid receipt, and up to three verified owner routes. Every advertised owner
route must have its own matching receipt; one valid route cannot authorize
unreceipted alternatives. Dedupe is by Exit PeerID.
The newest non-conflicting descriptor wins; receipts for another descriptor
remain only locator history and are not relabeled as freshness for the new
payload. Foreign caches preserve source timestamps and never extend expiry.

A still-valid older descriptor may locate the authenticated Exit long enough
to start PREPARE; it does not authorize an old AWG binding. The responder
returns its current signed descriptor in READY. The initiator requires that
descriptor to be the same or a strictly newer version for the same PeerID,
checks the exact attempt/NetworkGeneration transcript, and commits the newer
binding to rollback protection before AWG promotion. A future or conflicting
locator is rejected rather than silently renamed.

## GeoIP country provenance

The production `CountryResolver` uses an operator-installed MaxMind-compatible
MMDB through pinned `maxminddb-golang/v2`. The database is local: Connect does
not download it, call an online GeoIP API, or resolve DNS. The lookup key is the
source IP observed by the direct authenticated registration connection.
Forwarded client IP and self-declared country are not observation inputs.

The receipt records country, IP, family, database type/build time, and
observation time. `country.iso_code` is used; `registered_country` and
`represented_country` are decoded but do not replace it. Known results enter
the exact OBSERVED pool. Missing records are UNKNOWN. Claimed metadata never
silently becomes OBSERVED, and conflicting IPv4/IPv6 or owner observations do
not satisfy exact `CountryIntent` in v1.

MMDB replacement opens and validates the next snapshot before atomically
swapping readers. A failed reload leaves the previous reader usable while
reporting degraded status. CI creates a redistributable synthetic MMDB fixture
and exercises this real reader. `REGISTRATION_IP_GEOIP` is not proof of the
country of every IPv4/IPv6 Internet egress flow.

## Kademlia and working set

The service CIDs are deterministic hashes of:

```text
hydra/v1/rendezvous
hydra/v1/rendezvous/<COUNTRY>
```

A country key means that the server currently has a local observed pool for
that country; it says nothing about the server's own location. DHT provider
records are hints. Hydra authenticates the peer, verifies its M11 public proof
and current announcement, and performs a real protocol request before trust.
The pinned kad-dht provider validity is 48h, its subsystem default reprovide is
22h, and Hydra's existing discovery owner republishes its own service records
every 12h. Hydra stops local reprovide when a pool disappears but cannot
promise immediate deletion of distributed stale hints.

The client keeps one sticky PRIMARY, up to two FALLBACK peers, and at most 32
application-level cached servers. Explicit configured peers are tried as the
sticky set; bounded DHT discovery is fallback. Recovery is PRIMARY, FALLBACK,
cache, then coalesced DHT. A provider outage never replaces healthy ACTIVE.
Once a verified cache exists, callers receive it immediately; a due DHT refresh
runs in the background with 24-36 minute jitter and coalescing. The first cold
lookup remains synchronously bounded to five seconds. Empty results use a
5-15 second negative cache.
The legacy `hydra/v1/exit/<country>` discovery path remains compatibility-only
and is not used as a liveness database.

## Distributed sampling and acquisition

`GET_CANDIDATES` samples the local country index and may ask at most three
remote verified Rendezvous owners. `SAMPLE_LOCAL` is a separate authorized
handler and cannot recursively call GET, SAMPLE, or DHT. A request gathers at
most `L` local plus `3L` remote records, validates/dedupes/shuffles them, and
returns at most `L` within the byte budget. Partial success is normal.

The M9 adapter first targets five unique peers. With two sources it requests
three from PRIMARY and two from FALLBACK; one source may provide all five. A
later top-up reaches at most ten unique peers. One acquisition round uses at
most four total GET RPCs, concurrency two, exclusions 32, and no lower-layer
retry loop. M9 retains probe concurrency five and promotes the first complete
working ICE/AWG/lease path; it does not wait merely to fill ACTIVE/RESERVE/COLD.
Unknown direct-control RTT remains unknown rather than fake zero or infinity.

The client retains at most 320 descriptor-epoch route entries and evicts them
FIFO. An ingress forwarder retains at most 1024 full owner tuples and removes
them when their descriptor/receipt validity expires. Routes are keyed by
`(server PeerID, server incarnation, registration ID)`, not merely by server,
so one owner can route independently to many registered Exits. Candidate
acquisition also records the exact ingress that supplied each owner hint;
signaling retries at most three verified `(ingress, owner)` paths, and every
retry consumes the common message/byte budget.

Sampling is randomized locally but not claimed globally uniform: partial DHT
views, unequal pools, and multiple registration owners introduce bias.

## Admission and overload

Service handlers and pending contexts are semaphore/map bounded. A token-bucket
admission layer accounts separately by authenticated PeerID and observed source
prefix (`/24` IPv4, `/64` IPv6). Source bursts are multiplied by 64 so CGNAT is
not treated as one identity, while a source flood remains bounded. Defaults are:

| Operation | Per-PeerID policy |
|---|---:|
| REGISTER/UPDATE | one token / 10s, burst 2 |
| HEARTBEAT | one / 5s, burst 3 |
| GET | one / 1s, burst 3 |
| SAMPLE | one / 1s, burst 3 |
| SIGNAL | one / 250ms, burst 8 |

The limiter has at most 40,000 keys and evicts expired buckets before admitting
new keys. `SAMPLE_LOCAL` additionally requires an authenticated, unexpired
announcement carrying both Rendezvous and public-control capability. Overload
returns typed RATE_LIMITED, BUSY, PARTIAL, or route errors rather than growing
queues. This is resource bounding, not Sybil resistance.

## Signaling routes and security boundary

The maximum route is `Client -> ingress -> registration owner -> Exit`.
The owner delivers through the exact current registration channel and does not
forward farther. Replies return through the request context. One attempt may
try at most three verified ingress/owner paths within the caller's remaining
deadline.
A failed owner route is not proof that the Exit failed.

End-peer signed transcripts, not broker libp2p identity, enter authorization.
Rendezvous can censor, delay, bias a sample, and see v1 ICE credentials. It
cannot change the authenticated AWG binding, identities, predecessor hashes,
lease owner, or message body without detection. M12 does not claim signaling
confidentiality, global reputation, diversity, Sybil resistance, TURN/relay,
public UDP capability, geographic egress attestation, or telemetry.
