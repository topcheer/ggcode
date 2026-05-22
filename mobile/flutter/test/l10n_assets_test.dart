import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/l10n/app_localizations.dart';

void main() {
  test('translation assets stay valid and free of duplicate keys', () {
    for (final locale in ['en', 'zh-CN']) {
      final file = File('assets/translations/$locale.json');
      final content = file.readAsStringSync();

      expect(() => jsonDecode(content), returnsNormally);

      final matches =
          RegExp(r'^\s*"([^"]+)":', multiLine: true).allMatches(content);
      final seen = <String>{};
      final duplicates = <String>{};
      for (final match in matches) {
        final key = match.group(1)!;
        if (!seen.add(key)) {
          duplicates.add(key);
        }
      }

      expect(duplicates, isEmpty,
          reason: '$locale has duplicate translation keys');
    }
  });

  test('empty language input normalizes to english', () {
    expect(normalizeLanguage(''), defaultLanguage);
    expect(normalizeLanguage('unexpected'), defaultLanguage);
  });
}
