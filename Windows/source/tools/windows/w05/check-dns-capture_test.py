# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

import pathlib
import struct
import subprocess
import sys
import tempfile
import unittest


def packet(port, payload, protocol=17):
    ethernet = bytes(12) + b"\x08\x00"
    ip = bytearray(20)
    ip[0], ip[9] = 0x45, protocol
    ip[12:16] = bytes([10, 0, 2, 15])
    return ethernet + ip + struct.pack("!HHHH", 53000, port, len(payload) + 8, 0) + payload


def capture(leak=None, baseline=True, rounds=3):
    frames = [packet(53, b"baseline-test")] if baseline else []
    for _ in range(rounds):
        for stage in ("protection-installed", "lookup-cancelled-policy-still-active",
                      "post-hold-policy-still-active", "dns-restored-policy-still-active",
                      "before-protection-remove", "after-protection-remove"):
            frames.append(packet(49495, b"irvio-dns-marker-" + stage.encode()))
            if stage == "dns-restored-policy-still-active" and leak:
                frames.append(packet(53, b"leaked", leak))
        frames.append(packet(53, b"ordinary-after-disconnect"))
    result = struct.pack("<IHHIIII", 0xA1B2C3D4, 2, 4, 0, 0, 65535, 1)
    for n, frame in enumerate(frames):
        result += struct.pack("<IIII", n, 0, len(frame), len(frame)) + frame
    return result


class CaptureOracle(unittest.TestCase):
    def check(self, data, success):
        with tempfile.TemporaryDirectory() as directory:
            source = pathlib.Path(directory) / "synthetic.pcap"
            source.write_bytes(data)
            result = subprocess.run([sys.executable, str(pathlib.Path(__file__).with_name("check-dns-capture.py")), str(source)], capture_output=True)
            self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)

    def test_clean(self):
        self.check(capture(), True)

    def test_udp_leak(self):
        self.check(capture(leak=17), False)

    def test_tcp_leak(self):
        self.check(capture(leak=6), False)

    def test_missing_positive_control(self):
        self.check(capture(baseline=False), False)

    def test_missing_repeat(self):
        self.check(capture(rounds=2), False)

    def test_truncated(self):
        self.check(capture()[:-1], False)


if __name__ == "__main__":
    unittest.main()
