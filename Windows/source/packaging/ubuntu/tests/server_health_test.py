#!/usr/bin/env python3
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me
import datetime
import importlib.util
import json
import pathlib
import unittest

spec = importlib.util.spec_from_file_location("health", pathlib.Path(__file__).parents[1] / "scripts/server-health.py")
health = importlib.util.module_from_spec(spec)
spec.loader.exec_module(health)


class ServerHealthTest(unittest.TestCase):
    def setUp(self):
        self.now = datetime.datetime(2026, 10, 4, tzinfo=datetime.timezone.utc)
        self.events = [
            dict(msg="PRODUCT_RUNTIME_STARTED", role="server"),
            dict(msg="PUBLIC_STUN_READY"),
            dict(msg="PUBLIC_REACHABILITY_VERIFIED", family=1, expires_at="2026-10-04T00:05:00Z"),
            dict(msg="RENDEZVOUS_SERVICE_ACTIVE", geoip_degraded=False),
            dict(msg="EXIT_CAPABILITY_READY"),
            dict(msg="RENDEZVOUS_REGISTRATIONS", active=2, closed=False),
        ]

    def state(self, events):
        return health.readiness(map(json.dumps, events), self.now)

    def test_all_gates_required(self):
        self.assertTrue(all(self.state(self.events).values()))
        for i in range(len(self.events)):
            self.assertFalse(all(self.state(self.events[:i]+self.events[i+1:]).values()))

    def test_stale_or_revoked_not_ready(self):
        for event in [dict(msg="PUBLIC_REACHABILITY_UNVERIFIED"), dict(msg="EXIT_CAPABILITY_SUSPENDED"), dict(msg="EXIT_COUNTRY_UNKNOWN"), dict(msg="RENDEZVOUS_SERVICE_DEGRADED"), dict(msg="RENDEZVOUS_REGISTRATIONS", active=0, closed=False), dict(msg="PUBLIC_REACHABILITY_VERIFIED", family=1, expires_at="2026-10-03T00:00:00Z")]:
            self.assertFalse(all(self.state(self.events+[event]).values()))

    def test_bad_logs_and_no_current_invocation(self):
        self.assertFalse(all(health.readiness(["garbage", "null", "[]"], self.now).values()))
        self.assertFalse(all(self.state([]).values()))

    def test_geoip_degraded(self):
        self.assertFalse(all(self.state(self.events+[dict(msg="RENDEZVOUS_SERVICE_ACTIVE", geoip_degraded=True)]).values()))

    def test_ipv6_unverified_does_not_hide_verified_ipv4(self):
        self.assertTrue(all(self.state(self.events+[dict(msg="PUBLIC_REACHABILITY_UNVERIFIED", family=2)]).values()))


if __name__ == "__main__":
    unittest.main()
