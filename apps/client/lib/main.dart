import 'dart:async' show unawaited;
import 'dart:io' as io;
import 'dart:math' as math;

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart' show MethodChannel;
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:logging/logging.dart';

import 'app/router.dart';
import 'app/theme.dart';
import 'core/ai/ai_surface.dart';
import 'core/ui/biu_scroll_behavior.dart';
import 'data/agent_plane/biu_daemon_manager.dart';
import 'data/api/_http_helpers.dart' show authErrorHandler, billingErrorHandler;
import 'data/api/hub_backend.dart';
import 'features/apps/host/repo_app_window.dart';
import 'features/apps/host/repo_app_window_app.dart';
import 'features/chat/application/chat_preferences.dart';
import 'features/chat/presentation/v2/insufficient_credits_modal.dart';
import 'features/chat/sync/chat_sync_manager.dart';
import 'features/creation/application/credits_controller.dart';
import 'l10n/app_localizations.dart';
import 'features/settings/application/settings_controller.dart';
import 'services/auth_service.dart';
import 'services/settings_repo.dart' show ThemePreference;
import 'services/login_shell_env.dart';
import 'services/token_manager.dart'
    show
        tokenManagerProvider,
        sessionExpiredCountProvider,
        sessionExpiredReasonProvider,
        SessionExpiredReason;
import 'services/token_refresher.dart';

void main(List<String> args) {
  // 必须最先初始化 binding — 下面的 ProviderContainer 创建 + 启动块
  // (ensureOriginDevice/ensureInstallationId)会在 runApp 之前触发
  // settingsController.build() → keychain/shared_preferences 等平台通道调用,
  // 没有 binding 时这些调用直接抛 "Binding has not yet been initialized",
  // settings load 静默返回空 → 已登录会话被"藏"住并被启动写入覆盖。
  WidgetsFlutterBinding.ensureInitialized();

  // desktop_multi_window 子窗口 engine 分发：插件原生侧以
  // ["multi_window", windowId, argumentsJson] 作为 entrypoint args
  // 起第二个 engine —— 检出即运行独立的 RepoAppWindowApp（全新
  // Riverpod 树），绝不初始化主 app 的任何 provider/持久化状态。
  if (RepoAppWindowArgs.isSubWindowEngineArgs(args)) {
    unawaited(runRepoAppWindowApp(args));
    return;
  }

  // macOS 焦点桥：原生侧（MainFlutterWindow）监视到点击落在平台视图
  // （笔记编辑器 WKWebView）区域时通知这里。落在平台视图上的点击进不了
  // 框架手势体系，文本框 FocusNode 不会自动 unfocus，其原生文本会话
  // （FlutterTextInputPlugin 的隐藏输入框）就一直占着第一响应者，导致
  // 编辑器点击后无法编辑 —— 收到通知主动收敛框架焦点。
  if (!kIsWeb && io.Platform.isMacOS) {
    const MethodChannel('biumind/focus').setMethodCallHandler((call) async {
      if (call.method == 'platformViewTapped') {
        FocusManager.instance.primaryFocus?.unfocus();
      }
      return null;
    });
  }

  Logger.root.level = Level.INFO;
  Logger.root.onRecord.listen((rec) {
    // ignore: avoid_print
    print('${rec.time} [${rec.level.name}] ${rec.loggerName}: ${rec.message}');
  });

  // Build the ProviderContainer ourselves so the router (built before
  // any widget mounts) can read + listen to providers directly.
  final container = ProviderContainer(
    overrides: [
      // If credentials present, route AiSurface through the real model-relay;
      // otherwise stay on DevEchoBackend so unauth'd dev runs still work.
      aiSurfaceBackendProvider.overrideWith((ref) {
        final creds = ref.watch(hubCredentialsProvider);
        if (creds == null) return DevEchoBackend();
        return RelayBackend(
          HubConfig(endpoint: creds.endpoint, bearerToken: creds.bearerToken),
        );
      }),
    ],
  );

  // 全局 401 拦截：所有走 apiRequest / sseStream 的 HTTP 调用收到 401
  // 时，会调这个 handler → tokenManager 触发 refresh → 返回新 token →
  // helper 自动 retry 一次。仅设置一次（容器是 app 生命周期单例）。
  authErrorHandler = () => container.read(tokenManagerProvider).handle401();

  // 编码任务多端同步: 首次启动生成 origin device id (uuid v4) + label。
  // 跟着 settings 持久化, 后续不变。fire-and-forget — 不阻塞 runApp,
  // settings 读取 & async 持久化在 runApp 之后第一帧空闲时跑。
  unawaited(() async {
    final ctl = container.read(settingsControllerProvider.notifier);
    final id = _genUuidV4();
    final label = _platformDeviceLabel();
    await ctl.ensureOriginDevice(generatedId: id, label: label);
    // 设备授权 ID — 跟 origin_device_id 独立, 这个给 identity 端做"同设备
    // 复用 refresh_token". 首次启动生成, 跨登入登出永久持久化.
    await ctl.ensureInstallationId(_genUuidV4());
  }());

  runApp(
    UncontrolledProviderScope(
      container: container,
      child: BiuMindApp(container: container),
    ),
  );
}

