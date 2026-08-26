// 我的分享（S1，设置页入口）—— 当前账号所有笔记分享的管理列表。
//
// 数据源 = myNoteSharesProvider（brain GET /v1/notes/shares），与笔记列表
// 项的外链徽标同源（契约：客户端用同一接口渲染徽标与管理列表）。分享状态
// 不进 Drift，实时拉取；停用 / 恢复 / 重置链接后 invalidate 刷新。
//
// 状态机（服务端计算，契约 active / disabled / expired）：生效中 / 已停用 /
// 已过期 三色 chip。操作：复制链接（仅生效中）、停用（可恢复）、恢复
// （= 重新 PUT，契约：以原 token 恢复并更新配置）、重置链接（旧链接作废）。

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../data/api/notes_client.dart' as api;
import '../../../services/auth_service.dart';
import '../../notes/application/note_share_providers.dart';

class MySharesPane extends ConsumerWidget {
  const MySharesPane({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final c = Theme.of(context).extension<BiuColors>()!;
    final sharesAsync = ref.watch(myNoteSharesProvider);
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 720),
        child: sharesAsync.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (_, _) => Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: <Widget>[
                Text(
                  '加载失败，请检查网络后重试',
                  style: TextStyle(fontSize: 13, color: c.textMuted),
                ),
                const SizedBox(height: 8),
                TextButton.icon(
                  onPressed: () => ref.invalidate(myNoteSharesProvider),
                  icon: const Icon(Icons.refresh, size: 16),
                  label: const Text('重试'),
                ),
              ],
            ),
          ),
          data: (items) {
            if (items.isEmpty) {
              return Center(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: <Widget>[
                    Icon(Icons.share_outlined, size: 40, color: c.textMuted),
                    const SizedBox(height: 12),
                    Text(
                      '还没有分享过笔记',
                      style: TextStyle(fontSize: 13, color: c.text2),
                    ),
                    const SizedBox(height: 4),
                    Text(
                      '在笔记编辑器工具栏或列表右键菜单中发起分享',
                      style: TextStyle(fontSize: 12, color: c.textMuted),
                    ),
                  ],
                ),
              );
            }
            return ListView.separated(
              padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 16),
              itemCount: items.length,
              separatorBuilder: (_, _) =>
                  Divider(height: 1, color: c.borderHairline),
              itemBuilder: (context, i) => _ShareRow(item: items[i]),
            );
          },
        ),
      ),
    );
  }
}

class _ShareRow extends ConsumerStatefulWidget {
  const _ShareRow({required this.item});

  final api.NoteShareListItem item;

  @override
  ConsumerState<_ShareRow> createState() => _ShareRowState();
}

class _ShareRowState extends ConsumerState<_ShareRow> {
  /// 动作进行中（防连点）。
  bool _acting = false;

  api.NoteShareListItem get item => widget.item;

  void _toast(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(message)));
  }

  Future<void> _mutate(
    Future<void> Function(api.NotesClient client) action,
    String doneToast,
  ) async {
    final client = ref.read(noteShareClientProvider);
    if (client == null || _acting) return;
    setState(() => _acting = true);
    try {
      await action(client);
      invalidateNoteShareProviders(ref, item.noteId);
      _toast(doneToast);
    } on Exception catch (e) {
      _toast('操作失败：$e');
    } finally {
      if (mounted) setState(() => _acting = false);
    }
  }

  Future<void> _copyLink() async {
    final creds = ref.read(hubCredentialsProvider);
    if (creds == null) return;
    await Clipboard.setData(
      ClipboardData(text: noteShareUrl(creds.endpoint, item.share.token)),
    );
    _toast('链接已复制');
  }

  Future<void> _disable() async {
    final c = Theme.of(context).extension<BiuColors>()!;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('停止分享'),
        content: Text(
          '停止后「${item.noteTitle.isEmpty ? '无标题笔记' : item.noteTitle}」的链接立即无法访问，之后可随时恢复（链接地址不变）。',
        ),
        actions: <Widget>[
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: c.error),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('停止分享'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    await _mutate((client) => client.deleteShare(item.noteId), '已停止分享');
  }

  Future<void> _restore() async {
    // 对已停用分享 PUT = 以原 token 恢复（契约）；expires_in 缺省 =
    // 保持现有 expires_at 不变（契约修订，不再归桶反推上送）。
    await _mutate(
      (client) => client.putShare(item.noteId).then((_) {}),
      '分享已恢复',
    );
  }

  Future<void> _rotate() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('重置链接'),
        content: const Text('重置后旧链接立即作废，之前发出去的链接将无法访问。确定重置？'),
        actions: <Widget>[
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('重置'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    await _mutate(
      (client) => client.rotateShare(item.noteId).then((_) {}),
      '链接已重置',
    );
  }

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    final now = DateTime.now();
    final active = item.status == api.NoteShareStatus.active;
    final (chipLabel, chipBg, chipFg) = switch (item.status) {
      api.NoteShareStatus.active => ('生效中', c.successSoft, c.success),
      api.NoteShareStatus.disabled => ('已停用', c.surface2, c.textMuted),
      api.NoteShareStatus.expired => ('已过期', c.warningSoft, c.warning),
    };
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 10),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: <Widget>[
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: <Widget>[
                Row(
                  children: <Widget>[
                    Flexible(
                      child: Text(
                        item.noteTitle.isEmpty ? '无标题笔记' : item.noteTitle,
                        style: TextStyle(
                          fontSize: 13,
                          fontWeight: FontWeight.w600,
                          color: c.text1,
                        ),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    const SizedBox(width: 8),
                    Container(
                      padding: const EdgeInsets.symmetric(
                        horizontal: 6,
                        vertical: 1,
                      ),
                      decoration: BoxDecoration(
                        color: chipBg,
                        borderRadius: BorderRadius.circular(4),
                      ),
                      child: Text(
                        chipLabel,
                        style: TextStyle(fontSize: 10, color: chipFg),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 4),
                Text(
                  '${noteShareExpiryLabel(item.share.expiresAt, now)}'
                  ' · 累计访问 ${item.share.viewCount} 次'
                  '${item.share.passwordSet ? ' · 已设密码' : ''}',
                  style: TextStyle(fontSize: 11, color: c.textMuted),
                ),
              ],
            ),
          ),
          const SizedBox(width: 8),
          // 操作：复制链接（仅生效中）/ 停用·恢复 / 重置链接
          if (active)
            IconButton(
              tooltip: '复制链接',
              onPressed: _acting ? null : _copyLink,
              icon: Icon(Icons.copy, size: 16, color: c.text2),
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
            ),
          if (item.status == api.NoteShareStatus.disabled)
            IconButton(
              tooltip: '恢复分享',
              onPressed: _acting ? null : _restore,
              icon: Icon(Icons.restore, size: 16, color: c.text2),
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
            )
          else
            IconButton(
              tooltip: '停止分享',
              onPressed: _acting ? null : _disable,
              icon: Icon(Icons.link_off, size: 16, color: c.error),
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
            ),
          IconButton(
            tooltip: '重置链接',
            onPressed: _acting ? null : _rotate,
            icon: Icon(Icons.refresh, size: 16, color: c.text2),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
          ),
        ],
      ),
    );
  }
}
