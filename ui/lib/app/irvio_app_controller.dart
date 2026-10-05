// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../irvio/irvio_client.dart';
import '../l10n/irvio_localizations.dart';
import '../models/country.dart';
import '../models/irvio_models.dart';
import 'linux_autostart.dart';
import 'linux_notifications.dart';

enum IrvioPage { home, settings, diagnostics, logs, share, about }

class IrvioAppController extends ChangeNotifier {
  IrvioAppController(
    this._clientFactory, {
    IrvioUiPreferences? preferences,
    IrvioAutostart? autostart,
    IrvioNotificationSink? notifications,
    this.reconnectDelays = const [
      Duration(seconds: 1),
      Duration(seconds: 2),
      Duration(seconds: 5),
      Duration(seconds: 10),
    ],
    this.countryRetryDelays = const [
      Duration(seconds: 2),
      Duration(seconds: 5),
      Duration(seconds: 10),
    ],
    this.countryRetrySteadyDelay = const Duration(seconds: 10),
    this.countryRefreshInterval = const Duration(seconds: 10),
    this.bootstrapRefreshInterval = const Duration(milliseconds: 500),
    this.bootstrapRefreshSteadyInterval = const Duration(seconds: 5),
  }) : _preferences = preferences ?? const SharedIrvioUiPreferences(),
       _autostart = autostart ?? const NoopIrvioAutostart(),
       _notificationSink = notifications ?? const NoopIrvioNotificationSink();

  final IrvioClientFactory _clientFactory;
  final IrvioUiPreferences _preferences;
  final IrvioAutostart _autostart;
  final IrvioNotificationSink _notificationSink;
  final List<Duration> reconnectDelays;
  final List<Duration> countryRetryDelays;
  final Duration countryRetrySteadyDelay;
  final Duration countryRefreshInterval;
  final Duration bootstrapRefreshInterval;
  final Duration bootstrapRefreshSteadyInterval;

  IrvioClient? _client;
  IrvioClient? _logClient;
  StreamSubscription<IrvioStatusEvent>? _eventSubscription;
  StreamSubscription<Object>? _errorSubscription;
  StreamSubscription<IrvioLogEvent>? _logEventSubscription;
  StreamSubscription<Object>? _logErrorSubscription;
  Timer? _reconnectTimer;
  Timer? _trafficTimer;
  bool _trafficPending = false;
  final trafficStats = ValueNotifier<IrvioTrafficStats?>(null);
  Timer? _logReconnectTimer;
  Timer? _bootstrapRefreshTimer;
  Timer? _countryRetryTimer;
  int _reconnectAttempt = 0;
  int _bootstrapRefreshes = 0;
  int _countryRetryIndex = 0;
  bool _started = false;
  bool _closed = false;
  bool _syncing = false;
  bool _logSyncing = false;
  bool _logConnectRequested = false;
  bool _commandPending = false;
  bool _connectPending = false;
  bool _disconnectPending = false;
  bool _bootstrapPending = false;
  bool _profileDeletePending = false;
  bool _countriesPending = false;
  bool _countriesRefreshQueued = false;
  bool _countriesRefreshForceQueued = false;
  bool _countryRetryActive = false;
  bool _logsPaused = false;
  bool _statusInitialized = false;
  bool _shutdownInProgress = false;
  int _lastLogSequence = 0;
  List<int> _logIncarnation = const [];
  String? _preferredCountryCode;

