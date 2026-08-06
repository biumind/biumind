// GitPanel —— 编码模块「Git」主 Tab(M4-C)。源代码管理:变更/暂存/提交/推送 +
// 文件 diff + 历史。
//
// 布局:左列(顶部 更改/历史 切换 → 变更列表 + 提交框 / 历史列表),右列(diff 视图)。
// 真相源在本地 git,经 gitControllerProvider 操作。

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../application/git_controller.dart';
import '../data/code_bridge_provider.dart';
import '../domain/git_models.dart';

class GitPanel extends ConsumerStatefulWidget {
  const GitPanel({super.key});

  @override
  ConsumerState<GitPanel> createState() => _GitPanelState();
}

enum _View { changes, history }

class _GitPanelState extends ConsumerState<GitPanel> {
  final _msgCtrl = TextEditingController();
  _View _view = _View.changes;

  @override
  void dispose() {
    _msgCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final bridgeReady = ref.watch(codeBridgeClientProvider) != null;
    if (!bridgeReady) {
      return _hint(Icons.cloud_off_rounded,
          '本地 daemon 未就绪 —— Git 不可用\n登录后桌面端会自动启动 biu serve');
    }
    final state = ref.watch(gitControllerProvider);

    return Row(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        SizedBox(
          width: 380,
          child: Column(
            children: [
              _Header(state: state, view: _view, onView: (v) {
                setState(() => _view = v);
                if (v == _View.history && state.history.isEmpty) {
                  ref.read(gitControllerProvider.notifier).loadHistory();
                }
              }),
              if (state.error != null) _ErrorBar(state.error!),
              Expanded(
                child: _view == _View.changes
                    ? _ChangesColumn(state: state, msgCtrl: _msgCtrl)
                    : _HistoryColumn(state: state),
              ),
            ],
          ),
        ),
        Container(width: 1, color: BiuTokens.borderSubtle),
        Expanded(
          child: _view == _View.changes
              ? _DiffView(
                  title: state.selectedPath,
                  diff: state.diff,
                  loading: state.diffLoading,
                  emptyHint: '选择左侧文件查看 diff',
                )
              : _CommitDetailView(state: state),
        ),
      ],
    );
  }

  Widget _hint(IconData icon, String text) => Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 32, color: BiuTokens.textMuted),
            const SizedBox(height: 10),
            Text(text,
                textAlign: TextAlign.center,
                style: TextStyle(
                    fontSize: 12.5,
                    color: BiuTokens.textSecondary,
                    height: 1.5)),
          ],
        ),
      );
}

// ─── Header:分支 + 拉取/推送 + 更改/历史 切换 ─────────────────

class _Header extends ConsumerWidget {
  const _Header({required this.state, required this.view, required this.onView});
  final GitState state;
  final _View view;
  final ValueChanged<_View> onView;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final notifier = ref.read(gitControllerProvider.notifier);
    final counts = state.counts;
    return Container(
      padding: const EdgeInsets.fromLTRB(12, 8, 8, 8),
      decoration: BoxDecoration(
        border: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Icon(Icons.account_tree_rounded,
                  size: 14, color: BiuTokens.purple),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  state.branch.isEmpty ? '(无分支)' : state.branch,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                      fontSize: 12.5,
                      fontWeight: FontWeight.w600,
                      color: BiuTokens.text),
                ),
              ),
              if (counts.ahead > 0)
                _Pill('↑${counts.ahead}', BiuTokens.purple),
              if (counts.behind > 0) _Pill('↓${counts.behind}', BiuTokens.textSecondary),
              const SizedBox(width: 4),
              _IconBtn(
                icon: Icons.download_rounded,
                tip: '拉取 (pull)',
                busy: state.pushing,
                onTap: () => _run(context, () => notifier.pull(), '拉取'),
              ),
              _IconBtn(
                icon: Icons.upload_rounded,
                tip: '推送 (push)',
                busy: state.pushing,
                onTap: () => _run(context, () => notifier.push(), '推送'),
              ),
              _IconBtn(
                icon: Icons.refresh_rounded,
                tip: '刷新',
                busy: state.loading,
                onTap: notifier.refresh,
              ),
            ],
          ),
          const SizedBox(height: 8),
          _SegToggle(view: view, onView: onView),
        ],
      ),
    );
  }

  Future<void> _run(
      BuildContext context, Future<String?> Function() act, String label) async {
    final out = await act();
    if (!context.mounted) return;
    if (out != null) {
      final msg = out.trim().isEmpty ? '$label完成' : out.trim();
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(msg, maxLines: 3, overflow: TextOverflow.ellipsis),
        duration: const Duration(seconds: 3),
      ));
    }
  }
}

