// 客户端更新检测 controller。
//
// 启动时拉 <origin>/downloads/releases.json (origin = identityUri 单源),
// 比对当前版本 (PackageInfo.fromPlatform),有新版返回 UpdateInfo 供 banner 展示。
//
// 设计 (见 docs/BiuMind-Client-Release-Manifest.md):
//   - 单 origin 寻址: origin = settingsControllerProvider.identityUrl (经 site nginx)
//   - 版本规范化: strip v + drop +build, pub_semver Version.parse 比对
//   - 平台选择: dart:io Platform (web 端跳外部下载页, 不直选 platform asset)
//   - 网络/解析错: 静默 (return null, 不吓用户), 见 security_alert_banner 约定
//   - autoDispose + banner watch: 只在 shell 渲染时触发, 不额外占资源

import 'dart:convert' show jsonDecode;
import 'dart:io' as io;

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:http/http.dart' as http;
import 'package:package_info_plus/package_info_plus.dart';
import 'package:pub_semver/pub_semver.dart';

import '../../../features/settings/application/settings_controller.dart';
import '../../../features/update/domain/release_manifest.dart';

/// 检测结果:有新版则填充,无新版/未登录/网络错 → null。
class UpdateInfo {
  final Version targetVersion;
  final String downloadPageUrl; // 跳官网 /download 页 (单 origin 下)
  final String notes;

  const UpdateInfo({
    required this.targetVersion,
    required this.downloadPageUrl,
    required this.notes,
  });
}

/// 启动时拉 releases.json 比对当前版本。
/// autoDispose: 仅 banner 挂载时触发; null = 无更新或拉失败 (静默)。
final updateAvailableProvider =
    FutureProvider.autoDispose<UpdateInfo?>((ref) async {
  final settings = ref.watch(settingsControllerProvider).valueOrNull;
  if (settings == null) return null;
  final origin = (settings.identityUrl ?? '').trim();
  if (origin.isEmpty) return null; // 未配置服务器地址, 不查

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

    // 当前版本
    final info = await PackageInfo.fromPlatform();
    final current = _normalizeVersion(info.version);

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
});

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

/// 当前平台对应的 asset platform key (web 不选, 跳下载页让浏览器检测)。
/// 本轮 banner 只跳 /download 页, 此函数预留给后续"直选对应包下载"。
String? currentAssetPlatform() {
  if (kIsWeb) return null;
  if (io.Platform.isMacOS) return 'macos-arm64'; // Apple Silicon 主流; intel 留官网检测
  if (io.Platform.isWindows) return 'windows-x64';
  if (io.Platform.isLinux) return 'linux-appimage';
  if (io.Platform.isAndroid) return 'android-apk';
  return null;
}
