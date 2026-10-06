import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

// zz_issue2847_test.dart - probe for #2847 (source-level assertions chosen so
// the fix's explanatory comments cannot satisfy them):
// 1. _send's _pendingImages.clear() must be wrapped in setState - the bare
//    clear relied on a watched provider changing in the same frame to rebuild
//    the thumbnail row.
// 2. _pickImage must guard mounted after EVERY await - setState after
//    dispose threw when the user backed out mid-pick/read.
void main() {
  test('issue 2847: send clears pending images inside setState', () {
    final src = File('lib/features/chat/input_bar.dart').readAsStringSync();
    expect(
      src.contains('setState(() {\n      _pendingImages.clear();\n    });'),
      isTrue,
      reason: '#2847 _pendingImages.clear() must be wrapped in setState',
    );
  });

  test('issue 2847: pickImage guards mounted after awaits', () {
    final src = File('lib/features/chat/input_bar.dart').readAsStringSync();
    final pickStart = src.indexOf('Future<void> _pickImage(');
    expect(pickStart, greaterThanOrEqualTo(0));
    final body = src.substring(pickStart);
    final guards = 'if (!mounted) return;'.allMatches(body).length;
    expect(guards, greaterThanOrEqualTo(2),
        reason: '#2847 need a mounted guard per await in _pickImage');
  });
}
