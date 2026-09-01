// DocprocPreferences（设置 > 通用 > 文档处理，P2 W3）单测：
//   * SharedPreferences `biu.wiki.docproc` 读写 + 跨实例恢复 + 坏数据回默认；
//   * 三态 → 本机/云端映射矩阵（docprocShouldParseLocally 纯函数），
//     含 hasLocalDocproc=false 强制云端。

import 'package:biumind/core/platform/platform_caps.dart';
import 'package:biumind/features/wiki/application/docproc_preferences.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

PlatformCaps _caps({bool localDocproc = true, bool mobile = false}) =>
    PlatformCaps(
      hasLocalPty: true,
      hasFileSystem: true,
      hasNotifications: true,
      supportsBackgroundIsolates: true,
      hasPersistentSqlite: true,
      hasEmbeddedWebView: true,
      hasRepoAppRunner: false,
      hasLocalDocproc: localDocproc,
      isMobile: mobile,
      docprocQueueMaxBytes: mobile ? 80 * 1024 * 1024 : 200 * 1024 * 1024,
      docprocQueueConcurrency: mobile ? 1 : 3,
    );

const _mb = 1024 * 1024;

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  group('持久化', () {
    test('默认 auto；setLocation 后跨实例恢复', () async {
      final n = DocprocPreferencesNotifier();
      await Future<void>.delayed(Duration.zero);
      expect(n.state.location, DocprocProcessLocation.auto);

      await n.setLocation(DocprocProcessLocation.preferLocal);

      final n2 = DocprocPreferencesNotifier();
      await Future<void>.delayed(Duration.zero);
      expect(n2.state.location, DocprocProcessLocation.preferLocal);
    });

    test('坏数据 / 未知枚举值回退 auto', () async {
      SharedPreferences.setMockInitialValues({
        'biu.wiki.docproc': '{"location": "banana"}',
      });
      final n = DocprocPreferencesNotifier();
      await Future<void>.delayed(Duration.zero);
      expect(n.state.location, DocprocProcessLocation.auto);
    });

    test('JSON round-trip', () {
      const p = DocprocPreferences(location: DocprocProcessLocation.preferCloud);
      final back = DocprocPreferences.fromJson(p.toJson());
      expect(back.location, DocprocProcessLocation.preferCloud);
    });
  });

  group('映射矩阵（docprocShouldParseLocally）', () {
    test('自动：桌面 ≤50MB 本机，超出云端', () {
      final caps = _caps();
      expect(
        docprocShouldParseLocally(
            location: DocprocProcessLocation.auto, caps: caps, byteSize: 50 * _mb),
        isTrue,
      );
      expect(
        docprocShouldParseLocally(
            location: DocprocProcessLocation.auto,
            caps: caps,
            byteSize: 50 * _mb + 1),
        isFalse,
      );
    });

    test('自动：移动端 ≤10MB 本机', () {
      final caps = _caps(mobile: true);
      expect(
        docprocShouldParseLocally(
            location: DocprocProcessLocation.auto, caps: caps, byteSize: 10 * _mb),
        isTrue,
      );
      expect(
        docprocShouldParseLocally(
            location: DocprocProcessLocation.auto,
            caps: caps,
            byteSize: 11 * _mb),
        isFalse,
      );
    });

    test('优先本机：≤ docprocQueueMaxBytes（桌面 200MB / 移动 80MB）都本机',
        () {
      expect(
        docprocShouldParseLocally(
            location: DocprocProcessLocation.preferLocal,
            caps: _caps(),
            byteSize: 200 * _mb),
        isTrue,
      );
      expect(
        docprocShouldParseLocally(
            location: DocprocProcessLocation.preferLocal,
            caps: _caps(),
            byteSize: 200 * _mb + 1),
        isFalse,
      );
      expect(
        docprocShouldParseLocally(
            location: DocprocProcessLocation.preferLocal,
            caps: _caps(mobile: true),
            byteSize: 80 * _mb),
        isTrue,
      );
      expect(
        docprocShouldParseLocally(
            location: DocprocProcessLocation.preferLocal,
            caps: _caps(mobile: true),
            byteSize: 81 * _mb),
        isFalse,
      );
    });

    test('优先云端：任何大小都云端', () {
      for (final size in [1, 10 * _mb, 500 * _mb]) {
        expect(
          docprocShouldParseLocally(
              location: DocprocProcessLocation.preferCloud,
              caps: _caps(),
              byteSize: size),
          isFalse,
        );
      }
    });

    test('hasLocalDocproc=false：任何设置/大小都强制云端', () {
      final caps = _caps(localDocproc: false);
      for (final loc in DocprocProcessLocation.values) {
        expect(
          docprocShouldParseLocally(location: loc, caps: caps, byteSize: 1),
          isFalse,
          reason: '$loc 也应强制云端',
        );
      }
    });
  });
}
