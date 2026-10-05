// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';

import '../models/irvio_models.dart';

enum IrvioLanguage {
  english('en', 'English'),
  russian('ru', 'Русский'),
  german('de', 'Deutsch');

  const IrvioLanguage(this.code, this.nativeName);

  final String code;
  final String nativeName;

  Locale get locale => Locale(code);

  static IrvioLanguage fromCode(String? code) => switch (code) {
    'ru' => IrvioLanguage.russian,
    'de' => IrvioLanguage.german,
    _ => IrvioLanguage.english,
  };
}

class IrvioLocalizations {
  const IrvioLocalizations(this.language);

  final IrvioLanguage language;

  static const LocalizationsDelegate<IrvioLocalizations> delegate =
      _IrvioLocalizationsDelegate();

  static IrvioLocalizations of(BuildContext context) =>
      Localizations.of<IrvioLocalizations>(context, IrvioLocalizations) ??
      const IrvioLocalizations(IrvioLanguage.english);

  static IrvioLocalizations forLanguage(IrvioLanguage language) =>
      IrvioLocalizations(language);

  String text(String key, [Map<String, Object> arguments = const {}]) {
    var value =
        _translations[language.code]?[key] ?? _translations['en']![key] ?? key;
    for (final entry in arguments.entries) {
      value = value.replaceAll('{${entry.key}}', '${entry.value}');
    }
    return value;
  }

  Set<String> get missingTranslationKeys {
    final english = _translations['en']!.keys.toSet();
    return english.difference(_translations[language.code]!.keys.toSet());
  }

  String countryName(String countryCode) {
    final code = countryCode.toUpperCase();
    return _countryNames[language.code]?[code] ??
        _countryNames['en']?[code] ??
        code;
  }

  String connectionState(IrvioConnectionState state) => switch (state) {
    IrvioConnectionState.disconnected => text('stateDisconnected'),
    IrvioConnectionState.connecting => text('stateConnecting'),
    IrvioConnectionState.connected => text('stateConnected'),
    IrvioConnectionState.recovering => text('stateRecovering'),
    IrvioConnectionState.noNetwork => text('stateNoNetwork'),
    IrvioConnectionState.noExit => text('stateNoExit'),
    IrvioConnectionState.disconnecting => text('stateDisconnecting'),
    IrvioConnectionState.error => text('stateError'),
  };

  String reachableNodes(int count) =>
      text(count == 1 ? 'reachableNode' : 'reachableNodes', {'count': count});

  String generatedAge(Duration age) {
    if (age.isNegative || age.inMinutes < 1) return text('generatedJustNow');
    if (age.inMinutes == 1) return text('generatedOneMinuteAgo');
    return text('generatedMinutesAgo', {'count': age.inMinutes});
  }
}

extension IrvioLocalizationsContext on BuildContext {
  IrvioLocalizations get l10n => IrvioLocalizations.of(this);
}

class _IrvioLocalizationsDelegate
    extends LocalizationsDelegate<IrvioLocalizations> {
  const _IrvioLocalizationsDelegate();

  @override
  bool isSupported(Locale locale) => IrvioLanguage.values.any(
    (language) => language.code == locale.languageCode,
  );

  @override
  Future<IrvioLocalizations> load(Locale locale) => SynchronousFuture(
    IrvioLocalizations(IrvioLanguage.fromCode(locale.languageCode)),
  );

  @override
  bool shouldReload(_IrvioLocalizationsDelegate old) => false;
}

