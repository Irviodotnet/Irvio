// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/irvio/process_local_api_transport.dart';

void main() {
  test(
    'process transport relays bytes and drains stderr without buffering',
    () async {
      final process = _BridgeProcess();
      final transport = ProcessLocalApiTransport(process);
      final response = transport.bytes.first;
      process.output.add([0, 255, 128, 1]);
      expect(await response, [0, 255, 128, 1]);
      await transport.write(Uint8List.fromList([0, 128, 255]));
      expect(process.writes, [
        [0, 128, 255],
      ]);
      for (var i = 0; i < 100; i++) {
        process.errors.add(List.filled(4096, 42));
      }
      await transport.close();
      await transport.close();
      expect(process.kills, 0);
      expect(process.writes, hasLength(1));
      await expectLater(transport.write(Uint8List(1)), throwsStateError);
    },
  );

  test('stalled helper is terminated, not the daemon', () async {
    final process = _BridgeProcess()..exitOnEOF = false;
    final transport = ProcessLocalApiTransport(process);
    await transport.close();
    expect(process.kills, 1);
    expect(process.writes, isEmpty);
    await transport.close();
    expect(process.kills, 1);
  });

  test('unexpected process EOF reaches the common client', () async {
    final process = _BridgeProcess();
    final transport = ProcessLocalApiTransport(process);
    final received = transport.bytes.toList();
    process.finish(1);
    expect(await received, isEmpty);
    await transport.close();
    expect(process.kills, 0);
  });
}

class _BridgeProcess implements Process {
  _BridgeProcess() {
    input.stream.listen(
      writes.add,
      onDone: () {
        if (exitOnEOF) finish(0);
      },
    );
    stdin = IOSink(input.sink);
  }

  final input = StreamController<List<int>>();
  final output = StreamController<List<int>>();
  final errors = StreamController<List<int>>();
  final done = Completer<int>();
  final writes = <List<int>>[];
  bool exitOnEOF = true;
  int kills = 0;

  @override
  late final IOSink stdin;
  @override
  Stream<List<int>> get stdout => output.stream;
  @override
  Stream<List<int>> get stderr => errors.stream;
  @override
  Future<int> get exitCode => done.future;
  @override
  int get pid => 123;

  void finish(int code) {
    if (done.isCompleted) return;
    done.complete(code);
    unawaited(output.close());
    unawaited(errors.close());
  }

  @override
  bool kill([ProcessSignal signal = ProcessSignal.sigterm]) {
    kills++;
    finish(1);
    return true;
  }
}
