// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';
import 'dart:typed_data';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:qr_flutter/qr_flutter.dart';

import '../app/irvio_app_controller.dart';
import '../irvio/bootstrap_qr.dart';
import '../irvio/private_qr.dart';
import '../irvio/generated/irvio/local/v1/control.pb.dart' as wire;
import '../l10n/irvio_localizations.dart';
import '../models/irvio_models.dart';
import '../widgets/connection_control.dart';
import '../widgets/private_peer_selector.dart';

class PrivateNetworkScreen extends StatefulWidget {
  const PrivateNetworkScreen({super.key, required this.controller});
  final IrvioAppController controller;

  @override
  State<PrivateNetworkScreen> createState() => _PrivateNetworkScreenState();
}

class _PrivateNetworkScreenState extends State<PrivateNetworkScreen> {
  final _link = TextEditingController();
  final _name = TextEditingController();
  String? _panel;
  bool _startingSharing = false;
  bool _fileBusy = false;
  String? _fileError;
  Timer? _refreshTimer;

  bool _validContext(int revision) =>
      mounted && widget.controller.privateNetwork.contextRevision == revision;

  Future<void> _importQr() async {
    if (_fileBusy) return;
    final revision = widget.controller.privateNetwork.contextRevision;
    setState(() {
      _fileBusy = true;
      _fileError = null;
    });
    try {
      final file = await openFile(
        acceptedTypeGroups: const [
          XTypeGroup(label: 'QR', extensions: ['png', 'jpg', 'jpeg']),
        ],
      );
      if (file == null || !_validContext(revision)) return;
      final bytes = BytesBuilder(copy: false);
      await for (final chunk in file.openRead()) {
        if (bytes.length + chunk.length > maxBootstrapQrImageBytes) {
          throw const FormatException('QR image too large.');
        }
        bytes.add(chunk);
      }
      final text = await compute(decodeIrvioPrivateQr, bytes.takeBytes());
      if (_validContext(revision)) setState(() => _link.text = text);
    } on Object {
      if (_validContext(revision)) {
        setState(() => _fileError = 'privateQrInvalid');
      }
    } finally {
      if (mounted) setState(() => _fileBusy = false);
    }
  }

