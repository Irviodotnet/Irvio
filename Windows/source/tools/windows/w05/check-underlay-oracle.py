# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

import argparse
import collections
import json
import pathlib
import re


def verify(lines, runs):
    seen = collections.Counter()
    for line in lines:
        if line.strip() == "W05 echo oracle ready":
            continue
        event = json.loads(line)
        token = event["token"]
        ordinary = re.fullmatch(r"irvio-nat-wfp-backend-owned-(baseline|cleanup)-\d+", token)
        control = re.fullmatch(r"irvio-nat-wfp-owned-\d+-(first|second|after-first-close|cycle-\d+)", token)
        if ordinary and event["protocol"] in ("tcp", "udp"):
            seen[(event["protocol"], ordinary[1])] += 1
        elif control and event["protocol"] == "tcp":
            seen[("tcp", control[1])] += 1
        else:
            raise ValueError(f"Unexpected or forbidden packet at oracle: {token}")
    expected = collections.Counter({(p, tag): runs for p in ("tcp", "udp") for tag in ("baseline", "cleanup")})
    expected.update({("tcp", tag): runs for tag in ("first", "second", "after-first-close")})
    expected.update({("tcp", f"cycle-{i}"): runs for i in range(20)})
    if seen != expected:
        raise ValueError(f"Oracle coverage mismatch: missing={expected-seen}, extra={seen-expected}")
    return {"runs": runs, "accepted_packets": sum(seen.values()), "forbidden_tokens": 0, "result": "PASS"}


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("log", type=pathlib.Path)
    parser.add_argument("--runs", type=int, default=3)
    args = parser.parse_args()
    if args.runs < 1:
        parser.error("runs must be positive")
    print(json.dumps(verify(args.log.read_text().splitlines(), args.runs), sort_keys=True))
