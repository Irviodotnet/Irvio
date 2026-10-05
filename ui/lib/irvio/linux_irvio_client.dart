// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:fixnum/fixnum.dart';

import '../models/irvio_models.dart';
import 'generated/hydra/local/v1/control.pb.dart' as wire;
import 'irvio_client.dart';

const defaultIrvioSocketPath = '/run/irvio/control.sock';
const maxLocalApiFrame = 64 * 1024;
const _apiMajor = 1;
const _apiMinor = 5;
const _maxPendingRequests = 16;
const _requestTimeout = Duration(seconds: 5);
const _disconnectTimeout = Duration(seconds: 65);
const _productOperationTimeout = Duration(seconds: 50);

class LinuxIrvioClient implements IrvioClient {
  LinuxIrvioClient._(
    this._socket,
    this._responseTimeout,
    this._disconnectResponseTimeout,
  );

  final Socket _socket;
  final Duration _responseTimeout;
  final Duration _disconnectResponseTimeout;
  final _decoder = _FrameDecoder();
  final _pending = <int, Completer<wire.Response>>{};
  final _statusEvents = StreamController<IrvioStatusEvent>.broadcast();
  final _logEvents = StreamController<IrvioLogEvent>.broadcast();
  final _connectionErrors = StreamController<Object>.broadcast();
  StreamSubscription<Uint8List>? _subscription;
  Future<void> _writeTail = Future.value();
  int _nextRequestId = 0;
  int _negotiatedMinor = _apiMinor;
  bool _closed = false;
  late final IrvioHello _hello;

  static Future<LinuxIrvioClient> connectTo({
    String socketPath = defaultIrvioSocketPath,
    Duration timeout = _requestTimeout,
    Duration requestTimeout = _requestTimeout,
    Duration disconnectTimeout = _disconnectTimeout,
  }) async {
    final address = InternetAddress(socketPath, type: InternetAddressType.unix);
    final socket = await Socket.connect(address, 0, timeout: timeout);
    final client = LinuxIrvioClient._(socket, requestTimeout, disconnectTimeout)
      .._startReading();
    try {
      final response = await client._call(wire.Method.METHOD_HELLO);
      if (!response.hasHello() || response.hello.serverApiMajor != _apiMajor) {
        throw const IrvioApiException(
          IrvioErrorCode.unsupportedApi,
          'This IRVIO interface requires Local API v1.',
        );
      }
      client._hello = IrvioHello(
        serverMajor: response.hello.serverApiMajor,
        serverMinor: response.hello.serverApiMinor,
        negotiatedMinor: response.hello.negotiatedApiMinor,
        productVersion: response.hello.productVersion,
        coreVersion: response.hello.coreVersion,
      );
      if (client._hello.negotiatedMinor > _apiMinor ||
          client._hello.negotiatedMinor > client._hello.serverMinor) {
        throw const IrvioApiException(
          IrvioErrorCode.protocol,
          'The IRVIO service returned an invalid API negotiation.',
        );
      }
      client._negotiatedMinor = client._hello.negotiatedMinor;
      return client;
    } catch (_) {
      await client.close();
      rethrow;
    }
  }

  @override
  IrvioHello get hello => _hello;

  @override
  Stream<IrvioStatusEvent> get statusEvents => _statusEvents.stream;

  @override
  Stream<IrvioLogEvent> get logEvents => _logEvents.stream;

  @override
  Stream<Object> get connectionErrors => _connectionErrors.stream;

  void _startReading() {
    _subscription = _socket.listen(
      (chunk) {
        try {
          for (final bytes in _decoder.add(chunk)) {
            _handleEnvelope(wire.Envelope.fromBuffer(bytes));
          }
        } on Object catch (error) {
          _fail(error);
        }
      },
      onError: _fail,
      onDone: () => _fail(
        const IrvioApiException(
          IrvioErrorCode.unavailable,
          'The IRVIO service connection closed.',
        ),
      ),
      cancelOnError: true,
    );
  }

