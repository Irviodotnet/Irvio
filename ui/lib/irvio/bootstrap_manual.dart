// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:convert';
import 'dart:io';

import 'package:fixnum/fixnum.dart';

import 'generated/irvio/local/v1/control.pb.dart' as wire;

String createManualBootstrapCode({
  required String peerId,
  required String literalIp,
  required int port,
}) {
  final normalizedPeer = peerId.trim();
  var normalizedIp = literalIp.trim();
  if (normalizedIp.startsWith('[') && normalizedIp.endsWith(']')) {
    normalizedIp = normalizedIp.substring(1, normalizedIp.length - 1);
  }
  final address = InternetAddress.tryParse(normalizedIp);
  if (normalizedPeer.isEmpty || address == null || port < 1 || port > 65535) {
    throw const FormatException('Enter a PeerID, literal IP and valid port.');
  }
  final bundle = wire.BootstrapBundle(
    formatVersion: 1,
    generatedAtUnixMs: Int64(DateTime.now().millisecondsSinceEpoch),
    nodes: [
      wire.BootstrapNode(
        peerId: normalizedPeer,
        endpoints: [wire.BootstrapEndpoint(ip: address.rawAddress, port: port)],
      ),
    ],
  );
  return 'irvio-bootstrap:v1:'
      '${base64Url.encode(bundle.writeToBuffer()).replaceAll('=', '')}';
}
