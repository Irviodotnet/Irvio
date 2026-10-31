#!/usr/bin/env bash
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me
set -Eeuo pipefail
export LC_ALL=C
HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
[[ $EUID == 0 ]] || { echo 'Run: sudo /opt/irvio/scripts/check-server.sh'; exit 1; }
deadline=$((SECONDS + 300))
detail='Waiting for service startup.'
while ((SECONDS < deadline)); do
    invocation=$(systemctl show irvio-server.service -p InvocationID --value)
    pid=$(systemctl show irvio-server.service -p MainPID --value)
    if systemctl is-active --quiet irvio-server.service && [[ $pid =~ ^[1-9][0-9]*$ && $invocation =~ ^[a-f0-9]{32}$ ]]; then
        if detail=$(journalctl "_SYSTEMD_INVOCATION_ID=$invocation" -n 4000 --no-pager -o cat | python3 "$HERE/server-health.py"); then
            sockets=$(ss -H -luntp)
            firewall=$(nft list ruleset)
            if grep -E "udp .*:3478 .*pid=$pid," <<< "$sockets" >/dev/null &&
               grep -E "udp .*:51820 .*pid=$pid," <<< "$sockets" >/dev/null &&
               grep -E "tcp .*:4000 .*pid=$pid," <<< "$sockets" >/dev/null &&
               [[ $firewall == *drop-safety-v0-tcp* && $firewall == *drop-safety-v0-udp* ]] &&
               [[ $(systemctl show irvio-server.service -p InvocationID --value) == "$invocation" ]]; then
                echo 'IRVIO server: OK — service running, fixed ports bound, M11 verified, Rendezvous/Exit registered, Safety Policy installed.'
                echo 'Provider firewall must permit TCP/4000 and UDP/3478,51820. This local check does not replace an external client traffic test.'
                exit 0
            fi
            detail='Readiness logs OK; required sockets or Safety Policy rules are missing.'
        fi
    else
        detail='Service is not running or is restarting.'
        if systemctl is-failed --quiet irvio-server.service; then break; fi
    fi
    sleep 3
done
echo "IRVIO server: NOT READY. $detail" >&2
echo 'Installation may be present; do not delete the profile or reinstall blindly.' >&2
echo 'Inspect: sudo systemctl status irvio-server.service --no-pager' >&2
echo 'Logs: sudo journalctl -u irvio-server.service -n 100 --no-pager' >&2
echo 'Check bootstrap availability, two independent M11 verifiers, GeoIP DB and provider firewall; free conflicting ports if bind fails.' >&2
echo 'After correcting the cause: sudo systemctl restart irvio-server.service' >&2
echo 'Recheck: sudo /opt/irvio/scripts/check-server.sh' >&2
exit 1
