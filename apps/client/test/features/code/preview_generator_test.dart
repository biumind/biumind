// PreviewGenerator behavioural tests.
//
// 三类:
//   pure helpers (sizeSummary / diffStatSummary / firstParagraph)
//   image preview (临时目录 + image 包构造 PNG)
//   document preview (临时目录写 .md)
//
// 不测 codeFile diff 路径 — 那条要 git init + commit 的真实 worktree,
// 留给集成测试 (服务端 / 端到端)。

import 'dart:convert' show base64Decode;
import 'dart:io';
import 'dart:typed_data';

import 'package:biumind/features/code/domain/artifact.dart';
import 'package:biumind/features/code/workspace/preview_generator.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:image/image.dart' as img;
import 'package:path/path.dart' as p;

void main() {
  // ─── pure helpers ─────────────────────────────────────

  group('sizeSummary', () {
    Artifact mk(int n) => Artifact(
          id: 'a',
          taskId: 't',
          kind: ArtifactKind.binary,
          relPath: 'x',
          sizeBytes: n,
          sha256: 's',
          op: ArtifactOp.created,
          createdAt: DateTime.utc(2026, 1, 1),
        );

    test('byte / KB / MB / GB tiers', () {
      expect(PreviewGenerator.sizeSummary(mk(0)), '0 B');
      expect(PreviewGenerator.sizeSummary(mk(512)), '512 B');
      expect(PreviewGenerator.sizeSummary(mk(2048)), '2.0 KB');
      expect(PreviewGenerator.sizeSummary(mk(2 * 1024 * 1024)), '2.0 MB');
      expect(PreviewGenerator.sizeSummary(mk(3 * 1024 * 1024 * 1024)),
          '3.00 GB');
    });
  });

  group('diffStatSummary', () {
    test('counts +/- lines but ignores diff headers', () {
      const diff = '''
diff --git a/x.dart b/x.dart
index abc..def 100644
--- a/x.dart
+++ b/x.dart
@@ -1,3 +1,4 @@
 unchanged
-removed
+added one
+added two
 still
''';
      // +++/--- header lines should NOT count; @@ also not.
      expect(PreviewGenerator.diffStatSummary(diff), '+2 -1');
    });

    test('empty diff returns "diff" placeholder', () {
      expect(PreviewGenerator.diffStatSummary(''), 'diff');
      expect(PreviewGenerator.diffStatSummary('  \n\n'), 'diff');
    });

    test('only additions / only deletions', () {
      expect(
        PreviewGenerator.diffStatSummary('+a\n+b\n+c\n'),
        '+3 -0',
      );
      expect(
        PreviewGenerator.diffStatSummary('-a\n-b\n'),
        '+0 -2',
      );
    });
  });

  group('firstParagraph', () {
    test('extracts up to first \\n\\n', () {
      const txt = 'first paragraph here.\n\nsecond paragraph.';
      expect(PreviewGenerator.firstParagraph(txt), 'first paragraph here.');
    });

    test('trims leading whitespace', () {
      expect(PreviewGenerator.firstParagraph('   hello'), 'hello');
    });

    test('truncates with ellipsis when over maxChars', () {
      final long = 'x' * 600;
      final got = PreviewGenerator.firstParagraph(long, maxChars: 500);
      expect(got.length, lessThanOrEqualTo(501));
      expect(got.endsWith('…'), isTrue);
    });

    test('returns whole input when no double newline + within budget', () {
      expect(PreviewGenerator.firstParagraph('one line only'), 'one line only');
    });
  });

  // ─── image preview ────────────────────────────────────

  group('image preview', () {
    late Directory tmp;
    setUp(() async {
      tmp = await Directory.systemTemp.createTemp('biumind-preview-img-');
    });
    tearDown(() async {
      if (await tmp.exists()) await tmp.delete(recursive: true);
    });

    Artifact mkImage(String relPath, int size) => Artifact(
          id: 'a',
          taskId: 't',
          kind: ArtifactKind.image,
          relPath: relPath,
          mimeType: 'image/png',
          sizeBytes: size,
          sha256: 's',
          op: ArtifactOp.created,
          createdAt: DateTime.utc(2026, 1, 1),
        );

    test('decodes + thumbnails a real PNG, returns base64 jpeg ≤ budget',
        () async {
      // 构造一个 800×600 渐变 PNG, 写到临时目录
      final src = img.Image(width: 800, height: 600);
      img.fill(src, color: img.ColorRgb8(120, 200, 80));
      final pngBytes = img.encodePng(src);
      final f = File(p.join(tmp.path, 'pic.png'));
      await f.writeAsBytes(pngBytes);

      final gen = PreviewGenerator(worktreePath: tmp.path, thumbSize: 256);
      final b = await gen.generate(mkImage('pic.png', pngBytes.length));

      expect(b.dataB64, isNotNull, reason: 'should produce thumbnail');
      expect(b.mimeType, 'image/jpeg');
      expect(b.summary, contains('800×600'));

      // 反解 jpeg, 验证缩放后尺寸 + 体积合规
      final thumb = img.decodeJpg(Uint8List.fromList(base64Decode(b.dataB64!)));
      expect(thumb, isNotNull);
      expect(thumb!.width, lessThanOrEqualTo(256));
      expect(thumb.height, lessThanOrEqualTo(256));
      // 长边 = 256 (横图 width 较大 → width = 256)
      expect(thumb.width, 256);
    });

    test('preserves aspect for portrait (height > width)', () async {
      final src = img.Image(width: 300, height: 900);
      img.fill(src, color: img.ColorRgb8(80, 80, 200));
      final pngBytes = img.encodePng(src);
      final f = File(p.join(tmp.path, 'tall.png'));
      await f.writeAsBytes(pngBytes);

      final gen = PreviewGenerator(worktreePath: tmp.path, thumbSize: 256);
      final b = await gen.generate(mkImage('tall.png', pngBytes.length));
      expect(b.dataB64, isNotNull);
      final thumb = img.decodeJpg(Uint8List.fromList(base64Decode(b.dataB64!)));
      // portrait: 长边 height = 256, width 等比缩放
      expect(thumb!.height, 256);
      expect(thumb.width, lessThan(256));
    });

    test('returns size-only summary when file missing', () async {
      final gen = PreviewGenerator(worktreePath: tmp.path);
      final b = await gen.generate(mkImage('missing.png', 12345));
      expect(b.dataB64, isNull);
    });

    test('skips decode for huge images (>32MB), gives size summary only',
        () async {
      // 不实际写 32MB+ 文件 — 用 sizeBytes 字段触发 short-circuit。
      // 文件本身是 1×1 的小 PNG, 读不到也无所谓 (size 检查在前面)。
      final src = img.Image(width: 1, height: 1);
      final pngBytes = img.encodePng(src);
      final f = File(p.join(tmp.path, 'huge.png'));
      await f.writeAsBytes(pngBytes);

      final gen = PreviewGenerator(worktreePath: tmp.path);
      final b = await gen.generate(mkImage('huge.png', 64 * 1024 * 1024));
      expect(b.dataB64, isNull);
      expect(b.summary, contains('MB'));
    });
  });

  // ─── document preview ─────────────────────────────────

  group('document preview', () {
    late Directory tmp;
    setUp(() async {
      tmp = await Directory.systemTemp.createTemp('biumind-preview-doc-');
    });
    tearDown(() async {
      if (await tmp.exists()) await tmp.delete(recursive: true);
    });

    Artifact mkDoc(String rel, String mime, int size) => Artifact(
          id: 'a',
          taskId: 't',
          kind: ArtifactKind.document,
          relPath: rel,
          mimeType: mime,
          sizeBytes: size,
          sha256: 's',
          op: ArtifactOp.created,
          createdAt: DateTime.utc(2026, 1, 1),
        );

    test('markdown: 第一段 + 字数 summary', () async {
      // _firstParagraph 拿第一个 \n\n 之前的所有文本作为 "第一段" — markdown
      // 标题跟正文之间有空行时, 第一段是 标题; 这里测纯正文场景。
      const content = 'First paragraph body.\nstill first.\n\n'
          'Second paragraph here.\n';
      final f = File(p.join(tmp.path, 'doc.md'));
      await f.writeAsBytes(content.codeUnits);

      final gen = PreviewGenerator(worktreePath: tmp.path);
      final b = await gen.generate(
          mkDoc('doc.md', 'text/markdown', content.length));
      expect(b.summary, contains('词'));
      expect(b.dataB64, isNotNull);
      // dataB64 反解后只含第一段, 不含第二段
      final body = String.fromCharCodes(base64Decode(b.dataB64!));
      expect(body, contains('First paragraph'));
      expect(body, contains('still first'));
      expect(body, isNot(contains('Second paragraph')));
    });

    test('non-text mime (e.g. application/pdf) → size summary, no dataB64',
        () async {
      final f = File(p.join(tmp.path, 'a.pdf'));
      await f.writeAsBytes([0x25, 0x50, 0x44, 0x46]); // %PDF magic
      final gen = PreviewGenerator(worktreePath: tmp.path);
      final b = await gen.generate(mkDoc('a.pdf', 'application/pdf', 4));
      expect(b.dataB64, isNull, reason: 'PDF parsing not in P2.B scope');
      expect(b.summary, isNotNull);
    });
  });

  // ─── sensitive short-circuit ──────────────────────────

  test('sensitive file: generate() returns "(敏感, preview 已跳过)" no data',
      () async {
    final tmp = await Directory.systemTemp.createTemp('biumind-preview-');
    try {
      final f = File(p.join(tmp.path, '.env'));
      await f.writeAsString('SECRET=topsecret\n');
      final gen = PreviewGenerator(worktreePath: tmp.path);
      final b = await gen.generate(Artifact(
        id: 'a',
        taskId: 't',
        kind: ArtifactKind.document,
        relPath: '.env',
        mimeType: 'text/plain',
        sizeBytes: 16,
        sha256: 's',
        op: ArtifactOp.created,
        createdAt: DateTime.utc(2026, 1, 1),
      ));
      expect(b.summary, contains('敏感'));
      expect(b.dataB64, isNull);
    } finally {
      await tmp.delete(recursive: true);
    }
  });

  // ─── audio/video/dataset/binary ───────────────────────

  test('non-preview kinds (audio/video/dataset/binary) → size summary only',
      () async {
    final tmp = await Directory.systemTemp.createTemp('biumind-preview-');
    try {
      final gen = PreviewGenerator(worktreePath: tmp.path);
      for (final k in [
        ArtifactKind.audio,
        ArtifactKind.video,
        ArtifactKind.dataset,
        ArtifactKind.binary,
      ]) {
        final b = await gen.generate(Artifact(
          id: 'a',
          taskId: 't',
          kind: k,
          relPath: 'f',
          sizeBytes: 12345,
          sha256: 's',
          op: ArtifactOp.created,
          createdAt: DateTime.utc(2026, 1, 1),
        ));
        expect(b.dataB64, isNull, reason: '$k should not produce dataB64');
        expect(b.summary, isNotNull, reason: '$k should have summary');
      }
    } finally {
      await tmp.delete(recursive: true);
    }
  });
}
