# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

import json
import socket
import time

started = time.monotonic()
with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
    s.setsockopt(socket.IPPROTO_IP, 10, 2)
    s.settimeout(12)
    s.connect(('203.0.113.79', 33435))
    before = s.getsockopt(socket.IPPROTO_IP, 14)
    assert before > 1000, 'preexisting PMTU would invalidate the experiment'
    s.sendall(b'T' * 8192)
    reply = b''
    while not reply.endswith(b'\n'):
        chunk = s.recv(100)
        assert chunk, 'connection closed before the data proof'
        reply += chunk
        assert len(reply) <= 100
    assert reply == b'TCP_PMTU_PASS\n', 'oracle did not acknowledge all 8192 bytes'
    after = s.getsockopt(socket.IPPROTO_IP, 14)
    assert after == 1000, 'TCP did not learn the router PMTU'
    print(json.dumps({'result': 'PRODUCT_TCP_PMTU_PASS', 'before_mtu': before,
                      'after_mtu': after, 'acknowledged_bytes': 8192,
                      'elapsed_ms': round((time.monotonic() - started) * 1000, 2)}))
