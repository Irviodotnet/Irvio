#!/usr/bin/env bash
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

# Real filesystem/dependency tests, simulated systemd/DNS/nft only. No hydrad
# is executed. Not a substitute for the prior live networking acceptance.
set -Eeuo pipefail
[[ -f /.dockerenv && ${IRVIO_PACKAGING_TEST:-} == 1 && $EUID == 0 ]] || exit 90
fail() { echo "FAIL: $*" >&2; exit 1; }
expect_failure() { if "$@"; then fail "Unexpected success: $*"; fi; }

apt-get update
apt-get install -y --no-install-recommends systemd iproute2 nftables procps \
    shellcheck desktop-file-utils file binutils
shellcheck -x /source/install.sh /source/uninstall.sh /source/scripts/common.sh /source/build.sh /source/integration/irvio
for script in /source/install.sh /source/uninstall.sh /source/scripts/common.sh /source/build.sh; do bash -n "$script"; done
useradd --uid 2000 --create-home irvio-test
mkdir -p /run/systemd/system /run/irvio /dev/net
[[ -c /dev/net/tun ]] || mknod /dev/net/tun c 10 200
install -m 0755 /source/tests/mock-systemctl /usr/local/bin/systemctl
install -m 0755 /source/tests/mock-nft /usr/local/bin/nft
install -m 0755 /source/tests/mock-resolvectl /usr/bin/resolvectl
dns_before=$(sha256sum /etc/resolv.conf)

# A broken payload is rejected before any host install writes.
cp -a /package /tmp/irvio-corrupt-package
printf 'corrupt' >> /tmp/irvio-corrupt-package/payload/ui/data/flutter_assets/assets/irvio-globe.png
expect_failure /tmp/irvio-corrupt-package/install.sh --check
[[ ! -e /opt/irvio ]] || fail 'Corrupt archive was installed'

/package/install.sh --user irvio-test
[[ -x /opt/irvio/ui/irvio && -x /opt/irvio/bin/irviod ]] || fail 'Missing binaries'
[[ $(stat -c '%U:%G:%a' /opt/irvio) == root:root:755 ]] || fail 'Unsafe installation ownership'
[[ $(stat -c '%U:%a' /opt/irvio/bin/irviod) == root:755 ]] || fail 'Unsafe daemon permissions'
! id -nG irvio-test | grep -qw irvio || fail 'Installer unexpectedly changed user groups'
grep -qx 'SocketUser=2000' /etc/systemd/system/irvio.socket || fail 'Missing selected UID'
grep -qx 'SocketMode=0600' /etc/systemd/system/irvio.socket || fail 'Unsafe socket mode'
grep -qx 'SocketGroup=root' /etc/systemd/system/irvio.socket || fail 'Unexpected socket group'
grep -qx 'Environment=IRVIO_LOCAL_API_UID=2000' /etc/systemd/system/irviod.service || fail 'Missing UID authorization'
cmp /package/payload/geo/country.mmdb /var/lib/irvio/geo/country.mmdb
[[ ! -e /var/lib/irvio/profile ]] || fail 'Installer created a device profile'
[[ $dns_before == "$(sha256sum /etc/resolv.conf)" ]] || fail 'Host DNS config changed'
! pgrep -x hydrad || fail 'Installer started the old daemon'
! pgrep -x irviod || fail 'Installer started the daemon'
! grep -E 'start.*irviod.service' /run/irvio-package-test/systemctl.log || fail 'Installer requested daemon start'
systemd-analyze verify --man=no /etc/systemd/system/irviod.service /etc/systemd/system/irvio.socket /etc/systemd/system/irvio-boot-recover.service
desktop-file-validate /usr/local/share/applications/irvio.desktop
/package/install.sh --check
expect_failure /package/install.sh --user irvio-test --no-deps

# Run the real UI in a virtual display with a nonexistent API socket. This
# exercises relocation/Flutter/GTK only; no daemon is executed or activated.
ui_rc=0
runuser -u irvio-test -- dbus-run-session -- xvfb-run -a \
    env HYDRA_CONTROL_SOCKET=/tmp/irvio-nonexistent-control.sock LIBGL_ALWAYS_SOFTWARE=1 \
    timeout --kill-after=2s 8s /opt/irvio/ui/irvio > /tmp/irvio-ui-smoke.log 2>&1 || ui_rc=$?
cat /tmp/irvio-ui-smoke.log
[[ $ui_rc == 124 ]] || fail "Relocated UI exited before the smoke timeout ($ui_rc)"
! pgrep -x irvio || fail 'UI smoke process residue'
! pgrep -x hydrad || fail 'UI smoke activated the daemon'
! pgrep -x irviod || fail 'UI smoke activated the daemon'

# Guard against an active process, crash intent, firewall residue and edited units.
cp /usr/bin/sleep /tmp/hydrad
/tmp/hydrad 300 &
fake_pid=$!
expect_failure /opt/irvio/uninstall.sh
kill "$fake_pid"
wait "$fake_pid" || true
printf 'fixture' > /run/irvio/session-state
expect_failure /opt/irvio/uninstall.sh
rm /run/irvio/session-state
touch /run/irvio-package-test/nft-residue
expect_failure /opt/irvio/uninstall.sh
rm /run/irvio-package-test/nft-residue
printf '\n# admin change\n' >> /etc/systemd/system/irviod.service
expect_failure /opt/irvio/uninstall.sh
cp /opt/irvio/integration/systemd/irviod.service /etc/systemd/system/irviod.service

# Uninstall/reinstall must preserve profile, database and unrelated files.
mkdir -p /var/lib/irvio/profile
printf 'fixture-profile-not-a-real-key' > /var/lib/irvio/profile/preserve-test
geo_inode=$(stat -c %i /var/lib/irvio/geo/country.mmdb)
/opt/irvio/uninstall.sh
[[ ! -e /opt/irvio && ! -e /usr/local/bin/irvio && ! -e /etc/systemd/system/irvio.socket ]] || fail 'Uninstall residue'
[[ -f /var/lib/irvio/profile/preserve-test ]] || fail 'Profile was removed'
[[ $(stat -c %i /var/lib/irvio/geo/country.mmdb) == "$geo_inode" ]] || fail 'Database removed'
/package/install.sh --user irvio-test --no-deps
[[ $(stat -c %i /var/lib/irvio/geo/country.mmdb) == "$geo_inode" ]] || fail 'Database overwritten'
[[ -f /var/lib/irvio/profile/preserve-test ]] || fail 'Profile overwritten'
/opt/irvio/uninstall.sh
[[ $dns_before == "$(sha256sum /etc/resolv.conf)" ]] || fail 'DNS changed during uninstall'
ln -s /var/lib/irvio /opt/irvio
expect_failure /package/install.sh --user irvio-test --no-deps
expect_failure /package/uninstall.sh
rm /opt/irvio
[[ -f /var/lib/irvio/profile/preserve-test ]] || fail 'Symlink guard failed'
echo 'PASS: package checksums, apt dependencies, units, desktop entry, ownership, install, guards, uninstall, reinstall and preservation.'
echo 'PASS: relocated UI stayed running in Xvfb without a daemon/API socket.'
echo 'LIMITATION: systemd/DNS/nft were fixtures; no daemon, VPN connection or physical reboot was exercised.'
