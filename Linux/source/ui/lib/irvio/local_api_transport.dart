// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:typed_data';

// Adapters own authenticated OS connections; the shared client owns protobuf framing and requests.
abstract interface class LocalApiTransport {
  Stream<Uint8List> get bytes;
  Future<void> write(Uint8List frame);
  Future<void> close();
}
