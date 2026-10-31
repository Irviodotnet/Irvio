// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/app/windows_desktop.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  final messenger =
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
  tearDown(
    () => messenger.setMockMethodCallHandler(windowsDesktopChannel, null),
  );

  test(
    'Windows autostart only changes the UI registration on explicit intent',
    () async {
      var enabled = false;
      final calls = <String>[];
      messenger.setMockMethodCallHandler(windowsDesktopChannel, (call) async {
        calls.add(call.method);
        if (call.method == 'autostartGet') return enabled;
        expect(call.method, 'autostartSet');
        enabled = call.arguments as bool;
        return null;
      });
      const autostart = WindowsAutostart();
      expect(await autostart.isEnabled(), false);
      expect(calls, ['autostartGet']);
      await autostart.setEnabled(true);
      expect(await autostart.isEnabled(), true);
      await autostart.setEnabled(false);
      expect(await autostart.isEnabled(), false);
    },
  );

  test(
    'autostart failures are visible instead of silently succeeding',
    () async {
      messenger.setMockMethodCallHandler(windowsDesktopChannel, (_) async {
        throw PlatformException(code: 'autostart_conflict');
      });
      await expectLater(
        const WindowsAutostart().setEnabled(true),
        throwsA(isA<PlatformException>()),
      );
    },
  );

  test('Windows notification is bounded best-effort platform work', () async {
    messenger.setMockMethodCallHandler(windowsDesktopChannel, (call) async {
      expect(call.method, 'notify');
      expect(call.arguments, {'title': 'IRVIO', 'body': 'Подключено'});
      throw PlatformException(code: 'no_tray');
    });
    await const WindowsNotificationSink().show(
      title: 'IRVIO',
      body: 'Подключено',
    );
  });
}
