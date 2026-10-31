# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

import json
import struct
import sys

stages = ["protection-installed", "lookup-cancelled-policy-still-active",
          "post-hold-policy-still-active", "dns-restored-policy-still-active",
          "before-protection-remove", "after-protection-remove"]
windows = []
active = None
dns_outside = 0
protected_queries_outside = 0
baseline = 0
with open(sys.argv[1], "rb") as source:
    header = source.read(24)
    if len(header) != 24 or header[:4] != b"\xd4\xc3\xb2\xa1" or struct.unpack_from("<I", header, 20)[0] != 1:
        raise SystemExit("Unsupported PCAP format")
    while True:
        row = source.read(16)
        if not row:
            break
        if len(row) != 16:
            raise SystemExit("Truncated PCAP record")
        sec, usec, size, original = struct.unpack("<IIII", row)
        if size > 65536:
            raise SystemExit("Oversized frame")
        frame = source.read(size)
        if len(frame) != size:
            raise SystemExit("Truncated frame")
        if len(frame) < 34 or frame[12:14] != b"\x08\x00" or frame[26:30] != bytes([10, 0, 2, 15]):
            continue
        offset = 14 + (frame[14] & 15) * 4
        proto = frame[23]
        if proto not in (6, 17) or len(frame) < offset + 8:
            continue
        port = struct.unpack_from("!H", frame, offset + 2)[0]
        payload = frame[offset + 8:]
        timestamp = sec + usec / 1_000_000
        if proto == 17 and port == 49495 and payload.startswith(b"irvio-dns-marker-"):
            stage = payload.removeprefix(b"irvio-dns-marker-").decode("ascii")
            if stage == stages[0]:
                if active is not None:
                    raise SystemExit("Nested policy markers")
                active = {"markers": [], "physical_dns_packets": 0}
            if active is None or stage != stages[len(active["markers"])]:
                raise SystemExit("Unexpected marker order")
            active["markers"].append({"stage": stage, "pcap_time": timestamp})
            if stage == stages[-1]:
                windows.append(active)
                active = None
        if port == 53:
            if active is not None:
                active["physical_dns_packets"] += 1
            else:
                dns_outside += 1
                if b"baseline-" in frame:
                    baseline += 1
                if b"\x05irvio\x07invalid\x00" in frame and b"baseline-" not in frame:
                    protected_queries_outside += 1
if active is not None or len(windows) != 3 or baseline == 0:
    raise SystemExit("Incomplete capture or missing positive control")
report = {"intervals": windows, "physical_dns_outside_protection": dns_outside,
          "baseline_query_packets": baseline, "test_queries_after_removal": protected_queries_outside,
          "result": "PASS" if all(w["physical_dns_packets"] == 0 for w in windows) else "FAIL"}
print(json.dumps(report, indent=2))
if report["result"] != "PASS":
    raise SystemExit(1)
