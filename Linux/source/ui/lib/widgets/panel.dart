// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/material.dart';

class IrvioPanel extends StatelessWidget {
  const IrvioPanel({
    super.key,
    required this.child,
    this.padding = const EdgeInsets.all(18),
  });

  final Widget child;
  final EdgeInsets padding;

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    return DecoratedBox(
      decoration: BoxDecoration(
        color: dark
            ? const Color(0xB3121E2B)
            : Colors.white.withValues(alpha: .9),
        borderRadius: BorderRadius.circular(13),
        border: Border.all(
          color: dark ? const Color(0xFF273545) : const Color(0xFFD7E2EE),
        ),
        boxShadow: dark
            ? null
            : const [BoxShadow(color: Color(0x15000000), blurRadius: 16)],
      ),
      child: Padding(padding: padding, child: child),
    );
  }
}
