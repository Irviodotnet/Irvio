// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/services.dart';

import 'linux_autostart.dart';
import 'linux_notifications.dart';

const windowsDesktopChannel = MethodChannel('irvio/windows_desktop');

class WindowsAutostart implements IrvioAutostart {
  const WindowsAutostart();

  @override
  Future<bool> isEnabled() async =>
      await windowsDesktopChannel.invokeMethod<bool>('autostartGet') ?? false;

  @override
  Future<void> setEnabled(bool enabled) async {
    await windowsDesktopChannel.invokeMethod<void>('autostartSet', enabled);
  }
}

class WindowsNotificationSink implements IrvioNotificationSink {
  const WindowsNotificationSink();

  @override
  Future<void> show({required String title, required String body}) async {
    try {
      await windowsDesktopChannel.invokeMethod<bool>('notify', {
        'title': title,
        'body': body,
      });
    } on PlatformException {
      // Notifications remain best effort and never gate VPN state.
    } on MissingPluginException {
      // The window may already be closing.
    }
  }
}
