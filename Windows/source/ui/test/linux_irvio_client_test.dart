// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

@TestOn('linux')
library;

import 'dart:io';
import 'dart:typed_data';

import 'package:fixnum/fixnum.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/irvio/generated/irvio/local/v1/control.pb.dart' as wire;
import 'package:irvio/irvio/linux_irvio_client.dart';
import 'package:irvio/models/irvio_models.dart';

void main() {
  test('HOSTING remains distinct from connected and disconnected', () async {
    final fixture = await _LocalApiFixture.start(apiMinor: 6);
    addTearDown(fixture.close);
    fixture.statusState = wire.ConnectionState.CONNECTION_STATE_HOSTING;
    fixture.privateTarget = 'pinned-peer';
    final client = await LinuxIrvioClient.connectTo(socketPath: fixture.path);
    addTearDown(client.close);
    final status = await client.getStatus();
    expect(status.state, IrvioConnectionState.hosting);
    expect(status.isConnected, isFalse);
    expect(status.privateTargetPeerId, 'pinned-peer');
    expect(status.privateHostingWanted, isTrue);
  });

  test('private capability needs both version and backend support', () async {
    for (final minor in [5, 6]) {
      final fixture = await _LocalApiFixture.start(apiMinor: minor);
      addTearDown(fixture.close);
      final client = await LinuxIrvioClient.connectTo(socketPath: fixture.path);
      addTearDown(client.close);
      expect(client.hello.supportsPrivateNetwork, isFalse);
      await expectLater(
        client.getPrivateState(),
        throwsA(isA<IrvioApiException>()),
      );
      await expectLater(
        client.privateCommand(wire.PrivateCommand()),
        throwsA(isA<IrvioApiException>()),
      );
      expect(fixture.methods, [wire.Method.METHOD_HELLO]);
    }
  });

  test('private commands and state use negotiated wire fields', () async {
    final fixture = await _LocalApiFixture.start(apiMinor: 6);
    addTearDown(fixture.close);
    fixture.privateSupported = true;
    fixture.privateResponse = (request) {
      if (request.method == wire.Method.METHOD_GET_PRIVATE_STATE) {
        expect(request.getPrivateState.offset, 0);
        expect(request.getPrivateState.limit, 64);
        return wire.Response(
          code: wire.ResultCode.RESULT_CODE_OK,
          privateState: wire.PrivateState(localPeerId: 'local'),
        );
      }
      expect(
        request.privateCommand.action,
        wire.PrivateAction.PRIVATE_ACTION_CONNECT,
      );
      expect(request.privateCommand.hosting, isTrue);
      expect(request.privateCommand.peerId, isEmpty);
      return wire.Response(
        code: wire.ResultCode.RESULT_CODE_OK,
        accepted: true,
        privateResult: wire.PrivateCommandResult(),
      );
    };
    final client = await LinuxIrvioClient.connectTo(socketPath: fixture.path);
    addTearDown(client.close);
    expect(client.hello.supportsPrivateNetwork, isTrue);
    expect((await client.getPrivateState()).localPeerId, 'local');
    await client.privateCommand(
      wire.PrivateCommand(
        action: wire.PrivateAction.PRIVATE_ACTION_CONNECT,
        hosting: true,
      ),
    );
    expect(fixture.minors.first, 7);
    expect(fixture.minors.skip(1), everyElement(6));
  });

  test('traffic API preserves unsigned bytes and optional zero', () async {
    final fixture = await _LocalApiFixture.start(apiMinor: 5);
    addTearDown(fixture.close);
    final client = await LinuxIrvioClient.connectTo(socketPath: fixture.path);
    addTearDown(client.close);
    final stats = await client.getTrafficStats();
    expect(stats.clients, 0);
    expect(stats.myUploadBytes, BigInt.parse('18446744073709551615'));
    expect(stats.myDownloadBytes, BigInt.parse('18446744073709551615'));
    expect(stats.myTotalBytes, BigInt.parse('36893488147419103230'));
    expect(stats.sharedUploadBytes, null);
  });

  test('old daemon does not receive traffic requests', () async {
    final fixture = await _LocalApiFixture.start(apiMinor: 4);
    addTearDown(fixture.close);
    final client = await LinuxIrvioClient.connectTo(socketPath: fixture.path);
    addTearDown(client.close);
    await expectLater(
      client.getTrafficStats(),
      throwsA(isA<IrvioApiException>()),
    );
    expect(
      fixture.methods,
      isNot(contains(wire.Method.METHOD_GET_TRAFFIC_STATS)),
    );
  });
  test(
    'Linux client speaks the exact Local API framing and protobuf contract',
    () async {
      final fixture = await _LocalApiFixture.start();
      addTearDown(fixture.close);

      final client = await LinuxIrvioClient.connectTo(socketPath: fixture.path);
      addTearDown(client.close);
      expect(client.hello.serverMajor, 1);
      expect(client.hello.negotiatedMinor, 0);
      expect(client.hello.coreVersion, 'v1');

      final status = await client.getStatus();
      expect(status.state, IrvioConnectionState.connected);
      expect(status.countryCode, 'DE');

      await expectLater(
        client.getContribution(),
        throwsA(
          isA<IrvioApiException>().having(
            (error) => error.code,
            'code',
            IrvioErrorCode.unsupported,
          ),
        ),
      );
      await client.connect('DE');
      await client.disconnect();
      await expectLater(
        client.getRecentLogs(),
        throwsA(
          isA<IrvioApiException>().having(
            (error) => error.code,
            'code',
            IrvioErrorCode.unsupported,
          ),
        ),
      );

      final nextEvent = client.statusEvents.first;
      await client.subscribeEvents();
      expect((await nextEvent).status.state, IrvioConnectionState.recovering);
      expect(
        fixture.methods,
        containsAll(<wire.Method>[
          wire.Method.METHOD_HELLO,
          wire.Method.METHOD_GET_STATUS,
          wire.Method.METHOD_CONNECT,
          wire.Method.METHOD_DISCONNECT,
          wire.Method.METHOD_GET_CONTRIBUTION,
          wire.Method.METHOD_SUBSCRIBE_EVENTS,
        ]),
      );
      expect(
        fixture.methods,
        isNot(contains(wire.Method.METHOD_GET_RECENT_LOGS)),
      );
      expect(fixture.minors.skip(1).every((minor) => minor == 0), isTrue);
    },
  );

  test('Linux client uses additive Local API v1.1 methods', () async {
    final fixture = await _LocalApiFixture.start(apiMinor: 1);
    addTearDown(fixture.close);
    final client = await LinuxIrvioClient.connectTo(socketPath: fixture.path);
    addTearDown(client.close);

    expect(client.hello.supportsV11, isTrue);
    final tail = await client.getRecentLogs();
    expect(tail.records.single.message, 'BOOTSTRAP_CONNECT_SUCCESS');
    expect((await client.getBootstrapState()).phase, IrvioBootstrapPhase.empty);
    expect(
      (await client.importBootstrapBundle('irvio-bootstrap:v1:test')).phase,
      IrvioBootstrapPhase.connecting,
    );
    expect((await client.generateBootstrapBundle()).nodeCount, 1);
    await expectLater(
      client.deleteProfile(),
      throwsA(
        isA<IrvioApiException>().having(
          (error) => error.code,
          'code',
          IrvioErrorCode.unsupported,
        ),
      ),
    );
    expect(fixture.methods, isNot(contains(wire.Method.METHOD_DELETE_PROFILE)));
    final event = client.logEvents.first;
    await client.subscribeLogs(
      daemonIncarnation: tail.daemonIncarnation,
      afterSequence: tail.lastSequence,
    );
    expect((await event).record.message, 'DHT_BOOTSTRAP_COMPLETE');
  });

  test(
    'Linux client sends confirmed Delete profile only with API v1.2',
    () async {
      final fixture = await _LocalApiFixture.start(apiMinor: 2);
      addTearDown(fixture.close);
      final client = await LinuxIrvioClient.connectTo(socketPath: fixture.path);
      addTearDown(client.close);

      expect(client.hello.supportsV12, isTrue);
      await client.deleteProfile();
      expect(fixture.methods, contains(wire.Method.METHOD_DELETE_PROFILE));
      expect(fixture.deleteProfileConfirmed, isTrue);
    },
  );

  test('Linux client maps dynamic countries through Local API v1.3', () async {
    final fixture = await _LocalApiFixture.start(apiMinor: 3);
    addTearDown(fixture.close);
    final client = await LinuxIrvioClient.connectTo(socketPath: fixture.path);
    addTearDown(client.close);

    expect(client.hello.supportsV13, isTrue);
    final available = await client.getAvailableCountries(refresh: true);
    expect(available.status, IrvioAvailableCountriesStatus.partial);
    expect(available.countryCodes, ['DE', 'NL']);
    expect(
      fixture.methods,
      contains(wire.Method.METHOD_GET_AVAILABLE_COUNTRIES),
    );
  });

  test('Linux client gives Disconnect its bounded teardown budget', () async {
    final fixture = await _LocalApiFixture.start(
      disconnectDelay: const Duration(milliseconds: 100),
    );
    addTearDown(fixture.close);
    final client = await LinuxIrvioClient.connectTo(
      socketPath: fixture.path,
      requestTimeout: const Duration(milliseconds: 30),
      disconnectTimeout: const Duration(milliseconds: 250),
    );
    addTearDown(client.close);

    await client.disconnect();
    expect(fixture.methods, contains(wire.Method.METHOD_DISCONNECT));
  });

  test('Linux client sends Shutdown only with Local API v1.4', () async {
    final oldFixture = await _LocalApiFixture.start(apiMinor: 3);
    addTearDown(oldFixture.close);
    final oldClient = await LinuxIrvioClient.connectTo(
      socketPath: oldFixture.path,
    );
    addTearDown(oldClient.close);
    await expectLater(oldClient.shutdown(), throwsA(isA<IrvioApiException>()));
    expect(oldFixture.methods, isNot(contains(wire.Method.METHOD_SHUTDOWN)));

    final fixture = await _LocalApiFixture.start(apiMinor: 4);
    addTearDown(fixture.close);
    final client = await LinuxIrvioClient.connectTo(socketPath: fixture.path);
    addTearDown(client.close);
    expect(client.hello.supportsV14, isTrue);
    await client.shutdown();
    expect(fixture.methods, contains(wire.Method.METHOD_SHUTDOWN));
  });
}

