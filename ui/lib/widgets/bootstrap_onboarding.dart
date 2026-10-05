// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';

import '../app/irvio_app_controller.dart';
import '../irvio/bootstrap_qr.dart';
import '../l10n/irvio_localizations.dart';
import '../models/irvio_models.dart';
import 'panel.dart';

class BootstrapOnboarding extends StatefulWidget {
  const BootstrapOnboarding({super.key, required this.controller});

  final IrvioAppController controller;

  @override
  State<BootstrapOnboarding> createState() => _BootstrapOnboardingState();
}

class _BootstrapOnboardingState extends State<BootstrapOnboarding> {
  final _code = TextEditingController();
  String? _error;

  @override
  void dispose() {
    _code.dispose();
    super.dispose();
  }

  Future<void> _importText() async {
    final value = _code.text.trim();
    if (value.isEmpty) {
      setState(
        () => _error = widget.controller.strings.text('pasteBootstrapError'),
      );
      return;
    }
    setState(() => _error = null);
    await widget.controller.importBootstrapBundle(value);
  }

  Future<void> _importImage() async {
    try {
      final selected = await openFile(
        acceptedTypeGroups: [
          XTypeGroup(
            label: widget.controller.strings.text('qrImages'),
            extensions: const ['png', 'jpg', 'jpeg'],
          ),
        ],
      );
      if (selected == null) return;
      if (await selected.length() > maxBootstrapQrImageBytes) {
        if (mounted) {
          setState(() => _error = widget.controller.strings.text('qrTooLarge'));
        }
        return;
      }
      final text = decodeIrvioBootstrapQr(await selected.readAsBytes());
      _code.text = text;
      setState(() => _error = null);
      await widget.controller.importBootstrapBundle(text);
    } on Object {
      if (mounted) {
        setState(() => _error = widget.controller.strings.text('noIrvioQr'));
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final state = widget.controller.bootstrapState;
    final joining = state?.phase == IrvioBootstrapPhase.connecting;
    return SingleChildScrollView(
      key: const Key('bootstrap-onboarding'),
      padding: const EdgeInsets.fromLTRB(24, 4, 24, 22),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            context.l10n.text('joinIrvio'),
            style: Theme.of(context).textTheme.headlineSmall,
          ),
          const SizedBox(height: 8),
          Text(
            context.l10n.text('provideIntroduction'),
            style: Theme.of(context).textTheme.bodySmall,
          ),
          const SizedBox(height: 18),
          IrvioPanel(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                TextField(
                  key: const Key('bootstrap-code'),
                  controller: _code,
                  minLines: 2,
                  maxLines: 4,
                  maxLength: 24576,
                  decoration: InputDecoration(
                    labelText: context.l10n.text('pasteBootstrap'),
                  ),
                ),
                const SizedBox(height: 8),
                FilledButton(
                  key: const Key('bootstrap-import-text'),
                  onPressed: widget.controller.bootstrapPending
                      ? null
                      : _importText,
                  child: Text(context.l10n.text('joinNetwork')),
                ),
                const SizedBox(height: 8),
                OutlinedButton.icon(
                  key: const Key('bootstrap-import-qr'),
                  onPressed: widget.controller.bootstrapPending
                      ? null
                      : _importImage,
                  icon: const Icon(Icons.image_outlined),
                  label: Text(context.l10n.text('importQr')),
                ),
              ],
            ),
          ),
          if (joining) ...[
            const SizedBox(height: 14),
            const LinearProgressIndicator(),
            const SizedBox(height: 7),
            Text(context.l10n.text('authenticatingIntroducer')),
          ],
          if (_error != null ||
              widget.controller.bootstrapError != null ||
              state?.userMessage != null) ...[
            const SizedBox(height: 12),
            Text(
              _localizedBootstrapError(
                context.l10n,
                _error ??
                    widget.controller.bootstrapError ??
                    state!.userMessage!,
              ),
              key: const Key('bootstrap-error'),
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          ],
        ],
      ),
    );
  }
}

String _localizedBootstrapError(IrvioLocalizations strings, String value) {
  final normalized = value.toLowerCase();
  if (normalized.contains('unsupported') && normalized.contains('version')) {
    return strings.text('unsupportedBootstrap');
  }
  if (normalized.contains('too large') || normalized.contains('oversized')) {
    return strings.text('bundleTooLarge');
  }
  if (normalized.contains('no valid') || normalized.contains('no usable')) {
    return strings.text('noValidBootstrapNodes');
  }
  if (normalized.contains('invalid') || normalized.contains('malformed')) {
    return strings.text('invalidBootstrap');
  }
  return value;
}
