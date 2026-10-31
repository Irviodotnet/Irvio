# Ubuntu desktop/server tarball

Desktop target: Ubuntu 24.04/26.04 amd64, systemd-resolved already configured.
Desktop Local API uses the selected user's UID and a mode-0600 systemd socket;
no group-membership refresh, logout or reboot is required after installation.
Headless server target: Ubuntu 24.04/26.04 amd64, no GUI or resolved dependency.
Server install: `sudo ./install.sh --role server --bootstrap 'irvio-bootstrap:v1:<BUNDLE>'`.
Server preset uses TCP/4000, UDP/3478 and fixed UDP/51820; readiness is checked after installation.
Installation instructions are in `install.md` (also copied beside and into the
archive). Existing manual deployments are deliberately refused, not migrated.

Build from the repository root, with the official September 2026 DB-IP Country
Lite MMDB downloaded outside Git:

```bash
bash packaging/ubuntu/build.sh /tmp/irvio-output /path/to/dbip-country-lite-2026-09.mmdb
python3 packaging/ubuntu/tests/audit_archive.py /tmp/irvio-output/irvio-1.1.0-alpha.20261005.4-ubuntu-amd64.tar.gz
```

The dated package name, DB checksum, attribution and user instructions are
intentionally pinned together for this Alpha artifact. Update them together for
a later release. `BUILD_INFO.json` records HEAD and dirty source status. UI builds
take place in a separate source copy, never over a running workstation bundle.

The builder uses an explicit payload allowlist, not an archive of the repository
or installed profile. It includes the full Flutter bundle, daemon, GeoIP,
third-party notices and the upstream sources of the two MPL-covered Go modules.
It never reads device identities or private keys. GeoIP data remains outside Git.

Local Private IRVIO preview (not a public Alpha release): use a separate clean
committed checkout and an output directory outside that checkout.

```bash
bash packaging/ubuntu/build.sh /tmp/irvio-private-output /path/to/dbip-country-lite-2026-09.mmdb --private-preview
```

The archive is named `irvio-private-preview-<12-character-commit>-ubuntu-amd64`.
Dirty or untracked source is refused before building. The public Alpha name and
default invocation remain unchanged. Audit the resulting archive with the same
`tests/audit_archive.py`; a successful build is not installation acceptance and
does not authorize publication or production deployment.

Container validation:

```bash
docker build -t irvio-packaging-test:ubuntu24.04 packaging/ubuntu/tests
docker run --rm --ulimit core=0 -e IRVIO_PACKAGING_TEST=1 \
  -e DEBIAN_FRONTEND=noninteractive \
  -v /absolute/repo/packaging/ubuntu:/source:ro \
  -v /absolute/extracted/package:/package:ro \
  -w /source irvio-packaging-test:ubuntu24.04 bash tests/container-test.sh
```

The container tests real dependency resolution, file installation/removal,
permissions and a relocated UI in Xvfb. Host services/DNS/nft are **fixtures**.
No production daemon is run. These tests do not substitute for live socket
activation, VPN, DNS or reboot testing on the mini-PC.
