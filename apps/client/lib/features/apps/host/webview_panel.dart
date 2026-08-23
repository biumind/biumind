// WebViewPanel — embedded webview + browser bar for M12.
//
// Design decisions baked in here:
//
//   * Single shared profile across all webview Apps. Cookie / storage
//     isolation between different domains is already provided by the
//     browser's same-origin model; per-install isolation was retired
//     after the v2.0 design review. See FAQ in DevPlan §M12.
//
//   * Anchor-host navigation guard. The panel records the host of the
//     initial URL; navigation to a different host pops a confirmation
//     dialog before allowing the load (anti-phishing). Same host (any
//     subdomain) is allowed silently.
//
//   * JS bridge disabled. We never call addJavaScriptChannel — webview
//     apps are content surfaces, not API endpoints back into BiuMind.
//
//   * No file:// / data:// schemes. Navigation is restricted to
//     http(s):// to keep the attack surface minimal.
//
//   * Web platform fallback. webview_flutter does not support web; we
//     show a "open externally" placeholder and a copy-link button.
//     (M1.14: 判定从裸 kIsWeb 改为读 PlatformCaps.hasEmbeddedWebView —
//     Windows/Linux 同样没有 webview 引擎，一并走 _WebFallback。)
//
// Cookie cleanup on uninstall is handled by webview_panel's static
// helper [WebViewPanel.clearForOrigin], invoked from the install
// settings flow when the user removes a webview App.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';
import 'package:webview_flutter/webview_flutter.dart';

import '../../../app/theme.dart';
import '../../../core/platform/platform_caps.dart';

/// eTLD+1 误判修复（M1.14）：裸末两段比较会把 `a.com.cn` / `b.com.cn`
/// 判成同站跳过跨站确认。这里对已知二级公共后缀（com.cn / co.jp 等最
/// 小清单）回退到末三段比较；IP 字面量（127.0.0.1 —— repo app 的本机
/// 地址）不做后缀比较，直接 false。
bool sameRegistrableSuffix(String a, String b) {
  // 已知二级公共后缀最小清单 —— 不是完整 PSL，只挡主流错判。
  const secondLevelPublicSuffixes = {
    'com.cn', 'org.cn', 'net.cn', 'edu.cn', 'gov.cn',
    'co.jp', 'or.jp', 'ne.jp', 'ac.jp',
    'co.uk', 'org.uk',
    'com.au', 'net.au',
    'com.tw', 'com.hk', 'com.sg',
  };
  bool isIpLiteral(String h) =>
      RegExp(r'^\d{1,3}(\.\d{1,3}){3}$').hasMatch(h) || h.contains(':');
  if (isIpLiteral(a) || isIpLiteral(b)) return false;
  final sa = a.toLowerCase().split('.');
  final sb = b.toLowerCase().split('.');
  if (sa.length < 2 || sb.length < 2) return false;
  String last(List<String> s, int n) => s.skip(s.length - n).join('.');
  // 末两段命中已知公共后缀 → 真正的可注册段是末三段。
  final need = secondLevelPublicSuffixes.contains(last(sa, 2)) ||
          secondLevelPublicSuffixes.contains(last(sb, 2))
      ? 3
      : 2;
  if (sa.length < need || sb.length < need) return false;
  return last(sa, need) == last(sb, need);
}

class WebViewPanel extends ConsumerStatefulWidget {
  const WebViewPanel({
    super.key,
    required this.initialUrl,
    this.title,
    this.showBrowserBar = true,
  });

  final String initialUrl;

  /// 面板标题 —— 当前仅作语义属性（伪独立窗口页自绘标题栏时用），
  /// 不在面板内渲染。
  final String? title;

  /// 是否渲染浏览器工具条（后退/前进/地址/复制/外部打开）。伪独立窗
  /// 口默认保持 true（现有行为）；嵌入狭小布局时可关。
  final bool showBrowserBar;

  @override
  ConsumerState<WebViewPanel> createState() => _WebViewPanelState();