class _LocalApiFixture {
  _LocalApiFixture(
    this.directory,
    this.path,
    this.server,
    this.apiMinor,
    this.disconnectDelay,
  );

  final Directory directory;
  final String path;
  final ServerSocket server;
  final int apiMinor;
  final Duration disconnectDelay;
  final methods = <wire.Method>[];
  final minors = <int>[];
  bool deleteProfileConfirmed = false;
  bool privateSupported = false;
  String privateTarget = '';
  wire.ConnectionState statusState =
      wire.ConnectionState.CONNECTION_STATE_CONNECTED;
  wire.Response Function(wire.Request)? privateResponse;
  final sockets = <Socket>[];

  static Future<_LocalApiFixture> start({
    int apiMinor = 0,
    Duration disconnectDelay = Duration.zero,
  }) async {
    final directory = await Directory.systemTemp.createTemp(
      'irvio-ui-localapi-',
    );
    final path = '${directory.path}/control.sock';
    final server = await ServerSocket.bind(
      InternetAddress(path, type: InternetAddressType.unix),
      0,
    );
    final fixture = _LocalApiFixture(
      directory,
      path,
      server,
      apiMinor,
      disconnectDelay,
    );
    server.listen(fixture._accept);
    return fixture;
  }

  void _accept(Socket socket) {
    sockets.add(socket);
    final decoder = _FixtureDecoder();
    var writeTail = Future<void>.value();
    socket.listen((chunk) {
      for (final frame in decoder.add(chunk)) {
        final envelope = wire.Envelope.fromBuffer(frame);
        final method = envelope.request.method;
        methods.add(method);
        minors.add(envelope.apiMinor);
        if (method == wire.Method.METHOD_DELETE_PROFILE) {
          deleteProfileConfirmed =
              envelope.request.hasDeleteProfile() &&
              envelope.request.deleteProfile.confirm;
        }
        final response =
            (method == wire.Method.METHOD_GET_PRIVATE_STATE ||
                    method == wire.Method.METHOD_PRIVATE_COMMAND) &&
                privateResponse != null
            ? privateResponse!(envelope.request)
            : _responseFor(method);
        writeTail = writeTail.then((_) async {
          if (method == wire.Method.METHOD_DISCONNECT &&
              disconnectDelay > Duration.zero) {
            await Future<void>.delayed(disconnectDelay);
          }
          await _write(
            socket,
            wire.Envelope(
              apiMajor: 1,
              apiMinor: 0,
              requestId: envelope.requestId,
              response: response,
            ),
          );
        });
        if (method == wire.Method.METHOD_SUBSCRIBE_EVENTS) {
          writeTail = writeTail.then(
            (_) => _write(
              socket,
              wire.Envelope(
                apiMajor: 1,
                apiMinor: 0,
                requestId: Int64.ZERO,
                event: wire.Event(
                  sequence: Int64.ONE,
                  statusChanged: wire.StatusChanged(
                    status: wire.Status(
                      state: wire.ConnectionState.CONNECTION_STATE_RECOVERING,
                      countryCode: 'DE',
                    ),
                  ),
                ),
              ),
            ),
          );
        } else if (method == wire.Method.METHOD_SUBSCRIBE_LOGS) {
          writeTail = writeTail.then(
            (_) => _write(
              socket,
              wire.Envelope(
                apiMajor: 1,
                apiMinor: apiMinor,
                requestId: Int64.ZERO,
                event: wire.Event(
                  sequence: Int64(2),
                  logRecordAdded: wire.LogRecordAdded(
                    daemonIncarnation: const [7],
                    record: wire.LogRecord(
                      sequence: Int64(2),
                      timestampUnixMs: Int64(42),
                      severity: wire.LogSeverity.LOG_SEVERITY_INFO,
                      message: 'DHT_BOOTSTRAP_COMPLETE',
                    ),
                  ),
                ),
              ),
            ),
          );
        }
      }
    });
  }

