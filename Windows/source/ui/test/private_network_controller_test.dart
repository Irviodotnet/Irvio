// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/app/irvio_app_controller.dart';
import 'package:irvio/app/private_network_controller.dart';
import 'package:irvio/irvio/generated/irvio/local/v1/control.pb.dart' as wire;
import 'package:irvio/irvio/private_irvio_client.dart';
import 'package:irvio/models/irvio_models.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'fake_irvio_client.dart';

class _AppClient extends FakeIrvioClient implements PrivateIrvioClient {
  _AppClient() : super(apiMinor: 6);
  final gate = Completer<wire.PrivateCommandResult>();

  @override
  IrvioHello get hello => const IrvioHello(
    serverMajor: 1,
    serverMinor: 6,
    negotiatedMinor: 6,
    productVersion: 'fixture',
    coreVersion: 'fixture',
    privateNetworkSupported: true,
  );

  @override
  Future<wire.PrivateState> getPrivateState() async => wire.PrivateState();

  @override
  Future<wire.PrivateCommandResult> privateCommand(
    wire.PrivateCommand command,
  ) => gate.future;
}

class _Client implements PrivateIrvioClient {
  int reads = 0;
  int commands = 0;
  Completer<wire.PrivateState>? readGate;
  Completer<wire.PrivateCommandResult>? commandGate;
  Object? failure;

  @override
  Future<wire.PrivateState> getPrivateState() async {
    reads++;
    return readGate == null
        ? wire.PrivateState(localPeerId: 'new')
        : readGate!.future;
  }

  @override
  Future<wire.PrivateCommandResult> privateCommand(
    wire.PrivateCommand command,
  ) async {
    commands++;
    if (failure != null) throw failure!;
    return commandGate == null
        ? wire.PrivateCommandResult()
        : commandGate!.future;
  }
}

class _EnrollmentClient extends _Client {
  wire.PrivateState value = wire.PrivateState();
  final actions = <wire.PrivateAction>[];
  bool approved = false;
  bool denied = false;

  @override
  Future<wire.PrivateState> getPrivateState() async => value.deepCopy();

  @override
  Future<wire.PrivateCommandResult> privateCommand(
    wire.PrivateCommand command,
  ) async {
    actions.add(command.action);
    if (failure != null) throw failure!;
    switch (command.action) {
      case wire.PrivateAction.PRIVATE_ACTION_IMPORT_INVITATION:
        value = wire.PrivateState(
          enrollments: [
            wire.PrivateEnrollment(hostPeerId: 'friend', state: 'imported'),
          ],
        );
      case wire.PrivateAction.PRIVATE_ACTION_RESUME_ENROLLMENT:
        if (commandGate != null) await commandGate!.future;
        value = approved
            ? wire.PrivateState(targets: [wire.PrivateTarget(peerId: 'friend')])
            : wire.PrivateState(
                enrollments: [
                  wire.PrivateEnrollment(
                    hostPeerId: 'friend',
                    state: denied ? 'denied' : 'pending',
                  ),
                ],
              );
      case wire.PrivateAction.PRIVATE_ACTION_CANCEL_ENROLLMENT:
        value = wire.PrivateState();
      case wire.PrivateAction.PRIVATE_ACTION_CONNECT:
        if (!approved) throw StateError('unapproved CONNECT');
      default:
        throw StateError('unexpected command: ${command.action}');
    }
    return wire.PrivateCommandResult(
      targetPeerId:
          command.action == wire.PrivateAction.PRIVATE_ACTION_IMPORT_INVITATION
          ? 'friend'
          : '',
      enrollmentState: approved
          ? 'APPROVED'
          : denied
          ? 'DENIED'
          : 'PENDING',
    );
  }
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets(
    'explicit invitation CONNECT is fenced by Disconnect during redemption',
    (tester) async {
      final controller = PrivateNetworkController();
      addTearDown(controller.dispose);
      final client = _EnrollmentClient()
        ..approved = true
        ..commandGate = Completer<wire.PrivateCommandResult>();
      controller.attach(client);
      final pending = controller.connectInvitation('link', 'friend');
      await tester.pump(const Duration(milliseconds: 1));
      expect(controller.connectingInvitation, isTrue);
      controller.attach(null);
      client.commandGate!.complete(wire.PrivateCommandResult());
      await pending;
      expect(
        client.actions,
        isNot(contains(wire.PrivateAction.PRIVATE_ACTION_CONNECT)),
      );
      expect(controller.state, isNull);
      expect(controller.connectingInvitation, isFalse);
    },
  );