  /// Clear cookies + storage scoped to the given origin. Called from
  /// the uninstall flow before the install row is deleted so the user
  /// doesn't have to log out manually.
  ///
  /// [caps] 传入当前平台能力（M1.14）：无嵌入式 webview 的平台
  /// （Windows / Linux / Web）没有可清的存储，早退。不传时退回
  /// PlatformCaps.detect() —— 调用方在 widget 上下文应尽量传
  /// `ref.read(platformCapsProvider)`。
  static Future<void> clearForOrigin(Uri origin, {PlatformCaps? caps}) async {
    if (!(caps ?? PlatformCaps.detect()).hasEmbeddedWebView) return;
    // webview_flutter exposes a single global cookie manager; per-origin
    // delete is supported via setCookie with negative expiry but the
    // simpler path is clearCookies() — which clears the entire panel
    // dataStore. For v2.0 we err on the side of "clean enough":
    //
    //  - The user is removing a webview app; if they have other webview
    //    apps logged in, those will be cleared too. The trade-off is
    //    acceptable because (a) most users have ≤ 3 webview apps,
    //    (b) re-login per app is a one-time cost, (c) per-origin
    //    delete requires platform-specific code we'd rather defer.
    //
    // v2.5 plan: add platform-channel implementations of
    // WKWebsiteDataStore.removeData(forDomain:) on iOS/macOS and
    // CookieManager.removeAllCookies(scoped) on Android.
    final cookies = WebViewCookieManager();
    await cookies.clearCookies();
  }
}

class _WebViewPanelState extends ConsumerState<WebViewPanel> {
  late final WebViewController _ctrl;
  late final String _anchorHost;
  bool _ready = false;
  bool _canBack = false;
  bool _canForward = false;
  String _currentUrl = '';
  double _progress = 0;

  /// 主框架加载失败（断网 / DNS / 连接拒绝）—— 展示错误态而非无限
  /// 转圈（M1.14 修复）。非主框架资源失败不置位。
  WebResourceError? _loadError;

  @override
  void initState() {
    super.initState();
    _anchorHost = Uri.tryParse(widget.initialUrl)?.host ?? '';
    // 无嵌入式 webview 能力的平台（Windows / Linux / Web）不创建
    // controller —— WebViewController() 会直接抛 MissingPluginException。
    // fallback 在 build 里渲染。
    if (!ref.read(platformCapsProvider).hasEmbeddedWebView) return;
    _ctrl = WebViewController()
      ..setJavaScriptMode(JavaScriptMode.unrestricted)
      ..setBackgroundColor(BiuTokens.surface)
      ..setNavigationDelegate(NavigationDelegate(
        onProgress: (p) => setState(() => _progress = p / 100),
        onPageStarted: (u) => setState(() {
          _currentUrl = u;
          _progress = 0.05;
          _loadError = null;
        }),
        onPageFinished: (u) async {
          final back = await _ctrl.canGoBack();
          final fwd = await _ctrl.canGoForward();
          if (!mounted) return;
          setState(() {
            _currentUrl = u;
            _progress = 0;
            _canBack = back;
            _canForward = fwd;
            _ready = true;
          });
        },
        onWebResourceError: (err) {
          // 只关心主框架失败；图片/脚本等资源失败交给页面自身容错。
          if (err.isForMainFrame ?? false) {
            setState(() => _loadError = err);
          }
        },
        onNavigationRequest: _gateNavigation,
      ))
      ..loadRequest(Uri.parse(widget.initialUrl));
  }

