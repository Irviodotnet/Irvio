// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import '../models/irvio_models.dart';
import 'generated/irvio/local/v1/control.pb.dart' as wire;

abstract interface class PrivateIrvioClient {
  Future<wire.PrivateState> getPrivateState();

  Future<wire.PrivateCommandResult> privateCommand(wire.PrivateCommand command);
}

typedef PrivatePageReader = Future<wire.PrivateState> Function(int offset);

Future<wire.PrivateState> readPrivateSnapshot(PrivatePageReader read) async {
  for (var attempt = 0; attempt < 2; attempt++) {
    final first = await read(0);
    final counts = _counts(first);
    if (counts[0] > 128 ||
        counts[1] > 256 ||
        counts[2] > 128 ||
        counts[3] > 128) {
      throw _invalidPage;
    }
    final result = first.deepCopy();
    var changed = false;
    for (
      var offset = 0;
      offset < counts.fold(1, (a, b) => a > b ? a : b);
      offset += 64
    ) {
      final page = offset == 0 ? first : await read(offset);
      if (page.revision != first.revision ||
          page.localPeerId != first.localPeerId ||
          page.targetPeerId != first.targetPeerId ||
          page.hostingWanted != first.hostingWanted ||
          !_sameCounts(counts, _counts(page))) {
        changed = true;
        break;
      }
      final lengths = [
        page.targets.length,
        page.members.length,
        page.invitations.length,
        page.enrollments.length,
      ];
      for (var i = 0; i < counts.length; i++) {
        if (lengths[i] != (counts[i] - offset).clamp(0, 64)) {
          throw _invalidPage;
        }
      }
      if (offset != 0) {
        result.targets.addAll(page.targets);
        result.members.addAll(page.members);
        result.invitations.addAll(page.invitations);
        result.enrollments.addAll(page.enrollments);
      }
    }
    if (!changed) return result..freeze();
  }
  throw const IrvioApiException(
    IrvioErrorCode.busy,
    'Private state changed; refresh to try again.',
  );
}

const _invalidPage = IrvioApiException(
  IrvioErrorCode.protocol,
  'Invalid private state page.',
);

List<int> _counts(wire.PrivateState state) => [
  state.targetCount,
  state.memberCount,
  state.invitationCount,
  state.enrollmentCount,
];

bool _sameCounts(List<int> a, List<int> b) {
  for (var i = 0; i < a.length; i++) {
    if (a[i] != b[i]) return false;
  }
  return true;
}
