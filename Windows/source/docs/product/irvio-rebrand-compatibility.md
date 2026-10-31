# IRVIO naming and compatibility

The product names are `irvio` (Linux desktop UI) and `irviod` (daemon). The
first-party source headers declare `AGPL-3.0-only`; the full license is in
`LICENSE`. Bundled third-party notices and the DB-IP attribution remain
separate.

The pre-release product uses `irvio-bootstrap:v1:`, `irviod.service`,
`irvio.socket`, `irvio-boot-recover.service`, and the `irvio` Local API group.
Canonical directories are `/var/lib/irvio`, `/run/irvio`, and `/etc/irvio`.
Old bootstrap prefixes are rejected. Existing lab deployments require a
coordinated offline migration preserving identities; the installer refuses
legacy state rather than silently creating a second profile.

Source paths use `cmd/irviod`, `proto/irvio`, and
`ui/lib/irvio/generated/irvio`. The Go module is
`github.com/Irviodotnet/Irvio`; imports and build scripts use these names.

The following network and engineering identifiers are intentionally unchanged:

- `hydra.v1` and `hydra.local.v1` protobuf package identities;
- the `hydra0` interface and existing cryptographic signature domains;
- legacy engineering/test environment variables, nftables objects, and profile
  markers where renaming them would break existing tests or live state.

Do not replace these remaining identifiers mechanically. The bootstrap text
prefix changed, but the v1 payload and network wire protocols did not.

The Ubuntu tarball installs the new executable names, but deliberately refuses
to overwrite an older installation. It has been validated in a disposable
Ubuntu 24.04 container; host VPN, DNS, socket activation, and reboot behavior
are not certified by that container test.
