# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

import csv
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("cycles", Path(__file__).with_name("analyze-private-cycles.py"))
cycles = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cycles)


class AnalysisTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.path = Path(self.temp.name)
        self.verdict = {
            "result": "PASS", "warmup": 10, "measured_cycles": 100,
            "network_cleanup_each_cycle": True, "physical_https_blocked_each_cycle": True,
        }
        self.rows = []
        for index in range(1, 111):
            row = {name: 0 for name in cycles.METRICS}
            row.update(iteration=index, warmup=str(index <= 10), download_bytes=65536,
                       physical_https_blocked="True", handles=500 if index > 10 else 900,
                       rss_bytes=index * 1000, connect_ms=index)
            self.rows.append(row)

    def write(self):
        (self.path / "verdict.json").write_text(json.dumps(self.verdict), encoding="utf-16")
        with (self.path / "cycles.csv").open("w", encoding="utf-8-sig", newline="") as stream:
            writer = csv.DictWriter(stream, fieldnames=self.rows[0].keys())
            writer.writeheader()
            writer.writerows(self.rows)

    def test_distribution_excludes_warmup(self):
        self.write()
        result = cycles.analyze(self.path)
        self.assertEqual(result["metrics"]["handles"]["max"], 500)
        self.assertEqual(result["metrics"]["rss_bytes"]["slope_per_cycle"], 1000)
        self.assertEqual(result["metrics"]["connect_ms"]["p95"], 105)
        self.assertEqual(result["windows"]["rss_bytes"]["delta"], 80000)
        self.assertEqual(result["measured_download_bytes"], 6553600)
        self.assertIn("REQUIRES REVIEW", result["resource_verdict"])

    def test_incomplete_rows_rejected(self):
        self.rows.pop()
        self.write()
        with self.assertRaises(ValueError):
            cycles.analyze(self.path)

    def test_invalid_evidence_rejected(self):
        for field, value in (("iteration", 1), ("warmup", "True"), ("tcp", 1),
                             ("udp", 1), ("download_bytes", 0),
                             ("physical_https_blocked", "False"), ("handles", -1)):
            with self.subTest(field=field):
                original = self.rows[20][field]
                self.rows[20][field] = value
                self.write()
                with self.assertRaises(ValueError):
                    cycles.analyze(self.path)
                self.rows[20][field] = original

    def test_failure_file_rejected(self):
        self.write()
        (self.path / "failure.txt").write_text("cleanup failure")
        with self.assertRaises(ValueError):
            cycles.analyze(self.path)

    def test_bad_verdict_rejected(self):
        for field, value in (("result", "FAIL"), ("measured_cycles", 99),
                             ("network_cleanup_each_cycle", False),
                             ("physical_https_blocked_each_cycle", False)):
            with self.subTest(field=field):
                original = self.verdict[field]
                self.verdict[field] = value
                self.write()
                with self.assertRaises(ValueError):
                    cycles.analyze(self.path)
                self.verdict[field] = original


if __name__ == "__main__":
    unittest.main()
