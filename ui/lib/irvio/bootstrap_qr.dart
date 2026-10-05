// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:typed_data';

import 'package:image/image.dart' as image;
import 'package:qr_flutter/qr_flutter.dart' as qr;
import 'package:zxing2/qrcode.dart';

const maxBootstrapQrImageBytes = 10 * 1024 * 1024;
const maxBootstrapQrDimension = 4096;

Uint8List encodeIrvioBootstrapQr(String code) {
  final matrix = qr.QrImage(
    qr.QrCode.fromData(data: code, errorCorrectLevel: qr.QrErrorCorrectLevel.M),
  );
  const quietZone = 4;
  const moduleSize = 8;
  final dimension = (matrix.moduleCount + quietZone * 2) * moduleSize;
  final raster = image.Image(width: dimension, height: dimension);
  image.fill(raster, color: image.ColorRgb8(255, 255, 255));
  final black = image.ColorRgb8(0, 0, 0);
  for (var row = 0; row < matrix.moduleCount; row++) {
    for (var column = 0; column < matrix.moduleCount; column++) {
      if (!matrix.isDark(row, column)) continue;
      final x = (column + quietZone) * moduleSize;
      final y = (row + quietZone) * moduleSize;
      image.fillRect(
        raster,
        x1: x,
        y1: y,
        x2: x + moduleSize - 1,
        y2: y + moduleSize - 1,
        color: black,
      );
    }
  }
  return image.encodePng(raster);
}

String decodeIrvioBootstrapQr(Uint8List bytes) {
  if (bytes.isEmpty || bytes.length > maxBootstrapQrImageBytes) {
    throw const FormatException('The QR image is empty or too large.');
  }
  image.Image? decoded;
  try {
    decoded = image.decodeImage(bytes);
  } on Object {
    throw const FormatException(
      'The selected file is not a supported QR image.',
    );
  }
  if (decoded == null ||
      decoded.width <= 0 ||
      decoded.height <= 0 ||
      decoded.width > maxBootstrapQrDimension ||
      decoded.height > maxBootstrapQrDimension) {
    throw const FormatException(
      'The selected file is not a supported QR image.',
    );
  }
  final rgba = decoded
      .convert(numChannels: 4)
      .getBytes(order: image.ChannelOrder.rgba);
  final source = RGBLuminanceSource(
    decoded.width,
    decoded.height,
    rgba.buffer.asInt32List(rgba.offsetInBytes, rgba.lengthInBytes ~/ 4),
  );
  Result result;
  try {
    result = QRCodeReader().decode(BinaryBitmap(HybridBinarizer(source)));
  } on Object {
    throw const FormatException('No IRVIO bootstrap QR was found.');
  }
  final text = result.text.trim();
  if (!text.startsWith('irvio-bootstrap:')) {
    throw const FormatException('No IRVIO bootstrap QR was found.');
  }
  return text;
}
