// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:io';

abstract interface class IrvioAutostart {
  Future<bool> isEnabled();

  Future<void> setEnabled(bool enabled);
}

class NoopIrvioAutostart implements IrvioAutostart {
  const NoopIrvioAutostart();

  @override
  Future<bool> isEnabled() async => false;

  @override
  Future<void> setEnabled(bool enabled) async {}
}

class LinuxXdgAutostart implements IrvioAutostart {
  LinuxXdgAutostart({String? configHome, String? executablePath})
    : _configHome = configHome ?? _defaultConfigHome(),
      _executablePath = executablePath ?? Platform.resolvedExecutable;

  static const _managedMarker = 'X-IRVIO-Managed';
  static const _legacyManagedMarker = 'X-HYDRA-Managed';
  static const entryName = 'irvio.desktop';
  static const _legacyEntryName = 'hydra.desktop';

  final String _configHome;
  final String _executablePath;

  File get _entry => File('$_configHome/autostart/$entryName');
  File get _legacyEntry => File('$_configHome/autostart/$_legacyEntryName');

  @override
  Future<bool> isEnabled() async {
    for (final entry in [_entry, _legacyEntry]) {
      if (!await entry.exists()) continue;
      final contents = await entry.readAsString();
      if (_isOurEntry(entry, contents) &&
          _desktopValue(contents, 'Hidden') != 'true' &&
          _desktopValue(contents, 'X-GNOME-Autostart-enabled') != 'false') {
        return true;
      }
    }
    return false;
  }

  @override
  Future<void> setEnabled(bool enabled) async {
    final entry = _entry;
    if (!enabled) {
      for (final candidate in [entry, _legacyEntry]) {
        if (!await candidate.exists()) continue;
        final contents = await candidate.readAsString();
        if (_isOurEntry(candidate, contents)) await candidate.delete();
      }
      return;
    }

    if (await entry.exists()) {
      final contents = await entry.readAsString();
      if (!_isOurEntry(entry, contents)) {
        throw const FileSystemException(
          'Refusing to replace an unmanaged autostart entry.',
        );
      }
    }

    final directory = entry.parent;
    await directory.create(recursive: true);
    final temporary = File('${entry.path}.tmp-$pid');
    try {
      await temporary.writeAsString(_desktopEntry(), flush: true);
      await temporary.rename(entry.path);
      final legacy = _legacyEntry;
      if (await legacy.exists() &&
          _isOurEntry(legacy, await legacy.readAsString())) {
        await legacy.delete();
      }
    } finally {
      if (await temporary.exists()) await temporary.delete();
    }
  }

  String _desktopEntry() =>
      '[Desktop Entry]\n'
      'Type=Application\n'
      'Version=1.0\n'
      'Name=IRVIO\n'
      'Comment=IRVIO Linux interface\n'
      'Exec=${_quoteDesktopArgument(_executablePath)}\n'
      'Terminal=false\n'
      'X-GNOME-Autostart-enabled=true\n'
      '$_managedMarker=true\n';

  bool _isOurEntry(File entry, String contents) {
    if (entry.path.endsWith('/$_legacyEntryName')) {
      return _desktopValue(contents, _legacyManagedMarker) == 'true';
    }
    if (_desktopValue(contents, _managedMarker) == 'true') return true;
    // Older IRVIO deployments installed this entry without a managed marker.
    final executable = _desktopValue(contents, 'Exec');
    return _desktopValue(contents, 'Name') == 'IRVIO' &&
        _desktopValue(contents, 'Type') == 'Application' &&
        (executable == _executablePath ||
            executable == _quoteDesktopArgument(_executablePath) ||
            executable == '/opt/irvio/ui/irvio');
  }

  static String? _desktopValue(String contents, String key) {
    for (final line in contents.split('\n')) {
      if (line.startsWith('$key=')) {
        return line.substring(key.length + 1).trim();
      }
    }
    return null;
  }

  static String _defaultConfigHome() {
    final configured = Platform.environment['XDG_CONFIG_HOME'];
    if (configured != null && configured.isNotEmpty) return configured;
    final home = Platform.environment['HOME'];
    if (home == null || home.isEmpty) {
      throw const FileSystemException(
        'HOME or XDG_CONFIG_HOME is required for desktop autostart.',
      );
    }
    return '$home/.config';
  }
}

String _quoteDesktopArgument(String value) {
  final escaped = value
      .replaceAll('\\', '\\\\')
      .replaceAll('"', '\\"')
      .replaceAll('`', r'\`')
      .replaceAll(r'$', r'\$');
  return '"$escaped"';
}
