// RepoWindowPage —— Repo App 打开页（路由 /apps/repo-window/:installId，
// 注册在 ShellRoute 之外，先例 /suggestions）。
//
// 两条出口：
//   1. macOS（runner + webview 都有）→ 经 desktop_multi_window 开
//      真·原生子窗口（repo_app_window.dart / repo_app_window_app.dart），
//      本页落「已在新窗口打开」态；
//   2. 其余（Linux / 开窗失败回退）→ 本页应用内全屏：自绘标题栏 +
//      WebViewPanel（Linux 自动 _WebFallback 外部浏览器）。
//
// 启动链路（技术方案 §3.4）：先 GET /v1/apps/installs/{id}/runtime 快路
// 径（runner 已在跑就直接拿 URL），否则经 RepoAppLauncher 执行
// `biu repo-app ensure <install>`，解析 stdout 的 `BIU_REPOAPP_URL=`
// 通告。确认页暂存在 repoAppPendingEnvProvider 的机密 env（D9）随
// ensure 下发一次后立即清除。
//
// 平台门控：!hasRepoAppRunner（Windows / 移动端 / Web）显示降级说明。

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/platform/platform_caps.dart';
import '../../../core/platform/window_drag.dart';
import '../../../data/agent_plane/repo_app_launcher.dart';
import '../../../data/apps_providers.dart';
import '../../../services/login_shell_env.dart';
import '../host/repo_app_window.dart';
import '../host/webview_panel.dart';

class RepoWindowPage extends ConsumerStatefulWidget {
  const RepoWindowPage({super.key, required this.installId});

  final String installId;

  @override
  ConsumerState<RepoWindowPage> createState() => _RepoWindowPageState();
}

enum _Phase { checking, starting, ready, openedExternally, error }

class _RepoWindowPageState extends ConsumerState<RepoWindowPage> {
  _Phase _phase = _Phase.checking;
  String _url = '';
  String _error = '';
  String _lastLog = '';

  @override
  void initState() {
    super.initState();
    unawaited(_boot());
  }

  Future<void> _boot() async {
    setState(() {
      _phase = _Phase.checking;
      _error = '';
      _lastLog = '';
    });
    final client = ref.read(appsClientProvider);
    final token = ref.read(appsBearerProvider);

    // 快路径：runner 已在跑（上次 ensure 后没停）→ 直接加载。
    if (client != null && token != null) {
      try {
        final rt = await client.getRepoRuntime(
            installId: widget.installId, token: token);
        if (rt.isRunning) {
          if (!mounted) return;
          if (await _openNativeWindowIfCapable(rt.url)) return;
          if (!mounted) return;
          setState(() {
            _url = rt.url;
            _phase = _Phase.ready;
          });
          return;
        }
      } catch (_) {
        // runtime 端点未就绪 / 404（老服务端）→ 继续走 ensure。
      }
    }

    if (!mounted) return;
    setState(() => _phase = _Phase.starting);

    // 机密 env 内存接力：取出即清，绝不持久化（D9）。
    final pending =
        ref.read(repoAppPendingEnvProvider)[widget.installId] ?? const {};
    final launcher = RepoAppLauncher(
      // login shell env 带给子进程完整 PATH（GUI app 的
      // Platform.environment 找不到 git/node/uv）。未加载也有 fallback
      // 查找链，不阻塞启动。
      shellEnv: ref.read(loginShellEnvProvider).valueOrNull,
    );
    try {
      final res = await launcher.ensure(
        widget.installId,
        env: pending,
        onLog: (line) {
          if (!mounted) return;
          setState(() => _lastLog = line);
        },
      );
      if (!mounted) return;
      if (pending.isNotEmpty) {
        final cur = ref.read(repoAppPendingEnvProvider);
        final next = Map<String, Map<String, String>>.from(cur)
          ..remove(widget.installId);
        ref.read(repoAppPendingEnvProvider.notifier).state = next;
      }
      if (await _openNativeWindowIfCapable(res.url)) return;
      if (!mounted) return;
      setState(() {
        _url = res.url;
        _phase = _Phase.ready;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _phase = _Phase.error;
      });
    }
  }

  /// macOS（有 runner 且有嵌入式 webview）→ 开真·原生窗口，本页落
  /// "已在新窗口打开" 态。返回 true 表示已走原生路径（调用方不再
  /// 进 ready）；平台不满足或开窗失败（插件通道异常）返回 false，
  /// 回退应用内全屏 WebViewPanel。
  Future<bool> _openNativeWindowIfCapable(String url) async {
    final caps = ref.read(platformCapsProvider);
    if (!shouldUseNativeRepoWindow(caps)) return false;
    final install = await ref
        .read(installationProvider(widget.installId).future)
        .catchError((_) => null);
    final title = (install?.identifier.isNotEmpty ?? false)
        ? install!.identifier
        : 'GitHub 应用';
    try {
      await openNativeRepoWindow(RepoAppWindowArgs(
        title: title,
        url: url,
        installId: widget.installId,
      ));
    } catch (_) {
      return false; // 回退应用内全屏
    }
    if (!mounted) return true;
    setState(() => _phase = _Phase.openedExternally);
    return true;
  }

