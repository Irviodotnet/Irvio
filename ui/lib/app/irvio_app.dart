// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';

import '../l10n/irvio_localizations.dart';
import '../theme/irvio_theme.dart';
import 'irvio_app_controller.dart';
import 'irvio_shell.dart';
import 'linux_desktop_integration.dart';

class IrvioApp extends StatefulWidget {
  const IrvioApp({super.key, required this.controller, this.desktop});

  final IrvioAppController controller;
  final LinuxDesktopIntegration? desktop;

  @override
  State<IrvioApp> createState() => _IrvioAppState();
}

class _IrvioAppState extends State<IrvioApp> {
  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_changed);
  }

  @override
  void didUpdateWidget(covariant IrvioApp oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.controller != widget.controller) {
      oldWidget.controller.removeListener(_changed);
      widget.controller.addListener(_changed);
    }
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    widget.controller.removeListener(_changed);
    widget.desktop?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => MaterialApp(
    navigatorKey: widget.desktop?.navigatorKey,
    title: 'IRVIO',
    debugShowCheckedModeBanner: false,
    theme: irvioLightTheme(),
    darkTheme: irvioDarkTheme(),
    themeMode: widget.controller.themeMode,
    locale: widget.controller.language.locale,
    supportedLocales: IrvioLanguage.values
        .map((language) => language.locale)
        .toList(growable: false),
    localizationsDelegates: const [
      IrvioLocalizations.delegate,
      GlobalMaterialLocalizations.delegate,
      GlobalWidgetsLocalizations.delegate,
      GlobalCupertinoLocalizations.delegate,
    ],
    home: IrvioShell(
      controller: widget.controller,
      onDrag: widget.desktop == null
          ? null
          : () => widget.desktop!.startDragging(),
      onMinimize: widget.desktop == null
          ? null
          : () => widget.desktop!.minimize(),
      onClose: widget.desktop == null
          ? null
          : () => widget.desktop!.requestClose(),
    ),
  );
}
