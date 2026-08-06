// dev_apps_provider — pure-Dart parsing tests.
//
// Network roundtrip is exercised by the manual test (start a real
// `biu app run --dev` and observe the apps page). Here we only pin
// the JSON parser — regressions in field naming would silently empty
// the dev list.

import 'package:biumind/data/dev_apps_provider.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('parses a complete DevApp payload', () {
    final a = DevApp.fromJson({
      'slug': 'rss',
      'identifier': 'rss',
      'title': 'RSS',
      'version': '0.1.0',
      'manifest': {
        'name': 'rss',
        'views': [
          {'id': 'home', 'route': '/apps/rss', 'layout': 'list'}
        ],
      },
      'source_path': '/Users/me/projects/rss',
      'mock': false,
    });
    expect(a.slug, 'rss');
    expect(a.identifier, 'rss');
    expect(a.title, 'RSS');
    expect(a.version, '0.1.0');
    expect(a.sourcePath, '/Users/me/projects/rss');
    expect(a.mock, false);
    final views = a.manifest['views'] as List;
    expect(views.length, 1);
  });

  test('mock=true round-trips', () {
    final a = DevApp.fromJson({'slug': 'x', 'mock': true});
    expect(a.mock, true);
  });

  test('empty / mistyped fields → safe defaults', () {
    final a = DevApp.fromJson({});
    expect(a.slug, '');
    expect(a.identifier, '');
    expect(a.version, '');
    expect(a.manifest, isEmpty);
    expect(a.mock, false);
  });
}
