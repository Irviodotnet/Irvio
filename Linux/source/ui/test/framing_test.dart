// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:irvio/irvio/linux_irvio_client.dart';
import 'package:irvio/models/irvio_models.dart';

void main() {
  Uint8List frame(List<int> payload) {
    final header = ByteData(4)..setUint32(0, payload.length, Endian.big);
    return Uint8List.fromList([...header.buffer.asUint8List(), ...payload]);
  }

  test('decodes complete and partial frames', () {
    final bytes = frame([1, 2, 3, 4]);
    expect(
      decodeLocalApiChunks([
        Uint8List.sublistView(bytes, 0, 2),
        Uint8List.sublistView(bytes, 2, 5),
        Uint8List.sublistView(bytes, 5),
      ]).single,
      [1, 2, 3, 4],
    );
  });

  test('decodes multiple frames without retaining an unbounded chunk', () {
    final bytes = Uint8List.fromList([
      ...frame([1]),
      ...frame([2, 3]),
    ]);
    expect(decodeLocalApiChunks([bytes]).toList(), [
      [1],
      [2, 3],
    ]);
  });

  test('rejects zero and oversized lengths before payload allocation', () {
    final zero = Uint8List(4);
    final oversized = ByteData(4)
      ..setUint32(0, maxLocalApiFrame + 1, Endian.big);

    expect(
      () => decodeLocalApiChunks([zero]).toList(),
      throwsA(isA<IrvioApiException>()),
    );
    expect(
      () => decodeLocalApiChunks([oversized.buffer.asUint8List()]).toList(),
      throwsA(isA<IrvioApiException>()),
    );
  });

  test(
    'truncated frame produces no payload and allocates only declared size',
    () {
      final bytes = frame([1, 2, 3]);
      expect(
        decodeLocalApiChunks([Uint8List.sublistView(bytes, 0, 5)]),
        isEmpty,
      );
    },
  );
}
