// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:fixnum/fixnum.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/app/irvio_app.dart';
import 'package:irvio/app/irvio_app_controller.dart';
import 'package:irvio/irvio/generated/irvio/local/v1/control.pb.dart' as wire;
import 'package:irvio/irvio/private_irvio_client.dart';
import 'package:irvio/models/irvio_models.dart';
import 'package:irvio/l10n/irvio_localizations.dart';
import 'package:irvio/main.dart' show irvioInitialWindowSize;
import 'package:qr_flutter/qr_flutter.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:irvio/widgets/connection_control.dart';
import 'package:irvio/widgets/private_peer_selector.dart';

import 'fake_irvio_client.dart';

class _PrivateClient extends FakeIrvioClient implements PrivateIrvioClient {
  _PrivateClient() : super(apiMinor: 7);
  final commands = <wire.PrivateCommand>[];
  bool enrollmentFlow = false;
  bool approved = false;
  Completer<void>? hostingGate;
  Completer<void>? privateDisconnectGate;
  int privateReads = 0;
  wire.PrivateState privateState = wire.PrivateState();
  final lifecycle = <String>[];
  @override
  Future<void> disconnect() async {
    lifecycle.add('disconnect');
    await super.disconnect();
    await privateDisconnectGate?.future;
    currentStatus = const IrvioStatus.disconnected();
    emit(currentStatus);
    privateState.targetPeerId = '';
  }

  @override
  IrvioHello get hello => const IrvioHello(
    serverMajor: 1,
    serverMinor: 7,
    negotiatedMinor: 7,
    productVersion: 'fixture',
    coreVersion: 'fixture',
    privateNetworkSupported: true,
  );
  @override
  Future<wire.PrivateState> getPrivateState() async {
    privateReads++;
    return privateState;
  }

