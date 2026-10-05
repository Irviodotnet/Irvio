// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/material.dart';

import '../app/irvio_app_controller.dart';
import '../l10n/irvio_localizations.dart';
import '../models/country.dart';
import '../models/irvio_models.dart';
import '../widgets/connection_control.dart';
import '../widgets/bootstrap_onboarding.dart';
import '../widgets/country_flag.dart';

class HomeScreen extends StatelessWidget {
  const HomeScreen({super.key, required this.controller});

  final IrvioAppController controller;

  @override
  Widget build(BuildContext context) {
    final status = controller.status;
    final bootstrap = controller.bootstrapState;
    if (controller.bootstrapAvailable &&
        (bootstrap?.requiresOnboarding == true ||
            bootstrap?.phase == IrvioBootstrapPhase.connecting) &&
        status.state != IrvioConnectionState.connected &&
        status.state != IrvioConnectionState.recovering) {
      return BootstrapOnboarding(controller: controller);
    }
    final countryLocked = switch (status.state) {
      IrvioConnectionState.disconnected ||
      IrvioConnectionState.noExit ||
      IrvioConnectionState.noNetwork ||
      IrvioConnectionState.error => false,
      _ => true,
    };
    return Padding(
      padding: const EdgeInsets.fromLTRB(28, 4, 28, 22),
      child: Column(
        children: [
          Expanded(
            child: SingleChildScrollView(
              child: Column(
                children: [
                  Text(context.l10n.text('chooseCountry')),
                  const SizedBox(height: 10),
                  _AvailableCountrySelector(
                    controller: controller,
                    locked: countryLocked,
                  ),
                  const SizedBox(height: 17),
                  ConnectionControl(
                    status: status,
                    enabled:
                        controller.daemonAvailable &&
                        !controller.commandPending,
                    disconnectIntent: controller.connectionIntentActive,
                    connectPending: controller.connectPending,
                    disconnectPending: controller.disconnectPending,
                    onConnect: controller.connect,
                    onDisconnect: controller.disconnect,
                  ),
                  if (!controller.daemonAvailable ||
                      controller.apiError != null) ...[
                    _ServiceMessage(controller: controller),
                    const SizedBox(height: 10),
                  ],
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _AvailableCountrySelector extends StatelessWidget {
  const _AvailableCountrySelector({
    required this.controller,
    required this.locked,
  });

  final IrvioAppController controller;
  final bool locked;

  @override
  Widget build(BuildContext context) {
    if (controller.availableCountriesLoading && !locked) {
      return _CountryAvailabilityMessage(
        key: const Key('country-loading'),
        icon: Icons.sync,
        message: context.l10n.text('loadingLocations'),
      );
    }
    final available = controller.availableCountries;
    final connectedCountry = controller.selectedCountry;
    final countries = <IrvioCountry>[
      if (locked &&
          !available.any((item) => item.code == connectedCountry.code))
        connectedCountry,
      ...available,
    ];
    if (countries.isEmpty) {
      if (controller.availableCountriesStatus ==
          IrvioAvailableCountriesStatus.empty) {
        return _CountryAvailabilityMessage(
          key: const Key('country-empty'),
          icon: Icons.public_off_outlined,
          message: context.l10n.text('noExitLocations'),
        );
      }
      return _CountryAvailabilityMessage(
        key: const Key('country-unavailable'),
        icon: Icons.cloud_off_outlined,
        message:
            controller.availableCountriesError ??
            context.l10n.text('unableLocations'),
        action: TextButton(
          key: const Key('country-retry'),
          onPressed: () => controller.refreshAvailableCountries(refresh: true),
          child: Text(context.l10n.text('retry')),
        ),
      );
    }
    return Column(
      children: [
        DropdownButtonFormField<String>(
          key: const Key('country-selector'),
          initialValue: connectedCountry.code,
          isExpanded: true,
          decoration: InputDecoration(
            filled: true,
            fillColor: Theme.of(context).cardColor,
            contentPadding: const EdgeInsets.symmetric(
              horizontal: 16,
              vertical: 12,
            ),
            border: OutlineInputBorder(
              borderRadius: BorderRadius.circular(12),
              borderSide: BorderSide(color: Theme.of(context).dividerColor),
            ),
          ),
          items: countries
              .map(
                (country) => DropdownMenuItem(
                  value: country.code,
                  child: Row(
                    children: [
                      CountryFlag(code: country.code),
                      const SizedBox(width: 11),
                      Expanded(
                        child: Text(
                          context.l10n.countryName(country.code),
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                    ],
                  ),
                ),
              )
              .toList(),
          onChanged: locked
              ? null
              : (code) {
                  if (code != null) {
                    controller.selectCountry(
                      countries.firstWhere((country) => country.code == code),
                    );
                  }
                },
        ),
        const SizedBox(height: 9),
        Text(
          locked
              ? context.l10n.text('connectedThrough', {
                  'country': context.l10n.countryName(connectedCountry.code),
                })
              : context.l10n.text('willConnectThrough', {
                  'country': context.l10n.countryName(connectedCountry.code),
                }),
          textAlign: TextAlign.center,
          style: Theme.of(context).textTheme.bodySmall,
        ),
      ],
    );
  }
}

class _CountryAvailabilityMessage extends StatelessWidget {
  const _CountryAvailabilityMessage({
    super.key,
    required this.icon,
    required this.message,
    this.action,
  });

  final IconData icon;
  final String message;
  final Widget? action;

  @override
  Widget build(BuildContext context) => Container(
    width: double.infinity,
    padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 13),
    decoration: BoxDecoration(
      color: Theme.of(context).cardColor,
      borderRadius: BorderRadius.circular(12),
      border: Border.all(color: Theme.of(context).dividerColor),
    ),
    child: Row(
      children: [
        Icon(icon, size: 19, color: Theme.of(context).colorScheme.primary),
        const SizedBox(width: 10),
        Expanded(child: Text(message)),
        ?action,
      ],
    ),
  );
}

class _ServiceMessage extends StatelessWidget {
  const _ServiceMessage({required this.controller});

  final IrvioAppController controller;

  @override
  Widget build(BuildContext context) {
    return Container(
      key: const Key('service-message'),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 9),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.error.withValues(alpha: .09),
        borderRadius: BorderRadius.circular(9),
        border: Border.all(
          color: Theme.of(context).colorScheme.error.withValues(alpha: .32),
        ),
      ),
      child: Row(
        children: [
          Icon(
            controller.daemonConnecting ? Icons.sync : Icons.info_outline,
            size: 17,
            color: Theme.of(context).colorScheme.error,
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              controller.daemonConnecting
                  ? context.l10n.text('connectingIrviod')
                  : controller.apiError ??
                        context.l10n.text('irviodUnavailable'),
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ),
          if (!controller.daemonConnecting)
            TextButton(
              onPressed: controller.reconnectNow,
              child: Text(context.l10n.text('retry')),
            ),
        ],
      ),
    );
  }
}
