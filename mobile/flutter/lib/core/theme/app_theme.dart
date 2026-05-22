import 'package:flutter/material.dart';

class AppColors {
  static const background = Color(0xFF0B1020);
  static const backgroundElevated = Color(0xFF121A2B);
  static const surface = Color(0xFF131C30);
  static const surfaceElevated = Color(0xFF1A2540);
  static const surfaceMuted = Color(0xFF22304F);
  static const border = Color(0xFF2A3655);
  static const borderStrong = Color(0xFF3A4A70);
  static const textPrimary = Color(0xFFF5F7FB);
  static const textSecondary = Color(0xFF9EABC7);
  static const textMuted = Color(0xFF7180A3);
  static const accent = Color(0xFF6EA8FF);
  static const accentSoft = Color(0xFF8A7CFF);
  static const success = Color(0xFF56D39B);
  static const warning = Color(0xFFF3B35C);
  static const danger = Color(0xFFFF6C7A);
}

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

ThemeData buildAppTheme() {
  const colorScheme = ColorScheme.dark(
    primary: AppColors.accent,
    secondary: AppColors.accentSoft,
    surface: AppColors.surface,
    onSurface: AppColors.textPrimary,
    error: AppColors.danger,
  );

  return ThemeData(
    useMaterial3: true,
    colorScheme: colorScheme,
    scaffoldBackgroundColor: AppColors.background,
    dividerColor: AppColors.border,
    dialogTheme: const DialogThemeData(
      backgroundColor: AppColors.surface,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.all(Radius.circular(AppRadii.md)),
      ),
    ),
    bottomSheetTheme: const BottomSheetThemeData(
      backgroundColor: AppColors.surface,
      modalBackgroundColor: AppColors.surface,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(AppRadii.lg)),
      ),
    ),
    cardTheme: const CardThemeData(
      color: AppColors.surface,
      elevation: 0,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.all(Radius.circular(AppRadii.md)),
        side: BorderSide(color: AppColors.border),
      ),
    ),
    appBarTheme: const AppBarTheme(
      backgroundColor: AppColors.background,
      surfaceTintColor: Colors.transparent,
      elevation: 0,
      scrolledUnderElevation: 0,
      iconTheme: IconThemeData(color: AppColors.textPrimary),
      titleTextStyle: TextStyle(
        color: AppColors.textPrimary,
        fontSize: 18,
        fontWeight: FontWeight.w600,
      ),
    ),
    textTheme: const TextTheme(
      bodyMedium: TextStyle(
        color: AppColors.textPrimary,
        fontSize: 14,
        height: 1.45,
      ),
      bodySmall: TextStyle(
        color: AppColors.textSecondary,
        fontSize: 12,
      ),
      titleMedium: TextStyle(
        color: AppColors.textPrimary,
        fontSize: 16,
        fontWeight: FontWeight.w600,
      ),
    ),
  );
}
