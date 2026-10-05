// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:window_manager/window_manager.dart';

import 'app/irvio_app.dart';
import 'app/irvio_app_controller.dart';
import 'app/linux_autostart.dart';
import 'app/linux_desktop_integration.dart';
import 'app/linux_notifications.dart';
import 'irvio/linux_irvio_client.dart';

const irvioInitialWindowSize = Size(549, 682);

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  if (!Platform.isLinux) {
    throw UnsupportedError('P2 supports Linux desktop only.');
  }
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

  final socketPath =
      Platform.environment['HYDRA_CONTROL_SOCKET'] ?? defaultIrvioSocketPath;
  final controller = IrvioAppController(
    () => LinuxIrvioClient.connectTo(socketPath: socketPath),
    autostart: LinuxXdgAutostart(),
    notifications: const LinuxDesktopNotificationSink(),
  );
  final desktop = LinuxDesktopIntegration(controller);
  runApp(IrvioApp(controller: controller, desktop: desktop));
  unawaited(controller.start());
  unawaited(desktop.initialize());
}
