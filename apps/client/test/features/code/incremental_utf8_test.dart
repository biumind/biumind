// IncrementalUtf8Decoder 单测 —— PTY 字节流跨块切断多字节字符不乱码。

import 'dart:convert';
import 'dart:typed_data';

import 'package:biumind/features/code/data/incremental_utf8.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

Uint8List bytes(String s) => Uint8List.fromList(utf8.encode(s));

void main() {
  test('ASCII passes through whole', () {
    final d = IncrementalUtf8Decoder();
    expect(d.decode(bytes('hello')), 'hello');
  });

  test('multibyte char split across two chunks reassembles', () {
    final d = IncrementalUtf8Decoder();
    final full = bytes('中'); // 3 bytes: E4 B8 AD
    // 切在第 1 字节后:首块只有 lead → 应 hold,返回空
    final first = d.decode(Uint8List.sublistView(full, 0, 1));
    expect(first, '');
    // 余下 2 字节到达 → 拼齐输出 '中'
    final rest = d.decode(Uint8List.sublistView(full, 1));
    expect(rest, '中');
  });

  test('emoji (4-byte) split across chunks', () {
    final d = IncrementalUtf8Decoder();
    final full = bytes('😀'); // F0 9F 98 80
    expect(d.decode(Uint8List.sublistView(full, 0, 2)), ''); // 不完整
    expect(d.decode(Uint8List.sublistView(full, 2)), '😀'); // 拼齐
  });

  test('ASCII tail after complete multibyte flushes immediately', () {
    final d = IncrementalUtf8Decoder();
    final data = bytes('a中b');
    // 一次给全:应完整输出
    expect(d.decode(data), 'a中b');
  });

  test('mixed stream across many chunks preserves order', () {
    final d = IncrementalUtf8Decoder();
    final full = bytes('你好world日本語');
    final out = StringBuffer();
    // 每 1 字节喂一次 —— 极端分块
    for (var i = 0; i < full.length; i++) {
      out.write(d.decode(Uint8List.sublistView(full, i, i + 1)));
    }
    expect(out.toString(), '你好world日本語');
  });

  test('validPrefixLen excludes trailing incomplete lead', () {
    // 'A' + lead byte of 中 (E4) → valid prefix = 1 (just 'A')
    final b = Uint8List.fromList([0x41, 0xE4]);
    expect(IncrementalUtf8Decoder.validPrefixLen(b), 1);
  });
}
