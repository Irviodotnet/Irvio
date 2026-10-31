// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:file_selector/file_selector.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:qr_flutter/qr_flutter.dart';

import '../app/irvio_app_controller.dart';
import '../irvio/bootstrap_qr.dart';
import '../l10n/irvio_localizations.dart';
import '../widgets/panel.dart';

class ShareIrvioScreen extends StatefulWidget {
  const ShareIrvioScreen({super.key, required this.controller});

  final IrvioAppController controller;

  @override
  State<ShareIrvioScreen> createState() => _ShareIrvioScreenState();
}

class _ShareIrvioScreenState extends State<ShareIrvioScreen> {
  bool _requested = false;
  bool _saving = false;

  Future<void> _saveQr() async {
    final bundle = widget.controller.sharedBootstrapBundle;
    if (bundle == null || _saving) return;
    final strings = context.l10n;
    setState(() => _saving = true);
    try {
      final location = await getSaveLocation(
        suggestedName: 'irvio-bootstrap.png',
        acceptedTypeGroups: const [
          XTypeGroup(label: 'PNG', extensions: ['png']),
        ],
      );
      if (location == null) return;
      final bytes = await compute(encodeIrvioBootstrapQr, bundle.code);
      await XFile.fromData(bytes, mimeType: 'image/png').saveTo(location.path);
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(strings.text('qrSaved'))));
      }
    } on Object {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text(strings.text('qrSaveFailed'))));
      }
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (!_requested &&
        widget.controller.bootstrapAvailable &&
        widget.controller.sharedBootstrapBundle == null) {
      _requested = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        widget.controller.generateBootstrapBundle();
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final bundle = widget.controller.sharedBootstrapBundle;
    final supported =
        widget.controller.hello?.supportsV11 == true &&
        widget.controller.bootstrapAvailable;
    return Padding(
      padding: const EdgeInsets.fromLTRB(24, 4, 24, 22),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            context.l10n.text('shareIrvio'),
            style: Theme.of(context).textTheme.headlineSmall,
          ),
          const SizedBox(height: 7),
          Text(
            context.l10n.text('shareDescription'),
            style: Theme.of(context).textTheme.bodySmall,
          ),
          const SizedBox(height: 18),
          Expanded(
            child: Center(
              child: IrvioPanel(
                child: SizedBox.square(
                  dimension: 190,
                  child: bundle == null
                      ? Center(
                          child: widget.controller.bootstrapPending
                              ? const CircularProgressIndicator()
                              : Icon(
                                  Icons.qr_code_2_rounded,
                                  size: 86,
                                  color: Theme.of(context).disabledColor
                                      .withValues(alpha: .55),
                                ),
                        )
                      : DecoratedBox(
                          decoration: BoxDecoration(
                            color: Colors.white,
                            borderRadius: BorderRadius.circular(8),
                          ),
                          child: Padding(
                            padding: const EdgeInsets.all(10),
                            child: QrImageView(
                              key: const Key('share-qr'),
                              data: bundle.code,
                              version: QrVersions.auto,
                              errorCorrectionLevel: QrErrorCorrectLevel.M,
                              backgroundColor: Colors.white,
                              eyeStyle: const QrEyeStyle(
                                eyeShape: QrEyeShape.square,
                                color: Colors.black,
                              ),
                              dataModuleStyle: const QrDataModuleStyle(
                                dataModuleShape: QrDataModuleShape.square,
                                color: Colors.black,
                              ),
                            ),
                          ),
                        ),
                ),
              ),
            ),
          ),
          Center(
            child: Text(
              !supported
                  ? context.l10n.text('shareUnavailableV10')
                  : bundle == null
                  ? widget.controller.bootstrapError ??
                        context.l10n.text('noReachableBootstrap')
                  : context.l10n.reachableNodes(bundle.nodeCount),
              key: const Key('share-status'),
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ),
          if (bundle != null) ...[
            const SizedBox(height: 3),
            Center(
              child: Text(
                context.l10n.generatedAge(
                  DateTime.now().difference(bundle.generatedAt),
                ),
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ),
          ],
          const SizedBox(height: 14),
          SizedBox(
            width: double.infinity,
            child: OutlinedButton.icon(
              key: const Key('share-refresh'),
              onPressed:
                  supported && !widget.controller.bootstrapPending && !_saving
                  ? widget.controller.generateBootstrapBundle
                  : null,
              icon: const Icon(Icons.refresh_rounded),
              label: Text(context.l10n.text('refreshQr')),
            ),
          ),
          const SizedBox(height: 8),
          SizedBox(
            width: double.infinity,
            child: OutlinedButton.icon(
              key: const Key('share-save'),
              onPressed:
                  bundle == null ||
                      widget.controller.bootstrapPending ||
                      _saving
                  ? null
                  : _saveQr,
              icon: const Icon(Icons.save_alt_rounded),
              label: Text(context.l10n.text('saveQr')),
            ),
          ),
          const SizedBox(height: 8),
          SizedBox(
            width: double.infinity,
            child: OutlinedButton.icon(
              key: const Key('share-copy'),
              onPressed: bundle == null
                  ? null
                  : () => Clipboard.setData(ClipboardData(text: bundle.code)),
              icon: const Icon(Icons.copy_rounded),
              label: Text(context.l10n.text('copyBootstrap')),
            ),
          ),
        ],
      ),
    );
  }
}
