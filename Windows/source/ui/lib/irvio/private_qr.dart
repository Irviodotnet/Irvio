// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:typed_data';

import 'bootstrap_qr.dart';

const maxPrivateInvitationBytes = 2048;
final _privateLink = RegExp(r'^irvio-private:v1:[A-Za-z0-9_-]+$');

String _validate(String link) {
  final match = _privateLink.matchAsPrefix(link);
  if (link.length > maxPrivateInvitationBytes ||
      match == null ||
      match.end != link.length) {
    throw const FormatException('Invalid private invitation format.');
  }
  return link;
}

String decodeIrvioPrivateQr(Uint8List bytes) => _validate(
  decodeIrvioQr(
    bytes,
    prefix: 'irvio-private:v1:',
    maxTextBytes: maxPrivateInvitationBytes,
  ),
);

Uint8List encodeIrvioPrivateQr(String link) =>
    encodeIrvioBootstrapQr(_validate(link));
