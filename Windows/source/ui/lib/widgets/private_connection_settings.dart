// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';

import 'package:flutter/material.dart';

import '../app/irvio_app_controller.dart';
import '../irvio/generated/irvio/local/v1/control.pb.dart' as wire;
import '../l10n/irvio_localizations.dart';
import '../models/irvio_models.dart';
import 'panel.dart';
import 'private_peer_selector.dart';

class PrivateConnectionSettings extends StatefulWidget {
  const PrivateConnectionSettings({super.key, required this.controller});
  final IrvioAppController controller;
  @override
  State<PrivateConnectionSettings> createState() =>
      _PrivateConnectionSettingsState();
}

class _PrivateConnectionSettingsState extends State<PrivateConnectionSettings> {
  String? _selected;
  bool _deleting = false;

  Future<bool> _waitDisconnected() async {
    final app = widget.controller;
    if (app.status.state == IrvioConnectionState.disconnected) return true;
    final ready = Completer<bool>();
    void check() {
      if (!ready.isCompleted &&
          app.status.state == IrvioConnectionState.disconnected) {
        ready.complete(true);
      }
    }

    app.addListener(check);
    final timer = Timer(
      const Duration(seconds: 15),
      () => ready.complete(false),
    );
    try {
      check();
      return await ready.future;
    } finally {
      timer.cancel();
      app.removeListener(check);
    }
  }

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) widget.controller.privateNetwork.refresh();
    });
  }

  Future<void> _delete(String peer) async {
    final app = widget.controller;
    final model = app.privateNetwork;
    final epoch = model.contextRevision;
    final identity = model.state?.localPeerId;
    setState(() => _deleting = true);
    try {
      var skip = false;
      if (!app.skipFriendDeleteConfirmation) {
        final approved = await showDialog<bool>(
          context: context,
          builder: (context) => StatefulBuilder(
            builder: (context, update) => AlertDialog(
              title: Text(context.l10n.text('privateDeleteConnection')),
              content: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(context.l10n.text('privateDeleteWarning')),
                  const SizedBox(height: 12),
                  Text(privatePeerLabel(peer, [peer])),
                  CheckboxListTile(
                    contentPadding: EdgeInsets.zero,
                    title: Text(context.l10n.text('dontShowAgain')),
                    value: skip,
                    onChanged: (value) => update(() => skip = value ?? false),
                  ),
                ],
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(context, false),
                  child: Text(context.l10n.text('cancel')),
                ),
                FilledButton(
                  key: const Key('private-delete-confirm'),
                  style: FilledButton.styleFrom(
                    backgroundColor: Theme.of(context).colorScheme.error,
                  ),
                  onPressed: () => Navigator.pop(context, true),
                  child: Text(context.l10n.text('delete')),
                ),
              ],
            ),
          ),
        );
        if (approved != true) return;
      }
      if (!mounted ||
          epoch != model.contextRevision ||
          app.commandPending ||
          model.busy) {
        return;
      }
      if (app.status.privateTargetPeerId == peer ||
          model.state?.targetPeerId == peer) {
        await app.disconnect();
        if (!mounted ||
            app.apiError != null ||
            !await _waitDisconnected() ||
            !mounted) {
          return;
        }
        await model.refresh();
        if (!mounted ||
            identity == null ||
            identity != model.state?.localPeerId ||
            model.state?.targets.any((target) => target.peerId == peer) !=
                true) {
          return;
        }
      }
      final removeEpoch = model.contextRevision;
      if (skip) await app.setSkipFriendDeleteConfirmation(true);
      if (!mounted || removeEpoch != model.contextRevision) return;
      await model.command(
        wire.PrivateCommand(
          action: wire.PrivateAction.PRIVATE_ACTION_REMOVE_TARGET,
          peerId: peer,
        ),
      );
    } finally {
      if (mounted) setState(() => _deleting = false);
    }
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: widget.controller.privateNetwork,
    builder: (context, _) {
      final app = widget.controller;
      final model = app.privateNetwork;
      if (!model.supported) return const SizedBox.shrink();
      final targets = model.state?.targets ?? <wire.PrivateTarget>[];
      final selected = selectedPrivatePeer(targets, _selected);
      final blocked =
          _deleting ||
          app.commandPending ||
          model.busy ||
          model.connectingInvitation ||
          !app.daemonAvailable;
      return Padding(
        padding: const EdgeInsets.only(top: 16),
        child: IrvioPanel(
          padding: const EdgeInsets.all(14),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(context.l10n.text('privateSavedConnections')),
              const SizedBox(height: 12),
              if (targets.isEmpty)
                Text(context.l10n.text('privateNoConnections'))
              else ...[
                PrivatePeerSelector(
                  targets: targets,
                  selected: selected,
                  onChanged: blocked
                      ? null
                      : (peer) => setState(() => _selected = peer),
                ),
                const SizedBox(height: 12),
                OutlinedButton(
                  key: const Key('private-delete-connection'),
                  onPressed: blocked || selected == null
                      ? null
                      : () => _delete(selected),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: Theme.of(context).colorScheme.error,
                  ),
                  child: Text(context.l10n.text('privateDeleteConnection')),
                ),
              ],
              if (model.error != null)
                Text(
                  context.l10n.text('privateError'),
                  style: TextStyle(color: Theme.of(context).colorScheme.error),
                ),
            ],
          ),
        ),
      );
    },
  );
}
