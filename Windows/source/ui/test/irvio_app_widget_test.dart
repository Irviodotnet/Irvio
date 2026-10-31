// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/app/irvio_app.dart';
import 'package:irvio/app/irvio_app_controller.dart';
import 'package:irvio/irvio/bootstrap_qr.dart';
import 'package:irvio/l10n/irvio_localizations.dart';
import 'package:irvio/models/irvio_models.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fake_irvio_client.dart';

void main() {
  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  Future<(IrvioAppController, FakeIrvioClient)> pumpIrvio(
    WidgetTester tester,
    IrvioStatus status, {
    int apiMinor = 3,
    Completer<void>? disconnectGate,
    IrvioAvailableCountries availableCountries = const IrvioAvailableCountries(
      status: IrvioAvailableCountriesStatus.ready,
      countryCodes: ['DE'],
      generatedAt: null,
    ),
  }) async {
    await tester.binding.setSurfaceSize(const Size(492, 620));
    final fake = FakeIrvioClient(
      apiMinor: apiMinor,
      disconnectGate: disconnectGate,
      currentStatus: status,
      availableCountries: availableCountries,
    );
    final controller = IrvioAppController(() async => fake);
    await controller.start();
    await tester.pumpWidget(IrvioApp(controller: controller));
    await tester.pump();
    await tester.pump();
    return (controller, fake);
  }

  testWidgets('disconnected view sends Connect through IrvioClient', (
    tester,
  ) async {
    final (controller, fake) = await pumpIrvio(
      tester,
      const IrvioStatus.disconnected(),
    );

    expect(find.text('CONNECT'), findsOneWidget);
    expect(find.text('Germany'), findsOneWidget);
    await tester.tap(find.byKey(const Key('primary-connection-action')));
    await tester.pump();

    expect(fake.connectCalls, 1);
    expect(fake.connectedCountry, 'DE');
    controller.dispose();
  });

  testWidgets('IRVIO wordmark and globe asset are visible', (tester) async {
    final (controller, _) = await pumpIrvio(
      tester,
      const IrvioStatus.disconnected(),
    );
    expect(find.text('IRVIO'), findsOneWidget);
    expect(find.byKey(const Key('irvio-logo')), findsOneWidget);
    controller.dispose();
  });

  for (final state in [
    IrvioConnectionState.connected,
    IrvioConnectionState.noExit,
    IrvioConnectionState.recovering,
  ]) {
    testWidgets('private $state keeps Global separate without disconnecting', (
      tester,
    ) async {
      final (controller, fake) = await pumpIrvio(
        tester,
        IrvioStatus(
          state: state,
          privateTargetPeerId: 'pinned-peer',
          privateHostingWanted: true,
        ),
      );
      expect(find.text('pinned-peer'), findsNothing);
      expect(find.byKey(const Key('country-selector')), findsOneWidget);
      expect(find.text('CONNECT'), findsNothing);
      expect(controller.connectionIntentActive, isTrue);
      expect(find.byKey(const Key('primary-connection-action')), findsNothing);
      await tester.tap(find.byKey(const Key('global-open-private')));
      await tester.pump();
      expect(fake.disconnectCalls, 0);
      expect(fake.connectCalls, 0);
      controller.dispose();
    });
  }

  testWidgets(
    'hosting-only has no Global stop action but explicit Disconnect still works',
    (tester) async {
      final gate = Completer<void>();
      final (controller, fake) = await pumpIrvio(
        tester,
        const IrvioStatus(state: IrvioConnectionState.hosting),
        disconnectGate: gate,
      );
      expect(controller.connectionIntentActive, isTrue);
      expect(find.byKey(const Key('global-open-private')), findsOneWidget);
      expect(find.text('CONNECT'), findsNothing);
      expect(find.byKey(const Key('country-selector')), findsOneWidget);
      expect(find.byKey(const Key('primary-connection-action')), findsNothing);
      final disconnect = controller.disconnect();
      await tester.pump();
      expect(fake.disconnectCalls, 1);
      expect(fake.connectCalls, 0);
      expect(controller.disconnectPending, isTrue);
      fake.disconnectGate!.complete();
      await disconnect;
      await tester.pump();
      await tester.pump();
      expect(controller.disconnectPending, isFalse);
      expect(controller.status.state, IrvioConnectionState.hosting);
      fake.emit(const IrvioStatus.disconnected());
      await tester.pump();
      expect(controller.status.state, IrvioConnectionState.disconnected);
      controller.dispose();
    },
  );

  testWidgets('Connect immediately animates while daemon response is pending', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(492, 620));
    final gate = Completer<void>();
    final fake = FakeIrvioClient(connectGate: gate);
    final controller = IrvioAppController(() async => fake);
    await controller.start();
    await tester.pumpWidget(IrvioApp(controller: controller));
    await tester.pump();

    await tester.tap(find.byKey(const Key('primary-connection-action')));
    await tester.pump();
    expect(fake.connectCalls, 1);
    expect(controller.commandPending, isTrue);
    expect(controller.status.state, IrvioConnectionState.disconnected);
    expect(find.text('Connecting…'), findsOneWidget);
    expect(find.text('Establishing a secure connection'), findsOneWidget);

    final icon = tester.widget<Icon>(
      find.byKey(const Key('connection-center-icon')),
    );
    expect(icon.color, const Color(0xFF78B9CE));
    final glow = tester.widget<AnimatedContainer>(
      find.byKey(const Key('connection-glow-blue')),
    );
    final decoration = glow.decoration! as BoxDecoration;
    expect(
      decoration.boxShadow!.single.color,
      icon.color!.withValues(alpha: .16),
    );
    final ring = find
        .descendant(
          of: find.byKey(const Key('connection-glow-blue')),
          matching: find.byType(CustomPaint),
        )
        .first;
    final before = tester.widget<CustomPaint>(ring).painter!;
    await tester.pump(const Duration(seconds: 4));
    expect(
      tester.widget<CustomPaint>(ring).painter!.shouldRepaint(before),
      isTrue,
    );
    expect(find.text('Connecting…'), findsOneWidget);
    await tester.tap(find.byKey(const Key('primary-connection-action')));
    expect(fake.connectCalls, 1);

    // Authoritative success must not be hidden by the pending API reply.
    fake.emit(
      const IrvioStatus(
        state: IrvioConnectionState.connected,
        countryCode: 'DE',
      ),
    );
    await tester.pump();
    expect(find.text('CONNECTED'), findsOneWidget);
    expect(find.byKey(const Key('connection-glow-green')), findsOneWidget);
    gate.complete();
    await tester.pump();
    expect(controller.commandPending, isFalse);
    controller.dispose();
  });

  testWidgets('Rejected Connect removes immediate connecting feedback', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(492, 620));
    final gate = Completer<void>();
    final fake = FakeIrvioClient(connectGate: gate);
    final controller = IrvioAppController(() async => fake);
    await controller.start();
    await tester.pumpWidget(IrvioApp(controller: controller));
    await tester.pump();

    await tester.tap(find.byKey(const Key('primary-connection-action')));
    await tester.pump();
    expect(find.text('Connecting…'), findsOneWidget);
    gate.completeError(StateError('Connect rejected'));
    await tester.pump();
    expect(controller.commandPending, isFalse);
    expect(controller.apiError, isNotNull);
    expect(find.text('Connecting…'), findsNothing);
    expect(find.text('CONNECT'), findsOneWidget);
    expect(controller.status.state, IrvioConnectionState.disconnected);
    controller.dispose();
  });

  testWidgets('Diagnostics omits unreported Network row', (tester) async {
    final (controller, _) = await pumpIrvio(
      tester,
      const IrvioStatus.disconnected(),
    );
    controller.selectPage(IrvioPage.diagnostics);
    await tester.pumpAndSettle();
    expect(find.text('Not reported by API'), findsNothing);
    expect(find.text('Network'), findsNothing);
    expect(find.text('Local API'), findsOneWidget);
    controller.dispose();
  });

  for (final language in IrvioLanguage.values) {
    testWidgets(
      'Diagnostics and Share omit technical footnotes (${language.code})',
      (tester) async {
        final (controller, _) = await pumpIrvio(
          tester,
          const IrvioStatus.disconnected(),
        );
        controller.selectLanguage(language);
        final strings = IrvioLocalizations.forLanguage(language);
        // Removed copy must not survive in another locale or English fallback.
        expect(
          strings.text('diagnosticsUnavailable'),
          'diagnosticsUnavailable',
        );
        expect(strings.text('bootstrapHint'), 'bootstrapHint');
        controller.selectPage(IrvioPage.diagnostics);
        await tester.pumpAndSettle();
        expect(find.text(strings.text('localApi')), findsOneWidget);
        expect(
          find.textContaining(RegExp(r'NAT type|Тип NAT|NAT-Typ')),
          findsNothing,
        );
        controller.selectPage(IrvioPage.share);
        await tester.pumpAndSettle();
        expect(find.byKey(const Key('share-save')), findsOneWidget);
        expect(find.byKey(const Key('share-copy')), findsOneWidget);
        expect(
          find.textContaining(
            RegExp(
              r'introduction hint|подсказкой для знакомства|Hinweis zur Einführung',
            ),
          ),
          findsNothing,
        );
        expect(tester.takeException(), isNull);
        controller.dispose();
      },
    );
  }

  testWidgets('connected CZ survives country list object replacement', (
    tester,
  ) async {
    final (controller, fake) = await pumpIrvio(
      tester,
      const IrvioStatus(
        state: IrvioConnectionState.connected,
        countryCode: 'CZ',
      ),
      availableCountries: const IrvioAvailableCountries(
        status: IrvioAvailableCountriesStatus.ready,
        countryCodes: ['CZ', 'NL'],
        generatedAt: null,
      ),
    );
    expect(tester.takeException(), isNull);
    for (final codes in [
      ['NL', 'CZ'],
      <String>[],
      ['CZ', 'DE'],
    ]) {
      fake.availableCountries = IrvioAvailableCountries(
        status: codes.isEmpty
            ? IrvioAvailableCountriesStatus.empty
            : IrvioAvailableCountriesStatus.ready,
        countryCodes: codes,
        generatedAt: null,
      );
      await controller.refreshAvailableCountries();
      await tester.pump();
      expect(tester.takeException(), isNull);
      expect(find.text('CZ'), findsOneWidget);
      expect(controller.selectedCountry.code, 'CZ');
    }
    expect(fake.connectCalls, 0);
    expect(fake.disconnectCalls, 0);
    controller.dispose();
  });

  testWidgets('backend NL only renders only Netherlands', (tester) async {
    final (controller, _) = await pumpIrvio(
      tester,
      const IrvioStatus.disconnected(),
      availableCountries: const IrvioAvailableCountries(
        status: IrvioAvailableCountriesStatus.ready,
        countryCodes: ['NL'],
        generatedAt: null,
      ),
    );

    expect(find.text('Netherlands'), findsOneWidget);
    expect(find.text('Germany'), findsNothing);
    expect(find.text('France'), findsNothing);
    expect(controller.selectedCountry.code, 'NL');
    controller.dispose();
  });

  testWidgets('open country dropdown gains RU after periodic refresh', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(492, 620));
    var requests = 0;
    final fake = FakeIrvioClient(
      availableCountriesHandler: (refresh) {
        requests++;
        return Future.value(
          IrvioAvailableCountries(
            status: IrvioAvailableCountriesStatus.ready,
            countryCodes: requests == 1 ? const ['NL'] : const ['NL', 'RU'],
            generatedAt: null,
          ),
        );
      },
    );
    final controller = IrvioAppController(
      () async => fake,
      countryRefreshInterval: const Duration(seconds: 1),
    );
    await controller.start();
    await tester.pumpWidget(IrvioApp(controller: controller));
    await tester.pump();
    await tester.pump();
    expect(find.text('Netherlands'), findsOneWidget);
    expect(controller.availableCountries.map((item) => item.code), ['NL']);

    await tester.pump(const Duration(seconds: 1));
    await tester.pump();
    expect(fake.lastAvailableCountriesRefresh, isTrue);
    expect(controller.availableCountries.map((item) => item.code), [
      'NL',
      'RU',
    ]);
    await tester.tap(find.byKey(const Key('country-selector')));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 300));
    expect(find.text('Russia'), findsOneWidget);
    expect(fake.connectCalls, 0);
    expect(fake.disconnectCalls, 0);
    controller.dispose();
  });

  testWidgets('backend RU renders the Russian flag and country name', (
    tester,
  ) async {
    final (controller, _) = await pumpIrvio(
      tester,
      const IrvioStatus.disconnected(),
      availableCountries: const IrvioAvailableCountries(
        status: IrvioAvailableCountriesStatus.ready,
        countryCodes: ['RU'],
        generatedAt: null,
      ),
    );

    expect(find.text('Russia'), findsOneWidget);
    expect(
      find.byWidgetPredicate(
        (widget) =>
            widget is Image &&
            widget.image is AssetImage &&
            (widget.image as AssetImage).assetName == 'assets/flags/ru.png',
      ),
      findsOneWidget,
    );
    expect(find.text('Netherlands'), findsNothing);
    controller.dispose();
  });

  testWidgets('country availability shows loading empty and unavailable', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(492, 620));
    final gate = Completer<void>();
    final fake = FakeIrvioClient(availableCountriesGate: gate);
    final controller = IrvioAppController(() async => fake);
    await controller.start();
    await tester.pumpWidget(IrvioApp(controller: controller));
    await tester.pump();
    expect(find.byKey(const Key('country-loading')), findsOneWidget);

    fake.availableCountries = const IrvioAvailableCountries(
      status: IrvioAvailableCountriesStatus.empty,
      countryCodes: [],
      generatedAt: null,
    );
    gate.complete();
    await tester.pump();
    await tester.pump();
    expect(find.byKey(const Key('country-empty')), findsOneWidget);

    fake.availableCountries = const IrvioAvailableCountries(
      status: IrvioAvailableCountriesStatus.unavailable,
      countryCodes: [],
      generatedAt: null,
    );
    await controller.refreshAvailableCountries(refresh: true);
    await tester.pump();
    expect(find.byKey(const Key('country-unavailable')), findsOneWidget);

    fake.availableCountries = const IrvioAvailableCountries(
      status: IrvioAvailableCountriesStatus.ready,
      countryCodes: ['NL'],
      generatedAt: null,
    );
    await tester.tap(find.byKey(const Key('country-retry')));
    await tester.pump();
    await tester.pump();
    expect(fake.lastAvailableCountriesRefresh, isTrue);
    expect(find.text('Netherlands'), findsOneWidget);
    controller.dispose();
  });

  testWidgets('availability metadata never changes an active VPN lifecycle', (
    tester,
  ) async {
    final (controller, fake) = await pumpIrvio(
      tester,
      const IrvioStatus(
        state: IrvioConnectionState.connected,
        countryCode: 'NL',
        exitCountryCode: 'NL',
      ),
      availableCountries: const IrvioAvailableCountries(
        status: IrvioAvailableCountriesStatus.ready,
        countryCodes: ['DE'],
        generatedAt: null,
      ),
    );

    expect(controller.status.state, IrvioConnectionState.connected);
    expect(controller.selectedCountry.code, 'NL');
    expect(find.text('Netherlands'), findsOneWidget);
    expect(fake.connectCalls, 0);
    expect(fake.disconnectCalls, 0);
    controller.dispose();
  });

  testWidgets('connected and recovering states only reflect Core events', (
    tester,
  ) async {
    final (controller, fake) = await pumpIrvio(
      tester,
      const IrvioStatus(
        state: IrvioConnectionState.connected,
        countryCode: 'DE',
        exitCountryCode: 'DE',
      ),
    );
    expect(find.text('CONNECTED'), findsOneWidget);
    expect(find.text('Secure connection established'), findsOneWidget);
    expect(find.byKey(const Key('connection-secondary-action')), findsNothing);

    fake.emit(
      const IrvioStatus(
        state: IrvioConnectionState.recovering,
        countryCode: 'DE',
      ),
    );
    await tester.pump();
    await tester.pump();
    expect(find.text('Reconnecting…'), findsOneWidget);
    expect(find.text('Finding a new peer'), findsOneWidget);
    expect(fake.connectCalls, 0);

    fake.emit(
      const IrvioStatus(
        state: IrvioConnectionState.connected,
        countryCode: 'DE',
      ),
      sequence: 2,
    );
    await tester.pump();
    await tester.pump();
    expect(find.text('CONNECTED'), findsOneWidget);
    controller.dispose();
  });

  testWidgets('No Exit, No Network and Error have distinct honest copy', (
    tester,
  ) async {
    final (controller, fake) = await pumpIrvio(
      tester,
      const IrvioStatus(state: IrvioConnectionState.noExit, countryCode: 'DE'),
    );
    expect(find.text('No peers available'), findsOneWidget);

    fake.emit(
      const IrvioStatus(
        state: IrvioConnectionState.noNetwork,
        countryCode: 'DE',
      ),
    );
    await tester.pump();
    await tester.pump();
    expect(find.text('No network'), findsWidgets);
    expect(find.text('Check your Internet connection'), findsOneWidget);

    fake.emit(
      const IrvioStatus(
        state: IrvioConnectionState.error,
        errorMessage: 'Safe daemon error',
      ),
    );
    await tester.pump();
    await tester.pump();
    expect(find.text('Something went wrong'), findsOneWidget);
    expect(find.text('Safe daemon error'), findsOneWidget);
    controller.dispose();
  });

  testWidgets('unfinished contribution and Settings placeholders are absent', (
    tester,
  ) async {
    final (controller, _) = await pumpIrvio(
      tester,
      const IrvioStatus.disconnected(),
      apiMinor: 0,
    );

    expect(find.text('Help IRVIO network'), findsNothing);
    expect(find.textContaining('Coming later'), findsNothing);
    expect(find.textContaining('Not available in this version'), findsNothing);

    controller.selectPage(IrvioPage.settings);
    await tester.pump();
    expect(find.text('Help IRVIO network'), findsNothing);
    expect(find.text('Advanced'), findsNothing);
    expect(find.text('Start at login'), findsOneWidget);
    expect(find.text('Show notifications'), findsOneWidget);
    expect(find.text('Language'), findsOneWidget);
    expect(find.text('Theme'), findsOneWidget);
    await tester.scrollUntilVisible(
      find.text('Delete profile'),
      180,
      scrollable: find.byType(Scrollable).last,
    );
    expect(find.text('Delete profile'), findsOneWidget);
    controller.dispose();
  });

  testWidgets('closing and restarting GUI restores Connected without command', (
    tester,
  ) async {
    final first = await pumpIrvio(
      tester,
      const IrvioStatus(
        state: IrvioConnectionState.connected,
        countryCode: 'DE',
      ),
    );
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump();
    expect(first.$2.disconnectCalls, 0);
    first.$1.dispose();

    final second = await pumpIrvio(
      tester,
      const IrvioStatus(
        state: IrvioConnectionState.connected,
        countryCode: 'DE',
      ),
    );
    expect(find.text('CONNECTED'), findsOneWidget);
    expect(second.$2.connectCalls, 0);
    second.$1.dispose();
  });

  testWidgets('daemon unavailable remains stable and offers retry', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(492, 620));
    final controller = IrvioAppController(
      () async => throw const SocketException('missing socket'),
      reconnectDelays: const [Duration(hours: 1)],
    );
    await controller.start();
    await tester.pumpWidget(IrvioApp(controller: controller));
    await tester.pump();

    expect(find.byKey(const Key('service-message')), findsOneWidget);
    expect(find.text('Retry'), findsOneWidget);
    expect(controller.daemonAvailable, isFalse);
    controller.dispose();
  });

  testWidgets('Disconnect action uses Local API and not widget networking', (
    tester,
  ) async {
    final (controller, fake) = await pumpIrvio(
      tester,
      const IrvioStatus(
        state: IrvioConnectionState.connected,
        countryCode: 'DE',
      ),
    );
    await tester.tap(find.byKey(const Key('primary-connection-action')));
    await tester.pump();
    expect(fake.disconnectCalls, 1);
    expect(find.byKey(const Key('connection-secondary-action')), findsNothing);
    controller.dispose();
  });

  testWidgets('Disconnect shows amber ring without duplicate progress bar', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(492, 620));
    final gate = Completer<void>();
    final fake = FakeIrvioClient(
      currentStatus: const IrvioStatus(
        state: IrvioConnectionState.connected,
        countryCode: 'DE',
      ),
      disconnectGate: gate,
    );
    final controller = IrvioAppController(() async => fake);
    await controller.start();
    await tester.pumpWidget(IrvioApp(controller: controller));
    await tester.pump();

    await tester.tap(find.byKey(const Key('primary-connection-action')));
    await tester.pump();
    expect(fake.disconnectCalls, 1);
    expect(controller.disconnectPending, isTrue);
    expect(find.byKey(const Key('disconnect-progress')), findsNothing);
    expect(find.byType(LinearProgressIndicator), findsNothing);
    expect(find.byKey(const Key('connection-glow-amber')), findsOneWidget);
    expect(find.text('Disconnecting…'), findsWidgets);
    expect(find.text('Closing the secure connection'), findsOneWidget);
    expect(controller.status.state, IrvioConnectionState.connected);

    gate.complete();
    await tester.pump();
    expect(controller.disconnectPending, isFalse);
    controller.dispose();
  });

  testWidgets('Connection icon is centered with label directly below', (
    tester,
  ) async {
    final (controller, _) = await pumpIrvio(
      tester,
      const IrvioStatus.disconnected(),
    );
    final icon = find.byKey(const Key('connection-center-icon'));
    final circle = find.byKey(const Key('connection-glow-none'));
    final label = find.text('CONNECT');
    expect(
      (tester.getCenter(icon).dy - tester.getCenter(circle).dy).abs(),
      lessThan(0.1),
    );
    expect(
      tester.getTopLeft(label).dy - tester.getBottomLeft(icon).dy,
      closeTo(10, 1),
    );
    await tester.pumpAndSettle();
    expect(tester.hasRunningAnimations, isFalse);
    controller.dispose();
  });

  testWidgets('Recovering central action cancels through Disconnect', (
    tester,
  ) async {
    final (controller, fake) = await pumpIrvio(
      tester,
      const IrvioStatus(
        state: IrvioConnectionState.recovering,
        countryCode: 'DE',
      ),
    );
    await tester.tap(find.byKey(const Key('primary-connection-action')));
    await tester.pump();
    expect(fake.disconnectCalls, 1);
    expect(fake.connectCalls, 0);
    controller.dispose();
  });

  testWidgets('Settings Delete profile requires confirmation and uses v1.2', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(492, 620));
    final statusClient = FakeIrvioClient(apiMinor: 2);
    final logClient = FakeIrvioClient(apiMinor: 2);
    final clients = <FakeIrvioClient>[statusClient, logClient];
    final controller = IrvioAppController(() async => clients.removeAt(0));
    await controller.start();
    controller.selectPage(IrvioPage.settings);
    await tester.pumpWidget(IrvioApp(controller: controller));
    await tester.pump();

    await tester.drag(find.byType(ListView), const Offset(0, -500));
    await tester.pumpAndSettle();
    final deleteButton = find.byKey(const Key('delete-profile-button'));
    await tester.tap(deleteButton);
    await tester.pumpAndSettle();
    expect(find.text('Delete IRVIO profile?'), findsOneWidget);
    expect(statusClient.deleteProfileCalls, 0);

    await tester.tap(find.byKey(const Key('delete-profile-cancel')));
    await tester.pumpAndSettle();
    expect(statusClient.deleteProfileCalls, 0);

    await tester.tap(deleteButton);
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('delete-profile-confirm')));
    await tester.pump();
    expect(statusClient.deleteProfileCalls, 1);
    controller.dispose();
  });

  testWidgets('Settings Delete profile is disabled with Local API v1.1', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(492, 620));
    final statusClient = FakeIrvioClient(apiMinor: 1);
    final logClient = FakeIrvioClient(apiMinor: 1);
    final clients = <FakeIrvioClient>[statusClient, logClient];
    final controller = IrvioAppController(() async => clients.removeAt(0));
    await controller.start();
    controller.selectPage(IrvioPage.settings);
    await tester.pumpWidget(IrvioApp(controller: controller));
    await tester.pump();

    await tester.drag(find.byType(ListView), const Offset(0, -500));
    await tester.pumpAndSettle();
    final deleteButton = find.byKey(const Key('delete-profile-button'));
    expect(tester.widget<OutlinedButton>(deleteButton).onPressed, isNull);
    expect(find.text('Requires IRVIO Local API v1.2.'), findsOneWidget);
    controller.dispose();
  });

  testWidgets('empty bootstrap state shows onboarding and imports text', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(492, 620));
    final statusClient = FakeIrvioClient(
      apiMinor: 1,
      bootstrapState: const IrvioBootstrapState(
        phase: IrvioBootstrapPhase.empty,
        configuredCount: 0,
        currentlyReachableCount: 0,
      ),
    );
    final logClient = FakeIrvioClient(apiMinor: 1);
    final clients = <FakeIrvioClient>[statusClient, logClient];
    final controller = IrvioAppController(() async => clients.removeAt(0));
    await controller.start();
    await tester.pumpWidget(IrvioApp(controller: controller));
    await tester.pump();

    expect(find.byKey(const Key('bootstrap-onboarding')), findsOneWidget);
    expect(find.byKey(const Key('bootstrap-advanced')), findsNothing);
    expect(find.text('Advanced'), findsNothing);
    expect(
      tester
          .widget<TextField>(find.byKey(const Key('bootstrap-code')))
          .decoration
          ?.hintText,
      isNull,
    );
    await tester.enterText(
      find.byKey(const Key('bootstrap-code')),
      'irvio-bootstrap:v1:test',
    );
    await tester.tap(find.byKey(const Key('bootstrap-import-text')));
    await tester.pump();
    expect(statusClient.bootstrapImportCalls, 1);
    controller.dispose();
  });

  testWidgets('empty bootstrap opens onboarding despite saved Connect intent', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(492, 620));
    final statusClient = FakeIrvioClient(
      currentStatus: const IrvioStatus(
        state: IrvioConnectionState.noNetwork,
        countryCode: 'NL',
      ),
      bootstrapState: const IrvioBootstrapState(
        phase: IrvioBootstrapPhase.empty,
        configuredCount: 0,
        currentlyReachableCount: 0,
      ),
    );
    final logClient = FakeIrvioClient();
    final clients = <FakeIrvioClient>[statusClient, logClient];
    final controller = IrvioAppController(() async => clients.removeAt(0));
    await controller.start();
    await tester.pumpWidget(IrvioApp(controller: controller));
    await tester.pump();
    expect(find.byKey(const Key('bootstrap-onboarding')), findsOneWidget);
    expect(find.byKey(const Key('country-selector')), findsNothing);
    controller.dispose();
  });

  testWidgets('Logs screen filters and pauses the separate log view', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(492, 620));
    final statusClient = FakeIrvioClient(apiMinor: 1);
    final logClient = FakeIrvioClient(
      apiMinor: 1,
      logTail: IrvioLogTail(
        records: [
          IrvioLogRecord(
            sequence: 1,
            timestamp: DateTime.utc(2026),
            severity: IrvioLogSeverity.info,
            message: 'INFO_RECORD',
          ),
          IrvioLogRecord(
            sequence: 2,
            timestamp: DateTime.utc(2026),
            severity: IrvioLogSeverity.warning,
            message: 'WARN_RECORD',
          ),
        ],
        lastSequence: 2,
        daemonIncarnation: const [3],
        gap: false,
      ),
    );
    final clients = <FakeIrvioClient>[statusClient, logClient];
    final controller = IrvioAppController(() async => clients.removeAt(0));
    await controller.start();
    await logClient.logSubscribed.future;
    controller.selectPage(IrvioPage.logs);
    await tester.pumpWidget(IrvioApp(controller: controller));
    await tester.pump();

    expect(
      find.textContaining('INFO_RECORD', findRichText: true),
      findsOneWidget,
    );
    expect(
      find.textContaining('WARN_RECORD', findRichText: true),
      findsOneWidget,
    );
    await tester.tap(find.byKey(const Key('logs-filter-warning')));
    await tester.pump();
    expect(
      find.textContaining('INFO_RECORD', findRichText: true),
      findsNothing,
    );
    expect(
      find.textContaining('WARN_RECORD', findRichText: true),
      findsOneWidget,
    );
    await tester.tap(find.byKey(const Key('logs-pause')));
    await tester.pump();
    expect(controller.logsPaused, isTrue);
    controller.dispose();
  });

  testWidgets('Share IRVIO renders daemon-generated code as QR', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(492, 620));
    final statusClient = FakeIrvioClient(apiMinor: 1)
      ..generatedBundle = IrvioGeneratedBootstrapBundle(
        code: 'irvio-bootstrap:v1:AQIDBA',
        nodeCount: 1,
        generatedAt: DateTime.now(),
      );
    final logClient = FakeIrvioClient(apiMinor: 1);
    final clients = <FakeIrvioClient>[statusClient, logClient];
    final controller = IrvioAppController(() async => clients.removeAt(0));
    await controller.start();
    await controller.generateBootstrapBundle();
    controller.selectPage(IrvioPage.share);
    await tester.pumpWidget(IrvioApp(controller: controller));
    await tester.pump();

    expect(find.byKey(const Key('share-qr')), findsOneWidget);
    final save = find.byKey(const Key('share-save'));
    expect(save, findsOneWidget);
    expect(tester.widget<OutlinedButton>(save).onPressed, isNotNull);
    expect(
      tester.getTopLeft(save).dy,
      greaterThan(
        tester.getBottomLeft(find.byKey(const Key('share-refresh'))).dy,
      ),
    );
    expect(
      tester.getBottomLeft(save).dy,
      lessThan(tester.getTopLeft(find.byKey(const Key('share-copy'))).dy),
    );
    expect(find.text('1 reachable bootstrap node'), findsOneWidget);
    expect(statusClient.bootstrapGenerateCalls, 1);

    final directory = Directory.systemTemp.createTempSync('irvio-qr-save-');
    final exported = File('${directory.path}/irvio-bootstrap.png');
    const channel = MethodChannel('plugins.flutter.io/file_selector');
    final messenger = tester.binding.defaultBinaryMessenger;
    var chooserCalls = 0;
    messenger.setMockMethodCallHandler(channel, (call) async {
      expect(call.method, 'getSavePath');
      expect(call.arguments['suggestedName'], 'irvio-bootstrap.png');
      chooserCalls++;
      return exported.path;
    });
    addTearDown(() {
      messenger.setMockMethodCallHandler(channel, null);
      directory.deleteSync(recursive: true);
    });
    await tester.runAsync(() async {
      await tester.tap(save);
      for (var retry = 0; retry < 60; retry++) {
        if (exported.existsSync() && exported.lengthSync() > 0) break;
        await Future<void>.delayed(const Duration(milliseconds: 50));
      }
      expect(exported.existsSync(), isTrue);
      expect(
        decodeIrvioBootstrapQr(await exported.readAsBytes()),
        statusClient.generatedBundle!.code,
      );
    });
    await tester.pump();
    expect(find.text('QR code saved.'), findsOneWidget);
    expect(tester.widget<OutlinedButton>(save).onPressed, isNotNull);

    // Cancelling the native chooser must leave the UI ready for another save.
    messenger.setMockMethodCallHandler(channel, (call) async {
      chooserCalls++;
      return null;
    });
    await tester.tap(save);
    await tester.pump();
    expect(chooserCalls, 2);
    expect(tester.widget<OutlinedButton>(save).onPressed, isNotNull);
    expect(statusClient.bootstrapGenerateCalls, 1);
    controller.dispose();
  });

  testWidgets('Local API v1.0 keeps baseline UI and marks P2.1 unavailable', (
    tester,
  ) async {
    final (controller, _) = await pumpIrvio(
      tester,
      const IrvioStatus.disconnected(),
      apiMinor: 0,
    );
    expect(find.byKey(const Key('bootstrap-onboarding')), findsNothing);
    expect(find.byKey(const Key('country-unavailable')), findsOneWidget);
    expect(find.byKey(const Key('country-selector')), findsNothing);
    controller.selectPage(IrvioPage.share);
    await tester.pump();
    expect(
      find.text('Share IRVIO unavailable with Local API v1.0.'),
      findsOneWidget,
    );
    controller.selectPage(IrvioPage.logs);
    await tester.pump();
    expect(find.text('Logs unavailable with Local API v1.0.'), findsOneWidget);
    controller.dispose();
  });
}
