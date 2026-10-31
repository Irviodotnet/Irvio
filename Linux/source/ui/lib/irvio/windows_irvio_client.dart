// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:io';

import 'local_irvio_client.dart';
import 'process_local_api_transport.dart';

abstract final class WindowsIrvioClient {
  static Future<LocalIrvioClient> connectTo() async {
    if (!Platform.isWindows) {
      throw UnsupportedError('Windows Local API transport requires Windows');
    }
    final directory = File(Platform.resolvedExecutable).parent;
    final bridge = File('${directory.path}\\irvio-ui-bridge.exe');
    final process = await Process.start(
      bridge.path,
      const [],
      workingDirectory: directory.path,
      runInShell: false,
    );
    return LocalIrvioClient.connectTransport(
      ProcessLocalApiTransport(process),
      handshakeTimeout: const Duration(seconds: 35),
    );
  }
}
