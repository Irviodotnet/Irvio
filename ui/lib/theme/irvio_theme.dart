// IRVIO Project
// Copyright (C) 2026 IRVIO Project contributors
// SPDX-License-Identifier: AGPL-3.0-only
// https://irvio.net
// irvio@proton.me

import 'package:flutter/material.dart';

const irvioBlue = Color(0xFF3D82FF);
const irvioConnectingBlue = Color(0xFF78B9CE);
const irvioGreen = Color(0xFF43DF6D);
const irvioAmber = Color(0xFFFFC43D);
const irvioRed = Color(0xFFFF516B);

ThemeData irvioDarkTheme() {
  const scheme = ColorScheme.dark(
    primary: irvioBlue,
    secondary: Color(0xFF67A1FF),
    surface: Color(0xFF111C29),
    error: irvioRed,
  );
  return ThemeData(
    useMaterial3: true,
    brightness: Brightness.dark,
    colorScheme: scheme,
    scaffoldBackgroundColor: const Color(0xFF08131F),
    fontFamily: 'sans',
    textTheme: const TextTheme(
      headlineSmall: TextStyle(fontWeight: FontWeight.w700, letterSpacing: .1),
      titleMedium: TextStyle(fontWeight: FontWeight.w600),
      bodyMedium: TextStyle(color: Color(0xFFD7E1ED)),
      bodySmall: TextStyle(color: Color(0xFF93A3B7)),
    ),
    dividerColor: const Color(0xFF273545),
    cardColor: const Color(0x9913212F),
    dropdownMenuTheme: const DropdownMenuThemeData(
      textStyle: TextStyle(color: Color(0xFFEAF1FA)),
    ),
  );
}

ThemeData irvioLightTheme() {
  final scheme = ColorScheme.fromSeed(
    seedColor: irvioBlue,
    brightness: Brightness.light,
  );
  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    scaffoldBackgroundColor: const Color(0xFFF0F5FA),
    cardColor: Colors.white,
    textTheme: const TextTheme(
      headlineSmall: TextStyle(fontWeight: FontWeight.w700),
      titleMedium: TextStyle(fontWeight: FontWeight.w600),
    ),
  );
}
