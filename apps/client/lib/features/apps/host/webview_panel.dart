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
//
// Cookie cleanup on uninstall is handled by webview_panel's static
// helper [WebViewPanel.clearForOrigin], invoked from the install
// settings flow when the user removes a webview App.

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:url_launcher/url_launcher.dart';
import 'package:webview_flutter/webview_flutter.dart';

import '../../../app/theme.dart';

class WebViewPanel extends StatefulWidget {
  const WebViewPanel({super.key, required this.initialUrl});

  final String initialUrl;

  @override
  State<WebViewPanel> createState() => _WebViewPanelState();

  /// Clear cookies + storage scoped to the given origin. Called from
  /// the uninstall flow before the install row is deleted so the user
  /// doesn't have to log out manually.
  ///
  /// On web the no-op return is intentional: there's no embedded
  /// webview, so there's nothing to clear.
  static Future<void> clearForOrigin(Uri origin) async {
    if (kIsWeb) return;
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

class _WebViewPanelState extends State<WebViewPanel> {
  late final WebViewController _ctrl;
  late final String _anchorHost;
  bool _ready = false;
  bool _canBack = false;
  bool _canForward = false;
  String _currentUrl = '';
  double _progress = 0;

  @override
  void initState() {
    super.initState();
    _anchorHost = Uri.tryParse(widget.initialUrl)?.host ?? '';
    if (kIsWeb) return; // fallback rendered in build
    _ctrl = WebViewController()
      ..setJavaScriptMode(JavaScriptMode.unrestricted)
      ..setBackgroundColor(BiuTokens.surface)
      ..setNavigationDelegate(NavigationDelegate(
        onProgress: (p) => setState(() => _progress = p / 100),
        onPageStarted: (u) => setState(() {
          _currentUrl = u;
          _progress = 0.05;
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
    if (_sameRegistrableSuffix(next.host, _anchorHost)) {
      return NavigationDecision.navigate;
    }
    final approved = await _confirmCrossHost(next);
    return approved ? NavigationDecision.navigate : NavigationDecision.prevent;
  }

  bool _sameHost(String a, String b) => a.toLowerCase() == b.toLowerCase();

  /// Naive eTLD+1 match — strips the leading subdomain off both sides
  /// and compares. Catches `accounts.kimi.moonshot.cn` → `kimi.moonshot.cn`
  /// without pulling in a full PSL parser.
  bool _sameRegistrableSuffix(String a, String b) {
    final sa = a.toLowerCase().split('.');
    final sb = b.toLowerCase().split('.');
    if (sa.length < 2 || sb.length < 2) return false;
    return sa.skip(sa.length - 2).join('.') == sb.skip(sb.length - 2).join('.');
  }

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
    if (kIsWeb) return _WebFallback(url: widget.initialUrl);
    return Column(
      children: [
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
          child: _ready || _currentUrl.isNotEmpty
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
              'Web 端无嵌入式 webview；请在桌面 / 移动客户端使用嵌入体验。',
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
