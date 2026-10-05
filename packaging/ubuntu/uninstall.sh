#!/usr/bin/env bash
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

set -Eeuo pipefail
BUNDLE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
# shellcheck source=scripts/common.sh
. "$BUNDLE/scripts/common.sh"
case ${1:-} in
    -h|--help) printf '%s\n' 'Usage: sudo ./uninstall.sh' 'First choose Shutdown IRVIO. Profile, GeoIP, config, group and user settings are preserved.'; exit 0 ;;
    '') ;;
    *) die 'No purge/force option exists. Use Delete profile in the UI separately if wanted.' ;;
esac
require_root
if [[ -f $PREFIX/INSTALL_MARKER && ! -L $PREFIX/INSTALL_MARKER && $(<"$PREFIX/INSTALL_MARKER") == irvio-ubuntu-server-v1 ]]; then
    . "$BUNDLE/scripts/server.sh"
    uninstall_server
    exit 0
fi
check_owned_install
check_quiescent
# Close the activation path first; if an API client raced with this, refuse to
# remove the daemon. Never kill a connected daemon or flush protection here.
systemctl disable --now irvio.socket
if ! (check_quiescent); then
    systemctl enable --now irvio.socket
    die 'A session appeared while uninstalling; socket activation restored. Complete Shutdown and retry.'
fi
systemctl disable --now irvio-boot-recover.service
for unit in "${UNITS[@]}"; do rm -f -- "$UNIT_DIR/$unit"; done
rm -f -- "$LAUNCHER" "$DESKTOP"
systemctl daemon-reload
systemctl reset-failed irviod.service irvio.socket irvio-boot-recover.service 2>/dev/null || true
# Exact, root-owned installation verified above, no nested mounts, no profile.
rm -rf --one-file-system -- /opt/irvio
if command -v update-desktop-database >/dev/null; then
    update-desktop-database /usr/local/share/applications || true
fi
note 'Removed /opt/irvio, its launcher, desktop entry and three systemd units.'
note 'Preserved /var/lib/irvio (profile/GeoIP), /etc/irvio, irvio group and per-user settings/autostart.'
note 'No system packages, DNS settings or unrelated firewall rules were removed.'
