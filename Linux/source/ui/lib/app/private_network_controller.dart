// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';

import 'package:flutter/foundation.dart';

import '../irvio/generated/irvio/local/v1/control.pb.dart' as wire;
import '../irvio/private_irvio_client.dart';
import '../models/irvio_models.dart';

class PrivateNetworkController extends ChangeNotifier {
  PrivateNetworkController({
    this.enrollmentInterval = const Duration(seconds: 5),
  });

  final Duration enrollmentInterval;
  bool Function()? canPoll;
  Timer? _enrollmentTimer;
  bool _automaticEnrollment = true;
  String? _lastEnrollment;
  final Map<String, IrvioErrorCode> _enrollmentErrors = {};
  final Map<String, int> _enrollmentFailures = {};
  PrivateIrvioClient? _client;
  int _epoch = 0;
  int _read = 0;
  bool _closed = false;
  bool busy = false;
  bool loading = false;
  bool connectingInvitation = false;
  wire.PrivateState? state;
  IrvioErrorCode? error;

  bool get supported => _client != null;
  int get contextRevision => _epoch;

  bool enrollmentNeedsRetry(String peer) =>
      !_automaticEnrollment || _enrollmentErrors.containsKey(peer);

  // Called only by an explicit CONNECT click, never by background approval polling.
  Future<void> connectInvitation(String link, String name) async {
    if (connectingInvitation || busy || !(canPoll?.call() ?? true)) return;
    final epoch = _epoch;
    connectingInvitation = true;
    notifyListeners();
    try {
      final imported = await command(
        wire.PrivateCommand(
          action: wire.PrivateAction.PRIVATE_ACTION_IMPORT_INVITATION,
          invitationLink: link,
          name: name,
        ),
      );
      if (!_current(epoch) ||
          imported == null ||
          imported.targetPeerId.isEmpty) {
        return;
      }
      wire.PrivateCommandResult? result;
      for (var attempt = 0; attempt < 3; attempt++) {
        if (!_current(epoch) || !(canPoll?.call() ?? true)) return;
        result = await command(
          wire.PrivateCommand(
            action: wire.PrivateAction.PRIVATE_ACTION_RESUME_ENROLLMENT,
            peerId: imported.targetPeerId,
          ),
          automatic: true,
        );
        if (!_current(epoch) || result != null || error != null) break;
        if (attempt < 2) await Future<void>.delayed(enrollmentInterval);
      }
      if (!_current(epoch) ||
          result?.enrollmentState != 'APPROVED' ||
          !(canPoll?.call() ?? true)) {
        return;
      }
      await command(
        wire.PrivateCommand(
          action: wire.PrivateAction.PRIVATE_ACTION_CONNECT,
          peerId: imported.targetPeerId,
        ),
      );
    } finally {
      if (_current(epoch)) {
        connectingInvitation = false;
        notifyListeners();
        _scheduleEnrollment();
      }
    }
  }

  void attach(PrivateIrvioClient? client, {bool? automaticEnrollment}) {
    if (_closed) return;
    _enrollmentTimer?.cancel();
    _enrollmentTimer = null;
    if (automaticEnrollment != null) _automaticEnrollment = automaticEnrollment;
    _lastEnrollment = null;
    _enrollmentErrors.clear();
    _enrollmentFailures.clear();
    _epoch++;
    _read++;
    _client = client;
    busy = false;
    connectingInvitation = false;
    loading = false;
    state = null;
    error = null;
    notifyListeners();
  }

  Future<void> refresh() async {
    final client = _client;
    if (_closed || client == null || loading || busy) return;
    final epoch = _epoch;
    final read = ++_read;
    loading = true;
    error = null;
    notifyListeners();
    try {
      final value = await client.getPrivateState();
      if (_current(epoch) && read == _read) {
        state = value.deepCopy()..freeze();
        final peers = value.enrollments
            .map((entry) => entry.hostPeerId)
            .toSet();
        _enrollmentErrors.removeWhere((peer, _) => !peers.contains(peer));
        _enrollmentFailures.removeWhere((peer, _) => !peers.contains(peer));
      }
    } on Object catch (failure) {
      if (_current(epoch) && read == _read) error = _code(failure);
    } finally {
      if (_current(epoch) && read == _read) {
        loading = false;
        notifyListeners();
        if (error == null) _scheduleEnrollment();
      }
    }
  }

