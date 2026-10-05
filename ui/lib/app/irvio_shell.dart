// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/material.dart';

import '../l10n/irvio_localizations.dart';
import '../models/irvio_models.dart';
import '../screens/about_screen.dart';
import '../screens/diagnostics_screen.dart';
import '../screens/home_screen.dart';
import '../screens/logs_screen.dart';
import '../screens/settings_screen.dart';
import '../screens/share_irvio_screen.dart';
import '../theme/irvio_theme.dart';
import '../widgets/irvio_logo.dart';
import '../widgets/traffic_summary.dart';
import 'irvio_app_controller.dart';

class IrvioShell extends StatelessWidget {
  const IrvioShell({
    super.key,
    required this.controller,
    this.onDrag,
    this.onMinimize,
    this.onClose,
  });

  final IrvioAppController controller;
  final VoidCallback? onDrag;
  final VoidCallback? onMinimize;
  final VoidCallback? onClose;

  @override
  Widget build(BuildContext context) {
    final dark = Theme.of(context).brightness == Brightness.dark;
    return Scaffold(
      body: DecoratedBox(
        decoration: BoxDecoration(
          gradient: dark
              ? const RadialGradient(
                  center: Alignment(-.2, -.8),
                  radius: 1.25,
                  colors: [Color(0xFF152B43), Color(0xFF07121D)],
                )
              : const LinearGradient(
                  begin: Alignment.topLeft,
                  end: Alignment.bottomRight,
                  colors: [Color(0xFFF7FAFD), Color(0xFFE8F0F8)],
                ),
        ),
        child: SafeArea(
          child: Column(
            children: [
              _TitleBar(
                onDrag: onDrag,
                onMinimize: onMinimize,
                onClose: onClose,
              ),
              Expanded(
                child: LayoutBuilder(
                  builder: (context, constraints) => Row(
                    children: [
                      SizedBox(
                        width:
                            149 + (constraints.maxWidth - 522).clamp(0.0, 27.0),
                        child: _Sidebar(controller: controller),
                      ),
                      VerticalDivider(
                        width: 1,
                        thickness: 1,
                        color: Theme.of(context).dividerColor
                            .withValues(alpha: .45),
                      ),
                      Expanded(
                        child: AnimatedSwitcher(
                          duration: const Duration(milliseconds: 180),
                          child: switch (controller.page) {
                            IrvioPage.home => HomeScreen(
                              key: const ValueKey('home'),
                              controller: controller,
                            ),
                            IrvioPage.settings => SettingsScreen(
                              key: const ValueKey('settings'),
                              controller: controller,
                            ),
                            IrvioPage.diagnostics => DiagnosticsScreen(
                              key: const ValueKey('diagnostics'),
                              controller: controller,
                            ),
                            IrvioPage.logs => LogsScreen(
                              key: const ValueKey('logs'),
                              controller: controller,
                            ),
                            IrvioPage.share => ShareIrvioScreen(
                              key: const ValueKey('share'),
                              controller: controller,
                            ),
                            IrvioPage.about => AboutScreen(
                              key: const ValueKey('about'),
                              controller: controller,
                            ),
                          },
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _TitleBar extends StatelessWidget {
  const _TitleBar({this.onDrag, this.onMinimize, this.onClose});

  final VoidCallback? onDrag;
  final VoidCallback? onMinimize;
  final VoidCallback? onClose;

  @override
  Widget build(BuildContext context) => GestureDetector(
    behavior: HitTestBehavior.translucent,
    onPanStart: onDrag == null ? null : (_) => onDrag!(),
    child: SizedBox(
      height: 58,
      child: Row(
        children: [
          const Padding(
            padding: EdgeInsets.only(left: 18),
            child: IrvioLogo(size: 29),
          ),
          const Spacer(),
          IconButton(
            tooltip: context.l10n.text('minimize'),
            onPressed: onMinimize,
            icon: const Icon(Icons.remove_rounded, size: 20),
          ),
          IconButton(
            tooltip: context.l10n.text('closeInterface'),
            onPressed: onClose,
            icon: const Icon(Icons.close_rounded, size: 20),
          ),
          const SizedBox(width: 6),
        ],
      ),
    ),
  );
}

class _Sidebar extends StatelessWidget {
  const _Sidebar({required this.controller});

  final IrvioAppController controller;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(12, 18, 12, 12),
    child: LayoutBuilder(
      builder: (context, constraints) => SingleChildScrollView(
        child: ConstrainedBox(
          constraints: BoxConstraints(minHeight: constraints.maxHeight),
          child: IntrinsicHeight(
            child: Column(
              children: [
                _NavItem(
                  icon: Icons.home_outlined,
                  label: context.l10n.text('home'),
                  selected: controller.page == IrvioPage.home,
                  onTap: () => controller.selectPage(IrvioPage.home),
                ),
                _NavItem(
                  icon: Icons.settings_outlined,
                  label: context.l10n.text('settings'),
                  selected: controller.page == IrvioPage.settings,
                  onTap: () => controller.selectPage(IrvioPage.settings),
                ),
                _NavItem(
                  icon: Icons.bar_chart_rounded,
                  label: context.l10n.text('diagnostics'),
                  selected: controller.page == IrvioPage.diagnostics,
                  onTap: () => controller.selectPage(IrvioPage.diagnostics),
                ),
                _NavItem(
                  icon: Icons.notes_rounded,
                  label: context.l10n.text('logs'),
                  selected: controller.page == IrvioPage.logs,
                  onTap: () => controller.selectPage(IrvioPage.logs),
                ),
                _NavItem(
                  icon: Icons.qr_code_2_rounded,
                  label: context.l10n.text('shareIrvio'),
                  selected: controller.page == IrvioPage.share,
                  onTap: () => controller.selectPage(IrvioPage.share),
                ),
                _NavItem(
                  icon: Icons.info_outline_rounded,
                  label: context.l10n.text('about'),
                  selected: controller.page == IrvioPage.about,
                  onTap: () => controller.selectPage(IrvioPage.about),
                ),
                const Spacer(),
                TrafficSummary(stats: controller.trafficStats),
                const SizedBox(height: 6),
                Row(
                  children: [
                    Container(
                      width: 8,
                      height: 8,
                      decoration: BoxDecoration(
                        shape: BoxShape.circle,
                        color: _statusColor(controller),
                      ),
                    ),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        _shortStatus(controller),
                        overflow: TextOverflow.ellipsis,
                        style: Theme.of(context).textTheme.bodySmall,
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 9),
                Align(
                  alignment: Alignment.centerLeft,
                  child: Text(
                    'v1.1.0',
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    ),
  );
}

class _NavItem extends StatelessWidget {
  const _NavItem({
    required this.icon,
    required this.label,
    required this.selected,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(bottom: 7),
    child: Material(
      color: selected
          ? Theme.of(context).colorScheme.primary.withValues(alpha: .23)
          : Colors.transparent,
      borderRadius: BorderRadius.circular(9),
      child: InkWell(
        key: Key('nav-${label.toLowerCase()}'),
        onTap: onTap,
        borderRadius: BorderRadius.circular(9),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 12),
          child: Row(
            children: [
              Icon(
                icon,
                size: 21,
                color: selected
                    ? Theme.of(context).colorScheme.primary
                    : Theme.of(context).textTheme.bodySmall?.color,
              ),
              const SizedBox(width: 10),
              Expanded(
                child: Text(
                  label,
                  style: TextStyle(
                    fontSize: 12,
                    color: selected
                        ? Theme.of(context).colorScheme.primary
                        : null,
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    ),
  );
}

Color _statusColor(IrvioAppController controller) {
  if (!controller.daemonAvailable) return Colors.grey;
  if (controller.disconnectPending) return irvioAmber;
  return switch (controller.status.state) {
    IrvioConnectionState.connected => irvioGreen,
    IrvioConnectionState.connecting ||
    IrvioConnectionState.recovering ||
    IrvioConnectionState.disconnecting => irvioAmber,
    IrvioConnectionState.noNetwork ||
    IrvioConnectionState.noExit ||
    IrvioConnectionState.error => irvioRed,
    IrvioConnectionState.disconnected => Colors.grey,
  };
}

String _shortStatus(IrvioAppController controller) {
  if (!controller.daemonAvailable) {
    return controller.strings.text('daemonOffline');
  }
  if (controller.disconnectPending) {
    return controller.strings.connectionState(
      IrvioConnectionState.disconnecting,
    );
  }
  return controller.strings.connectionState(controller.status.state);
}
