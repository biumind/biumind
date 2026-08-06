// NoteAttachmentResolver 单元测试 —— biu-file:// ↔ 临时 URL 双向重写。
//
// 锁的语义：
//   * 正则提取：只认 uuid，去重且保持出现顺序；
//   * resolveForEditor：换取临时 URL 并全文替换，单文件失败保留原 URI；
//   * toCanonical：按显式映射换回 biu-file://，不依赖 URL 里出现 uuid；
//   * 同一 fileId 重新登记后旧 URL 不再反解（防止过期 URL 换回）。

import 'package:biumind/features/notes/data/note_attachment_resolver.dart';
import 'package:flutter_test/flutter_test.dart';

const _idA = '11111111-2222-3333-4444-555555555555';
const _idB = 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee';
const _idC = 'cccccccc-1111-2222-3333-dddddddddddd';

/// 临时 URL 故意不含 uuid —— 反解必须靠映射，不能靠正则从 URL 反推。
String _fakeUrl(String fileId) =>
    'https://minio.example/bucket/obj-${fileId.substring(0, 4)}'
    '?X-Amz-Signature=sig${fileId.substring(0, 8)}&X-Amz-Expires=900';

NoteAttachmentResolver _resolver({Set<String> failing = const {}}) {
  return NoteAttachmentResolver(
    presignGet: (fileId) async {
      if (failing.contains(fileId)) throw Exception('presign boom');
      return _fakeUrl(fileId);
    },
  );
}

