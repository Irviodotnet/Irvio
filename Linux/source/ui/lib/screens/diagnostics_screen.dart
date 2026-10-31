// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';

import 'package:flutter/material.dart';

import '../app/irvio_app_controller.dart';
import '../l10n/irvio_localizations.dart';
import '../widgets/panel.dart';

class DiagnosticsScreen extends StatefulWidget {
  const DiagnosticsScreen({super.key, required this.controller});

  final IrvioAppController controller;

  @override
  State<DiagnosticsScreen> createState() => _DiagnosticsScreenState();
}

class _DiagnosticsScreenState extends State<DiagnosticsScreen> {
  late final Timer _timer = Timer.periodic(const Duration(seconds: 1), (_) {
    if (mounted) setState(() {});
  });

  @override
  void dispose() {
    _timer.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final controller = widget.controller;
    final status = controller.status;
    return Padding(
      padding: const EdgeInsets.fromLTRB(24, 4, 24, 24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            context.l10n.text('diagnostics'),
            style: Theme.of(context).textTheme.headlineSmall,
          ),
          const SizedBox(height: 16),
          IrvioPanel(
            child: Column(
              children: [
                _DiagnosticRow(
                  label: context.l10n.text('daemonStatus'),
                  value: controller.daemonAvailable
                      ? context.l10n.text('running')
                      : context.l10n.text('unavailable'),
                  healthy: controller.daemonAvailable,
                ),
                _DiagnosticRow(
                  label: context.l10n.text('currentState'),
                  value: context.l10n.connectionState(status.state),
                ),
                _DiagnosticRow(
                  label: context.l10n.text('selectedCountry'),
                  value: status.countryCode.isEmpty
                      ? '—'
                      : context.l10n.countryName(status.countryCode),
                ),
                _DiagnosticRow(
                  label: context.l10n.text('connectionUptime'),
                  value: formatConnectionUptime(
                    status.connectedSince,
                    DateTime.now(),
                  ),
                ),
                _DiagnosticRow(
                  label: context.l10n.text('localApi'),
                  value: controller.hello == null
                      ? '—'
                      : '${controller.hello!.serverMajor}.${controller.hello!.serverMinor}',
                ),
                _DiagnosticRow(
                  label: context.l10n.text('lastError'),
                  value: status.errorMessage ?? controller.apiError ?? '—',
                  last: true,
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _DiagnosticRow extends StatelessWidget {
  const _DiagnosticRow({
    required this.label,
    required this.value,
    this.healthy,
    this.last = false,
  });

  final String label;
  final String value;
  final bool? healthy;
  final bool last;

  @override
  Widget build(BuildContext context) => Padding(
    padding: EdgeInsets.only(bottom: last ? 0 : 13),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(
          width: 125,
          child: Text(label, style: Theme.of(context).textTheme.bodySmall),
        ),
        if (healthy != null) ...[
          Container(
            width: 8,
            height: 8,
            margin: const EdgeInsets.only(top: 5, right: 8),
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              color: healthy! ? const Color(0xFF43DF6D) : Colors.redAccent,
            ),
          ),
        ],
        Expanded(child: Text(value)),
      ],
    ),
  );
}

String formatConnectionUptime(DateTime? since, DateTime now) {
  if (since == null) return '—';
  final elapsed = now.difference(since);
  if (elapsed.isNegative) return '—';
  final hours = elapsed.inHours;
  final minutes = elapsed.inMinutes.remainder(60);
  final seconds = elapsed.inSeconds.remainder(60);
  return '${hours.toString().padLeft(2, '0')}:'
      '${minutes.toString().padLeft(2, '0')}:'
      '${seconds.toString().padLeft(2, '0')}';
}
