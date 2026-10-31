#!/usr/bin/env bash
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me
set -Eeuo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)
. "$root/packaging/ubuntu/scripts/common.sh"
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
mkdir -p "$work/integration/systemd"
for uid in 1000 2000 123456; do
    cp "$root/packaging/systemd/irvio.socket" "$root/packaging/systemd/irviod.service" "$work/integration/systemd/"
    configure_desktop_access "$work" "$uid"
    grep -qx "SocketUser=$uid" "$work/integration/systemd/irvio.socket"
    grep -qx 'SocketGroup=root' "$work/integration/systemd/irvio.socket"
    grep -qx 'SocketMode=0600' "$work/integration/systemd/irvio.socket"
    grep -qx "Environment=IRVIO_LOCAL_API_UID=$uid" "$work/integration/systemd/irviod.service"
    grep -qx 'User=root' "$work/integration/systemd/irviod.service"
done
for uid in 0 999 -1 +1000 01000 bad 4294967295 4294967296; do
    if (configure_desktop_access "$work" "$uid") >/dev/null 2>&1; then
        echo "FAIL: accepted invalid UID $uid"; exit 1
    fi
done
echo 'PASS: desktop socket/daemon UID configuration, no host installation.'
