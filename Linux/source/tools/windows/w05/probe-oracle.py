# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

import json
import socketserver
import threading


class Probe(socketserver.BaseRequestHandler):
    def handle(self):
        self.request.settimeout(5)
        payload = b''
        while len(payload) < 32:
            part = self.request.recv(32 - len(payload))
            if not part:
                break
            payload += part
        port = self.server.server_address[1]
        if not payload:
            print(json.dumps({'port': port, 'kind': 'candidate'}), flush=True)
            return
        if len(payload) != 32 or payload[:4] != b'HYU1':
            raise ValueError('Unexpected challenge')
        print(json.dumps({'port': port, 'kind': 'nonce'}), flush=True)
        if port == 49498:
            while self.request.recv(1):
                pass
            return
        if port == 49497:
            payload = payload[:-1] + bytes([payload[-1] ^ 1])
        self.request.sendall(payload)


class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


if __name__ == '__main__':
    servers = [Server(('192.168.23.2', port), Probe) for port in (49496, 49497, 49498, *range(49500, 49505))]
    for server in servers:
        threading.Thread(target=server.serve_forever, daemon=True).start()
    print('PROBE_ORACLE_READY', flush=True)
    threading.Event().wait()