class _SegToggle extends StatelessWidget {
  const _SegToggle({required this.view, required this.onView});
  final _View view;
  final ValueChanged<_View> onView;

  @override
  Widget build(BuildContext context) {
    Widget seg(String label, _View v) {
      final sel = v == view;
      return Expanded(
        child: GestureDetector(
          onTap: () => onView(v),
          child: Container(
            height: 26,
            alignment: Alignment.center,
            decoration: BoxDecoration(
              color: sel ? BiuTokens.purpleSoft : Colors.transparent,
              borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
            ),
            child: Text(label,
                style: TextStyle(
                    fontSize: 12,
                    fontWeight: sel ? FontWeight.w600 : FontWeight.w500,
                    color: sel ? BiuTokens.purple : BiuTokens.textSecondary)),
          ),
        ),
      );
    }

    return Container(
      padding: const EdgeInsets.all(2),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Row(children: [seg('更改', _View.changes), seg('历史', _View.history)]),
    );
  }
}

// ─── 更改列:变更列表 + 提交框 ─────────────────────────────────

class _ChangesColumn extends ConsumerWidget {
  const _ChangesColumn({required this.state, required this.msgCtrl});
  final GitState state;
  final TextEditingController msgCtrl;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final notifier = ref.read(gitControllerProvider.notifier);
    if (state.loading && state.clean) {
      return const Center(child: CircularProgressIndicator(strokeWidth: 2));
    }
    return Column(
      children: [
        Expanded(
          child: ListView(
            padding: const EdgeInsets.symmetric(vertical: 4),
            children: [
              if (state.clean)
                Padding(
                  padding: const EdgeInsets.all(24),
                  child: Center(
                    child: Text('工作区干净 ✓',
                        style: TextStyle(
                            fontSize: 12.5, color: BiuTokens.textMuted)),
                  ),
                ),
              if (state.staged.isNotEmpty)
                _Section(
                  label: '已暂存的更改',
                  count: state.staged.length,
                  actions: [
                    _TextAct('取消全部', () => notifier.unstageAll()),
                  ],
                  children: [
                    for (final f in state.staged)
                      _FileRow(
                        file: f,
                        selected:
                            state.selectedPath == f.path && state.selectedStaged,
                        onTap: () => notifier.selectFile(f.path, true),
                        trailing: _RowBtn(
                          icon: Icons.remove_rounded,
                          tip: '取消暂存',
                          onTap: () => notifier.unstage([f.path]),
                        ),
                      ),
                  ],
                ),
              if (state.unstaged.isNotEmpty)
                _Section(
                  label: '更改',
                  count: state.unstaged.length,
                  actions: [
                    _TextAct('全部暂存', () => notifier.stageAll()),
                    _TextAct('丢弃全部', () => _confirmDiscardAll(context, notifier)),
                  ],
                  children: [
                    for (final f in state.unstaged)
                      _FileRow(
                        file: f,
                        selected: state.selectedPath == f.path &&
                            !state.selectedStaged,
                        onTap: () => notifier.selectFile(f.path, false),
                        trailing: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            _RowBtn(
                              icon: Icons.undo_rounded,
                              tip: '丢弃更改',
                              onTap: () => _confirmDiscard(context, notifier, f),
                            ),
                            _RowBtn(
                              icon: Icons.add_rounded,
                              tip: '暂存',
                              onTap: () => notifier.stage([f.path]),
                            ),
                          ],
                        ),
                      ),
                  ],
                ),
            ],
          ),
        ),
        _CommitBox(state: state, msgCtrl: msgCtrl),
      ],
    );
  }

  Future<void> _confirmDiscard(
      BuildContext context, GitController n, GitFileChange f) async {
    final ok = await _confirm(context, '丢弃 ${f.path} 的更改?', '此操作不可撤销。');
    if (ok) n.discardFile(f);
  }

  Future<void> _confirmDiscardAll(BuildContext context, GitController n) async {
    final ok = await _confirm(context, '丢弃所有未暂存更改?', '所有未暂存改动 + 未跟踪文件将被删除,不可撤销。');
    if (ok) n.discardAll();
  }
}

