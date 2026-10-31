// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:io';

import 'package:image/image.dart' as image;

Future<void> main() async {
  final source = image.decodePng(
    await File('assets/irvio-globe.png').readAsBytes(),
  );
  if (source == null) throw const FormatException('IRVIO logo is not a PNG');
  final frames = [
    for (final size in [16, 24, 32, 48, 64, 128, 256])
      image.copyResize(
        source,
        width: size,
        height: size,
        interpolation: image.Interpolation.average,
      ),
  ];
  final bytes = image.IcoEncoder().encodeImages(frames);
  await File('assets/irvio.ico').writeAsBytes(bytes, flush: true);
  await File('windows/runner/resources/app_icon.ico')
      .writeAsBytes(bytes, flush: true);
}
