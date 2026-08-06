/// /connect?ar=… —— OAuth 2.1 同意页（外部 AI 客户端授权 biumind 数据）。
///
/// 流程：
///   1. 外部客户端（Claude.ai / Cursor / Codex 等）发起 PKCE 授权
///   2. brain `/v1/wiki/oauth/authorize` 校验 + 302 redirect 到本页 `/connect?ar=...`
///   3. 本页调 `/v1/wiki/oauth/authorize/info?ar=...` 拉 client metadata + scopes
///   4. 用户同意 → POST `/v1/wiki/oauth/grant` → 拿 redirect URL（含 code）
///   5. 把 redirect URL 用 url_launcher 打开（系统默认浏览器接管）
///
/// 简化设计：单页 + 同意/拒绝两按钮 + 列出 scopes。Cancel 关闭页面 +
/// 提示用户回到原客户端。
library;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../../app/theme.dart';
import '../../../../data/api/wiki_client.dart' show OAuthAuthorizeInfo;
import '../../../../data/wiki_providers.dart' show wikiRepositoryProvider;

class OAuthConnectPage extends ConsumerStatefulWidget {
  const OAuthConnectPage({super.key, required this.ar});

  /// 签名后的授权请求 token，brain 通过 ?ar=... 传入。
  final String ar;

  @override
  ConsumerState<OAuthConnectPage> createState() => _OAuthConnectPageState();
}

class _OAuthConnectPageState extends ConsumerState<OAuthConnectPage> {
  bool _loading = true;
  String? _error;
  OAuthAuthorizeInfo? _info;
  bool _granting = false;

  @override
  void initState() {
    super.initState();
    _loadInfo();
  }

