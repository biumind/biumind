import 'package:biumind/features/wiki/presentation/frontmatter/frontmatter_helpers.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('stringFieldValue', () {
    test('passes through strings', () {
      expect(stringFieldValue('hello'), 'hello');
    });
    test('coerces numbers', () {
      expect(stringFieldValue(42), '42');
      expect(stringFieldValue(3.14), '3.14');
    });
    test('coerces bool', () {
      expect(stringFieldValue(true), 'true');
    });
    test('lists / null become empty', () {
      expect(stringFieldValue(null), '');
      expect(stringFieldValue(<String>[]), '');
      expect(stringFieldValue(<String>['a', 'b']), '');
    });
  });

  group('listFieldValue', () {
    test('list of strings', () {
      expect(listFieldValue(['a', 'b', 'c']), ['a', 'b', 'c']);
    });
    test('trims and drops empties', () {
      expect(listFieldValue([' a ', '', 'b', null]), ['a', 'b']);
    });
    test('single string becomes 1-element list', () {
      expect(listFieldValue('only'), ['only']);
    });
    test('null / map becomes empty', () {
      expect(listFieldValue(null), <String>[]);
      expect(listFieldValue(<String, String>{'k': 'v'}), <String>[]);
    });
  });

  group('setField', () {
    test('sets non-empty value', () {
      final fm = setField({'a': 1}, 'b', 'hi');
      expect(fm, {'a': 1, 'b': 'hi'});
    });
    test('removes key when value is empty string', () {
      final fm = setField({'a': 1, 'b': 'hi'}, 'b', '');
      expect(fm, {'a': 1});
    });
    test('removes key when value is null', () {
      final fm = setField({'a': 1, 'b': 'hi'}, 'b', null);
      expect(fm, {'a': 1});
    });
    test('removes key when value is empty list', () {
      final fm = setField({'a': 1, 'b': []}, 'b', []);
      expect(fm, {'a': 1});
    });
    test('does not mutate input', () {
      final input = {'a': 1};
      setField(input, 'b', 'hi');
      expect(input, {'a': 1});
    });
  });

  group('addToListField', () {
    test('appends to existing list', () {
      final fm = addToListField({
        'tags': ['a']
      }, 'tags', 'b');
      expect(fm['tags'], ['a', 'b']);
    });
    test('creates list when key absent', () {
      final fm = addToListField({}, 'tags', 'a');
      expect(fm['tags'], ['a']);
    });
    test('case-insensitive dedupe (first casing wins)', () {
      final fm = addToListField({
        'tags': ['Foo']
      }, 'tags', 'foo');
      expect(fm['tags'], ['Foo']);
    });
    test('trims whitespace', () {
      final fm = addToListField({}, 'tags', '  hello  ');
      expect(fm['tags'], ['hello']);
    });
    test('blank input returns map unchanged', () {
      final input = {
        'tags': ['a']
      };
      // Trim-empty new value is a no-op — we return the SAME map ref.
      expect(identical(addToListField(input, 'tags', '   '), input), isTrue);
    });
  });

  group('removeFromListField', () {
    test('removes by index', () {
      final fm = removeFromListField({
        'tags': ['a', 'b', 'c']
      }, 'tags', 1);
      expect(fm['tags'], ['a', 'c']);
    });
    test('out-of-range no-op', () {
      final input = {
        'tags': ['a']
      };
      final fm = removeFromListField(input, 'tags', 5);
      expect(fm, input);
    });
    test('removing last entry drops the key', () {
      final fm = removeFromListField({
        'tags': ['only']
      }, 'tags', 0);
      expect(fm.containsKey('tags'), isFalse);
    });
  });

  group('renameKey', () {
    test('renames preserving value', () {
      final fm = renameKey({'old': 'v'}, 'old', 'new');
      expect(fm, {'new': 'v'});
    });
    test('preserves order', () {
      final fm = renameKey({'a': 1, 'old': 'v', 'b': 2}, 'old', 'new');
      expect(fm.keys.toList(), ['a', 'new', 'b']);
    });
    test('refuses on collision', () {
      final input = {'a': 1, 'b': 2};
      final fm = renameKey(input, 'a', 'b');
      expect(fm, input); // unchanged
    });
    test('refuses blank new key', () {
      final input = {'a': 1};
      expect(renameKey(input, 'a', '   '), input);
      expect(renameKey(input, 'a', ''), input);
    });
    test('no-op when oldKey missing', () {
      final input = {'a': 1};
      expect(renameKey(input, 'missing', 'b'), input);
    });
  });

  group('isDirty', () {
    test('detects added key', () {
      expect(isDirty({'a': 1}, {'a': 1, 'b': 2}), isTrue);
    });
    test('detects removed key', () {
      expect(isDirty({'a': 1, 'b': 2}, {'a': 1}), isTrue);
    });
    test('detects value change', () {
      expect(isDirty({'a': 1}, {'a': 2}), isTrue);
    });
    test('returns false on identical maps', () {
      expect(isDirty({'a': 1, 'b': 'hi'}, {'a': 1, 'b': 'hi'}), isFalse);
    });
    test('list element-by-element comparison', () {
      expect(isDirty({
        'tags': ['a', 'b']
      }, {
        'tags': ['a', 'b']
      }), isFalse);
      expect(isDirty({
        'tags': ['a', 'b']
      }, {
        'tags': ['a', 'c']
      }), isTrue);
    });
  });

  group('unwrapWikilink', () {
    test('plain target', () {
      expect(unwrapWikilinkLabel('[[Page]]'), 'Page');
      expect(unwrapWikilinkSlug('[[Page]]'), 'Page');
    });
    test('with alias: label and slug differ', () {
      expect(unwrapWikilinkLabel('[[slug|Display]]'), 'Display');
      expect(unwrapWikilinkSlug('[[slug|Display]]'), 'slug');
    });
    test('non-wikilink passes through', () {
      expect(unwrapWikilinkLabel('plain string'), 'plain string');
      expect(unwrapWikilinkSlug('plain string'), 'plain string');
    });
    test('handles whitespace', () {
      expect(unwrapWikilinkLabel('  [[Page]]  '), 'Page');
    });
  });
}
