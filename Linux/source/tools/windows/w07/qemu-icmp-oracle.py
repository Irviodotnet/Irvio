# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

import argparse
import json
import pathlib
import signal
import socket
import struct
import time

p = argparse.ArgumentParser()
p.add_argument('directory')
a = p.parse_args()
root = pathlib.Path(a.directory)
root.mkdir(mode=0o700)
events = (root / 'events.ndjson').open('x', encoding='utf-8')
listeners = []
connections = []
stopped = False
tcp_flows = {}

def tcp_reply(ip):
    if len(ip) < 40:
        raise ValueError('truncated TCP header')
    header = (ip[32] >> 4) * 4
    if header < 20 or 20 + header > len(ip):
        raise ValueError('invalid TCP header')
    pseudo = ip[12:20] + struct.pack('!BBH', 0, 6, len(ip) - 20)
    if checksum(bytes(pseudo + ip[20:])):
        raise ValueError('invalid TCP checksum')
    sport, dport, seq, ack = struct.unpack('!HHII', ip[20:32])
    if dport != 33435:
        return None, None
    flags = ip[33]
    key = (bytes(ip[12:16]), sport)
    if flags & 4:
        tcp_flows.pop(key, None)
        return None, None
    if flags & 2:
        if key not in tcp_flows and len(tcp_flows) >= 16:
            raise ValueError('TCP fixture connection limit')
        tcp_flows[key] = {'next': (seq + 1) & 0xffffffff, 'bytes': 0}
        options, payload, response_flags, response_seq = b'\x02\x04\x05\xb4', b'', 0x12, 100000
    else:
        flow = tcp_flows.get(key)
        if flow is None:
            return None, None
        data = ip[20 + header:]
        if not data and not flags & 1:
            return None, None
        if seq == flow['next']:
            if data != b'T' * len(data) or flow['bytes'] + len(data) > 8192:
                raise ValueError('unexpected TCP fixture payload')
            flow['bytes'] += len(data)
            flow['next'] = (flow['next'] + len(data) + bool(flags & 1)) & 0xffffffff
        options, payload, response_flags, response_seq = b'', b'', 0x10, 100001
        if flow['bytes'] == 8192:
            payload = b'TCP_PMTU_PASS\n'
            response_flags = 0x18
    flow = tcp_flows[key]
    tcp = bytearray(struct.pack('!HHIIBBHHH', dport, sport, response_seq, flow['next'], (5 + len(options) // 4) << 4, response_flags, 65535, 0, 0) + options + payload)
    pseudo = ip[16:20] + ip[12:16] + struct.pack('!BBH', 0, 6, len(tcp))
    tcp[16:18] = struct.pack('!H', checksum(bytes(pseudo + tcp)))
    result = bytearray(20) + tcp
    result[0], result[9] = 0x45, 6
    result[2:4] = struct.pack('!H', len(result))
    result[12:16], result[16:20] = ip[16:20], ip[12:16]
    return result, 'tcp-syn-ack' if flags & 2 else 'tcp-data-ack'

def stop(*_):
    global stopped
    stopped = True
signal.signal(signal.SIGTERM, stop)
signal.signal(signal.SIGINT, stop)

def checksum(data):
    if len(data) % 2:
        data += b'\0'
    total = sum(struct.unpack('!%dH' % (len(data) // 2), data))
    while total >> 16:
        total = (total & 65535) + (total >> 16)
    return (~total) & 65535

def exact(s, size):
    result = bytearray()
    while len(result) < size and not stopped:
        try:
            part = s.recv(size - len(result))
        except socket.timeout:
            continue
        if not part:
            raise EOFError()
        result.extend(part)
    if len(result) != size:
        raise EOFError()
    return bytes(result)

try:
    for name in ('mirror.sock', 'inject.sock'):
        s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        s.bind(str(root / name))
        s.listen(1)
        s.settimeout(1)
        listeners.append(s)
    print('READY', flush=True)
    for s in listeners:
        while not stopped:
            try:
                c, _ = s.accept()
                c.settimeout(1)
                connections.append(c)
                break
            except socket.timeout:
                continue
    mirror, inject = connections
    count = 0
    while not stopped:
        length = struct.unpack('!I', exact(mirror, 4))[0]
        if length > 65549 or length < 14:
            raise ValueError('invalid QEMU Ethernet record size')
        frame = exact(mirror, length)
        if frame[12:14] != b'\x08\x00':
            continue
        ip = bytearray(frame[14:])
        if len(ip) < 28 or ip[0] != 0x45 or ip[9] not in (1, 6, 17):
            continue
        size = struct.unpack('!H', ip[2:4])[0]
        ip = ip[:size]
        target = '203.0.113.79' if ip[9] == 6 else '203.0.113.77'
        if ip[12:16] != socket.inet_aton('10.0.2.15') or ip[16:20] != socket.inet_aton(target):
            continue
        if size != len(ip) or checksum(bytes(ip[:20])) or struct.unpack('!H', ip[6:8])[0] & 0xbfff:
            raise ValueError('invalid isolated echo request')
        kind = 'echo'
        if ip[9] == 1:
            if ip[20:22] != b'\x08\x00' or checksum(bytes(ip[20:])):
                continue
            ip[12:16], ip[16:20] = ip[16:20], ip[12:16]
            ip[20] = 0
            ip[22:24] = b'\0\0'
            ip[22:24] = struct.pack('!H', checksum(bytes(ip[20:])))
        else:
            if ip[22:24] != struct.pack('!H', 33435 if ip[9] == 6 else 33434):
                continue
            if size > 1000:
                if not (ip[6] & 0x40):
                    raise ValueError('PMTU oracle requires DF')
                quote = ip[:28]
                ip = bytearray(28) + quote
                ip[0], ip[9], ip[20], ip[21] = 0x45, 1, 3, 4
                ip[2:4] = struct.pack('!H', len(ip))
                ip[12:16] = socket.inet_aton('203.0.113.78')
                ip[16:20] = quote[12:16]
                ip[26:28] = struct.pack('!H', 1000)
                ip[22:24] = struct.pack('!H', checksum(bytes(ip[20:])))
                kind = 'fragmentation-needed'
            elif ip[9] == 6:
                ip, kind = tcp_reply(ip)
                if ip is None:
                    continue
            else:
                ip[12:16], ip[16:20] = ip[16:20], ip[12:16]
                ip[20:22], ip[22:24] = ip[22:24], ip[20:22]
                ip[26:28] = b'\0\0'
                kind = 'udp-echo'
        ip[8] = 63
        ip[10:12] = b'\0\0'
        ip[10:12] = struct.pack('!H', checksum(bytes(ip[:20])))
        reply = frame[6:12] + frame[:6] + frame[12:14] + ip
        inject.sendall(struct.pack('!I', len(reply)) + reply)
        count += 1
        record = json.dumps({'reply': count, 'kind': kind, 'request_ip_bytes': size, 'monotonic': time.monotonic()})
        print(record, flush=True)
        events.write(record + '\n')
        events.flush()
        if count >= 1000:
            break
except EOFError:
    pass
finally:
    events.close()
    for s in connections + listeners:
        s.close()
    for name in ('mirror.sock', 'inject.sock'):
        path = root / name
        if path.is_socket():
            path.unlink()
