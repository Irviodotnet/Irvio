# IRVIO Project
# Copyright (C) 2026 IRVIO Project contributors
# SPDX-License-Identifier: AGPL-3.0-only
# https://irvio.net
# irvio@proton.me

import json
import socketserver
import threading
import time


def record(protocol, peer, payload):
    print(json.dumps({"time_ns": time.time_ns(), "protocol": protocol,
                      "peer": peer, "token": payload.decode("ascii").strip()}), flush=True)


class TCP(socketserver.StreamRequestHandler):
    def handle(self):
        self.connection.settimeout(60)
        while True:
            payload = self.rfile.readline(512)
            if not payload or not payload.startswith(b"irvio-nat-wfp-"):
                return
            record("tcp", self.client_address, payload)
            self.wfile.write(payload)
            self.wfile.flush()


class UDP(socketserver.BaseRequestHandler):
    def handle(self):
        payload, sock = self.request
        if len(payload) <= 512 and payload.startswith(b"irvio-nat-wfp-"):
            record("udp", self.client_address, payload)
            sock.sendto(payload, self.client_address)


class TCPServer(socketserver.ThreadingTCPServer):
    daemon_threads = True


if __name__ == "__main__":
    with TCPServer(("192.168.23.2", 49494), TCP) as tcp, socketserver.UDPServer(("192.168.23.2", 49495), UDP) as udp:
        threading.Thread(target=tcp.serve_forever, daemon=True).start()
        print("W05 echo oracle ready", flush=True)
        udp.serve_forever()