  IrvioStatus status = const IrvioStatus.disconnected();
  IrvioContribution contribution = const IrvioContribution(
    supported: false,
    enabled: false,
  );
  IrvioHello? hello;
  IrvioBootstrapState? bootstrapState;
  IrvioGeneratedBootstrapBundle? sharedBootstrapBundle;
  final List<IrvioLogRecord> logs = [];
  IrvioCountry selectedCountry = irvioCountries.first;
  List<IrvioCountry> availableCountries = const [];
  IrvioAvailableCountriesStatus availableCountriesStatus =
      IrvioAvailableCountriesStatus.unavailable;
  IrvioPage page = IrvioPage.home;
  ThemeMode themeMode = ThemeMode.dark;
  IrvioLanguage language = IrvioLanguage.english;
  bool startAtLogin = false;
  bool skipShutdownConfirmation = false;
  bool startAtLoginPending = false;
  String? startAtLoginError;
  bool notificationsEnabled = false;
  bool daemonAvailable = false;
  bool daemonConnecting = true;
  String? apiError;
  String? logsError;
  String? bootstrapError;
  String? availableCountriesError;
  bool logsAvailable = false;
  bool bootstrapAvailable = false;
  bool availableCountriesLoading = true;

  IrvioLocalizations get strings => IrvioLocalizations.forLanguage(language);

  bool get commandPending => _commandPending;
  bool get connectPending => _connectPending;
  bool get disconnectPending => _disconnectPending;
  bool get bootstrapPending => _bootstrapPending;
  bool get profileDeletePending => _profileDeletePending;
  bool get profileDeleteAvailable => hello?.supportsV12 == true;
  bool get logsPaused => _logsPaused;
  bool get availableCountriesSupported => hello?.supportsV13 == true;

  bool get connectionIntentActive {
    if (status.state == IrvioConnectionState.disconnecting ||
        status.state == IrvioConnectionState.disconnected) {
      return false;
    }
    if (status.state == IrvioConnectionState.noExit ||
        status.state == IrvioConnectionState.noNetwork ||
        status.state == IrvioConnectionState.error) {
      return status.countryCode.isNotEmpty;
    }
    return true;
  }

  Future<void> start() async {
    if (_started || _closed) return;
    _started = true;
    final theme = await _preferences.getString('theme');
    themeMode = switch (theme) {
      'system' => ThemeMode.system,
      'light' => ThemeMode.light,
      _ => ThemeMode.dark,
    };
    language = IrvioLanguage.fromCode(await _preferences.getString('language'));
    notificationsEnabled =
        await _preferences.getString('notifications') == 'true';
    skipShutdownConfirmation =
        await _preferences.getString('skipShutdownConfirmation') == 'true';
    try {
      startAtLogin = await _autostart.isEnabled();
    } on Object {
      startAtLoginError = strings.text('autostartUpdateFailed');
    }
    final country = await _preferences.getString('country');
    if (country != null) _preferredCountryCode = country.toUpperCase();
    notifyListeners();
    await reconnectNow();
  }

  Future<void> reconnectNow() async {
    if (_closed || _syncing) return;
    _syncing = true;
    _reconnectTimer?.cancel();
    daemonConnecting = true;
    notifyListeners();
    await _disposeClient();
    try {
      final client = await _clientFactory();
      if (_closed) {
        await client.close();
        return;
      }
      _client = client;
      hello = client.hello;
      _eventSubscription = client.statusEvents.listen(
        (event) => _applyStatus(event.status),
        onError: _handleConnectionFailure,
        onDone: () => _handleConnectionFailure(
          IrvioApiException(
            IrvioErrorCode.unavailable,
            strings.text('eventStreamClosed'),
          ),
        ),
      );
      _errorSubscription = client.connectionErrors.listen(
        _handleConnectionFailure,
      );

      // GET_STATUS is authoritative and must precede event subscription.
      _applyStatus(await client.getStatus());
      try {
        contribution = await client.getContribution();
      } on IrvioApiException catch (error) {
        if (error.code != IrvioErrorCode.unsupported) rethrow;
        contribution = const IrvioContribution(
          supported: false,
          enabled: false,
        );
      }
      if (client.hello.supportsV11) {
        try {
          bootstrapState = await client.getBootstrapState();
          bootstrapAvailable = true;
          bootstrapError = null;
        } on IrvioApiException catch (error) {
          if (error.code != IrvioErrorCode.unsupported) rethrow;
          bootstrapAvailable = false;
          bootstrapState = null;
        }
      } else {
        bootstrapAvailable = false;
        bootstrapState = null;
      }
      await client.subscribeEvents();
      daemonAvailable = true;
      if (client.hello.supportsTrafficStats) {
        _trafficTimer = Timer.periodic(
          const Duration(seconds: 2),
          (_) => unawaited(_refreshTraffic()),
        );
        unawaited(_refreshTraffic());
      }
      daemonConnecting = false;
      _profileDeletePending = false;
      apiError = null;
      _reconnectAttempt = 0;
      if (client.hello.supportsV13) {
        if ((bootstrapState?.configuredCount ?? 0) > 0) {
          _startCountryRetryWindow();
        }
        unawaited(refreshAvailableCountries());
      } else {
        availableCountries = const [];
        availableCountriesStatus = IrvioAvailableCountriesStatus.unavailable;
        availableCountriesLoading = false;
        availableCountriesError = strings.text('locationsUnavailableVersion');
      }
      if (client.hello.supportsV11 && !_logsPaused) {
        unawaited(_connectLogs());
      } else {
        logsAvailable = false;
      }
    } on Object catch (error) {
      daemonAvailable = false;
      daemonConnecting = false;
      apiError = _safeMessage(error);
      _scheduleReconnect();
    } finally {
      _syncing = false;
      if (!_closed) notifyListeners();
    }
  }

