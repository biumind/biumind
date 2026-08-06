// MessageOutline —— markdown heading 提取单测。

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/domain/message_outline.dart';

void main() {
  test('empty / no headings returns empty', () {
    expect(parseOutline(''), isEmpty);
    expect(parseOutline('plain text without heading'), isEmpty);
  });

  test('< 3 headings → returns empty (avoid noise)', () {
    expect(
      parseOutline('# only one heading\n\nbody'),
      isEmpty,
    );
    expect(
      parseOutline('# one\n## two\nbody'),
      isEmpty,
    );
  });

  test('extracts 3+ headings with correct levels', () {
    final out = parseOutline('# Intro\n## Background\n### Detail\n## Wrap');
    expect(out.length, 4);
    expect(out[0].level, 1);
    expect(out[0].title, 'Intro');
    expect(out[1].level, 2);
    expect(out[2].level, 3);
    expect(out[3].title, 'Wrap');
  });

  test('skips fenced code blocks (no false positives)', () {
    const text = '''
# Real

```
# fake one
## fake two
```

## Real two

### Real three
''';
    final out = parseOutline(text);
    expect(out.length, 3);
    expect(out.map((o) => o.title), ['Real', 'Real two', 'Real three']);
  });

  test('ignores leading whitespace before #', () {
    // markdown 严格说 heading 必须行首；indented `# foo` 不是 heading。
    final out = parseOutline('# A\n# B\n   # not heading\n# C');
    expect(out.map((o) => o.title), ['A', 'B', 'C']);
  });

  test('ignores 7+ pound signs (max H6)', () {
    expect(
      parseOutline('# A\n####### too many\n## B\n### C'),
      hasLength(3),
    );
  });
}
