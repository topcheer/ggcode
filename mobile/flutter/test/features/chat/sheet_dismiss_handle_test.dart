import 'package:flutter/material.dart';
import 'package:flutter/semantics.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/features/chat/sheet_dismiss_handle.dart';

Widget _host({required VoidCallback onDismiss}) => MaterialApp(
      home: Scaffold(
        body: SheetDismissHandle(onDismiss: onDismiss),
      ),
    );

void main() {
  testWidgets('tapping the handle bar dismisses the sheet', (tester) async {
    var dismissed = 0;
    await tester.pumpWidget(_host(onDismiss: () => dismissed++));

    await tester.tap(find.byType(SheetDismissHandle));
    await tester.pump();

    expect(dismissed, 1);
  });

  testWidgets('exposes a Dismiss button with a tap action to assistive tech',
      (tester) async {
    var dismissed = 0;
    await tester.pumpWidget(_host(onDismiss: () => dismissed++));

    final node = tester.getSemantics(find.byType(SheetDismissHandle));
    final data = node.getSemanticsData();
    expect(data.label, 'Dismiss');
    expect(node.flagsCollection.isButton, isTrue);
    expect(data.hasAction(SemanticsAction.tap), isTrue);

    // What UI automation (idb/XCUITest "tap element") performs on the node.
    tester.semantics.tap(find.semantics.byLabel('Dismiss'));
    await tester.pump();

    expect(dismissed, 1);
  });

  testWidgets('hit area spans the sheet width at a comfortable height',
      (tester) async {
    await tester.pumpWidget(_host(onDismiss: () {}));

    final rect = tester.getRect(find.byType(SheetDismissHandle));
    expect(rect.width, 800); // default test surface width
    expect(rect.height, greaterThanOrEqualTo(44));
  });
}