class _CommitBox extends ConsumerWidget {
  const _CommitBox({required this.state, required this.msgCtrl});
  final GitState state;
  final TextEditingController msgCtrl;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final notifier = ref.read(gitControllerProvider.notifier);
    return Container(
      padding: const EdgeInsets.all(8),
      decoration: BoxDecoration(
        border: Border(top: BorderSide(color: BiuTokens.borderSubtle)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TextField(
            controller: msgCtrl,
            minLines: 2,
            maxLines: 5,
            style: TextStyle(fontSize: 12.5, color: BiuTokens.text),
            decoration: InputDecoration(
              hintText: '提交信息(Conventional Commits)',
              hintStyle: TextStyle(fontSize: 12.5, color: BiuTokens.textMuted),
              isDense: true,
              contentPadding: const EdgeInsets.all(8),
              border: OutlineInputBorder(
                borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
                borderSide: BorderSide(color: BiuTokens.borderSubtle),
              ),
              enabledBorder: OutlineInputBorder(
                borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
                borderSide: BorderSide(color: BiuTokens.borderSubtle),
              ),
            ),
          ),
          const SizedBox(height: 6),
          Row(
            children: [
              _GenBtn(
                // 有任何改动即可生成(daemon 端没暂存就回退工作区 diff);不再要求先暂存。
                busy: state.generatingMsg,
                enabled: !state.clean && !state.committing,
                onTap: () async {
                  final msg = await notifier.generateCommitMessage();
                  if (msg != null && msg.isNotEmpty) {
                    msgCtrl.text = msg;
                  }
                },
              ),
              const Spacer(),
              FilledButton(
                onPressed: (state.committing || !state.hasStaged)
                    ? null
                    : () => _commit(context, notifier),
                style: FilledButton.styleFrom(
                  backgroundColor: BiuTokens.purple,
                  padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 8),
                  minimumSize: const Size(0, 32),
                ),
                child: state.committing
                    ? const SizedBox(
                        width: 14,
                        height: 14,
                        child: CircularProgressIndicator(
                            strokeWidth: 2, color: Colors.white))
                    : Text('提交 ${state.staged.length} 项',
                        style: const TextStyle(fontSize: 12.5)),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Future<void> _commit(BuildContext context, GitController n) async {
    final ok = await n.commit(msgCtrl.text);
    if (ok) msgCtrl.clear();
  }
}

class _GenBtn extends StatelessWidget {
  const _GenBtn(
      {required this.busy, required this.enabled, required this.onTap});
  final bool busy;
  final bool enabled;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return OutlinedButton.icon(
      onPressed: (busy || !enabled) ? null : onTap,
      style: OutlinedButton.styleFrom(
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
        minimumSize: const Size(0, 32),
        side: BorderSide(color: BiuTokens.borderSubtle),
      ),
      icon: busy
          ? const SizedBox(
              width: 12, height: 12, child: CircularProgressIndicator(strokeWidth: 2))
          : Icon(Icons.auto_awesome_rounded, size: 14, color: BiuTokens.purple),
      label: Text('AI 生成',
          style: TextStyle(fontSize: 12, color: BiuTokens.textSecondary)),
    );
  }
}

// ─── 历史列 ──────────────────────────────────────────────────

class _HistoryColumn extends ConsumerWidget {
  const _HistoryColumn({required this.state});
  final GitState state;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final notifier = ref.read(gitControllerProvider.notifier);
    if (state.historyLoading && state.history.isEmpty) {
      return const Center(child: CircularProgressIndicator(strokeWidth: 2));
    }
    if (state.history.isEmpty) {
      return Center(
        child: Text('无提交历史',
            style: TextStyle(fontSize: 12.5, color: BiuTokens.textMuted)),
      );
    }
    return ListView.builder(
      itemCount: state.history.length,
      itemBuilder: (ctx, i) {
        final c = state.history[i];
        final sel = c.hash == state.selectedCommit;
        return InkWell(
          onTap: () => notifier.selectCommit(c.hash),
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
            color: sel ? BiuTokens.purpleSoft : Colors.transparent,
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(c.message,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(
                        fontSize: 12.5,
                        fontWeight: FontWeight.w500,
                        color: BiuTokens.text)),
                const SizedBox(height: 2),
                Row(
                  children: [
                    Text(c.shortHash,
                        style: TextStyle(
                            fontSize: 11,
                            fontFamily: 'SF Mono',
                            color: BiuTokens.purple)),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text('${c.author} · ${c.date}',
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                              fontSize: 11, color: BiuTokens.textMuted)),
                    ),
                  ],
                ),
              ],
            ),
          ),
        );
      },
    );
  }
}

class _CommitDetailView extends StatelessWidget {
  const _CommitDetailView({required this.state});
  final GitState state;

