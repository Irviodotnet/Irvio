// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/material.dart';

import '../irvio/generated/irvio/local/v1/control.pb.dart' as wire;
import '../l10n/irvio_localizations.dart';

String? selectedPrivatePeer(
  List<wire.PrivateTarget> targets,
  String? selected,
) => targets.any((target) => target.peerId == selected)
    ? selected
    : targets.firstOrNull?.peerId;

String privatePeerLabel(String peer, Iterable<String> peers) {
  if (peer.length <= 12) return peer;
  for (var length = 5; length < peer.length - 4; length++) {
    final prefix = peer.substring(0, length);
    final suffix = peer.substring(peer.length - 4);
    if (!peers.any(
      (other) =>
          other != peer && other.startsWith(prefix) && other.endsWith(suffix),
    )) {
      return '$prefix…$suffix';
    }
  }
  return peer;
}

class PrivatePeerSelector extends StatelessWidget {
  const PrivatePeerSelector({
    super.key,
    required this.targets,
    this.selected,
    this.onChanged,
  });
  final List<wire.PrivateTarget> targets;
  final String? selected;
  final ValueChanged<String>? onChanged;

  @override
  Widget build(BuildContext context) {
    final value = selectedPrivatePeer(targets, selected);
    return DropdownButtonFormField<String>(
      key: ValueKey('private-peer-$value'),
      initialValue: value,
      isExpanded: true,
      decoration: InputDecoration(
        labelText: context.l10n.text('privateSavedConnections'),
      ),
      items: targets
          .map(
            (target) => DropdownMenuItem(
              value: target.peerId,
              child: Tooltip(
                message: target.peerId,
                child: Text(
                  privatePeerLabel(
                    target.peerId,
                    targets.map((target) => target.peerId),
                  ),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ),
          )
          .toList(),
      onChanged: onChanged == null
          ? null
          : (value) {
              if (value != null) onChanged!(value);
            },
    );
  }
}
