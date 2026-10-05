// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

enum IrvioConnectionState {
  disconnected,
  connecting,
  connected,
  recovering,
  noNetwork,
  noExit,
  disconnecting,
  error,
}

enum IrvioErrorCode {
  invalidArgument,
  unsupportedApi,
  unauthorized,
  busy,
  notReady,
  unsupported,
  internal,
  protocol,
  unavailable,
}

class IrvioStatus {
  const IrvioStatus({
    required this.state,
    this.countryCode = '',
    this.exitCountryCode = '',
    this.latencyMs,
    this.connectedSince,
    this.errorMessage,
  });

  const IrvioStatus.disconnected()
    : state = IrvioConnectionState.disconnected,
      countryCode = '',
      exitCountryCode = '',
      latencyMs = null,
      connectedSince = null,
      errorMessage = null;

  final IrvioConnectionState state;
  final String countryCode;
  final String exitCountryCode;
  final int? latencyMs;
  final DateTime? connectedSince;
  final String? errorMessage;

  bool get isConnected => state == IrvioConnectionState.connected;
}

class IrvioContribution {
  const IrvioContribution({required this.supported, required this.enabled});

  final bool supported;
  final bool enabled;
}

class IrvioHello {
  const IrvioHello({
    required this.serverMajor,
    required this.serverMinor,
    required this.negotiatedMinor,
    required this.productVersion,
    required this.coreVersion,
  });

  final int serverMajor;
  final int serverMinor;
  final int negotiatedMinor;
  final String productVersion;
  final String coreVersion;

  bool get supportsV11 => negotiatedMinor >= 1;
  bool get supportsV12 => negotiatedMinor >= 2;
  bool get supportsV13 => negotiatedMinor >= 3;
  bool get supportsV14 => negotiatedMinor >= 4;
  bool get supportsTrafficStats => negotiatedMinor >= 5;
}

class IrvioTrafficStats {
  const IrvioTrafficStats({
    this.sessionId = 0,
    this.sampledAt,
    this.clients,
    this.myUploadBytes,
    this.myDownloadBytes,
    this.sharedUploadBytes,
    this.complete = false,
    this.active = false,
  });

  final int sessionId;
  final DateTime? sampledAt;
  final int? clients;
  final BigInt? myUploadBytes;
  final BigInt? myDownloadBytes;
  BigInt? get myTotalBytes => myUploadBytes != null && myDownloadBytes != null
      ? myUploadBytes! + myDownloadBytes!
      : null;
  final BigInt? sharedUploadBytes;
  final bool complete;
  final bool active;
}

enum IrvioAvailableCountriesStatus { ready, partial, empty, unavailable }

class IrvioAvailableCountries {
  const IrvioAvailableCountries({
    required this.status,
    required this.countryCodes,
    required this.generatedAt,
  });

  final IrvioAvailableCountriesStatus status;
  final List<String> countryCodes;
  final DateTime? generatedAt;
}

class IrvioStatusEvent {
  const IrvioStatusEvent({required this.sequence, required this.status});

  final int sequence;
  final IrvioStatus status;
}

enum IrvioLogSeverity { info, warning, error }

class IrvioLogRecord {
  const IrvioLogRecord({
    required this.sequence,
    required this.timestamp,
    required this.severity,
    required this.message,
    this.fields = const {},
  });

  final int sequence;
  final DateTime timestamp;
  final IrvioLogSeverity severity;
  final String message;
  final Map<String, String> fields;
}

class IrvioLogTail {
  const IrvioLogTail({
    required this.records,
    required this.lastSequence,
    required this.daemonIncarnation,
    required this.gap,
  });

  final List<IrvioLogRecord> records;
  final int lastSequence;
  final List<int> daemonIncarnation;
  final bool gap;
}

class IrvioLogEvent {
  const IrvioLogEvent({required this.daemonIncarnation, required this.record});

  final List<int> daemonIncarnation;
  final IrvioLogRecord record;
}

enum IrvioBootstrapPhase { empty, connecting, ready, degraded, failed }

class IrvioBootstrapState {
  const IrvioBootstrapState({
    required this.phase,
    required this.configuredCount,
    required this.currentlyReachableCount,
    this.lastErrorCode,
    this.userMessage,
  });

  final IrvioBootstrapPhase phase;
  final int configuredCount;
  final int currentlyReachableCount;
  final IrvioErrorCode? lastErrorCode;
  final String? userMessage;

  // Imported bundles support bounded discovery while the daemon is IDLE without live bootstrap sockets.
  bool get requiresOnboarding => configuredCount == 0;
}

class IrvioGeneratedBootstrapBundle {
  const IrvioGeneratedBootstrapBundle({
    required this.code,
    required this.nodeCount,
    required this.generatedAt,
  });

  final String code;
  final int nodeCount;
  final DateTime generatedAt;
}

class IrvioApiException implements Exception {
  const IrvioApiException(this.code, this.message);

  final IrvioErrorCode code;
  final String message;

  @override
  String toString() => message;
}
