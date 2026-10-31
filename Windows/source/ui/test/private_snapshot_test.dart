// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:fixnum/fixnum.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/irvio/generated/irvio/local/v1/control.pb.dart' as wire;
import 'package:irvio/irvio/private_irvio_client.dart';
import 'package:irvio/models/irvio_models.dart';

wire.PrivateState page(int offset, {int revision = 1, int count = 70}) =>
    wire.PrivateState(
      localPeerId: 'local',
      revision: Int64(revision),
      targetCount: count,
      targets: List.generate(
        (count - offset).clamp(0, 64),
        (i) => wire.PrivateTarget(peerId: 'target-${offset + i}'),
      ),
    );

void main() {
  test('pages form one immutable snapshot', () async {
    final offsets = <int>[];
    final snapshot = await readPrivateSnapshot((offset) async {
      offsets.add(offset);
      return page(offset);
    });
    expect(offsets, [0, 64]);
    expect(snapshot.targets.length, 70);
    expect(snapshot.targets.last.peerId, 'target-69');
    expect(() => snapshot.targets.clear(), throwsUnsupportedError);
  });

  test('changed revision discards earlier pages before retry', () async {
    var calls = 0;
    final snapshot = await readPrivateSnapshot((offset) async {
      calls++;
      return page(offset, revision: calls == 1 ? 1 : 2);
    });
    expect(calls, 4);
    expect(snapshot.revision, Int64(2));
    expect(snapshot.targets.length, 70);
  });

  test('constant changes terminate with BUSY', () async {
    var calls = 0;
    await expectLater(
      readPrivateSnapshot((offset) async => page(offset, revision: ++calls)),
      throwsA(
        isA<IrvioApiException>().having(
          (e) => e.code,
          'code',
          IrvioErrorCode.busy,
        ),
      ),
    );
    expect(calls, 4);
  });

  test(
    'hosting transition is inconsistent even with unchanged revision',
    () async {
      await expectLater(
        readPrivateSnapshot(
          (offset) async => page(offset)..hostingWanted = offset != 0,
        ),
        throwsA(
          isA<IrvioApiException>().having(
            (e) => e.code,
            'code',
            IrvioErrorCode.busy,
          ),
        ),
      );
    },
  );

  test('invalid counts and incomplete pages fail closed', () async {
    for (final invalid in [
      page(0, count: 129),
      page(0)..targets.removeLast(),
      wire.PrivateState(targets: [wire.PrivateTarget(peerId: 'unexpected')]),
    ]) {
      await expectLater(
        readPrivateSnapshot((_) async => invalid),
        throwsA(
          isA<IrvioApiException>().having(
            (e) => e.code,
            'code',
            IrvioErrorCode.protocol,
          ),
        ),
      );
    }
  });

  test('empty state needs exactly one page', () async {
    var calls = 0;
    final snapshot = await readPrivateSnapshot((_) async {
      calls++;
      return wire.PrivateState();
    });
    expect(calls, 1);
    expect(snapshot.targets, isEmpty);
  });
}