  void _handleEnvelope(wire.Envelope envelope) {
    if (envelope.hasResponse()) {
      final requestId = envelope.requestId.toInt();
      _pending.remove(requestId)?.complete(envelope.response);
      return;
    }
    if (envelope.hasEvent() && envelope.requestId == Int64.ZERO) {
      final event = envelope.event;
      if (event.hasStatusChanged() && event.statusChanged.hasStatus()) {
        _statusEvents.add(
          IrvioStatusEvent(
            sequence: event.sequence.toInt(),
            status: _mapStatus(event.statusChanged.status),
          ),
        );
      } else if (event.hasLogRecordAdded() &&
          event.logRecordAdded.hasRecord()) {
        _logEvents.add(
          IrvioLogEvent(
            daemonIncarnation: List<int>.unmodifiable(
              event.logRecordAdded.daemonIncarnation,
            ),
            record: _mapLogRecord(event.logRecordAdded.record),
          ),
        );
      }
      return;
    }
    _fail(
      const IrvioApiException(
        IrvioErrorCode.protocol,
        'The IRVIO service sent an unexpected message.',
      ),
    );
  }

  @override
  Future<IrvioStatus> getStatus() async {
    final response = await _call(wire.Method.METHOD_GET_STATUS);
    if (!response.hasStatus()) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'The IRVIO service returned no status.',
      );
    }
    return _mapStatus(response.status);
  }

  @override
  Future<IrvioTrafficStats> getTrafficStats() async {
    if (!hello.supportsTrafficStats) {
      throw const IrvioApiException(
        IrvioErrorCode.unsupported,
        'Traffic statistics unavailable.',
      );
    }
    final response = await _call(wire.Method.METHOD_GET_TRAFFIC_STATS);
    if (!response.hasTrafficStats()) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'Missing traffic statistics.',
      );
    }
    final value = response.trafficStats;
    return IrvioTrafficStats(
      sessionId: value.sessionId.toInt(),
      sampledAt: value.sampledAtUnixMs.isZero
          ? null
          : DateTime.fromMillisecondsSinceEpoch(
              value.sampledAtUnixMs.toInt(),
              isUtc: true,
            ),
      clients: value.hasClients() ? value.clients : null,
      myUploadBytes: value.hasMyUploadBytes()
          ? BigInt.parse(value.myUploadBytes.toStringUnsigned())
          : null,
      myDownloadBytes: value.hasMyDownloadBytes()
          ? BigInt.parse(value.myDownloadBytes.toStringUnsigned())
          : null,
      sharedUploadBytes: value.hasSharedUploadBytes()
          ? BigInt.parse(value.sharedUploadBytes.toStringUnsigned())
          : null,
      complete: value.complete,
      active: value.active,
    );
  }

  @override
  Future<void> connect(String countryCode) async {
    final response = await _call(
      wire.Method.METHOD_CONNECT,
      countryCode: countryCode,
      responseTimeout: _productOperationTimeout,
    );
    if (!response.accepted) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'The IRVIO service did not accept Connect.',
      );
    }
  }

  @override
  Future<void> disconnect() async {
    final response = await _call(
      wire.Method.METHOD_DISCONNECT,
      responseTimeout: _disconnectResponseTimeout,
    );
    if (!response.accepted) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'The IRVIO service did not accept Disconnect.',
      );
    }
  }

  @override
  Future<void> shutdown() async {
    if (!_hello.supportsV14) {
      throw const IrvioApiException(
        IrvioErrorCode.unsupported,
        'This IRVIO daemon does not support Shutdown.',
      );
    }
    final response = await _call(
      wire.Method.METHOD_SHUTDOWN,
      responseTimeout: _disconnectResponseTimeout,
    );
    if (!response.accepted) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'The IRVIO service did not complete Shutdown.',
      );
    }
  }

  @override
  Future<IrvioContribution> getContribution() async {
    final response = await _call(wire.Method.METHOD_GET_CONTRIBUTION);
    if (!response.hasContribution()) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'The IRVIO service returned no contribution status.',
      );
    }
    return IrvioContribution(
      supported: response.contribution.supported,
      enabled: response.contribution.enabled,
    );
  }

  @override
  Future<void> setContribution(bool enabled) async {
    final response = await _call(
      wire.Method.METHOD_SET_CONTRIBUTION,
      contributionEnabled: enabled,
    );
    if (!response.accepted) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'The IRVIO service did not accept the contribution setting.',
      );
    }
  }

  @override
  Future<void> subscribeEvents() async {
    final response = await _call(wire.Method.METHOD_SUBSCRIBE_EVENTS);
    if (!response.accepted) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'The IRVIO service did not accept event subscription.',
      );
    }
  }

  @override
  Future<IrvioLogTail> getRecentLogs({int limit = 200}) async {
    _requireV11();
    final response = await _call(
      wire.Method.METHOD_GET_RECENT_LOGS,
      getRecentLogs: wire.GetRecentLogsRequest(limit: limit),
    );
    if (!response.hasLogTail()) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'The IRVIO service returned no log tail.',
      );
    }
    return _mapLogTail(response.logTail);
  }

  @override
  Future<IrvioLogTail> subscribeLogs({
    required List<int> daemonIncarnation,
    required int afterSequence,
  }) async {
    _requireV11();
    final response = await _call(
      wire.Method.METHOD_SUBSCRIBE_LOGS,
      subscribeLogs: wire.SubscribeLogsRequest(
        daemonIncarnation: daemonIncarnation,
        afterSequence: Int64(afterSequence),
      ),
    );
    if (!response.accepted || !response.hasLogTail()) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'The IRVIO service did not accept log subscription.',
      );
    }
    return _mapLogTail(response.logTail);
  }

  @override
  Future<IrvioBootstrapState> getBootstrapState() async {
    _requireV11();
    final response = await _call(wire.Method.METHOD_GET_BOOTSTRAP_STATE);
    if (!response.hasBootstrapState()) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'The IRVIO service returned no bootstrap state.',
      );
    }
    return _mapBootstrapState(response.bootstrapState);
  }

  @override
  Future<IrvioBootstrapState> importBootstrapBundle(String code) async {
    _requireV11();
    final response = await _call(
      wire.Method.METHOD_IMPORT_BOOTSTRAP_BUNDLE,
      importBootstrapBundle: wire.ImportBootstrapBundleRequest(code: code),
    );
    if (!response.accepted || !response.hasBootstrapState()) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'The IRVIO service did not accept the bootstrap bundle.',
      );
    }
    return _mapBootstrapState(response.bootstrapState);
  }

  @override
  Future<IrvioGeneratedBootstrapBundle> generateBootstrapBundle({
    int preferredNodeCount = 4,
  }) async {
    _requireV11();
    final response = await _call(
      wire.Method.METHOD_GENERATE_BOOTSTRAP_BUNDLE,
      generateBootstrapBundle: wire.GenerateBootstrapBundleRequest(
        preferredNodeCount: preferredNodeCount,
      ),
    );
    if (!response.hasGeneratedBootstrapBundle()) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'The IRVIO service returned no bootstrap bundle.',
      );
    }
    final bundle = response.generatedBootstrapBundle;
    return IrvioGeneratedBootstrapBundle(
      code: bundle.code,
      nodeCount: bundle.nodeCount,
      generatedAt: DateTime.fromMillisecondsSinceEpoch(
        bundle.generatedAtUnixMs.toInt(),
      ),
    );
  }

  @override
  Future<void> deleteProfile() async {
    _requireV12();
    final response = await _call(
      wire.Method.METHOD_DELETE_PROFILE,
      deleteProfile: wire.DeleteProfileRequest(confirm: true),
    );
    if (!response.accepted) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'The IRVIO service did not accept profile deletion.',
      );
    }
  }

  @override
  Future<IrvioAvailableCountries> getAvailableCountries({
    bool refresh = false,
  }) async {
    _requireV13();
    final response = await _call(
      wire.Method.METHOD_GET_AVAILABLE_COUNTRIES,
      getAvailableCountries: wire.GetAvailableCountriesRequest(
        refresh: refresh,
      ),
      responseTimeout: _productOperationTimeout,
    );
    if (!response.hasAvailableCountries()) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'The IRVIO service returned no available locations.',
      );
    }
    return _mapAvailableCountries(response.availableCountries);
  }

  void _requireV11() {
    if (_hello.supportsV11) return;
    throw const IrvioApiException(
      IrvioErrorCode.unsupported,
      'This feature requires IRVIO Local API v1.1.',
    );
  }

  void _requireV12() {
    if (_hello.supportsV12) return;
    throw const IrvioApiException(
      IrvioErrorCode.unsupported,
      'Delete profile requires IRVIO Local API v1.2.',
    );
  }

  void _requireV13() {
    if (_hello.supportsV13) return;
    throw const IrvioApiException(
      IrvioErrorCode.unsupported,
      'Dynamic locations require IRVIO Local API v1.3.',
    );
  }

  Future<wire.Response> _call(
    wire.Method method, {
    String countryCode = '',
    bool contributionEnabled = false,
    wire.GetRecentLogsRequest? getRecentLogs,
    wire.SubscribeLogsRequest? subscribeLogs,
    wire.ImportBootstrapBundleRequest? importBootstrapBundle,
    wire.GenerateBootstrapBundleRequest? generateBootstrapBundle,
    wire.DeleteProfileRequest? deleteProfile,
    wire.GetAvailableCountriesRequest? getAvailableCountries,
    Duration? responseTimeout,
  }) async {
    if (_closed) {
      throw const IrvioApiException(
        IrvioErrorCode.unavailable,
        'The IRVIO service is unavailable.',
      );
    }
    if (_pending.length >= _maxPendingRequests) {
      throw const IrvioApiException(
        IrvioErrorCode.busy,
        'Too many pending local requests.',
      );
    }
    final id = ++_nextRequestId;
    final completer = Completer<wire.Response>();
    _pending[id] = completer;
    final envelope = wire.Envelope(
      apiMajor: _apiMajor,
      apiMinor: method == wire.Method.METHOD_HELLO
          ? _apiMinor
          : _negotiatedMinor,
      requestId: Int64(id),
      request: wire.Request(
        method: method,
        countryCode: countryCode,
        contributionEnabled: contributionEnabled,
        getRecentLogs: getRecentLogs,
        subscribeLogs: subscribeLogs,
        importBootstrapBundle: importBootstrapBundle,
        generateBootstrapBundle: generateBootstrapBundle,
        deleteProfile: deleteProfile,
        getAvailableCountries: getAvailableCountries,
      ),
    );
    try {
      await _write(envelope);
      final response = await completer.future.timeout(
        responseTimeout ?? _responseTimeout,
      );
      if (response.code != wire.ResultCode.RESULT_CODE_OK) {
        throw IrvioApiException(
          _mapErrorCode(response.code),
          response.userMessage.isEmpty
              ? 'The IRVIO service rejected the request.'
              : response.userMessage,
        );
      }
      return response;
    } on TimeoutException {
      throw const IrvioApiException(
        IrvioErrorCode.unavailable,
        'The IRVIO service did not respond in time.',
      );
    } finally {
      _pending.remove(id);
    }
  }

  Future<void> _write(wire.Envelope envelope) {
    final payload = envelope.writeToBuffer();
    if (payload.isEmpty || payload.length > maxLocalApiFrame) {
      throw const IrvioApiException(
        IrvioErrorCode.protocol,
        'Local API frame is outside the allowed size.',
      );
    }
    final header = ByteData(4)..setUint32(0, payload.length, Endian.big);
    final operation = _writeTail.then((_) async {
      _socket.add(header.buffer.asUint8List());
      _socket.add(payload);
      await _socket.flush();
    });
    _writeTail = operation.catchError((_) {});
    return operation;
  }

  void _fail(Object error) {
    if (_closed) return;
    _closed = true;
    for (final request in _pending.values) {
      if (!request.isCompleted) request.completeError(error);
    }
    _pending.clear();
    _connectionErrors.add(error);
    unawaited(_subscription?.cancel());
    unawaited(_socket.close());
  }

  @override
  Future<void> close() async {
    if (!_closed) {
      _closed = true;
      for (final request in _pending.values) {
        if (!request.isCompleted) {
          request.completeError(
            const IrvioApiException(
              IrvioErrorCode.unavailable,
              'The IRVIO interface was closed.',
            ),
          );
        }
      }
      _pending.clear();
    }
    await _subscription?.cancel();
    await _socket.close();
    await _statusEvents.close();
    await _logEvents.close();
    await _connectionErrors.close();
  }
}

