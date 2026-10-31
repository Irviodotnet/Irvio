// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/irvio/generated/irvio/local/v1/control.pb.dart' as wire;
import 'package:irvio/irvio/local_api_transport.dart';
import 'package:irvio/irvio/local_irvio_client.dart';
import 'package:irvio/models/irvio_models.dart';

void main() {
  test('transport ownership is closed when subscription fails', () async {
    final transport = _UnreadableTransport();
    await expectLater(
      LocalIrvioClient.connectTransport(transport),
      throwsStateError,
    );
    expect(transport.closed, isTrue);
  });

  test(
    'shared client negotiates and reads fragmented status without sockets',
    () async {
      final transport = _MemoryTransport()..fragment = true;
      final client = await LocalIrvioClient.connectTransport(transport);
      addTearDown(client.close);
      expect(client.hello.negotiatedMinor, 7);
      expect(client.hello.supportsPrivateNetwork, isTrue);
      final status = await client.getStatus();
      expect(status.state, IrvioConnectionState.connected);
      expect(status.countryCode, 'NL');
      expect(
        transport.requests.map((e) => e.requestId.toInt()).toSet(),
        hasLength(2),
      );
    },
  );

  test('both VPN modes use the same serialized channel', () async {
    final transport = _MemoryTransport();
    final client = await LocalIrvioClient.connectTransport(transport);
    addTearDown(client.close);
    await Future.wait([
      client.getStatus(),
      client.getPrivateState(),
      client.privateCommand(
        wire.PrivateCommand(
          action: wire.PrivateAction.PRIVATE_ACTION_CONNECT,
          peerId: 'test-peer',
        ),
      ),
    ]);
    await client.connect('NL');
    await client.disconnect();
    expect(transport.requests.skip(1).map((e) => e.request.method), [
      wire.Method.METHOD_GET_STATUS,
      wire.Method.METHOD_GET_PRIVATE_STATE,
      wire.Method.METHOD_PRIVATE_COMMAND,
      wire.Method.METHOD_CONNECT,
      wire.Method.METHOD_DISCONNECT,
    ]);
    expect(transport.peakWrites, 1);
  });

  test('UI channel closure does not issue DISCONNECT or SHUTDOWN', () async {
    final transport = _MemoryTransport();
    final client = await LocalIrvioClient.connectTransport(transport);
    await client.close();
    await client.close();
    expect(transport.closeCount, 1);
    expect(transport.requests.map((e) => e.request.method), [
      wire.Method.METHOD_HELLO,
    ]);
  });

  test('bad negotiation closes transferred transport ownership', () async {
    final transport = _MemoryTransport()..major = 9;
    await expectLater(
      LocalIrvioClient.connectTransport(transport),
      throwsA(isA<IrvioApiException>()),
    );
    expect(transport.closeCount, 1);
  });

  test('EOF fails pending request and closes channel', () async {
    final transport = _MemoryTransport();
    final client = await LocalIrvioClient.connectTransport(transport);
    addTearDown(client.close);
    transport.silent = true;
    final written = Completer<void>();
    transport.onRequest = (_) => written.complete();
    final pending = client.getStatus();
    final expectation = expectLater(pending, throwsA(isA<IrvioApiException>()));
    await written.future;
    await transport.close();
    await expectation;
    expect(transport.closeCount, 1);
  });

  test(
    'EOF during a blocked write fails immediately without unhandled errors',
    () async {
      final transport = _MemoryTransport();
      final client = await LocalIrvioClient.connectTransport(transport);
      addTearDown(client.close);
      final barrier = Completer<void>();
      final entered = Completer<void>();
      transport
        ..silent = true
        ..writeBarrier = barrier.future
        ..onRequest = (_) => entered.complete();
      final failure = expectLater(
        client.getStatus(),
        throwsA(isA<IrvioApiException>()),
      );
      await entered.future;
      try {
        await transport.close();
        await failure.timeout(const Duration(seconds: 2));
      } finally {
        barrier.complete();
      }
      expect(transport.closeCount, 1);
    },
  );

  test('write deadline closes the channel and fences queued CONNECT', () async {
    final transport = _MemoryTransport();
    final client = await LocalIrvioClient.connectTransport(
      transport,
      requestTimeout: const Duration(milliseconds: 100),
    );
    addTearDown(client.close);
    final barrier = Completer<void>();
    final entered = Completer<void>();
    transport
      ..silent = true
      ..writeBarrier = barrier.future
      ..onRequest = (_) => entered.complete();
    final first = expectLater(
      client.getStatus(),
      throwsA(isA<IrvioApiException>()),
    );
    await entered.future;
    final queued = expectLater(
      client.connect('NL'),
      throwsA(isA<IrvioApiException>()),
    );
    try {
      await Future.wait([first, queued]).timeout(const Duration(seconds: 2));
    } finally {
      barrier.complete();
    }
    await Future<void>.delayed(const Duration(milliseconds: 20));
    expect(transport.requests.map((e) => e.request.method), [
      wire.Method.METHOD_HELLO,
      wire.Method.METHOD_GET_STATUS,
    ]);
    expect(transport.closeCount, 1);
    await expectLater(client.getStatus(), throwsA(isA<IrvioApiException>()));
  });

  test('explicit close during write consumes a late write failure', () async {
    final transport = _MemoryTransport();
    final client = await LocalIrvioClient.connectTransport(transport);
    final barrier = Completer<void>();
    final entered = Completer<void>();
    transport
      ..silent = true
      ..writeBarrier = barrier.future
      ..onRequest = (_) => entered.complete();
    final failure = expectLater(
      client.getStatus(),
      throwsA(isA<IrvioApiException>()),
    );
    await entered.future;
    try {
      await client.close();
      await failure.timeout(const Duration(seconds: 2));
    } finally {
      barrier.completeError(StateError('late transport failure'));
    }
    await Future<void>.delayed(const Duration(milliseconds: 20));
    expect(transport.closeCount, 1);
  });

  test(
    'malformed frame fails pending requests without another write',
    () async {
      final transport = _MemoryTransport();
      final client = await LocalIrvioClient.connectTransport(transport);
      addTearDown(client.close);
      transport.silent = true;
      final written = Completer<void>();
      transport.onRequest = (_) => written.complete();
      final failure = expectLater(
        client.getStatus(),
        throwsA(isA<IrvioApiException>()),
      );
      await written.future;
      transport.input.add(Uint8List(4));
      await failure;
      await expectLater(client.getStatus(), throwsA(isA<IrvioApiException>()));
    },
  );
}

