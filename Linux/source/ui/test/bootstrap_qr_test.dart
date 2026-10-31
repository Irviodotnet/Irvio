// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/irvio/bootstrap_manual.dart';
import 'package:irvio/irvio/bootstrap_qr.dart';
import 'package:image/image.dart' as image;
import 'package:qr/qr.dart';

void main() {
  test('exported PNG preserves bootstrap code and opaque white quiet zone', () {
    final code = createManualBootstrapCode(
      peerId: '12D3KooWN2M6B9pp1CW6rjeKca2hukYqrwgS55biswiGmM1R3j9x',
      literalIp: '203.0.113.12',
      port: 4001,
    );
    expect(code, startsWith('irvio-bootstrap:v1:'));
    final bytes = encodeIrvioBootstrapQr(code);
    final raster = image.decodePng(bytes)!;
    expect(raster.width, raster.height);
    expect(raster.getPixel(0, 0).r, 255);
    expect(raster.getPixel(0, 0).g, 255);
    expect(raster.getPixel(0, 0).b, 255);
    expect(raster.getPixel(0, 0).a, 255);
    expect(decodeIrvioBootstrapQr(bytes), code);
  });

  test('generated QR decodes to the exact bootstrap text', () {
    final code = createManualBootstrapCode(
      peerId: '12D3KooWN2M6B9pp1CW6rjeKca2hukYqrwgS55biswiGmM1R3j9x',
      literalIp: '203.0.113.12',
      port: 4001,
    );
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

    expect(decodeIrvioBootstrapQr(image.encodePng(raster)), code);
  });

  test('non-image bootstrap import is rejected cleanly', () {
    expect(
      () => decodeIrvioBootstrapQr(Uint8List.fromList([1, 2, 3])),
      throwsFormatException,
    );
  });
}
