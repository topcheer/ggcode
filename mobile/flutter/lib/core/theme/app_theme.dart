import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

// ─── Color Palette ───────────────────────────────────

class _Palette {
  final Color background;
  final Color backgroundElevated;
  final Color surface;
  final Color surfaceElevated;
  final Color surfaceMuted;
  final Color border;
  final Color borderStrong;
  final Color textPrimary;
  final Color textSecondary;
  final Color textMuted;
  final Color accent;
  final Color accentSoft;
  final Color success;
  final Color warning;
  final Color danger;

  const _Palette({
    required this.background,
    required this.backgroundElevated,
    required this.surface,
    required this.surfaceElevated,
    required this.surfaceMuted,
    required this.border,
    required this.borderStrong,
    required this.textPrimary,
    required this.textSecondary,
    required this.textMuted,
    required this.accent,
    required this.accentSoft,
    required this.success,
    required this.warning,
    required this.danger,
  });
}

// ─── Theme Palettes ──────────────────────────────────

const _palettes = <String, _Palette>{
  'midnight': _Palette(
    background: Color(0xFF0B1020),
    backgroundElevated: Color(0xFF121A2B),
    surface: Color(0xFF131C30),
    surfaceElevated: Color(0xFF1A2540),
    surfaceMuted: Color(0xFF22304F),
    border: Color(0xFF2A3655),
    borderStrong: Color(0xFF3A4A70),
    textPrimary: Color(0xFFF5F7FB),
    textSecondary: Color(0xFF9EABC7),
    textMuted: Color(0xFF7180A3),
    accent: Color(0xFF6EA8FF),
    accentSoft: Color(0xFF8A7CFF),
    success: Color(0xFF56D39B),
    warning: Color(0xFFF3B35C),
    danger: Color(0xFFFF6C7A),
  ),
  'oled': _Palette(
    background: Color(0xFF000000),
    backgroundElevated: Color(0xFF0A0A0A),
    surface: Color(0xFF0D0D0D),
    surfaceElevated: Color(0xFF141414),
    surfaceMuted: Color(0xFF1C1C1C),
    border: Color(0xFF282828),
    borderStrong: Color(0xFF3A3A3A),
    textPrimary: Color(0xFFF0F0F0),
    textSecondary: Color(0xFFAAAAAA),
    textMuted: Color(0xFF888888),
    accent: Color(0xFF64B4FF),
    accentSoft: Color(0xFF9E8AFF),
    success: Color(0xFF50DC8C),
    warning: Color(0xFFFFB932),
    danger: Color(0xFFFF5F5F),
  ),
  'nord': _Palette(
    background: Color(0xFF2E3440),
    backgroundElevated: Color(0xFF333A47),
    surface: Color(0xFF343B48),
    surfaceElevated: Color(0xFF3B4252),
    surfaceMuted: Color(0xFF434C5E),
    border: Color(0xFF434C5E),
    borderStrong: Color(0xFF4C566A),
    textPrimary: Color(0xFFECEFF4),
    textSecondary: Color(0xFFD8DEE9),
    textMuted: Color(0xFF808BA0),
    accent: Color(0xFF88C0D0),
    accentSoft: Color(0xFFB48EAD),
    success: Color(0xFFA3BE8C),
    warning: Color(0xFFEBCB8B),
    danger: Color(0xFFFF6E6E),
  ),
  'rose': _Palette(
    background: Color(0xFF190F14),
    backgroundElevated: Color(0xFF21151D),
    surface: Color(0xFF23141C),
    surfaceElevated: Color(0xFF2D1C26),
    surfaceMuted: Color(0xFF3A2432),
    border: Color(0xFF412834),
    borderStrong: Color(0xFF553548),
    textPrimary: Color(0xFFF8F0F3),
    textSecondary: Color(0xFFCCAAB8),
    textMuted: Color(0xFFA0788A),
    accent: Color(0xFFF48FB1),
    accentSoft: Color(0xFFCE93D8),
    success: Color(0xFF81C784),
    warning: Color(0xFFFFB74D),
    danger: Color(0xFFEF5350),
  ),
  'forest': _Palette(
    background: Color(0xFF0C140F),
    backgroundElevated: Color(0xFF111D15),
    surface: Color(0xFF101C14),
    surfaceElevated: Color(0xFF16261C),
    surfaceMuted: Color(0xFF1E3326),
    border: Color(0xFF233C2A),
    borderStrong: Color(0xFF2E5038),
    textPrimary: Color(0xFFEBF8F0),
    textSecondary: Color(0xFFA8C8AE),
    textMuted: Color(0xFF789A80),
    accent: Color(0xFF66D296),
    accentSoft: Color(0xFF81C784),
    success: Color(0xFF4CD282),
    warning: Color(0xFFFFBE50),
    danger: Color(0xFFEF6464),
  ),
  'light': _Palette(
    background: Color(0xFFFAFAFC),
    backgroundElevated: Color(0xFFF2F4F8),
    surface: Color(0xFFF2F4F8),
    surfaceElevated: Color(0xFFEAECF2),
    surfaceMuted: Color(0xFFE0E3EB),
    border: Color(0xFFD2D7E1),
    borderStrong: Color(0xFFB8BFCB),
    textPrimary: Color(0xFF1E232D),
    textSecondary: Color(0xFF5A6478),
    textMuted: Color(0xFF6E7686),
    accent: Color(0xFF3264C8),
    accentSoft: Color(0xFF6C5CE7),
    success: Color(0xFF14823B),
    warning: Color(0xFFB4820F),
    danger: Color(0xFFD23232),
  ),
};