  testWidgets('import requests approval automatically but never connects', (
    tester,
  ) async {
    final controller = PrivateNetworkController();
    addTearDown(controller.dispose);
    final client = _EnrollmentClient();
    controller.attach(client);
    await controller.command(
      wire.PrivateCommand(
        action: wire.PrivateAction.PRIVATE_ACTION_IMPORT_INVITATION,
      ),
    );
    await tester.pump(const Duration(milliseconds: 1));
    expect(client.actions, [
      wire.PrivateAction.PRIVATE_ACTION_IMPORT_INVITATION,
      wire.PrivateAction.PRIVATE_ACTION_RESUME_ENROLLMENT,
    ]);
    expect(controller.state!.enrollments.single.state, 'pending');
    expect(controller.enrollmentNeedsRetry('friend'), isFalse);
    client.approved = true;
    await tester.pump(const Duration(seconds: 5));
    expect(controller.state!.targets.single.peerId, 'friend');
    expect(controller.state!.enrollments, isEmpty);
    final calls = client.actions.length;
    await tester.pump(const Duration(minutes: 1));
    expect(client.actions.length, calls);
    expect(
      client.actions,
      isNot(contains(wire.PrivateAction.PRIVATE_ACTION_CONNECT)),
    );
  });

  testWidgets(
    'failure stops polling until explicit retry, even after refresh',
    (tester) async {
      final controller = PrivateNetworkController();
      addTearDown(controller.dispose);
      final client = _EnrollmentClient();
      controller.attach(client);
      await controller.command(
        wire.PrivateCommand(
          action: wire.PrivateAction.PRIVATE_ACTION_IMPORT_INVITATION,
        ),
      );
      client.failure = const IrvioApiException(
        IrvioErrorCode.unauthorized,
        'private secret',
      );
      await tester.pump(const Duration(milliseconds: 1));
      expect(controller.enrollmentNeedsRetry('friend'), isTrue);
      final calls = client.actions.length;
      await controller.refresh();
      await tester.pump(const Duration(minutes: 1));
      expect(client.actions.length, calls);
      client.failure = null;
      client.approved = true;
      await controller.command(
        wire.PrivateCommand(
          action: wire.PrivateAction.PRIVATE_ACTION_RESUME_ENROLLMENT,
          peerId: 'friend',
        ),
      );
      expect(controller.state!.targets.single.peerId, 'friend');
    },
  );

  testWidgets(
    'pending state resumes on reopen but explicit disconnect stays paused',
    (tester) async {
      final controller = PrivateNetworkController();
      addTearDown(controller.dispose);
      final client = _EnrollmentClient()
        ..value = wire.PrivateState(
          enrollments: [
            wire.PrivateEnrollment(hostPeerId: 'friend', state: 'pending'),
          ],
        );
      controller.attach(client);
      await controller.refresh();
      await tester.pump(const Duration(seconds: 5));
      expect(client.actions.length, 1);
      controller.attach(client, automaticEnrollment: false);
      await controller.refresh();
      await tester.pump(const Duration(minutes: 1));
      expect(client.actions.length, 1);
      expect(controller.enrollmentNeedsRetry('friend'), isTrue);
    },
  );

  testWidgets('slow request cannot overlap and detach discards its result', (
    tester,
  ) async {
    final controller = PrivateNetworkController();
    addTearDown(controller.dispose);
    final client = _EnrollmentClient()
      ..commandGate = Completer<wire.PrivateCommandResult>();
    controller.attach(client);
    await controller.command(
      wire.PrivateCommand(
        action: wire.PrivateAction.PRIVATE_ACTION_IMPORT_INVITATION,
      ),
    );
    await tester.pump(const Duration(milliseconds: 1));
    expect(controller.busy, isTrue);
    await tester.pump(const Duration(seconds: 20));
    expect(client.actions.length, 2);
    controller.attach(null);
    client.commandGate!.complete(wire.PrivateCommandResult());
    await tester.pump();
    expect(controller.state, isNull);
    await tester.pump(const Duration(minutes: 1));
    expect(client.actions.length, 2);
  });

  testWidgets(
    'transient discovery errors retry boundedly without overlapping user work',
    (tester) async {
      final controller = PrivateNetworkController();
      addTearDown(controller.dispose);
      final client = _EnrollmentClient();
      var allowed = false;
      controller.canPoll = () => allowed;
      controller.attach(client);
      await controller.command(
        wire.PrivateCommand(
          action: wire.PrivateAction.PRIVATE_ACTION_IMPORT_INVITATION,
        ),
      );
      client.failure = const IrvioApiException(
        IrvioErrorCode.notReady,
        'not ready',
      );
      await tester.pump(const Duration(milliseconds: 1));
      expect(client.actions.length, 1);
      allowed = true;
      for (var i = 0; i < 3; i++) {
        await tester.pump(const Duration(seconds: 5));
      }
      expect(client.actions.length, 4);
      expect(controller.enrollmentNeedsRetry('friend'), isTrue);
      await tester.pump(const Duration(minutes: 1));
      expect(client.actions.length, 4);
    },
  );