  /// onNavigationRequest gate: anti-phishing + scheme allowlist.
  /// Returning NavigationDecision.prevent + showing a dialog is safer
  /// than a silent block — the user gets to make the call.
  Future<NavigationDecision> _gateNavigation(NavigationRequest req) async {
    final next = Uri.tryParse(req.url);
    if (next == null) return NavigationDecision.prevent;

    // Scheme allowlist — block javascript:, data:, file:, ftp:, ws:.
    if (next.scheme != 'http' && next.scheme != 'https') {
      // about:blank shows up during the WebView's own bootstrap on
      // some platforms; allow that one.
      if (next.scheme == 'about') return NavigationDecision.navigate;
      return NavigationDecision.prevent;
    }

    if (_anchorHost.isEmpty) return NavigationDecision.navigate;
    if (_sameHost(next.host, _anchorHost)) {
      return NavigationDecision.navigate;
    }

    // Cross-host — confirm. Most legit sites' OAuth flows transit
    // through accounts.* / login.* / id.* on the same TLD+1; we
    // auto-allow that to keep sign-in flows smooth without disabling
    // the guard entirely.
    if (sameRegistrableSuffix(next.host, _anchorHost)) {
      return NavigationDecision.navigate;
    }
    final approved = await _confirmCrossHost(next);
    return approved ? NavigationDecision.navigate : NavigationDecision.prevent;
  }

  bool _sameHost(String a, String b) => a.toLowerCase() == b.toLowerCase();

  Future<bool> _confirmCrossHost(Uri next) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        icon: const Icon(Icons.warning_amber_rounded),
        title: const Text('离开当前站点'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('正在跳转到 ${next.host}',
                style: Theme.of(ctx).textTheme.bodyMedium),
            const SizedBox(height: 8),
            Text('原始站点：$_anchorHost',
                style: Theme.of(ctx).textTheme.bodySmall),
            const SizedBox(height: 12),
            Text(
              '该站点是这个 WebView 应用的非预期跳转，请确认是否信任。',
              style: Theme.of(ctx).textTheme.bodySmall?.copyWith(
                  color: Theme.of(ctx).colorScheme.onSurfaceVariant),
            ),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('继续访问'),
          ),
        ],
      ),
    );
    return ok ?? false;
  }

  @override
  Widget build(BuildContext context) {
    // M1.14: 从裸 kIsWeb 改为读 caps —— Windows / Linux 同样没有
    // webview 引擎（pubspec 只声明 wkwebview/android 实现），走同一个
    // 外部浏览器 fallback。
    if (!ref.watch(platformCapsProvider).hasEmbeddedWebView) {
      return _WebFallback(url: widget.initialUrl);
    }
    return Column(
      children: [
        if (widget.showBrowserBar)
          _BrowserBar(
            url: _currentUrl.isEmpty ? widget.initialUrl : _currentUrl,
            canBack: _canBack,
            canForward: _canForward,
            onBack: () => _ctrl.goBack(),
            onForward: () => _ctrl.goForward(),
            onReload: () => _ctrl.reload(),
            onCopy: () {
              Clipboard.setData(ClipboardData(text: _currentUrl));
              ScaffoldMessenger.of(context).showSnackBar(
                const SnackBar(content: Text('链接已复制'),
                    duration: Duration(milliseconds: 1200)),
              );
            },
            onOpenExternally: () => launchUrl(Uri.parse(_currentUrl),
                mode: LaunchMode.externalApplication),
          ),
        if (_progress > 0 && _progress < 1)
          LinearProgressIndicator(value: _progress, minHeight: 2),
        Expanded(
          child: _loadError != null && !_ready
              ? _LoadErrorView(
                  url: _currentUrl.isEmpty ? widget.initialUrl : _currentUrl,
                  error: _loadError!,
                  onRetry: () {
                    setState(() {
                      _loadError = null;
                      _progress = 0.05;
                    });
                    _ctrl.loadRequest(Uri.parse(widget.initialUrl));
                  },
                )
              : _ready || _currentUrl.isNotEmpty
                  ? WebViewWidget(controller: _ctrl)
                  : const Center(child: CircularProgressIndicator()),
        ),
      ],
    );
  }
}

class _BrowserBar extends StatelessWidget {
  const _BrowserBar({
    required this.url,
    required this.canBack,
    required this.canForward,
    required this.onBack,
    required this.onForward,
    required this.onReload,
    required this.onCopy,
    required this.onOpenExternally,
  });

