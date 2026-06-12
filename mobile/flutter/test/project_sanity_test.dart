import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

void main() {
  test('pubspec pins the tested Dart SDK floor', () {
    final pubspec = File('pubspec.yaml').readAsStringSync();
    expect(pubspec, contains("sdk: '>=3.4.0 <4.0.0'"));
  });

  test('android source tree keeps only the active MainActivity package path',
      () {
    final active = File(
      'android/app/src/main/kotlin/gg/ai/ggcode/mobile/MainActivity.kt',
    );
    final stale = File(
      'android/app/src/main/kotlin/gg/ai/ggcode/ggcode_mobile/MainActivity.kt',
    );

    expect(active.existsSync(), isTrue);
    expect(stale.existsSync(), isFalse);
  });
}
