// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../app/irvio_app_controller.dart';
import '../l10n/irvio_localizations.dart';
import '../models/irvio_models.dart';
import '../theme/irvio_theme.dart';
import '../widgets/panel.dart';

enum _LogFilter { all, info, warning, error }

class LogsScreen extends StatefulWidget {
  const LogsScreen({super.key, required this.controller});

  final IrvioAppController controller;

  @override
  State<LogsScreen> createState() => _LogsScreenState();
}

class _LogsScreenState extends State<LogsScreen> {
  final _scroll = ScrollController();
  _LogFilter _filter = _LogFilter.all;
  bool _autoScroll = true;

  Iterable<IrvioLogRecord> get _visible => widget.controller.logs.where(
    (record) => switch (_filter) {
      _LogFilter.all => true,
      _LogFilter.info => record.severity == IrvioLogSeverity.info,
      _LogFilter.warning => record.severity == IrvioLogSeverity.warning,
      _LogFilter.error => record.severity == IrvioLogSeverity.error,
    },
  );

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (_autoScroll && !widget.controller.logsPaused) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && _scroll.hasClients) {
          _scroll.jumpTo(_scroll.position.maxScrollExtent);
        }
      });
    }
    final records = _visible.toList(growable: false);
    return Padding(
      padding: const EdgeInsets.fromLTRB(22, 4, 22, 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            context.l10n.text('logs'),
            style: Theme.of(context).textTheme.headlineSmall,
          ),
          const SizedBox(height: 11),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: _LogFilter.values
                .map(
                  (filter) => ChoiceChip(
                    key: Key('logs-filter-${filter.name}'),
                    label: Text(_filterLabel(context.l10n, filter)),
                    selected: _filter == filter,
                    onSelected: (_) => setState(() => _filter = filter),
                  ),
                )
                .toList(),
          ),
          const SizedBox(height: 5),
          SwitchListTile(
            dense: true,
            contentPadding: EdgeInsets.zero,
            title: Text(context.l10n.text('autoScroll')),
            value: _autoScroll,
            onChanged: (value) => setState(() => _autoScroll = value),
          ),
          if (!widget.controller.logsAvailable) ...[
            Text(
              widget.controller.hello?.supportsV11 == false
                  ? context.l10n.text('logsUnavailableV10')
                  : widget.controller.logsError ??
                        context.l10n.text('connectingDaemonLogs'),
              key: const Key('logs-unavailable'),
              style: Theme.of(context).textTheme.bodySmall,
            ),
            const SizedBox(height: 7),
          ] else if (widget.controller.logsError != null) ...[
            Text(
              widget.controller.logsError!,
              key: const Key('logs-gap'),
              style: TextStyle(color: irvioAmber, fontSize: 11),
            ),
            const SizedBox(height: 7),
          ],
          Expanded(
            child: IrvioPanel(
              padding: EdgeInsets.zero,
              child: records.isEmpty
                  ? Center(
                      child: Text(
                        widget.controller.logsPaused
                            ? context.l10n.text('logViewPaused')
                            : context.l10n.text('noLogRecords'),
                        style: Theme.of(context).textTheme.bodySmall,
                      ),
                    )
                  : ListView.builder(
                      key: const Key('logs-list'),
                      controller: _scroll,
                      itemCount: records.length,
                      itemBuilder: (context, index) =>
                          _LogRow(record: records[index]),
                    ),
            ),
          ),
          const SizedBox(height: 10),
          Wrap(
            spacing: 8,
            runSpacing: 7,
            children: [
              OutlinedButton.icon(
                key: const Key('logs-pause'),
                onPressed: widget.controller.hello?.supportsV11 == true
                    ? () => widget.controller.setLogsPaused(
                        !widget.controller.logsPaused,
                      )
                    : null,
                icon: Icon(
                  widget.controller.logsPaused
                      ? Icons.play_arrow_rounded
                      : Icons.pause_rounded,
                  size: 17,
                ),
                label: Text(
                  widget.controller.logsPaused
                      ? context.l10n.text('resume')
                      : context.l10n.text('pause'),
                ),
              ),
              OutlinedButton.icon(
                key: const Key('logs-copy'),
                onPressed: records.isEmpty
                    ? null
                    : () => Clipboard.setData(
                        ClipboardData(
                          text: records.map(_plainLogRecord).join('\n'),
                        ),
                      ),
                icon: const Icon(Icons.copy_rounded, size: 17),
                label: Text(context.l10n.text('copy')),
              ),
              OutlinedButton.icon(
                key: const Key('logs-clear'),
                onPressed: widget.controller.logs.isEmpty
                    ? null
                    : widget.controller.clearLogView,
                icon: const Icon(Icons.clear_all_rounded, size: 17),
                label: Text(context.l10n.text('clearView')),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _LogRow extends StatelessWidget {
  const _LogRow({required this.record});

  final IrvioLogRecord record;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(10, 7, 10, 2),
    child: SelectableText.rich(
      TextSpan(
        style: const TextStyle(fontFamily: 'monospace', fontSize: 10.5),
        children: [
          TextSpan(text: '${_clock(record.timestamp)}  '),
          TextSpan(
            text: _severityLabel(record.severity).padRight(5),
            style: TextStyle(color: _severityColor(record.severity)),
          ),
          TextSpan(text: '  ${record.message}${_fieldSuffix(record.fields)}'),
        ],
      ),
    ),
  );
}

String _filterLabel(IrvioLocalizations strings, _LogFilter filter) =>
    switch (filter) {
      _LogFilter.all => strings.text('all'),
      _LogFilter.info => strings.text('info'),
      _LogFilter.warning => strings.text('warning'),
      _LogFilter.error => strings.text('error'),
    };

String _severityLabel(IrvioLogSeverity severity) => switch (severity) {
  IrvioLogSeverity.info => 'INFO',
  IrvioLogSeverity.warning => 'WARN',
  IrvioLogSeverity.error => 'ERROR',
};

Color _severityColor(IrvioLogSeverity severity) => switch (severity) {
  IrvioLogSeverity.info => irvioBlue,
  IrvioLogSeverity.warning => irvioAmber,
  IrvioLogSeverity.error => irvioRed,
};

String _clock(DateTime value) =>
    '${value.hour.toString().padLeft(2, '0')}:'
    '${value.minute.toString().padLeft(2, '0')}:'
    '${value.second.toString().padLeft(2, '0')}';

String _fieldSuffix(Map<String, String> fields) => fields.isEmpty
    ? ''
    : '  ${fields.entries.map((entry) => '${entry.key}=${entry.value}').join(' ')}';

String _plainLogRecord(IrvioLogRecord record) =>
    '${_clock(record.timestamp)} ${_severityLabel(record.severity).padRight(5)} '
    '${record.message}${_fieldSuffix(record.fields)}';
