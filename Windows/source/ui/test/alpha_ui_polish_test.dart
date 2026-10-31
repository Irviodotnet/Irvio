// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/app/irvio_app.dart';
import 'package:irvio/app/irvio_app_controller.dart';
import 'package:irvio/app/linux_desktop_integration.dart';
import 'package:irvio/app/linux_autostart.dart';
import 'package:irvio/app/linux_notifications.dart';
import 'package:irvio/l10n/irvio_localizations.dart';
import 'package:irvio/models/irvio_models.dart';
import 'package:irvio/main.dart' show irvioInitialWindowSize;
import 'package:irvio/widgets/connection_control.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fake_irvio_client.dart';

class _RecordingAutostart implements IrvioAutostart {
  bool enabled = false;
  int writes = 0;

  @override
  Future<bool> isEnabled() async => enabled;

  @override
  Future<void> setEnabled(bool value) async {
    writes++;
    enabled = value;
  }
}

class _RecordingNotifications implements IrvioNotificationSink {
  final List<({String title, String body})> messages = [];

  @override
  Future<void> show({required String title, required String body}) async {
    messages.add((title: title, body: body));
  }
}

void main() {
  setUpAll(() async {
    // Use proportional fonts for layout tests; Ahem glyphs cause artificial wrapping.
    final packageConfig = File('.dart_tool/package_config.json').absolute;
    final config = jsonDecode(await packageConfig.readAsString()) as Map;
    final flutterPackage = (config['packages'] as List).firstWhere(
      (package) => package['name'] == 'flutter',
    ) as Map;
    final sdk = Directory.fromUri(
      packageConfig.uri.resolve(flutterPackage['rootUri'] as String),
    ).parent.parent;
    final bytes = await File(
      '${sdk.path}/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf',
    ).readAsBytes();
    for (final family in ['sans', 'Roboto']) {
      final loader = FontLoader(family)
        ..addFont(Future.value(ByteData.sublistView(bytes)));
      await loader.load();
    }
  });

  setUp(() => SharedPreferences.setMockInitialValues({}));

  test(
    'all supported languages are complete and unknown locales use English',
    () {
      for (final language in IrvioLanguage.values) {
        expect(
          IrvioLocalizations.forLanguage(language).missingTranslationKeys,
          isEmpty,
          reason: 'Missing ${language.code} translations',
        );
      }
      expect(IrvioLanguage.fromCode('fr'), IrvioLanguage.english);
      expect(
        IrvioLocalizations.forLanguage(IrvioLanguage.fromCode('fr'))
            .text('settings'),
        'Settings',
      );
    },
  );

  test('connection states map to restrained ambient glow categories', () {
    expect(
      connectionGlowForState(IrvioConnectionState.disconnected),
      ConnectionGlowCategory.none,
    );
    expect(
      connectionGlowForState(IrvioConnectionState.connecting),
      ConnectionGlowCategory.blue,
    );
    expect(
      connectionGlowForState(IrvioConnectionState.connected),
      ConnectionGlowCategory.green,
    );
    expect(
      connectionGlowForState(IrvioConnectionState.hosting),
      ConnectionGlowCategory.blue,
    );
    expect(
      connectionGlowForState(IrvioConnectionState.recovering),
      ConnectionGlowCategory.amber,
    );
    expect(
      connectionGlowForState(IrvioConnectionState.disconnecting),
      ConnectionGlowCategory.amber,
    );
    for (final state in [
      IrvioConnectionState.noNetwork,
      IrvioConnectionState.noExit,
      IrvioConnectionState.error,
    ]) {
      expect(connectionGlowForState(state), ConnectionGlowCategory.red);
    }
  });

  test('tray menu snapshot ignores view-only controller changes', () async {
    final privateController = IrvioAppController(() async => FakeIrvioClient());
    privateController.status = const IrvioStatus(
      state: IrvioConnectionState.connected,
      privateTargetPeerId: 'private-peer',
      privateHostingWanted: true,
    );
    expect(trayStatusLabel(privateController), 'Connected — Private VPN');
    final privateSignature = trayMenuSignature(privateController);
    privateController.status = const IrvioStatus(
      state: IrvioConnectionState.connected,
      privateTargetPeerId: 'other-private-peer',
    );
    expect(trayMenuSignature(privateController), isNot(privateSignature));
    privateController.dispose();
    final controller = IrvioAppController(() async => FakeIrvioClient());
    await controller.start();
    final initial = trayMenuSignature(controller);
    controller.selectPage(IrvioPage.about);
    controller.selectTheme(ThemeMode.light);
    expect(trayMenuSignature(controller), initial);
    controller.dispose();
  });

  test('background log events do not rebuild the home screen', () async {
    final statusClient = FakeIrvioClient();
    final logClient = FakeIrvioClient();
    final clients = [statusClient, logClient];
    final controller = IrvioAppController(() async => clients.removeAt(0));
    await controller.start();
    await Future<void>.delayed(Duration.zero);
    var notifications = 0;
    controller.addListener(() => notifications++);
    logClient.emitLog(
      IrvioLogRecord(
        sequence: 1,
        timestamp: DateTime.utc(2026),
        severity: IrvioLogSeverity.info,
        message: 'routine event',
      ),
    );
    await Future<void>.delayed(Duration.zero);
    expect(controller.logs, hasLength(1));
    expect(notifications, 0);
    controller.selectPage(IrvioPage.logs);
    notifications = 0;
    logClient.emitLog(
      IrvioLogRecord(
        sequence: 2,
        timestamp: DateTime.utc(2026),
        severity: IrvioLogSeverity.info,
        message: 'visible event',
      ),
    );
    await Future<void>.delayed(Duration.zero);
    expect(notifications, 1);
    controller.dispose();
  });

  test(
    'XDG autostart creates and removes only the managed IRVIO entry',
    () async {
      final root = await Directory.systemTemp.createTemp(
        'irvio-autostart-test-',
      );
      addTearDown(() => root.delete(recursive: true));
      final configHome = '${root.path}/config';
      final autostart = LinuxXdgAutostart(
        configHome: configHome,
        executablePath: '/opt/Irvio UI/irvio_ui',
      );
      final unrelated = File('$configHome/autostart/other.desktop');
      await unrelated.parent.create(recursive: true);
      await unrelated.writeAsString('[Desktop Entry]\nName=Other\n');

      await autostart.setEnabled(true);

      final entry = File(
        '$configHome/autostart/${LinuxXdgAutostart.entryName}',
      );
      expect(await autostart.isEnabled(), isTrue);
      expect(await entry.exists(), isTrue);
      final contents = await entry.readAsString();
      expect(contents, contains('X-IRVIO-Managed=true'));
      expect(contents, contains('Name=IRVIO'));
      expect(contents, contains('Exec="/opt/Irvio UI/irvio_ui"'));

      await autostart.setEnabled(false);

      expect(await entry.exists(), isFalse);
      expect(await unrelated.exists(), isTrue);

      await entry.writeAsString('[Desktop Entry]\nName=Foreign\n');
      await autostart.setEnabled(false);
      expect(await entry.exists(), isTrue);
      await expectLater(
        autostart.setEnabled(true),
        throwsA(isA<FileSystemException>()),
      );
    },
  );

  test(
    'legacy IRVIO autostart is visible and removed when switched off',
    () async {
      final root = await Directory.systemTemp.createTemp(
        'irvio-autostart-legacy-',
      );
      addTearDown(() => root.delete(recursive: true));
      final configHome = '${root.path}/config';
      final autostart = LinuxXdgAutostart(
        configHome: configHome,
        executablePath: '/opt/irvio/ui/irvio',
      );
      final directory = Directory('$configHome/autostart');
      await directory.create(recursive: true);
      final manual = File('${directory.path}/irvio.desktop');
      await manual.writeAsString(
        '[Desktop Entry]\nType=Application\nName=IRVIO\n'
        'Exec=/opt/irvio/ui/irvio\nX-GNOME-Autostart-enabled=true\n',
      );
      expect(await autostart.isEnabled(), isTrue);
      final legacy = File('${directory.path}/hydra.desktop');
      await legacy.writeAsString(
        '[Desktop Entry]\nName=IRVIO\nExec=/opt/irvio/ui/irvio\n'
        'X-HYDRA-Managed=true\n',
      );

      expect(await autostart.isEnabled(), isTrue);
      await autostart.setEnabled(false);
      expect(await manual.exists(), isFalse);
      expect(await legacy.exists(), isFalse);
      expect(await autostart.isEnabled(), isFalse);
    },
  );

  test(
    'autostart and notification preferences persist through the controller',
    () async {
      final autostart = _RecordingAutostart();
      final controller = IrvioAppController(
        () async => FakeIrvioClient(apiMinor: 0),
        autostart: autostart,
      );
      await controller.start();

      await controller.setStartAtLogin(true);
      controller.setNotificationsEnabled(true);
      await Future<void>.delayed(Duration.zero);

      expect(controller.startAtLogin, isTrue);
      expect(autostart.writes, 1);
      expect(
        (await SharedPreferences.getInstance()).getString('notifications'),
        'true',
      );
      controller.dispose();

      final restarted = IrvioAppController(
        () async => FakeIrvioClient(apiMinor: 0),
        autostart: autostart,
      );
      await restarted.start();
      expect(restarted.startAtLogin, isTrue);
      expect(restarted.notificationsEnabled, isTrue);
      restarted.dispose();
    },
  );

  test(
    'notifications emit once for meaningful transitions and respect OFF',
    () async {
      SharedPreferences.setMockInitialValues({'notifications': 'true'});
      final client = FakeIrvioClient(apiMinor: 0);
      final notifications = _RecordingNotifications();
      final controller = IrvioAppController(
        () async => client,
        notifications: notifications,
      );
      await controller.start();

      client.emit(
        const IrvioStatus(
          state: IrvioConnectionState.connected,
          countryCode: 'NL',
          exitCountryCode: 'NL',
        ),
      );
      await Future<void>.delayed(Duration.zero);
      expect(notifications.messages, hasLength(1));
      expect(notifications.messages.single.title, 'IRVIO');
      expect(notifications.messages.single.body, 'Connected to Netherlands');

      client.emit(
        const IrvioStatus(
          state: IrvioConnectionState.connected,
          countryCode: 'NL',
          exitCountryCode: 'NL',
        ),
        sequence: 2,
      );
      client.emit(
        const IrvioStatus(
          state: IrvioConnectionState.connecting,
          countryCode: 'NL',
        ),
        sequence: 3,
      );
      await Future<void>.delayed(Duration.zero);
      expect(notifications.messages, hasLength(1));

      controller.setNotificationsEnabled(false);
      client.emit(
        const IrvioStatus(
          state: IrvioConnectionState.recovering,
          countryCode: 'NL',
        ),
        sequence: 4,
      );
      await Future<void>.delayed(Duration.zero);
      expect(notifications.messages, hasLength(1));
      controller.dispose();
    },
  );

  for (final caseValue in [
    (
      language: IrvioLanguage.english,
      settings: 'Settings',
      general: 'GENERAL',
      notifications: 'Show notifications',
    ),
    (
      language: IrvioLanguage.russian,
      settings: 'Настройки',
      general: 'ОСНОВНЫЕ',
      notifications: 'Показывать уведомления',
    ),
    (
      language: IrvioLanguage.german,
      settings: 'Einstellungen',
      general: 'ALLGEMEIN',
      notifications: 'Benachrichtigungen anzeigen',
    ),
  ]) {
    testWidgets('Settings renders completely in ${caseValue.language.code}', (
      tester,
    ) async {
      SharedPreferences.setMockInitialValues({
        'language': caseValue.language.code,
      });
      await tester.binding.setSurfaceSize(irvioInitialWindowSize);
      final controller = IrvioAppController(
        () async => FakeIrvioClient(),
        autostart: _RecordingAutostart(),
      );
      await controller.start();
      controller.selectPage(IrvioPage.settings);
      await tester.pumpWidget(IrvioApp(controller: controller));
      await tester.pump();
      await tester.pump();

      expect(find.text(caseValue.settings), findsWidgets);
      expect(find.text(caseValue.general), findsOneWidget);
      expect(find.text(caseValue.notifications), findsOneWidget);
      expect(find.byKey(const Key('start-at-login-toggle')), findsOneWidget);
      expect(find.byKey(const Key('notifications-toggle')), findsOneWidget);
      expect(find.byKey(const Key('language-selector')), findsOneWidget);
      expect(find.byKey(const Key('theme-selector')), findsOneWidget);
      final settingsScroll = tester.state<ScrollableState>(
        find.byType(Scrollable).last,
      );
      expect(
        settingsScroll.position.maxScrollExtent,
        0,
        reason:
            'Viewport ${tester.getSize(find.byType(ListView))}; '
            'delete ${tester.getRect(find.byKey(const Key('delete-profile-button')))}',
      );
      expect(find.byKey(const Key('delete-profile-button')), findsOneWidget);
      expect(
        find.byKey(const Key('delete-profile-button')).hitTestable(),
        findsOneWidget,
      );
      expect(find.textContaining('Coming later'), findsNothing);
      expect(
        find.textContaining('Not available in this version'),
        findsNothing,
      );
      controller.dispose();
    });
  }
}