final availableThemes = _palettes.keys.toList();
const _themePreferenceKey = 'theme_scheme';
const themeDisplayNames = <String, String>{
  'midnight': 'Midnight',
  'oled': 'OLED Black',
  'nord': 'Nord',
  'rose': 'Rose',
  'forest': 'Forest',
  'light': 'Light',
};

String normalizeThemeName(String name) =>
    _palettes.containsKey(name) ? name : 'midnight';

String displayThemeName(String name) =>
    themeDisplayNames[normalizeThemeName(name)] ?? 'Midnight';

// ─── Static Access (backward compatible) ─────────────

_Palette _current = _palettes['midnight']!;

/// Static color accessors — drop-in replacement for old AppColors.xxx.
/// Updated when theme changes via [setAppTheme].
class AppColors {
  static Color get background => _current.background;
  static Color get backgroundElevated => _current.backgroundElevated;
  static Color get surface => _current.surface;
  static Color get surfaceElevated => _current.surfaceElevated;
  static Color get surfaceMuted => _current.surfaceMuted;
  static Color get border => _current.border;
  static Color get borderStrong => _current.borderStrong;
  static Color get textPrimary => _current.textPrimary;
  static Color get textSecondary => _current.textSecondary;
  static Color get textMuted => _current.textMuted;
  static Color get accent => _current.accent;
  static Color get accentSoft => _current.accentSoft;
  static Color get success => _current.success;
  static Color get warning => _current.warning;
  static Color get danger => _current.danger;
}

// ─── Theme Provider ──────────────────────────────────

final themeProvider = NotifierProvider<_ThemeNotifier, String>(
  _ThemeNotifier.new,
);

class _ThemeNotifier extends Notifier<String> {
  @override
  String build() => 'midnight';

  void setTheme(String name) {
    final normalized = normalizeThemeName(name);
    if (state == normalized && identical(_current, _palettes[normalized]!)) {
      return;
    }
    state = normalized;
    _current = _palettes[normalized]!;
    unawaited(_persistTheme(normalized));
  }

  Future<void> loadThemePreference() async {
    final prefs = await SharedPreferences.getInstance();
    final stored = prefs.getString(_themePreferenceKey);
    if (stored != null && stored.isNotEmpty) {
      setTheme(stored);
    }
  }

  Future<void> _persistTheme(String name) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_themePreferenceKey, name);
  }
}

// ─── Radius & Shadows (theme-independent) ────────────

class AppRadii {
  static const double xs = 10;
  static const double sm = 14;
  static const double md = 18;
  static const double lg = 24;
}

class AppShadows {
  static const panel = [
    BoxShadow(
      color: Color(0x26000000),
      blurRadius: 24,
      offset: Offset(0, 10),
    ),
  ];
}

// ─── Build ThemeData from current palette ────────────

ThemeData buildAppTheme() {
  final p = _current;
  final isLight = p.background.computeLuminance() > 0.5;

  final colorScheme = ColorScheme(
    brightness: isLight ? Brightness.light : Brightness.dark,
    primary: p.accent,
    onPrimary: isLight ? const Color(0xFFFFFFFF) : const Color(0xFF0A1432),
    secondary: p.accentSoft,
    onSecondary: p.textPrimary,
    surface: p.surface,
    onSurface: p.textPrimary,
    error: p.danger,
    onError: p.textPrimary,
  );

  return ThemeData(
    useMaterial3: true,
    colorScheme: colorScheme,
    scaffoldBackgroundColor: p.background,
    dividerColor: p.border,
    dialogTheme: DialogThemeData(
      backgroundColor: p.surface,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.all(Radius.circular(AppRadii.md)),
      ),
    ),
    bottomSheetTheme: BottomSheetThemeData(
      backgroundColor: p.surface,
      modalBackgroundColor: p.surface,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(AppRadii.lg)),
      ),
    ),
    cardTheme: CardThemeData(
      color: p.surface,
      elevation: 0,
      shape: RoundedRectangleBorder(
        borderRadius: const BorderRadius.all(Radius.circular(AppRadii.md)),
        side: BorderSide(color: p.border),
      ),
    ),
    appBarTheme: AppBarTheme(
      backgroundColor: p.background,
      surfaceTintColor: Colors.transparent,
      elevation: 0,
      scrolledUnderElevation: 0,
      iconTheme: IconThemeData(color: p.textPrimary),
      titleTextStyle: TextStyle(
        color: p.textPrimary,
        fontSize: 18,
        fontWeight: FontWeight.w600,
      ),
    ),
    textTheme: TextTheme(
      bodyMedium: TextStyle(
        color: p.textPrimary,
        fontSize: 14,
        height: 1.45,
      ),
      bodySmall: TextStyle(
        color: p.textSecondary,
        fontSize: 12,
      ),
      titleMedium: TextStyle(
        color: p.textPrimary,
        fontSize: 16,
        fontWeight: FontWeight.w600,
      ),
    ),
  );
}
