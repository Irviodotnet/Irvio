// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/app/irvio_app.dart';
import 'package:irvio/app/irvio_app_controller.dart';
import 'package:irvio/l10n/irvio_localizations.dart';
import 'package:irvio/models/irvio_models.dart';
import 'package:irvio/widgets/traffic_summary.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fake_irvio_client.dart';

void main() {
  test('decimal upload formatter and uint64 boundaries', () {
    expect(formatTrafficBytes(null), '—');
    for (final entry in <String, String>{
      '0': '0 MB',
      '999999': '0 MB',
      '100000000': '100 MB',
      '999999999': '999 MB',
      '1000000000': '1 GB',
      '1200000000': '1.2 GB',
      '1250000000': '1.3 GB',
      '1000000000000': '1 TB',
      '18446744073709551615': '18446744.1 TB',
    }.entries) {
      expect(formatTrafficBytes(BigInt.parse(entry.key)), entry.value);
    }
  });

  for (final language in IrvioLanguage.values) {
    testWidgets('sidebar statistics fit default window ${language.code}', (
      tester,
    ) async {
      SharedPreferences.setMockInitialValues({'language': language.code});
      await tester.binding.setSurfaceSize(const Size(549, 682));
      addTearDown(() => tester.binding.setSurfaceSize(null));
      final fake = FakeIrvioClient(apiMinor: 5);
      fake.trafficStats = IrvioTrafficStats(
        clients: 1,
        myUploadBytes: BigInt.from(300000000),
        myDownloadBytes: BigInt.from(700000000),
        sharedUploadBytes: BigInt.from(1200000000),
        complete: true,
      );
      final controller = IrvioAppController(() async => fake);
      await controller.start();
      await tester.pumpWidget(IrvioApp(controller: controller));
      await tester.pump();
      expect(find.text('1.2 GB'), findsOneWidget);
      expect(find.text('1 GB'), findsOneWidget);
      expect(tester.takeException(), isNull);
      controller.dispose();
    });
  }

  test('own total requires both directions and does not overflow uint64', () {
    expect(IrvioTrafficStats(myUploadBytes: BigInt.one).myTotalBytes, isNull);
    final max = BigInt.parse('18446744073709551615');
    expect(
      IrvioTrafficStats(myUploadBytes: max, myDownloadBytes: max).myTotalBytes,
      max * BigInt.two,
    );
  });
}
