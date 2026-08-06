// releases.json 领域模型解析测试。
//
// 校验 fromJson 防御性默认值 + 版本规范化 (strip v + drop +build),
// 覆盖 schema/release/v1 契约的关键字段。纯逻辑无网络/mock。

import 'package:biumind/features/update/domain/release_manifest.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:pub_semver/pub_semver.dart';

void main() {
  group('ReleaseManifest.fromJson', () {
    test('解析完整合法清单', () {
      const j = {
        'version': '0.2.0',
        'releasedAt': '2026-07-15T08:00:00Z',
        'channel': 'stable',
        'notes': '修了些 bug',
        'notesEn': 'bugfix',
        'assets': [
          {
            'platform': 'macos-arm64',
            'url': 'https://your-biumind.example.com/downloads/a.dmg',
            'filename': 'a.dmg',
            'size': 1024,
            'sha256': 'abc',
            'signed': false,
            'arch': 'arm64',
          },
          {
            'platform': 'android-apk',
            'url': 'https://your-biumind.example.com/downloads/b.apk',
            'filename': 'b.apk',
            'size': 2048,
            'sha256': 'def',
            'signed': true,
            'arch': 'universal',
          },
        ],
      };
      final m = ReleaseManifest.fromJson(j);
      expect(m.version.toString(), '0.2.0');
      expect(m.channel, 'stable');
      expect(m.notes, '修了些 bug');
      expect(m.assets.length, 2);
      expect(m.assets[0].platform, 'macos-arm64');
      expect(m.assets[0].signed, isFalse);
      expect(m.assets[1].signed, isTrue);
      expect(m.assets[1].size, 2048);
    });

    test('缺字段用防御性默认值', () {
      final m = ReleaseManifest.fromJson({'version': '1.0.0'});
      expect(m.version.toString(), '1.0.0');
      expect(m.channel, 'stable');
      expect(m.notes, '');
      expect(m.assets, isEmpty);
    });

    test('version 带 v 前缀被 strip', () {
      final m = ReleaseManifest.fromJson({'version': 'v0.3.0'});
      expect(m.version.toString(), '0.3.0');
    });

    test('version 带 +build 后缀被 drop', () {
      final m = ReleaseManifest.fromJson({'version': '0.1.0+42'});
      expect(m.version.toString(), '0.1.0');
    });

    test('version 非法字符串 → Version.none (不抛)', () {
      final m = ReleaseManifest.fromJson({'version': 'not-a-version'});
      expect(m.version, predicate((v) => v.toString() == '0.0.0'));
    });
  });

  group('版本优先级比对', () {
    Version parse(String s) =>
        ReleaseManifest.fromJson({'version': s}).version;

    test('高版本 > 低版本 (compareTo > 0)', () {
      expect(parse('0.2.0').compareTo(parse('0.1.0')), greaterThan(0));
      expect(parse('1.0.0').compareTo(parse('0.9.9')), greaterThan(0));
    });

    test('相等版本 compareTo == 0', () {
      expect(parse('0.1.0').compareTo(parse('0.1.0')), 0);
    });

    test('同版本不同 build 后缀视为相等 (build 被 drop)', () {
      expect(parse('0.1.0+1').compareTo(parse('0.1.0+99')), 0);
    });

    test('更新检测条件: target.compareTo(current) > 0', () {
      final target = parse('0.2.0');
      final current = parse('0.1.0');
      expect(target.compareTo(current) > 0, isTrue);
    });
  });
}
