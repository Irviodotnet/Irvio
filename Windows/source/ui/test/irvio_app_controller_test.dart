// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/app/irvio_app_controller.dart';
import 'package:irvio/models/irvio_models.dart';
import 'package:irvio/screens/diagnostics_screen.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fake_irvio_client.dart';

Future<void> waitForController(
  IrvioAppController controller,
  bool Function() predicate,
) async {
  if (predicate()) return;
  final ready = Completer<void>();
  void listener() {
    if (predicate() && !ready.isCompleted) ready.complete();
  }

  controller.addListener(listener);
  try {
    await ready.future.timeout(const Duration(seconds: 2));
  } finally {
    controller.removeListener(listener);
  }
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('country refresh and steady retry default to ten seconds', () {
    final controller = IrvioAppController(() async => FakeIrvioClient());
    expect(controller.countryRefreshInterval, const Duration(seconds: 10));
    expect(controller.countryRetrySteadyDelay, const Duration(seconds: 10));
    controller.dispose();
  });

  test(
    'confirmed Shutdown stops UI reconnect and persists warning preference',
    () async {
      final fake = FakeIrvioClient(apiMinor: 4);
      final controller = IrvioAppController(() async => fake);
      await controller.start();
      await controller.setSkipShutdownConfirmation(true);
      expect(controller.skipShutdownConfirmation, isTrue);
      expect(await controller.shutdownIrvio(), isTrue);
      expect(fake.shutdownCalls, 1);
      await controller.reconnectNow();
      expect(fake.shutdownCalls, 1);
      controller.dispose();

      final next = IrvioAppController(() async => FakeIrvioClient());
      await next.start();
      expect(next.skipShutdownConfirmation, isTrue);
      next.dispose();
    },
  );

  test('startup gets authoritative status before subscribing', () async {
    final fake = FakeIrvioClient(
      currentStatus: const IrvioStatus(
        state: IrvioConnectionState.connected,
        countryCode: 'DE',
        exitCountryCode: 'DE',
      ),
    );
    final controller = IrvioAppController(() async => fake);

    await controller.start();

    expect(controller.daemonAvailable, isTrue);
    expect(controller.status.state, IrvioConnectionState.connected);
    expect(controller.selectedCountry.code, 'DE');
    expect(fake.statusCalls, 1);
    expect(fake.subscribeCalls, 1);
    controller.dispose();
  });

  test(
    'connect and disconnect delegate without creating a UI lifecycle',
    () async {
      final fake = FakeIrvioClient();
      final controller = IrvioAppController(() async => fake);
      await controller.start();
      await waitForController(
        controller,
        () => !controller.availableCountriesLoading,
      );

      await controller.connect();
      await controller.connect();
      await controller.disconnect();
      await controller.disconnect();

      expect(fake.connectCalls, 2);
      expect(fake.connectedCountry, 'DE');
      expect(fake.disconnectCalls, 2);
      controller.dispose();
    },
  );

  test('Delete profile is delegated only through Local API v1.2', () async {
    final statusClient = FakeIrvioClient(apiMinor: 2);
    final logClient = FakeIrvioClient(apiMinor: 2);
    final clients = <FakeIrvioClient>[statusClient, logClient];
    final controller = IrvioAppController(() async => clients.removeAt(0));
    await controller.start();
    await logClient.logSubscribed.future;

    await controller.deleteProfile();

    expect(statusClient.deleteProfileCalls, 1);
    expect(controller.profileDeletePending, isTrue);
    expect(controller.daemonAvailable, isFalse);
    expect(controller.apiError, isNull);
    controller.dispose();
  });

  test('Delete profile is unavailable with Local API v1.1', () async {
    final statusClient = FakeIrvioClient(apiMinor: 1);
    final logClient = FakeIrvioClient(apiMinor: 1);
    final clients = <FakeIrvioClient>[statusClient, logClient];
    final controller = IrvioAppController(() async => clients.removeAt(0));
    await controller.start();
    await logClient.logSubscribed.future;

    await controller.deleteProfile();

    expect(statusClient.deleteProfileCalls, 0);
    expect(controller.profileDeletePending, isFalse);
    expect(controller.apiError, contains('v1.2'));
    controller.dispose();
  });

  test(
    'v1.1 loads bootstrap state and follows logs on a separate client',
    () async {
      final statusClient = FakeIrvioClient(
        apiMinor: 1,
        bootstrapState: const IrvioBootstrapState(
          phase: IrvioBootstrapPhase.empty,
          configuredCount: 0,
          currentlyReachableCount: 0,
        ),
      );
      final logClient = FakeIrvioClient(
        apiMinor: 1,
        logTail: IrvioLogTail(
          records: [
            IrvioLogRecord(
              sequence: 1,
              timestamp: DateTime.utc(2026),
              severity: IrvioLogSeverity.info,
              message: 'DHT_BOOTSTRAP_COMPLETE',
            ),
          ],
          lastSequence: 1,
          daemonIncarnation: const [9],
          gap: false,
        ),
      );
      final clients = <FakeIrvioClient>[statusClient, logClient];
      final controller = IrvioAppController(() async => clients.removeAt(0));

      await controller.start();
      await logClient.logSubscribed.future;
      await waitForController(controller, () => controller.logsAvailable);

      expect(controller.bootstrapAvailable, isTrue);
      expect(controller.bootstrapState?.phase, IrvioBootstrapPhase.empty);
      expect(controller.logsAvailable, isTrue);
      expect(controller.logs.single.message, 'DHT_BOOTSTRAP_COMPLETE');
      expect(statusClient.subscribeCalls, 1);
      expect(logClient.logSubscribeCalls, 1);

      logClient.emitLog(
        IrvioLogRecord(
          sequence: 2,
          timestamp: DateTime.utc(2026),
          severity: IrvioLogSeverity.warning,
          message: 'RECOVERING',
        ),
        incarnation: const [9],
      );
      await Future<void>.delayed(Duration.zero);
      expect(controller.logs.last.message, 'RECOVERING');
      controller.dispose();
    },
  );

  test('log pause closes the stream instead of accumulating backlog', () async {
    final statusClient = FakeIrvioClient(apiMinor: 1);
    final logClient = FakeIrvioClient(apiMinor: 1);
    final clients = <FakeIrvioClient>[statusClient, logClient];
    final controller = IrvioAppController(() async => clients.removeAt(0));
    await controller.start();
    await logClient.logSubscribed.future;

    controller.setLogsPaused(true);
    await Future<void>.delayed(Duration.zero);
    logClient.emitLog(
      IrvioLogRecord(
        sequence: 1,
        timestamp: DateTime.utc(2026),
        severity: IrvioLogSeverity.info,
        message: 'not buffered',
      ),
    );
    await Future<void>.delayed(Duration.zero);
    expect(controller.logs, isEmpty);
    expect(logClient.closeCalls, 1);
    controller.dispose();
  });

  test(
    'bootstrap import remains daemon-owned and reports returned state',
    () async {
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
      await Future<void>.delayed(Duration.zero);

      await controller.importBootstrapBundle('irvio-bootstrap:v1:test');
      expect(statusClient.bootstrapImportCalls, 1);
      expect(controller.bootstrapState?.phase, IrvioBootstrapPhase.connecting);
      controller.dispose();
    },
  );

  test('bootstrap polling continues beyond the fast join window', () async {
    final statusClient = FakeIrvioClient(
      bootstrapState: const IrvioBootstrapState(
        phase: IrvioBootstrapPhase.empty,
        configuredCount: 0,
        currentlyReachableCount: 0,
      ),
      bootstrapStateHandler: (calls) => IrvioBootstrapState(
        phase: calls == 1
            ? IrvioBootstrapPhase.empty
            : calls >= 45
            ? IrvioBootstrapPhase.ready
            : IrvioBootstrapPhase.connecting,
        configuredCount: calls == 1 ? 0 : 1,
        currentlyReachableCount: calls >= 45 ? 1 : 0,
      ),
    );
    final logClient = FakeIrvioClient();
    final clients = <FakeIrvioClient>[statusClient, logClient];
    final controller = IrvioAppController(
      () async => clients.removeAt(0),
      bootstrapRefreshInterval: Duration.zero,
      bootstrapRefreshSteadyInterval: Duration.zero,
    );
    await controller.start();
    await controller.importBootstrapBundle('irvio-bootstrap:v1:test');
    await waitForController(
      controller,
      () => controller.bootstrapState?.phase == IrvioBootstrapPhase.ready,
    );
    expect(statusClient.bootstrapStateCalls, 45);
    controller.dispose();
  });

  test('bootstrap READY refreshes stale unavailable locations', () async {
    final fake = FakeIrvioClient(
      bootstrapState: const IrvioBootstrapState(
        phase: IrvioBootstrapPhase.empty,
        configuredCount: 0,
        currentlyReachableCount: 0,
      ),
      importedBootstrapState: const IrvioBootstrapState(
        phase: IrvioBootstrapPhase.ready,
        configuredCount: 2,
        currentlyReachableCount: 2,
      ),
      availableCountries: const IrvioAvailableCountries(
        status: IrvioAvailableCountriesStatus.unavailable,
        countryCodes: [],
        generatedAt: null,
      ),
    );
    final controller = IrvioAppController(() async => fake);
    await controller.start();
    await waitForController(
      controller,
      () => !controller.availableCountriesLoading,
    );
    expect(
      controller.availableCountriesStatus,
      IrvioAvailableCountriesStatus.unavailable,
    );

    fake.availableCountries = const IrvioAvailableCountries(
      status: IrvioAvailableCountriesStatus.ready,
      countryCodes: ['NL', 'RU'],
      generatedAt: null,
    );
    await controller.importBootstrapBundle('irvio-bootstrap:v1:test');
    await waitForController(
      controller,
      () =>
          controller.availableCountriesStatus ==
          IrvioAvailableCountriesStatus.ready,
    );
    expect(controller.availableCountries.map((country) => country.code), [
      'NL',
      'RU',
    ]);
    expect(fake.lastAvailableCountriesRefresh, isTrue);
    controller.dispose();
  });

  test('READY refresh is not lost behind an in-flight query', () async {
    final first = Completer<IrvioAvailableCountries>();
    var requests = 0;
    final fake = FakeIrvioClient(
      bootstrapState: const IrvioBootstrapState(
        phase: IrvioBootstrapPhase.empty,
        configuredCount: 0,
        currentlyReachableCount: 0,
      ),
      importedBootstrapState: const IrvioBootstrapState(
        phase: IrvioBootstrapPhase.ready,
        configuredCount: 2,
        currentlyReachableCount: 2,
      ),
      availableCountriesHandler: (refresh) {
        requests++;
        if (requests == 1) return first.future;
        return Future.value(
          const IrvioAvailableCountries(
            status: IrvioAvailableCountriesStatus.ready,
            countryCodes: ['NL', 'RU'],
            generatedAt: null,
          ),
        );
      },
    );
    final controller = IrvioAppController(() async => fake);
    await controller.start();
    expect(requests, 1);
    await controller.importBootstrapBundle('irvio-bootstrap:v1:test');
    first.complete(
      const IrvioAvailableCountries(
        status: IrvioAvailableCountriesStatus.unavailable,
        countryCodes: [],
        generatedAt: null,
      ),
    );
    await waitForController(
      controller,
      () =>
          controller.availableCountriesStatus ==
          IrvioAvailableCountriesStatus.ready,
    );
    expect(requests, 2);
    expect(fake.lastAvailableCountriesRefresh, isTrue);
    controller.dispose();
  });

  test('READY country discovery retries a bounded transient failure', () async {
    var requests = 0;
    final fake = FakeIrvioClient(
      bootstrapState: const IrvioBootstrapState(
        phase: IrvioBootstrapPhase.empty,
        configuredCount: 0,
        currentlyReachableCount: 0,
      ),
      importedBootstrapState: const IrvioBootstrapState(
        phase: IrvioBootstrapPhase.ready,
        configuredCount: 2,
        currentlyReachableCount: 2,
      ),
      availableCountriesHandler: (refresh) {
        requests++;
        return Future.value(
          IrvioAvailableCountries(
            status: requests < 3
                ? IrvioAvailableCountriesStatus.unavailable
                : IrvioAvailableCountriesStatus.ready,
            countryCodes: requests < 3 ? const [] : const ['NL', 'RU'],
            generatedAt: null,
          ),
        );
      },
    );
    final controller = IrvioAppController(
      () async => fake,
      countryRetryDelays: const [Duration.zero],
    );
    await controller.start();
    await waitForController(
      controller,
      () => !controller.availableCountriesLoading,
    );
    await controller.importBootstrapBundle('irvio-bootstrap:v1:test');
    await waitForController(
      controller,
      () =>
          controller.availableCountriesStatus ==
          IrvioAvailableCountriesStatus.ready,
    );
    expect(requests, 3);
    expect(controller.availableCountries.map((country) => country.code), [
      'NL',
      'RU',
    ]);
    controller.dispose();
  });

  test(
    'post-bootstrap country retries respect the fast retry budget',
    () async {
      var requests = 0;
      final fake = FakeIrvioClient(
        bootstrapState: const IrvioBootstrapState(
          phase: IrvioBootstrapPhase.empty,
          configuredCount: 0,
          currentlyReachableCount: 0,
        ),
        importedBootstrapState: const IrvioBootstrapState(
          phase: IrvioBootstrapPhase.ready,
          configuredCount: 2,
          currentlyReachableCount: 2,
        ),
        availableCountriesHandler: (refresh) {
          requests++;
          return Future.value(
            const IrvioAvailableCountries(
              status: IrvioAvailableCountriesStatus.unavailable,
              countryCodes: [],
              generatedAt: null,
            ),
          );
        },
      );
      final controller = IrvioAppController(
        () async => fake,
        countryRetryDelays: const [Duration.zero, Duration.zero],
        countryRetrySteadyDelay: const Duration(seconds: 1),
      );
      await controller.start();
      await waitForController(
        controller,
        () => !controller.availableCountriesLoading,
      );
      await controller.importBootstrapBundle('irvio-bootstrap:v1:test');
      await waitForController(
        controller,
        () => requests == 4 && !controller.availableCountriesLoading,
      );
      await Future<void>.delayed(Duration.zero);
      expect(requests, 4);
      expect(
        controller.availableCountriesStatus,
        IrvioAvailableCountriesStatus.unavailable,
      );
      controller.dispose();
    },
  );

  test('countries recover after the fast bootstrap retry window', () async {
    var requests = 0;
    final fake = FakeIrvioClient(
      bootstrapState: const IrvioBootstrapState(
        phase: IrvioBootstrapPhase.empty,
        configuredCount: 0,
        currentlyReachableCount: 0,
      ),
      importedBootstrapState: const IrvioBootstrapState(
        phase: IrvioBootstrapPhase.ready,
        configuredCount: 2,
        currentlyReachableCount: 2,
      ),
      availableCountriesHandler: (refresh) {
        requests++;
        return Future.value(
          IrvioAvailableCountries(
            status: requests < 5
                ? IrvioAvailableCountriesStatus.unavailable
                : IrvioAvailableCountriesStatus.ready,
            countryCodes: requests < 5 ? const [] : const ['NL', 'RU'],
            generatedAt: null,
          ),
        );
      },
    );
    final controller = IrvioAppController(
      () async => fake,
      countryRetryDelays: const [Duration.zero, Duration.zero],
      countryRetrySteadyDelay: Duration.zero,
    );
    await controller.start();
    await waitForController(
      controller,
      () => !controller.availableCountriesLoading,
    );
    await controller.importBootstrapBundle('irvio-bootstrap:v1:test');
    await waitForController(
      controller,
      () =>
          controller.availableCountriesStatus ==
          IrvioAvailableCountriesStatus.ready,
    );
    expect(requests, 5);
    expect(controller.availableCountries.map((country) => country.code), [
      'NL',
      'RU',
    ]);
    controller.dispose();
  });

  test('open GUI discovers new countries on periodic forced refresh', () async {
    final secondStarted = Completer<void>();
    final secondResponse = Completer<IrvioAvailableCountries>();
    var requests = 0;
    final fake = FakeIrvioClient(
      currentStatus: const IrvioStatus(
        state: IrvioConnectionState.connected,
        countryCode: 'NL',
        exitCountryCode: 'NL',
      ),
      availableCountriesHandler: (refresh) {
        requests++;
        if (requests == 1) {
          expect(refresh, isFalse);
          return Future.value(
            const IrvioAvailableCountries(
              status: IrvioAvailableCountriesStatus.ready,
              countryCodes: ['NL'],
              generatedAt: null,
            ),
          );
        }
        expect(refresh, isTrue);
        if (!secondStarted.isCompleted) secondStarted.complete();
        return secondResponse.future;
      },
    );
    final controller = IrvioAppController(
      () async => fake,
      countryRefreshInterval: const Duration(milliseconds: 10),
    );
    await controller.start();
    await waitForController(
      controller,
      () =>
          controller.availableCountries.map((item) => item.code).join() == 'NL',
    );
    await secondStarted.future.timeout(const Duration(seconds: 2));
    expect(controller.availableCountriesLoading, isFalse);
    expect(controller.availableCountries.map((item) => item.code), ['NL']);
    secondResponse.complete(
      const IrvioAvailableCountries(
        status: IrvioAvailableCountriesStatus.ready,
        countryCodes: ['NL', 'RU'],
        generatedAt: null,
      ),
    );
    await waitForController(
      controller,
      () => controller.availableCountries.any((item) => item.code == 'RU'),
    );
    expect(controller.status.state, IrvioConnectionState.connected);
    expect(controller.selectedCountry.code, 'NL');
    expect(fake.connectCalls, 0);
    expect(fake.disconnectCalls, 0);
    controller.dispose();
  });

  test('bootstrap parser errors remain safe product errors', () async {
    final statusClient = FakeIrvioClient(
      apiMinor: 1,
      bootstrapImportError: const IrvioApiException(
        IrvioErrorCode.unsupported,
        'Bootstrap bundle version is unsupported.',
      ),
    );
    final logClient = FakeIrvioClient(apiMinor: 1);
    final clients = <FakeIrvioClient>[statusClient, logClient];
    final controller = IrvioAppController(() async => clients.removeAt(0));
    await controller.start();

    await controller.importBootstrapBundle('irvio-bootstrap:v2:AAAA');
    expect(
      controller.bootstrapError,
      'Bootstrap bundle version is unsupported.',
    );
    expect(statusClient.bootstrapImportCalls, 1);
    controller.dispose();
  });

  test(
    'log gap is visible and daemon reconnect restores both streams',
    () async {
      final firstStatus = FakeIrvioClient(apiMinor: 1);
      final firstLogs = FakeIrvioClient(
        apiMinor: 1,
        logTail: const IrvioLogTail(
          records: [],
          lastSequence: 700,
          daemonIncarnation: [1],
          gap: true,
        ),
      );
      final secondStatus = FakeIrvioClient(
        apiMinor: 1,
        currentStatus: const IrvioStatus(
          state: IrvioConnectionState.connected,
          countryCode: 'NL',
        ),
      );
      final secondLogs = FakeIrvioClient(
        apiMinor: 1,
        logTail: IrvioLogTail(
          records: [
            IrvioLogRecord(
              sequence: 1,
              timestamp: DateTime.utc(2026),
              severity: IrvioLogSeverity.info,
              message: 'LOCAL_API_STARTED',
            ),
          ],
          lastSequence: 1,
          daemonIncarnation: const [2],
          gap: false,
        ),
      );
      final clients = <FakeIrvioClient>[
        firstStatus,
        firstLogs,
        secondStatus,
        secondLogs,
      ];
      final controller = IrvioAppController(
        () async => clients.removeAt(0),
        reconnectDelays: const [Duration(milliseconds: 1)],
      );
      await controller.start();
      await firstLogs.logSubscribed.future;
      await waitForController(controller, () => controller.logsAvailable);
      expect(controller.logsError, contains('no longer available'));

      firstStatus.errors.add(
        const IrvioApiException(IrvioErrorCode.unavailable, 'daemon restarted'),
      );
      await secondLogs.logSubscribed.future.timeout(const Duration(seconds: 2));
      await waitForController(
        controller,
        () => controller.logsAvailable && controller.logs.isNotEmpty,
      );
      expect(controller.status.state, IrvioConnectionState.connected);
      expect(controller.selectedCountry.code, 'NL');
      expect(controller.logs.single.message, 'LOCAL_API_STARTED');
      expect(secondStatus.statusCalls, 1);
      controller.dispose();
    },
  );

  test('daemon restart fences an in-flight old log tail', () async {
    final oldTailGate = Completer<void>();
    final firstStatus = FakeIrvioClient(apiMinor: 1);
    final firstLogs = FakeIrvioClient(apiMinor: 1, recentLogsGate: oldTailGate);
    final secondStatus = FakeIrvioClient(
      apiMinor: 1,
      currentStatus: const IrvioStatus(
        state: IrvioConnectionState.connected,
        countryCode: 'DE',
      ),
    );
    final secondLogs = FakeIrvioClient(apiMinor: 1);
    final clients = <FakeIrvioClient>[
      firstStatus,
      firstLogs,
      secondStatus,
      secondLogs,
    ];
    final controller = IrvioAppController(
      () async => clients.removeAt(0),
      reconnectDelays: const [Duration(milliseconds: 1)],
    );
    await controller.start();
    await firstLogs.recentLogsStarted.future;

    firstStatus.errors.add(
      const IrvioApiException(IrvioErrorCode.unavailable, 'daemon restarted'),
    );
    await waitForController(controller, () => secondStatus.statusCalls == 1);
    oldTailGate.complete();

    await secondLogs.logSubscribed.future.timeout(const Duration(seconds: 2));
    expect(firstLogs.logSubscribeCalls, 0);
    expect(secondLogs.logSubscribeCalls, 1);
    expect(controller.status.state, IrvioConnectionState.connected);
    controller.dispose();
  });

  test('connection uptime is rendered as HH:MM:SS', () {
    final now = DateTime.utc(2026, 1, 1, 5, 37, 11);
    expect(formatConnectionUptime(DateTime.utc(2026, 1, 1), now), '05:37:11');
    expect(formatConnectionUptime(null, now), '—');
  });

  test('event updates presentation and recovery stays Core-owned', () async {
    final fake = FakeIrvioClient(
      currentStatus: const IrvioStatus(
        state: IrvioConnectionState.connected,
        countryCode: 'DE',
      ),
    );
    final controller = IrvioAppController(() async => fake);
    await controller.start();

    fake.emit(
      const IrvioStatus(
        state: IrvioConnectionState.recovering,
        countryCode: 'DE',
      ),
    );
    await Future<void>.delayed(Duration.zero);
    expect(controller.status.state, IrvioConnectionState.recovering);
    expect(fake.connectCalls, 0);

    fake.emit(
      const IrvioStatus(
        state: IrvioConnectionState.connected,
        countryCode: 'DE',
      ),
      sequence: 2,
    );
    await Future<void>.delayed(Duration.zero);
    expect(controller.status.state, IrvioConnectionState.connected);
    controller.dispose();
  });

  test(
    'event failure reconnects and resynchronizes through GET_STATUS',
    () async {
      final first = FakeIrvioClient();
      final second = FakeIrvioClient(
        currentStatus: const IrvioStatus(
          state: IrvioConnectionState.connected,
          countryCode: 'NL',
        ),
      );
      var calls = 0;
      final controller = IrvioAppController(
        () async => calls++ == 0 ? first : second,
        reconnectDelays: const [Duration(milliseconds: 1)],
      );
      await controller.start();

      first.errors.add(
        const IrvioApiException(IrvioErrorCode.unavailable, 'connection lost'),
      );
      await Future<void>.delayed(const Duration(milliseconds: 20));

      expect(second.statusCalls, 1);
      expect(second.subscribeCalls, 1);
      expect(controller.status.state, IrvioConnectionState.connected);
      expect(controller.selectedCountry.code, 'NL');
      controller.dispose();
    },
  );

  test(
    'unsupported contribution remains disabled and is never fabricated',
    () async {
      final fake = FakeIrvioClient(
        contributionError: const IrvioApiException(
          IrvioErrorCode.unsupported,
          'unsupported',
        ),
      );
      final controller = IrvioAppController(() async => fake);
      await controller.start();

      expect(controller.contribution.supported, isFalse);
      expect(controller.contribution.enabled, isFalse);
      controller.dispose();
    },
  );

  test(
    'closing controller closes API only and never disconnects VPN',
    () async {
      final fake = FakeIrvioClient(
        apiMinor: 0,
        currentStatus: const IrvioStatus(
          state: IrvioConnectionState.connected,
          countryCode: 'DE',
        ),
      );
      final controller = IrvioAppController(() async => fake);
      await controller.start();

      controller.dispose();
      await Future<void>.delayed(Duration.zero);

      expect(fake.disconnectCalls, 0);
      expect(fake.closeCalls, 1);
    },
  );
}