class _UnreadableTransport implements LocalApiTransport {
  bool closed = false;
  @override
  Stream<Uint8List> get bytes => throw StateError('subscription unavailable');
  @override
  Future<void> write(Uint8List frame) async =>
      throw StateError('unexpected write');
  @override
  Future<void> close() async {
    closed = true;
  }
}

class _MemoryTransport implements LocalApiTransport {
  final input = StreamController<Uint8List>();
  final requests = <wire.Envelope>[];
  bool fragment = false;
  bool silent = false;
  int major = 1;
  int closeCount = 0;
  int activeWrites = 0;
  int peakWrites = 0;
  void Function(wire.Envelope)? onRequest;
  Future<void>? writeBarrier;
  Future<void>? _closing;

  @override
  Stream<Uint8List> get bytes => input.stream;

  @override
  Future<void> write(Uint8List frame) async {
    activeWrites++;
    if (activeWrites > peakWrites) peakWrites = activeWrites;
    try {
      await Future<void>.delayed(Duration.zero);
      if (_closing != null) throw StateError('transport closed');
      expect(
        ByteData.sublistView(frame).getUint32(0, Endian.big),
        frame.length - 4,
      );
      final request = wire.Envelope.fromBuffer(Uint8List.sublistView(frame, 4));
      requests.add(request);
      onRequest?.call(request);
      if (writeBarrier != null) await writeBarrier;
      if (silent) return;
      final response = wire.Response(
        code: wire.ResultCode.RESULT_CODE_OK,
        accepted: true,
      );
      switch (request.request.method) {
        case wire.Method.METHOD_HELLO:
          response.hello = wire.Hello(
            serverApiMajor: major,
            serverApiMinor: 7,
            negotiatedApiMinor: 7,
            privateNetworkSupported: true,
          );
        case wire.Method.METHOD_GET_STATUS:
          response.status = wire.Status(
            state: wire.ConnectionState.CONNECTION_STATE_CONNECTED,
            countryCode: 'NL',
          );
        case wire.Method.METHOD_GET_PRIVATE_STATE:
          response.privateState = wire.PrivateState(localPeerId: 'local');
        case wire.Method.METHOD_PRIVATE_COMMAND:
          response.privateResult = wire.PrivateCommandResult();
        default:
          break;
      }
      final payload = wire.Envelope(
        apiMajor: 1,
        apiMinor: 7,
        requestId: request.requestId,
        response: response,
      ).writeToBuffer();
      final header = ByteData(4)..setUint32(0, payload.length, Endian.big);
      final bytes = Uint8List.fromList([
        ...header.buffer.asUint8List(),
        ...payload,
      ]);
      if (fragment) {
        for (final byte in bytes) {
          input.add(Uint8List.fromList([byte]));
        }
      } else {
        input.add(bytes);
      }
    } finally {
      activeWrites--;
    }
  }

  @override
  Future<void> close() => _closing ??= _close();

  Future<void> _close() async {
    closeCount++;
    await input.close();
  }
}
