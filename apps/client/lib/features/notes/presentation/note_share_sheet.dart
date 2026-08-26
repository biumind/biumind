/// 笔记分享弹层（S1，产品设计 D5）—— 链接复制 / 密码 / 有效期 / 重置 /
/// 停止分享。
///
/// 数据纯服务端实时拉取（noteShareProvider，不进 Drift）：未创建过分享
/// （GET 404）时显示「创建分享链接」主按钮；已创建显示管理面板。
/// 所有变更（创建 / 改密码 / 改有效期 / rotate / 停用 / 恢复）后统一走
/// invalidateNoteShareProviders 刷新单篇状态 + 列表徽标。
///
/// 桌面 showDialog（440 宽）/ 手机 bottom sheet（同一组件，F1 惯例）。
library;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/layout/form_factor.dart';
import '../../../data/api/notes_client.dart' as api;
import '../../../services/auth_service.dart';
import '../application/note_share_providers.dart';
import 'notes_home_page.dart' show relativeTime;

/// 有效期档位（契约 expires_in 取值）。
const _expiresOptions = <(String, String)>[
  ('1d', '1 天'),
  ('7d', '7 天'),
  ('30d', '30 天'),
  ('never', '永久'),
];

class NoteShareSheet extends ConsumerStatefulWidget {
  const NoteShareSheet({super.key, required this.noteId});

  final String noteId;

  /// 桌面弹对话框、手机弹 bottom sheet（复用同一组件）。
  static Future<void> show(BuildContext context, {required String noteId}) {
    if (isPhoneLayout(context)) {
      return showModalBottomSheet<void>(
        context: context,
        showDragHandle: true,
        isScrollControlled: true,
        builder: (sheetCtx) => Padding(
          padding: EdgeInsets.only(
            bottom: MediaQuery.viewInsetsOf(sheetCtx).bottom,
          ),
          child: NoteShareSheet(noteId: noteId),
        ),
      );
    }
    return showDialog<void>(
      context: context,
      builder: (_) => Dialog(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 440),
          child: NoteShareSheet(noteId: noteId),
        ),
      ),
    );
  }

  @override
  ConsumerState<NoteShareSheet> createState() => _NoteShareSheetState();
}

class _NoteShareSheetState extends ConsumerState<NoteShareSheet> {
  final _passwordController = TextEditingController();

  /// 密码输入区显隐（开关 on 或「修改密码」展开时为 true）。
  bool _passwordInputVisible = false;

  /// 动作进行中（防连点）。
  bool _acting = false;

  /// 本次会话内在弹层里设置过的密码 —— 服务端只回 password_set，不回
  /// 密码本体；「链接 + 密码」合并复制文案只在用户刚设过密码时可用。
  String? _lastSetPassword;

  @override
  void dispose() {
    _passwordController.dispose();
    super.dispose();
  }

  void _toast(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(
      context,
    ).showSnackBar(SnackBar(content: Text(message)));
  }

  String? _shareUrl(api.NoteShare share) {
    final creds = ref.read(hubCredentialsProvider);
    if (creds == null) return null;
    return noteShareUrl(creds.endpoint, share.token);
  }

  /// 统一动作执行：防连点 + 失败 toast + 成功后失效刷新。
  /// 返回是否成功 —— 调用方有后续本地状态更新（如记录刚设的密码）时
  /// 必须据此判断，失败不能落本地态。
  Future<bool> _run(
    Future<void> Function(api.NotesClient client) action, {
    String? doneToast,
  }) async {
    final client = ref.read(noteShareClientProvider);
    if (client == null || _acting) return false;
    setState(() => _acting = true);
    try {
      await action(client);
      invalidateNoteShareProviders(ref, widget.noteId);
      if (doneToast != null) _toast(doneToast);
      return true;
    } on Exception catch (e) {
      _toast('操作失败：$e');
      return false;
    } finally {
      if (mounted) setState(() => _acting = false);
    }
  }

  // ─── 动作 ────────────────────────────────────────────────

  Future<void> _createShare() => _run(
    // 创建默认永久有效、无密码；配置在面板上再调（PUT 幂等）。
    (client) => client.putShare(widget.noteId, expiresIn: 'never').then((_) {}),
    doneToast: '分享链接已创建',
  );

  Future<void> _restoreShare(api.NoteShare share) => _run(
    // 对已停用分享 PUT = 以原 token 恢复并更新配置（契约）。
    (client) => client
        .putShare(
          widget.noteId,
          expiresIn: noteShareExpiresInOf(share.expiresAt, DateTime.now()),
        )
        .then((_) {}),
    doneToast: '分享已恢复',
  );

  Future<void> _setExpiresIn(String expiresIn) => _run(
    // password 字段缺省 = 保持不变（契约 presence 语义）。
    (client) =>
        client.putShare(widget.noteId, expiresIn: expiresIn).then((_) {}),
  );

