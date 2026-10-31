// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import '../models/irvio_models.dart';

abstract interface class IrvioClient {
  IrvioHello get hello;

  Stream<IrvioStatusEvent> get statusEvents;

  Stream<IrvioLogEvent> get logEvents;

  Stream<Object> get connectionErrors;

  Future<IrvioStatus> getStatus();

  Future<IrvioTrafficStats> getTrafficStats();

  Future<void> connect(String countryCode);

  Future<void> disconnect();

  Future<void> shutdown();

  Future<IrvioContribution> getContribution();

  Future<void> setContribution(bool enabled);

  Future<void> subscribeEvents();

  Future<IrvioLogTail> getRecentLogs({int limit = 200});

  Future<IrvioLogTail> subscribeLogs({
    required List<int> daemonIncarnation,
    required int afterSequence,
  });

  Future<IrvioBootstrapState> getBootstrapState();

  Future<IrvioBootstrapState> importBootstrapBundle(String code);

  Future<IrvioGeneratedBootstrapBundle> generateBootstrapBundle({
    int preferredNodeCount = 4,
  });

  Future<void> deleteProfile();

  Future<IrvioAvailableCountries> getAvailableCountries({bool refresh = false});

  Future<void> close();
}

typedef IrvioClientFactory = Future<IrvioClient> Function();
