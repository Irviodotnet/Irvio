# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

import json
import socket
import socketserver
import struct
import threading


class Datagram(socketserver.BaseRequestHandler):
    def handle(self):
        payload, sock = self.request
        event = {'port': self.server.server_address[1], 'source': self.client_address}
        if len(payload) >= 20 and payload[:2] == b'\x00\x01' and payload[4:8] == b'\x21\x12\xa4\x42':
            event['kind'] = 'stun'
            address = struct.unpack('!I', socket.inet_aton(self.client_address[0]))[0] ^ 0x2112a442
            attribute = struct.pack('!HHBBHI', 0x20, 8, 0, 1, self.client_address[1] ^ 0x2112, address)
            response = b'\x01\x01' + struct.pack('!H', len(attribute)) + payload[4:20] + attribute
        else:
            event['kind'] = 'echo'
            event['token'] = payload.decode('ascii')
            response = payload
        print(json.dumps(event), flush=True)
        sock.sendto(response, self.client_address)


if __name__ == '__main__':
    servers = [socketserver.UDPServer(('192.168.23.2', port), Datagram) for port in (49505, 49506)]
    for server in servers:
        threading.Thread(target=server.serve_forever, daemon=True).start()
    print('DATAGRAM_ORACLE_READY', flush=True)
    threading.Event().wait()