IrvioLogRecord _mapLogRecord(wire.LogRecord record) => IrvioLogRecord(
  sequence: record.sequence.toInt(),
  timestamp: DateTime.fromMillisecondsSinceEpoch(
    record.timestampUnixMs.toInt(),
  ),
  severity: switch (record.severity) {
    wire.LogSeverity.LOG_SEVERITY_WARNING => IrvioLogSeverity.warning,
    wire.LogSeverity.LOG_SEVERITY_ERROR => IrvioLogSeverity.error,
    _ => IrvioLogSeverity.info,
  },
  message: record.message,
  fields: Map<String, String>.unmodifiable(record.fields),
);

IrvioLogTail _mapLogTail(wire.LogTail tail) => IrvioLogTail(
  records: List<IrvioLogRecord>.unmodifiable(tail.records.map(_mapLogRecord)),
  lastSequence: tail.lastSequence.toInt(),
  daemonIncarnation: List<int>.unmodifiable(tail.daemonIncarnation),
  gap: tail.gap,
);

IrvioBootstrapState _mapBootstrapState(wire.BootstrapState state) =>
    IrvioBootstrapState(
      phase: switch (state.phase) {
        wire.BootstrapPhase.BOOTSTRAP_PHASE_CONNECTING =>
          IrvioBootstrapPhase.connecting,
        wire.BootstrapPhase.BOOTSTRAP_PHASE_READY => IrvioBootstrapPhase.ready,
        wire.BootstrapPhase.BOOTSTRAP_PHASE_DEGRADED =>
          IrvioBootstrapPhase.degraded,
        wire.BootstrapPhase.BOOTSTRAP_PHASE_FAILED =>
          IrvioBootstrapPhase.failed,
        _ => IrvioBootstrapPhase.empty,
      },
      configuredCount: state.configuredCount,
      currentlyReachableCount: state.currentlyReachableCount,
      lastErrorCode: state.lastErrorCode == wire.ResultCode.RESULT_CODE_OK
          ? null
          : _mapErrorCode(state.lastErrorCode),
      userMessage: state.userMessage.isEmpty ? null : state.userMessage,
    );

