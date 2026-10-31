// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/material.dart';

import '../app/irvio_app_controller.dart';
import '../l10n/irvio_localizations.dart';
import '../widgets/irvio_logo.dart';
import '../widgets/panel.dart';

class AboutScreen extends StatelessWidget {
  const AboutScreen({super.key, required this.controller});

  final IrvioAppController controller;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(24, 4, 24, 24),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          context.l10n.text('about'),
          style: Theme.of(context).textTheme.headlineSmall,
        ),
        const SizedBox(height: 16),
        IrvioPanel(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const IrvioLogo(size: 34),
              const SizedBox(height: 18),
              Text(
                context.l10n.text('tagline'),
                style: const TextStyle(fontWeight: FontWeight.w600),
              ),
              const SizedBox(height: 10),
              Text(
                context.l10n.text('aboutLifecycle'),
                style: Theme.of(context).textTheme.bodySmall,
              ),
              const Divider(height: 30),
              _VersionRow(
                label: context.l10n.text('interface'),
                value: '1.0.0',
              ),
              _VersionRow(
                label: context.l10n.text('localApi'),
                value: controller.hello == null
                    ? '1.0'
                    : '${controller.hello!.serverMajor}.${controller.hello!.serverMinor}',
              ),
              _VersionRow(
                label: context.l10n.text('irvioCore'),
                value: controller.hello?.coreVersion ?? 'v1',
              ),
              const Divider(height: 30),
              Text(
                context.l10n.text('geoAttribution'),
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ],
          ),
        ),
      ],
    ),
  );
}

class _VersionRow extends StatelessWidget {
  const _VersionRow({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 4),
    child: Row(
      children: [
        Expanded(
          child: Text(label, style: Theme.of(context).textTheme.bodySmall),
        ),
        Text(value),
      ],
    ),
  );
}