  for (final action in ['cancel', 'denied', 'dispose']) {
    testWidgets('$action stops automatic enrollment', (tester) async {
      final controller = PrivateNetworkController();
      addTearDown(() {
        if (action != 'dispose') controller.dispose();
      });
      final client = _EnrollmentClient()..denied = action == 'denied';
      controller.attach(client);
      await controller.command(
        wire.PrivateCommand(
          action: wire.PrivateAction.PRIVATE_ACTION_IMPORT_INVITATION,
        ),
      );
      await tester.pump(const Duration(milliseconds: 1));
      if (action == 'cancel') {
        await controller.command(
          wire.PrivateCommand(
            action: wire.PrivateAction.PRIVATE_ACTION_CANCEL_ENROLLMENT,
            peerId: 'friend',
          ),
        );
      } else if (action == 'dispose') {
        controller.dispose();
      }
      final calls = client.actions.length;
      await tester.pump(const Duration(minutes: 1));
      expect(client.actions.length, calls);
    });
  }

  test(
    'app Disconnect is available during private work and fences late result',
    () async {
      final client = _AppClient();
      final app = IrvioAppController(() async => client);
      addTearDown(app.dispose);
      await app.start();
      expect(app.privateNetwork.supported, isTrue);
      final pending = app.privateNetwork.command(wire.PrivateCommand());
      expect(app.privateNetwork.busy, isTrue);
      await app.connect();
      expect(client.connectCalls, 0);
      await app.disconnect();
      expect(client.disconnectCalls, 1);
      client.gate.complete(wire.PrivateCommandResult(invitationLink: 'secret'));
      expect(await pending, isNull);
      expect(app.privateNetwork.supported, isTrue);
    },
  );

  test(
    'app Shutdown suppresses late invitation and does not resume private work',
    () async {
      final client = _AppClient();
      final app = IrvioAppController(() async => client);
      addTearDown(app.dispose);
      await app.start();
      final pending = app.privateNetwork.command(wire.PrivateCommand());
      expect(await app.shutdownIrvio(), isTrue);
      client.gate.complete(wire.PrivateCommandResult(invitationLink: 'secret'));
      expect(await pending, isNull);
      expect(client.shutdownCalls, 1);
      expect(app.privateNetwork.supported, isFalse);
    },
  );

  test('attach does not join or command; reads are coalesced', () async {
    final controller = PrivateNetworkController();
    addTearDown(controller.dispose);
    final client = _Client()..readGate = Completer<wire.PrivateState>();
    controller.attach(client);
    expect(client.reads, 0);
    expect(client.commands, 0);
    final pending = controller.refresh();
    await controller.refresh();
    expect(client.reads, 1);
    client.readGate!.complete(wire.PrivateState(localPeerId: 'local'));
    await pending;
    expect(controller.state!.localPeerId, 'local');
    expect(
      () => controller.state!.localPeerId = 'changed',
      throwsUnsupportedError,
    );
  });

  test('late snapshot cannot repopulate detached profile', () async {
    final controller = PrivateNetworkController();
    addTearDown(controller.dispose);
    final old = _Client()..readGate = Completer<wire.PrivateState>();
    controller.attach(old);
    final pending = controller.refresh();
    controller.attach(_Client());
    await controller.refresh();
    old.readGate!.complete(wire.PrivateState(localPeerId: 'old'));
    await pending;
    expect(controller.state!.localPeerId, 'new');
  });

  test('command immediately becomes busy and is never replayed', () async {
    final controller = PrivateNetworkController();
    addTearDown(controller.dispose);
    final client = _Client()
      ..commandGate = Completer<wire.PrivateCommandResult>();
    controller.attach(client);
    final pending = controller.command(wire.PrivateCommand());
    expect(controller.busy, isTrue);
    expect(await controller.command(wire.PrivateCommand()), isNull);
    client.commandGate!.complete(
      wire.PrivateCommandResult(invitationLink: 'secret'),
    );
    final result = await pending;
    expect(result!.invitationLink, 'secret');
    expect(client.commands, 1);
    expect(client.reads, 1);
    expect(controller.busy, isFalse);
  });

  test('detach suppresses late invitation and does not refresh', () async {
    final controller = PrivateNetworkController();
    addTearDown(controller.dispose);
    final client = _Client()
      ..commandGate = Completer<wire.PrivateCommandResult>();
    controller.attach(client);
    final pending = controller.command(wire.PrivateCommand());
    controller.attach(null);
    client.commandGate!.complete(
      wire.PrivateCommandResult(invitationLink: 'secret'),
    );
    expect(await pending, isNull);
    expect(controller.state, isNull);
    expect(client.reads, 0);
  });

  test(
    'errors retain codes but not daemon text or invitation secrets',
    () async {
      final controller = PrivateNetworkController();
      addTearDown(controller.dispose);
      final client = _Client()
        ..failure = const IrvioApiException(
          IrvioErrorCode.unauthorized,
          'secret invitation content',
        );
      controller.attach(client);
      expect(await controller.command(wire.PrivateCommand()), isNull);
      expect(controller.error, IrvioErrorCode.unauthorized);
      expect(client.commands, 1);
      expect(client.reads, 0);
    },
  );
}
