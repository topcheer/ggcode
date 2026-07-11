import 'dart:convert';
import 'dart:io';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

// ─── Language Provider ───────────────────────────────

const defaultLanguage = 'en';
const languagePreferenceKey = 'language_preference';
const supportedLanguages = [
  'en',
  'zh-CN',
  'zh-TW',
  'ja',
  'ko',
  'es',
  'fr',
  'de',
  'ru',
  'pt',
];

/// Map a raw locale string (e.g. "zh_CN", "pt-BR", "ja") to a supported language.
/// Falls back to [defaultLanguage] if no match.
String normalizeLanguage(String lang) {
  if (supportedLanguages.contains(lang)) return lang;
  // Convert underscores to hyphens ("zh_CN" → "zh-CN")
  final normalized = lang.replaceAll('_', '-');
  if (supportedLanguages.contains(normalized)) return normalized;
  // Try base language (e.g. "zh" → "zh-CN", "pt-BR" → "pt")
  if (normalized.contains('-')) {
    final base = normalized.split('-')[0];
    // Special handling for Chinese variants
    if (base == 'zh') {
      final region = normalized.split('-').last.toLowerCase();
      if (['tw', 'hk', 'mo', 'hant'].contains(region)) return 'zh-TW';
      return 'zh-CN';
    }
    for (final l in supportedLanguages) {
      if (l == base) return l;
    }
  } else if (normalized.length == 2) {
    for (final l in supportedLanguages) {
      if (l.startsWith('$normalized-')) return l;
    }
  }
  return defaultLanguage;
}

/// Detect system locale and normalize to a supported language.
String detectSystemLanguage() {
  try {
    return normalizeLanguage(Platform.localeName);
  } catch (_) {
    return defaultLanguage;
  }
}

class _LanguageNotifier extends Notifier<String> {
  @override
  String build() => defaultLanguage;

  void setLanguage(String lang) {
    final normalized = normalizeLanguage(lang);
    if (normalized != state) {
      state = normalized;
    }
  }
}

final languageProvider = NotifierProvider<_LanguageNotifier, String>(
  _LanguageNotifier.new,
);

// ─── Translations Loader ─────────────────────────────

Map<String, String> _translations = {};

Future<void> loadTranslations(String language) async {
  final normalized = normalizeLanguage(language);
  try {
    final path = 'assets/translations/$normalized.json';
    final data = await rootBundle.loadString(path);
    final Map<String, dynamic> decoded = jsonDecode(data);
    _translations = decoded.map((k, v) => MapEntry(k, v.toString()));
  } catch (_) {
    if (normalized != defaultLanguage) {
      await loadTranslations(defaultLanguage);
      return;
    }
    _translations = {};
  }
}

/// Translate a key with optional arguments.
/// Usage: t('chat.placeholder', args: {'status': 'working'})
String t(String key, {Map<String, String>? args}) {
  var value = _translations[key] ?? key;
  if (args != null) {
    args.forEach((k, v) {
      value = value.replaceAll('{$k}', v);
    });
  }
  return value;
}

// ─── Language Persistence ────────────────────────────

/// On startup, load saved language preference.
/// If none saved (or 'auto'), detect from OS locale.
/// Desktop sync always overrides via [applyLanguageFromDesktop].
Future<String> loadLanguagePreference(WidgetRef ref) async {
  final prefs = await SharedPreferences.getInstance();
  final stored = prefs.getString(languagePreferenceKey);

  String lang;
  if (stored != null && stored.isNotEmpty && stored != 'auto') {
    lang = stored;
  } else {
    lang = detectSystemLanguage();
  }

  final normalized = normalizeLanguage(lang);
  ref.read(languageProvider.notifier).setLanguage(normalized);
  await loadTranslations(normalized);
  return normalized;
}

/// User manually picked a language — persist and apply.
Future<void> persistLanguageChoice(WidgetRef ref, String lang) async {
  final normalized = normalizeLanguage(lang);
  ref.read(languageProvider.notifier).setLanguage(normalized);
  await loadTranslations(normalized);
  final prefs = await SharedPreferences.getInstance();
  await prefs.setString(languagePreferenceKey, normalized);
}
