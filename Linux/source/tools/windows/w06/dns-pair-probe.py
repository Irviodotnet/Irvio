#!/usr/bin/env python3
# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

import argparse
import ipaddress
import json
import socket
import struct
import time


def main():
    parser = argparse.ArgumentParser(description="Direct paired DNS measurement on an isolated lab host")
    parser.add_argument("--source", required=True, type=ipaddress.IPv4Address)
    parser.add_argument("--resolver", required=True, type=ipaddress.IPv4Address)
    parser.add_argument("--iterations", type=int, choices=range(1, 51), default=10)
    args = parser.parse_args()
    question = b"\x05speed\x0acloudflare\x03com\x00"
    for gap in (0, 20):
        for iteration in range(args.iterations):
            ids = (32768 + gap * 100 + iteration * 2, 32769 + gap * 100 + iteration * 2)
            with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
                sock.bind((str(args.source), 0))
                sock.connect((str(args.resolver), 53))
                started = time.monotonic()
                for index, kind in enumerate((1, 28)):
                    if index and gap:
                        time.sleep(gap / 1000)
                    sock.send(struct.pack("!6H", ids[index], 0x100, 1, 0, 0, 0) + question + struct.pack("!2H", kind, 1))
                seen = {}
                while len(seen) < 2:
                    remaining = 0.8 - (time.monotonic() - started)
                    if remaining <= 0:
                        break
                    sock.settimeout(remaining)
                    try:
                        packet = sock.recv(65535)
                    except socket.timeout:
                        break
                    if len(packet) < 12 or not packet[2] & 128:
                        continue
                    identifier = struct.unpack_from("!H", packet)[0]
                    if identifier in ids:
                        seen[identifier] = {"elapsed_ms": (time.monotonic() - started) * 1000, "rcode": packet[3] & 15}
                print(json.dumps({"iteration": iteration, "gap_ms": gap, "source": str(args.source),
                                  "resolver": str(args.resolver), "port": sock.getsockname()[1],
                                  "a_id": ids[0], "aaaa_id": ids[1], "a": seen.get(ids[0]), "aaaa": seen.get(ids[1])}), flush=True)


if __name__ == "__main__":
    main()
