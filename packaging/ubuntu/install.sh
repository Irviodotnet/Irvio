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

usage() {
    printf '%s\n' 'Usage: sudo ./install.sh [--role desktop] [--user LOGIN] [--no-deps]' \
        '       sudo ./install.sh --role server --bootstrap irvio-bootstrap:v1:<BUNDLE>' \
        '       ./install.sh --check' \
        'Installs the Ubuntu desktop package, without connecting the VPN.' \
        '--check: read-only bundle/host/library check; no installation.' \
        '--no-deps: do not run apt-get (dependencies must already be installed).'
}

target_user=${SUDO_USER:-}
install_deps=true
check_only=false
role=desktop
bootstrap=
while (($#)); do
    case $1 in
        --role) (($# >= 2)) || die '--role requires desktop or server'; role=$2; shift 2 ;;
        --bootstrap) (($# >= 2)) || die '--bootstrap requires a bundle'; bootstrap=$2; shift 2 ;;
        --user) (($# >= 2)) || die '--user requires a login'; target_user=$2; shift 2 ;;
        --no-deps) install_deps=false; shift ;;
        --check) check_only=true; shift ;;
        -h|--help) usage; exit 0 ;;
        *) usage; die "Unknown option: $1" ;;
    esac
done
[[ $role == desktop || $role == server ]] || die 'Invalid role: expected desktop or server.'
if [[ $role == server ]]; then
    . "$BUNDLE/scripts/server.sh"
    install_server
    exit 0
fi
[[ -z $bootstrap ]] || die 'Desktop bootstrap must be imported through the UI.'
check_bundle "$BUNDLE"
check_host
if $check_only; then
    check_libraries "$BUNDLE"
    note 'Read-only check PASS. This does not certify that old installations/session residue are absent.'
    exit 0
fi
require_root
[[ -n $target_user && $target_user != root && $target_user != -* ]] || die 'Specify the desktop login with --user LOGIN (not root).'
id "$target_user" >/dev/null 2>&1 || die "Unknown desktop user: $target_user"
target_uid=$(id -u "$target_user")
(( target_uid >= 1000 && target_uid < 4294967295 )) || die 'Refusing to grant Local API access to a system account.'
check_fresh_targets
# Dependencies are explicit. In particular, we do not install/switch DNS managers.
if $install_deps; then
    apt-get update
    apt-get install -y --no-install-recommends "${PACKAGES[@]}"
fi
for tool in nft ip pgrep; do command -v "$tool" >/dev/null || die "Missing dependency: $tool"; done
check_libraries "$BUNDLE"
check_quiescent

stage=$(mktemp -d /opt/.irvio-install.XXXXXX)
trap 'note "Installation did not complete; inspect /opt/irvio and the systemd units before retrying. No VPN Connect was requested." >&2' ERR
cp -a --no-preserve=ownership "$BUNDLE/payload/bin" "$BUNDLE/payload/ui" "$BUNDLE/integration" "$BUNDLE/licenses" "$stage/"
install -d -m 0755 "$stage/scripts"
install -m 0644 "$BUNDLE/scripts/common.sh" "$stage/scripts/common.sh"
install -m 0755 "$BUNDLE/uninstall.sh" "$stage/uninstall.sh"
install -m 0644 "$BUNDLE/install.md" "$BUNDLE/BUILD_INFO.json" "$stage/"
install -m 0644 "$BUNDLE/integration/INSTALL_MARKER" "$stage/INSTALL_MARKER"
configure_desktop_access "$stage" "$target_uid"
chmod -R go-w "$stage"
chmod 0755 "$stage"
chown -R root:root "$stage"

getent group irvio >/dev/null || groupadd --system irvio
[[ -d $STATE ]] || install -d -o root -g irvio -m 0700 "$STATE"
[[ -d $STATE/geo ]] || install -d -o root -g root -m 0755 "$STATE/geo"
if [[ ! -e $STATE/geo/country.mmdb ]]; then
    install -o root -g root -m 0644 "$BUNDLE/payload/geo/country.mmdb" "$STATE/geo/country.mmdb"
    install -o root -g root -m 0644 "$BUNDLE/payload/geo/ATTRIBUTION.md" "$STATE/geo/ATTRIBUTION.md"
else
    note 'Existing GeoIP database preserved (not overwritten).'
fi
mv -T "$stage" "$PREFIX"
install -d -m 0755 /usr/local/bin /usr/local/share/applications "$UNIT_DIR"
install -m 0755 "$PREFIX/integration/irvio" "$LAUNCHER"
install -m 0644 "$PREFIX/integration/irvio.desktop" "$DESKTOP"
for unit in "${UNITS[@]}"; do install -m 0644 "$PREFIX/integration/systemd/$unit" "$UNIT_DIR/$unit"; done
systemctl daemon-reload
systemctl enable irvio-boot-recover.service irvio.socket
# This runs boot recovery and opens the API socket. It does NOT start hydrad.
systemctl start irvio.socket
systemctl is-active --quiet irvio.socket
trap - ERR
if command -v update-desktop-database >/dev/null; then
    update-desktop-database /usr/local/share/applications || true
fi
note "Installed for $target_user (UID $target_uid). Local API access is available immediately; no logout or reboot is required."
note 'Then open IRVIO from the application menu (or run irvio as your normal user).'
note 'No bootstrap/profile is included, no VPN connection was started.'
