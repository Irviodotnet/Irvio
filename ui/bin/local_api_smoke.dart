// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:irvio/irvio/bootstrap_qr.dart';
import 'package:irvio/irvio/linux_irvio_client.dart';
import 'package:irvio/models/irvio_models.dart';
import 'package:image/image.dart' as image;
// ignore: depend_on_referenced_packages
import 'package:qr/qr.dart';

Future<void> main(List<String> arguments) async {
  try {
    final options = _Options.parse(arguments);
    final client = await LinuxIrvioClient.connectTo(socketPath: options.socket);
    try {
      switch (options.command) {
        case 'status':
          final status = await client.getStatus();
          stdout.writeln(_statusLine(status));
        case 'connect':
          await client.connect(options.country);
          stdout.writeln('connect=accepted country=${options.country}');
        case 'disconnect':
          await client.disconnect();
          stdout.writeln('disconnect=accepted');
        case 'delete-profile':
          await client.deleteProfile();
          stdout.writeln('delete-profile=accepted');
        case 'cycle':
          await _cycle(client, options.country);
        case 'observe-failover':
          await _observeFailover(client, options.country);
        case 'bootstrap-state':
          _printBootstrapState(await client.getBootstrapState());
        case 'import-bootstrap':
          final code = options.argument;
          if (code == null || code.isEmpty) {
            throw const FormatException('import-bootstrap requires a code');
          }
          _printBootstrapState(await client.importBootstrapBundle(code));
        case 'import-qr':
          final path = options.argument;
          if (path == null || path.isEmpty) {
            throw const FormatException('import-qr requires an image path');
          }
          final code = decodeIrvioBootstrapQr(await File(path).readAsBytes());
          _printBootstrapState(await client.importBootstrapBundle(code));
        case 'encode-qr':
          final code = options.argument;
          final path = options.secondArgument;
          if (code == null || code.isEmpty || path == null || path.isEmpty) {
            throw const FormatException('encode-qr requires CODE and PATH');
          }
          await _writeBootstrapQr(code, path);
          stdout.writeln('qr=$path');
        case 'share':
          final bundle = await client.generateBootstrapBundle();
          stdout.writeln(bundle.code);
        case 'logs':
          final tail = await client.getRecentLogs(limit: 20);
          stdout.writeln(
            jsonEncode({
              'incarnation': base64Url.encode(tail.daemonIncarnation),
              'last_sequence': tail.lastSequence,
              'gap': tail.gap,
              'records': tail.records
                  .map(
                    (record) => {
                      'sequence': record.sequence,
                      'timestamp': record.timestamp.toUtc().toIso8601String(),
                      'severity': record.severity.name,
                      'message': record.message,
                      'fields': record.fields,
                    },
                  )
                  .toList(growable: false),
            }),
          );
        default:
          throw const FormatException('unknown Local API smoke command');
      }
    } finally {
      await client.close();
    }
  } on Object catch (error) {
    stderr.writeln('IRVIO UI smoke failed: $error');
    exitCode = 1;
  }
}

Future<void> _writeBootstrapQr(String code, String path) async {
  final qr = QrImage(
    QrCode.fromData(data: code, errorCorrectLevel: QrErrorCorrectLevel.M),
  );
  const quietZone = 4;
  const moduleSize = 8;
  final dimension = (qr.moduleCount + quietZone * 2) * moduleSize;
  final raster = image.Image(width: dimension, height: dimension);
  image.fill(raster, color: image.ColorRgb8(255, 255, 255));
  for (var row = 0; row < qr.moduleCount; row++) {
    for (var column = 0; column < qr.moduleCount; column++) {
      if (!qr.isDark(row, column)) continue;
      for (var y = 0; y < moduleSize; y++) {
        for (var x = 0; x < moduleSize; x++) {
          raster.setPixelRgb(
            (column + quietZone) * moduleSize + x,
            (row + quietZone) * moduleSize + y,
            0,
            0,
            0,
          );
        }
      }
    }
  }
  await File(path).writeAsBytes(image.encodePng(raster), flush: true);
}

void _printBootstrapState(IrvioBootstrapState state) {
  stdout.writeln(
    jsonEncode({
      'phase': state.phase.name,
      'configured_count': state.configuredCount,
      'currently_reachable_count': state.currentlyReachableCount,
      'last_error_code': state.lastErrorCode?.name,
      'user_message': state.userMessage,
    }),
  );
}