  @override
  Widget build(BuildContext context) {
    final d = state.commitDetail;
    if (state.selectedCommit == null) {
      return _centerHint('选择左侧提交查看详情');
    }
    if (d == null) {
      return const Center(child: CircularProgressIndicator(strokeWidth: 2));
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Container(
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            border: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(d.message,
                  style: TextStyle(
                      fontSize: 13,
                      fontWeight: FontWeight.w600,
                      color: BiuTokens.text)),
              const SizedBox(height: 4),
              Text(
                  '${d.shortHash} · ${d.author} · ${d.date}  '
                  '· +${d.totalAdditions} −${d.totalDeletions} · ${d.files.length} 文件',
                  style: TextStyle(fontSize: 11.5, color: BiuTokens.textMuted)),
            ],
          ),
        ),
        Expanded(child: _DiffText(state.commitDiff ?? '')),
      ],
    );
  }
}

// ─── 通用:文件行 / 分区 / diff 视图 / 小组件 ──────────────────

class _Section extends StatelessWidget {
  const _Section({
    required this.label,
    required this.count,
    required this.children,
    this.actions = const [],
  });
  final String label;
  final int count;
  final List<Widget> children;
  final List<Widget> actions;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(12, 8, 8, 4),
          child: Row(
            children: [
              Text('$label  $count',
                  style: TextStyle(
                      fontSize: 11,
                      fontWeight: FontWeight.w700,
                      letterSpacing: 0.3,
                      color: BiuTokens.textSecondary)),
              const Spacer(),
              ...actions,
            ],
          ),
        ),
        ...children,
      ],
    );
  }
}

class _FileRow extends StatelessWidget {
  const _FileRow({
    required this.file,
    required this.selected,
    required this.onTap,
    required this.trailing,
  });
  final GitFileChange file;
  final bool selected;
  final VoidCallback onTap;
  final Widget trailing;

  @override
  Widget build(BuildContext context) {
    final (color, letter) = _statusStyle(file.status);
    final slash = file.path.lastIndexOf('/');
    final name = slash >= 0 ? file.path.substring(slash + 1) : file.path;
    final dir = slash >= 0 ? file.path.substring(0, slash) : '';
    return InkWell(
      onTap: onTap,
      child: Container(
        height: 28,
        padding: const EdgeInsets.symmetric(horizontal: 12),
        color: selected ? BiuTokens.purpleSoft : Colors.transparent,
        child: Row(
          children: [
            SizedBox(
              width: 14,
              child: Text(letter,
                  style: TextStyle(
                      fontSize: 11,
                      fontWeight: FontWeight.w700,
                      fontFamily: 'SF Mono',
                      color: color)),
            ),
            const SizedBox(width: 4),
            Flexible(
              child: Text(name,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(fontSize: 12.5, color: BiuTokens.text)),
            ),
            if (dir.isNotEmpty) ...[
              const SizedBox(width: 6),
              Flexible(
                child: Text(dir,
                    overflow: TextOverflow.ellipsis,
                    style:
                        TextStyle(fontSize: 11, color: BiuTokens.textMuted)),
              ),
            ],
            const Spacer(),
            trailing,
          ],
        ),
      ),
    );
  }

  (Color, String) _statusStyle(String s) => switch (s) {
        'A' => (BiuTokens.green, 'A'),
        '?' => (BiuTokens.green, 'U'),
        'D' => (BiuTokens.error, 'D'),
        'M' => (BiuTokens.purple, 'M'),
        'R' => (BiuTokens.purple, 'R'),
        'C' => (BiuTokens.purple, 'C'),
        _ => (BiuTokens.textSecondary, s.isEmpty ? '·' : s),
      };
}

class _DiffView extends StatelessWidget {
  const _DiffView({
    required this.title,
    required this.diff,
    required this.loading,
    required this.emptyHint,
  });
  final String? title;
  final String? diff;
  final bool loading;
  final String emptyHint;

  @override
  Widget build(BuildContext context) {
    if (title == null) return _centerHint(emptyHint);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          decoration: BoxDecoration(
            border: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
          ),
          child: Text(title!,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                  fontSize: 12.5,
                  fontFamily: 'SF Mono',
                  color: BiuTokens.text)),
        ),
        Expanded(
          child: loading
              ? const Center(child: CircularProgressIndicator(strokeWidth: 2))
              : _DiffText(diff ?? ''),
        ),
      ],
    );
  }
}

/// 把 unified diff 文本按行着色(+绿/−红/@@蓝/其余默认)。等宽,可横向滚动。
class _DiffText extends StatelessWidget {
  const _DiffText(this.diff);
  final String diff;