  int beginDisconnect() {
    _enrollmentTimer?.cancel();
    _enrollmentTimer = null;
    _automaticEnrollment = false;
    _epoch++;
    _read++;
    busy = true;
    connectingInvitation = false;
    loading = false;
    error = null;
    notifyListeners();
    return _epoch;
  }

  Future<void> finishDisconnect(int epoch) async {
    if (!_current(epoch)) return;
    busy = false;
    await refresh();
  }

  Future<wire.PrivateCommandResult?> command(
    wire.PrivateCommand command, {
    bool automatic = false,
  }) async {
    final client = _client;
    if (_closed || client == null || busy) return null;
    _enrollmentTimer?.cancel();
    _enrollmentTimer = null;
    final resume =
        command.action == wire.PrivateAction.PRIVATE_ACTION_RESUME_ENROLLMENT;
    final importing =
        command.action == wire.PrivateAction.PRIVATE_ACTION_IMPORT_INVITATION;
    if (resume || importing) _automaticEnrollment = true;
    if (resume) {
      _enrollmentErrors.remove(command.peerId);
      if (!automatic) _enrollmentFailures.remove(command.peerId);
    }
    final epoch = _epoch;
    _read++;
    loading = false;
    busy = true;
    error = null;
    notifyListeners();
    wire.PrivateCommandResult? result;
    try {
      result = await client.privateCommand(command);
      if (!_current(epoch)) return null;
      if (resume) _enrollmentFailures.remove(command.peerId);
    } on Object catch (failure) {
      if (_current(epoch)) {
        error = _code(failure);
        if (resume) {
          final failures = (_enrollmentFailures[command.peerId] ?? 0) + 1;
          _enrollmentFailures[command.peerId] = failures;
          if ((error == IrvioErrorCode.notReady ||
                  error == IrvioErrorCode.unavailable) &&
              failures < 3) {
            error = null;
          } else {
            _enrollmentErrors[command.peerId] = error!;
          }
        }
      }
    } finally {
      if (_current(epoch)) {
        busy = false;
        notifyListeners();
      }
    }
    if (!_current(epoch)) return null;
    if (result != null) await refresh();
    if (_current(epoch)) {
      if (importing && result != null) {
        _enrollmentTimer?.cancel();
        _enrollmentTimer = null;
        _scheduleEnrollment(immediate: true);
      } else {
        _scheduleEnrollment();
      }
    }
    return _current(epoch) ? result : null;
  }

  List<wire.PrivateEnrollment> get _pendingEnrollments =>
      state?.enrollments
          .where(
            (entry) =>
                (entry.state == 'imported' || entry.state == 'pending') &&
                !_enrollmentErrors.containsKey(entry.hostPeerId),
          )
          .toList() ??
      [];

  void _scheduleEnrollment({bool immediate = false}) {
    if (_closed ||
        connectingInvitation ||
        !_automaticEnrollment ||
        _client == null ||
        _enrollmentTimer != null ||
        _pendingEnrollments.isEmpty) {
      return;
    }
    final epoch = _epoch;
    _enrollmentTimer = Timer(
      immediate ? Duration.zero : enrollmentInterval,
      () async {
        _enrollmentTimer = null;
        if (!_current(epoch)) return;
        if (busy || loading || !(canPoll?.call() ?? true)) {
          _scheduleEnrollment();
          return;
        }
        final pending = _pendingEnrollments;
        if (pending.isEmpty) return;
        final previous = pending.indexWhere(
          (entry) => entry.hostPeerId == _lastEnrollment,
        );
        final next = pending[(previous + 1) % pending.length];
        _lastEnrollment = next.hostPeerId;
        await command(
          wire.PrivateCommand(
            action: wire.PrivateAction.PRIVATE_ACTION_RESUME_ENROLLMENT,
            peerId: next.hostPeerId,
          ),
          automatic: true,
        );
      },
    );
  }

  bool _current(int epoch) => !_closed && epoch == _epoch;

  IrvioErrorCode _code(Object failure) =>
      failure is IrvioApiException ? failure.code : IrvioErrorCode.internal;

  @override
  void dispose() {
    _enrollmentTimer?.cancel();
    _closed = true;
    _epoch++;
    _client = null;
    state = null;
    super.dispose();
  }
}
