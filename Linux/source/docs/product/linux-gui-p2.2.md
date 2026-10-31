# P2.2: Production GeoIP and dynamic available countries

P2.2 makes the Linux location selector a view of bounded, fresh HYDRA
presence rather than a static list. It does not change candidate selection,
session establishment, or any active VPN lifecycle.

## Design boundary

- Rendezvous classifies the source address of an authenticated Exit
  registration with the existing M12 `CountryResolver`. The existing live
  registry country index remains the only local source of fresh, accepting,
  observed Exit countries.
- A dedicated read-only libp2p operation returns only the sorted ISO 3166-1
  alpha-2 codes in one Rendezvous server's local registry. The handler never
  performs discovery, `GET_CANDIDATES`, or remote sampling.
- The client daemon queries at most ten discovered/configured Rendezvous
  peers, with at most five concurrent calls and bounded per-peer and overall
  deadlines. It unions, validates, deduplicates, and sorts successful replies.
- A small product cache prevents GUI redraws from causing distributed work.
  Results distinguish `READY`, `PARTIAL`, `EMPTY`, and `UNAVAILABLE`.
- Local API v1.3 adds only `GET_AVAILABLE_COUNTRIES`. The Unix socket,
  framing, authorization, 64 KiB bound, and authoritative status contract are
  unchanged. Older clients continue to work; a new GUI connected to an older
  daemon shows locations as unavailable.
- Flutter contains only ISO presentation metadata (localized name and flag).
  It does not perform GeoIP or query Rendezvous. Only daemon-returned codes
  are selectable. A connected country remains visible even if a later
  availability snapshot omits it; that metadata never triggers disconnect,
  failover, or reselection.

## Country semantics

An available country means that at least one Rendezvous in the bounded queried
set currently reports a fresh, accepting Exit with an `OBSERVED` country from
the registration connection's server-observed source IP. Community and
project-operated nodes use the same Exit capability and registration path.
`CLAIMED`, `UNKNOWN`, invalid, stale, expired, closed, and non-accepting
registrations do not contribute.

The result is a bounded fresh distributed view, not a globally consistent
registry. Absence means that the sampled live Rendezvous set did not confirm a
country; it does not prove that no such Exit exists anywhere in HYDRA.

## GeoIP deployment

Rendezvous operators install a Country-format MMDB at
`/var/lib/irvio/geo/country.mmdb` and pass it with
`--geoip-db /var/lib/irvio/geo/country.mmdb`. Client-only and Exit-only nodes
do not require the database. A production database is external runtime data
and is never committed, embedded in `hydrad`, shipped in Flutter assets, or
included in bootstrap bundles.

DB-IP Country Lite is supported through its standard `country.iso_code` MMDB
field. Operational replacement validates a downloaded file as
`country.mmdb.new`, writes and syncs it, atomically renames it over the active
path, and then reloads the resolver. The M12 resolver retains its previous
validated reader when opening a replacement fails.

DB-IP publishes the current monthly Country Lite MMDB at
<https://db-ip.com/db/download/ip-to-country-lite>. Select the MMDB format,
retain the accompanying license information, and stage the decompressed file
outside the active path. One installation workflow is:

```bash
install -d -o root -g irvio -m 0750 /var/lib/irvio/geo
mmdbinspect -db country.mmdb.new 193.33.194.211
install -o root -g irvio -m 0640 country.mmdb.new /var/lib/irvio/geo/country.mmdb.new
sync /var/lib/irvio/geo/country.mmdb.new
mv /var/lib/irvio/geo/country.mmdb.new /var/lib/irvio/geo/country.mmdb
```

Use an MMDB-aware validator available in the operator environment; the command
above is illustrative and must succeed before replacement. A newly started
Rendezvous opens the file passed through `--geoip-db`. A running process may
use the existing atomic `MMDBResolver.Reload` integration point; P2.2 does not
add an in-daemon downloader or repurpose an unrelated signal. Verification is
an actual public IPv4 and IPv6 lookup followed by observing the expected
country in the live registration pool.

Suggested ownership is `root:hydra`, mode `0750` for the directory and `0640`
for the database file. Missing, corrupt, or unresolved data remains explicitly
degraded/`UNKNOWN`; a claimed country is never promoted to observed.

DB-IP Country Lite is licensed under CC BY 4.0. Required attribution:
**IP geolocation data by DB-IP**. See the provider's
[MMDB format description](https://db-ip.com/db/format/ip-to-country-lite/mmdb.html).
