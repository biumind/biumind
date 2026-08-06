// TokensPane — API Tokens (PAT) self-serve.
//
// Mirrors /v1/identity/me/tokens. Users mint long-lived bearer tokens
// for MCP servers, CI scripts, and other programmatic-access scenarios
// where pasting a 24h-expiring session JWT isn't practical.
//
// Created tokens display their secret ONCE in a copy-only dialog —
// the server never returns it again, so this is the only chance to
// capture it. The list view shows redacted prefixes + scopes for
// later identification + revoke.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/ui/biu_card.dart';
import '../../../core/ui/biu_text_field.dart';
import '../../../data/api/identity_client.dart';
import '../application/settings_controller.dart';

class TokensPane extends ConsumerStatefulWidget {
  const TokensPane({super.key});

  @override
  ConsumerState<TokensPane> createState() => _TokensPaneState();
}

class _TokenInfo {
  final String id;
  final String name;
  final String redacted;
  final List<String> scopes;
  final DateTime? lastUsedAt;
  final DateTime expiresAt;
  final DateTime? revokedAt;
  final DateTime createdAt;

  _TokenInfo({
    required this.id,
    required this.name,
    required this.redacted,
    required this.scopes,
    required this.lastUsedAt,
    required this.expiresAt,
    required this.revokedAt,
    required this.createdAt,
  });

  factory _TokenInfo.fromJson(Map<String, dynamic> j) {
    DateTime? parseTs(Object? v) {
      if (v is String && v.isNotEmpty) return DateTime.tryParse(v);
      return null;
    }
    return _TokenInfo(
      id: j['id'] as String,
      name: (j['name'] as String?) ?? '',
      redacted: (j['redacted'] as String?) ?? '',
      scopes: ((j['scopes'] as List?) ?? const [])
          .whereType<String>()
          .toList(growable: false),
      lastUsedAt: parseTs(j['last_used_at']),
      expiresAt: parseTs(j['expires_at']) ?? DateTime.now(),
      revokedAt: parseTs(j['revoked_at']),
      createdAt: parseTs(j['created_at']) ?? DateTime.now(),
    );
  }

  bool get isRevoked => revokedAt != null;
  bool get isExpired => !isRevoked && DateTime.now().isAfter(expiresAt);
}

class _TokensPaneState extends ConsumerState<TokensPane> {
  List<_TokenInfo> _tokens = const [];
  bool _loading = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _refresh();
    });
  }

  IdentityClient _client() {
    final s = ref.read(settingsControllerProvider).valueOrNull;
    final url = s?.identityUrl;
    if (url == null || url.isEmpty) {
      throw const _NoCreds();
    }
    return IdentityClient(Uri.parse(url));
  }

  String _accessToken() {
    final s = ref.read(settingsControllerProvider).valueOrNull;
    final tok = s?.accessToken;
    if (tok == null || tok.isEmpty) {
      throw const _NoCreds();
    }
    return tok;
  }

  Future<void> _refresh() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final raw = await _client().listApiTokens(_accessToken());
      if (!mounted) return;
      setState(() {
        _tokens = raw.map(_TokenInfo.fromJson).toList(growable: false);
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = e.toString());
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _create() async {
    final result = await showDialog<_CreateTokenResult>(
      context: context,
      builder: (_) => const _CreateTokenDialog(),
    );
    if (result == null || !mounted) return;

    setState(() => _loading = true);
    try {
      final raw = await _client().createApiToken(
        _accessToken(),
        name: result.name,
        scopes: result.scopes,
        ttlSeconds: result.ttlSeconds,
      );
      if (!mounted) return;
      final secret = raw['secret'] as String? ?? '';
      // Show secret once. Block until user dismisses so they can't
      // accidentally close the page and lose it.
      await showDialog<void>(
        context: context,
        barrierDismissible: false,
        builder: (_) => _SecretDialog(secret: secret),
      );
      await _refresh();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('创建失败：$e')),
      );
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _revoke(_TokenInfo t) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (c) => AlertDialog(
        title: const Text('撤销 PAT'),
        content: Text(
          '撤销 "${t.name}" 后, 任何使用该 token 的客户端会立即失败。'
          '当前部署不强制撤销 (token 在 expires_at 之前仍有效); '
          '需要立即生效请缩短 TTL 重新签发。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(c).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(c).pop(true),
            style: FilledButton.styleFrom(backgroundColor: BiuTokens.error),
            child: const Text('撤销'),
          ),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    try {
      await _client().revokeApiToken(_accessToken(), t.id);
      await _refresh();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('撤销失败：$e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(BiuTokens.space5),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              const Expanded(
                child: Text(
                  'API Tokens (PAT)',
                  style: TextStyle(fontSize: 16, fontWeight: FontWeight.w600),
                ),
              ),
              IconButton(
                tooltip: '刷新',
                icon: const Icon(Icons.refresh, size: 18),
                onPressed: _loading ? null : _refresh,
              ),
              FilledButton.icon(
                onPressed: _loading ? null : _create,
                icon: const Icon(Icons.add, size: 16),
                label: const Text('创建'),
              ),
            ],
          ),
          const SizedBox(height: BiuTokens.space2),
          Text(
            '长期有效 bearer token，给 MCP server / CI 脚本 / 自动化使用。'
            '创建后 secret 仅显示一次。',
            style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
          ),
          const SizedBox(height: BiuTokens.space4),
          if (_error != null)
            Container(
              padding: const EdgeInsets.all(BiuTokens.space3),
              margin: const EdgeInsets.only(bottom: BiuTokens.space3),
              decoration: BoxDecoration(
                color: BiuTokens.errorSoft,
                borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
              ),
              child: Text(_error!,
                  style: const TextStyle(color: BiuTokens.error)),
            ),
          Expanded(
            child: _loading && _tokens.isEmpty
                ? const Center(child: CircularProgressIndicator())
                : _tokens.isEmpty
                    ? const _EmptyView()
                    : ListView.separated(
                        itemCount: _tokens.length,
                        separatorBuilder: (_, _) =>
                            const SizedBox(height: BiuTokens.space2),
                        itemBuilder: (_, i) =>
                            _TokenCard(token: _tokens[i], onRevoke: _revoke),
                      ),
          ),
        ],
      ),
    );
  }
}

