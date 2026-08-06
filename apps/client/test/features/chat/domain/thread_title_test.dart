// titleFromPrompt —— thread 自动改名单测。

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/domain/thread_title.dart';

void main() {
  test('empty / blank prompt returns empty', () {
    expect(titleFromPrompt(''), '');
    expect(titleFromPrompt('   \n\n   '), '');
  });

  test('takes first non-blank line', () {
    expect(titleFromPrompt('\n\nhello world\nsecond line'), 'hello world');
  });

  test('strips markdown heading prefix', () {
    expect(titleFromPrompt('## 你好世界'), '你好世界');
    expect(titleFromPrompt('# Title'), 'Title');
  });

  test('strips quote / list prefix', () {
    expect(titleFromPrompt('> quoted'), 'quoted');
    expect(titleFromPrompt('- bullet'), 'bullet');
    expect(titleFromPrompt('* bullet'), 'bullet');
    expect(titleFromPrompt('1. numbered'), 'numbered');
  });

  test('truncates over maxChars + appends ellipsis', () {
    final long = 'a' * 50;
    final t = titleFromPrompt(long, maxChars: 30);
    expect(t.length, 31); // 30 + ellipsis
    expect(t.endsWith('…'), true);
  });

  test('短于 maxChars 不加省略号', () {
    expect(titleFromPrompt('短标题'), '短标题');
  });

  test('CJK 也按字符数截 (不按字节)', () {
    final s = '一二三四五六七八九十' * 4; // 40 CJK chars
    final t = titleFromPrompt(s, maxChars: 10);
    expect(t, '一二三四五六七八九十…');
  });

  test('多种前缀同时存在只剥一层', () {
    // # 是 heading 前缀；后续仍可能有空格。
    expect(titleFromPrompt('# 你好'), '你好');
  });
}