const _translations = <String, Map<String, String>>{
  'en': {
    'home': 'Home',
    'settings': 'Settings',
    'diagnostics': 'Diagnostics',
    'logs': 'Logs',
    'shareIrvio': 'Share IRVIO',
    'about': 'About',
    'trafficClients': 'CLIENTS',
    'trafficShared': 'SHARED TRAFFIC',
    'trafficMine': 'MY TRAFFIC',
    'trafficSessionHint': 'This session: MY TRAFFIC includes your download + upload through IRVIO. SHARED TRAFFIC counts clients’ upload through your Exit. IP headers included.',
    'trafficPartialHint': 'Partial session totals: some accounting was unavailable or the daemon recovered. Upload only.',
    'minimize': 'Minimize',
    'closeInterface': 'Shutdown IRVIO',
    'daemonOffline': 'Daemon offline',
    'stateDisconnected': 'Disconnected',
    'stateConnecting': 'Connecting',
    'stateConnected': 'Connected',
    'stateRecovering': 'Recovering',
    'stateNoNetwork': 'No network',
    'stateNoExit': 'No exit available',
    'stateDisconnecting': 'Disconnecting',
    'stateError': 'Error',
    'chooseCountry': 'Choose a country',
    'loadingLocations': 'Loading available locations…',
    'noExitLocations': 'No Exit locations currently available',
    'unableLocations': 'Unable to load available locations',
    'retry': 'Retry',
    'connectedThrough': 'Connected through a peer in {country}.',
    'willConnectThrough': 'You will be connected through a peer in {country}.',
    'connectingIrviod': 'Connecting to IRVIO service…',
    'irviodUnavailable': 'IRVIO service is unavailable.',
    'connect': 'CONNECT',
    'privateAccess': 'Private, decentralized access',
    'connectingEllipsis': 'Connecting…',
    'establishingSecure': 'Establishing a secure connection',
    'connectedUpper': 'CONNECTED',
    'secureEstablished': 'Secure connection established',
    'reconnectingEllipsis': 'Reconnecting…',
    'findingPeer': 'Finding a new peer',
    'noNetworkTitle': 'No network',
    'checkInternet': 'Check your Internet connection',
    'noPeersTitle': 'No peers available',
    'noSuitablePeers': 'No suitable peers found for this country',
    'disconnectingEllipsis': 'Disconnecting…',
    'closingSecure': 'Closing the secure connection',
    'somethingWrong': 'Something went wrong',
    'operationFailed': 'IRVIO could not complete this operation',
    'disconnect': 'Disconnect',
    'general': 'GENERAL',
    'startAtLogin': 'Start at login',
    'startAtLoginSubtitle': 'Open IRVIO with your desktop session.',
    'showNotifications': 'Show notifications',
    'showNotificationsSubtitle':
        'Notify only on meaningful connection changes.',
    'appearance': 'APPEARANCE',
    'language': 'Language',
    'theme': 'Theme',
    'themeSystem': 'System',
    'themeLight': 'Light',
    'themeDark': 'Dark',
    'profile': 'PROFILE',
    'deleteProfile': 'Delete profile',
    'deleteProfileSubtitle':
        'Remove this device identity, bootstrap data, and local IRVIO state.',
    'deleteRequiresApi': 'Requires IRVIO Local API v1.2.',
    'restarting': 'Restarting…',
    'delete': 'Delete',
    'deleteProfileQuestion': 'Delete IRVIO profile?',
    'deleteProfileWarning': 'This disconnects the VPN and permanently deletes this device identity, bootstrap information, and local IRVIO database. You will need a bootstrap QR or code to join again.',
    'cancel': 'Cancel',
    'autostartUpdateFailed': 'Could not update desktop autostart.',
    'eventStreamClosed': 'The IRVIO event stream closed.',
    'locationsUnavailableVersion':
        'Locations unavailable with this IRVIO service version.',
    'chooseValidCountry': 'Choose a valid country.',
    'locationsUnavailable': 'Locations unavailable.',
    'irviodAutoReconnect': 'IRVIO service is not available. The interface will reconnect automatically.',
    'locationsUnavailableOffline':
        'Locations unavailable while the IRVIO service is offline.',
    'logStreamClosed': 'The IRVIO log stream closed.',
    'logGap': 'Some earlier log records are no longer available.',
    'logRestarted':
        'IRVIO service restarted; the log stream was resynchronized.',
    'logMissed': 'Some log records were missed.',
    'logBounded': 'Older log records were removed from this bounded view.',
    'irviodSocketUnavailable':
        'IRVIO service is not running or the control socket is unavailable.',
    'interfaceUnavailable': 'The IRVIO interface is temporarily unavailable.',
    'daemonStatus': 'Daemon status',
    'running': 'Running',
    'unavailable': 'Unavailable',
    'currentState': 'Current state',
    'selectedCountry': 'Selected country',
    'connectionUptime': 'Connection uptime',
    'localApi': 'Local API',
    'lastError': 'Last error',
    'all': 'All',
    'info': 'Info',
    'warning': 'Warning',
    'error': 'Error',
    'autoScroll': 'Auto-scroll',
    'logsUnavailableV10': 'Logs unavailable with Local API v1.0.',
    'connectingDaemonLogs': 'Connecting to daemon logs…',
    'logViewPaused': 'Log view paused',
    'noLogRecords': 'No log records',
    'resume': 'Resume',
    'pause': 'Pause',
    'copy': 'Copy',
    'clearView': 'Clear view',
    'shareDescription': 'Help another device join the IRVIO network.',
    'shareUnavailableV10': 'Share IRVIO unavailable with Local API v1.0.',
    'noReachableBootstrap': 'No reachable bootstrap nodes yet',
    'reachableNode': '{count} reachable bootstrap node',
    'reachableNodes': '{count} reachable bootstrap nodes',
    'generatedJustNow': 'Generated just now',
    'generatedOneMinuteAgo': 'Generated 1 minute ago',
    'generatedMinutesAgo': 'Generated {count} minutes ago',
    'refreshQr': 'Refresh QR',
    'saveQr': 'Save QR code',
    'qrSaved': 'QR code saved.',
    'qrSaveFailed': 'Could not save the QR code.',
    'copyBootstrap': 'Copy bootstrap code',
    'tagline': 'Private. Decentralized. For a freer internet.',
    'aboutLifecycle': 'Closing IRVIO shuts down the VPN. Minimize the window to keep the connection running.',
    'interface': 'Interface',
    'irvioCore': 'IRVIO Core',
    'geoAttribution': 'IP geolocation data by DB-IP',
    'joinIrvio': 'Join IRVIO network',
    'provideIntroduction':
        'To discover the IRVIO network, provide an introduction.',
    'pasteBootstrap': 'Paste bootstrap code',
    'pasteBootstrapError': 'Paste an IRVIO bootstrap code.',
    'joinNetwork': 'Join network',
    'importQr': 'Import QR image',
    'authenticatingIntroducer': 'Authenticating introducer…',
    'qrImages': 'QR images',
    'qrTooLarge': 'The QR image is too large.',
    'noIrvioQr': 'No IRVIO QR found.',
    'invalidBootstrap': 'Invalid bootstrap bundle.',
    'unsupportedBootstrap': 'Unsupported bootstrap bundle version.',
    'bundleTooLarge': 'Bootstrap bundle is too large.',
    'noValidBootstrapNodes': 'No valid bootstrap nodes.',
    'openIrvio': 'Open IRVIO',
    'disconnectVpn': 'Disconnect VPN',
    'quitUi': 'Quit UI · VPN keeps running',
    'shutdownIrvio': 'Shutdown IRVIO',
    'shutdownWarning': 'This will disconnect the VPN, stop Exit participation, and close IRVIO completely. Your ordinary Internet connection will be restored.',
    'dontShowAgain': 'Do not show this again',
    'shutdownUnavailable':
        'IRVIO cannot shut down safely while the daemon is unavailable.',
    'notificationConnected': 'Connected to {country}',
    'notificationRecovering': 'Connection lost. Reconnecting…',
    'notificationNewPeer': 'Connected to a new peer',
    'notificationNoExit': 'No Exit available in {country}',
    'notificationNoNetwork': 'IRVIO network unavailable',
  },
  'ru': {
    'home': 'Главная',
    'settings': 'Настройки',
    'diagnostics': 'Диагностика',
    'logs': 'Журнал',
    'shareIrvio': 'Поделиться IRVIO',
    'about': 'О программе',
    'trafficClients': 'КЛИЕНТЫ',
    'trafficShared': 'ТРАНЗИТНЫЙ ТРАФИК',
    'trafficMine': 'МОЙ ТРАФИК',
    'trafficSessionHint': 'За сессию: МОЙ ТРАФИК — ваша загрузка + отправка через IRVIO. ОБЩИЙ ТРАФИК — отправка клиентов через ваш Exit. Включая IP-заголовки.',
    'trafficPartialHint': 'Неполные итоги сессии: часть измерений недоступна или daemon восстановился. Только отправленный трафик.',
    'minimize': 'Свернуть',
    'closeInterface': 'Завершить работу IRVIO',
    'daemonOffline': 'Демон недоступен',
    'stateDisconnected': 'Отключено',
    'stateConnecting': 'Подключение',
    'stateConnected': 'Подключено',
    'stateRecovering': 'Восстановление',
    'stateNoNetwork': 'Нет сети',
    'stateNoExit': 'Нет доступных Exit',
    'stateDisconnecting': 'Отключение',
    'stateError': 'Ошибка',
    'chooseCountry': 'Выберите страну',
    'loadingLocations': 'Загрузка доступных локаций…',
    'noExitLocations': 'Сейчас нет доступных Exit-локаций',
    'unableLocations': 'Не удалось загрузить доступные локации',
    'retry': 'Повторить',
    'connectedThrough': 'Подключено через пир в стране: {country}.',
    'willConnectThrough':
        'Подключение будет выполнено через пир в стране: {country}.',
    'connectingIrviod': 'Подключение к службе IRVIO…',
    'irviodUnavailable': 'Служба IRVIO недоступна.',
    'connect': 'ПОДКЛЮЧИТЬ',
    'privateAccess': 'Приватный децентрализованный доступ',
    'connectingEllipsis': 'Подключение…',
    'establishingSecure': 'Устанавливается защищённое соединение',
    'connectedUpper': 'ПОДКЛЮЧЕНО',
    'secureEstablished': 'Защищённое соединение установлено',
    'reconnectingEllipsis': 'Переподключение…',
    'findingPeer': 'Поиск нового пира',
    'noNetworkTitle': 'Нет сети',
    'checkInternet': 'Проверьте подключение к интернету',
    'noPeersTitle': 'Нет доступных пиров',
    'noSuitablePeers': 'Для этой страны не найдено подходящих пиров',
    'disconnectingEllipsis': 'Отключение…',
    'closingSecure': 'Завершение защищённого соединения',
    'somethingWrong': 'Что-то пошло не так',
    'operationFailed': 'IRVIO не удалось выполнить операцию',
    'disconnect': 'Отключить',
    'general': 'ОСНОВНЫЕ',
    'startAtLogin': 'Запускать при входе',
    'startAtLoginSubtitle': 'Открывать IRVIO вместе с сеансом рабочего стола.',
    'showNotifications': 'Показывать уведомления',
    'showNotificationsSubtitle':
        'Уведомлять только о важных изменениях соединения.',
    'appearance': 'ОФОРМЛЕНИЕ',
    'language': 'Язык',
    'theme': 'Тема',
    'themeSystem': 'Системная',
    'themeLight': 'Светлая',
    'themeDark': 'Тёмная',
    'profile': 'ПРОФИЛЬ',
    'deleteProfile': 'Удалить профиль',
    'deleteProfileSubtitle': 'Удалить идентичность устройства, bootstrap-данные и локальное состояние IRVIO.',
    'deleteRequiresApi': 'Требуется IRVIO Local API v1.2.',
    'restarting': 'Перезапуск…',
    'delete': 'Удалить',
    'deleteProfileQuestion': 'Удалить профиль IRVIO?',
    'deleteProfileWarning': 'VPN будет отключён, а идентичность устройства, bootstrap-информация и локальная база IRVIO будут удалены навсегда. Для повторного входа потребуется bootstrap QR-код или текстовый код.',
    'cancel': 'Отмена',
    'autostartUpdateFailed': 'Не удалось обновить автозапуск рабочего стола.',
    'eventStreamClosed': 'Поток событий IRVIO закрыт.',
    'locationsUnavailableVersion':
        'Локации недоступны с этой версией службы IRVIO.',
    'chooseValidCountry': 'Выберите допустимую страну.',
    'locationsUnavailable': 'Локации недоступны.',
    'irviodAutoReconnect':
        'Служба IRVIO недоступна. Интерфейс переподключится автоматически.',
    'locationsUnavailableOffline':
        'Локации недоступны, пока служба IRVIO не работает.',
    'logStreamClosed': 'Поток журнала IRVIO закрыт.',
    'logGap': 'Некоторые ранние записи журнала больше недоступны.',
    'logRestarted':
        'Служба IRVIO перезапущена; поток журнала синхронизирован заново.',
    'logMissed': 'Некоторые записи журнала были пропущены.',
    'logBounded':
        'Старые записи удалены из ограниченного локального представления.',
    'irviodSocketUnavailable':
        'Служба IRVIO не запущена или управляющий сокет недоступен.',
    'interfaceUnavailable': 'Интерфейс IRVIO временно недоступен.',
    'daemonStatus': 'Состояние демона',
    'running': 'Работает',
    'unavailable': 'Недоступно',
    'currentState': 'Текущее состояние',
    'selectedCountry': 'Выбранная страна',
    'connectionUptime': 'Время подключения',
    'localApi': 'Local API',
    'lastError': 'Последняя ошибка',
    'all': 'Все',
    'info': 'Инфо',
    'warning': 'Предупреждения',
    'error': 'Ошибки',
    'autoScroll': 'Автопрокрутка',
    'logsUnavailableV10': 'Журнал недоступен с Local API v1.0.',
    'connectingDaemonLogs': 'Подключение к журналу демона…',
    'logViewPaused': 'Просмотр журнала приостановлен',
    'noLogRecords': 'Записей нет',
    'resume': 'Продолжить',
    'pause': 'Пауза',
    'copy': 'Копировать',
    'clearView': 'Очистить вид',
    'shareDescription': 'Помогите другому устройству войти в сеть IRVIO.',
    'shareUnavailableV10': 'Поделиться IRVIO нельзя с Local API v1.0.',
    'noReachableBootstrap': 'Пока нет доступных bootstrap-узлов',
    'reachableNode': 'Доступных bootstrap-узлов: {count}',
    'reachableNodes': 'Доступных bootstrap-узлов: {count}',
    'generatedJustNow': 'Создано только что',
    'generatedOneMinuteAgo': 'Создано 1 минуту назад',
    'generatedMinutesAgo': 'Создано {count} мин. назад',
    'refreshQr': 'Обновить QR',
    'saveQr': 'Сохранить QR-код',
    'qrSaved': 'QR-код сохранён.',
    'qrSaveFailed': 'Не удалось сохранить QR-код.',
    'copyBootstrap': 'Копировать bootstrap-код',
    'tagline': 'Приватно. Децентрализованно. Для свободного интернета.',
    'aboutLifecycle': 'Закрытие IRVIO завершает VPN-соединение. Сверните окно, чтобы соединение продолжало работать.',
    'interface': 'Интерфейс',
    'irvioCore': 'IRVIO Core',
    'geoAttribution': 'Данные IP-геолокации предоставлены DB-IP',
    'joinIrvio': 'Войти в сеть IRVIO',
    'provideIntroduction': 'Чтобы найти сеть IRVIO, предоставьте приглашение.',
    'pasteBootstrap': 'Вставьте bootstrap-код',
    'pasteBootstrapError': 'Вставьте bootstrap-код IRVIO.',
    'joinNetwork': 'Войти в сеть',
    'importQr': 'Импортировать QR',
    'authenticatingIntroducer': 'Аутентификация introducer-узла…',
    'qrImages': 'QR-изображения',
    'qrTooLarge': 'QR-изображение слишком большое.',
    'noIrvioQr': 'QR-код IRVIO не найден.',
    'invalidBootstrap': 'Некорректный bootstrap bundle.',
    'unsupportedBootstrap': 'Версия bootstrap bundle не поддерживается.',
    'bundleTooLarge': 'Bootstrap bundle слишком большой.',
    'noValidBootstrapNodes': 'Нет допустимых bootstrap-узлов.',
    'openIrvio': 'Открыть IRVIO',
    'disconnectVpn': 'Отключить VPN',
    'quitUi': 'Выйти из UI · VPN продолжит работу',
    'shutdownIrvio': 'Завершить работу IRVIO',
    'shutdownWarning': 'VPN будет отключён, участие как Exit прекращено, а IRVIO полностью закрыта. Обычное подключение к Интернету восстановится.',
    'dontShowAgain': 'Больше не показывать',
    'shutdownUnavailable':
        'Нельзя безопасно завершить IRVIO, пока демон недоступен.',
    'notificationConnected': 'Подключено: {country}',
    'notificationRecovering': 'Соединение потеряно. Переподключение…',
    'notificationNewPeer': 'Подключено к новому пиру',
    'notificationNoExit': 'Нет доступных Exit в стране: {country}',
    'notificationNoNetwork': 'Сеть IRVIO недоступна',
  },
  'de': {
    'home': 'Start',
    'settings': 'Einstellungen',
    'diagnostics': 'Diagnose',
    'logs': 'Protokoll',
    'shareIrvio': 'IRVIO teilen',
    'about': 'Über IRVIO',
    'trafficClients': 'CLIENTS',
    'trafficShared': 'WEITERGELEITET',
    'trafficMine': 'MEIN DATENVERKEHR',
    'trafficSessionHint': 'Diese Sitzung: MEIN TRAFFIC umfasst eigenen Download + Upload über IRVIO. GETEILTER TRAFFIC zählt Client-Upload über Ihren Exit. Mit IP-Headern.',
    'trafficPartialHint': 'Unvollständige Sitzungswerte: Messungen fehlen oder der Dienst wurde wiederhergestellt. Nur Upload.',
    'minimize': 'Minimieren',
    'closeInterface': 'IRVIO beenden',
    'daemonOffline': 'Daemon nicht erreichbar',
    'stateDisconnected': 'Getrennt',
    'stateConnecting': 'Verbindung wird hergestellt',
    'stateConnected': 'Verbunden',
    'stateRecovering': 'Wiederherstellung',
    'stateNoNetwork': 'Kein Netzwerk',
    'stateNoExit': 'Kein Exit verfügbar',
    'stateDisconnecting': 'Verbindung wird getrennt',
    'stateError': 'Fehler',
    'chooseCountry': 'Land auswählen',
    'loadingLocations': 'Verfügbare Standorte werden geladen…',
    'noExitLocations': 'Derzeit sind keine Exit-Standorte verfügbar',
    'unableLocations': 'Verfügbare Standorte konnten nicht geladen werden',
    'retry': 'Erneut versuchen',
    'connectedThrough': 'Verbunden über einen Peer in {country}.',
    'willConnectThrough': 'Verbindung erfolgt über einen Peer in {country}.',
    'connectingIrviod': 'Verbindung mit dem IRVIO-Dienst…',
    'irviodUnavailable': 'Der IRVIO-Dienst ist nicht verfügbar.',
    'connect': 'VERBINDEN',
    'privateAccess': 'Privater, dezentraler Zugang',
    'connectingEllipsis': 'Verbindung…',
    'establishingSecure': 'Sichere Verbindung wird hergestellt',
    'connectedUpper': 'VERBUNDEN',
    'secureEstablished': 'Sichere Verbindung hergestellt',
    'reconnectingEllipsis': 'Erneute Verbindung…',
    'findingPeer': 'Neuer Peer wird gesucht',
    'noNetworkTitle': 'Kein Netzwerk',
    'checkInternet': 'Internetverbindung überprüfen',
    'noPeersTitle': 'Keine Peers verfügbar',
    'noSuitablePeers': 'Keine passenden Peers für dieses Land gefunden',
    'disconnectingEllipsis': 'Trennen…',
    'closingSecure': 'Sichere Verbindung wird beendet',
    'somethingWrong': 'Ein Fehler ist aufgetreten',
    'operationFailed': 'IRVIO konnte diesen Vorgang nicht abschließen',
    'disconnect': 'Trennen',
    'general': 'ALLGEMEIN',
    'startAtLogin': 'Beim Anmelden starten',
    'startAtLoginSubtitle': 'IRVIO mit der Desktop-Sitzung öffnen.',
    'showNotifications': 'Benachrichtigungen anzeigen',
    'showNotificationsSubtitle':
        'Nur über wichtige Verbindungsänderungen informieren.',
    'appearance': 'DARSTELLUNG',
    'language': 'Sprache',
    'theme': 'Design',
    'themeSystem': 'System',
    'themeLight': 'Hell',
    'themeDark': 'Dunkel',
    'profile': 'PROFIL',
    'deleteProfile': 'Profil löschen',
    'deleteProfileSubtitle':
        'Geräteidentität, Bootstrap-Daten und lokalen IRVIO-Status entfernen.',
    'deleteRequiresApi': 'IRVIO Local API v1.2 ist erforderlich.',
    'restarting': 'Neustart…',
    'delete': 'Löschen',
    'deleteProfileQuestion': 'IRVIO-Profil löschen?',
    'deleteProfileWarning': 'Dadurch wird das VPN getrennt und die Geräteidentität, Bootstrap-Informationen sowie die lokale IRVIO-Datenbank werden dauerhaft gelöscht. Für den erneuten Beitritt ist ein Bootstrap-QR-Code oder Textcode erforderlich.',
    'cancel': 'Abbrechen',
    'autostartUpdateFailed':
        'Desktop-Autostart konnte nicht aktualisiert werden.',
    'eventStreamClosed': 'Der IRVIO-Ereignisstrom wurde geschlossen.',
    'locationsUnavailableVersion':
        'Standorte sind mit dieser Version des IRVIO-Dienstes nicht verfügbar.',
    'chooseValidCountry': 'Wählen Sie ein gültiges Land.',
    'locationsUnavailable': 'Standorte sind nicht verfügbar.',
    'irviodAutoReconnect': 'Der IRVIO-Dienst ist nicht verfügbar. Die Oberfläche verbindet sich automatisch neu.',
    'locationsUnavailableOffline':
        'Standorte sind nicht verfügbar, solange der IRVIO-Dienst offline ist.',
    'logStreamClosed': 'Der IRVIO-Protokollstrom wurde geschlossen.',
    'logGap': 'Einige ältere Protokolleinträge sind nicht mehr verfügbar.',
    'logRestarted': 'Der IRVIO-Dienst wurde neu gestartet; der Protokollstrom wurde neu synchronisiert.',
    'logMissed': 'Einige Protokolleinträge wurden verpasst.',
    'logBounded': 'Ältere Einträge wurden aus der begrenzten Ansicht entfernt.',
    'irviodSocketUnavailable': 'Der IRVIO-Dienst läuft nicht oder der Steuerungssocket ist nicht verfügbar.',
    'interfaceUnavailable':
        'Die IRVIO-Oberfläche ist vorübergehend nicht verfügbar.',
    'daemonStatus': 'Daemon-Status',
    'running': 'Aktiv',
    'unavailable': 'Nicht verfügbar',
    'currentState': 'Aktueller Status',
    'selectedCountry': 'Ausgewähltes Land',
    'connectionUptime': 'Verbindungsdauer',
    'localApi': 'Local API',
    'lastError': 'Letzter Fehler',
    'all': 'Alle',
    'info': 'Info',
    'warning': 'Warnung',
    'error': 'Fehler',
    'autoScroll': 'Automatisch scrollen',
    'logsUnavailableV10': 'Protokoll ist mit Local API v1.0 nicht verfügbar.',
    'connectingDaemonLogs': 'Verbindung zum Daemon-Protokoll…',
    'logViewPaused': 'Protokollansicht pausiert',
    'noLogRecords': 'Keine Protokolleinträge',
    'resume': 'Fortsetzen',
    'pause': 'Pausieren',
    'copy': 'Kopieren',
    'clearView': 'Ansicht leeren',
    'shareDescription':
        'Einem anderen Gerät den Beitritt zum IRVIO-Netz ermöglichen.',
    'shareUnavailableV10':
        'IRVIO teilen ist mit Local API v1.0 nicht verfügbar.',
    'noReachableBootstrap': 'Noch keine erreichbaren Bootstrap-Knoten',
    'reachableNode': '{count} erreichbarer Bootstrap-Knoten',
    'reachableNodes': '{count} erreichbare Bootstrap-Knoten',
    'generatedJustNow': 'Gerade erstellt',
    'generatedOneMinuteAgo': 'Vor 1 Minute erstellt',
    'generatedMinutesAgo': 'Vor {count} Minuten erstellt',
    'refreshQr': 'QR aktualisieren',
    'saveQr': 'QR-Code speichern',
    'qrSaved': 'QR-Code gespeichert.',
    'qrSaveFailed': 'Der QR-Code konnte nicht gespeichert werden.',
    'copyBootstrap': 'Bootstrap-Code kopieren',
    'tagline': 'Privat. Dezentral. Für ein freieres Internet.',
    'aboutLifecycle': 'Das Schließen von IRVIO beendet die VPN-Verbindung. Minimieren Sie das Fenster, damit die Verbindung bestehen bleibt.',
    'interface': 'Oberfläche',
    'irvioCore': 'IRVIO Core',
    'geoAttribution': 'IP-Geolokalisierungsdaten von DB-IP',
    'joinIrvio': 'Dem IRVIO-Netz beitreten',
    'provideIntroduction':
        'Geben Sie eine Einführung an, um das IRVIO-Netz zu finden.',
    'pasteBootstrap': 'Bootstrap-Code einfügen',
    'pasteBootstrapError': 'Fügen Sie einen IRVIO-Bootstrap-Code ein.',
    'joinNetwork': 'Netz beitreten',
    'importQr': 'QR-Bild importieren',
    'authenticatingIntroducer': 'Introducer wird authentifiziert…',
    'qrImages': 'QR-Bilder',
    'qrTooLarge': 'Das QR-Bild ist zu groß.',
    'noIrvioQr': 'Kein IRVIO-QR-Code gefunden.',
    'invalidBootstrap': 'Ungültiges Bootstrap-Bundle.',
    'unsupportedBootstrap': 'Bootstrap-Bundle-Version wird nicht unterstützt.',
    'bundleTooLarge': 'Bootstrap-Bundle ist zu groß.',
    'noValidBootstrapNodes': 'Keine gültigen Bootstrap-Knoten.',
    'openIrvio': 'IRVIO öffnen',
    'disconnectVpn': 'VPN trennen',
    'quitUi': 'UI beenden · VPN bleibt aktiv',
    'shutdownIrvio': 'IRVIO beenden',
    'shutdownWarning': 'Das VPN wird getrennt, die Exit-Teilnahme beendet und IRVIO vollständig geschlossen. Die normale Internetverbindung wird wiederhergestellt.',
    'dontShowAgain': 'Nicht erneut anzeigen',
    'shutdownUnavailable': 'IRVIO kann nicht sicher beendet werden, solange der Daemon nicht erreichbar ist.',
    'notificationConnected': 'Verbunden mit {country}',
    'notificationRecovering': 'Verbindung verloren. Erneute Verbindung…',
    'notificationNewPeer': 'Mit einem neuen Peer verbunden',
    'notificationNoExit': 'Kein Exit in {country} verfügbar',
    'notificationNoNetwork': 'IRVIO-Netzwerk nicht verfügbar',
  },
};

const _countryNames = <String, Map<String, String>>{
  'en': {
    'DE': 'Germany',
    'NL': 'Netherlands',
    'RU': 'Russia',
    'FR': 'France',
    'GB': 'United Kingdom',
    'US': 'United States',
    'CA': 'Canada',
  },
  'ru': {
    'DE': 'Германия',
    'NL': 'Нидерланды',
    'RU': 'Россия',
    'FR': 'Франция',
    'GB': 'Великобритания',
    'US': 'США',
    'CA': 'Канада',
  },
  'de': {
    'DE': 'Deutschland',
    'NL': 'Niederlande',
    'RU': 'Russland',
    'FR': 'Frankreich',
    'GB': 'Vereinigtes Königreich',
    'US': 'Vereinigte Staaten',
    'CA': 'Kanada',
  },
};
