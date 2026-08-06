// ActivityFeedPane — user-facing event stream.
//
// Mirrors GET /v1/identity/me/activity. Newest events first, infinite
// scroll via `before` cursor. Tap-through to the target (PAT settings,
// page, skill) is left for follow-up — current MVP just renders.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/ui/biu_card.dart';
import '../../../data/api/identity_client.dart';
import '../application/settings_controller.dart';

class ActivityPane extends ConsumerStatefulWidget {
  const ActivityPane({super.key});

  @override
  ConsumerState<ActivityPane> createState() => _ActivityPaneState();
}

class _ActivityEvent {
  final String id;
  final String kind;
  final String summary;
  final String targetType;
  final String targetId;
  final DateTime createdAt;

  _ActivityEvent({
    required this.id,
    required this.kind,
    required this.summary,
    required this.targetType,
    required this.targetId,
    required this.createdAt,
  });

  factory _ActivityEvent.fromJson(Map<String, dynamic> j) {
    return _ActivityEvent(
      id: j['id'] as String,
      kind: (j['kind'] as String?) ?? '',
      summary: (j['summary'] as String?) ?? '',
      targetType: (j['target_type'] as String?) ?? '',
      targetId: (j['target_id'] as String?) ?? '',
      createdAt:
          DateTime.tryParse((j['created_at'] as String?) ?? '') ??
              DateTime.now(),
    );
  }
}

class _ActivityPaneState extends ConsumerState<ActivityPane> {
  final List<_ActivityEvent> _events = [];
  String? _next;
  bool _loading = false;
  bool _exhausted = false;
  String? _error;

  final _scroll = ScrollController();

  @override
  void initState() {
    super.initState();
    _scroll.addListener(_maybeLoadMore);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _refresh();
    });
  }

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
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

  void _maybeLoadMore() {
    if (_loading || _exhausted || _next == null) return;
    if (_scroll.position.pixels >=
        _scroll.position.maxScrollExtent - 200) {
      _loadMore();
    }
  }

  Future<void> _refresh() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final r = await _client().listActivity(_accessToken());
      if (!mounted) return;
      setState(() {
        _events
          ..clear()
          ..addAll(r.events.map(_ActivityEvent.fromJson));
        _next = r.next;
        _exhausted = r.next == null || r.next!.isEmpty;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = e.toString());
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _loadMore() async {
    if (_loading || _next == null) return;
    setState(() => _loading = true);
    try {
      final r = await _client().listActivity(
        _accessToken(),
        before: _next,
      );
      if (!mounted) return;
      setState(() {
        _events.addAll(r.events.map(_ActivityEvent.fromJson));
        _next = r.next;
        _exhausted = r.next == null || r.next!.isEmpty;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = e.toString());
    } finally {
      if (mounted) setState(() => _loading = false);
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
                  '活动',
                  style: TextStyle(fontSize: 16, fontWeight: FontWeight.w600),
                ),
              ),
              IconButton(
                tooltip: '刷新',
                icon: const Icon(Icons.refresh, size: 18),
                onPressed: _loading ? null : _refresh,
              ),
            ],
          ),
          const SizedBox(height: BiuTokens.space2),
          Text(
            '展示你最近的操作记录 — PAT 创建/撤销、未来还会有 page 编辑、skill 安装等。',
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
            child: _loading && _events.isEmpty
                ? const Center(child: CircularProgressIndicator())
                : _events.isEmpty
                    ? const _EmptyView()
                    : ListView.separated(
                        controller: _scroll,
                        itemCount: _events.length + (_exhausted ? 0 : 1),
                        separatorBuilder: (_, _) =>
                            const SizedBox(height: BiuTokens.space2),
                        itemBuilder: (_, i) {
                          if (i >= _events.length) {
                            return const Padding(
                              padding: EdgeInsets.all(BiuTokens.space3),
                              child: Center(
                                child: SizedBox(
                                  width: 16,
                                  height: 16,
                                  child: CircularProgressIndicator(
                                      strokeWidth: 2),
                                ),
                              ),
                            );
                          }
                          return _EventCard(event: _events[i]);
                        },
                      ),
          ),
        ],
      ),
    );
  }
}

class _EmptyView extends StatelessWidget {
  const _EmptyView();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Text(
        '还没有活动 — 创建一个 API Token 或编辑一个 page 试试',
        style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
      ),
    );
  }
}

class _EventCard extends StatelessWidget {
  const _EventCard({required this.event});
  final _ActivityEvent event;

  @override
  Widget build(BuildContext context) {
    final icon = _iconFor(event.kind);
    return BiuCard(
      lift: 0,
      padding: const EdgeInsets.all(BiuTokens.space3),
      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Container(
            width: 32,
            height: 32,
            decoration: BoxDecoration(
              color: BiuTokens.purpleSoft,
              borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
            ),
            child: Icon(icon, size: 16, color: BiuTokens.purple),
          ),
          const SizedBox(width: BiuTokens.space3),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  event.summary,
                  style: TextStyle(
                      fontSize: 13,
                      fontWeight: FontWeight.w500,
                      color: BiuTokens.text),
                ),
                const SizedBox(height: 2),
                Row(
                  children: [
                    Text(
                      _relTime(event.createdAt),
                      style: TextStyle(
                          fontSize: 10, color: BiuTokens.textMuted),
                    ),
                    if (event.kind.isNotEmpty) ...[
                      const SizedBox(width: 6),
                      Text('·',
                          style: TextStyle(
                              fontSize: 10, color: BiuTokens.textMuted)),
                      const SizedBox(width: 6),
                      Text(
                        event.kind,
                        style: TextStyle(
                            fontSize: 10,
                            color: BiuTokens.textMuted,
                            fontFamily:
                                'JetBrains Mono, ui-monospace, monospace'),
                      ),
                    ],
                  ],
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  static IconData _iconFor(String kind) {
    if (kind.startsWith('pat.')) return Icons.vpn_key_outlined;
    if (kind.startsWith('page.')) return Icons.article_outlined;
    if (kind.startsWith('skill.')) return Icons.extension_outlined;
    if (kind.startsWith('auth.')) return Icons.login_outlined;
    return Icons.history_outlined;
  }

  static String _relTime(DateTime t) {
    final d = DateTime.now().difference(t);
    if (d.inSeconds < 60) return '刚刚';
    if (d.inMinutes < 60) return '${d.inMinutes} 分钟前';
    if (d.inHours < 24) return '${d.inHours} 小时前';
    if (d.inDays < 30) return '${d.inDays} 天前';
    return t.toLocal().toString().substring(0, 10);
  }
}

class _NoCreds implements Exception {
  const _NoCreds();
  @override
  String toString() => '请先登录 BiuMind 账号';
}
