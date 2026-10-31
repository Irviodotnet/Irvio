// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:io';

abstract interface class IrvioNotificationSink {
  Future<void> show({required String title, required String body});
}

class NoopIrvioNotificationSink implements IrvioNotificationSink {
  const NoopIrvioNotificationSink();

  @override
  Future<void> show({required String title, required String body}) async {}
}

class LinuxDesktopNotificationSink implements IrvioNotificationSink {
  const LinuxDesktopNotificationSink();

  @override
  Future<void> show({required String title, required String body}) async {
    try {
      await Process.run('notify-send', [
        '--app-name=IRVIO',
        '--icon=network-vpn',
        '--urgency=normal',
        '--expire-time=6000',
        title,
        body,
      ]);
    } on ProcessException {
      // Desktop notifications are best effort and never affect the GUI or VPN.
    }
  }
}
