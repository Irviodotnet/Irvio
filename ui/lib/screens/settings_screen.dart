// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/material.dart';

import '../app/irvio_app_controller.dart';
import '../l10n/irvio_localizations.dart';
import '../widgets/panel.dart';

class SettingsScreen extends StatelessWidget {
  const SettingsScreen({super.key, required this.controller});

  final IrvioAppController controller;

  Future<void> _confirmDelete(BuildContext context) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text(context.l10n.text('deleteProfileQuestion')),
        content: Text(context.l10n.text('deleteProfileWarning')),
        actions: [
          TextButton(
            key: const Key('delete-profile-cancel'),
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: Text(context.l10n.text('cancel')),
          ),
          FilledButton(
            key: const Key('delete-profile-confirm'),
            onPressed: () => Navigator.of(dialogContext).pop(true),
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(context).colorScheme.error,
            ),
            child: Text(context.l10n.text('deleteProfile')),
          ),
        ],
      ),
    );
    if (confirmed == true) {
      await controller.deleteProfile();
    }
  }

  @override
  Widget build(BuildContext context) {
    return _Page(
      title: context.l10n.text('settings'),
      child: ListView(
        children: [
          Text(
            context.l10n.text('general'),
            style: Theme.of(context).textTheme.labelSmall,
          ),
          const SizedBox(height: 8),
          IrvioPanel(
            padding: const EdgeInsets.all(14),
            child: Column(
              children: [
                _SettingRow(
                  icon: Icons.login_rounded,
                  title: context.l10n.text('startAtLogin'),
                  subtitle: context.l10n.text('startAtLoginSubtitle'),
                  switchKey: const Key('start-at-login-toggle'),
                  value: controller.startAtLogin,
                  onChanged: controller.startAtLoginPending
                      ? null
                      : controller.setStartAtLogin,
                ),
                const Divider(height: 24),
                _SettingRow(
                  icon: Icons.notifications_none_rounded,
                  title: context.l10n.text('showNotifications'),
                  subtitle: context.l10n.text('showNotificationsSubtitle'),
                  switchKey: const Key('notifications-toggle'),
                  value: controller.notificationsEnabled,
                  onChanged: controller.setNotificationsEnabled,
                ),
              ],
            ),
          ),
          if (controller.startAtLoginError != null) ...[
            const SizedBox(height: 6),
            Text(
              controller.startAtLoginError!,
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          ],
          const SizedBox(height: 16),
          Text(
            context.l10n.text('appearance'),
            style: Theme.of(context).textTheme.labelSmall,
          ),
          const SizedBox(height: 8),
          IrvioPanel(
            padding: const EdgeInsets.all(14),
            child: Column(
              children: [
                _ChoiceRow(
                  icon: Icons.language_rounded,
                  label: context.l10n.text('language'),
                  child: DropdownButton<IrvioLanguage>(
                    key: const Key('language-selector'),
                    value: controller.language,
                    underline: const SizedBox.shrink(),
                    items: IrvioLanguage.values
                        .map(
                          (language) => DropdownMenuItem(
                            value: language,
                            child: Text(language.nativeName),
                          ),
                        )
                        .toList(growable: false),
                    onChanged: (language) {
                      if (language != null) {
                        controller.selectLanguage(language);
                      }
                    },
                  ),
                ),
                const Divider(height: 24),
                _ChoiceRow(
                  icon: Icons.monitor_outlined,
                  label: context.l10n.text('theme'),
                  child: DropdownButton<ThemeMode>(
                    key: const Key('theme-selector'),
                    value: controller.themeMode,
                    underline: const SizedBox.shrink(),
                    items: [
                      DropdownMenuItem(
                        value: ThemeMode.system,
                        child: Text(context.l10n.text('themeSystem')),
                      ),
                      DropdownMenuItem(
                        value: ThemeMode.dark,
                        child: Text(context.l10n.text('themeDark')),
                      ),
                      DropdownMenuItem(
                        value: ThemeMode.light,
                        child: Text(context.l10n.text('themeLight')),
                      ),
                    ],
                    onChanged: (mode) {
                      if (mode != null) controller.selectTheme(mode);
                    },
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(height: 16),
          Text(
            context.l10n.text('profile'),
            style: Theme.of(context).textTheme.labelSmall,
          ),
          const SizedBox(height: 8),
          IrvioPanel(
            padding: const EdgeInsets.all(14),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Icon(
                      Icons.delete_forever_outlined,
                      color: Theme.of(context).colorScheme.error,
                    ),
                    const SizedBox(width: 14),
                    Expanded(child: Text(context.l10n.text('deleteProfile'))),
                    const SizedBox(width: 12),
                    OutlinedButton(
                      key: const Key('delete-profile-button'),
                      onPressed:
                          controller.daemonAvailable &&
                              controller.profileDeleteAvailable &&
                              !controller.profileDeletePending
                          ? () => _confirmDelete(context)
                          : null,
                      style: OutlinedButton.styleFrom(
                        foregroundColor: Theme.of(context).colorScheme.error,
                        side: BorderSide(
                          color: Theme.of(context).colorScheme.error,
                        ),
                      ),
                      child: Text(
                        controller.profileDeletePending
                            ? context.l10n.text('restarting')
                            : context.l10n.text('delete'),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 3),
                Text(
                  controller.profileDeleteAvailable
                      ? context.l10n.text('deleteProfileSubtitle')
                      : context.l10n.text('deleteRequiresApi'),
                  style: Theme.of(context).textTheme.bodySmall,
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _Page extends StatelessWidget {
  const _Page({required this.title, required this.child});

  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(24, 4, 24, 24),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(title, style: Theme.of(context).textTheme.headlineSmall),
        const SizedBox(height: 16),
        Expanded(child: child),
      ],
    ),
  );
}

class _SettingRow extends StatelessWidget {
  const _SettingRow({
    required this.icon,
    required this.title,
    required this.subtitle,
    required this.switchKey,
    required this.value,
    required this.onChanged,
  });

  final IconData icon;
  final String title;
  final String subtitle;
  final Key switchKey;
  final bool value;
  final ValueChanged<bool>? onChanged;

  @override
  Widget build(BuildContext context) => Row(
    children: [
      Icon(icon, color: Theme.of(context).disabledColor),
      const SizedBox(width: 14),
      Expanded(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(title),
            const SizedBox(height: 3),
            Text(subtitle, style: Theme.of(context).textTheme.bodySmall),
          ],
        ),
      ),
      Switch(key: switchKey, value: value, onChanged: onChanged),
    ],
  );
}

class _ChoiceRow extends StatelessWidget {
  const _ChoiceRow({
    required this.icon,
    required this.label,
    required this.child,
  });

  final IconData icon;
  final String label;
  final Widget child;

  @override
  Widget build(BuildContext context) => Row(
    children: [
      Icon(icon, color: Theme.of(context).colorScheme.primary),
      const SizedBox(width: 14),
      Expanded(child: Text(label)),
      child,
    ],
  );
}
