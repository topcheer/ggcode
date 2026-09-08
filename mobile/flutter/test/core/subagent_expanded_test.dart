import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/providers/ui_providers.dart';

/// #1874 case 1: the expanded set lives in the provider layer so the
/// connection provider's 5s completed-card cleanup can defer removal
/// while the user is reading the card. Pins the toggle semantics the
/// panel and the cleanup both rely on.
void main() {
  test('toggle adds then removes an agent id', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(subagentExpandedProvider.notifier);
    expect(container.read(subagentExpandedProvider), isEmpty);

    notifier.toggle('agent-1');
    expect(container.read(subagentExpandedProvider), {'agent-1'});

    notifier.toggle('agent-2');
    expect(container.read(subagentExpandedProvider), {'agent-1', 'agent-2'});

    notifier.toggle('agent-1');
    expect(container.read(subagentExpandedProvider), {'agent-2'});
  });

  test('toggle produces new set instances (no in-place mutation)', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(subagentExpandedProvider.notifier);
    notifier.toggle('a');
    final first = container.read(subagentExpandedProvider);
    notifier.toggle('a');
    final second = container.read(subagentExpandedProvider);
    expect(identical(first, second), isFalse);
    expect(second, isEmpty);
  });

  test('remove drops a stale entry without touching others (panel filter '
      'keeps a completed agent visible while expanded)', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(subagentExpandedProvider.notifier);
    notifier.toggle('a');
    notifier.toggle('b');

    // The panel visibility predicate: a completed agent stays visible only
    // while its id is in the expanded set.
    bool visible(String id) => container.read(subagentExpandedProvider).contains(id);
    expect(visible('a'), isTrue);

    notifier.remove('a');
    expect(container.read(subagentExpandedProvider), {'b'});
    expect(visible('a'), isFalse);
  });
}
