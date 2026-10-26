import 'package:flutter/material.dart';

class AppTheme {
  const AppTheme._();

  static const Color seedColor = Color(0xFF487CE5);

  static const double maxContentWidth = 1040;

  static const double spacing = 8;

  static ThemeData dark() {
    final colorScheme = ColorScheme.fromSeed(
      seedColor: seedColor,
      brightness: Brightness.dark,
    );

    return ThemeData(
      useMaterial3: true,
      colorScheme: colorScheme,
      dividerTheme: DividerThemeData(color: colorScheme.outlineVariant),
    );
  }
}
