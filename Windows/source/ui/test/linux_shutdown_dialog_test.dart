// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/app/irvio_app_controller.dart';
import 'package:irvio/app/linux_desktop_integration.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:tray_manager/tray_manager.dart';

import 'fake_irvio_client.dart';

class DialogController extends IrvioAppController {
  DialogController() : super(() async => FakeIrvioClient());
  int shutdownCalls = 0;

  @override
  Future<bool> shutdownIrvio() async {
    shutdownCalls++;
    return true;
  }
}

class DialogDesktop extends LinuxDesktopIntegration {
  DialogDesktop(super.controller, {super.manualTrayMenu});
  int exits = 0;
  int shows = 0;
  int menus = 0;

  @override
  Future<void> showTrayMenu() async {
    menus++;
  }

  @override
  Future<void> show({IrvioPage? page}) async {
    shows++;
  }

  @override
  Future<void> quitUi() async {
    exits++;
  }
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('Windows right-click explicitly opens the tray menu; Linux keeps native handling', () async {
    final controller = DialogController();
    addTearDown(controller.dispose);
    final windows = DialogDesktop(controller, manualTrayMenu: true);
    final linux = DialogDesktop(controller);
    windows.onTrayIconRightMouseDown();
    linux.onTrayIconRightMouseDown();
    expect(windows.menus, 1);
    expect(linux.menus, 0);
    expect(controller.shutdownCalls, 0);
  });

  testWidgets('close guard survives the dialog dismissal animation', (
    tester,
  ) async {
    final controller = DialogController();
    final desktop = DialogDesktop(controller);
    addTearDown(controller.dispose);
    await tester.pumpWidget(
      MaterialApp(
        navigatorKey: desktop.navigatorKey,
        home: const Scaffold(body: Text('alive')),
      ),
    );
    desktop.onWindowClose();
    await tester.pumpAndSettle();
    expect(desktop.shows, 1);
    await tester.tap(
      find.widgetWithText(TextButton, controller.strings.text('cancel')),
    );
    await tester.pump();
    desktop.onWindowClose();
    desktop.onTrayMenuItemClick(MenuItem(key: 'shutdown', label: 'Shutdown'));
    await tester.pump();
    expect(
      desktop.shows,
      1,
      reason:
          'do not present the native window while its old dialog is fading out',
    );
    await tester.pumpAndSettle();
    expect(find.byType(AlertDialog), findsNothing);
    expect(find.text('alive'), findsOneWidget);
    expect(controller.shutdownCalls, 0);
    desktop.onWindowClose();
    await tester.pumpAndSettle();
    expect(desktop.shows, 2);
    expect(find.byType(AlertDialog), findsOneWidget);
    await tester.tap(
      find.widgetWithText(TextButton, controller.strings.text('cancel')),
    );
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
  });

  for (final fromTray in [false, true]) {
    testWidgets(
      'confirmed ${fromTray ? 'tray' : 'window'} dialog shuts down the real controller',
      (tester) async {
        final fake = FakeIrvioClient(apiMinor: 4);
        final controller = IrvioAppController(() async => fake);
        final desktop = DialogDesktop(controller);
        addTearDown(controller.dispose);
        await controller.start();
        await tester.pumpWidget(
          MaterialApp(
            navigatorKey: desktop.navigatorKey,
            home: const Scaffold(body: Text('alive')),
          ),
        );
        if (fromTray) {
          desktop.onTrayMenuItemClick(
            MenuItem(key: 'shutdown', label: 'Shutdown'),
          );
        } else {
          desktop.onWindowClose();
        }
        await tester.pumpAndSettle();
        await tester.tap(
          find.widgetWithText(
            FilledButton,
            controller.strings.text('shutdownIrvio'),
          ),
        );
        await tester.pumpAndSettle();
        expect(fake.shutdownCalls, 1);
        // Subscription cancellation can complete outside the widget clock's microtasks.
        for (var i = 0; i < 20 && desktop.exits == 0; i++) {
          await tester.runAsync(
            () => Future<void>.delayed(const Duration(milliseconds: 10)),
          );
          await tester.pump();
        }
        expect(desktop.exits, 1);
        expect(controller.apiError, isNull);
      },
    );
  }

  testWidgets('rapid cancel/reclose keeps one live shutdown dialog', (
    tester,
  ) async {
    final controller = DialogController();
    final desktop = DialogDesktop(controller);
    addTearDown(controller.dispose);
    await tester.pumpWidget(
      MaterialApp(
        navigatorKey: desktop.navigatorKey,
        home: const Scaffold(body: Text('alive')),
      ),
    );

    for (var i = 0; i < 10; i++) {
      desktop.onWindowClose();
      desktop.onTrayMenuItemClick(MenuItem(key: 'shutdown', label: 'Shutdown'));
      await tester.pumpAndSettle();
      expect(find.byType(AlertDialog), findsOneWidget);
      await tester.tap(find.byType(CheckboxListTile));
      await tester.tap(
        find.widgetWithText(TextButton, controller.strings.text('cancel')),
      );
      await tester.pump();
      expect(controller.skipShutdownConfirmation, isFalse);
      expect(controller.shutdownCalls, 0);
      expect(desktop.exits, 0);
      final shows = desktop.shows;
      desktop.onWindowClose();
      desktop.onTrayMenuItemClick(MenuItem(key: 'shutdown', label: 'Shutdown'));
      await tester.pump();
      expect(desktop.shows, shows);
      await tester.pumpAndSettle();
      expect(find.byType(AlertDialog), findsNothing);
      expect(find.text('alive'), findsOneWidget);
    }
    await tester.pumpAndSettle();
    expect(find.byType(AlertDialog), findsNothing);
    expect(find.text('alive'), findsOneWidget);

    desktop.onTrayMenuItemClick(MenuItem(key: 'shutdown', label: 'Shutdown'));
    await tester.pumpAndSettle();
    await tester.tap(
      find.widgetWithText(
        FilledButton,
        controller.strings.text('shutdownIrvio'),
      ),
    );
    await tester.pumpAndSettle();
    expect(controller.shutdownCalls, 1);
    expect(controller.apiError, isNull);
    expect(desktop.exits, 1);
    expect(tester.takeException(), isNull);
  });
}