  @override
  Widget build(BuildContext context) {
    final caps = ref.watch(platformCapsProvider);
    final install =
        ref.watch(installationProvider(widget.installId)).valueOrNull;
    final appName = install?.identifier ?? '';
    final theme = Theme.of(context);

    return Scaffold(
      backgroundColor: BiuTokens.bg,
      body: Column(
        children: [
          // ── 自绘标题栏（应用名 + 关闭）──
          WindowDragArea(
            child: Container(
              height: 44,
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
                      appName.isEmpty ? 'GitHub 应用' : appName,
                      style: theme.textTheme.titleSmall,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  IconButton(
                    tooltip: '关闭',
                    iconSize: 18,
                    icon: const Icon(Icons.close),
                    onPressed: () => Navigator.of(context).maybePop(),
                  ),
                ],
              ),
            ),
          ),
          Expanded(
            child: !caps.hasRepoAppRunner
                ? const _UnsupportedPlatformBody()
                : switch (_phase) {
                    _Phase.ready => WebViewPanel(
                        initialUrl: _url,
                        title: appName,
                      ),
                    _Phase.openedExternally => const _OpenedExternallyBody(),
                    _Phase.error => _ErrorBody(
                        message: _error,
                        onRetry: () => unawaited(_boot()),
                      ),
                    _ => _StartingBody(
                        phase: _phase,
                        lastLog: _lastLog,
                      ),
                  },
          ),
        ],
      ),
    );
  }
}

class _StartingBody extends StatelessWidget {
  const _StartingBody({required this.phase, required this.lastLog});

  final _Phase phase;
  final String lastLog;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space6),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const CircularProgressIndicator(),
            const SizedBox(height: BiuTokens.space3),
            Text(
              phase == _Phase.checking ? '正在检查运行状态…' : '正在启动应用…',
              style: theme.textTheme.titleMedium,
            ),
            const SizedBox(height: BiuTokens.space2),
            Text(
              '首次启动需要克隆仓库并安装依赖，可能需要几分钟。',
              textAlign: TextAlign.center,
              style: theme.textTheme.bodySmall
                  ?.copyWith(color: theme.colorScheme.onSurfaceVariant),
            ),
            if (lastLog.isNotEmpty) ...[
              const SizedBox(height: BiuTokens.space3),
              ConstrainedBox(
                constraints: const BoxConstraints(maxWidth: 520),
                child: Text(
                  lastLog,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  textAlign: TextAlign.center,
                  style: const TextStyle(fontFamily: 'monospace', fontSize: 11),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _OpenedExternallyBody extends StatelessWidget {
  const _OpenedExternallyBody();

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space6),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.open_in_new,
                size: 32, color: theme.colorScheme.onSurfaceVariant),
            const SizedBox(height: BiuTokens.space3),
            Text('已在新窗口打开', style: theme.textTheme.titleMedium),
            const SizedBox(height: BiuTokens.space2),
            Text(
              '应用在独立窗口中运行；关闭窗口不影响后台服务，下次打开秒连。',
              textAlign: TextAlign.center,
              style: theme.textTheme.bodySmall
                  ?.copyWith(color: theme.colorScheme.onSurfaceVariant),
            ),
            const SizedBox(height: BiuTokens.space3),
            TextButton(
              onPressed: () => Navigator.of(context).maybePop(),
              child: const Text('返回'),
            ),
          ],
        ),
      ),
    );
  }
}

class _ErrorBody extends StatelessWidget {
  const _ErrorBody({required this.message, required this.onRetry});

  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space6),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.error_outline,
                size: 32, color: theme.colorScheme.error),
            const SizedBox(height: BiuTokens.space3),
            Text('启动失败', style: theme.textTheme.titleMedium),
            const SizedBox(height: BiuTokens.space2),
            ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 520),
              child: SelectableText(
                message,
                textAlign: TextAlign.center,
                style: theme.textTheme.bodySmall
                    ?.copyWith(color: theme.colorScheme.onSurfaceVariant),
              ),
            ),
            const SizedBox(height: BiuTokens.space3),
            FilledButton.icon(
              onPressed: onRetry,
              icon: const Icon(Icons.refresh, size: 18),
              label: const Text('重试'),
            ),
          ],
        ),
      ),
    );
  }
}

class _UnsupportedPlatformBody extends StatelessWidget {
  const _UnsupportedPlatformBody();

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space6),
        child: Text(
          '当前平台暂不支持在本机运行 GitHub 应用（macOS / Linux 客户端可用）。',
          textAlign: TextAlign.center,
          style: theme.textTheme.bodyMedium
              ?.copyWith(color: theme.colorScheme.onSurfaceVariant),
        ),
      ),
    );
  }
}
