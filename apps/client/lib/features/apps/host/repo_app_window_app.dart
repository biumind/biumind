// RepoAppWindowApp —— Repo App 原生子窗口的独立 Flutter engine 入口。
//
// desktop_multi_window 为每个子窗口起一个全新 engine：没有主 app 的
// ProviderContainer / 单例 / 持久化状态。本文件自包含：
//   - main() 经 RepoAppWindowArgs.isSubWindowEngineArgs 分发进来
//     （runRepoAppWindowApp）
//   - 自己的 ProviderScope（platformCapsProvider 重新 detect —— 子
//     engine 与主 engine 同平台，detect 结果一致，WebViewPanel 的
//     caps 分支照常工作）
//   - 首帧经 `biumind/repo_window` 自定义通道自检配窗口（尺寸/居中/
//     标题/显示）—— 插件原生只支持 show/hide，通道在 macOS/Linux 的
//     window-created 回调里注册；通道缺失（如未来 Windows）回退
//     WindowController.show()。
//
// UI = 自绘标题栏（应用名 + 关闭按钮）+ WebViewPanel。关闭只关窗口，
// runner 进程独立存活（下次打开秒连）。

import 'dart:async';

import 'package:desktop_multi_window/desktop_multi_window.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme/theme.dart';
import 'repo_app_window.dart';
import 'webview_panel.dart';

const _windowChannel = MethodChannel('biumind/repo_window');

/// 子窗口 engine 入口（main.dart 分发调用）。engineArgs 是插件传入的
/// `["multi_window", windowId, argumentsJson]`。
Future<void> runRepoAppWindowApp(List<String> engineArgs) async {
  final args = RepoAppWindowArgs.fromEngineArgs(engineArgs) ??
      const RepoAppWindowArgs(title: '', url: '', installId: '');
  runApp(ProviderScope(child: RepoAppWindowApp(args: args)));
  // 自检配在 runApp 之后 fire-and-forget —— 原生侧窗口已存在（插件先
  // 建窗口再起 engine），通道调用无需等首帧。
  unawaited(_configureWindow(args));
}

/// 子窗口自检配：尺寸/居中/标题/显示。通道缺失回退插件自带 show()。
Future<void> _configureWindow(RepoAppWindowArgs args) async {
  try {
    await _windowChannel.invokeMethod<void>('configure', {
      'width': kRepoAppWindowWidth,
      'height': kRepoAppWindowHeight,
      'title': args.title.isEmpty ? 'BiuMind' : args.title,
    });
  } catch (_) {
    try {
      final me = await WindowController.fromCurrentEngine();
      await me.show();
    } catch (_) {/* 显示失败也无计可施，窗口保持隐藏 */}
  }
}

/// 关闭当前子窗口。通道优先；缺失时回退 hide（进程级 runner 不受影响）。
Future<void> closeRepoAppWindow() async {
  try {
    await _windowChannel.invokeMethod<void>('close');
    return;
  } catch (_) {/* fall through */}
  try {
    final me = await WindowController.fromCurrentEngine();
    await me.hide();
  } catch (_) {/* ignore */}
}

class RepoAppWindowApp extends StatelessWidget {
  const RepoAppWindowApp({super.key, required this.args});

  final RepoAppWindowArgs args;

  @override
  Widget build(BuildContext context) {
    // 子窗口无设置页 —— 主题用默认 palette/字号，跟随系统明暗。
    const palette = PaletteId.inkblueOrange;
    return MaterialApp(
      title: args.title.isEmpty ? 'BiuMind' : args.title,
      debugShowCheckedModeBanner: false,
      theme: buildTheme(
        palette: palette,
        mode: Brightness.light,
        fontSize: FontSize.small,
      ),
      darkTheme: buildTheme(
        palette: palette,
        mode: Brightness.dark,
        fontSize: FontSize.small,
      ),
      // 与主 app 一致：BiuTokens shim 全局状态在进树前同步（
      // WebViewPanel 的 setBackgroundColor 读 BiuTokens.surface）。
      builder: (context, child) {
        BiuTokens.brightness = Theme.of(context).brightness;
        BiuTokens.palette = palette;
        return child ?? const SizedBox.shrink();
      },
      home: _RepoAppWindowHome(args: args),
    );
  }
}

class _RepoAppWindowHome extends StatelessWidget {
  const _RepoAppWindowHome({required this.args});

  final RepoAppWindowArgs args;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final displayTitle = args.title.isEmpty ? 'GitHub 应用' : args.title;
    return Scaffold(
      body: Column(
        children: [
          // ── 自绘标题栏（应用名 + 关闭）—— 拖拽由原生标题栏负责
          // （子窗口是标准 titled NSWindow / GtkHeaderBar），这里只
          // 承载应用名与关闭语义。
          Container(
            height: 40,
            padding: const EdgeInsets.symmetric(horizontal: 8),
            decoration: BoxDecoration(
              color: theme.colorScheme.surfaceContainerHigh,
              border: Border(
                bottom: BorderSide(
                    color: theme.dividerColor.withValues(alpha: 0.3)),
              ),
            ),
            child: Row(
              children: [
                const SizedBox(width: 4),
                Icon(Icons.terminal,
                    size: 16, color: theme.colorScheme.onSurfaceVariant),
                const SizedBox(width: 8),
                Expanded(
                  child: Text(
                    displayTitle,
                    style: theme.textTheme.titleSmall,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                IconButton(
                  tooltip: '关闭',
                  iconSize: 18,
                  icon: const Icon(Icons.close),
                  onPressed: () => unawaited(closeRepoAppWindow()),
                ),
              ],
            ),
          ),
          Expanded(
            child: args.url.isEmpty
                ? const Center(child: Text('窗口参数缺失，请关闭后重新打开'))
                : WebViewPanel(initialUrl: args.url, title: displayTitle),
          ),
        ],
      ),
    );
  }
}
