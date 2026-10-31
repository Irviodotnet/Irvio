# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

import json
import socket
import threading
import time

deadline = time.monotonic() + 60


def send(kind, port):
    sequence = 0
    while time.monotonic() < deadline:
        sequence += 1
        ok = False
        with socket.socket(socket.AF_INET, kind) as sock:
            sock.settimeout(0.5)
            try:
                if kind == socket.SOCK_DGRAM:
                    token = f'irvio-inbound-{sequence}'.encode()
                    sock.sendto(token, ('192.168.23.23', port))
                    reply, _ = sock.recvfrom(128)
                    ok = reply == token
                else:
                    sock.connect(('192.168.23.23', port))
                    ok = sock.recv(128) == b'IRVIO-INBOUND-OK\n'
                    if ok:
                        sock.sendall(b'OK')
            except OSError:
                pass
        print(json.dumps({'port': port, 'sequence': sequence, 'reply': ok}), flush=True)
        time.sleep(0.15)


if __name__ == '__main__':
    threads = [threading.Thread(target=send, args=(socket.SOCK_DGRAM, 49507)), threading.Thread(target=send, args=(socket.SOCK_STREAM, 49508))]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