  final String url;
  final bool canBack;
  final bool canForward;
  final VoidCallback onBack;
  final VoidCallback onForward;
  final VoidCallback onReload;
  final VoidCallback onCopy;
  final VoidCallback onOpenExternally;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      height: 36,
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHigh,
        border: Border(
          bottom: BorderSide(color: theme.dividerColor.withValues(alpha: 0.3)),
        ),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 4),
      child: Row(
        children: [
          _BarBtn(
              icon: Icons.arrow_back, tooltip: '后退',
              onTap: canBack ? onBack : null),
          _BarBtn(
              icon: Icons.arrow_forward, tooltip: '前进',
              onTap: canForward ? onForward : null),
          _BarBtn(icon: Icons.refresh, tooltip: '刷新', onTap: onReload),
          const SizedBox(width: 4),
          Expanded(
            child: Container(
              height: 26,
              padding: const EdgeInsets.symmetric(horizontal: 8),
              alignment: Alignment.centerLeft,
              decoration: BoxDecoration(
                color: theme.colorScheme.surface,
                borderRadius: BorderRadius.circular(4),
              ),
              child: Text(
                url,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: theme.textTheme.bodySmall,
              ),
            ),
          ),
          _BarBtn(icon: Icons.copy, tooltip: '复制链接', onTap: onCopy),
          _BarBtn(
              icon: Icons.open_in_new, tooltip: '在浏览器中打开',
              onTap: onOpenExternally),
        ],
      ),
    );
  }
}

class _BarBtn extends StatelessWidget {
  const _BarBtn({required this.icon, required this.tooltip, this.onTap});
  final IconData icon;
  final String tooltip;
  final VoidCallback? onTap;
  @override
  Widget build(BuildContext context) {
    return Tooltip(
      message: tooltip,
      child: IconButton(
        iconSize: 16,
        padding: EdgeInsets.zero,
        constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
        icon: Icon(icon),
        onPressed: onTap,
      ),
    );
  }
}

/// 主框架加载失败的错误态（M1.14）—— 断网 / DNS / 连接拒绝不再无限
/// 转圈，给可读原因 + 重试。
class _LoadErrorView extends StatelessWidget {
  const _LoadErrorView({
    required this.url,
    required this.error,
    required this.onRetry,
  });

  final String url;
  final WebResourceError error;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space5),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.cloud_off_outlined,
                size: 32, color: theme.colorScheme.onSurfaceVariant),
            const SizedBox(height: BiuTokens.space2),
            Text('页面加载失败', style: theme.textTheme.titleMedium),
            const SizedBox(height: BiuTokens.space2),
            SelectableText(
              url,
              style: theme.textTheme.bodySmall,
              textAlign: TextAlign.center,
            ),
            if ((error.description).isNotEmpty) ...[
              const SizedBox(height: BiuTokens.space2),
              Text(
                error.description,
                textAlign: TextAlign.center,
                style: theme.textTheme.bodySmall?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant),
              ),
            ],
            const SizedBox(height: BiuTokens.space3),
            FilledButton.icon(
              icon: const Icon(Icons.refresh, size: 18),
              label: const Text('重试'),
              onPressed: onRetry,
            ),
          ],
        ),
      ),
    );
  }
}

class _WebFallback extends StatelessWidget {
  const _WebFallback({required this.url});
  final String url;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space5),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text('在浏览器中打开', style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: BiuTokens.space2),
            SelectableText(url, style: Theme.of(context).textTheme.bodySmall),
            const SizedBox(height: BiuTokens.space3),
            FilledButton.icon(
              icon: const Icon(Icons.open_in_new),
              label: const Text('打开'),
              onPressed: () => launchUrl(Uri.parse(url),
                  mode: LaunchMode.externalApplication),
            ),
            const SizedBox(height: BiuTokens.space2),
            Text(
              '当前平台无嵌入式 webview；请改用外部浏览器，或在 macOS / 移动客户端使用嵌入体验。',
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.bodySmall?.copyWith(
                  color: Theme.of(context).colorScheme.onSurfaceVariant),
            ),
          ],
        ),
      ),
    );
  }
}