  Future<void> connect() => _runCommand(() async {
    final code = selectedCountry.code.toUpperCase();
    if (!RegExp(r'^[A-Z]{2}$').hasMatch(code) ||
        !availableCountries.any((country) => country.code == code)) {
      throw IrvioApiException(
        IrvioErrorCode.invalidArgument,
        strings.text('chooseValidCountry'),
      );
    }
    await _requireClient().connect(code);
  }, connecting: true);

  Future<void> disconnect() =>
      _runCommand(() => _requireClient().disconnect(), disconnecting: true);

  Future<void> setSkipShutdownConfirmation(bool value) async {
    skipShutdownConfirmation = value;
    await _preferences.setString('skipShutdownConfirmation', value.toString());
    notifyListeners();
  }

  Future<bool> shutdownIrvio() async {
    if (_closed || _shutdownInProgress || _commandPending) return false;
    final client = _client;
    if (client == null || !daemonAvailable || !client.hello.supportsV14) {
      apiError = strings.text('shutdownUnavailable');
      notifyListeners();
      return false;
    }
    _shutdownInProgress = true;
    _trafficTimer?.cancel();
    _commandPending = true;
    _disconnectPending = true;
    apiError = null;
    notifyListeners();
    try {
      await client.shutdown();
      _closed = true;
      _reconnectTimer?.cancel();
      _logReconnectTimer?.cancel();
      _bootstrapRefreshTimer?.cancel();
      await _disposeClient();
      return true;
    } on Object catch (error) {
      apiError = _safeMessage(error);
      return false;
    } finally {
      _shutdownInProgress = false;
      if (!_closed && (_client?.hello.supportsTrafficStats ?? false)) {
        _trafficTimer = Timer.periodic(
          const Duration(seconds: 2),
          (_) => unawaited(_refreshTraffic()),
        );
      }
      _commandPending = false;
      _disconnectPending = false;
      notifyListeners();
    }
  }

  Future<void> setContribution(bool enabled) => _runCommand(() async {
    if (!contribution.supported) {
      throw const IrvioApiException(
        IrvioErrorCode.unsupported,
        'Contribution controls are not available in Local API v1.0.',
      );
    }
    await _requireClient().setContribution(enabled);
  });

  Future<void> importBootstrapBundle(String code) async {
    if (_bootstrapPending) return;
    _bootstrapPending = true;
    bootstrapError = null;
    notifyListeners();
    try {
      bootstrapState = await _requireClient().importBootstrapBundle(code);
      bootstrapAvailable = true;
      _bootstrapRefreshes = 0;
      if ((bootstrapState?.configuredCount ?? 0) > 0 &&
          availableCountriesSupported) {
        _startCountryRetryWindow();
        unawaited(refreshAvailableCountries(refresh: true));
      }
      _scheduleBootstrapRefresh();
    } on Object catch (error) {
      bootstrapError = _safeMessage(error);
    } finally {
      _bootstrapPending = false;
      if (!_closed) notifyListeners();
    }
  }

