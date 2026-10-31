// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';

import 'package:irvio/irvio/irvio_client.dart';
import 'package:irvio/models/irvio_models.dart';

class FakeIrvioClient implements IrvioClient {
  IrvioTrafficStats trafficStats = const IrvioTrafficStats();
  @override
  Future<IrvioTrafficStats> getTrafficStats() async => trafficStats;
  FakeIrvioClient({
    this.apiMinor = 3,
    this.currentStatus = const IrvioStatus.disconnected(),
    this.contribution = const IrvioContribution(
      supported: false,
      enabled: false,
    ),
    this.contributionError,
    this.bootstrapImportError,
    this.importedBootstrapState,
    this.bootstrapStateHandler,
    this.recentLogsGate,
    this.bootstrapState = const IrvioBootstrapState(
      phase: IrvioBootstrapPhase.ready,
      configuredCount: 1,
      currentlyReachableCount: 1,
    ),
    this.logTail = const IrvioLogTail(
      records: [],
      lastSequence: 0,
      daemonIncarnation: [1],
      gap: false,
    ),
    this.availableCountries = const IrvioAvailableCountries(
      status: IrvioAvailableCountriesStatus.ready,
      countryCodes: ['DE'],
      generatedAt: null,
    ),
    this.availableCountriesGate,
    this.availableCountriesHandler,
    this.connectGate,
    this.disconnectGate,
  });

  final int apiMinor;
  IrvioStatus currentStatus;
  IrvioContribution contribution;
  IrvioApiException? contributionError;
  IrvioApiException? bootstrapImportError;
  IrvioBootstrapState? importedBootstrapState;
  final IrvioBootstrapState Function(int callCount)? bootstrapStateHandler;
  final Completer<void>? recentLogsGate;
  IrvioBootstrapState bootstrapState;
  IrvioLogTail logTail;
  IrvioAvailableCountries availableCountries;
  final Completer<void>? availableCountriesGate;
  final Future<IrvioAvailableCountries> Function(bool refresh)?
  availableCountriesHandler;
  final Completer<void>? connectGate;
  final Completer<void>? disconnectGate;
  IrvioGeneratedBootstrapBundle? generatedBundle;
  final events = StreamController<IrvioStatusEvent>.broadcast();
  final logStream = StreamController<IrvioLogEvent>.broadcast();
  final errors = StreamController<Object>.broadcast();
  final logSubscribed = Completer<void>();
  final recentLogsStarted = Completer<void>();
  int connectCalls = 0;
  int disconnectCalls = 0;
  int shutdownCalls = 0;
  int subscribeCalls = 0;
  int statusCalls = 0;
  int recentLogsCalls = 0;
  int logSubscribeCalls = 0;
  int bootstrapStateCalls = 0;
  int bootstrapImportCalls = 0;
  int bootstrapGenerateCalls = 0;
  int deleteProfileCalls = 0;
  int availableCountriesCalls = 0;
  bool? lastAvailableCountriesRefresh;
  int closeCalls = 0;
  String? connectedCountry;

  @override
  IrvioHello get hello => IrvioHello(
    serverMajor: 1,
    serverMinor: apiMinor,
    negotiatedMinor: apiMinor,
    productVersion: 'test',
    coreVersion: 'v1',
  );

  @override
  Stream<Object> get connectionErrors => errors.stream;

  @override
  Stream<IrvioLogEvent> get logEvents => logStream.stream;

  @override
  Stream<IrvioStatusEvent> get statusEvents => events.stream;

  @override
  Future<void> connect(String countryCode) async {
    connectCalls++;
    connectedCountry = countryCode;
    await connectGate?.future;
  }

  @override
  Future<void> disconnect() async {
    disconnectCalls++;
    await disconnectGate?.future;
  }

  @override
  Future<void> shutdown() async {
    shutdownCalls++;
  }

  @override
  Future<IrvioContribution> getContribution() async {
    if (contributionError case final error?) throw error;
    return contribution;
  }

  @override
  Future<IrvioStatus> getStatus() async {
    statusCalls++;
    return currentStatus;
  }

  @override
  Future<IrvioLogTail> getRecentLogs({int limit = 200}) async {
    _requireV11();
    recentLogsCalls++;
    if (!recentLogsStarted.isCompleted) recentLogsStarted.complete();
    await recentLogsGate?.future;
    return logTail;
  }

  @override
  Future<IrvioLogTail> subscribeLogs({
    required List<int> daemonIncarnation,
    required int afterSequence,
  }) async {
    _requireV11();
    logSubscribeCalls++;
    if (!logSubscribed.isCompleted) logSubscribed.complete();
    return IrvioLogTail(
      records: const [],
      lastSequence: afterSequence,
      daemonIncarnation: List<int>.unmodifiable(daemonIncarnation),
      gap: false,
    );
  }

  @override
  Future<IrvioBootstrapState> getBootstrapState() async {
    _requireV11();
    bootstrapStateCalls++;
    return bootstrapStateHandler?.call(bootstrapStateCalls) ?? bootstrapState;
  }

  @override
  Future<IrvioBootstrapState> importBootstrapBundle(String code) async {
    _requireV11();
    bootstrapImportCalls++;
    if (bootstrapImportError case final error?) throw error;
    bootstrapState =
        importedBootstrapState ??
        const IrvioBootstrapState(
          phase: IrvioBootstrapPhase.connecting,
          configuredCount: 1,
          currentlyReachableCount: 0,
        );
    return bootstrapState;
  }

  @override
  Future<IrvioGeneratedBootstrapBundle> generateBootstrapBundle({
    int preferredNodeCount = 4,
  }) async {
    _requireV11();
    bootstrapGenerateCalls++;
    return generatedBundle ??
        IrvioGeneratedBootstrapBundle(
          code: 'irvio-bootstrap:v1:test',
          nodeCount: 1,
          generatedAt: DateTime.now(),
        );
  }

  @override
  Future<void> deleteProfile() async {
    if (apiMinor < 2) {
      throw const IrvioApiException(
        IrvioErrorCode.unsupported,
        'Local API v1.2 is unavailable.',
      );
    }
    deleteProfileCalls++;
  }

  @override
  Future<IrvioAvailableCountries> getAvailableCountries({
    bool refresh = false,
  }) async {
    if (apiMinor < 3) {
      throw const IrvioApiException(
        IrvioErrorCode.unsupported,
        'Local API v1.3 is unavailable.',
      );
    }
    availableCountriesCalls++;
    lastAvailableCountriesRefresh = refresh;
    if (availableCountriesHandler != null) {
      return await availableCountriesHandler!(refresh);
    }
    await availableCountriesGate?.future;
    return availableCountries;
  }

  @override
  Future<void> setContribution(bool enabled) async {
    contribution = IrvioContribution(supported: true, enabled: enabled);
  }

  @override
  Future<void> subscribeEvents() async {
    subscribeCalls++;
  }

  void emit(IrvioStatus next, {int sequence = 1}) {
    currentStatus = next;
    events.add(IrvioStatusEvent(sequence: sequence, status: next));
  }

  void emitLog(IrvioLogRecord record, {List<int> incarnation = const [1]}) {
    logStream.add(
      IrvioLogEvent(daemonIncarnation: incarnation, record: record),
    );
  }

  void _requireV11() {
    if (apiMinor >= 1) return;
    throw const IrvioApiException(
      IrvioErrorCode.unsupported,
      'Local API v1.1 is unavailable.',
    );
  }

  @override
  Future<void> close() async {
    closeCalls++;
  }
}