IrvioAvailableCountries _mapAvailableCountries(
  wire.AvailableCountries available,
) => IrvioAvailableCountries(
  status: switch (available.status) {
    wire.AvailableCountriesStatus.AVAILABLE_COUNTRIES_STATUS_READY =>
      IrvioAvailableCountriesStatus.ready,
    wire.AvailableCountriesStatus.AVAILABLE_COUNTRIES_STATUS_PARTIAL =>
      IrvioAvailableCountriesStatus.partial,
    wire.AvailableCountriesStatus.AVAILABLE_COUNTRIES_STATUS_EMPTY =>
      IrvioAvailableCountriesStatus.empty,
    _ => IrvioAvailableCountriesStatus.unavailable,
  },
  countryCodes: List<String>.unmodifiable(available.countryCodes),
  generatedAt: available.generatedAtUnixMs > Int64.ZERO
      ? DateTime.fromMillisecondsSinceEpoch(available.generatedAtUnixMs.toInt())
      : null,
);

class _FrameDecoder {
  final _header = Uint8List(4);
  int _headerLength = 0;
  Uint8List? _payload;
  int _payloadLength = 0;

  Iterable<Uint8List> add(Uint8List chunk) sync* {
    var offset = 0;
    while (offset < chunk.length) {
      if (_payload == null) {
        final take = (4 - _headerLength).clamp(0, chunk.length - offset);
        _header.setRange(_headerLength, _headerLength + take, chunk, offset);
        _headerLength += take;
        offset += take;
        if (_headerLength < 4) continue;
        final length = ByteData.sublistView(_header).getUint32(0, Endian.big);
        if (length == 0 || length > maxLocalApiFrame) {
          throw const IrvioApiException(
            IrvioErrorCode.protocol,
            'The IRVIO service sent an invalid frame length.',
          );
        }
        _payload = Uint8List(length);
        _payloadLength = 0;
      }
      final payload = _payload!;
      final take = (payload.length - _payloadLength).clamp(
        0,
        chunk.length - offset,
      );
      payload.setRange(_payloadLength, _payloadLength + take, chunk, offset);
      _payloadLength += take;
      offset += take;
      if (_payloadLength == payload.length) {
        yield payload;
        _payload = null;
        _payloadLength = 0;
        _headerLength = 0;
      }
    }
  }
}

