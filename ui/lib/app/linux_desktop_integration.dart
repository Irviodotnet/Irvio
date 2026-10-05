// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:tray_manager/tray_manager.dart';
import 'package:window_manager/window_manager.dart';

import '../models/irvio_models.dart';
import 'irvio_app_controller.dart';

class LinuxDesktopIntegration with TrayListener, WindowListener {
  LinuxDesktopIntegration(this.controller);

  final IrvioAppController controller;
  final navigatorKey = GlobalKey<NavigatorState>();
  bool _trayReady = false;
  bool _quitting = false;
  bool _shutdownRequested = false;
  bool _menuUpdating = false;
  String? _appliedMenuSignature;

  Future<void> initialize() async {
    windowManager.addListener(this);
    await windowManager.setPreventClose(true);
    trayManager.addListener(this);
    controller.addListener(_controllerChanged);
    try {
      await trayManager.setIcon('assets/irvio-globe.png');
      _trayReady = true;
      await _updateMenu();
    } on Object {
      // A desktop without an AppIndicator host still gets a usable window.
      _trayReady = false;
    }
  }

  void _controllerChanged() {
    if (_trayReady &&
        !_menuUpdating &&
        trayMenuSignature(controller) != _appliedMenuSignature) {
      unawaited(_updateMenu());
    }
  }

  Future<void> _updateMenu() async {
    if (_menuUpdating || !_trayReady) return;
    _menuUpdating = true;
    try {
      while (_trayReady && !_quitting) {
        final signature = trayMenuSignature(controller);
        if (signature == _appliedMenuSignature) break;
        await _applyMenu();
        _appliedMenuSignature = signature;
      }
    } on Object {
      // A missing AppIndicator host must not interrupt the UI event loop.
      _trayReady = false;
    } finally {
      _menuUpdating = false;
    }
  }

  Future<void> _applyMenu() async {
    final status = controller.status;
    final strings = controller.strings;
    final state = controller.disconnectPending
        ? IrvioConnectionState.disconnecting
        : status.state;
    final countryCode = status.countryCode.isEmpty
        ? controller.selectedCountry.code
        : status.countryCode;
    final country = strings.countryName(countryCode);
    final connected = state == IrvioConnectionState.connected;
    final canDisconnect = switch (state) {
      IrvioConnectionState.disconnected => false,
      _ => controller.daemonAvailable && !controller.commandPending,
    };
    final label = connected
        ? '${strings.connectionState(state)} — $country'
        : strings.connectionState(state);
    await trayManager.setContextMenu(
      Menu(
        items: [
          MenuItem(key: 'status', label: 'IRVIO  ·  $label', disabled: true),
          MenuItem.separator(),
          MenuItem(key: 'open', label: strings.text('openIrvio')),
          MenuItem(
            key: 'disconnect',
            label: strings.text('disconnectVpn'),
            disabled: !canDisconnect,
          ),
          MenuItem.separator(),
          MenuItem(key: 'settings', label: strings.text('settings')),
          MenuItem(key: 'about', label: strings.text('about')),
          MenuItem.separator(),
          MenuItem(key: 'shutdown', label: strings.text('shutdownIrvio')),
        ],
      ),
    );
  }

  Future<void> show({IrvioPage? page}) async {
    if (page != null) controller.selectPage(page);
    await windowManager.show();
    await windowManager.focus();
  }

  Future<void> requestClose() => windowManager.close();

  Future<void> minimize() => windowManager.minimize();

  Future<void> startDragging() => windowManager.startDragging();

  @override
  void onTrayIconMouseDown() {
    unawaited(show());
  }

  @override
  void onTrayMenuItemClick(MenuItem menuItem) {
    switch (menuItem.key) {
      case 'open':
        unawaited(show(page: IrvioPage.home));
      case 'disconnect':
        unawaited(controller.disconnect());
      case 'settings':
        unawaited(show(page: IrvioPage.settings));
      case 'about':
        unawaited(show(page: IrvioPage.about));
      case 'shutdown':
        unawaited(requestShutdown());
    }
  }

  @override
  void onWindowClose() {
    if (_quitting || _shutdownRequested) return;
    unawaited(requestShutdown());
  }

  Future<void> requestShutdown() async {
    if (_quitting || _shutdownRequested) return;
    _shutdownRequested = true;
    try {
      await show();
      final context = navigatorKey.currentContext;
      if (context == null || !context.mounted) return;
      var skipNextTime = false;
      if (!controller.skipShutdownConfirmation) {
        final confirmed = await showDialog<bool>(
          context: context,
          barrierDismissible: false,
          builder: (dialogContext) => StatefulBuilder(
            builder: (dialogContext, setDialogState) => AlertDialog(
              title: Text(controller.strings.text('shutdownIrvio')),
              content: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(controller.strings.text('shutdownWarning')),
                  CheckboxListTile(
                    contentPadding: EdgeInsets.zero,
                    title: Text(controller.strings.text('dontShowAgain')),
                    value: skipNextTime,
                    onChanged: (value) =>
                        setDialogState(() => skipNextTime = value ?? false),
                  ),
                ],
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(dialogContext, false),
                  child: Text(controller.strings.text('cancel')),
                ),
                FilledButton(
                  onPressed: () => Navigator.pop(dialogContext, true),
                  child: Text(controller.strings.text('shutdownIrvio')),
                ),
              ],
            ),
          ),
        );
        if (confirmed != true) return;
        if (skipNextTime) await controller.setSkipShutdownConfirmation(true);
      }
      if (await controller.shutdownIrvio()) {
        await quitUi();
      }
    } finally {
      _shutdownRequested = false;
    }
  }

  Future<void> quitUi() async {
    if (_quitting) return;
    _quitting = true;
    controller.removeListener(_controllerChanged);
    windowManager.removeListener(this);
    trayManager.removeListener(this);
    if (_trayReady) await trayManager.destroy();
    controller.dispose();
    // After daemon shutdown acknowledgement, exit the UI to avoid Wayland EGL teardown crashes.
    exit(0);
  }

  void dispose() {
    controller.removeListener(_controllerChanged);
    windowManager.removeListener(this);
    trayManager.removeListener(this);
  }
}

String trayMenuSignature(IrvioAppController controller) {
  final state = controller.disconnectPending
      ? IrvioConnectionState.disconnecting
      : controller.status.state;
  final countryCode = controller.status.countryCode.isEmpty
      ? controller.selectedCountry.code
      : controller.status.countryCode;
  return '${controller.language.code}|${state.name}|$countryCode|'
      '${controller.daemonAvailable}|${controller.commandPending}';
}
