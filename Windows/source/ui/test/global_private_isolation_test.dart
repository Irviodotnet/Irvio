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
import 'package:irvio/widgets/connection_control.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fake_irvio_client.dart';

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  for (final language in IrvioLanguage.values) {
    for (final status in [
      const IrvioStatus(state: IrvioConnectionState.hosting),
      const IrvioStatus(
        state: IrvioConnectionState.hosting,
        privateHostingWanted: true,
      ),
      const IrvioStatus(
        state: IrvioConnectionState.connected,
        privateTargetPeerId: 'friend',
      ),
      const IrvioStatus(
        state: IrvioConnectionState.recovering,
        privateTargetPeerId: 'friend',
      ),
    ]) {
      testWidgets('Global preserves private ${status.state} ${language.code} '
          '${status.isPrivate}', (tester) async {
        await tester.binding.setSurfaceSize(const Size(700, 900));
        addTearDown(() => tester.binding.setSurfaceSize(null));
        final client = FakeIrvioClient(currentStatus: status);
        final app = IrvioAppController(() async => client);
        await app.start();
        app.selectLanguage(language);
        await tester.pumpWidget(IrvioApp(controller: app));
        await tester.pumpAndSettle();
        expect(find.text(app.strings.text('chooseCountry')), findsOneWidget);
        final title = find.byKey(const Key('global-vpn-title'));
        expect(
          tester.widget<Text>(title).style,
          Theme.of(tester.element(title)).textTheme.headlineSmall,
        );
        expect(
          tester.getRect(find.text(app.strings.text('chooseCountry'))).top -
              tester.getRect(title).bottom,
          closeTo(16, 0.1),
        );
        expect(find.byKey(const Key('country-selector')), findsOneWidget);
        expect(find.byType(ConnectionControl), findsNothing);
        await tester.tap(find.byKey(const Key('global-open-private')));
        await tester.pumpAndSettle();
        expect(app.page, IrvioPage.privateNetwork);
        await tester.tap(find.text(app.strings.text('home')));
        await tester.pumpAndSettle();
        expect(app.page, IrvioPage.home);
        expect(app.status.state, status.state);
        expect(client.disconnectCalls, 0);
        expect(client.connectCalls, 0);
        expect(find.byType(ConnectionControl), findsNothing);
        client.emit(const IrvioStatus.disconnected());
        await tester.pumpAndSettle();
        expect(find.byType(ConnectionControl), findsOneWidget);
        expect(find.byKey(const Key('global-open-private')), findsNothing);
        expect(find.byKey(const Key('country-selector')), findsOneWidget);
        expect(tester.takeException(), isNull);
        app.dispose();
      });
    }
  }
}
