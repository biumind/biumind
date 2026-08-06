// DraftHistoryNotifier —— P0-6 composer 历史栈单测。

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:biumind/features/chat/application/draft_history_controller.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('push dedups consecutive duplicates', () async {
    final n = DraftHistoryNotifier();
    await Future.delayed(Duration.zero);
    n.push('hello');
    n.push('hello');
    expect(n.state.history, ['hello']);
  });

  test('push moves an existing entry to the top', () async {
    final n = DraftHistoryNotifier();
    await Future.delayed(Duration.zero);
    n.push('a');
    n.push('b');
    n.push('a');
    expect(n.state.history, ['a', 'b']);
  });

  test('history capped at 20', () async {
    final n = DraftHistoryNotifier();
    await Future.delayed(Duration.zero);
    for (var i = 0; i < 25; i++) {
      n.push('p$i');
    }
    expect(n.state.history.length, 20);
    expect(n.state.history.first, 'p24');
  });

  test('prev cycles older entries; cap at oldest', () async {
    final n = DraftHistoryNotifier();
    await Future.delayed(Duration.zero);
    n.push('a');
    n.push('b');
    n.push('c'); // [c, b, a]
    expect(n.prev(), 'c');
    expect(n.prev(), 'b');
    expect(n.prev(), 'a');
    expect(n.prev(), isNull);
    expect(n.state.cursor, 2);
  });

  test('next walks forward; reaching most-recent then exit clears', () async {
    final n = DraftHistoryNotifier();
    await Future.delayed(Duration.zero);
    n.push('a');
    n.push('b'); // [b, a]
    n.prev(); // 'b'
    n.prev(); // 'a'
    expect(n.next(), 'b');
    expect(n.next(), '');
    expect(n.state.cursor, isNull);
  });

  test('resetCursor stops browsing without changing history', () async {
    final n = DraftHistoryNotifier();
    await Future.delayed(Duration.zero);
    n.push('a');
    n.prev();
    expect(n.state.cursor, 0);
    n.resetCursor();
    expect(n.state.cursor, isNull);
    expect(n.state.history, ['a']);
  });

  test('empty / whitespace push is no-op', () async {
    final n = DraftHistoryNotifier();
    await Future.delayed(Duration.zero);
    n.push('');
    n.push('   ');
    expect(n.state.history, isEmpty);
  });
}
