// Repo App 真·原生多窗口（desktop_multi_window）—— 参数编解码 +
// 平台分支 + 主 engine 侧开窗入口。
//
// 架构（0.3.x API）：
//   - 每个子窗口是一个独立 Flutter engine，main() 收到
//     `["multi_window", windowId, argumentsJson]` 作为 entrypoint
//     args（插件原生侧硬编码，FlutterMultiWindowPlugin.swift /
//     multi_window_manager.cc），据此分发到 RepoAppWindowApp。
//   - 子窗口 engine 是全新 Riverpod 树，不共享主 app 的任何 provider /
//     单例 —— 参数只能经 createWindow 的 JSON 字符串传入。
//   - 窗口尺寸/居中/标题/显示由子窗口 engine 首帧经 `biumind/repo_window`
//     自定义通道自检配（插件原生只支持 show/hide；通道在 macOS
//     MainFlutterWindow.swift / Linux my_application.cc 的
//     window-created 回调里注册）。
//
// 平台策略：只有「有 runner 且有嵌入式 webview」的平台（实际 = macOS）
// 走原生窗口；Linux（有 runner 无 webview）保留应用内全屏路由 +
// _WebFallback 外部浏览器 —— 开一个只能显示 fallback 的原生窗口没有
// 意义。Windows 入口本来就隐藏。

import 'dart:convert';

import 'package:desktop_multi_window/desktop_multi_window.dart';

import '../../../core/platform/platform_caps.dart';

/// 子窗口默认尺寸（逻辑像素）。runner 适配层未来若带 window 字段
/// （宽高偏好），在 RepoAppWindowArgs 里扩展即可。
const double kRepoAppWindowWidth = 1280;
const double kRepoAppWindowHeight = 800;

/// createWindow 的 JSON 参数。字段全部防御性解析。
class RepoAppWindowArgs {
  final String title;
  final String url;
  final String installId;

  const RepoAppWindowArgs({
    required this.title,
    required this.url,
    required this.installId,
  });

  String encode() => jsonEncode({
        'title': title,
        'url': url,
        'install_id': installId,
      });

  /// 解析子窗口 engine 收到的 arguments JSON；任何不符 → null
  /// （调用方给兜底空壳而不是崩掉子窗口 engine）。
  static RepoAppWindowArgs? tryDecode(String raw) {
    if (raw.isEmpty) return null;
    try {
      final decoded = jsonDecode(raw);
      if (decoded is! Map) return null;
      final url = decoded['url'];
      if (url is! String || url.isEmpty) return null;
      return RepoAppWindowArgs(
        title: decoded['title'] as String? ?? '',
        url: url,
        installId: decoded['install_id'] as String? ?? '',
      );
    } catch (_) {
      return null;
    }
  }

  /// main() 的 entrypoint args 是否属于 desktop_multi_window 子窗口
  /// engine。插件原生侧固定传 `["multi_window", windowId, arguments]`。
  static bool isSubWindowEngineArgs(List<String> engineArgs) =>
      engineArgs.length >= 3 && engineArgs[0] == 'multi_window';

  /// 从 entrypoint args 提取业务参数（isSubWindowEngineArgs 为 true
  /// 时才有意义）。
  static RepoAppWindowArgs? fromEngineArgs(List<String> engineArgs) =>
      isSubWindowEngineArgs(engineArgs) ? tryDecode(engineArgs[2]) : null;
}

/// 是否走真·原生窗口。macOS：true（runner + webview 都有）；Linux：
/// false（无 webview，保留应用内全屏 + 外部浏览器 fallback）；其余平台
/// false（入口已隐藏）。
bool shouldUseNativeRepoWindow(PlatformCaps caps) =>
    caps.hasRepoAppRunner && caps.hasEmbeddedWebView;

/// 主 engine 侧：创建并返回子窗口 controller。子窗口 engine 启动后
/// 自行配置尺寸/居中/标题并显示（见 repo_app_window_app.dart），所以
/// 这里 hiddenAtLaunch: true 且不调 show() —— 避免 800x600@0,0 的默
/// 认帧先闪一下。
///
/// 同 installId 已有子窗口 → show() 提到前台复用，不重复开（包文档
/// 示例的 dedup 模式；重复点"打开"不开第二个窗口）。
///
/// 失败（通道未实现 / 平台不支持）抛异常，调用方回退应用内全屏。
Future<WindowController> openNativeRepoWindow(RepoAppWindowArgs args) async {
  try {
    final all = await WindowController.getAll();
    for (final c in all) {
      final existing = RepoAppWindowArgs.tryDecode(c.arguments);
      if (existing != null && existing.installId == args.installId) {
        await c.show();
        return c;
      }
    }
  } catch (_) {/* getAll 失败不阻塞正常开窗 */}
  return WindowController.create(WindowConfiguration(
    arguments: args.encode(),
    hiddenAtLaunch: true,
  ));
}
