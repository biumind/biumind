// 客户端更新检测 controller。
//
// 启动时拉 `<origin>/downloads/releases.json` (origin = identityUri 单源),
// 比对当前版本 (PackageInfo.fromPlatform),有新版返回 UpdateInfo 供 banner 展示。
//
// 设计 (见 docs/BiuMind-Client-Release-Manifest.md):
//   - 单 origin 寻址: origin = settingsControllerProvider.identityUrl (经 site nginx)
//   - 版本规范化: strip v + drop +build, pub_semver Version.parse 比对
//   - 平台选择: stable 跳 /download 页 (官网侧检测); nightly 直选当前平台
//     asset.url (OSS 直链), 无对应平台 → GH release 页
//   - 网络/解析错: 静默 (return null, 不吓用户), 见 security_alert_banner 约定
//   - autoDispose + banner watch: 只在 shell 渲染时触发, 不额外占资源

import 'dart:convert' show jsonDecode;
import 'dart:io' as io;

import 'package:flutter/foundation.dart' show kIsWeb, visibleForTesting;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:http/http.dart' as http;
import 'package:package_info_plus/package_info_plus.dart';
import 'package:pub_semver/pub_semver.dart';

import '../../../features/settings/application/settings_controller.dart';
import '../../../features/update/domain/release_manifest.dart';

/// 检测结果:有新版则填充,无新版/未登录/网络错 → null。
class UpdateInfo {
  final Version targetVersion;
  final String downloadPageUrl; // stable: 官网 /download; nightly: 平台 asset OSS 直链 (无则 GH 页)
  final String notes;

  /// true = nightly canary 通道 (设置→关于 开启"获取开发版"后); false = stable。
  final bool isNightly;

  /// nightly CI run number (仅 nightly 有意义); stable = 0。
  /// UpdateBanner 用它显示 #N + dismiss 时回写 lastNotifiedNightlyRun 去重。
  final int nightlyRun;

  const UpdateInfo({
    required this.targetVersion,
    required this.downloadPageUrl,
    required this.notes,
    this.isNightly = false,
    this.nightlyRun = 0,
  });
}

/// 启动时拉清单比对当前版本。
/// autoDispose: 仅 banner 挂载时触发; null = 无更新或拉失败 (静默)。
///
/// 通道优先级:用户在 设置→关于 开启"获取开发版"时, nightly canary 优先于
/// stable (opt-in bleeding edge); 否则只查 stable。
final updateAvailableProvider =
    FutureProvider.autoDispose<UpdateInfo?>((ref) async {
  final settings = ref.watch(settingsControllerProvider).valueOrNull;
  if (settings == null) return null;
  final origin = (settings.identityUrl ?? '').trim();
  if (origin.isEmpty) return null; // 未配置服务器地址, 不查

  Version current;
  // 已装构建戳: CI 以 --build-number=epoch秒 构建 (stable 与 nightly 共用
  // 时间轴), buildNumber 即构建时刻。解析失败按 0 (总视为更旧)。
  int installedBuild;
  try {
    final info = await PackageInfo.fromPlatform();
    current = _normalizeVersion(info.version);
    installedBuild = int.tryParse(info.buildNumber) ?? 0;
  } catch (_) {
    return null;
  }

  // 1. nightly canary — envelope 非 v1 (channel=nightly + run + build), 单独 fetch/parse。
  //    nightly 优先:开了开关的用户要 bleeding edge, 同一 banner 位不叠 stable。
  if (settings.fetchNightly) {
    final nightly = await checkNightly(
      origin: origin,
      installedBuild: installedBuild,
      lastNotifiedRun: settings.lastNotifiedNightlyRun,
    );
    if (nightly != null) return nightly;
  }

  // 2. stable — 官网默认通道。
  return _checkStable(origin: origin, current: current);
});

