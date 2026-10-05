#!/usr/bin/env bash
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

# Packaging helpers only. No networking lifecycle or protocol implementation.
set -Eeuo pipefail

PREFIX=/opt/irvio
STATE=/var/lib/irvio
UNIT_DIR=/etc/systemd/system
LAUNCHER=/usr/local/bin/irvio
DESKTOP=/usr/local/share/applications/irvio.desktop
MARKER=irvio-ubuntu-desktop-v1
UNITS=(irviod.service irvio.socket irvio-boot-recover.service)
# Used by install.sh, which sources this file.
# shellcheck disable=SC2034
PACKAGES=(libgtk-3-0t64 libayatana-appindicator3-1 libstdc++6 libgl1 libegl1 libgles2
          libnotify-bin iproute2 nftables procps ca-certificates)

die() { printf 'IRVIO: %s\n' "$*" >&2; exit 1; }
note() { printf 'IRVIO: %s\n' "$*"; }
require_root() { [[ $EUID == 0 ]] || die 'Run with sudo; do not run the UI as root.'; }

check_host() {
    [[ -r /etc/os-release ]] || die 'Cannot identify the operating system.'
    # shellcheck disable=SC1091
    . /etc/os-release
    [[ ${ID:-} == ubuntu && ( ${VERSION_ID:-} == 24.04 || ${VERSION_ID:-} == 26.04 ) ]] ||
        die 'This build supports Ubuntu 24.04/26.04 LTS only.'
    [[ $(dpkg --print-architecture) == amd64 ]] || die 'This build requires amd64 / x86-64.'
    [[ -d /run/systemd/system ]] || die 'A normal systemd host is required (not WSL/container/chroot).'
    [[ -c /dev/net/tun ]] || die '/dev/net/tun is unavailable. Enable TUN support first.'
    systemctl is-active --quiet systemd-resolved.service ||
        die 'systemd-resolved must already be running. DNS configuration is not changed by this installer.'
    [[ -x /usr/bin/resolvectl ]] || die 'The production DNS path requires /usr/bin/resolvectl.'
    /usr/bin/resolvectl status >/dev/null || die 'systemd-resolved is not usable.'
}

check_bundle() {
    local bundle=$1
    [[ -f $bundle/SHA256SUMS && -f $bundle/payload/bin/irviod ]] || die 'Incomplete extracted archive.'
    [[ -z $(find "$bundle/payload" "$bundle/integration" -type l -print -quit) ]] ||
        die 'Unexpected symbolic link in package payload.'
    (cd "$bundle" && sha256sum --check --strict --quiet SHA256SUMS) || die 'Package checksum verification failed.'
}

check_libraries() {
    local bundle=$1 file output soname
    for file in "$bundle/payload/ui/irvio" "$bundle/payload/ui/lib/"*.so; do
        if [[ $file == "$bundle/payload/ui/irvio" ]]; then
            # The executable must find its own bundle through its installed RPATH.
            output=$(env -u LD_PRELOAD -u LD_LIBRARY_PATH ldd "$file" 2>&1) || die "Cannot check shared libraries for $file: $output"
        else
            # Plugins are loaded by the executable with the engine already loaded.
            # Standalone ldd does not inherit that executable's $ORIGIN/lib context.
            output=$(env -u LD_PRELOAD LD_LIBRARY_PATH="$bundle/payload/ui/lib" ldd "$file" 2>&1) || die "Cannot check shared libraries for $file: $output"
        fi
        if [[ $output == *'not found'* ]]; then
            printf '%s\n' "$output" >&2
            die 'Required shared libraries are missing. Install the dependencies listed in install.md.'
        fi
    done
    # Flutter's renderer dlopens these; they do not all appear in ldd output.
    output=$(/sbin/ldconfig -p) || die 'Cannot inspect the shared-library cache.'
    for soname in libEGL.so.1 libGLESv2.so.2; do
        [[ $output == *"$soname"* ]] || die "Missing graphics runtime $soname (libegl1/libgles2)."
    done
}