  Future<void> refreshBootstrapState() async {
    if (!bootstrapAvailable || _closed || !daemonAvailable) return;
    final previousConfiguredCount = bootstrapState?.configuredCount ?? 0;
    try {
      bootstrapState = await _requireClient().getBootstrapState();
      bootstrapError = null;
      if ((bootstrapState?.configuredCount ?? 0) > 0 &&
          previousConfiguredCount == 0 &&
          availableCountriesSupported) {
        _startCountryRetryWindow();
        unawaited(refreshAvailableCountries(refresh: true));
      }
    } on Object catch (error) {
      bootstrapError = _safeMessage(error);
    }
    if (!_closed) notifyListeners();
  }

  Future<void> generateBootstrapBundle() async {
    if (_bootstrapPending) return;
    _bootstrapPending = true;
    bootstrapError = null;
    notifyListeners();
    try {
      sharedBootstrapBundle = await _requireClient().generateBootstrapBundle();
    } on Object catch (error) {
      bootstrapError = _safeMessage(error);
    } finally {
      _bootstrapPending = false;
      if (!_closed) notifyListeners();
    }
  }

  Future<void> deleteProfile() async {
    if (_profileDeletePending) return;
    if (!profileDeleteAvailable) {
      apiError = strings.text('deleteRequiresApi');
      notifyListeners();
      return;
    }
    _profileDeletePending = true;
    _commandPending = true;
    apiError = null;
    notifyListeners();
    try {
      await _requireClient().deleteProfile();
      daemonAvailable = false;
      daemonConnecting = true;
      bootstrapState = null;
      sharedBootstrapBundle = null;
      availableCountries = const [];
      availableCountriesStatus = IrvioAvailableCountriesStatus.unavailable;
      availableCountriesLoading = true;
      _reconnectAttempt = 0;
      await _disposeClient();
      _scheduleReconnect();
    } on Object catch (error) {
      _profileDeletePending = false;
      apiError = _safeMessage(error);
    } finally {
      _commandPending = false;
      if (!_closed) notifyListeners();
    }
  }

  void setLogsPaused(bool paused) {
    if (_logsPaused == paused) return;
    _logsPaused = paused;
    if (paused) {
      _logReconnectTimer?.cancel();
      unawaited(_disposeLogClient());
    } else if (hello?.supportsV11 == true && daemonAvailable) {
      unawaited(_connectLogs());
    }
    notifyListeners();
  }

  void clearLogView() {
    logs.clear();
    logsError = null;
    notifyListeners();
  }

  Future<void> _runCommand(
    Future<void> Function() operation, {
    bool connecting = false,
    bool disconnecting = false,
  }) async {
    if (_commandPending) return;
    _commandPending = true;
    _connectPending = connecting;
    _disconnectPending = disconnecting;
    apiError = null;
    notifyListeners();
    try {
      await operation();
    } on Object catch (error) {
      apiError = _safeMessage(error);
    } finally {
      _commandPending = false;
      _connectPending = false;
      _disconnectPending = false;
      if (!_closed) notifyListeners();
    }
  }

