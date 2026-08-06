// mermaid_url 编码 round-trip 验证。
//
// 编码出 pako URL 后, 用 archive 反向 inflate + base64url decode + json
// parse, 应能拿回原 source。模拟 mermaid.ink / mermaid.live 服务端解码
// 流程, 一次性测出"我编码的格式服务端能否解开"。

import 'dart:convert';
import 'package:archive/archive.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/wiki/presentation/mermaid/mermaid_url.dart';

Map<String, dynamic> _decodePakoUrl(String url) {
  // 提 pako: 之后部分
  final m = RegExp(r'pako:([A-Za-z0-9\-_]+)').firstMatch(url);
  if (m == null) {
    throw StateError('URL has no pako prefix: $url');
  }
  var encoded = m.group(1)!;
  // 补 padding 给 base64Decode 用
  while (encoded.length % 4 != 0) {
    encoded += '=';
  }
  final compressed = base64Url.decode(encoded);
  final json = utf8.decode(ZLibDecoder().decodeBytes(compressed));
  return jsonDecode(json) as Map<String, dynamic>;
}

void main() {
  group('isMermaidCode', () {
    test('explicit lang=mermaid', () {
      expect(isMermaidCode(lang: 'mermaid'), isTrue);
      expect(isMermaidCode(lang: 'MERMAID'), isTrue);
    });

    test('graph keyword', () {
      expect(isMermaidCode(text: 'graph TD\nA-->B'), isTrue);
      expect(isMermaidCode(text: '\n\ngraph LR\n  A --> B'), isTrue);
    });

    test('flowchart keyword', () {
      expect(isMermaidCode(text: 'flowchart TD\n  start --> stop'), isTrue);
    });

    test('sequenceDiagram', () {
      expect(isMermaidCode(text: 'sequenceDiagram\n A->>B: hi'), isTrue);
    });

    test('rejects non-mermaid code', () {
      expect(isMermaidCode(text: 'def main():\n    pass'), isFalse);
      expect(isMermaidCode(text: 'console.log("hi")'), isFalse);
      expect(isMermaidCode(text: 'graphql query'), isFalse);
    });

    test('handles empty / null', () {
      expect(isMermaidCode(), isFalse);
      expect(isMermaidCode(text: ''), isFalse);
      expect(isMermaidCode(text: '\n\n  '), isFalse);
    });
  });

  group('mermaidImageUrl pako encoding', () {
    test('简单 graph round-trip', () {
      final url = mermaidImageUrl('graph TD\nA-->B');
      expect(url, startsWith('https://mermaid.ink/img/pako:'));
      final state = _decodePakoUrl(url);
      expect(state['code'], 'graph TD\nA-->B');
      // mermaid 字段必须是 STRING (mermaid.ink 会再 JSON.parse 一次)
      expect(state['mermaid'], isA<String>());
      final config = jsonDecode(state['mermaid'] as String);
      expect(config, {'theme': 'default'});
    });

    test('CJK content round-trip', () {
      const src = 'sequenceDiagram\n    participant 客户端\n    participant 服务器\n'
          '    客户端->>服务器: GET /user/123';
      final url = mermaidImageUrl(src);
      final state = _decodePakoUrl(url);
      expect(state['code'], src);
    });

    test('classDiagram with newlines / special chars', () {
      const src = 'classDiagram\n'
          '    class Animal {\n'
          '        +String name\n'
          '        +int age\n'
          '        +eat()\n'
          '    }\n'
          '    Animal <|-- Dog';
      final url = mermaidImageUrl(src);
      final state = _decodePakoUrl(url);
      expect(state['code'], src);
    });

    test('endpoint 自定义 (Kroki self-host)', () {
      final url = mermaidImageUrl('graph LR\nA-->B',
          endpoint: 'https://kroki.example.com');
      expect(url, startsWith('https://kroki.example.com/img/pako:'));
      expect(_decodePakoUrl(url)['code'], 'graph LR\nA-->B');
    });

    test('endpoint 尾部 / 处理', () {
      final url = mermaidImageUrl('graph TD',
          endpoint: 'https://example.com/');
      expect(url, startsWith('https://example.com/img/pako:'));
      expect(url, isNot(contains('//img')));
    });

    test('format=svg', () {
      final url = mermaidImageUrl('graph TD\nA-->B', format: 'svg');
      expect(url, contains('/svg/pako:'));
    });

    test('base64url 字符集 (- 和 _, 没 + 或 /)', () {
      // 用一个会产生很多特殊字符的 source
      final url = mermaidImageUrl('graph TD\nA-->B\n' * 20);
      final m = RegExp(r'pako:([A-Za-z0-9\-_]+)$').firstMatch(url);
      expect(m, isNotNull, reason: 'URL 字符集不该有 +/');
      expect(url.contains('+'), isFalse);
      expect(url.contains('/', url.indexOf('pako:')), isFalse);
    });
  });

  group('mermaidLiveEditUrl', () {
    test('生成 /edit#pako:<...> 不含 base64: 前缀', () {
      final url = mermaidLiveEditUrl('graph TD\nA-->B');
      expect(url, startsWith('https://mermaid.live/edit#pako:'));
      expect(url, isNot(contains('base64:')));
      // 同样 round-trip 解出来
      final state = _decodePakoUrl(url);
      expect(state['code'], 'graph TD\nA-->B');
    });

    test('CJK content 也能解开', () {
      const src = 'sequenceDiagram\n    participant 用户\n    用户->>服务器: hi';
      final url = mermaidLiveEditUrl(src);
      expect(_decodePakoUrl(url)['code'], src);
    });
  });
}
