// SecurityPane — 已登录设备 (sessions) self-serve.
//
// Mirrors /v1/identity/me/sessions: list cards, each with a toggle that
// revokes that session. Current device gets a 当前设备 tag and confirms
// before kicking itself off (= server logout + clear local creds + the
// router auto-redirects to /login on creds going null).
//
// Empty state nudges the user to install the mobile client; the
// scan-to-install button is wired up once a mobile build pipeline ships
// — for now it's a noop placeholder.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/ui/biu_card.dart';
import '../../../data/api/identity_client.dart';
import '../../../services/settings_repo.dart';
import '../application/settings_controller.dart';

class SecurityPane extends ConsumerStatefulWidget {
  const SecurityPane({super.key});

  @override
  ConsumerState<SecurityPane> createState() => _SecurityPaneState();
}

class _SessionInfo {
  final String id;
  final String deviceName;
  final String deviceKind;
  final String lastIp;
  final DateTime? lastUsedAt;
  final DateTime expiresAt;
  final DateTime createdAt;
  final int ttlDays;
  final bool isCurrent;

  _SessionInfo({
    required this.id,
    required this.deviceName,
    required this.deviceKind,
    required this.lastIp,
    required this.lastUsedAt,
    required this.expiresAt,
    required this.createdAt,
    required this.ttlDays,
    required this.isCurrent,
  });

  factory _SessionInfo.fromJson(Map<String, dynamic> j) => _SessionInfo(
        id: j['id'] as String? ?? '',
        deviceName: j['device_name'] as String? ?? '未知设备',
        deviceKind: j['device_kind'] as String? ?? 'unknown',
        lastIp: j['last_ip'] as String? ?? '',
        lastUsedAt: _parseDate(j['last_used_at']),
        expiresAt: _parseDate(j['expires_at']) ?? DateTime.now(),
        createdAt: _parseDate(j['created_at']) ?? DateTime.now(),
        ttlDays: (j['ttl_days'] as num?)?.toInt() ?? 0,
        isCurrent: j['is_current'] as bool? ?? false,
      );

  static DateTime? _parseDate(Object? v) {
    if (v is String && v.isNotEmpty) return DateTime.tryParse(v);
    return null;
  }
}

