# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

"""Summarize completed lifecycle evidence without turning trends into a leak verdict."""

import argparse
import csv
import io
import json
import math
import statistics
from pathlib import Path


METRICS = (
    "connect_ms", "disconnect_ms", "handles", "threads", "rss_bytes",
    "private_bytes", "tcp", "tcp_fin_wait2", "udp",
)


def read_text(path):
    data = path.read_bytes()
    encoding = "utf-16" if data.startswith((b"\xff\xfe", b"\xfe\xff")) else "utf-8-sig"
    return data.decode(encoding)


def distribution(values):
    ordered = sorted(values)
    count = len(values)
    center = (count - 1) / 2
    denominator = sum((index - center) ** 2 for index in range(count))
    mean = statistics.mean(values)
    slope = sum((index - center) * (value - mean) for index, value in enumerate(values))
    return {
        "min": ordered[0], "median": statistics.median(values),
        "p90": ordered[math.ceil(count * 0.90) - 1],
        "p95": ordered[math.ceil(count * 0.95) - 1], "max": ordered[-1],
        "first": values[0], "last": values[-1],
        "slope_per_cycle": slope / denominator if denominator else 0,
        "nondecreasing": all(left <= right for left, right in zip(values, values[1:])),
    }


def analyze(directory):
    verdict = json.loads(read_text(directory / "verdict.json"))
    if verdict.get("result") != "PASS":
        raise ValueError("campaign verdict is not PASS; retain failure evidence")
    warmup = verdict.get("warmup")
    measured = verdict.get("measured_cycles")
    if type(warmup) is not int or type(measured) is not int or warmup < 0 or measured < 100:
        raise ValueError("at least 100 measured cycles and an explicit warmup count required")
    for key in ("network_cleanup_each_cycle", "physical_https_blocked_each_cycle"):
        if verdict.get(key) is not True:
            raise ValueError("campaign does not attest " + key)
    if (directory / "failure.txt").exists():
        raise ValueError("campaign contains failure.txt")
    rows = list(csv.DictReader(io.StringIO(read_text(directory / "cycles.csv"))))
    if len(rows) != warmup + measured:
        raise ValueError("CSV row count differs from completed campaign")
    for index, row in enumerate(rows, 1):
        if int(row["iteration"]) != index:
            raise ValueError("duplicate, missing or reordered iteration")
        if row["warmup"] != str(index <= warmup):
            raise ValueError("warmup classification differs from verdict")
        for name in METRICS + ("download_bytes",):
            row[name] = int(row[name])
            if row[name] < 0:
                raise ValueError("negative metric: " + name)
        if row["tcp"] or row["udp"]:
            raise ValueError("active idle sockets remain")
        if row["download_bytes"] != 65536 or row["physical_https_blocked"] != "True":
            raise ValueError("user traffic or targeted physical-WAN probe failed")
    samples = rows[warmup:]
    window = 20
    comparisons = {}
    for name in METRICS:
        first = statistics.median(row[name] for row in samples[:window])
        last = statistics.median(row[name] for row in samples[-window:])
        comparisons[name] = {
            "first_20_median": first, "last_20_median": last, "delta": last - first,
        }
    return {
        "measured_cycles": measured, "warmup_cycles": warmup,
        "csv_and_verdict_consistent": True,
        "percentiles": "nearest-rank; median is the statistical median",
        "metrics": {name: distribution([row[name] for row in samples]) for name in METRICS},
        "windows": comparisons,
        "measured_download_bytes": sum(row["download_bytes"] for row in samples),
        "measured_physical_https_blocked": len(samples),
        "resource_verdict": "REQUIRES REVIEW; slope or RSS delta alone is not a leak verdict",
        "security_scope": "targeted physical-source HTTPS probe, not exhaustive packet capture",
        "limits": "requires terminal runner and final cleanup verification outside this analyzer",
    }


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("evidence", type=Path)
    arguments = parser.parse_args()
    print(json.dumps(analyze(arguments.evidence), indent=2, allow_nan=False))
