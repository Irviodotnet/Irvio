// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/material.dart';

class CountryFlag extends StatelessWidget {
  const CountryFlag({super.key, required this.code});

  final String code;

  @override
  Widget build(BuildContext context) {
    final normalized = code.toLowerCase();
    const fallback = Icon(Icons.public, size: 21);
    if (!RegExp(r'^[a-z]{2}$').hasMatch(normalized)) return fallback;
    return Image.asset(
      'assets/flags/$normalized.png',
      width: 28,
      height: 21,
      fit: BoxFit.contain,
      excludeFromSemantics: true,
      errorBuilder: (context, error, stackTrace) => fallback,
    );
  }
}