IrvioStatus _mapStatus(wire.Status status) {
  DateTime? connectedSince;
  if (status.hasConnection() && status.connection.connectedSinceUnixMs > 0) {
    connectedSince = DateTime.fromMillisecondsSinceEpoch(
      status.connection.connectedSinceUnixMs.toInt(),
    );
  }
  return IrvioStatus(
    state: switch (status.state) {
      wire.ConnectionState.CONNECTION_STATE_DISCONNECTED =>
        IrvioConnectionState.disconnected,
      wire.ConnectionState.CONNECTION_STATE_CONNECTING =>
        IrvioConnectionState.connecting,
      wire.ConnectionState.CONNECTION_STATE_CONNECTED =>
        IrvioConnectionState.connected,
      wire.ConnectionState.CONNECTION_STATE_RECOVERING =>
        IrvioConnectionState.recovering,
      wire.ConnectionState.CONNECTION_STATE_NO_NETWORK =>
        IrvioConnectionState.noNetwork,
      wire.ConnectionState.CONNECTION_STATE_NO_EXIT =>
        IrvioConnectionState.noExit,
      wire.ConnectionState.CONNECTION_STATE_DISCONNECTING =>
        IrvioConnectionState.disconnecting,
      _ => IrvioConnectionState.error,
    },
    countryCode: status.countryCode,
    exitCountryCode: status.hasConnection()
        ? status.connection.exitCountryCode
        : '',
    latencyMs: status.hasConnection() && status.connection.hasLatencyMs()
        ? status.connection.latencyMs
        : null,
    connectedSince: connectedSince,
    errorMessage: status.hasError() && status.error.userMessage.isNotEmpty
        ? status.error.userMessage
        : null,
  );
}

IrvioErrorCode _mapErrorCode(wire.ResultCode code) => switch (code) {
  wire.ResultCode.RESULT_CODE_INVALID_ARGUMENT =>
    IrvioErrorCode.invalidArgument,
  wire.ResultCode.RESULT_CODE_UNSUPPORTED_API => IrvioErrorCode.unsupportedApi,
  wire.ResultCode.RESULT_CODE_UNAUTHORIZED => IrvioErrorCode.unauthorized,
  wire.ResultCode.RESULT_CODE_BUSY => IrvioErrorCode.busy,
  wire.ResultCode.RESULT_CODE_NOT_READY => IrvioErrorCode.notReady,
  wire.ResultCode.RESULT_CODE_UNSUPPORTED => IrvioErrorCode.unsupported,
  wire.ResultCode.RESULT_CODE_INTERNAL => IrvioErrorCode.internal,
  _ => IrvioErrorCode.protocol,
};

Iterable<Uint8List> decodeLocalApiChunks(Iterable<Uint8List> chunks) sync* {
  final decoder = _FrameDecoder();
  for (final chunk in chunks) {
    yield* decoder.add(chunk);
  }
}
