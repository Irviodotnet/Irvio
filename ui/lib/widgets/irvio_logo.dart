// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/material.dart';

class IrvioLogo extends StatelessWidget {
  const IrvioLogo({super.key, this.size = 30, this.withWordmark = true});

  final double size;
  final bool withWordmark;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Image.asset(
          'assets/irvio-globe.png',
          key: const Key('irvio-logo'),
          width: size,
          height: size,
          filterQuality: FilterQuality.high,
        ),
        if (withWordmark) ...[
          const SizedBox(width: 9),
          Text(
            'IRVIO',
            style: TextStyle(
              fontSize: size * .62,
              fontWeight: FontWeight.w800,
              letterSpacing: 1.2,
            ),
          ),
        ],
      ],
    );
  }
}