  wire.Response _responseFor(wire.Method method) => switch (method) {
    wire.Method.METHOD_GET_TRAFFIC_STATS => wire.Response(
      code: wire.ResultCode.RESULT_CODE_OK,
      trafficStats: wire.TrafficStats(
        clients: 0,
        myUploadBytes: Int64(-1),
        myDownloadBytes: Int64(-1),
      ),
    ),
    wire.Method.METHOD_HELLO => wire.Response(
      code: wire.ResultCode.RESULT_CODE_OK,
      hello: wire.Hello(
        serverApiMajor: 1,
        serverApiMinor: apiMinor,
        negotiatedApiMinor: apiMinor,
        productVersion: 'fixture',
        coreVersion: 'v1',
        privateNetworkSupported: privateSupported,
      ),
    ),
    wire.Method.METHOD_GET_STATUS => wire.Response(
      code: wire.ResultCode.RESULT_CODE_OK,
      status: wire.Status(
        state: statusState,
        privateTargetPeerId: privateTarget,
        privateHostingWanted: privateTarget.isNotEmpty,
        countryCode: 'DE',
        connection: wire.ConnectionInfo(exitCountryCode: 'DE'),
      ),
    ),
    wire.Method.METHOD_GET_CONTRIBUTION => wire.Response(
      code: wire.ResultCode.RESULT_CODE_UNSUPPORTED,
      userMessage: 'Contribution intent is not available in v1.0',
    ),
    wire.Method.METHOD_GET_RECENT_LOGS => wire.Response(
      code: wire.ResultCode.RESULT_CODE_OK,
      logTail: wire.LogTail(
        records: [
          wire.LogRecord(
            sequence: Int64.ONE,
            timestampUnixMs: Int64(21),
            severity: wire.LogSeverity.LOG_SEVERITY_INFO,
            message: 'BOOTSTRAP_CONNECT_SUCCESS',
          ),
        ],
        lastSequence: Int64.ONE,
        daemonIncarnation: const [7],
      ),
    ),
    wire.Method.METHOD_SUBSCRIBE_LOGS => wire.Response(
      code: wire.ResultCode.RESULT_CODE_OK,
      accepted: true,
      logTail: wire.LogTail(
        lastSequence: Int64.ONE,
        daemonIncarnation: const [7],
      ),
    ),
    wire.Method.METHOD_GET_BOOTSTRAP_STATE => wire.Response(
      code: wire.ResultCode.RESULT_CODE_OK,
      bootstrapState: wire.BootstrapState(
        phase: wire.BootstrapPhase.BOOTSTRAP_PHASE_EMPTY,
      ),
    ),
    wire.Method.METHOD_IMPORT_BOOTSTRAP_BUNDLE => wire.Response(
      code: wire.ResultCode.RESULT_CODE_OK,
      accepted: true,
      bootstrapState: wire.BootstrapState(
        phase: wire.BootstrapPhase.BOOTSTRAP_PHASE_CONNECTING,
        configuredCount: 1,
      ),
    ),
    wire.Method.METHOD_GENERATE_BOOTSTRAP_BUNDLE => wire.Response(
      code: wire.ResultCode.RESULT_CODE_OK,
      generatedBootstrapBundle: wire.GeneratedBootstrapBundle(
        code: 'irvio-bootstrap:v1:test',
        nodeCount: 1,
        generatedAtUnixMs: Int64(42),
      ),
    ),
    wire.Method.METHOD_GET_AVAILABLE_COUNTRIES => wire.Response(
      code: wire.ResultCode.RESULT_CODE_OK,
      availableCountries: wire.AvailableCountries(
        status:
            wire.AvailableCountriesStatus.AVAILABLE_COUNTRIES_STATUS_PARTIAL,
        countryCodes: const ['DE', 'NL'],
        generatedAtUnixMs: Int64(42),
      ),
    ),
    _ => wire.Response(code: wire.ResultCode.RESULT_CODE_OK, accepted: true),
  };

  Future<void> _write(Socket socket, wire.Envelope envelope) async {
    final payload = envelope.writeToBuffer();
    final header = ByteData(4)..setUint32(0, payload.length, Endian.big);
    socket.add(header.buffer.asUint8List());
    socket.add(payload);
    await socket.flush();
  }

  Future<void> close() async {
    for (final socket in sockets) {
      await socket.close();
    }
    await server.close();
    if (await directory.exists()) await directory.delete(recursive: true);
  }
}

class _FixtureDecoder {
  final bytes = <int>[];

  Iterable<Uint8List> add(Uint8List chunk) sync* {
    bytes.addAll(chunk);
    while (bytes.length >= 4) {
      final header = Uint8List.fromList(bytes.take(4).toList());
      final length = ByteData.sublistView(header).getUint32(0, Endian.big);
      if (bytes.length < 4 + length) return;
      yield Uint8List.fromList(bytes.sublist(4, 4 + length));
      bytes.removeRange(0, 4 + length);
    }
  }
}