  Future<void> refreshAvailableCountries({bool refresh = false}) async {
    if (_closed) return;
    if (_countriesPending) {
      _countriesRefreshQueued = true;
      _countriesRefreshForceQueued |= refresh;
      return;
    }
    final client = _client;
    if (!daemonAvailable || client == null || !client.hello.supportsV13) {
      availableCountries = const [];
      availableCountriesStatus = IrvioAvailableCountriesStatus.unavailable;
      availableCountriesLoading = false;
      availableCountriesError = strings.text('locationsUnavailable');
      notifyListeners();
      return;
    }
    _countryRetryTimer?.cancel();
    _countryRetryTimer = null;
    _countriesPending = true;
    availableCountriesLoading = availableCountries.isEmpty;
    availableCountriesError = null;
    notifyListeners();
    try {
      final result = await client.getAvailableCountries(refresh: refresh);
      if (_client != client || _closed) return;
      final seen = <String>{};
      final next = <IrvioCountry>[];
      for (final value in result.countryCodes) {
        final code = value.toUpperCase();
        if (RegExp(r'^[A-Z]{2}$').hasMatch(code) && seen.add(code)) {
          next.add(countryForCode(code));
        }
      }
      next.sort((left, right) => left.name.compareTo(right.name));
      availableCountries = List<IrvioCountry>.unmodifiable(next);
      availableCountriesStatus = result.status;
      availableCountriesLoading = false;
      if (!connectionIntentActive && next.isNotEmpty) {
        final preferred = _preferredCountryCode ?? selectedCountry.code;
        selectedCountry = next.firstWhere(
          (country) => country.code == preferred,
          orElse: () => next.first,
        );
      }
    } on Object catch (error) {
      if (_client != client || _closed) return;
      availableCountries = const [];
      availableCountriesStatus = IrvioAvailableCountriesStatus.unavailable;
      availableCountriesLoading = false;
      availableCountriesError = _safeMessage(error);
    } finally {
      _countriesPending = false;
      if (!_closed) {
        notifyListeners();
        if (_countriesRefreshQueued) {
          final forceRefresh = _countriesRefreshForceQueued;
          _countriesRefreshQueued = false;
          _countriesRefreshForceQueued = false;
          unawaited(refreshAvailableCountries(refresh: forceRefresh));
        } else {
          _scheduleCountryRetry();
        }
      }
    }
  }

  IrvioClient _requireClient() {
    final client = _client;
    if (!daemonAvailable || client == null) {
      throw IrvioApiException(
        IrvioErrorCode.unavailable,
        strings.text('irviodAutoReconnect'),
      );
    }
    return client;
  }

  void selectCountry(IrvioCountry country) {
    if (!availableCountries.any((value) => value.code == country.code)) return;
    selectedCountry = country;
    _preferredCountryCode = country.code;
    unawaited(_preferences.setString('country', country.code));
    notifyListeners();
  }

  void selectPage(IrvioPage nextPage) {
    page = nextPage;
    notifyListeners();
  }

  void selectTheme(ThemeMode mode) {
    themeMode = mode;
    final value = switch (mode) {
      ThemeMode.system => 'system',
      ThemeMode.light => 'light',
      ThemeMode.dark => 'dark',
    };
    unawaited(_preferences.setString('theme', value));
    notifyListeners();
  }

  void selectLanguage(IrvioLanguage nextLanguage) {
    language = nextLanguage;
    unawaited(_preferences.setString('language', nextLanguage.code));
    notifyListeners();
  }

  void setNotificationsEnabled(bool enabled) {
    notificationsEnabled = enabled;
    unawaited(_preferences.setString('notifications', enabled.toString()));
    notifyListeners();
  }

  Future<void> setStartAtLogin(bool enabled) async {
    if (startAtLoginPending) return;
    startAtLoginPending = true;
    startAtLoginError = null;
    notifyListeners();
    try {
      await _autostart.setEnabled(enabled);
      startAtLogin = await _autostart.isEnabled();
    } on Object {
      startAtLoginError = strings.text('autostartUpdateFailed');
    } finally {
      startAtLoginPending = false;
      if (!_closed) notifyListeners();
    }
  }

  void _applyStatus(IrvioStatus nextStatus) {
    final previousStatus = status;
    status = nextStatus;
    final authoritativeCountry = nextStatus.countryCode.isNotEmpty
        ? nextStatus.countryCode
        : nextStatus.exitCountryCode;
    if (authoritativeCountry.isNotEmpty) {
      selectedCountry = countryForCode(authoritativeCountry);
    }
    if (nextStatus.state == IrvioConnectionState.noExit &&
        previousStatus.state != IrvioConnectionState.noExit &&
        availableCountriesSupported) {
      unawaited(refreshAvailableCountries(refresh: true));
    }
    if (_statusInitialized) {
      _notifyMeaningfulTransition(previousStatus, nextStatus);
    }
    _statusInitialized = true;
    if (!_closed) notifyListeners();
  }