class _SecurityPaneState extends ConsumerState<SecurityPane> {
  bool _loading = false;
  String? _error;
  List<_SessionInfo> _sessions = const [];

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _refresh());
  }

  IdentityClient? _client() {
    final s = ref.read(settingsControllerProvider).valueOrNull;
    final url = (s?.identityUrl ?? '').trim();
    if (url.isEmpty) return null;
    return IdentityClient(Uri.parse(url));
  }

  String? _accessToken() {
    final s = ref.read(settingsControllerProvider).valueOrNull;
    final t = (s?.accessToken ?? '').trim();
    return t.isEmpty ? null : t;
  }

  Future<void> _refresh() async {
    final client = _client();
    final token = _accessToken();
    if (client == null || token == null) {
      setState(() {
        _error = '请先登录后再管理设备';
        _sessions = const [];
      });
      return;
    }
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final raw = await client.listSessions(token);
      final list = raw.map(_SessionInfo.fromJson).toList();
      // current first, then by lastUsedAt desc
      list.sort((a, b) {
        if (a.isCurrent != b.isCurrent) return a.isCurrent ? -1 : 1;
        final aT = a.lastUsedAt ?? a.createdAt;
        final bT = b.lastUsedAt ?? b.createdAt;
        return bT.compareTo(aT);
      });
      setState(() => _sessions = list);
    } on IdentityApiError catch (e) {
      setState(() => _error = e.friendlyMessage);
    } catch (e) {
      setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _revoke(_SessionInfo s) async {
    final confirmText = s.isCurrent
        ? '退出此设备将清除本机登录态。'
        : '确认撤销该设备的授权？该设备将无法继续使用您的账号，需要重新登录。';
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(s.isCurrent ? '退出此设备' : '撤销授权'),
        content: Text(confirmText),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: BiuTokens.error,
            ),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: Text(s.isCurrent ? '退出' : '撤销'),
          ),
        ],
      ),
    );
    if (ok != true || !mounted) return;

    final client = _client();
    final token = _accessToken();
    if (client == null || token == null) return;

    try {
      final wasSelf = await client.revokeSession(token, s.id);
      if (!mounted) return;
      if (wasSelf || s.isCurrent) {
        // 撤了当前 session — 服务端 refresh_token 已撤, 本机 access 还有 ≤15min
        // 自然过期; 立刻清本地 creds, 路由 redirect 到 /login.
        await ref.read(settingsControllerProvider.notifier).signOut();
      } else {
        await _refresh();
      }
    } on IdentityApiError catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('撤销失败: ${e.friendlyMessage}')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('撤销失败: $e')),
      );
    }
  }

  Future<void> _revokeOthers() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('踢出所有其他设备'),
        content: const Text('除当前设备外，立即注销所有授权。被踢出的设备需要重新登录。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: BiuTokens.error),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('确认踢出'),
          ),
        ],
      ),
    );
    if (ok != true || !mounted) return;

    final client = _client();
    final token = _accessToken();
    if (client == null || token == null) return;

    try {
      final n = await client.revokeOtherSessions(token);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('已踢出 $n 台设备')),
      );
      await _refresh();
    } on IdentityApiError catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('操作失败: ${e.friendlyMessage}')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('操作失败: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final s = ref.watch(settingsControllerProvider).valueOrNull ??
        const AppSettings();
    final loggedIn =
        (s.userEmail ?? '').isNotEmpty && (s.accessToken ?? '').isNotEmpty;

    final hasOthers = _sessions.where((x) => !x.isCurrent).isNotEmpty;

    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 720),
        child: ListView(
          padding: const EdgeInsets.symmetric(
              horizontal: BiuTokens.space5, vertical: BiuTokens.space6),
          children: [
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text('已登录设备',
                          style: Theme.of(context).textTheme.headlineLarge),
                      const SizedBox(height: BiuTokens.space1),
                      Text(
                        '查看并管理已登录到您账号的所有设备。撤销授权后该设备需重新登录。',
                        style: TextStyle(
                            fontSize: 13, color: BiuTokens.textSecondary),
                      ),
                    ],
                  ),
                ),
                IconButton(
                  tooltip: '刷新',
                  icon: const Icon(Icons.refresh, size: 18),
                  onPressed: _loading ? null : _refresh,
                ),
              ],
            ),
            const SizedBox(height: BiuTokens.space5),

            if (!loggedIn)
              _infoBanner('请先登录后再管理设备', warn: true)
            else if (_loading && _sessions.isEmpty)
              const Center(
                  child: Padding(
                padding: EdgeInsets.all(BiuTokens.space5),
                child: CircularProgressIndicator(),
              ))
            else if (_error != null)
              _infoBanner(_error!, warn: true)
            else ...[
              for (final s in _sessions) ...[
                _SessionCard(info: s, onRevoke: () => _revoke(s)),
                const SizedBox(height: BiuTokens.space3),
              ],
              if (_sessions.length == 1) _emptyOthersHint(),
              if (hasOthers) ...[
                const SizedBox(height: BiuTokens.space3),
                Container(
                  padding: const EdgeInsets.all(BiuTokens.space4),
                  decoration: BoxDecoration(
                    color: BiuTokens.errorSoft,
                    borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
                    border:
                        Border.all(color: BiuTokens.error.withValues(alpha: 0.2)),
                  ),
                  child: Row(
                    children: [
                      const Icon(Icons.warning_amber_rounded,
                          size: 18, color: BiuTokens.error),
                      const SizedBox(width: BiuTokens.space2),
                      Expanded(
                        child: Text(
                          '踢出所有其他设备\n除当前设备外，立即注销所有授权。',
                          style: TextStyle(
                              fontSize: 13, color: BiuTokens.textSecondary),
                        ),
                      ),
                      OutlinedButton(
                        onPressed: _loading ? null : _revokeOthers,
                        style: OutlinedButton.styleFrom(
                          foregroundColor: BiuTokens.error,
                          side: const BorderSide(color: BiuTokens.error),
                        ),
                        child: const Text('一键踢出'),
                      ),
                    ],
                  ),
                ),
              ],
            ],
          ],
        ),
      ),
    );
  }

  Widget _infoBanner(String text, {bool warn = false}) {
    final color = warn ? BiuTokens.error : BiuTokens.purple;
    final bg = warn ? BiuTokens.errorSoft : BiuTokens.purpleSoft;
    return Container(
      padding: EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Row(
        children: [
          Icon(warn ? Icons.error_outline : Icons.info_outline,
              size: 16, color: color),
          SizedBox(width: BiuTokens.space2),
          Expanded(child: Text(text, style: TextStyle(fontSize: 13, color: color))),
        ],
      ),
    );
  }

  Widget _emptyOthersHint() {
    return Container(
      padding: EdgeInsets.all(BiuTokens.space4),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: Row(
        children: [
          Icon(Icons.smartphone_outlined, size: 20, color: BiuTokens.textMuted),
          const SizedBox(width: BiuTokens.space3),
          Expanded(
            child: Text(
              '还没有其他设备登录\n在手机上下载 BiuMind 客户端，登录同一账号即可同步。',
              style: TextStyle(fontSize: 13, color: BiuTokens.textSecondary),
            ),
          ),
        ],
      ),
    );
  }
}