class BiuMindApp extends ConsumerStatefulWidget {
  const BiuMindApp({required this.container, super.key});
  final ProviderContainer container;

  @override
  ConsumerState<BiuMindApp> createState() => _BiuMindAppState();
}

class _BiuMindAppState extends ConsumerState<BiuMindApp>
    with WidgetsBindingObserver {
  late final _router = buildRouter(widget.container);

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    // 跨模态计费拦截:所有走 apiRequest/binaryRequest 的模态调用(TTS/STT/
    // image/video/embedding...)收到 402 余额不足 / 429 配额 时统一弹卡,
    // 各 feature 不必各自处理。
    billingErrorHandler = _onBillingError;
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  bool _billingDialogOpen = false;

  /// 计费拦截 hook 的 UI 落地:402 余额不足弹充值卡(去重,一次只一个);
  /// 429 配额/限流用 SnackBar 轻提示(非阻塞)。从非 widget 代码(transport
  /// 层)触发,故用 rootNavigatorKey / rootScaffoldMessengerKey + postFrame。
  void _onBillingError(int status, String code, String message) {
    if (status == 429) {
      rootScaffoldMessengerKey.currentState?.showSnackBar(
        SnackBar(
          content: Text(message.isNotEmpty ? message : '请求过于频繁或配额已用尽,请稍后再试'),
        ),
      );
      return;
    }
    // 402 insufficient_credits → 充值卡(去重)
    if (_billingDialogOpen) return;
    final ctx = rootNavigatorKey.currentContext;
    if (ctx == null) return;
    _billingDialogOpen = true;
    WidgetsBinding.instance.addPostFrameCallback((_) async {
      if (!mounted) {
        _billingDialogOpen = false;
        return;
      }
      try {
        await showInsufficientCreditsModal(ctx);
      } finally {
        _billingDialogOpen = false;
      }
    });
  }

  bool _sessionDialogOpen = false;

  /// 弹一次性"会话过期"对话框。用 rootNavigatorKey 拿 Material context,
  /// 这样可以在 GoRouter redirect 还没把用户推到 /login 之前就盖在
  /// 当前页面上。同时去重: 多次连续 bump (refresher + handle401 同时撞)
  /// 只显示一个对话框。
  void _showSessionExpiredDialog() {
    if (_sessionDialogOpen) return;
    final ctx = rootNavigatorKey.currentContext;
    if (ctx == null) return;
    _sessionDialogOpen = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) {
        _sessionDialogOpen = false;
        return;
      }
      // reason 决定文案: 普通过期 vs reuse detection (token 可能被盗)。
      final reason = ProviderScope.containerOf(
        ctx,
      ).read(sessionExpiredReasonProvider);
      final (title, body) = switch (reason) {
        SessionExpiredReason.tokenReuse => (
          '会话被吊销',
          '检测到该账号在另一处使用了已失效的凭证, 已强制下线所有设备。'
              '若不是你本人操作, 建议立即修改密码。',
        ),
        SessionExpiredReason.expired => ('会话已过期', '你的登录会话已过期, 请重新登录以继续使用。'),
      };
      showDialog<void>(
        context: ctx,
        barrierDismissible: false,
        builder: (dctx) => AlertDialog(
          title: Text(title),
          content: Text(body),
          actions: [
            FilledButton(
              onPressed: () => Navigator.of(dctx).pop(),
              child: const Text('去登录'),
            ),
          ],
        ),
      ).whenComplete(() {
        _sessionDialogOpen = false;
      });
    });
  }

  /// macOS App Nap / iOS background → resume 时**仅在 access_token 接近
  /// 过期时**才触发 refresh (refreshIfNearExpiry, 5min 门控)。缓解后台 timer
  /// 被系统暂停 → token 漂到过期之后才发现的场景；同时避免每次 resume 都
  /// 盲刷 (会触发 hubCredentialsProvider 重 emit → 下游连锁)。距过期 >5min
  /// 时后台 refresher 已在管，这里 noop。
  ///
  /// 同时刷新积分余额: 用户从外部支付完回到 app (微信 / 支付宝 webview),
  /// 后端 webhook 已加余额但 Flutter 5min 缓存还是旧数字. resumed 一刷
  /// 立即看到新余额. 失败/无变化也无害 (只是多打一个 GET).
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      ref.read(tokenManagerProvider).refreshIfNearExpiry();
      ref.invalidate(creditsBalanceProvider);
      // 跨设备下行同步补拉:SSE 重连 kick + 一次轻量 syncThreads(节流的,
      // 不阻塞 UI)。后台期间 SSE 可能已被系统掐断,其他设备的消息靠这
      // 一下兜底对齐。
      ref.read(chatSyncManagerProvider).onAppResumed();
    }
  }

  @override
  Widget build(BuildContext context) {
    // Boot the background token refresher (lazy provider — needs to be
    // touched once to start). Runs for the app's lifetime.
    ref.watch(tokenRefresherStarterProvider);
    // Pre-warm login shell env (PATH / LANG / LC_*) so coding workbench's
    // BiuAdapter can spawn `biu` from Dock-launched apps. macOS / Linux
    // only; web / mobile no-op.
    ref.watch(loginShellEnvProvider);
    // S6-3：桌面端自动起 `biu serve` daemon → 注册 brain agent_environments
    // → Agent 模式 NewThreadDialog 立刻有可选 worker。watch 触发 lazy
    // 构建；container.dispose → SIGTERM。web / 移动 / 未登录 时 noop。
    ref.watch(biuDaemonManagerProvider);
    // token 轮换: access_token refresh 后推给运行中的 daemon (热更不重启),
    // 避免 1h TTL 过期后 daemon 401 退出 → brain GC environment → environment_id 报错。
    ref.watch(biuDaemonTokenPusherProvider);
    // 编码任务 100% 本地(D4 / Code-I6)—— codeSync 已删除,无 flusher / realtime。

    // 聊天跨设备下行同步:watch 一次即挂载 —— 冷启动已登录立即 hydrate +
    // 启动 chat realtime 监听;login/logout 由 manager 内部响应 creds 变化。
    // 未登录 noop。数据灌进 Drift,会话列表/消息 UI 经现有 watcher 自动更新。
    ref.watch(chatSyncManagerProvider);

    // Token refresh 失败 → token_manager 强制 signOut 并 bump 这个计数器。
    // 监听上升沿弹一次"会话过期"对话框, 让用户知道为何突然被踢回登录页。
    // 用户主动 signOut (settings UI) 不 bump → 不弹。
    ref.listen<int>(sessionExpiredCountProvider, (prev, next) {
      if (next <= (prev ?? 0)) return;
      _showSessionExpiredDialog();
    });

    // ThemeMode: system / light / dark — settings.theme 决定。settings 未
    // 加载时回退 system。watch settings 让 palette / fontSize / theme 任一
    // 变化都触发 MaterialApp 重 build,buildTheme 重跑;BiuTokens shim 在
    // builder 里同步 _isDark + _currentPalette,让老调用点跟着切。
    final settings = ref.watch(settingsControllerProvider).valueOrNull;
    final themeMode = switch (settings?.theme) {
      ThemePreference.light => ThemeMode.light,
      ThemePreference.dark => ThemeMode.dark,
      _ => ThemeMode.system,
    };
    final palette = settings?.palette ?? PaletteId.inkblueOrange;
    final fontSize = settings?.fontSize ?? FontSize.small;
    // i18n: chat v2 偏好里有 localeOverride 就用，否则跟系统（locale: null）。
    final localeOverride = ref.watch(
      chatPreferencesProvider.select((p) => p.localeOverride),
    );
    final locale = (localeOverride == null || localeOverride.isEmpty)
        ? null
        : Locale(localeOverride);

    return MaterialApp.router(
      title: 'BiuMind',
      scaffoldMessengerKey: rootScaffoldMessengerKey,
      // 全 app 滚动行为收口 — 桌面 overlay 滚动条(静止隐藏/悬停出现),
      // 视觉见 theme_builder.dart scrollbarTheme。
      scrollBehavior: const BiuScrollBehavior(),
      theme: buildTheme(
        palette: palette,
        mode: Brightness.light,
        fontSize: fontSize,
      ),
      darkTheme: buildTheme(
        palette: palette,
        mode: Brightness.dark,
        fontSize: fontSize,
      ),
      themeMode: themeMode,
      locale: locale,
      // builder: 在 MaterialApp 构造完 ThemeData 之后, 进树前同步 BiuTokens
      // shim 全局状态 — Theme.of(context) 已经反映 themeMode 决议结果。
      // 老 BiuTokens.x getter 读这里同步好的 brightness + palette。
      builder: (context, child) {
        BiuTokens.brightness = Theme.of(context).brightness;
        BiuTokens.palette = palette;
        return child ?? const SizedBox.shrink();
      },
      routerConfig: _router,
      debugShowCheckedModeBanner: false,
      // i18n: en + zh today, picks system locale automatically. Adding
      // a language is one ARB + map entry in app_localizations.dart.
      localizationsDelegates: const [
        AppLocalizations.delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
      ],
      supportedLocales: AppLocalizations.supportedLocales,
    );
  }
}

