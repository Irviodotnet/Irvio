// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:window_manager/window_manager.dart';

import 'app/irvio_app.dart';
import 'app/irvio_app_controller.dart';
import 'app/desktop_platform.dart';
import 'app/desktop_integration.dart';

const irvioInitialWindowSize = Size(620, 682);

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final platform = DesktopPlatform.current();
  await windowManager.ensureInitialized();
  const options = WindowOptions(
    size: irvioInitialWindowSize,
    minimumSize: Size(480, 580),
    center: true,
    backgroundColor: Colors.transparent,
    title: 'IRVIO',
    titleBarStyle: TitleBarStyle.hidden,
    windowButtonVisibility: false,
  );
  windowManager.waitUntilReadyToShow(options, () async {
    await windowManager.show();
    await windowManager.focus();
  });

  final controller = IrvioAppController(
    platform.connect,
    autostart: platform.autostart,
    notifications: platform.notifications,
  );
  final desktop = DesktopIntegration(
    controller,
    trayIcon: platform.trayIcon,
    manualTrayMenu: platform.manualTrayMenu,
  );
  runApp(IrvioApp(controller: controller, desktop: desktop));
  unawaited(controller.start());
  unawaited(desktop.initialize());
}