class _SessionCard extends StatelessWidget {
  const _SessionCard({required this.info, required this.onRevoke});
  final _SessionInfo info;
  final VoidCallback onRevoke;

  IconData get _icon {
    switch (info.deviceKind) {
      case 'mobile':
        return Icons.smartphone_outlined;
      case 'browser':
        return Icons.public_outlined;
      case 'desktop':
        return Icons.computer_outlined;
      default:
        return Icons.vpn_key_outlined;
    }
  }

  String _humanLastUsed(DateTime? t) {
    if (t == null) return '未知';
    final delta = DateTime.now().difference(t);
    if (delta.inMinutes < 1) return '刚刚';
    if (delta.inHours < 1) return '${delta.inMinutes} 分钟前';
    if (delta.inDays < 1) return '${delta.inHours} 小时前';
    if (delta.inDays < 30) return '${delta.inDays} 天前';
    return '${(delta.inDays / 30).floor()} 个月前';
  }

  String _formatExpiry(DateTime t) =>
      '${t.year.toString().padLeft(4, "0")}-${t.month.toString().padLeft(2, "0")}-${t.day.toString().padLeft(2, "0")}';

  @override
  Widget build(BuildContext context) {
    final ttl = info.ttlDays > 0 ? '${info.ttlDays} 天' : '未知';
    final lastIpLine = info.lastIp.isEmpty
        ? '最近活跃：${_humanLastUsed(info.lastUsedAt)}'
        : '最近活跃：${_humanLastUsed(info.lastUsedAt)} · ${info.lastIp}';

    // 当前设备 selected=true → 1.5px brand 边框,加微染突出;其他设备
     // lift=0 静态 + hairline 边框,跟会话列表 prototype 视觉一致。
    return BiuCard(
      lift: 0,
      selected: info.isCurrent,
      padding: const EdgeInsets.all(BiuTokens.space4),
      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 40,
            height: 40,
            decoration: BoxDecoration(
              color: BiuTokens.surfaceMuted,
              borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
            ),
            child: Icon(_icon, size: 20, color: BiuTokens.textSecondary),
          ),
          const SizedBox(width: BiuTokens.space3),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Flexible(
                      child: Text(
                        info.deviceName,
                        style: const TextStyle(
                            fontSize: 15, fontWeight: FontWeight.w600),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    if (info.isCurrent) ...[
                      const SizedBox(width: BiuTokens.space2),
                      Container(
                        padding: const EdgeInsets.symmetric(
                            horizontal: 6, vertical: 2),
                        decoration: BoxDecoration(
                          color: BiuTokens.purple,
                          borderRadius: BorderRadius.circular(3),
                        ),
                        child: const Text(
                          '当前设备',
                          style: TextStyle(
                              fontSize: 11,
                              color: Colors.white,
                              fontWeight: FontWeight.w500),
                        ),
                      ),
                    ],
                  ],
                ),
                const SizedBox(height: 4),
                Text(
                  '已授权该设备访问您的 BiuMind 账号',
                  style: TextStyle(fontSize: 12, color: BiuTokens.textSecondary),
                ),
                const SizedBox(height: 6),
                Text(
                  lastIpLine,
                  style: TextStyle(
                      fontSize: 12, color: BiuTokens.textMuted),
                ),
                Text(
                  '生效期：$ttl；本次授权将于 ${_formatExpiry(info.expiresAt)} 到期',
                  style: TextStyle(
                      fontSize: 12, color: BiuTokens.textMuted),
                ),
              ],
            ),
          ),
          const SizedBox(width: BiuTokens.space3),
          TextButton(
            onPressed: onRevoke,
            style: TextButton.styleFrom(
              foregroundColor: BiuTokens.error,
              textStyle: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600),
            ),
            child: Text(info.isCurrent ? '退出' : '撤销'),
          ),
        ],
      ),
    );
  }
}
