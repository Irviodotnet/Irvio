# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

import json
import socket
import socketserver
import time


class Probe(socketserver.StreamRequestHandler):
    def handle(self):
        self.connection.settimeout(5)
        request = json.loads(self.rfile.readline(512))
        stage = request['stage']
        if not isinstance(stage, str) or not stage.isascii() or not stage.replace('-', '').isalnum() or len(stage) > 80:
            raise ValueError('Invalid probe stage')
        payload = ('irvio-incoming-' + stage + '\n').encode('ascii')
        ok = False
        detail = 'incomplete reply'
        started = time.monotonic_ns()
        try:
            with socket.create_connection(('192.168.23.23', 49498), timeout=1.5) as conn:
                conn.settimeout(1.5)
                conn.sendall(payload)
                received = b''
                while len(received) < len(payload):
                    part = conn.recv(len(payload) - len(received))
                    if not part:
                        break
                    received += part
                ok = received == payload
                if ok:
                    conn.sendall(b'ack\n')
                    detail = 'received full echo'
        except OSError as error:
            detail = type(error).__name__ + ': ' + str(error)
        result = {'stage': stage, 'ok': ok, 'time_ns': time.time_ns(), 'elapsed_ms': (time.monotonic_ns()-started)/1000000, 'detail': detail}
        print(json.dumps(result), flush=True)
        self.wfile.write((json.dumps(result) + '\n').encode('ascii'))


class Server(socketserver.ThreadingTCPServer):
    daemon_threads = True
    allow_reuse_address = True


if __name__ == '__main__':
    with Server(('192.168.23.2', 49497), Probe) as server:
        print('INCOMING_ORACLE_READY', flush=True)
        server.serve_forever()
