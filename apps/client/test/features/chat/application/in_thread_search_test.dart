// InThreadSearchController —— Cmd+F 线程内搜索状态机单测。

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/application/in_thread_search_controller.dart';
import 'package:biumind/features/chat/domain/chat_models.dart';

Message _msg({
  required String id,
  required MessageRole role,
  required String text,
  int seq = 0,
}) {
  return Message(
    id: id,
    threadId: 't1',
    role: role,
    status: MessageStatus.completed,
    seq: seq,
    createdAt: DateTime.utc(2026, 1, 1).add(Duration(seconds: seq)),
    blocks: [
      TextBlock(
        id: '$id-b0',
        index: 0,
        state: BlockState.closed,
        text: text,
      ),
    ],
  );
}

void main() {
  group('computeHits', () {
    test('empty query returns no hits', () {
      final msgs = [
        _msg(id: '1', role: MessageRole.user, text: 'hello world'),
      ];
      expect(computeHits(msgs, ''), isEmpty);
      expect(computeHits(msgs, '   '), isEmpty);
    });

    test('matches case-insensitive substring across messages', () {
      final msgs = [
        _msg(id: '1', role: MessageRole.user, text: 'Hello world'),
        _msg(id: '2', role: MessageRole.assistant, text: 'world peace'),
        _msg(id: '3', role: MessageRole.user, text: 'no match here'),
      ];
      expect(computeHits(msgs, 'world'), ['1', '2']);
      expect(computeHits(msgs, 'HELLO'), ['1']);
      expect(computeHits(msgs, 'xyz'), isEmpty);
    });

    test('skips toolResult role', () {
      final msgs = [
        _msg(id: '1', role: MessageRole.toolResult, text: 'world'),
        _msg(id: '2', role: MessageRole.assistant, text: 'world'),
      ];
      expect(computeHits(msgs, 'world'), ['2']);
    });
  });

  group('InThreadSearchNotifier', () {
    test('open/close toggle reset state', () {
      final n = InThreadSearchNotifier();
      n.open();
      expect(n.state.open, true);
      n.close();
      expect(n.state.open, false);
      expect(n.state.query, '');
      expect(n.state.hits, isEmpty);
    });

    test('setQuery computes hits + currentIndex starts at 0', () {
      final n = InThreadSearchNotifier();
      n.setMessages([
        _msg(id: 'a', role: MessageRole.user, text: 'foo'),
        _msg(id: 'b', role: MessageRole.assistant, text: 'foo bar'),
      ]);
      n.setQuery('foo');
      expect(n.state.hits, ['a', 'b']);
      expect(n.state.currentIndex, 0);
      expect(n.state.currentMessageId, 'a');
    });

    test('next/prev wraps around', () {
      final n = InThreadSearchNotifier();
      n.setMessages([
        _msg(id: 'a', role: MessageRole.user, text: 'foo'),
        _msg(id: 'b', role: MessageRole.assistant, text: 'foo'),
        _msg(id: 'c', role: MessageRole.user, text: 'foo'),
      ]);
      n.setQuery('foo');
      n.next();
      expect(n.state.currentIndex, 1);
      n.next();
      expect(n.state.currentIndex, 2);
      n.next();
      expect(n.state.currentIndex, 0);
      n.prev();
      expect(n.state.currentIndex, 2);
    });

    test('setMessages while query active preserves currentIndex if id still hits', () {
      final n = InThreadSearchNotifier();
      n.setMessages([
        _msg(id: 'a', role: MessageRole.user, text: 'foo'),
        _msg(id: 'b', role: MessageRole.user, text: 'foo'),
      ]);
      n.setQuery('foo');
      n.next(); // currentIndex=1, currentMessageId='b'
      n.setMessages([
        _msg(id: 'a', role: MessageRole.user, text: 'foo'),
        _msg(id: 'b', role: MessageRole.user, text: 'foo'),
        _msg(id: 'c', role: MessageRole.user, text: 'foo new'),
      ]);
      // 'b' 还在 hits 里 → currentIndex 还指 'b'
      expect(n.state.currentMessageId, 'b');
    });

    test('messageHasHit reflects current hit set', () {
      final n = InThreadSearchNotifier();
      n.setMessages([
        _msg(id: 'a', role: MessageRole.user, text: 'foo'),
        _msg(id: 'b', role: MessageRole.user, text: 'bar'),
      ]);
      n.setQuery('foo');
      expect(n.state.messageHasHit('a'), true);
      expect(n.state.messageHasHit('b'), false);
    });
  });
}
