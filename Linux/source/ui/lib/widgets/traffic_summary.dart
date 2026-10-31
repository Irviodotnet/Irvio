// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/material.dart';

import '../l10n/irvio_localizations.dart';
import '../models/irvio_models.dart';

// Decimal bytes, never bits. Integer arithmetic preserves uint64 precision.
String formatTrafficBytes(BigInt? bytes) {
  if (bytes == null || bytes.isNegative) return '—';
  final mb = BigInt.from(1000000);
  final gb = mb * BigInt.from(1000);
  if (bytes < gb) return '${bytes ~/ mb} MB';
  var unit = gb;
  var label = 'GB';
  if (bytes >= gb * BigInt.from(1000)) {
    unit *= BigInt.from(1000);
    label = 'TB';
  }
  final tenths = (bytes * BigInt.from(10) + unit ~/ BigInt.two) ~/ unit;
  final whole = tenths ~/ BigInt.from(10);
  final fraction = tenths % BigInt.from(10);
  return '$whole${fraction == BigInt.zero ? '' : '.$fraction'} $label';
}

class TrafficSummary extends StatelessWidget {
  const TrafficSummary({super.key, required this.stats});
  final ValueNotifier<IrvioTrafficStats?> stats;

  @override
  Widget build(
    BuildContext context,
  ) => ValueListenableBuilder<IrvioTrafficStats?>(
    valueListenable: stats,
    builder: (context, value, _) {
      final strings = context.l10n;
      final partial =
          value != null && !value.complete && value.sampledAt != null;
      return Tooltip(
        message: strings.text(
          partial ? 'trafficPartialHint' : 'trafficSessionHint',
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            _Stat(
              label: strings.text('trafficClients'),
              value: (value?.clients ?? 0) == 0 ? '—' : '${value!.clients}',
            ),
            const SizedBox(height: 5),
            _Stat(
              label: strings.text('trafficShared'),
              value:
                  '${partial && value.sharedUploadBytes != null ? '≈ ' : ''}${formatTrafficBytes(value?.sharedUploadBytes)}',
            ),
            const SizedBox(height: 5),
            _Stat(
              label: strings.text('trafficMine'),
              value:
                  '${partial && value.myTotalBytes != null ? '≈ ' : ''}${formatTrafficBytes(value?.myTotalBytes)}',
            ),
          ],
        ),
      );
    },
  );
}

class _Stat extends StatelessWidget {
  const _Stat({required this.label, required this.value});
  final String label;
  final String value;

  @override
  Widget build(BuildContext context) => Container(
    padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
    decoration: BoxDecoration(
      color: Theme.of(context).colorScheme.onSurface.withValues(alpha: 0.035),
      borderRadius: BorderRadius.circular(9),
      border: Border.all(
        color: Theme.of(context).colorScheme.onSurface.withValues(alpha: 0.07),
      ),
    ),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          label,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: Theme.of(context).textTheme.labelSmall
              ?.copyWith(fontSize: 9, height: 1.1, letterSpacing: 0.3),
        ),
        Text(
          value,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: Theme.of(context).textTheme.bodyMedium?.copyWith(
            fontWeight: FontWeight.w600,
            fontSize: 13,
            height: 1.1,
          ),
        ),
      ],
    ),
  );
}