  @override
  Future<wire.PrivateCommandResult> privateCommand(
    wire.PrivateCommand command,
  ) async {
    commands.add(command.deepCopy());
    if (command.action == wire.PrivateAction.PRIVATE_ACTION_REMOVE_TARGET) {
      lifecycle.add('remove');
      privateState.targets.removeWhere(
        (target) => target.peerId == command.peerId,
      );
    }
    if (command.action == wire.PrivateAction.PRIVATE_ACTION_CONNECT &&
        command.hosting) {
      await hostingGate?.future;
    }
    if (enrollmentFlow) {
      if (command.action ==
          wire.PrivateAction.PRIVATE_ACTION_IMPORT_INVITATION) {
        privateState = wire.PrivateState(
          enrollments: [
            wire.PrivateEnrollment(hostPeerId: 'friend', state: 'imported'),
          ],
        );
      } else if (command.action ==
          wire.PrivateAction.PRIVATE_ACTION_RESUME_ENROLLMENT) {
        privateState = approved
            ? wire.PrivateState(
                targets: [wire.PrivateTarget(peerId: 'friend', name: 'Friend')],
              )
            : wire.PrivateState(
                enrollments: [
                  wire.PrivateEnrollment(
                    hostPeerId: 'friend',
                    state: 'pending',
                  ),
                ],
              );
      }
    }
    if (command.action == wire.PrivateAction.PRIVATE_ACTION_CONNECT &&
        command.hosting) {
      privateState = wire.PrivateState(hostingWanted: true);
    }
    return wire.PrivateCommandResult(
      targetPeerId:
          command.action == wire.PrivateAction.PRIVATE_ACTION_IMPORT_INVITATION
          ? 'friend'
          : '',
      enrollmentState: approved ? 'APPROVED' : 'PENDING',
      invitationLink:
          command.action == wire.PrivateAction.PRIVATE_ACTION_CREATE_INVITATION
          ? 'irvio-private:v1:test-secret'
          : '',
    );
  }
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));
  Future<IrvioAppController> pump(
    WidgetTester tester,
    FakeIrvioClient client,
  ) async {
    await tester.binding.setSurfaceSize(const Size(700, 1100));
    final app = IrvioAppController(() async => client);
    await app.start();
    app.selectPage(IrvioPage.privateNetwork);
    await tester.pumpWidget(IrvioApp(controller: app));
    await tester.pumpAndSettle();
    return app;
  }

  test('PeerID labels expand when shortened names collide', () {
    const a = '12D3KooWaaaa1111';
    const b = '12D3KooWbbbb1111';
    expect(privatePeerLabel(a, [a]), '12D3K…1111');
    expect(privatePeerLabel(a, [a, b]), isNot(privatePeerLabel(b, [a, b])));
  });

  testWidgets('sharing hints, aligned actions and revoke confirmation', (
    tester,
  ) async {
    final client = _PrivateClient()
      ..privateState = wire.PrivateState(
        hostingWanted: true,
        invitations: [
          wire.PrivateInvitation(
            invitationId: 'test-invitation',
            state: 'open',
            expiresAtUnix: Int64(
              DateTime.now().millisecondsSinceEpoch ~/ 1000 + 3600,
            ),
          ),
        ],
      );
    final app = await pump(tester, client);
    for (final language in IrvioLanguage.values) {
      app.selectLanguage(language);
      await tester.pumpAndSettle();
      expect(find.text(app.strings.text('privateAccessWarning')), findsNothing);
      expect(find.text(app.strings.text('privateSharingActive')), findsNothing);
      final invite = find.byKey(const Key('private-invite-more'));
      final stop = find.byKey(const Key('private-stop-sharing'));
      expect(tester.getRect(invite).top, tester.getRect(stop).top);
      expect(tester.getSize(invite), tester.getSize(stop));
      final heading = find.text(
        app.strings.text('privateChooseConnectionType'),
      );
      expect(
        tester.getCenter(heading).dx,
        closeTo(
          (tester.getRect(invite).left + tester.getRect(stop).right) / 2,
          1,
        ),
      );
      expect(tester.takeException(), isNull);
    }
    final tooltip = find.byWidgetPredicate(
      (widget) =>
          widget is Tooltip &&
          widget.message!.contains(app.strings.text('privateAccessWarning')),
    );
    expect(tooltip, findsOneWidget);
    tester.state<TooltipState>(tooltip).ensureTooltipVisible();
    await tester.pumpAndSettle();
    expect(
      find.textContaining(app.strings.text('privateSharingActive')),
      findsOneWidget,
    );
    Tooltip.dismissAllToolTips();
    await tester.pumpAndSettle();
    final revoke = find.widgetWithText(
      FilledButton,
      app.strings.text('privateRevoke'),
    );
    final stop = find.byKey(const Key('private-stop-sharing'));
    final colors = Theme.of(tester.element(stop)).colorScheme;
    for (final button in [revoke, stop]) {
      expect(
        tester.widget<FilledButton>(button).style!.backgroundColor!.resolve({}),
        colors.errorContainer,
      );
    }
    await tester.tap(revoke);
    await tester.pumpAndSettle();
    expect(client.commands, isEmpty);
    await tester.tap(find.byKey(const Key('private-confirm')));
    await tester.pumpAndSettle();
    expect(
      client.commands.single.action,
      wire.PrivateAction.PRIVATE_ACTION_CANCEL_INVITATION,
    );
    app.dispose();
  });

  testWidgets(
    'Disconnect keeps private controls visible and refreshes immediately',
    (tester) async {
      final client = _PrivateClient()
        ..privateDisconnectGate = Completer<void>()
        ..currentStatus = const IrvioStatus(
          state: IrvioConnectionState.connected,
          privateTargetPeerId: 'friend',
        )
        ..privateState = wire.PrivateState(
          targetPeerId: 'friend',
          targets: [wire.PrivateTarget(peerId: 'friend')],
        );
      final app = await pump(tester, client);
      final reads = client.privateReads;
      final operation = app.disconnect();
      await tester.pump();
      expect(find.text(app.strings.text('privateUnsupported')), findsNothing);
      expect(find.byType(ConnectionControl), findsOneWidget);
      expect(find.byType(PrivatePeerSelector), findsOneWidget);
      expect(find.text(app.strings.text('privateShareChoice')), findsOneWidget);
      expect(app.privateNetwork.supported, isTrue);
      client.privateDisconnectGate!.complete();
      await operation;
      await tester.pumpAndSettle();
      expect(client.privateReads, greaterThan(reads));
      expect(find.text(app.strings.text('privateUnsupported')), findsNothing);
      expect(find.byType(ConnectionControl), findsOneWidget);
      expect(find.byType(PrivatePeerSelector), findsOneWidget);
      expect(app.status.state, IrvioConnectionState.disconnected);
      app.dispose();
    },
  );

  testWidgets(
    'sharing is disabled throughout a friend session and restored after disconnect',
    (tester) async {
      final client = _PrivateClient()
        ..privateState = wire.PrivateState(
          targets: [wire.PrivateTarget(peerId: 'friend')],
        );
      final app = await pump(tester, client);
      final share = find.ancestor(
        of: find.text(app.strings.text('privateShareChoice')),
        matching: find.byType(FilledButton),
      );
      expect(tester.widget<FilledButton>(share).onPressed, isNotNull);
      var sequence = 1;
      for (final state in [
        IrvioConnectionState.connecting,
        IrvioConnectionState.connected,
        IrvioConnectionState.recovering,
        IrvioConnectionState.noExit,
        IrvioConnectionState.disconnecting,
      ]) {
        client.emit(
          IrvioStatus(state: state, privateTargetPeerId: 'friend'),
          sequence: sequence++,
        );
        await tester.pump();
        await tester.pump(const Duration(milliseconds: 50));
        expect(
          tester.widget<FilledButton>(share).onPressed,
          isNull,
          reason: state.name,
        );
      }
      client.emit(const IrvioStatus.disconnected(), sequence: sequence++);
      await tester.pumpAndSettle();
      expect(tester.widget<FilledButton>(share).onPressed, isNotNull);
      expect(client.commands, isEmpty);
      app.dispose();
    },
  );

  testWidgets(
    'saved friend uses selector and shared ring without technical cards',
    (tester) async {
      const peer = '12D3KooW012345678901234567890123456789012345678901W2SD';
      final client = _PrivateClient()
        ..privateState = wire.PrivateState(
          targets: [wire.PrivateTarget(peerId: peer, name: 'Friend')],
        );
      final app = await pump(tester, client);
      expect(find.byType(ConnectionControl), findsOneWidget);
      expect(find.byType(PrivatePeerSelector), findsOneWidget);
      expect(find.text('12D3K…W2SD'), findsOneWidget);
      expect(find.text(peer), findsNothing);
      expect(find.text(app.strings.text('privateRemove')), findsNothing);
      expect(client.commands, isEmpty);
      app.dispose();
    },
  );

  testWidgets('Settings deletes a saved friend only after confirmation', (
    tester,
  ) async {
    final client = _PrivateClient()
      ..privateState = wire.PrivateState(
        targets: [wire.PrivateTarget(peerId: 'friend', name: 'Friend')],
      );
    final app = await pump(tester, client);
    app.selectPage(IrvioPage.settings);
    await tester.pumpAndSettle();
    final button = find.byKey(const Key('private-delete-connection'));
    await tester.ensureVisible(button);
    await tester.tap(button);
    await tester.pumpAndSettle();
    expect(client.commands, isEmpty);
    await tester.tap(find.text(app.strings.text('cancel')).last);
    await tester.pumpAndSettle();
    expect(client.commands, isEmpty);
    await tester.tap(button);
    await tester.pumpAndSettle();
    await tester.tap(find.byType(CheckboxListTile));
    await tester.pump();
    await tester.tap(find.byKey(const Key('private-delete-confirm')));
    await tester.pumpAndSettle();
    expect(
      client.commands.single.action,
      wire.PrivateAction.PRIVATE_ACTION_REMOVE_TARGET,
    );
    expect(client.commands.single.peerId, 'friend');
    expect(app.skipFriendDeleteConfirmation, isTrue);
    expect(app.skipShutdownConfirmation, isFalse);
    app.dispose();
  });

  testWidgets(
    'active friend disconnects before its saved connection is removed',
    (tester) async {
      final client = _PrivateClient()
        ..currentStatus = const IrvioStatus(
          state: IrvioConnectionState.connected,
          privateTargetPeerId: 'friend',
        )
        ..privateState = wire.PrivateState(
          localPeerId: 'self',
          targetPeerId: 'friend',
          targets: [wire.PrivateTarget(peerId: 'friend')],
        );
      final app = await pump(tester, client);
      app.selectPage(IrvioPage.settings);
      await tester.pumpAndSettle();
      final button = find.byKey(const Key('private-delete-connection'));
      await tester.ensureVisible(button);
      await tester.tap(button);
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('private-delete-confirm')));
      await tester.pumpAndSettle();
      expect(client.lifecycle, ['disconnect', 'remove']);
      expect(app.status.state, IrvioConnectionState.disconnected);
      app.dispose();
    },
  );

  for (final language in IrvioLanguage.values) {
    testWidgets('private screen fits default window ${language.code}', (
      tester,
    ) async {
      await tester.binding.setSurfaceSize(irvioInitialWindowSize);
      final client = _PrivateClient()
        ..privateState = wire.PrivateState(
          localPeerId: '12D3KooW0123456789012345678901234567890123456789012345',
          targets: [wire.PrivateTarget(peerId: 'peer', name: 'Friend')],
        );
      final app = IrvioAppController(() async => client);
      await app.start();
      app.selectLanguage(language);
      await tester.pumpWidget(IrvioApp(controller: app));
      await tester.pumpAndSettle();
      expect(app.strings.text('home'), 'Global VPN');
      expect(app.strings.text('privateNetwork'), 'Private VPN');
      final global = find.text('Global VPN').first;
      final private = find.text('Private VPN').first;
      final settings = find.text(app.strings.text('settings')).first;
      expect(
        tester.getTopLeft(global).dy,
        lessThan(tester.getTopLeft(private).dy),
      );
      expect(
        tester.getTopLeft(private).dy,
        lessThan(tester.getTopLeft(settings).dy),
      );
      app.selectPage(IrvioPage.privateNetwork);
      await tester.pumpWidget(IrvioApp(controller: app));
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      expect(find.text('Private VPN'), findsNWidgets(2));
      final title = find.byKey(const Key('private-vpn-title'));
      expect(
        tester.widget<Text>(title).style,
        Theme.of(tester.element(title)).textTheme.headlineSmall,
      );
      final heading = find.text(
        app.strings.text('privateChooseConnectionType'),
      );
      expect(heading, findsOneWidget);
      expect(
        tester.getRect(heading).top - tester.getRect(title).bottom,
        closeTo(16, 0.1),
      );
      expect(tester.widget<Text>(heading).style, isNull);
      expect(find.text(app.strings.text('privateStartHosting')), findsNothing);
      final shareButton = find.ancestor(
        of: find.text(app.strings.text('privateShareChoice')),
        matching: find.byType(FilledButton),
      );
      final joinButton = find.ancestor(
        of: find.text(app.strings.text('privateJoinChoice')),
        matching: find.byType(FilledButton),
      );
      expect(tester.getSize(shareButton), tester.getSize(joinButton));
      await tester.tap(find.text(app.strings.text('privateShareChoice')));
      await tester.pumpAndSettle();
      expect(find.text(app.strings.text('privateStartHosting')), findsNothing);
      expect(find.byType(QrImageView), findsOneWidget);
      await tester.tap(find.text(app.strings.text('cancel')).last);
      await tester.pumpAndSettle();
      expect(tester.takeException(), isNull);
      app.dispose();
    });
  }

  testWidgets('unsupported daemon shows clear message without commands', (
    tester,
  ) async {
    final client = FakeIrvioClient();
    final app = await pump(tester, client);
    expect(
      find.text('Private IRVIO requires a compatible running daemon.'),
      findsOneWidget,
    );
    expect(find.text('Import invitation'), findsNothing);
    expect(client.connectCalls, 0);
    app.dispose();
  });

  for (final outcome in [
    'cancel',
    'approve',
    'context-change',
    'revision-change',
  ]) {
    testWidgets('owner approval $outcome is explicit and context-fenced', (
      tester,
    ) async {
      final client = _PrivateClient()
        ..privateState = wire.PrivateState(
          invitations: [
            wire.PrivateInvitation(
              invitationId: 'invite-1',
              clientPeerId: 'verified-friend-peer',
              state: 'pending',
            ),
          ],
        );
      final app = await pump(tester, client);
      final approve = find.text('Approve this PeerID');
      await tester.scrollUntilVisible(
        approve,
        200,
        scrollable: find
            .descendant(
              of: find.byType(ListView),
              matching: find.byType(Scrollable),
            )
            .first,
      );
      await tester.tap(approve);
      await tester.pumpAndSettle();
      expect(client.commands, isEmpty);
      expect(
        find.descendant(
          of: find.byType(AlertDialog),
          matching: find.text('verified-friend-peer'),
        ),
        findsOneWidget,
      );
      if (outcome == 'context-change') app.privateNetwork.attach(null);
      if (outcome == 'revision-change') {
        client.privateState.revision = Int64(2);
        await app.privateNetwork.refresh();
      }
      await tester.tap(
        outcome == 'cancel'
            ? find.descendant(
                of: find.byType(AlertDialog),
                matching: find.text('Cancel'),
              )
            : find.byKey(const Key('private-confirm')),
      );
      await tester.pumpAndSettle();
      if (outcome == 'approve') {
        expect(
          client.commands.single.action,
          wire.PrivateAction.PRIVATE_ACTION_APPROVE_INVITATION,
        );
        expect(client.commands.single.invitationId, 'invite-1');
      } else {
        expect(client.commands, isEmpty);
      }
      expect(client.connectCalls, 0);
      app.dispose();
    });
  }

  testWidgets('revocation requires explicit confirmation of exact member', (
    tester,
  ) async {
    final client = _PrivateClient()
      ..privateState = wire.PrivateState(
        members: [
          wire.PrivateMember(peerId: 'member-1', name: 'Friend', allowed: true),
        ],
      );
    final app = await pump(tester, client);
    final revoke = find.text('Revoke access');
    await tester.scrollUntilVisible(
      revoke,
      200,
      scrollable: find
          .descendant(
            of: find.byType(ListView),
            matching: find.byType(Scrollable),
          )
          .first,
    );
    await tester.tap(revoke);
    await tester.pumpAndSettle();
    expect(client.commands, isEmpty);
    await tester.tap(find.byKey(const Key('private-confirm')));
    await tester.pumpAndSettle();
    expect(
      client.commands.single.action,
      wire.PrivateAction.PRIVATE_ACTION_REVOKE_MEMBER,
    );
    expect(client.commands.single.peerId, 'member-1');
    app.dispose();
  });

  testWidgets(
    'visible screen reloads after context replacement without enrollment',
    (tester) async {
      final client = _PrivateClient();
      final app = await pump(tester, client);
      app.privateNetwork.attach(null);
      await tester.pump();
      final next = _PrivateClient()
        ..privateState = wire.PrivateState(localPeerId: 'new-identity');
      app.privateNetwork.attach(next);
      await tester.pump(const Duration(seconds: 5));
      await tester.pumpAndSettle();
      expect(app.privateNetwork.state!.localPeerId, 'new-identity');
      expect(next.commands, isEmpty);
      expect(next.connectCalls, 0);
      app.dispose();
    },
  );

  testWidgets(
    'legacy pending invitation does not connect after later approval',
    (tester) async {
      final client = _PrivateClient()..enrollmentFlow = true;
      final app = await pump(tester, client);
      await tester.tap(find.text(app.strings.text('privateJoinChoice')));
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextField), 'irvio-private:v1:test');
      await tester.pump();
      await tester.tap(find.text('CONNECT'));
      await tester.pumpAndSettle();
      expect(client.commands.map((command) => command.action), [
        wire.PrivateAction.PRIVATE_ACTION_IMPORT_INVITATION,
        wire.PrivateAction.PRIVATE_ACTION_RESUME_ENROLLMENT,
      ]);
      expect(client.commands.first.name, 'Friend');
      expect(client.commands.first.hosting, isFalse);
      expect(
        find.textContaining(app.strings.text('privatePending')),
        findsOneWidget,
      );
      expect(find.text(app.strings.text('privateCheck')), findsNothing);
      client.approved = true;
      await tester.pump(const Duration(seconds: 5));
      await tester.pumpAndSettle();
      expect(app.privateNetwork.state!.targets.single.peerId, 'friend');
      expect(
        client.commands.any(
          (command) =>
              command.action == wire.PrivateAction.PRIVATE_ACTION_CONNECT,
        ),
        isFalse,
      );
      expect(client.connectCalls, 0);
      expect(find.text('irvio-private:v1:test'), findsNothing);
      app.dispose();
    },
  );

  testWidgets(
    'saved target Connect pins PeerID and does not call public Connect',
    (tester) async {
      final client = _PrivateClient()
        ..privateState = wire.PrivateState(
          targets: [wire.PrivateTarget(peerId: 'exact-peer', name: 'Friend')],
        );
      final app = await pump(tester, client);
      final button = find.text('CONNECT').last;
      await tester.ensureVisible(button);
      await tester.tap(button);
      await tester.pumpAndSettle();
      expect(
        client.commands.single.action,
        wire.PrivateAction.PRIVATE_ACTION_CONNECT,
      );
      expect(client.commands.single.peerId, 'exact-peer');
      expect(client.commands.single.hosting, isFalse);
      expect(client.connectCalls, 0);
      app.dispose();
    },
  );

  testWidgets(
    'Share starts once and repeated clicks do not issue invitations',
    (tester) async {
      final client = _PrivateClient()..hostingGate = Completer<void>();
      final app = await pump(tester, client);
      final share = find.text(app.strings.text('privateShareChoice'));
      await tester.tap(share);
      await tester.tap(share);
      await tester.pump();
      expect(client.commands, hasLength(1));
      expect(client.commands.single.hosting, isTrue);
      client.hostingGate!.complete();
      await tester.pumpAndSettle();
      expect(find.byType(QrImageView), findsOneWidget);
      expect(client.commands, hasLength(2));
      await tester.tap(find.text(app.strings.text('cancel')).last);
      await tester.pumpAndSettle();
      await tester.tap(find.text(app.strings.text('privateJoinChoice')));
      await tester.pumpAndSettle();
      await tester.tap(find.text(app.strings.text('cancel')).last);
      await tester.pumpAndSettle();
      await tester.tap(share);
      await tester.pumpAndSettle();
      expect(client.commands, hasLength(2));
      expect(find.text(app.strings.text('privateSharingActive')), findsNothing);
      expect(
        find.byWidgetPredicate(
          (widget) =>
              widget is Tooltip &&
              widget.message!.contains(
                app.strings.text('privateSharingActive'),
              ),
        ),
        findsOneWidget,
      );
      expect(find.text(app.strings.text('privateStartHosting')), findsNothing);
      app.dispose();
    },
  );

  testWidgets('Share without bootstrap explains setup without connecting', (
    tester,
  ) async {
    final client = _PrivateClient()
      ..bootstrapState = const IrvioBootstrapState(
        phase: IrvioBootstrapPhase.empty,
        configuredCount: 0,
        currentlyReachableCount: 0,
      );
    final app = await pump(tester, client);
    await tester.tap(find.text(app.strings.text('privateShareChoice')));
    await tester.pumpAndSettle();
    expect(client.commands, isEmpty);
    expect(
      find.text(app.strings.text('privateBootstrapRequired')),
      findsOneWidget,
    );
    app.dispose();
  });

  testWidgets('invitation QR is hidden when profile context changes', (
    tester,
  ) async {
    final client = _PrivateClient();
    final app = await pump(tester, client);
    await tester.tap(find.text(app.strings.text('privateShareChoice')));
    await tester.pumpAndSettle();
    expect(client.commands.map((value) => value.action), [
      wire.PrivateAction.PRIVATE_ACTION_CONNECT,
      wire.PrivateAction.PRIVATE_ACTION_CREATE_INVITATION,
    ]);
    expect(client.commands.last.autoApprove, isTrue);
    expect(client.commands.last.lifetimeSeconds, 3600);
    expect(find.byType(QrImageView), findsOneWidget);
    expect(find.text('Save invitation QR'), findsOneWidget);
    expect(find.text('irvio-private:v1:test-secret'), findsOneWidget);
    app.privateNetwork.attach(null);
    await tester.pumpAndSettle();
    expect(find.byType(QrImageView), findsNothing);
    expect(find.text('Save invitation QR'), findsNothing);
    expect(find.text('irvio-private:v1:test-secret'), findsNothing);
    app.dispose();
  });

  testWidgets(
    'first screen has two choices and CONNECT explicitly redeems and connects',
    (tester) async {
      final client = _PrivateClient()
        ..enrollmentFlow = true
        ..approved = true;
      final app = await pump(tester, client);
      expect(find.byType(TextField), findsNothing);
      expect(find.text(app.strings.text('privateStartHosting')), findsNothing);
      expect(client.commands, isEmpty);
      await tester.tap(find.text(app.strings.text('privateJoinChoice')));
      await tester.pumpAndSettle();
      expect(find.text(app.strings.text('privateStartHosting')), findsNothing);
      await tester.enterText(find.byType(TextField), 'irvio-private:v1:test');
      await tester.pump();
      expect(client.commands, isEmpty);
      await tester.tap(find.text('CONNECT'));
      await tester.pumpAndSettle();
      expect(client.commands.map((value) => value.action), [
        wire.PrivateAction.PRIVATE_ACTION_IMPORT_INVITATION,
        wire.PrivateAction.PRIVATE_ACTION_RESUME_ENROLLMENT,
        wire.PrivateAction.PRIVATE_ACTION_CONNECT,
      ]);
      expect(client.commands.last.peerId, 'friend');
      expect(client.connectCalls, 0);
      app.dispose();
    },
  );

  testWidgets('friend card reflects active lease and returns to disconnected', (
    tester,
  ) async {
    final client = _PrivateClient()
      ..privateState = wire.PrivateState(
        hostingWanted: true,
        members: [
          wire.PrivateMember(peerId: 'friend', allowed: true, connected: true),
        ],
      );
    final app = await pump(tester, client);
    expect(find.text(app.strings.text('connected')), findsWidgets);
    client.privateState.members.single.connected = false;
    await app.privateNetwork.refresh();
    await tester.pumpAndSettle();
    expect(find.text(app.strings.text('connected')), findsNothing);
    expect(find.text(app.strings.text('disconnected')), findsWidgets);
    app.dispose();
  });
}
