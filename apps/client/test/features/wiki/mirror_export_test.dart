import 'package:biumind/features/wiki/presentation/mirror/mirror_export.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('exportPageMarkdown', () {
    test('body_md 字面保留 [[wikilink]]（不做 wiki:// 重写）', () {
      final md = exportPageMarkdown(
        title: 'Alpha',
        id: 'p1',
        updatedAt: DateTime.utc(2026, 1, 2, 3, 4, 5),
        frontmatter: const {},
        bodyMd: '参见 [[Beta]] 和 [[Gamma|别名]]。',
      );
      expect(md, contains('[[Beta]]'));
      expect(md, contains('[[Gamma|别名]]'));
      expect(md, isNot(contains('wiki://')));
    });

    test('frontmatter 全字段透出（type/tags/related/自定义键）', () {
      final md = exportPageMarkdown(
        title: 'Alpha',
        id: 'p1',
        updatedAt: DateTime.utc(2026, 1, 2, 3, 4, 5),
        frontmatter: const {
          'type': 'entity',
          'tags': ['ai', 'moe'],
          'related': ['[[Beta]]'],
          'origin': 'deep-research',
        },
        bodyMd: 'body',
      );
      expect(md, startsWith('---\n'));
      expect(md, contains('id: "p1"'));
      expect(md, contains('title: "Alpha"'));
      expect(md, contains('updated_at: "2026-01-02T03:04:05.000Z"'));
      expect(md, contains('type: "entity"'));
      expect(md, contains('tags: ["ai", "moe"]'));
      expect(md, contains('related: ["[[Beta]]"]'));
      expect(md, contains('origin: "deep-research"'));
      expect(md, contains('---\n\nbody'));
    });

    test('frontmatter 里的 title/id/updated_at 不覆盖权威值', () {
      final md = exportPageMarkdown(
        title: 'Real Title',
        id: 'p1',
        updatedAt: DateTime.utc(2026, 1, 2),
        frontmatter: const {'title': 'Stale', 'id': 'other'},
        bodyMd: '',
      );
      expect(md, contains('title: "Real Title"'));
      expect(md, isNot(contains('Stale')));
    });

    test('字符串转义双引号与反斜杠', () {
      final md = exportPageMarkdown(
        title: 'say "hi" \\ ok',
        id: 'p1',
        updatedAt: DateTime.utc(2026, 1, 2),
        frontmatter: const {},
        bodyMd: '',
      );
      expect(md, contains(r'title: "say \"hi\" \\ ok"'));
    });

    test('嵌套 map 降级为 inline JSON', () {
      final md = exportPageMarkdown(
        title: 't',
        id: 'p1',
        updatedAt: DateTime.utc(2026, 1, 2),
        frontmatter: const {
          'parse_meta': {'parser': 'docproc', 'page_count': 3},
        },
        bodyMd: '',
      );
      expect(md, contains('parse_meta: {"parser":"docproc","page_count":3}'));
    });

    test('null 值跳过', () {
      final md = exportPageMarkdown(
        title: 't',
        id: 'p1',
        updatedAt: DateTime.utc(2026, 1, 2),
        frontmatter: const {'type': null, 'tags': <String>[]},
        bodyMd: '',
      );
      expect(md, isNot(contains('type:')));
      expect(md, contains('tags: []'));
    });
  });

  group('frontmatterToYaml', () {
    test('首尾 --- 包裹，末尾换行', () {
      final yaml = frontmatterToYaml(const {'a': 1, 'b': true});
      expect(yaml, '---\na: 1\nb: true\n---\n');
    });
  });

  group('safeExportFilename', () {
    test('禁字符替换为 -', () {
      expect(safeExportFilename('a/b\\c:d*e?f"g<h>i|j'), 'a-b-c-d-e-f-g-h-i-j');
    });

    test('空白折叠 + trim', () {
      expect(safeExportFilename('  hello   world  '), 'hello world');
    });

    test('空 → untitled', () {
      expect(safeExportFilename(''), 'untitled');
      expect(safeExportFilename('   '), 'untitled');
    });
  });
}
