// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'local_api_transport.dart';

class ProcessLocalApiTransport implements LocalApiTransport {
  ProcessLocalApiTransport(this._process) {
    unawaited(_process.stderr.drain<void>().catchError((Object _) {}));
    unawaited(_process.stdin.done.catchError((Object _) {}));
  }

  final Process _process;
  Future<void>? _closing;

  @override
  Stream<Uint8List> get bytes => _process.stdout.map(Uint8List.fromList);

  @override
  Future<void> write(Uint8List frame) async {
    if (_closing != null) throw StateError('Local API transport is closed');
    _process.stdin.add(frame);
    await _process.stdin.flush();
  }

  @override
  Future<void> close() => _closing ??= _close();

  Future<void> _close() async {
    unawaited(_process.stdin.close().catchError((Object _) {}));
    try {
      await _process.exitCode.timeout(const Duration(seconds: 1));
    } on TimeoutException {
      _process.kill();
      await _process.exitCode.timeout(const Duration(seconds: 2));
    }
  }
}