  Future<void> _saveQr(String link, int revision) async {
    if (_fileBusy || !_validContext(revision)) return;
    setState(() {
      _fileBusy = true;
      _fileError = null;
    });
    try {
      final location = await getSaveLocation(
        suggestedName: 'irvio-private-invitation.png',
        acceptedTypeGroups: const [
          XTypeGroup(label: 'PNG', extensions: ['png']),
        ],
      );
      if (location == null || !_validContext(revision)) return;
      final bytes = await compute(encodeIrvioPrivateQr, link);
      if (!_validContext(revision)) return;
      await XFile.fromData(bytes, mimeType: 'image/png').saveTo(location.path);
      if (mounted && _validContext(revision)) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text(context.l10n.text('qrSaved'))));
      }
    } on Object {
      if (_validContext(revision)) setState(() => _fileError = 'qrSaveFailed');
    } finally {
      if (mounted) setState(() => _fileBusy = false);
    }
  }

  @override
  void initState() {
    super.initState();
    _refreshTimer = Timer.periodic(const Duration(seconds: 5), (_) {
      if (mounted && !widget.controller.commandPending) {
        widget.controller.privateNetwork.refresh();
      }
    });
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) widget.controller.privateNetwork.refresh();
    });
  }

  @override
  void dispose() {
    _refreshTimer?.cancel();
    _link.dispose();
    _name.dispose();
    super.dispose();
  }

  Future<void> _run(wire.PrivateCommand command, {String? confirmPeer}) async {
    final app = widget.controller;
    if (app.commandPending ||
        app.privateNetwork.busy ||
        app.privateNetwork.connectingInvitation) {
      return;
    }
    if (confirmPeer != null) {
      final epoch = app.privateNetwork.contextRevision;
      final revision = app.privateNetwork.state?.revision;
      final approved = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text(context.l10n.text('privateConfirm')),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                context.l10n.text(
                  command.action ==
                          wire.PrivateAction.PRIVATE_ACTION_APPROVE_INVITATION
                      ? 'privateApproveWarning'
                      : 'privateRevokeWarning',
                ),
              ),
              SelectableText(confirmPeer),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: Text(context.l10n.text('cancel')),
            ),
            TextButton(
              key: const Key('private-confirm'),
              onPressed: () => Navigator.pop(context, true),
              child: Text(context.l10n.text('privateConfirm')),
            ),
          ],
        ),
      );
      if (!mounted ||
          approved != true ||
          app.commandPending ||
          app.privateNetwork.contextRevision != epoch ||
          app.privateNetwork.state?.revision != revision) {
        return;
      }
    }
    final result = await app.privateNetwork.command(command);
    if (!mounted || result == null) return;
    if (command.action == wire.PrivateAction.PRIVATE_ACTION_IMPORT_INVITATION) {
      _link.clear();
      _name.clear();
    }
    if (result.invitationLink.isNotEmpty) {
      final link = result.invitationLink;
      final revision = app.privateNetwork.contextRevision;
      await showDialog<void>(
        context: context,
        builder: (context) => ListenableBuilder(
          listenable: app.privateNetwork,
          builder: (context, _) =>
              app.privateNetwork.contextRevision != revision
              ? AlertDialog(
                  content: Text(context.l10n.text('privateError')),
                  actions: [
                    TextButton(
                      onPressed: () => Navigator.pop(context),
                      child: Text(context.l10n.text('cancel')),
                    ),
                  ],
                )
              : AlertDialog(
                  title: Text(context.l10n.text('privateInvitation')),
                  content: SizedBox(
                    width: 260,
                    child: SingleChildScrollView(
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Text(
                            context.l10n.text(
                              command.autoApprove
                                  ? 'privateAccessWarning'
                                  : 'privateInvitationWarning',
                            ),
                          ),
                          const SizedBox(height: 12),
                          QrImageView(
                            data: link,
                            size: 240,
                            backgroundColor: Colors.white,
                          ),
                          SelectableText(link),
                        ],
                      ),
                    ),
                  ),
                  actions: [
                    TextButton(
                      onPressed: () =>
                          Clipboard.setData(ClipboardData(text: link)),
                      child: Text(context.l10n.text('copy')),
                    ),
                    TextButton(
                      onPressed: () => _saveQr(link, revision),
                      child: Text(context.l10n.text('privateSaveQr')),
                    ),
                    TextButton(
                      onPressed: () => Navigator.pop(context),
                      child: Text(context.l10n.text('cancel')),
                    ),
                  ],
                ),
        ),
      );
    }
  }

  Widget _action(
    String label,
    wire.PrivateCommand command, {
    String? confirmPeer,
  }) {
    final onPressed =
        widget.controller.commandPending ||
            widget.controller.privateNetwork.busy ||
            widget.controller.privateNetwork.connectingInvitation
        ? null
        : () => _run(command, confirmPeer: confirmPeer);
    if (label == 'privateRevoke') {
      return FilledButton.tonal(
        style: _destructiveStyle,
        onPressed: onPressed,
        child: Text(context.l10n.text(label)),
      );
    }
    return TextButton(
      onPressed: onPressed,
      child: Text(context.l10n.text(label)),
    );
  }

  ButtonStyle get _destructiveStyle => FilledButton.styleFrom(
    backgroundColor: Theme.of(context).colorScheme.errorContainer,
    foregroundColor: Theme.of(context).colorScheme.onErrorContainer,
  );

  ButtonStyle get _sharingStyle => FilledButton.styleFrom(
    backgroundColor: Theme.of(context).colorScheme.primary
        .withValues(alpha: .18),
    foregroundColor: Theme.of(context).brightness == Brightness.dark
        ? Theme.of(context).colorScheme.secondary
        : Theme.of(context).colorScheme.primary,
  );

  String _stateLabel(String state) => context.l10n.text(switch (state) {
    'open' => 'privateOpen',
    'pending' => 'privatePending',
    'consumed' => 'privateConsumed',
    'imported' => 'privateImported',
    'denied' => 'privateDenied',
    _ => 'privateUnknown',
  });

  String _expiry(int seconds) {
    if (seconds <= 0 || seconds > 8640000000000) {
      return context.l10n.text('privateUnknown');
    }
    final date = DateTime.fromMillisecondsSinceEpoch(
      seconds * 1000,
      isUtc: true,
    ).toLocal();
    if (!date.isAfter(DateTime.now())) {
      return context.l10n.text('privateExpired');
    }
    final locale = MaterialLocalizations.of(context);
    return '${context.l10n.text('privateExpires')} ${locale.formatShortDate(date)} ${locale.formatTimeOfDay(TimeOfDay.fromDateTime(date))}';
  }

  String _errorLabel(IrvioErrorCode error) => context.l10n.text(switch (error) {
    IrvioErrorCode.unauthorized => 'privateDenied',
    IrvioErrorCode.busy => 'privateBusy',
    IrvioErrorCode.invalidArgument => 'privateInvalid',
    IrvioErrorCode.notReady => 'privateNotReady',
    IrvioErrorCode.unsupported ||
    IrvioErrorCode.unsupportedApi => 'privateUnsupported',
    IrvioErrorCode.unavailable => 'privateUnavailable',
    _ => 'privateError',
  });

  Widget _record(
    String title,
    String details,
    List<Widget> actions, {
    bool sharing = false,
  }) => Card(
    margin: sharing ? const EdgeInsets.only(bottom: 12) : null,
    child: Padding(
      padding: EdgeInsets.all(sharing ? 20 : 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            title,
            style: sharing ? Theme.of(context).textTheme.titleMedium : null,
          ),
          if (sharing) const SizedBox(height: 8),
          SelectableText(
            details,
            style: sharing
                ? Theme.of(context).textTheme.bodyMedium?.copyWith(
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                    height: 1.5,
                  )
                : null,
          ),
          if (sharing) const SizedBox(height: 16),
          Wrap(
            spacing: sharing ? 8 : 0,
            runSpacing: sharing ? 8 : 0,
            children: actions,
          ),
        ],
      ),
    ),
  );

  Future<void> _startSharing() async {
    final app = widget.controller;
    final model = app.privateNetwork;
    if (_startingSharing ||
        model.busy ||
        model.connectingInvitation ||
        app.commandPending) {
      return;
    }
    setState(() => _panel = 'share');
    if (model.state?.hostingWanted == true ||
        app.status.state != IrvioConnectionState.disconnected ||
        app.bootstrapState?.configuredCount == 0) {
      return;
    }
    final revision = model.contextRevision;
    setState(() => _startingSharing = true);
    try {
      await _run(
        wire.PrivateCommand(
          action: wire.PrivateAction.PRIVATE_ACTION_CONNECT,
          hosting: true,
        ),
      );
      if (_validContext(revision) && model.state?.hostingWanted == true) {
        await _createAccess();
      }
    } finally {
      if (mounted) setState(() => _startingSharing = false);
    }
  }

  Future<void> _createAccess() => _run(
    wire.PrivateCommand(
      action: wire.PrivateAction.PRIVATE_ACTION_CREATE_INVITATION,
      lifetimeSeconds: 3600,
      autoApprove: true,
    ),
  );

  Future<void> _connectInvitation() async {
    final model = widget.controller.privateNetwork;
    final revision = model.contextRevision;
    await model.connectInvitation(
      _link.text.trim(),
      context.l10n.text('privateFriend'),
    );
    if (_validContext(revision) && model.error == null) {
      _link.clear();
      final peer = model.state?.targetPeerId;
      if (peer != null && peer.isNotEmpty) {
        await widget.controller.selectPrivatePeer(peer);
      }
    }
  }

  Future<void> _showImport() async {
    setState(() => _panel = 'join');
    final revision = widget.controller.privateNetwork.contextRevision;
    final connect = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (dialogContext, update) => AlertDialog(
          title: Text(context.l10n.text('privateJoinChoice')),
          content: SizedBox(
            width: 420,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                  key: const Key('private-invitation-input'),
                  controller: _link,
                  maxLength: 2048,
                  minLines: 2,
                  maxLines: 4,
                  autocorrect: false,
                  enableSuggestions: false,
                  onChanged: (_) => update(() {}),
                  decoration: InputDecoration(
                    labelText: context.l10n.text('privateInvitation'),
                  ),
                ),
                if (_fileError != null) Text(context.l10n.text(_fileError!)),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(dialogContext, false),
              child: Text(context.l10n.text('cancel')),
            ),
            TextButton(
              onPressed: _fileBusy
                  ? null
                  : () async {
                      await _importQr();
                      if (dialogContext.mounted) update(() {});
                    },
              child: Text(context.l10n.text('importQr')),
            ),
            FilledButton(
              key: const Key('private-import-connect'),
              onPressed: _fileBusy || _link.text.trim().isEmpty
                  ? null
                  : () => Navigator.pop(dialogContext, true),
              child: Text(context.l10n.text('connect')),
            ),
          ],
        ),
      ),
    );
    if (connect == true && _validContext(revision)) await _connectInvitation();
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: widget.controller.privateNetwork,
    builder: (context, _) {
      final app = widget.controller;
      final model = app.privateNetwork;
      final state = model.state;
      final idle = app.status.state == IrvioConnectionState.disconnected;
      final s = context.l10n;
      final blocked =
          _startingSharing ||
          model.busy ||
          model.connectingInvitation ||
          app.commandPending;
      final bootstrapMissing =
          app.bootstrapState != null &&
          app.bootstrapState!.configuredCount == 0;
      final panel =
          _panel ??
          (state?.hostingWanted == true ||
                  state?.members.isNotEmpty == true ||
                  state?.invitations.isNotEmpty == true
              ? 'share'
              : (state?.targets.isNotEmpty == true ||
                    state?.enrollments.isNotEmpty == true)
              ? 'join'
              : null);
      return ListView(
        padding: const EdgeInsets.fromLTRB(24, 4, 24, 24),
        children: [
          Text(
            s.text('privateNetwork'),
            key: const Key('private-vpn-title'),
            style: Theme.of(context).textTheme.headlineSmall,
          ),
          const SizedBox(height: 16),
          Center(child: Text(s.text('privateChooseConnectionType'))),
          const SizedBox(height: 16),
          if (!model.supported ||
              app.hello?.negotiatedMinor == null ||
              app.hello!.negotiatedMinor < 7)
            Text(s.text('privateUnsupported'))
          else ...[
            IntrinsicHeight(
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Expanded(
                    child: Tooltip(
                      constraints: const BoxConstraints(maxWidth: 340),
                      padding: const EdgeInsets.symmetric(
                        horizontal: 14,
                        vertical: 10,
                      ),
                      message: [
                        s.text('privateAccessWarning'),
                        if (state?.hostingWanted == true)
                          s.text('privateSharingActive'),
                      ].join('\n\n'),
                      child: FilledButton.tonalIcon(
                        onPressed:
                            blocked ||
                                (!idle &&
                                    app.status.privateTargetPeerId.isNotEmpty)
                            ? null
                            : _startSharing,
                        icon: const Icon(Icons.wifi_tethering),
                        label: Text(s.text('privateShareChoice')),
                      ),
                    ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: FilledButton.tonalIcon(
                      onPressed: blocked || !idle || bootstrapMissing
                          ? null
                          : _showImport,
                      icon: const Icon(Icons.vpn_key_outlined),
                      label: Text(s.text('privateJoinChoice')),
                    ),
                  ),
                ],
              ),
            ),
            const SizedBox(height: 20),
            if (bootstrapMissing) Text(s.text('privateBootstrapRequired')),
            if (model.error != null) Text(_errorLabel(model.error!)),
            if (_fileError != null) Text(s.text(_fileError!)),
            if (panel == 'share' && (model.busy || model.connectingInvitation))
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 12),
                child: Row(
                  children: [
                    const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    ),
                    const SizedBox(width: 12),
                    Text(
                      s.text(
                        model.connectingInvitation
                            ? 'connecting'
                            : 'privateWorking',
                      ),
                    ),
                  ],
                ),
              ),
            if (panel == 'share') ...[
              if (!idle && state?.hostingWanted != true)
                Text(s.text('privateDisconnectFirst')),
              if (state?.hostingWanted == true) ...[
                IntrinsicHeight(
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      Expanded(
                        child: FilledButton.tonal(
                          key: const Key('private-invite-more'),
                          style: _sharingStyle,
                          onPressed: blocked ? null : _createAccess,
                          child: Text(s.text('privateInviteMore')),
                        ),
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: FilledButton.tonal(
                          key: const Key('private-stop-sharing'),
                          style: _destructiveStyle,
                          onPressed: app.commandPending ? null : app.disconnect,
                          child: Text(s.text('privateStopSharing')),
                        ),
                      ),
                    ],
                  ),
                ),
              ],
              const SizedBox(height: 16),
              for (final invitation
                  in state?.invitations ?? <wire.PrivateInvitation>[])
                if (invitation.state != 'consumed')
                  _record(
                    s.text(
                      invitation.state == 'pending'
                          ? 'privatePending'
                          : 'privateWaitingFriend',
                    ),
                    '${_expiry(invitation.expiresAtUnix.toInt())}${invitation.clientPeerId.isEmpty ? '' : '\n${invitation.clientPeerId}'}',
                    [
                      if (invitation.state == 'pending')
                        _action(
                          'privateApprove',
                          wire.PrivateCommand(
                            action: wire
                                .PrivateAction
                                .PRIVATE_ACTION_APPROVE_INVITATION,
                            invitationId: invitation.invitationId,
                          ),
                          confirmPeer: invitation.clientPeerId,
                        ),
                      _action(
                        'privateRevoke',
                        wire.PrivateCommand(
                          action: wire
                              .PrivateAction
                              .PRIVATE_ACTION_CANCEL_INVITATION,
                          invitationId: invitation.invitationId,
                        ),
                        confirmPeer: invitation.clientPeerId.isEmpty
                            ? s.text('privateWaitingFriend')
                            : invitation.clientPeerId,
                      ),
                    ],
                    sharing: true,
                  ),
              for (final member in state?.members ?? <wire.PrivateMember>[])
                if (member.allowed)
                  _record(
                    s.text(
                      !member.hasConnected()
                          ? 'privateUnknown'
                          : member.connected
                          ? 'connected'
                          : 'disconnected',
                    ),
                    member.peerId,
                    [
                      _action(
                        'privateRevoke',
                        wire.PrivateCommand(
                          action:
                              wire.PrivateAction.PRIVATE_ACTION_REVOKE_MEMBER,
                          peerId: member.peerId,
                        ),
                        confirmPeer: member.peerId,
                      ),
                    ],
                    sharing: true,
                  ),
            ],
            if (panel == 'join') ...[
              if (state?.targets.isNotEmpty == true ||
                  model.connectingInvitation) ...[
                PrivatePeerSelector(
                  targets: state?.targets ?? [],
                  selected: app.status.privateTargetPeerId.isNotEmpty
                      ? app.status.privateTargetPeerId
                      : app.selectedPrivatePeer,
                  onChanged: idle && !blocked ? app.selectPrivatePeer : null,
                ),
                const SizedBox(height: 32),
                ConnectionControl(
                  status: app.status.isPrivate || idle
                      ? app.status
                      : const IrvioStatus.disconnected(),
                  enabled:
                      app.daemonAvailable &&
                      !blocked &&
                      (idle || app.status.privateTargetPeerId.isNotEmpty),
                  connectPending: model.connectingInvitation || model.busy,
                  disconnectPending: app.disconnectPending,
                  disconnectIntent:
                      app.status.privateTargetPeerId.isNotEmpty &&
                      app.connectionIntentActive,
                  onConnect: () {
                    final targets = state?.targets ?? <wire.PrivateTarget>[];
                    if (targets.isEmpty || !idle) return;
                    final peer = selectedPrivatePeer(
                      targets,
                      app.selectedPrivatePeer,
                    );
                    app.selectPrivatePeer(peer!);
                    _run(
                      wire.PrivateCommand(
                        action: wire.PrivateAction.PRIVATE_ACTION_CONNECT,
                        peerId: peer,
                      ),
                    );
                  },
                  onDisconnect: app.disconnect,
                ),
              ],
              for (final enrollment
                  in state?.enrollments ?? <wire.PrivateEnrollment>[])
                _record(
                  s.text('privateFriend'),
                  '${enrollment.hostPeerId}\n${model.enrollmentNeedsRetry(enrollment.hostPeerId) ? s.text('privateRetryRequired') : _stateLabel(enrollment.state)}',
                  [
                    if (model.enrollmentNeedsRetry(enrollment.hostPeerId) ||
                        enrollment.state == 'denied')
                      _action(
                        'privateCheck',
                        wire.PrivateCommand(
                          action: wire
                              .PrivateAction
                              .PRIVATE_ACTION_RESUME_ENROLLMENT,
                          peerId: enrollment.hostPeerId,
                        ),
                      ),
                    _action(
                      'cancel',
                      wire.PrivateCommand(
                        action:
                            wire.PrivateAction.PRIVATE_ACTION_CANCEL_ENROLLMENT,
                        peerId: enrollment.hostPeerId,
                      ),
                    ),
                  ],
                ),
              if (model.connectingInvitation || model.busy)
                TextButton(
                  onPressed: app.commandPending ? null : app.disconnect,
                  child: Text(s.text('cancel')),
                ),
            ],
          ],
        ],
      );
    },
  );
}
