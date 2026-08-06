// Sanity tests on PlatformCaps. These run against the test runner's
// platform (always desktop in flutter_test), so we mostly verify field
// shape + that detection doesn't throw.

import 'package:biumind/core/platform/platform_caps.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('detect returns a valid struct on host platform', () {
    final caps = PlatformCaps.detect();
    // Whatever platform the test runs on, sqlite is available because
    // flutter_test always targets the host.
    expect(caps.hasPersistentSqlite, isTrue);
    expect(caps.toString(), contains('PlatformCaps('));
  });

  test('explicit web caps refuse PTY + persistent sqlite', () {
    const web = PlatformCaps(
      hasLocalPty: false,
      hasFileSystem: false,
      hasNotifications: false,
      supportsBackgroundIsolates: true,
      hasPersistentSqlite: false,
    );
    expect(web.hasLocalPty, isFalse);
    expect(web.hasPersistentSqlite, isFalse);
    // Background isolates are still supported (web workers).
    expect(web.supportsBackgroundIsolates, isTrue);
  });
}
