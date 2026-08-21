// 客户端更新检测 — releases.json 领域模型。
//
// 与 schema/release/v1/manifest.json 契约一一对应 (CI 生成、官网下载页同源)。
// 沿用本仓 data/api 既有约定 (手写 fromJson + 防御性默认值, 无 freezed)。
//
// 版本字符串规范 (见 docs/BiuMind-Client-Release-Manifest.md §4):
//   version 字段已是规范 semver (无 v 前缀无 +build)。
//   客户端 PackageInfo.version 也是规范 (0.1.0)。比对前两边都直 Version.parse。

import 'package:pub_semver/pub_semver.dart';

/// releases.json 顶层清单。CI 生成、存 MinIO releases bucket 根路径,
/// 官网 /download 页与本更新检测共用。
class ReleaseManifest {
  final Version version;
  final DateTime releasedAt;
  final String channel; // stable | beta | internal
  final String notes;
  final String notesEn;
  final List<ReleaseAsset> assets;

  const ReleaseManifest({
    required this.version,
    required this.releasedAt,
    required this.channel,
    required this.notes,
    required this.notesEn,
    required this.assets,
  });

  factory ReleaseManifest.fromJson(Map<String, dynamic> j) {
    final list = (j['assets'] as List?) ?? const [];
    return ReleaseManifest(
      version: _parseVersion(j['version']),
      releasedAt:
          DateTime.tryParse(j['releasedAt'] as String? ?? '') ??
          DateTime.fromMillisecondsSinceEpoch(0),
      channel: j['channel'] as String? ?? 'stable',
      notes: j['notes'] as String? ?? '',
      notesEn: j['notesEn'] as String? ?? '',
      assets: list
          .whereType<Map<String, dynamic>>()
          .map(ReleaseAsset.fromJson)
          .toList(growable: false),
    );
  }
}

/// nightly/index.json 顶层清单 (canary 通道)。
///
/// 与 [ReleaseManifest] (v1 releases.json) 不同 envelope:channel 固定 nightly,
/// 多 [run] (CI run number, 仅展示 #N + dismiss 去重) 与 [build] (stamp job 的
/// epoch 秒 = 本次产物的 versionCode/CFBundleVersion, 已装去重以它为准)。
/// Asset 对象沿用 [ReleaseAsset] (与 v1 同形状), 客户端 asset 解析代码可复用。
/// 额外 [releaseUrl] 指向 GH release 页, 作为"当前平台无对应 asset"(intel mac /
/// web / iOS)时的下载 fallback。
///
/// 由 .github/workflows/release-client-nightly.yml 在每次 nightly 构建时生成,
/// 上传到 `<bucket>/nightly/index.json` (根, 每次覆盖 = 最新夜版)。
class NightlyManifest {
  final Version version;
  final int run; // CI run number, 单调递增 (展示 + dismiss 去重)
  /// epoch 秒构建戳, 与产物 versionCode 同值; stable 与 nightly 共用这条
  /// 时间轴, 双向覆盖安装不产生 versionCode 降级 (-25)。
  /// 旧清单无此字段时回退 run (旧夜版 versionCode 就是 run)。
  final int build;
  final DateTime releasedAt;
  final String notes;
  final String releaseUrl; // GH release 页 (无平台 asset 时的 fallback)
  final List<ReleaseAsset> assets;

  const NightlyManifest({
    required this.version,
    required this.run,
    required this.build,
    required this.releasedAt,
    required this.notes,
    required this.releaseUrl,
    required this.assets,
  });

  factory NightlyManifest.fromJson(Map<String, dynamic> j) {
    final list = (j['assets'] as List?) ?? const [];
    final run = (j['run'] as num?)?.toInt() ?? 0;
    return NightlyManifest(
      version: _parseVersion(j['version']),
      run: run,
      build: (j['build'] as num?)?.toInt() ?? run,
      releasedAt:
          DateTime.tryParse(j['releasedAt'] as String? ?? '') ??
          DateTime.fromMillisecondsSinceEpoch(0),
      notes: j['notes'] as String? ?? '',
      releaseUrl: j['releaseUrl'] as String? ?? '',
      assets: list
          .whereType<Map<String, dynamic>>()
          .map(ReleaseAsset.fromJson)
          .toList(growable: false),
    );
  }
}

/// 单平台产物。platform 枚举见 schema/release/v1/manifest.json。
class ReleaseAsset {
  final String platform; // macos-arm64 | windows-x64 | linux-appimage | ...
  final String url; // 官网域名下载 url (经 site nginx 反代 MinIO)
  final String filename;
  final int size; // bytes
  final String sha256;
  final bool signed;
  final String arch; // arm64 | x64 | universal

  const ReleaseAsset({
    required this.platform,
    required this.url,
    required this.filename,
    required this.size,
    required this.sha256,
    required this.signed,
    required this.arch,
  });

  factory ReleaseAsset.fromJson(Map<String, dynamic> j) => ReleaseAsset(
        platform: j['platform'] as String? ?? '',
        url: j['url'] as String? ?? '',
        filename: j['filename'] as String? ?? '',
        size: (j['size'] as num?)?.toInt() ?? 0,
        sha256: j['sha256'] as String? ?? '',
        signed: j['signed'] as bool? ?? false,
        arch: j['arch'] as String? ?? '',
      );
}

/// 解析版本字符串为 semver,strip v 前缀 + drop +build 后缀。
/// releases.json version / pubspec 0.1.0+1 / tag client-v0.1.0 都归一。
Version _parseVersion(Object? raw) {
  var s = (raw as String? ?? '0.0.0').trim();
  if (s.startsWith('v') || s.startsWith('V')) s = s.substring(1);
  final plus = s.indexOf('+');
  if (plus >= 0) s = s.substring(0, plus);
  try {
    return Version.parse(s);
  } catch (_) {
    return Version.none;
  }
}
