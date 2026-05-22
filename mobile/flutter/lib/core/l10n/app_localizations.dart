import 'dart:convert';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

// ─── Language Provider ───────────────────────────────

const defaultLanguage = 'en';
const supportedLanguages = ['en', 'zh-CN'];

class _LanguageNotifier extends Notifier<String> {
  @override
  String build() => defaultLanguage;

  void setLanguage(String lang) {
    final normalized = supportedLanguages.contains(lang) ? lang : defaultLanguage;
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
  try {
    final path = 'assets/translations/$language.json';
    final data = await rootBundle.loadString(path);
    final Map<String, dynamic> decoded = jsonDecode(data);
    _translations = decoded.map((k, v) => MapEntry(k, v.toString()));
  } catch (_) {
    // Fallback to empty if file not found
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
