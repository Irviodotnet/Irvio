// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/widgets/country_flag.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  test('all bundled country flags decode offline', () async {
    final manifest = await AssetManifest.loadFromAssetBundle(rootBundle);
    final flags = manifest.listAssets().where(
      (path) => path.startsWith('assets/flags/') && path.endsWith('.png'),
    );
    expect(flags.length, greaterThanOrEqualTo(249));
    expect(flags, contains('assets/flags/cz.png'));
    for (final path in flags) {
      final data = await rootBundle.load(path);
      final codec = await ui.instantiateImageCodec(
        data.buffer.asUint8List(data.offsetInBytes, data.lengthInBytes),
      );
      final frame = await codec.getNextFrame();
      expect(frame.image.width, 96, reason: path);
      expect(frame.image.height, 72, reason: path);
      frame.image.dispose();
      codec.dispose();
    }
  });

  testWidgets('CZ uses the local image, not the globe', (tester) async {
    await tester.pumpWidget(const MaterialApp(home: CountryFlag(code: 'CZ')));
    await tester.pumpAndSettle();
    final image = tester.widget<Image>(find.byType(Image));
    expect((image.image as AssetImage).assetName, 'assets/flags/cz.png');
    expect(find.byIcon(Icons.public), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets('invalid and absent codes fall back safely', (tester) async {
    for (final code in ['../CZ', '', 'ZZ']) {
      await tester.pumpWidget(MaterialApp(home: CountryFlag(code: code)));
      await tester.pumpAndSettle();
      expect(find.byIcon(Icons.public), findsOneWidget);
      expect(tester.takeException(), isNull);
    }
  });
}