// ── widgets ───────────────────────────────────────────────────

class _EmptyView extends StatelessWidget {
  const _EmptyView();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Text(
        '还没有 token — 点右上角「创建」开一个',
        style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
      ),
    );
  }
}

class _TokenCard extends StatelessWidget {
  const _TokenCard({required this.token, required this.onRevoke});
  final _TokenInfo token;
  final void Function(_TokenInfo) onRevoke;

  @override
  Widget build(BuildContext context) {
    return BiuCard(
      lift: 0,
      padding: const EdgeInsets.all(BiuTokens.space3),
      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Text(token.name,
                        style: const TextStyle(
                            fontSize: 13, fontWeight: FontWeight.w600)),
                    const SizedBox(width: BiuTokens.space2),
                    if (token.isRevoked)
                      _StatusChip(
                          label: '已撤销', color: BiuTokens.error),
                    if (token.isExpired)
                      _StatusChip(
                          label: '已过期', color: BiuTokens.textMuted),
                    if (token.scopes.isNotEmpty)
                      _StatusChip(
                          label: token.scopes.join(', '),
                          color: BiuTokens.textSecondary),
                  ],
                ),
                const SizedBox(height: 4),
                Text(
                  token.redacted,
                  style: TextStyle(
                    fontSize: 11,
                    color: BiuTokens.textMuted,
                    fontFamily: 'JetBrains Mono, ui-monospace, monospace',
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  _subtitleFor(token),
                  style: TextStyle(
                      fontSize: 10, color: BiuTokens.textMuted),
                ),
              ],
            ),
          ),
          if (!token.isRevoked)
            TextButton(
              onPressed: () => onRevoke(token),
              child: const Text('撤销'),
            ),
        ],
      ),
    );
  }

  static String _subtitleFor(_TokenInfo t) {
    final last = t.lastUsedAt == null
        ? '从未使用'
        : '上次使用 ${_relTime(t.lastUsedAt!)}';
    final exp = '到期 ${t.expiresAt.toLocal().toString().substring(0, 10)}';
    return '$last · $exp';
  }

  static String _relTime(DateTime t) {
    final d = DateTime.now().difference(t);
    if (d.inMinutes < 1) return '刚刚';
    if (d.inHours < 1) return '${d.inMinutes} 分钟前';
    if (d.inDays < 1) return '${d.inHours} 小时前';
    if (d.inDays < 30) return '${d.inDays} 天前';
    return t.toLocal().toString().substring(0, 10);
  }
}

class _StatusChip extends StatelessWidget {
  const _StatusChip({required this.label, required this.color});
  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      margin: const EdgeInsets.only(right: 4),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        label,
        style: TextStyle(fontSize: 9, color: color, fontWeight: FontWeight.w600),
      ),
    );
  }
}