check_quiescent() {
    local process tables
    for process in hydrad hydra_ui irviod irvio; do
        if pgrep -x "$process" >/dev/null; then
            die "$process is running. Choose Shutdown IRVIO in the UI, then retry. No process has been killed."
        fi
    done
    [[ ! -e /run/irvio/session-state ]] ||
        die 'A same-boot session intent exists. Recover the existing daemon and use Shutdown first; do not delete protection manually.'
    if ip link show dev hydra0 >/dev/null 2>&1; then
        die 'hydra0 still exists. Complete daemon-owned teardown first.'
    fi
    tables=$(nft list tables) || die 'Cannot inspect nftables safely.'
    if [[ $tables =~ (hydra_|irvio_) ]]; then
        die 'HYDRA/IRVIO nftables state remains. Complete normal Shutdown first. No firewall state has been removed.'
    fi
}

check_fresh_targets() {
    local path unit
    for path in "$PREFIX" "$LAUNCHER" "$DESKTOP" /usr/local/bin/hydrad /usr/local/bin/irviod /opt/hydra/ui; do
        [[ ! -e $path && ! -L $path ]] || die "Existing installation/path: $path. See the migration/update section in install.md."
    done
    for unit in "${UNITS[@]}" hydrad.service hydra.socket hydra-boot-recover.service hydra.service hydra-client.service; do
        [[ $(systemctl show "$unit" -p LoadState --value) == not-found ]] ||
            die "Existing unit $unit found. This installer will not overwrite a manual/older deployment."
        [[ ! -e $UNIT_DIR/$unit && ! -L $UNIT_DIR/$unit ]] || die "Existing unit file: $UNIT_DIR/$unit"
    done
    for path in /var/lib/hydra /run/hydra /etc/hydra; do
        [[ ! -e $path && ! -L $path ]] || die "Legacy state at $path; complete a reviewed offline migration before installing."
    done
    [[ ! -e /etc/irvio/config.json ]] || die 'Existing /etc/irvio/config.json requires manual review before desktop installation.'
    for path in "$STATE" "$STATE/geo" "$STATE/geo/country.mmdb"; do
        [[ ! -L $path ]] || die "Refusing a symbolic link at $path."
    done
}

check_owned_install() {
    [[ -d $PREFIX && ! -L $PREFIX && $(realpath "$PREFIX") == /opt/irvio ]] || die 'No regular /opt/irvio installation found.'
    [[ -f $PREFIX/INSTALL_MARKER && ! -L $PREFIX/INSTALL_MARKER ]] || die 'Missing installation ownership marker.'
    [[ $(stat -c %u "$PREFIX") == 0 && $(stat -c %u "$PREFIX/INSTALL_MARKER") == 0 ]] || die 'Installation is not root-owned.'
    [[ $(<"$PREFIX/INSTALL_MARKER") == "$MARKER" ]] || die 'Unrecognized installation marker.'
    local unit
    for unit in "${UNITS[@]}"; do
        cmp -s "$PREFIX/integration/systemd/$unit" "$UNIT_DIR/$unit" || die "Modified/unmanaged unit $unit; refusing to remove it."
        [[ ! -d $UNIT_DIR/$unit.d ]] || die "Unit overrides exist for $unit; review them before uninstalling."
    done
    cmp -s "$PREFIX/integration/irvio" "$LAUNCHER" || die 'Launcher was modified; refusing to remove it.'
    cmp -s "$PREFIX/integration/irvio.desktop" "$DESKTOP" || die 'Desktop entry was modified; refusing to remove it.'
    [[ -z $(findmnt -rn -o TARGET | awk '$0 == "/opt/irvio" || index($0, "/opt/irvio/") == 1') ]] ||
        die 'A filesystem is mounted inside /opt/irvio; refusing removal.'
}
