import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:ggcode_mobile/core/providers/chat_provider.dart';

// issue2814_ack_test.dart guards the late-ack fixes (#2811):
// 1. unconfirmed is terminal-UNTIL-ACKED - a late relay_ack/server_ack must
//    clear it (the rank gate used to drop it forever).
// 2. bindRemoteUserMessage renames user-X to the eventId, but acks echo the
//    ORIGINAL client id - the alias must resolve both acks and the timeout
//    closure.

void main() {
  test('#2811 P1: late ack clears unconfirmed (terminal-until-acked)', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final notifier = container.read(chatProvider.notifier);
    notifier.set([
      ChatMessage(
        id: 'user-1',
        isUser: true,
        kind: '',
        text: 'hi',
        time: DateTime.now(),
        status: MessageStatus.unconfirmed,
      ),
    ]);

    notifier.updateMessageStatus('user-1', MessageStatus.delivered);
    final after = container.read(chatProvider).first;
    expect(after.status, MessageStatus.delivered,
        reason: 'late relay_ack must clear unconfirmed (#2811)');

    notifier.updateMessageStatus('user-1', MessageStatus.acknowledged);
    expect(container.read(chatProvider).first.status, MessageStatus.acknowledged);
  });

  test('#2811 P1 guard: unconfirmed does NOT accept a downgrade to sending', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final notifier = container.read(chatProvider.notifier);
    notifier.set([
      ChatMessage(
        id: 'user-1',
        isUser: true,
        kind: '',
        text: 'hi',
        time: DateTime.now(),
        status: MessageStatus.unconfirmed,
      ),
    ]);

    notifier.updateMessageStatus('user-1', MessageStatus.sending);
    expect(container.read(chatProvider).first.status, MessageStatus.unconfirmed,
        reason: 'rank monotonicity must hold for non-ack statuses');
  });

  test('#2811 P2: ack referencing the pre-rename client id resolves via alias',
      () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final notifier = container.read(chatProvider.notifier);
    notifier.set([
      ChatMessage(
        id: 'user-9',
        isUser: true,
        kind: '',
        text: 'hello',
        time: DateTime.now(),
        status: MessageStatus.sending,
      ),
    ]);

    final bound =
        notifier.bindRemoteUserMessage('hello', remoteMessageId: 'evt-42');
    expect(bound, isTrue);
    expect(container.read(chatProvider).first.id, 'evt-42');

    // The relay echoes the client-sent id (user-9) - must still land.
    notifier.updateMessageStatus('user-9', MessageStatus.delivered);
    final msg = container.read(chatProvider).first;
    expect(msg.status, MessageStatus.delivered,
        reason: 'ack via pre-rename id must resolve (#2811)');
  });
}
