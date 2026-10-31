# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

import errno
import json
import socket
import struct
import time

s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.setsockopt(socket.IPPROTO_IP, 10, 2)
s.setsockopt(socket.IPPROTO_IP, 11, 1)
s.settimeout(3)
s.connect(('203.0.113.77', 33434))
s.send(b'P' * 1172)
deadline = time.monotonic() + 5
seen = False
while time.monotonic() < deadline:
    try:
        data, controls, flags, address = s.recvmsg(2048, 512, socket.MSG_ERRQUEUE)
    except (BlockingIOError, socket.timeout):
        time.sleep(0.05)
        continue
    for level, kind, value in controls:
        if level == socket.IPPROTO_IP and kind == 11:
            error, origin, typ, code, pad, info, detail = struct.unpack('=I4BII', value[:16])
            print(json.dumps({'errno': error, 'origin': origin, 'type': typ, 'code': code, 'mtu': info}), flush=True)
            assert (error, origin, typ, code, info) == (errno.EMSGSIZE, 2, 3, 4, 1000)
            seen = True
    if seen:
        break
assert seen, 'ICMP fragmentation-needed was not delivered to the originating socket'
assert s.getsockopt(socket.IPPROTO_IP, 14) == 1000, 'kernel did not learn PMTU'
try:
    s.send(b'P' * 1172)
except OSError as e:
    assert e.errno == errno.EMSGSIZE
else:
    raise AssertionError('oversized DF datagram not rejected after PMTU update')
small = b'irvio-pmtu-' + b'Q' * 889
s.send(small)
assert s.recv(2048) == small, 'smaller datagram failed after PMTU update'
s.close()
print('PRODUCT_UDP_PMTU_PASS: MTU=1000, oversize rejected, 900-byte UDP payload delivered')
