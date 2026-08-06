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
