// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:io';

import '../irvio/irvio_client.dart';
import '../irvio/linux_irvio_client.dart';
import '../irvio/windows_irvio_client.dart';
import 'linux_autostart.dart';
import 'linux_notifications.dart';
import 'windows_desktop.dart';

class DesktopPlatform {
  const DesktopPlatform({
    required this.connect,
    required this.autostart,
    required this.notifications,
    required this.trayIcon,
    this.manualTrayMenu = false,
  });

  final Future<IrvioClient> Function() connect;
  final IrvioAutostart autostart;
  final IrvioNotificationSink notifications;
  final String trayIcon;
  final bool manualTrayMenu;

  static DesktopPlatform current() {
    if (Platform.isLinux) {
      final socket =
          Platform.environment['HYDRA_CONTROL_SOCKET'] ??
          defaultIrvioSocketPath;
      return DesktopPlatform(
        connect: () => LinuxIrvioClient.connectTo(socketPath: socket),
        autostart: LinuxXdgAutostart(),
        notifications: const LinuxDesktopNotificationSink(),
        trayIcon: 'assets/irvio-globe.png',
      );
    }
    if (Platform.isWindows) {
      return const DesktopPlatform(
        connect: WindowsIrvioClient.connectTo,
        autostart: WindowsAutostart(),
        notifications: WindowsNotificationSink(),
        trayIcon: 'assets/irvio.ico',
        manualTrayMenu: true,
      );
    }
    throw UnsupportedError('IRVIO desktop requires Linux or Windows.');
  }
}