  Future<void> _setPasswordKeepingExpiry(api.NoteShare share) async {
    final pwd = _passwordController.text;
    if (pwd.length < 4 || pwd.length > 8) {
      _toast('密码需为 4–8 位');
      return;
    }
    final ok = await _run(
      (client) => client
          .putShare(
            widget.noteId,
            password: pwd,
            expiresIn: noteShareExpiresInOf(share.expiresAt, DateTime.now()),
          )
          .then((_) {}),
      doneToast: '访问密码已设置',
    );
    // 只有服务端真的改成功才记录密码本体（合并复制文案用）—— 失败时
    // 若落了 _lastSetPassword，复制出来的密码与服务端不一致（交互 bug）。
    if (ok && mounted) {
      setState(() {
        _lastSetPassword = pwd;
        _passwordController.clear();
        _passwordInputVisible = false;
      });
    }
  }

  Future<void> _removePassword(api.NoteShare share) => _run(
    // '' = 移除密码（契约 presence 语义）。
    (client) => client
        .putShare(
          widget.noteId,
          password: '',
          expiresIn: noteShareExpiresInOf(share.expiresAt, DateTime.now()),
        )
        .then((_) {}),
    doneToast: '访问密码已移除',
  );

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
    await _run(
      (client) => client.rotateShare(widget.noteId).then((_) {}),
      doneToast: '链接已重置，请复制新链接',
    );
  }

  Future<void> _disable() async {
    final c = Theme.of(context).extension<BiuColors>()!;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('停止分享'),
        content: const Text('停止后链接立即无法访问，之后可随时恢复（链接地址不变）。'),
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
    await _run(
      (client) => client.deleteShare(widget.noteId),
      doneToast: '已停止分享',
    );
  }

  void _copyLink(api.NoteShare share) {
    final url = _shareUrl(share);
    if (url == null) return;
    // 含密码时复制「链接 + 密码」合并文案（方便粘贴到微信）—— 密码本体
    // 只有本次会话内在本弹层设置过才拿得到，否则只复制链接。
    final pwd = share.passwordSet ? _lastSetPassword : null;
    final text = pwd == null ? url : '链接：$url\n访问密码：$pwd';
    Clipboard.setData(ClipboardData(text: text));
    _toast(pwd == null ? '链接已复制' : '链接和访问密码已复制');
  }

  // ─── build ───────────────────────────────────────────────

  @override
  Widget build(BuildContext context) {
    final c = Theme.of(context).extension<BiuColors>()!;
    final shareAsync = ref.watch(noteShareProvider(widget.noteId));
    return SingleChildScrollView(
      padding: const EdgeInsets.all(20),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: <Widget>[
          Row(
            children: <Widget>[
              Icon(Icons.share_outlined, size: 18, color: c.text2),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  '分享笔记',
                  style: Theme.of(context).textTheme.titleMedium,
                ),
              ),
            ],
          ),
          const SizedBox(height: 16),
          shareAsync.when(
            loading: () => const Padding(
              padding: EdgeInsets.symmetric(vertical: 32),
              child: Center(child: CircularProgressIndicator()),
            ),
            error: (_, _) => Column(
              mainAxisSize: MainAxisSize.min,
              children: <Widget>[
                Text(
                  '加载分享状态失败',
                  style: TextStyle(fontSize: 13, color: c.textMuted),
                ),
                const SizedBox(height: 8),
                TextButton.icon(
                  onPressed: () =>
                      ref.invalidate(noteShareProvider(widget.noteId)),
                  icon: const Icon(Icons.refresh, size: 16),
                  label: const Text('重试'),
                ),
              ],
            ),
            data: (share) => share == null
                ? _buildCreatePrompt(c)
                : _buildManagePanel(c, share),
          ),
        ],
      ),
    );
  }

  /// 未创建过分享：说明 + 创建主按钮。
  Widget _buildCreatePrompt(BiuColors c) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: <Widget>[
        Text(
          '创建公开链接，任何获得链接的人都能查看这篇笔记（只读，无需登录）。'
          '笔记内容更新后对方刷新即可看到。',
          style: TextStyle(fontSize: 13, color: c.text2, height: 1.5),
        ),
        const SizedBox(height: 16),
        SizedBox(
          width: double.infinity,
          child: FilledButton.icon(
            onPressed: _acting ? null : _createShare,
            icon: const Icon(Icons.link, size: 18),
            label: const Text('创建分享链接'),
          ),
        ),
      ],
    );
  }

  /// 已有分享：链接 + 复制、密码、有效期、信息行、重置 / 停止（或恢复）。
  Widget _buildManagePanel(BiuColors c, api.NoteShare share) {
    final now = DateTime.now();
    final status = share.status(now);
    final url = _shareUrl(share) ?? '';
    final disabled = status == api.NoteShareStatus.disabled;
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: <Widget>[
        if (status != api.NoteShareStatus.active) ...<Widget>[
          Container(
            width: double.infinity,
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
            decoration: BoxDecoration(
              color: status == api.NoteShareStatus.disabled
                  ? c.surface2
                  : c.warningSoft,
              borderRadius: BorderRadius.circular(8),
            ),
            child: Text(
              status == api.NoteShareStatus.disabled
                  ? '已停止分享，链接当前无法访问'
                  : '分享已过期，链接当前无法访问',
              style: TextStyle(fontSize: 12, color: c.text2),
            ),
          ),
          const SizedBox(height: 12),
        ],
        // 链接 + 一键复制
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          decoration: BoxDecoration(
            color: c.surface2,
            borderRadius: BorderRadius.circular(8),
          ),
          child: Row(
            children: <Widget>[
              Expanded(
                child: SelectableText(
                  url,
                  style: TextStyle(fontSize: 13, color: c.text1),
                  maxLines: 1,
                ),
              ),
              IconButton(
                tooltip: '复制链接',
                onPressed: () => _copyLink(share),
                icon: Icon(Icons.copy, size: 16, color: c.text2),
                padding: EdgeInsets.zero,
                constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
              ),
            ],
          ),
        ),
        if (share.passwordSet && _lastSetPassword == null)
          Padding(
            padding: const EdgeInsets.only(top: 6),
            child: Text(
              '已设置访问密码（复制密码需在下方重新设置后复制）',
              style: TextStyle(fontSize: 11, color: c.textMuted),
            ),
          ),
        const SizedBox(height: 16),
        // 密码开关 + 输入
        Row(
          children: <Widget>[
            Expanded(
              child: Text(
                '访问密码',
                style: TextStyle(fontSize: 13, color: c.text1),
              ),
            ),
            Switch(
              value: share.passwordSet || _passwordInputVisible,
              onChanged: _acting
                  ? null
                  : (v) {
                      if (v) {
                        setState(() => _passwordInputVisible = true);
                      } else if (share.passwordSet) {
                        setState(() => _passwordInputVisible = false);
                        _removePassword(share);
                      } else {
                        setState(() => _passwordInputVisible = false);
                      }
                    },
            ),
          ],
        ),
        if (share.passwordSet || _passwordInputVisible)
          Padding(
            padding: const EdgeInsets.only(bottom: 4),
            child: Row(
              children: <Widget>[
                Expanded(
                  child: TextField(
                    controller: _passwordController,
                    style: TextStyle(fontSize: 13, color: c.text1),
                    decoration: InputDecoration(
                      isDense: true,
                      hintText: share.passwordSet ? '输入新密码以修改' : '4–8 位密码',
                      hintStyle: TextStyle(fontSize: 13, color: c.textMuted),
                      border: const OutlineInputBorder(),
                      contentPadding: const EdgeInsets.symmetric(
                        horizontal: 10,
                        vertical: 8,
                      ),
                    ),
                    onChanged: (_) => setState(() {}),
                  ),
                ),
                const SizedBox(width: 8),
                FilledButton(
                  onPressed:
                      _acting ||
                          _passwordController.text.length < 4 ||
                          _passwordController.text.length > 8
                      ? null
                      : () => _setPasswordKeepingExpiry(share),
                  child: Text(share.passwordSet ? '更新' : '设置'),
                ),
              ],
            ),
          ),
        const SizedBox(height: 8),
        // 有效期选择
        Row(
          children: <Widget>[
            Text('有效期', style: TextStyle(fontSize: 13, color: c.text1)),
            const SizedBox(width: 12),
            Expanded(
              child: SegmentedButton<String>(
                segments: <ButtonSegment<String>>[
                  for (final (value, label) in _expiresOptions)
                    ButtonSegment(value: value, label: Text(label)),
                ],
                selected: {noteShareExpiresInOf(share.expiresAt, now)},
                onSelectionChanged: _acting
                    ? null
                    : (sel) => _setExpiresIn(sel.first),
                showSelectedIcon: false,
                style: const ButtonStyle(
                  visualDensity: VisualDensity.compact,
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
              ),
            ),
          ],
        ),
        const SizedBox(height: 12),
        // 信息行：创建时间 / 有效期剩余 / 累计访问
        Text(
          '创建于 ${relativeTime(share.createdAt)}'
          ' · ${noteShareExpiryLabel(share.expiresAt, now)}'
          ' · 累计访问 ${share.viewCount} 次',
          style: TextStyle(fontSize: 11, color: c.textMuted),
        ),
        const SizedBox(height: 16),
        // 动作行
        Row(
          children: <Widget>[
            if (disabled)
              Expanded(
                child: FilledButton.icon(
                  onPressed: _acting ? null : () => _restoreShare(share),
                  icon: const Icon(Icons.restore, size: 16),
                  label: const Text('恢复分享'),
                ),
              )
            else ...<Widget>[
              TextButton.icon(
                onPressed: _acting ? null : _rotate,
                icon: Icon(Icons.refresh, size: 16, color: c.text2),
                label: Text('重置链接', style: TextStyle(color: c.text2)),
              ),
              const Spacer(),
              TextButton.icon(
                onPressed: _acting ? null : _disable,
                icon: Icon(Icons.link_off, size: 16, color: c.error),
                label: Text('停止分享', style: TextStyle(color: c.error)),
              ),
            ],
          ],
        ),
      ],
    );
  }
}