/// stable releases.json 检查。只看 stable channel, version 严格大于当前才提示。
/// 跳官网 /download 页 (单 origin); 产物 url 在官网侧检测平台。
Future<UpdateInfo?> _checkStable({
  required String origin,
  required Version current,
}) async {
  try {
    final res = await http
        .get(Uri.parse('$origin/downloads/releases.json'))
        .timeout(const Duration(seconds: 5));
    if (res.statusCode != 200) return null;
    final body = res.body;
    if (body.isEmpty) return null;
    final decoded = jsonDecode(body);
    if (decoded is! Map<String, dynamic>) return null;
    final manifest = ReleaseManifest.fromJson(decoded);
    // 只看 stable channel (官网默认拉 stable, 内测 channel 不主动弹更新)
    if (manifest.channel != 'stable') return null;
    // Version 实现 Comparable + 重载 >/</>=; 优先级高于当前版本才有更新
    if (manifest.version.compareTo(current) <= 0) return null;
    return UpdateInfo(
      targetVersion: manifest.version,
      // 跳官网下载页 (单 origin): origin + /download。web 端 externalApplication
      // 会新标签打开; 桌面端 url_launcher 调系统浏览器。
      downloadPageUrl: '$origin/download',
      notes: manifest.notes,
    );
  } catch (_) {
    return null; // 网络/解析错不吓用户
  }
}

/// nightly/index.json 检查 (canary 通道)。两级去重:
///   1. 已装去重:APK versionCode = 构建时刻 epoch 秒 (stable/nightly 共用
///      时间轴), manifest.build <= installedBuild 说明装的已是这版或更新,
///      不再弹;
///   2. 提示去重:dismiss 回写的 lastNotifiedRun 单调递增, 已提示过的 run 不再弹。
/// nightly 清单始终是"最新构建", 两级都未命中即提示 (用户已 opt-in)。
/// 下载 url:当前平台匹配的 asset.url (OSS CNAME 直链, 经 site nginx
/// 反代); 无对应平台 (intel mac / web / iOS) → GH release 页 fallback。
@visibleForTesting
Future<UpdateInfo?> checkNightly({
  required String origin,
  required int installedBuild,
  required int? lastNotifiedRun,
}) async {
  try {
    final res = await http
        .get(Uri.parse('$origin/downloads/nightly/index.json'))
        .timeout(const Duration(seconds: 5));
    if (res.statusCode != 200) return null;
    final body = res.body;
    if (body.isEmpty) return null;
    final decoded = jsonDecode(body);
    if (decoded is! Map<String, dynamic>) return null;
    final manifest = NightlyManifest.fromJson(decoded);
    // 已装去重:装的就是这次构建 (或回退装了更新构建), 无需提示。
    if (manifest.build <= installedBuild) return null;
    // 提示去重:已提示过的 run 不再弹 (跨重启), 直到更新的 run。
    if (lastNotifiedRun != null && manifest.run <= lastNotifiedRun) {
      return null;
    }
    // 平台选 asset.url; 无匹配 → releaseUrl (GH) → /download 兜底。
    final platform = currentAssetPlatform();
    final match = platform == null
        ? null
        : manifest.assets.where((a) => a.platform == platform).toList();
    String download;
    if (match != null && match.isNotEmpty && match.first.url.isNotEmpty) {
      download = match.first.url;
    } else if (manifest.releaseUrl.isNotEmpty) {
      download = manifest.releaseUrl;
    } else {
      download = '$origin/download';
    }
    return UpdateInfo(
      targetVersion: manifest.version,
      downloadPageUrl: download,
      notes: manifest.notes,
      isNightly: true,
      nightlyRun: manifest.run,
    );
  } catch (_) {
    return null; // 网络/解析错不吓用户
  }
}

/// strip v 前缀 + drop +build, 与 domain _parseVersion 同归一。
Version _normalizeVersion(String raw) {
  var s = raw.trim();
  if (s.startsWith('v') || s.startsWith('V')) s = s.substring(1);
  final plus = s.indexOf('+');
  if (plus >= 0) s = s.substring(0, plus);
  try {
    return Version.parse(s);
  } catch (_) {
    return Version.none;
  }
}

/// 当前平台对应的 asset platform key (web/iOS/intel-mac 不选 → null)。
/// nightly 检查用它直选 asset.url (OSS 直链); null 时 _checkNightly 回退 GH 页。
String? currentAssetPlatform() {
  if (kIsWeb) return null;
  if (io.Platform.isMacOS) return 'macos-arm64'; // Apple Silicon 主流; intel 留 GH 页检测
  if (io.Platform.isWindows) return 'windows-x64';
  if (io.Platform.isLinux) return 'linux-appimage';
  if (io.Platform.isAndroid) return 'android-apk';
  return null;
}
