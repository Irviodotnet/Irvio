// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

class IrvioCountry {
  const IrvioCountry(this.code, this.name, this.flag);

  final String code;
  final String name;
  final String flag;
}

// Country metadata is presentation-only; Local API alone supplies selectable availability.
const irvioCountries = <IrvioCountry>[
  IrvioCountry('DE', 'Germany', '🇩🇪'),
  IrvioCountry('NL', 'Netherlands', '🇳🇱'),
  IrvioCountry('RU', 'Russia', '🇷🇺'),
  IrvioCountry('FR', 'France', '🇫🇷'),
  IrvioCountry('GB', 'United Kingdom', '🇬🇧'),
  IrvioCountry('US', 'United States', '🇺🇸'),
  IrvioCountry('CA', 'Canada', '🇨🇦'),
];

IrvioCountry countryForCode(String code) => irvioCountries.firstWhere(
  (country) => country.code == code.toUpperCase(),
  orElse: () => IrvioCountry(code.toUpperCase(), code.toUpperCase(), '🌐'),
);