Future<void> _observeFailover(LinuxIrvioClient client, String country) async {
  final initial = await client.getStatus();
  if (initial.state != IrvioConnectionState.connected ||
      initial.countryCode != country) {
    throw StateError('expected an existing connected $country session');
  }

  var sawRecovering = false;
  final recovered = Completer<IrvioStatus>();
  final subscription = client.statusEvents.listen((event) {
    final status = event.status;
    if (status.state == IrvioConnectionState.recovering) {
      sawRecovering = true;
      stdout.writeln(
        jsonEncode({
          'msg': 'P2_OBSERVER_RECOVERING',
          'sequence': event.sequence,
          'country': status.countryCode,
        }),
      );
    }
    if (sawRecovering &&
        status.state == IrvioConnectionState.connected &&
        !recovered.isCompleted) {
      recovered.complete(status);
    }
  });
  try {
    await client.subscribeEvents();
    stdout.writeln(
      jsonEncode({
        'msg': 'P2_OBSERVER_READY',
        'state': initial.state.name,
        'country': initial.countryCode,
      }),
    );
    final status = await recovered.future.timeout(const Duration(seconds: 90));
    if (status.countryCode != country) {
      throw StateError(
        'recovered country ${status.countryCode} does not match $country',
      );
    }
    stdout.writeln(
      jsonEncode({
        'msg': 'P2_OBSERVER_RECOVERED',
        'state': status.state.name,
        'country': status.countryCode,
      }),
    );
  } finally {
    await subscription.cancel();
  }
}

Future<void> _cycle(LinuxIrvioClient client, String country) async {
  var status = await client.getStatus();
  if (status.state != IrvioConnectionState.connected) {
    throw StateError('expected an existing connected session');
  }
  await client.subscribeEvents();

  final disconnected = client.statusEvents
      .firstWhere(
        (event) => event.status.state == IrvioConnectionState.disconnected,
      )
      .timeout(const Duration(seconds: 30));
  await client.disconnect();
  status = (await disconnected).status;

  final connected = client.statusEvents
      .firstWhere(
        (event) => event.status.state == IrvioConnectionState.connected,
      )
      .timeout(const Duration(seconds: 75));
  await client.connect(country);
  status = (await connected).status;
  if (status.countryCode != country) {
    throw StateError(
      'connected country ${status.countryCode} does not match $country',
    );
  }
  stdout.writeln('cycle=ok ${_statusLine(status)}');
}

String _statusLine(IrvioStatus status) {
  final connectedSince = status.connectedSince;
  final uptimeSeconds = connectedSince == null
      ? null
      : DateTime.now().difference(connectedSince).inSeconds.clamp(0, 1 << 31);
  return 'state=${status.state.name} country=${status.countryCode} '
      'connected_since=${connectedSince?.toUtc().toIso8601String() ?? '-'} '
      'uptime_seconds=${uptimeSeconds ?? '-'}';
}

class _Options {
  const _Options(this.socket, this.command, this.arguments);

  final String socket;
  final String command;
  final List<String> arguments;

  String? get argument => arguments.isEmpty ? null : arguments.first;

  String? get secondArgument => arguments.length < 2 ? null : arguments[1];

  String get country => argument?.toUpperCase() ?? 'DE';

  static _Options parse(List<String> arguments) {
    var socket = defaultIrvioSocketPath;
    var index = 0;
    if (arguments.length >= 2 && arguments.first == '--socket') {
      socket = arguments[1];
      index = 2;
    }
    if (index >= arguments.length) {
      throw const FormatException(
        'usage: local_api_smoke [--socket PATH] '
        'status|connect|disconnect|cycle|observe-failover|bootstrap-state|'
        'import-bootstrap|import-qr|encode-qr|share|logs|delete-profile '
        '[ARGUMENTS]',
      );
    }
    final command = arguments[index++];
    final operands = arguments.sublist(index);
    if (operands.length > (command == 'encode-qr' ? 2 : 1)) {
      throw const FormatException('too many arguments');
    }
    final argument = operands.isEmpty ? null : operands.first;
    final country = argument?.toUpperCase() ?? 'DE';
    if ((command == 'connect' ||
            command == 'cycle' ||
            command == 'observe-failover') &&
        !RegExp(r'^[A-Z]{2}$').hasMatch(country)) {
      throw const FormatException('country must be ISO alpha-2');
    }
    return _Options(socket, command, operands);
  }
}
