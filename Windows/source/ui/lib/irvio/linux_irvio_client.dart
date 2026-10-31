// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:io';
import 'dart:typed_data';

import 'local_api_transport.dart';
import 'local_irvio_client.dart';

export 'local_irvio_client.dart' show maxLocalApiFrame, decodeLocalApiChunks;

const defaultIrvioSocketPath = '/run/irvio/control.sock';

abstract final class LinuxIrvioClient {
  static Future<LocalIrvioClient> connectTo({
    String socketPath = defaultIrvioSocketPath,
    Duration timeout = const Duration(seconds: 5),
    Duration requestTimeout = const Duration(seconds: 5),
    Duration disconnectTimeout = const Duration(seconds: 65),
  }) async {
    final address = InternetAddress(socketPath, type: InternetAddressType.unix);
    final socket = await Socket.connect(address, 0, timeout: timeout);
    return LocalIrvioClient.connectTransport(
      _LinuxSocketTransport(socket),
      requestTimeout: requestTimeout,
      disconnectTimeout: disconnectTimeout,
    );
  }
}

class _LinuxSocketTransport implements LocalApiTransport {
  _LinuxSocketTransport(this.socket);
  final Socket socket;
  Future<void>? _closing;

  @override
  Stream<Uint8List> get bytes => socket;

  @override
  Future<void> write(Uint8List frame) async {
    socket.add(frame);
    await socket.flush();
  }

  @override
  Future<void> close() => _closing ??= socket.close().then<void>((_) {});
}
