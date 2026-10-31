// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

@TestOn('linux')
library;

import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/irvio/generated/irvio/local/v1/control.pb.dart' as wire;
import 'package:irvio/irvio/linux_irvio_client.dart';

void main() {
  test('500 bounded Local API socket and command lifecycles', () async {
    final fixture = await _SoakFixture.start();
    addTearDown(fixture.close);

    for (var index = 0; index < 500; index++) {
      final client = await LinuxIrvioClient.connectTo(socketPath: fixture.path);
      expect((await client.getStatus()).countryCode, 'DE');
      if (index < 200) await client.subscribeEvents();
      await client.connect('DE');
      await client.disconnect();
      await client.close();
    }

    expect(fixture.connections, 500);
    expect(fixture.helloRequests, 500);
    expect(fixture.statusRequests, 500);
    expect(fixture.subscriptions, 200);
    expect(fixture.connectRequests, 500);
    expect(fixture.disconnectRequests, 500);
    expect(fixture.peakConnections, lessThanOrEqualTo(2));
  }, timeout: const Timeout(Duration(minutes: 2)));

  test('100 partial and rejected frame cycles stay bounded', () {
    final payload = Uint8List.fromList([8, 1, 16, 0]);
    final header = ByteData(4)..setUint32(0, payload.length, Endian.big);
    final valid = Uint8List.fromList([
      ...header.buffer.asUint8List(),
      ...payload,
    ]);
    final oversized = ByteData(4)
      ..setUint32(0, maxLocalApiFrame + 1, Endian.big);

    for (var index = 0; index < 100; index++) {
      final decoded = decodeLocalApiChunks([
        Uint8List.sublistView(valid, 0, 1),
        Uint8List.sublistView(valid, 1, 3),
        Uint8List.sublistView(valid, 3, 6),
        Uint8List.sublistView(valid, 6),
      ]).toList();
      expect(decoded, hasLength(1));
      expect(decoded.single, payload);
      expect(
        () => decodeLocalApiChunks([oversized.buffer.asUint8List()]).toList(),
        throwsA(isA<Object>()),
      );
    }
  });
}

class _SoakFixture {
  _SoakFixture(this.directory, this.path, this.server);

  final Directory directory;
  final String path;
  final ServerSocket server;
  final sockets = <Socket>{};
  late final StreamSubscription<Socket> _serverSubscription;
  int connections = 0;
  int activeConnections = 0;
  int peakConnections = 0;
  int helloRequests = 0;
  int statusRequests = 0;
  int subscriptions = 0;
  int connectRequests = 0;
  int disconnectRequests = 0;

  static Future<_SoakFixture> start() async {
    final directory = await Directory.systemTemp.createTemp('irvio-ui-soak-');
    final path = '${directory.path}/control.sock';
    final server = await ServerSocket.bind(
      InternetAddress(path, type: InternetAddressType.unix),
      0,
    );
    final fixture = _SoakFixture(directory, path, server);
    fixture._serverSubscription = server.listen(fixture._accept);
    return fixture;
  }

  void _accept(Socket socket) {
    connections++;
    activeConnections++;
    peakConnections = activeConnections > peakConnections
        ? activeConnections
        : peakConnections;
    sockets.add(socket);
    final decoder = _FixtureDecoder();
    var writes = Future<void>.value();
    socket.listen(
      (chunk) {
        for (final frame in decoder.add(chunk)) {
          final envelope = wire.Envelope.fromBuffer(frame);
          final response = _response(envelope.request.method);
          writes = writes.then(
            (_) => _write(
              socket,
              wire.Envelope(
                apiMajor: 1,
                apiMinor: 0,
                requestId: envelope.requestId,
                response: response,
              ),
            ),
          );
        }
      },
      onDone: () {
        sockets.remove(socket);
        activeConnections--;
      },
    );
  }

  wire.Response _response(wire.Method method) {
    switch (method) {
      case wire.Method.METHOD_HELLO:
        helloRequests++;
        return wire.Response(
          code: wire.ResultCode.RESULT_CODE_OK,
          hello: wire.Hello(
            serverApiMajor: 1,
            serverApiMinor: 0,
            negotiatedApiMinor: 0,
            productVersion: 'soak',
            coreVersion: 'v1',
          ),
        );
      case wire.Method.METHOD_GET_STATUS:
        statusRequests++;
        return wire.Response(
          code: wire.ResultCode.RESULT_CODE_OK,
          status: wire.Status(
            state: wire.ConnectionState.CONNECTION_STATE_CONNECTED,
            countryCode: 'DE',
            connection: wire.ConnectionInfo(exitCountryCode: 'DE'),
          ),
        );
      case wire.Method.METHOD_SUBSCRIBE_EVENTS:
        subscriptions++;
      case wire.Method.METHOD_CONNECT:
        connectRequests++;
      case wire.Method.METHOD_DISCONNECT:
        disconnectRequests++;
      default:
    }
    return wire.Response(code: wire.ResultCode.RESULT_CODE_OK, accepted: true);
  }

  Future<void> _write(Socket socket, wire.Envelope envelope) async {
    final payload = envelope.writeToBuffer();
    final header = ByteData(4)..setUint32(0, payload.length, Endian.big);
    socket.add(header.buffer.asUint8List());
    socket.add(payload);
    await socket.flush();
  }

  Future<void> close() async {
    await _serverSubscription.cancel();
    for (final socket in sockets.toList()) {
      await socket.close();
    }
    await server.close();
    if (await directory.exists()) await directory.delete(recursive: true);
  }
}

class _FixtureDecoder {
  final bytes = <int>[];

  Iterable<Uint8List> add(Uint8List chunk) sync* {
    bytes.addAll(chunk);
    while (bytes.length >= 4) {
      final header = Uint8List.fromList(bytes.take(4).toList());
      final length = ByteData.sublistView(header).getUint32(0, Endian.big);
      if (bytes.length < 4 + length) return;
      yield Uint8List.fromList(bytes.sublist(4, 4 + length));
      bytes.removeRange(0, 4 + length);
    }
  }
}
