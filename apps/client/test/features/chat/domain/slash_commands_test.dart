// SlashCommands —— composer 斜杠命令解析单测。

import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/domain/slash_commands.dart';

void main() {
  group('parseSlash', () {
    test('returns null for non-slash text', () {
      expect(parseSlash(''), isNull);
      expect(parseSlash('hello'), isNull);
      expect(parseSlash(' /new'), isNull);
    });

    test('parses bare slash as empty name', () {
      final p = parseSlash('/');
      expect(p, isNotNull);
      expect(p!.name, '');
      expect(p.args, isEmpty);
    });

    test('parses command name without args', () {
      final p = parseSlash('/new');
      expect(p!.name, 'new');
      expect(p.args, isEmpty);
    });

    test('parses command with args', () {
      final p = parseSlash('/branch from msg-3');
      expect(p!.name, 'branch');
      expect(p.args, ['from', 'msg-3']);
    });
  });

  group('filterSlashCommands', () {
    test('empty prefix returns all', () {
      expect(filterSlashCommands('').length, kSlashCommands.length);
    });

    test('matches by id prefix case-insensitive', () {
      expect(filterSlashCommands('n').map((c) => c.id), ['new', 'note']);
      expect(filterSlashCommands('NE').map((c) => c.id), ['new']);
      expect(filterSlashCommands('not').map((c) => c.id), ['note']);
      expect(filterSlashCommands('h').map((c) => c.id), ['help']);
    });

    test('returns empty when no match', () {
      expect(filterSlashCommands('xyz'), isEmpty);
    });
  });
}
