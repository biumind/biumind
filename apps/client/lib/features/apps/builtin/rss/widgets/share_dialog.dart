// M11.3 — share-a-view dialog. Calls shares_create, then shows the
// public read-only URL with a copy button + expiry note. QR rendering is
// intentionally deferred (would add a new package; per repo policy new
// deps need sign-off) — the URL + copy covers the core "send someone a
// read-only view" need. Clipboard is from flutter/services (no new dep).

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../../app/theme.dart';
import '../providers.dart';

/// Opens the share dialog for [viewKind] (today / radar / saved / inbox).
/// Mints the link on open; shows a spinner then the URL.
Future<void> showShareDialog(
  BuildContext context,
  WidgetRef ref, {
  required String viewKind,
  Map<String, dynamic>? filter,
}) {
  return showDialog<void>(
    context: context,
    builder: (_) => _ShareDialog(viewKind: viewKind, filter: filter),
  );
}

const _kindLabels = <String, String>{
  'today': '今日要闻',
  'radar': '雷达命中',
  'saved': '收藏',
  'inbox': '订阅',
};

class _ShareDialog extends ConsumerStatefulWidget {
  const _ShareDialog({required this.viewKind, this.filter});
  final String viewKind;
  final Map<String, dynamic>? filter;

  @override
  ConsumerState<_ShareDialog> createState() => _ShareDialogState();
}

class _ShareDialogState extends ConsumerState<_ShareDialog> {
  ShareLink? _link;
  String? _error;
  bool _copied = false;

  @override
  void initState() {
    super.initState();
    _create();
  }

  Future<void> _create() async {
    final actions = ref.read(rssActionsProvider);
    if (actions == null) {
      setState(() => _error = '未登录');
      return;
    }
    try {
      final link = await actions.sharesCreate(widget.viewKind, filter: widget.filter);
      if (!mounted) return;
      setState(() => _link = link);
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = '$e');
    }
  }

  Future<void> _copy() async {
    final url = _link?.url;
    if (url == null || url.isEmpty) return;
    await Clipboard.setData(ClipboardData(text: url));
    if (!mounted) return;
    setState(() => _copied = true);
  }

  @override
  Widget build(BuildContext context) {
    final label = _kindLabels[widget.viewKind] ?? '视图';
    final scheme = Theme.of(context).colorScheme;
    return AlertDialog(
      title: Row(
        children: [
          Icon(Icons.ios_share, size: 18, color: scheme.primary),
          const SizedBox(width: 8),
          Text('分享「$label」'),
        ],
      ),
      content: SizedBox(
        width: 420,
        child: _error != null
            ? Text('生成分享链接失败: $_error',
                style: TextStyle(color: scheme.error, fontSize: 13))
            : _link == null
                ? const SizedBox(
                    height: 80,
                    child: Center(child: CircularProgressIndicator(strokeWidth: 2)),
                  )
                : Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text('任何人凭此链接可只读查看（无需登录）',
                          style: TextStyle(
                              fontSize: 13, color: BiuTokens.textSecondary)),
                      const SizedBox(height: BiuTokens.space3),
                      Container(
                        padding: const EdgeInsets.all(BiuTokens.space3),
                        decoration: BoxDecoration(
                          color: BiuTokens.surfaceMuted,
                          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
                          border: Border.all(color: BiuTokens.borderSubtle),
                        ),
                        child: SelectableText(
                          _link!.url,
                          style: const TextStyle(fontSize: 13, fontFamily: 'monospace'),
                        ),
                      ),
                      const SizedBox(height: BiuTokens.space2),
                      Text(
                        _link!.expiresAt != null
                            ? '链接 ${_fmtDate(_link!.expiresAt!)} 过期'
                            : '默认 30 天后过期',
                        style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
                      ),
                    ],
                  ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('关闭'),
        ),
        if (_link != null)
          FilledButton.icon(
            onPressed: _copy,
            icon: Icon(_copied ? Icons.check : Icons.copy, size: 16),
            label: Text(_copied ? '已复制' : '复制链接'),
          ),
      ],
    );
  }

  String _fmtDate(DateTime d) {
    final l = d.toLocal();
    return '${l.year}-${l.month.toString().padLeft(2, '0')}-${l.day.toString().padLeft(2, '0')}';
  }
}
