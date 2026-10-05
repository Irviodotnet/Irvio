// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../l10n/irvio_localizations.dart';
import '../models/irvio_models.dart';
import '../theme/irvio_theme.dart';

class ConnectionControl extends StatefulWidget {
  const ConnectionControl({
    super.key,
    required this.status,
    required this.enabled,
    required this.disconnectIntent,
    this.connectPending = false,
    this.disconnectPending = false,
    required this.onConnect,
    required this.onDisconnect,
  });

  final IrvioStatus status;
  final bool enabled;
  final bool disconnectIntent;
  final bool connectPending;
  final bool disconnectPending;
  final VoidCallback onConnect;
  final VoidCallback onDisconnect;

  @override
  State<ConnectionControl> createState() => _ConnectionControlState();
}

class _ConnectionControlState extends State<ConnectionControl>
    with SingleTickerProviderStateMixin {
  bool _hovering = false;
  late final AnimationController _animation = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 1450),
  );

  IrvioConnectionState get _displayState {
    if (widget.disconnectPending) return IrvioConnectionState.disconnecting;
    // Local request feedback only: never replace an authoritative active state.
    if (widget.connectPending &&
        (widget.status.state == IrvioConnectionState.disconnected ||
            widget.status.state == IrvioConnectionState.noExit ||
            widget.status.state == IrvioConnectionState.noNetwork ||
            widget.status.state == IrvioConnectionState.error)) {
      return IrvioConnectionState.connecting;
    }
    return widget.status.state;
  }

  bool get _busy => switch (_displayState) {
    IrvioConnectionState.connecting ||
    IrvioConnectionState.recovering ||
    IrvioConnectionState.disconnecting => true,
    _ => false,
  };

  @override
  void initState() {
    super.initState();
    if (_busy) _animation.repeat();
  }

  @override
  void didUpdateWidget(covariant ConnectionControl oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (_busy && !_animation.isAnimating) {
      _animation.repeat();
    } else if (!_busy && _animation.isAnimating) {
      _animation.stop();
    }
  }

  @override
  void dispose() {
    _animation.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final strings = context.l10n;
    final state = _displayState;
    final presentation = _presentation(widget.status, state, strings);
    final glow = connectionGlowForState(state);
    final busy = _busy;
    final circleAction = state == IrvioConnectionState.disconnecting
        ? null
        : widget.disconnectIntent
        ? widget.onDisconnect
        : widget.onConnect;
    final actionHint = state == IrvioConnectionState.disconnecting
        ? presentation.subtitle
        : widget.disconnectIntent
        ? _hovering
              ? strings.text('disconnect')
              : presentation.subtitle
        : presentation.subtitle;

    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        AnimatedContainer(
          key: Key('connection-glow-${glow.name}'),
          duration: const Duration(milliseconds: 220),
          decoration: BoxDecoration(
            shape: BoxShape.circle,
            boxShadow: glow == ConnectionGlowCategory.none
                ? const []
                : [
                    BoxShadow(
                      color: _glowColor(glow).withValues(alpha: .16),
                      blurRadius: 46,
                      spreadRadius: 5,
                    ),
                  ],
          ),
          child: Semantics(
            button: circleAction != null,
            label: presentation.title,
            child: SizedBox.square(
              dimension: 188,
              child: AnimatedBuilder(
                animation: _animation,
                builder: (context, child) => CustomPaint(
                  painter: _RingPainter(
                    color: presentation.color,
                    rotating: busy,
                    phase: _animation.value,
                  ),
                  child: child,
                ),
                child: Material(
                  color: Colors.transparent,
                  shape: const CircleBorder(),
                  child: InkWell(
                    key: const Key('primary-connection-action'),
                    customBorder: const CircleBorder(),
                    onTap: widget.enabled ? circleAction : null,
                    onHover: (hovering) {
                      if (_hovering != hovering) {
                        setState(() => _hovering = hovering);
                      }
                    },
                    child: Stack(
                      alignment: Alignment.center,
                      children: [
                        Icon(
                          presentation.icon,
                          key: const Key('connection-center-icon'),
                          color: presentation.color,
                          size: 43,
                        ),
                        Positioned(
                          top: 188 / 2 + 43 / 2 + 10,
                          left: 4,
                          right: 4,
                          child: Text(
                            presentation.title,
                            textAlign: TextAlign.center,
                            maxLines: 2,
                            style: const TextStyle(
                              fontWeight: FontWeight.w700,
                              letterSpacing: .4,
                            ),
                          ),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
          ),
        ),
        const SizedBox(height: 12),
        SizedBox(
          height: 38,
          child: Text(
            actionHint,
            key: const Key('connection-subtitle'),
            textAlign: TextAlign.center,
            style: Theme.of(context).textTheme.bodySmall,
          ),
        ),
      ],
    );
  }
}

class _Presentation {
  const _Presentation(this.title, this.subtitle, this.color, this.icon);

  final String title;
  final String subtitle;
  final Color color;
  final IconData icon;
}

enum ConnectionGlowCategory { none, blue, green, amber, red }

ConnectionGlowCategory connectionGlowForState(IrvioConnectionState state) =>
    switch (state) {
      IrvioConnectionState.disconnected => ConnectionGlowCategory.none,
      IrvioConnectionState.connecting => ConnectionGlowCategory.blue,
      IrvioConnectionState.connected => ConnectionGlowCategory.green,
      IrvioConnectionState.recovering ||
      IrvioConnectionState.disconnecting => ConnectionGlowCategory.amber,
      IrvioConnectionState.noNetwork ||
      IrvioConnectionState.noExit ||
      IrvioConnectionState.error => ConnectionGlowCategory.red,
    };

Color _glowColor(ConnectionGlowCategory glow) => switch (glow) {
  ConnectionGlowCategory.none => Colors.transparent,
  ConnectionGlowCategory.blue => irvioConnectingBlue,
  ConnectionGlowCategory.green => irvioGreen,
  ConnectionGlowCategory.amber => irvioAmber,
  ConnectionGlowCategory.red => irvioRed,
};

_Presentation _presentation(
  IrvioStatus status,
  IrvioConnectionState state,
  IrvioLocalizations strings,
) => switch (state) {
  IrvioConnectionState.disconnected => _Presentation(
    strings.text('connect'),
    strings.text('privateAccess'),
    irvioBlue,
    Icons.power_settings_new_rounded,
  ),
  IrvioConnectionState.connecting => _Presentation(
    strings.text('connectingEllipsis'),
    strings.text('establishingSecure'),
    irvioConnectingBlue,
    Icons.lock_outline_rounded,
  ),
  IrvioConnectionState.connected => _Presentation(
    strings.text('connectedUpper'),
    status.latencyMs == null
        ? strings.text('secureEstablished')
        : '${status.latencyMs} ms',
    irvioGreen,
    Icons.check_rounded,
  ),
  IrvioConnectionState.recovering => _Presentation(
    strings.text('reconnectingEllipsis'),
    strings.text('findingPeer'),
    irvioAmber,
    Icons.sync_rounded,
  ),
  IrvioConnectionState.noNetwork => _Presentation(
    strings.text('noNetworkTitle'),
    strings.text('checkInternet'),
    irvioRed,
    Icons.signal_wifi_connected_no_internet_4_rounded,
  ),
  IrvioConnectionState.noExit => _Presentation(
    strings.text('noPeersTitle'),
    strings.text('noSuitablePeers'),
    irvioRed,
    Icons.priority_high_rounded,
  ),
  IrvioConnectionState.disconnecting => _Presentation(
    strings.text('disconnectingEllipsis'),
    strings.text('closingSecure'),
    irvioAmber,
    Icons.power_settings_new_rounded,
  ),
  IrvioConnectionState.error => _Presentation(
    strings.text('somethingWrong'),
    status.errorMessage ?? strings.text('operationFailed'),
    irvioRed,
    Icons.priority_high_rounded,
  ),
};

class _RingPainter extends CustomPainter {
  const _RingPainter({
    required this.color,
    required this.rotating,
    required this.phase,
  });

  final Color color;
  final bool rotating;
  final double phase;

  @override
  void paint(Canvas canvas, Size size) {
    final rect = Offset.zero & size;
    final center = rect.center;
    final radius = size.width / 2 - 8;
    canvas.drawCircle(
      center,
      radius,
      Paint()
        ..color = color.withValues(alpha: .12)
        ..style = PaintingStyle.stroke
        ..strokeWidth = 8,
    );
    final paint = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 8
      ..strokeCap = StrokeCap.round;
    canvas.drawArc(
      Rect.fromCircle(center: center, radius: radius),
      rotating ? phase * math.pi * 2 - math.pi / 2 : -math.pi / 2,
      rotating ? math.pi * 1.35 : math.pi * 2,
      false,
      paint,
    );
    canvas.drawCircle(
      center,
      radius - 17,
      Paint()
        ..shader = RadialGradient(
          colors: [color.withValues(alpha: .12), Colors.transparent],
        ).createShader(Rect.fromCircle(center: center, radius: radius)),
    );
  }

  @override
  bool shouldRepaint(covariant _RingPainter oldDelegate) =>
      oldDelegate.color != color ||
      oldDelegate.rotating != rotating ||
      oldDelegate.phase != phase;
}