  Future<void> _loadInfo() async {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) {
      setState(() {
        _loading = false;
        _error = '未配置后端凭证 — 请先登录';
      });
      return;
    }
    try {
      final info = await repo.client.oauthAuthorizeInfo(widget.ar);
      if (!mounted) return;
      setState(() {
        _info = info;
        _loading = false;
      });
    } on Exception catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  Future<void> _grant() async {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return;
    setState(() => _granting = true);
    try {
      final res = await repo.client.oauthGrant(widget.ar);
      final redirectUrl = res['redirect_url']?.toString() ?? '';
      if (!mounted) return;
      if (redirectUrl.isEmpty) {
        setState(() {
          _granting = false;
          _error = '后端未返回 redirect_url';
        });
        return;
      }
      final uri = Uri.tryParse(redirectUrl);
      if (uri == null) {
        setState(() {
          _granting = false;
          _error = 'redirect_url 格式不正确：$redirectUrl';
        });
        return;
      }
      await launchUrl(uri, mode: LaunchMode.externalApplication);
      if (!mounted) return;
      // 同意完成后展示一个收尾页（用户大概率会切到外部客户端去）
      setState(() {
        _granting = false;
        _info = null;
        _error = null;
      });
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('已同意 — 请回到原客户端继续')),
      );
    } on Exception catch (e) {
      if (!mounted) return;
      setState(() {
        _granting = false;
        _error = '授权失败：$e';
      });
    }
  }

  void _deny() {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('已拒绝授权')),
    );
    if (context.canPop()) {
      context.pop();
    } else {
      context.go('/chat');
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: BiuTokens.bg,
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 480),
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: _buildBody(),
          ),
        ),
      ),
    );
  }

  Widget _buildBody() {
    if (_loading) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return _StateView(
        icon: Icons.error_outline,
        color: BiuTokens.error,
        title: '授权信息加载失败',
        body: _error!,
        primaryLabel: '关闭',
        onPrimary: _deny,
      );
    }
    final info = _info;
    if (info == null) {
      return _StateView(
        icon: Icons.check_circle_outline,
        color: BiuTokens.success,
        title: '授权完成',
        body: '请回到原客户端继续；本页面可以关闭。',
        primaryLabel: '关闭',
        onPrimary: () => context.canPop() ? context.pop() : context.go('/chat'),
      );
    }
    return Container(
      padding: const EdgeInsets.all(24),
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              if (info.logoUri != null) ...[
                ClipRRect(
                  borderRadius: BorderRadius.circular(8),
                  child: Image.network(
                    info.logoUri!,
                    width: 36,
                    height: 36,
                    fit: BoxFit.cover,
                    errorBuilder: (_, _, _) => Icon(
                      Icons.apps_outlined,
                      size: 24,
                      color: BiuTokens.textSecondary,
                    ),
                  ),
                ),
                const SizedBox(width: 10),
              ] else ...[
                Container(
                  width: 36,
                  height: 36,
                  decoration: BoxDecoration(
                    color: BiuTokens.surfaceMuted,
                    borderRadius: BorderRadius.circular(8),
                  ),
                  alignment: Alignment.center,
                  child: Icon(
                    Icons.apps_outlined,
                    size: 18,
                    color: BiuTokens.textSecondary,
                  ),
                ),
                const SizedBox(width: 10),
              ],
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      info.clientName,
                      style: TextStyle(
                        color: BiuTokens.text,
                        fontSize: 16,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                    if (info.clientUri != null)
                      Text(
                        info.clientUri!,
                        style: TextStyle(
                          color: BiuTokens.textMuted,
                          fontSize: 11,
                        ),
                      ),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 16),
          Text(
            '此应用希望访问你的 BiuMind 数据：',
            style: TextStyle(color: BiuTokens.text, fontSize: 13),
          ),
          const SizedBox(height: 8),
          if (info.scopes.isEmpty)
            Text(
              '（未声明 scope —— brain 将按默认 read scope 授权）',
              style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
            )
          else
            Container(
              padding: const EdgeInsets.all(12),
              decoration: BoxDecoration(
                color: BiuTokens.surfaceMuted,
                borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  for (final s in info.scopes)
                    Padding(
                      padding: const EdgeInsets.symmetric(vertical: 3),
                      child: Row(
                        children: [
                          // checkmark = 语义 success(已授权 scope),保留 green
                          // 不跟随 brand — 这是"已确认"通用语义。
                          Icon(Icons.check, size: 14, color: SemanticTokens.success),
                          const SizedBox(width: 6),
                          Expanded(
                            child: Text(
                              _scopeLabel(s),
                              style: TextStyle(
                                color: BiuTokens.text,
                                fontSize: 12,
                              ),
                            ),
                          ),
                        ],
                      ),
                    ),
                ],
              ),
            ),
          const SizedBox(height: 8),
          Text(
            '同意后会跳转回 ${info.redirectUri.isEmpty ? "应用" : Uri.tryParse(info.redirectUri)?.host ?? info.redirectUri}。',
            style: TextStyle(color: BiuTokens.textMuted, fontSize: 11),
          ),
          const SizedBox(height: 20),
          Row(
            children: [
              Expanded(
                child: OutlinedButton(
                  onPressed: _granting ? null : _deny,
                  style: OutlinedButton.styleFrom(
                    foregroundColor: BiuTokens.text,
                    side: BorderSide(color: BiuTokens.borderSubtle),
                    padding: const EdgeInsets.symmetric(vertical: 12),
                  ),
                  child: const Text('拒绝'),
                ),
              ),
              const SizedBox(width: 10),
              Expanded(
                child: FilledButton(
                  onPressed: _granting ? null : _grant,
                  style: FilledButton.styleFrom(
                    backgroundColor: Theme.of(context).colorScheme.primary,
                    padding: const EdgeInsets.symmetric(vertical: 12),
                  ),
                  child: _granting
                      ? const SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(
                            strokeWidth: 2,
                            color: Colors.white,
                          ),
                        )
                      : const Text('同意授权'),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }

  String _scopeLabel(String s) {
    return switch (s) {
      'wiki.read' => '读取知识库内容',
      'wiki.write' => '创建/编辑知识库内容',
      'wiki.search' => '搜索知识库',
      'profile' => '读取你的账户基本信息',
      'email' => '读取邮箱地址',
      _ => s,
    };
  }
}

class _StateView extends StatelessWidget {
  const _StateView({
    required this.icon,
    required this.color,
    required this.title,
    required this.body,
    required this.primaryLabel,
    required this.onPrimary,
  });
  final IconData icon;
  final Color color;
  final String title;
  final String body;
  final String primaryLabel;
  final VoidCallback onPrimary;

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 48, color: color),
        const SizedBox(height: 16),
        Text(
          title,
          style: TextStyle(
            color: BiuTokens.text,
            fontSize: 16,
            fontWeight: FontWeight.w700,
          ),
        ),
        const SizedBox(height: 8),
        SelectableText(
          body,
          textAlign: TextAlign.center,
          style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
        ),
        const SizedBox(height: 16),
        FilledButton(
          onPressed: onPrimary,
          style: FilledButton.styleFrom(
              backgroundColor: Theme.of(context).colorScheme.primary),
          child: Text(primaryLabel),
        ),
      ],
    );
  }
}