// ── create dialog ─────────────────────────────────────────────

class _CreateTokenResult {
  final String name;
  final List<String> scopes;
  final int? ttlSeconds;
  const _CreateTokenResult({
    required this.name,
    required this.scopes,
    required this.ttlSeconds,
  });
}

class _CreateTokenDialog extends StatefulWidget {
  const _CreateTokenDialog();

  @override
  State<_CreateTokenDialog> createState() => _CreateTokenDialogState();
}

class _CreateTokenDialogState extends State<_CreateTokenDialog> {
  final _name = TextEditingController();
  bool _scopeRead = true;
  bool _scopeWrite = false;
  int _ttlDays = 365;

  @override
  void dispose() {
    _name.dispose();
    super.dispose();
  }

  void _submit() {
    final name = _name.text.trim();
    if (name.isEmpty) return;
    final scopes = <String>[
      if (_scopeRead) 'read',
      if (_scopeWrite) 'write',
    ];
    Navigator.of(context).pop(_CreateTokenResult(
      name: name,
      scopes: scopes,
      ttlSeconds: _ttlDays * 86400,
    ));
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('创建 API Token'),
      content: SizedBox(
        width: 400,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            BiuTextField(
              controller: _name,
              autofocus: true,
              labelText: '名称',
              hintText: '例如：claude-desktop / ci-deploy',
              onSubmitted: (_) => _submit(),
            ),
            const SizedBox(height: BiuTokens.space3),
            Text('权限',
                style: TextStyle(
                    fontSize: 11, color: BiuTokens.textMuted)),
            CheckboxListTile(
              dense: true,
              contentPadding: EdgeInsets.zero,
              value: _scopeRead,
              onChanged: (v) => setState(() => _scopeRead = v ?? false),
              title: const Text('read — 读取知识库 / 搜索 / 列表'),
            ),
            CheckboxListTile(
              dense: true,
              contentPadding: EdgeInsets.zero,
              value: _scopeWrite,
              onChanged: (v) => setState(() => _scopeWrite = v ?? false),
              title: const Text('write — 创建 / 编辑 / 合并 page'),
            ),
            const SizedBox(height: BiuTokens.space3),
            Text('有效期：$_ttlDays 天',
                style: TextStyle(
                    fontSize: 11, color: BiuTokens.textMuted)),
            Slider(
              value: _ttlDays.toDouble(),
              min: 7,
              max: 365,
              divisions: 51, // 7-day steps
              label: '$_ttlDays 天',
              onChanged: (v) => setState(() => _ttlDays = v.round()),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: _submit,
          child: const Text('创建'),
        ),
      ],
    );
  }
}

// ── secret dialog (one-time view) ─────────────────────────────

class _SecretDialog extends StatelessWidget {
  const _SecretDialog({required this.secret});
  final String secret;

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Token 已创建'),
      content: SizedBox(
        width: 500,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Builder(builder: (ctx) {
              final c = Theme.of(ctx).extension<BiuColors>()!;
              return Container(
                padding: const EdgeInsets.all(BiuTokens.space3),
                decoration: BoxDecoration(
                  color: c.warningSoft,
                  borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
                ),
                child: Row(
                  children: [
                    Icon(Icons.warning_amber_rounded,
                        size: 16, color: c.warning),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(
                        'Secret 仅显示这一次。关闭后无法再查看。立即复制保存。',
                        style: TextStyle(fontSize: 11, color: c.warning),
                      ),
                    ),
                  ],
                ),
              );
            }),
            const SizedBox(height: BiuTokens.space3),
            SelectableText(
              secret,
              style: const TextStyle(
                fontFamily: 'JetBrains Mono, ui-monospace, monospace',
                fontSize: 11,
              ),
            ),
          ],
        ),
      ),
      actions: [
        OutlinedButton.icon(
          onPressed: () async {
            await Clipboard.setData(ClipboardData(text: secret));
            if (!context.mounted) return;
            ScaffoldMessenger.of(context).showSnackBar(
              const SnackBar(content: Text('已复制到剪贴板')),
            );
          },
          icon: const Icon(Icons.copy, size: 14),
          label: const Text('复制'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('我已保存'),
        ),
      ],
    );
  }
}

// ── error sentinel ────────────────────────────────────────────

class _NoCreds implements Exception {
  const _NoCreds();
  @override
  String toString() => '请先登录 BiuMind 账号';
}