  void _notifyMeaningfulTransition(IrvioStatus previous, IrvioStatus next) {
    if (!notificationsEnabled || previous.state == next.state) return;
    final countryCode = next.exitCountryCode.isNotEmpty
        ? next.exitCountryCode
        : next.countryCode.isNotEmpty
        ? next.countryCode
        : selectedCountry.code;
    final country = strings.countryName(countryCode);
    final message = switch (next.state) {
      IrvioConnectionState.connected
          when previous.state == IrvioConnectionState.recovering =>
        strings.text('notificationNewPeer'),
      IrvioConnectionState.connected => strings.text('notificationConnected', {
        'country': country,
      }),
      IrvioConnectionState.recovering => strings.text('notificationRecovering'),
      IrvioConnectionState.noExit => strings.text('notificationNoExit', {
        'country': country,
      }),
      IrvioConnectionState.noNetwork => strings.text('notificationNoNetwork'),
      _ => null,
    };
    if (message != null) {
      unawaited(_notificationSink.show(title: 'IRVIO', body: message));
    }
  }

  void _handleConnectionFailure(Object error) {
    if (_closed) return;
    daemonAvailable = false;
    daemonConnecting = _profileDeletePending;
    apiError = _profileDeletePending ? null : _safeMessage(error);
    availableCountriesLoading = false;
    availableCountries = const [];
    availableCountriesStatus = IrvioAvailableCountriesStatus.unavailable;
    availableCountriesError = strings.text('locationsUnavailableOffline');
    unawaited(_disposeClient());
    _scheduleReconnect();
    notifyListeners();
  }

  Future<void> _connectLogs() async {
    if (_closed ||
        _logsPaused ||
        hello?.supportsV11 != true ||
        !daemonAvailable) {
      return;
    }
    if (_logSyncing) {
      _logConnectRequested = true;
      return;
    }
    _logSyncing = true;
    _logConnectRequested = false;
    final statusClient = _client;
    _logReconnectTimer?.cancel();
    await _disposeLogClient();
    try {
      final client = await _clientFactory();
      if (!_isCurrentLogContext(statusClient) || !client.hello.supportsV11) {
        await client.close();
        logsAvailable = false;
        return;
      }
      _logClient = client;
      final tail = await client.getRecentLogs();
      if (!_isCurrentLogContext(statusClient) || _logClient != client) {
        await _disposeLogClientIfCurrent(client);
        return;
      }
      logsError = null;
      _applyLogTail(tail, replace: true);
      _logEventSubscription = client.logEvents.listen(
        (event) {
          if (_logClient == client) _applyLogEvent(event);
        },
        onError: (Object error) {
          if (_logClient == client) _handleLogFailure(error);
        },
        onDone: () {
          if (_logClient == client) {
            _handleLogFailure(
              IrvioApiException(
                IrvioErrorCode.unavailable,
                strings.text('logStreamClosed'),
              ),
            );
          }
        },
      );
      _logErrorSubscription = client.connectionErrors.listen((error) {
        if (_logClient == client) _handleLogFailure(error);
      });
      final missed = await client.subscribeLogs(
        daemonIncarnation: tail.daemonIncarnation,
        afterSequence: tail.lastSequence,
      );
      if (!_isCurrentLogContext(statusClient) || _logClient != client) {
        await _disposeLogClientIfCurrent(client);
        return;
      }
      _applyLogTail(missed);
      logsAvailable = true;
    } on Object catch (error) {
      if (!_isCurrentLogContext(statusClient)) return;
      logsAvailable = false;
      logsError = _safeMessage(error);
      await _disposeLogClient();
      _scheduleLogReconnect();
    } finally {
      _logSyncing = false;
      if (!_closed) notifyListeners();
      final reconnect = _logConnectRequested;
      _logConnectRequested = false;
      if (reconnect && !_closed && !_logsPaused && daemonAvailable) {
        unawaited(_connectLogs());
      }
    }
  }