// ─── helpers for origin device 初始化 ────────────────────

String _genUuidV4() {
  // 不依赖 uuid 包 (这里 main.dart 不想拉太多依赖) — 用 dart:math 自家生成
  // 每次启动只调一次, 性能不敏感
  final r = math.Random.secure();
  final bytes = List<int>.generate(16, (_) => r.nextInt(256));
  bytes[6] = (bytes[6] & 0x0F) | 0x40; // version 4
  bytes[8] = (bytes[8] & 0x3F) | 0x80; // RFC 4122 variant
  String hex(int b) => b.toRadixString(16).padLeft(2, '0');
  final h = bytes.map(hex).join();
  return '${h.substring(0, 8)}-${h.substring(8, 12)}-${h.substring(12, 16)}-${h.substring(16, 20)}-${h.substring(20)}';
}

String _platformDeviceLabel() {
  if (kIsWeb) return 'Web';
  try {
    final host = io.Platform.localHostname;
    if (host.isNotEmpty) return host;
  } catch (_) {
    /* fallthrough */
  }
  if (io.Platform.isMacOS) return 'macOS';
  if (io.Platform.isLinux) return 'Linux';
  if (io.Platform.isWindows) return 'Windows';
  if (io.Platform.isIOS) return 'iOS';
  if (io.Platform.isAndroid) return 'Android';
  return 'Unknown';
}
