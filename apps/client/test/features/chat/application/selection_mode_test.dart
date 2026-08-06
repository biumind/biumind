// SelectionModeNotifier —— P0-3 多选状态机单测。

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/application/selection_mode_controller.dart';

void main() {
  group('SelectionModeNotifier', () {
    test('starts inactive with empty selection', () {
      final n = SelectionModeNotifier();
      expect(n.state.active, false);
      expect(n.state.threadId, isNull);
      expect(n.state.ids, isEmpty);
    });

    test('enter activates and pins thread id', () {
      final n = SelectionModeNotifier();
      n.enter('t1');
      expect(n.state.active, true);
      expect(n.state.threadId, 't1');
    });

    test('toggle adds then removes id', () {
      final n = SelectionModeNotifier();
      n.enter('t1');
      n.toggle('m1');
      expect(n.state.contains('m1'), true);
      n.toggle('m1');
      expect(n.state.contains('m1'), false);
    });

    test('selectAll replaces selection', () {
      final n = SelectionModeNotifier();
      n.enter('t1');
      n.toggle('a');
      n.selectAll(['x', 'y', 'z']);
      expect(n.state.ids, {'x', 'y', 'z'});
    });

    test('exit clears active + selection', () {
      final n = SelectionModeNotifier();
      n.enter('t1');
      n.toggle('m1');
      n.exit();
      expect(n.state.active, false);
      expect(n.state.threadId, isNull);
      expect(n.state.ids, isEmpty);
    });

    test('onThreadChanged with new thread resets', () {
      final n = SelectionModeNotifier();
      n.enter('t1');
      n.toggle('m1');
      n.onThreadChanged('t2');
      expect(n.state.active, false);
      expect(n.state.ids, isEmpty);
    });

    test('onThreadChanged to same thread is no-op', () {
      final n = SelectionModeNotifier();
      n.enter('t1');
      n.toggle('m1');
      n.onThreadChanged('t1');
      expect(n.state.active, true);
      expect(n.state.contains('m1'), true);
    });

    test('count reflects ids size', () {
      final n = SelectionModeNotifier();
      n.enter('t1');
      n.toggle('a');
      n.toggle('b');
      expect(n.state.count, 2);
    });
  });
}
