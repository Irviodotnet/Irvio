// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:io';

import 'package:irvio/irvio/windows_irvio_client.dart';

Future<void> main() async {
  try {
    final client = await WindowsIrvioClient.connectTo();
    try {
      final status = await client.getStatus();
      stdout.writeln(
        'API=1.${client.hello.negotiatedMinor} state=${status.state.name}',
      );
    } finally {
      await client.close();
    }
  } on Object catch (error) {
    stderr.writeln('Windows Local API smoke failed: $error');
    exitCode = 1;
  }
}
