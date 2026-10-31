// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:image/image.dart' as image;
import 'package:irvio/irvio/bootstrap_qr.dart';
import 'package:irvio/irvio/private_qr.dart';

void main() {
  test('JPEG QR also preserves the private link', () {
    const link = 'irvio-private:v1:ABC_def-012';
    final raster = image.decodePng(encodeIrvioPrivateQr(link))!;
    expect(decodeIrvioPrivateQr(image.encodeJpg(raster, quality: 95)), link);
  });

  test('private QR roundtrips with white quiet zone and remains separate from bootstrap', () {
    const link = 'irvio-private:v1:ABC_def-012';
    final bytes = encodeIrvioPrivateQr(link);
    expect(decodeIrvioPrivateQr(bytes), link);
    expect(image.decodePng(bytes)!.getPixel(0, 0).r, 255);
    expect(() => decodeIrvioBootstrapQr(bytes), throwsFormatException);
    expect(
      () => decodeIrvioPrivateQr(
        encodeIrvioBootstrapQr('irvio-bootstrap:v1:test'),
      ),
      throwsFormatException,
    );
  });

  test('private format bounds reject invalid schemes, unicode, whitespace and oversize', () {
    for (final link in [
      'irvio-private:v2:ABC',
      'irvio-private:v1:',
      'irvio-private:v1:A B',
      'irvio-private:v1:ABC\n',
      'irvio-private:v1:АБВ',
      'irvio-private:v1:${'A' * 2048}',
    ]) {
      expect(() => encodeIrvioPrivateQr(link), throwsFormatException);
    }
  });

  test('malformed, oversized and oversized-dimension images fail closed', () {
    for (final bytes in [
      Uint8List(0),
      Uint8List.fromList([1, 2, 3]),
      Uint8List(maxBootstrapQrImageBytes + 1),
      image.encodePng(
        image.Image(width: maxBootstrapQrDimension + 1, height: 1),
      ),
    ]) {
      expect(() => decodeIrvioPrivateQr(bytes), throwsFormatException);
    }
  });
}