  bool _isCurrentLogContext(IrvioClient? statusClient) =>
      !_closed &&
      !_logsPaused &&
      daemonAvailable &&
      statusClient != null &&
      _client == statusClient;

  Future<void> _disposeLogClientIfCurrent(IrvioClient client) async {
    if (_logClient == client) {
      await _disposeLogClient();
    } else {
      await client.close();
    }
  }

  void _applyLogTail(IrvioLogTail tail, {bool replace = false}) {
    final incarnationChanged =
        _logIncarnation.isNotEmpty &&
        !_sameBytes(_logIncarnation, tail.daemonIncarnation);
    if (replace || incarnationChanged) logs.clear();
    if (tail.gap || incarnationChanged) {
      logsError = strings.text('logGap');
    }
    _logIncarnation = List<int>.unmodifiable(tail.daemonIncarnation);
    for (final record in tail.records) {
      _appendLog(record);
    }
    if (tail.lastSequence > _lastLogSequence || incarnationChanged) {
      _lastLogSequence = tail.lastSequence;
    }
  }

  void _applyLogEvent(IrvioLogEvent event) {
    if (_logsPaused) return;
    if (!_sameBytes(_logIncarnation, event.daemonIncarnation)) {
      logs.clear();
      logsError = strings.text('logRestarted');
      _logIncarnation = List<int>.unmodifiable(event.daemonIncarnation);
      _lastLogSequence = 0;
    }
    if (event.record.sequence > _lastLogSequence + 1 && _lastLogSequence != 0) {
      logsError = strings.text('logMissed');
    }
    if (event.record.sequence > _lastLogSequence) {
      _appendLog(event.record);
      _lastLogSequence = event.record.sequence;
      if (!_closed && page == IrvioPage.logs) notifyListeners();
    }
  }

  void _appendLog(IrvioLogRecord record) {
    if (logs.any((current) => current.sequence == record.sequence)) return;
    logs.add(record);
    const maxViewRecords = 512;
    if (logs.length > maxViewRecords) {
      logs.removeRange(0, logs.length - maxViewRecords);
      logsError = strings.text('logBounded');
    }
  }

  void _handleLogFailure(Object error) {
    if (_closed || _logsPaused) return;
    logsAvailable = false;
    logsError = _safeMessage(error);
    unawaited(_disposeLogClient());
    _scheduleLogReconnect();
    notifyListeners();
  }

  void _scheduleReconnect() {
    if (_closed || _reconnectTimer?.isActive == true) return;
    final index = _reconnectAttempt.clamp(0, reconnectDelays.length - 1);
    final delay = reconnectDelays[index];
    _reconnectAttempt++;
    _reconnectTimer = Timer(delay, reconnectNow);
  }

  void _scheduleLogReconnect() {
    if (_closed ||
        _logsPaused ||
        _logReconnectTimer?.isActive == true ||
        !daemonAvailable) {
      return;
    }
    _logReconnectTimer = Timer(const Duration(seconds: 2), _connectLogs);
  }

  void _startCountryRetryWindow() {
    _countryRetryTimer?.cancel();
    _countryRetryTimer = null;
    _countryRetryIndex = 0;
    _countryRetryActive = true;
  }

  void _scheduleCountryRetry() {
    if (_closed ||
        !daemonAvailable ||
        (bootstrapState?.configuredCount ?? 0) == 0) {
      return;
    }
    late final Duration delay;
    if (availableCountriesStatus == IrvioAvailableCountriesStatus.unavailable) {
      if (!_countryRetryActive) {
        _countryRetryIndex = 0;
        _countryRetryActive = true;
      }
      delay = _countryRetryIndex < countryRetryDelays.length
          ? countryRetryDelays[_countryRetryIndex++]
          : countryRetrySteadyDelay;
    } else {
      _countryRetryActive = false;
      _countryRetryIndex = 0;
      delay = countryRefreshInterval;
    }
    _countryRetryTimer?.cancel();
    _countryRetryTimer = Timer(delay, () {
      _countryRetryTimer = null;
      unawaited(refreshAvailableCountries(refresh: true));
    });
  }