void main() {
  group('extractFileIds', () {
    test('提取并按出现顺序去重', () {
      final md = '![a](biu-file://$_idA)\n'
          '![b](biu-file://$_idB)\n'
          '![a2](biu-file://$_idA)';
      expect(_resolver().extractFileIds(md), [_idA, _idB]);
    });

    test('忽略非 uuid 与普通 http 图片', () {
      final md = '![x](biu-file://not-a-uuid)\n'
          '![y](https://example.com/pic.png)\n'
          'biu-file:// 后面没有 id';
      expect(_resolver().extractFileIds(md), isEmpty);
    });

    test('大写十六进制 uuid 也能匹配', () {
      const upper = 'AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE';
      expect(
        _resolver().extractFileIds('![u](biu-file://$upper)'),
        [upper],
      );
    });
  });

  group('resolveForEditor', () {
    test('biu-file 全部替换为临时 URL（同一文件多处引用都换）', () async {
      final md = '开头\n![a](biu-file://$_idA)\n中间\n'
          '![a-again](biu-file://$_idA)\n![b](biu-file://$_idB)\n结尾';
      final out = await _resolver().resolveForEditor(md);
      expect(out, isNot(contains('biu-file://')));
      expect(out, contains('![a](${_fakeUrl(_idA)})'));
      expect(out, contains('![a-again](${_fakeUrl(_idA)})'));
      expect(out, contains('![b](${_fakeUrl(_idB)})'));
    });

    test('无附件正文原样返回，不发起换取', () async {
      var calls = 0;
      final r = NoteAttachmentResolver(presignGet: (_) async {
        calls++;
        return 'x';
      });
      const md = '# 纯文本\n没有图片';
      expect(await r.resolveForEditor(md), md);
      expect(calls, 0);
    });

    test('单个文件换取失败时保留原 biu-file URI，其余照常替换', () async {
      final md = '![a](biu-file://$_idA)\n![b](biu-file://$_idB)';
      final out = await _resolver(failing: {_idA}).resolveForEditor(md);
      expect(out, contains('![a](biu-file://$_idA)'));
      expect(out, contains('![b](${_fakeUrl(_idB)})'));
    });

    test('多个附件并行换取：同时在飞，总耗时 ≈ 单次而非累加', () async {
      var inFlight = 0;
      var maxInFlight = 0;
      final r = NoteAttachmentResolver(presignGet: (fileId) async {
        inFlight++;
        maxInFlight = inFlight > maxInFlight ? inFlight : maxInFlight;
        await Future<void>.delayed(const Duration(milliseconds: 50));
        inFlight--;
        return _fakeUrl(fileId);
      });
      const md = '![a](biu-file://$_idA)\n'
          '![b](biu-file://$_idB)\n'
          '![c](biu-file://$_idC)';
      final sw = Stopwatch()..start();
      final out = await r.resolveForEditor(md);
      sw.stop();
      // 串行版三个请求依次 await：同时在飞最多 1 个、总耗时 ≥150ms。
      expect(maxInFlight, 3);
      expect(sw.elapsedMilliseconds, lessThan(150));
      expect(out, isNot(contains('biu-file://')));
      // 并行换取后映射登记齐全，可整体反解回规范 URI。
      expect(r.toCanonical(out), md);
    });

    test('并行换取中部分失败：成功的替换并登记，失败的保留原 URI', () async {
      var inFlight = 0;
      var maxInFlight = 0;
      final r = NoteAttachmentResolver(presignGet: (fileId) async {
        inFlight++;
        maxInFlight = inFlight > maxInFlight ? inFlight : maxInFlight;
        await Future<void>.delayed(const Duration(milliseconds: 20));
        inFlight--;
        if (fileId == _idB) throw Exception('presign boom');
        return _fakeUrl(fileId);
      });
      const md = '![a](biu-file://$_idA)\n'
          '![b](biu-file://$_idB)\n'
          '![c](biu-file://$_idC)';
      final out = await r.resolveForEditor(md);
      expect(maxInFlight, 3);
      expect(out, contains('![a](${_fakeUrl(_idA)})'));
      expect(out, contains('![b](biu-file://$_idB)'));
      expect(out, contains('![c](${_fakeUrl(_idC)})'));
      // 失败 id 未登记映射，toCanonical 原样保留；成功的正常反解。
      expect(r.toCanonical(out), md);
    });
  });

  group('toCanonical', () {
    test('往返：resolve → 编辑 → 换回 biu-file', () async {
      final r = _resolver();
      const original = '![a](biu-file://$_idA)\n![b](biu-file://$_idB)';
      final forEditor = await r.resolveForEditor(original);
      // 模拟用户在编辑器里加了文字、删了 b 图。
      final edited = '$forEditor\n新加一行'.replaceAll(
        '![b](${_fakeUrl(_idB)})',
        '',
      );
      final canonical = r.toCanonical(edited);
      expect(canonical, '![a](biu-file://$_idA)\n\n新加一行');
      expect(canonical, isNot(contains('X-Amz-Signature')));
    });

    test('未登记过的 URL 原样保留', () async {
      final r = _resolver();
      const md = '![ext](https://minio.example/bucket/obj-zzzz?sig=1)';
      expect(r.toCanonical(md), md);
    });

    test('空映射时是恒等转换', () {
      const md = '![a](biu-file://$_idA)';
      expect(_resolver().toCanonical(md), md);
    });
  });

  group('registerMapping / resolveOne', () {
    test('resolveOne 登记映射，toCanonical 可反解', () async {
      final r = _resolver();
      final url = await r.resolveOne(_idA);
      expect(url, _fakeUrl(_idA));
      expect(r.toCanonical('![new]($url)'), '![new](biu-file://$_idA)');
    });

    test('同一 fileId 重新换取后旧 URL 不再反解', () async {
      var n = 0;
      final r = NoteAttachmentResolver(presignGet: (_) async {
        n++;
        return 'https://minio.example/obj?v=$n&sig=abc';
      });
      final oldUrl = await r.resolveOne(_idA);
      final newUrl = await r.resolveOne(_idA);
      expect(oldUrl, isNot(newUrl));
      // 旧 URL 已过期，映射作废 → 原样保留；新 URL 正常反解。
      expect(r.toCanonical('![x]($oldUrl)'), '![x]($oldUrl)');
      expect(r.toCanonical('![x]($newUrl)'), '![x](biu-file://$_idA)');
    });
  });

  group('非图片附件链接', () {
    test('extractFileIds 识别普通链接里的 biu-file', () {
      final md = '[设计稿.pdf](biu-file://$_idA)\n[官网](https://example.com)';
      expect(_resolver().extractFileIds(md), [_idA]);
    });

    test('普通链接 resolve → toCanonical 往返', () async {
      final r = _resolver();
      const original =
          '见附件 [设计稿.pdf](biu-file://$_idA) 和 [规格](biu-file://$_idB)';
      final forEditor = await r.resolveForEditor(original);
      expect(
        forEditor,
        '见附件 [设计稿.pdf](${_fakeUrl(_idA)}) 和 [规格](${_fakeUrl(_idB)})',
      );
      expect(r.toCanonical(forEditor), original);
    });

    test('图片与链接混合都能 resolve / 回写', () async {
      final r = _resolver();
      const original = '![图](biu-file://$_idA)\n[附件](biu-file://$_idB)';
      final forEditor = await r.resolveForEditor(original);
      expect(
        forEditor,
        '![图](${_fakeUrl(_idA)})\n[附件](${_fakeUrl(_idB)})',
      );
      expect(r.toCanonical(forEditor), original);
    });

    test('非 biu-file 链接不受影响', () async {
      final r = _resolver();
      const md = '[官网](https://example.com) ![图](https://example.com/a.png)';
      expect(await r.resolveForEditor(md), md);
      expect(r.toCanonical(md), md);
    });
  });
}
