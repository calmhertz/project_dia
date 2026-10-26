import 'package:flutter/material.dart';

/// Material 3 theme for Aagasa.
///
/// Dark mode only in V1 (client-spec section 3). The seed lives here alone so
/// it can be changed in one place.
class AppTheme {
  const AppTheme._();

  /// Material 3 colour seed for V1.
  static const Color seedColor = Color(0xFF487CE5);

  /// Content wider than this is hard to read, so large windows centre it.
  static const double maxContentWidth = 1040;

  /// MD3 spacing step.
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