  @override
  Widget build(BuildContext context) {
    if (diff.trim().isEmpty) {
      return _centerHint('无差异');
    }
    final lines = diff.split('\n');
    return SelectionArea(
      child: Scrollbar(
        child: ListView.builder(
          primary: true,
          padding: const EdgeInsets.symmetric(vertical: 6),
          itemCount: lines.length,
          itemBuilder: (ctx, i) {
            final line = lines[i];
            final (bg, fg) = _lineStyle(line);
            return Container(
              width: double.infinity,
              color: bg,
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 1),
              child: Text(
                line.isEmpty ? ' ' : line,
                style: TextStyle(
                    fontSize: 12,
                    height: 1.4,
                    fontFamily: 'SF Mono',
                    color: fg),
              ),
            );
          },
        ),
      ),
    );
  }

  (Color, Color) _lineStyle(String line) {
    if (line.startsWith('+') && !line.startsWith('+++')) {
      return (BiuTokens.green.withValues(alpha: 0.12), BiuTokens.green);
    }
    if (line.startsWith('-') && !line.startsWith('---')) {
      return (BiuTokens.error.withValues(alpha: 0.12), BiuTokens.error);
    }
    if (line.startsWith('@@')) {
      return (Colors.transparent, BiuTokens.purple);
    }
    if (line.startsWith('diff ') ||
        line.startsWith('index ') ||
        line.startsWith('+++') ||
        line.startsWith('---')) {
      return (Colors.transparent, BiuTokens.textMuted);
    }
    return (Colors.transparent, BiuTokens.textSecondary);
  }
}

class _Pill extends StatelessWidget {
  const _Pill(this.text, this.color);
  final String text;
  final Color color;
  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.only(right: 4),
        child: Text(text,
            style: TextStyle(
                fontSize: 11, fontWeight: FontWeight.w600, color: color)),
      );
}

class _IconBtn extends StatelessWidget {
  const _IconBtn(
      {required this.icon,
      required this.tip,
      required this.onTap,
      this.busy = false});
  final IconData icon;
  final String tip;
  final VoidCallback onTap;
  final bool busy;

  @override
  Widget build(BuildContext context) {
    return Tooltip(
      message: tip,
      child: InkWell(
        onTap: busy ? null : onTap,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        child: SizedBox(
          width: 28,
          height: 28,
          child: busy
              ? const Padding(
                  padding: EdgeInsets.all(7),
                  child: CircularProgressIndicator(strokeWidth: 2))
              : Icon(icon, size: 16, color: BiuTokens.textSecondary),
        ),
      ),
    );
  }
}

class _RowBtn extends StatelessWidget {
  const _RowBtn({required this.icon, required this.tip, required this.onTap});
  final IconData icon;
  final String tip;
  final VoidCallback onTap;
  @override
  Widget build(BuildContext context) => Tooltip(
        message: tip,
        child: InkWell(
          onTap: onTap,
          borderRadius: BorderRadius.circular(4),
          child: Padding(
            padding: const EdgeInsets.all(3),
            child: Icon(icon, size: 14, color: BiuTokens.textMuted),
          ),
        ),
      );
}

class _TextAct extends StatelessWidget {
  const _TextAct(this.label, this.onTap);
  final String label;
  final VoidCallback onTap;
  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.only(left: 8),
        child: InkWell(
          onTap: onTap,
          child: Text(label,
              style: TextStyle(fontSize: 11, color: BiuTokens.purple)),
        ),
      );
}

class _ErrorBar extends StatelessWidget {
  const _ErrorBar(this.message);
  final String message;
  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      color: BiuTokens.error.withValues(alpha: 0.1),
      child: Row(
        children: [
          Icon(Icons.error_outline_rounded, size: 14, color: BiuTokens.error),
          const SizedBox(width: 6),
          Expanded(
            child: Text(message,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(fontSize: 11.5, color: BiuTokens.error)),
          ),
          InkWell(
            onTap: () => Clipboard.setData(ClipboardData(text: message)),
            child: Icon(Icons.copy_rounded, size: 13, color: BiuTokens.error),
          ),
        ],
      ),
    );
  }
}

Widget _centerHint(String text) => Center(
      child: Text(text,
          textAlign: TextAlign.center,
          style: TextStyle(fontSize: 12.5, color: BiuTokens.textMuted)),
    );

Future<bool> _confirm(BuildContext context, String title, String body) async {
  final res = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text(title, style: const TextStyle(fontSize: 15)),
      content: Text(body, style: const TextStyle(fontSize: 13)),
      actions: [
        TextButton(
            onPressed: () => Navigator.pop(ctx, false), child: const Text('取消')),
        FilledButton(
          onPressed: () => Navigator.pop(ctx, true),
          style: FilledButton.styleFrom(backgroundColor: BiuTokens.error),
          child: const Text('确认'),
        ),
      ],
    ),
  );
  return res ?? false;
}