  void _stopCountryRetryWindow() {
    _countryRetryTimer?.cancel();
    _countryRetryTimer = null;
    _countryRetryIndex = 0;
    _countryRetryActive = false;
  }

  void _scheduleBootstrapRefresh() {
    _bootstrapRefreshTimer?.cancel();
    if (_closed || !bootstrapAvailable) return;
    final phase = bootstrapState?.phase;
    if (phase == IrvioBootstrapPhase.ready ||
        phase == IrvioBootstrapPhase.failed ||
        phase == IrvioBootstrapPhase.empty) {
      return;
    }
    final delay = _bootstrapRefreshes < 40
        ? bootstrapRefreshInterval
        : bootstrapRefreshSteadyInterval;
    _bootstrapRefreshTimer = Timer(delay, () async {
      _bootstrapRefreshes++;
      await refreshBootstrapState();
      _scheduleBootstrapRefresh();
    });
  }

  String _safeMessage(Object error) {
    if (error is IrvioApiException) return error.message;
    if (error is SocketException) {
      return strings.text('irviodSocketUnavailable');
    }
    return strings.text('interfaceUnavailable');
  }

  Future<void> _disposeClient() async {
    _trafficTimer?.cancel();
    if (!_closed) trafficStats.value = null;
    _stopCountryRetryWindow();
    _countriesRefreshQueued = false;
    _countriesRefreshForceQueued = false;
    await _disposeLogClient();
    await _eventSubscription?.cancel();
    await _errorSubscription?.cancel();
    _eventSubscription = null;
    _errorSubscription = null;
    final client = _client;
    _client = null;
    if (client != null) await client.close();
  }

  Future<void> _disposeLogClient() async {
    await _logEventSubscription?.cancel();
    await _logErrorSubscription?.cancel();
    _logEventSubscription = null;
    _logErrorSubscription = null;
    final client = _logClient;
    _logClient = null;
    if (client != null) await client.close();
  }

  @override
  void dispose() {
    _closed = true;
    _reconnectTimer?.cancel();
    _logReconnectTimer?.cancel();
    _bootstrapRefreshTimer?.cancel();
    unawaited(_disposeClient());
    trafficStats.dispose();
    super.dispose();
  }

  Future<void> _refreshTraffic() async {
    final client = _client;
    if (_closed ||
        _shutdownInProgress ||
        _commandPending ||
        _trafficPending ||
        client == null) {
      return;
    }
    _trafficPending = true;
    try {
      final value = await client.getTrafficStats();
      if (!_closed && !_shutdownInProgress && identical(client, _client)) {
        trafficStats.value = value;
      }
    } on Object {
      // Optional statistics must never trigger daemon reconnect/socket activation.
      if (!_closed && !_shutdownInProgress && identical(client, _client)) {
        trafficStats.value = null;
      }
    } finally {
      _trafficPending = false;
    }
  }
}

bool _sameBytes(List<int> left, List<int> right) {
  if (left.length != right.length) return false;
  for (var index = 0; index < left.length; index++) {
    if (left[index] != right[index]) return false;
  }
  return true;
}

abstract interface class IrvioUiPreferences {
  Future<String?> getString(String key);

  Future<void> setString(String key, String value);
}

class SharedIrvioUiPreferences implements IrvioUiPreferences {
  const SharedIrvioUiPreferences();

  @override
  Future<String?> getString(String key) async =>
      (await SharedPreferences.getInstance()).getString(key);

  @override
  Future<void> setString(String key, String value) async {
    await (await SharedPreferences.getInstance()).setString(key, value);
  }
}
