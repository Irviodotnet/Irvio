#!/usr/bin/env python3
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

import datetime
import json
import sys


def readiness(lines, now):
    ready = dict(runtime=False, stun=False, m11=False, rendezvous=False, exit=False, registrations=False)
    expiry = None
    for line in lines:
        try:
            e = json.loads(line)
        except ValueError:
            continue
        if not isinstance(e, dict):
            continue
        msg = e.get("msg")
        if msg == "PRODUCT_RUNTIME_STARTED":
            ready["runtime"] = e.get("role") == "server"
        elif msg == "PUBLIC_STUN_READY":
            ready["stun"] = True
        elif msg == "PUBLIC_REACHABILITY_VERIFIED" and e.get("family") == 1:
            try:
                expiry = datetime.datetime.fromisoformat(e["expires_at"].replace("Z", "+00:00"))
                ready["m11"] = expiry > now
            except (ValueError, TypeError, KeyError):
                ready["m11"] = False
        elif msg in ("PUBLIC_REACHABILITY_UNVERIFIED", "PUBLIC_REACHABILITY_INVALIDATION_FAILED") and e.get("family", 1) == 1:
            ready["m11"] = False
        elif msg == "RENDEZVOUS_SERVICE_ACTIVE":
            ready["rendezvous"] = not e.get("geoip_degraded", True)
        elif msg == "RENDEZVOUS_SERVICE_DEGRADED":
            ready["rendezvous"] = False
        elif msg == "EXIT_CAPABILITY_READY":
            ready["exit"] = True
        elif msg in ("EXIT_CAPABILITY_SUSPENDED", "EXIT_COUNTRY_UNKNOWN", "EXIT_PUBLICATION_DEGRADED"):
            ready["exit"] = False
        elif msg == "RENDEZVOUS_REGISTRATIONS":
            ready["registrations"] = isinstance(e.get("active"), int) and e["active"] > 0 and not e.get("closed", True)
    if expiry is None or expiry <= now:
        ready["m11"] = False
    return ready


if __name__ == "__main__":
    state = readiness(sys.stdin, datetime.datetime.now(datetime.timezone.utc))
    missing = [key for key, value in state.items() if not value]
    print("Readiness pending: " + ", ".join(missing) if missing else "M11, STUN, Rendezvous, GeoIP-gated Exit and registrations: OK")
    sys.exit(bool(missing))
