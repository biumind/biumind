// 编码工作台。
//
//   ┌──┬────────┬──────────────────────────┬──────────┬──┐
//   │项│ Tasks  │   Main                   │  右栏    │图│
//   │目│ 220px  │   - 任务详情头 + 回放     │ 文件树   │标│
//   │  │        │   - 或:文件编辑 Tab     │ / Git    │条│
//   │  │        │   ─────────────────      │ / 历史   │48│
//   │  │        │   (底部:终端面板 可开关) │          │  │
//   └──┴────────┴──────────────────────────┴──────────┴──┘
//   底部 40px 命令栏:新建任务 + Agent / 模型 / 权限 + 用量
//
// 右侧竖向 48px 图标条:文件 / Git / 历史 选右栏面板;终端切底部面板;搜索开浮层。
// 主区:有文件打开 → 文件编辑 Tab;否则 → 当前任务会话回放(或对比组)。

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:multi_split_view/multi_split_view.dart';

import '../../../app/theme.dart';
import '../../../l10n/app_localizations.dart';
import '../../settings/application/settings_controller.dart';
import '../application/open_files_controller.dart';
import '../application/projects_controller.dart';
import '../application/tasks_controller.dart';
import '../data/code_bridge_provider.dart';
import '../domain/code_task.dart';
import 'branch_bar.dart';
import 'file_explorer_panel.dart';
import 'git_history_panel.dart';
import 'git_panel.dart';
import 'hooks_panel.dart';
import 'project_config_panel.dart';
import 'shell_terminal_panel.dart';
import 'skills_panel.dart';
import 'terminal_view.dart';
import 'usage_popover.dart';
import 'widgets/agent_stream.dart';
import 'widgets/compare_view.dart';
import 'widgets/file_editor_tabs.dart';
import 'widgets/file_search_dialog.dart';
import 'widgets/new_task_composer.dart';
import 'widgets/task_detail_header.dart';

/// claude/codex 在真 PTY 里跑(D6),主视图是 xterm 终端;biu 是进程内结构化流。
bool _isTerminalAgent(AgentKind a) =>
    a == AgentKind.claudeCode || a == AgentKind.codex;

/// 终态:PTY 进程已不在跑(实时终端无新字节)。
bool _isFinishedStatus(CodeTaskStatus s) =>
    s == CodeTaskStatus.done ||
    s == CodeTaskStatus.failed ||
    s == CodeTaskStatus.interrupted;

enum _RightPanel { files, git, history, skills, hooks, config }

class CodeWorkbenchPage extends ConsumerStatefulWidget {
  const CodeWorkbenchPage({super.key});

  @override
  ConsumerState<CodeWorkbenchPage> createState() => _CodeWorkbenchPageState();
}

class _CodeWorkbenchPageState extends ConsumerState<CodeWorkbenchPage> {
  _RightPanel _rightPanel = _RightPanel.files;
  bool _rightOpen = true; // 右栏面板是否展开(点激活图标可折叠)
  bool _terminalOpen = false;

  // tasks | main 两栏可拖拽;右栏面板放 split 外,便于折叠。
  late final MultiSplitViewController _splitCtrl =
      MultiSplitViewController(areas: [
    Area(size: 240, min: 180, max: 340),
    Area(),
  ]);

  @override
  void dispose() {
    _splitCtrl.dispose();
    super.dispose();
  }

  // 点右栏图标:已展开且点的是当前面板 → 折叠;否则切到该面板并展开。
  void _onPanel(_RightPanel p) {
    setState(() {
      if (_rightOpen && _rightPanel == p) {
        _rightOpen = false;
      } else {
        _rightPanel = p;
        _rightOpen = true;
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = AppLocalizations.of(context)!;
    final activeTask = ref.watch(activeCodeTaskProvider);
    final compareGroup = ref.watch(activeCompareGroupProvider);
    final focus = ref.watch(mainFocusProvider);

    // 切项目 → 清掉跨项目残留的文件 Tab(路径属于上个项目)。
    ref.listen(activeCodeProjectProvider, (prev, next) {
      if (prev?.id != next?.id) {
        ref.read(openFilesProvider.notifier).clear();
      }
    });

    // bridge 首次就绪(null→非 null,如热重启/崩溃采用了幸存 daemon)→ 拉活动 PTY
    // 对账:本地标 interrupted 但进程仍活的任务升级为 detached(可重新连接)。
    ref.listen(codeBridgeClientProvider, (prev, next) {
      if (prev == null && next != null) {
        next.liveTasks().then((ids) {
          ref.read(codeTasksProvider.notifier).reconcileLiveTasks(ids.toSet());
        }).catchError((Object _) {/* daemon 刚起/网络抖动,下次再对账 */});
      }
    });

    return Column(
      children: [
        Expanded(
          child: Row(
            children: [
              Expanded(
                child: MultiSplitViewTheme(
                  data: MultiSplitViewThemeData(
                    dividerThickness: 1,
                    dividerPainter: DividerPainters.background(
                      color: BiuTokens.borderSubtle,
                      highlightedColor: BiuTokens.purple,
                    ),
                  ),
                  child: MultiSplitView(
                    controller: _splitCtrl,
                    builder: (ctx, area) {
                      final idx = _splitCtrl.areas.indexOf(area);
                      if (idx == 0) return const _TasksPanel();
                      return Column(
                        children: [
                          Expanded(
                            child: _MainArea(
                              focus: focus,
                              compareGroup: compareGroup,
                            ),
                          ),
                          if (_terminalOpen)
                            _BottomTerminal(
                              onClose: () =>
                                  setState(() => _terminalOpen = false),
                            ),
                        ],
                      );
                    },
                  ),
                ),
              ),
              // 右栏面板(可折叠,固定宽 300)。
              if (_rightOpen)
                SizedBox(width: 300, child: _RightPanelBody(kind: _rightPanel)),
              _IconRail(
                panel: _rightPanel,
                rightOpen: _rightOpen,
                terminalOpen: _terminalOpen,
                onPanel: _onPanel,
                onToggleTerminal: () =>
                    setState(() => _terminalOpen = !_terminalOpen),
                onSearch: () => showFileSearchDialog(context, ref),
              ),
            ],
          ),
        ),
        // 底部命令栏 (40px)
        Container(
          height: 40,
          padding: const EdgeInsets.symmetric(horizontal: BiuTokens.space3),
          decoration: BoxDecoration(
            color: BiuTokens.surface,
            border: Border(top: BorderSide(color: BiuTokens.borderSubtle)),
          ),
          child: Row(
            children: [
              // 新建任务入口已上移到任务栏顶部;此处只显当前任务的
              // agent / 模型 / 权限 + 用量。
              if (activeTask != null) ...[
                Text(
                  activeTask.agent.label,
                  style: TextStyle(
                    fontSize: 11,
                    fontFamily: 'SF Mono',
                    color: BiuTokens.purple,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                if (activeTask.model != null && activeTask.model!.isNotEmpty) ...[
                  const SizedBox(width: 8),
                  Container(width: 1, height: 14, color: BiuTokens.borderSubtle),
                  const SizedBox(width: 8),
                  ConstrainedBox(
                    constraints: const BoxConstraints(maxWidth: 180),
                    child: Text(
                      activeTask.model!,
                      overflow: TextOverflow.ellipsis,
                      style: TextStyle(
                        fontSize: 11,
                        fontFamily: 'SF Mono',
                        color: BiuTokens.textSecondary,
                      ),
                    ),
                  ),
                ],
                const SizedBox(width: 8),
                Container(width: 1, height: 14, color: BiuTokens.borderSubtle),
                const SizedBox(width: 8),
                Text(
                  activeTask.mode.label,
                  style: TextStyle(
                    fontSize: 11,
                    fontFamily: 'SF Mono',
                    color: BiuTokens.textMuted,
                  ),
                ),
              ] else
                Text(
                  t.codeStatusBarHint,
                  style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
                ),
              const SizedBox(width: 8),
              Container(width: 1, height: 14, color: BiuTokens.borderSubtle),
              const SizedBox(width: 4),
              // 用量入口(M5):Claude 订阅 5h/7d + Codex 主/次额度。
              const UsagePopover(),
            ],
          ),
        ),
      ],
    );
  }
}

// ─── 右侧竖向图标条(48px) ────────────────────────────────

class _IconRail extends StatelessWidget {
  const _IconRail({
    required this.panel,
    required this.rightOpen,
    required this.terminalOpen,
    required this.onPanel,
    required this.onToggleTerminal,
    required this.onSearch,
  });
  final _RightPanel panel;
  final bool rightOpen;
  final bool terminalOpen;
  final ValueChanged<_RightPanel> onPanel;
  final VoidCallback onToggleTerminal;
  final VoidCallback onSearch;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 48,
      decoration: BoxDecoration(
        color: BiuTokens.bg,
        border: Border(left: BorderSide(color: BiuTokens.borderSubtle)),
      ),
      child: Column(
        children: [
          const SizedBox(height: 6),
          _railBtn(Icons.folder_outlined, '文件',
              active: rightOpen && panel == _RightPanel.files,
              onTap: () => onPanel(_RightPanel.files)),
          _railBtn(Icons.merge_type_rounded, 'Git',
              active: rightOpen && panel == _RightPanel.git,
              onTap: () => onPanel(_RightPanel.git)),
          _railBtn(Icons.history_rounded, '历史',
              active: rightOpen && panel == _RightPanel.history,
              onTap: () => onPanel(_RightPanel.history)),
          _railBtn(Icons.terminal_rounded, '终端',
              active: terminalOpen, onTap: onToggleTerminal),
          _railBtn(Icons.search_rounded, '搜索',
              active: false, onTap: onSearch),
          const Spacer(),
          // Skills + 项目配置 + 状态信号:偏设置类,置底,非高频。
          _railBtn(Icons.extension_rounded, 'Skills',
              active: rightOpen && panel == _RightPanel.skills,
              onTap: () => onPanel(_RightPanel.skills)),
          _railBtn(Icons.tune_rounded, '项目配置',
              active: rightOpen && panel == _RightPanel.config,
              onTap: () => onPanel(_RightPanel.config)),
          _railBtn(Icons.sensors_rounded, '状态信号 (hook)',
              active: rightOpen && panel == _RightPanel.hooks,
              onTap: () => onPanel(_RightPanel.hooks)),
          const SizedBox(height: 6),
        ],
      ),
    );
  }

  Widget _railBtn(IconData icon, String tip,
      {required bool active, required VoidCallback onTap}) {
    return Tooltip(
      message: tip,
      child: InkWell(
        onTap: onTap,
        child: Container(
          width: 48,
          height: 44,
          alignment: Alignment.center,
          decoration: BoxDecoration(
            border: Border(
              right: BorderSide(
                color: active ? BiuTokens.purple : Colors.transparent,
                width: 2,
              ),
            ),
          ),
          child: Icon(icon,
              size: 18,
              color: active ? BiuTokens.purple : BiuTokens.textSecondary),
        ),
      ),
    );
  }
}

class _RightPanelBody extends StatelessWidget {
  const _RightPanelBody({required this.kind});
  final _RightPanel kind;

  @override
  Widget build(BuildContext context) {
    return Container(
      color: BiuTokens.bg,
      child: switch (kind) {
        _RightPanel.files => const FileExplorerPanel(),
        _RightPanel.git => const GitPanel(),
        // 提交历史(CORE-5):gitLog 列表 + 点开看该提交 diff。
        _RightPanel.history => const GitHistoryPanel(),
        // 状态信号 hook(PERI-1):node/安装态 + 各 agent 就绪 + 一键装卸。
        _RightPanel.hooks => const HooksPanel(),
        // 项目配置(PERI-2):默认 agent/权限 + prompt 前缀,写 .biu/config.toml。
        _RightPanel.config => const ProjectConfigPanel(),
        // 项目级 Skills(PERI-3):把 ~/.biumind/skills symlink 进项目 .claude/.codex skills。
        _RightPanel.skills => const SkillsPanel(),
      },
    );
  }
}

// ─── 底部终端面板 ──────────────────────────────────────────

class _BottomTerminal extends StatelessWidget {
  const _BottomTerminal({required this.onClose});
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 280,
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        border: Border(top: BorderSide(color: BiuTokens.borderSubtle)),
      ),
      child: Column(
        children: [
          Container(
            height: 30,
            padding: const EdgeInsets.only(left: 12, right: 6),
            decoration: BoxDecoration(
              border: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
            ),
            child: Row(
              children: [
                Icon(Icons.terminal_rounded, size: 13, color: BiuTokens.textSecondary),
                const SizedBox(width: 6),
                Text('终端',
                    style: TextStyle(
                        fontSize: 12,
                        fontWeight: FontWeight.w600,
                        color: BiuTokens.textSecondary)),
                const Spacer(),
                InkWell(
                  onTap: onClose,
                  borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
                  child: Padding(
                    padding: const EdgeInsets.all(4),
                    child: Icon(Icons.close_rounded,
                        size: 15, color: BiuTokens.textMuted),
                  ),
                ),
              ],
            ),
          ),
          const Expanded(child: ShellTerminalPanel()),
        ],
      ),
    );
  }
}

// ─── 左栏：任务列表（reactive） ───────────────────────────

class _TasksPanel extends ConsumerStatefulWidget {
  const _TasksPanel();

  @override
  ConsumerState<_TasksPanel> createState() => _TasksPanelState();
}

class _TasksPanelState extends ConsumerState<_TasksPanel> {
  String _query = '';

  Future<void> _clearAll(List<CodeTask> tasks) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('清除全部任务?', style: TextStyle(fontSize: 15)),
        content: Text('将删除当前项目的 ${tasks.length} 个任务(含 worktree)。不可撤销。',
            style: const TextStyle(fontSize: 13)),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('取消')),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            style: FilledButton.styleFrom(backgroundColor: BiuTokens.error),
            child: const Text('全部清除'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    final ctl = ref.read(codeTasksProvider.notifier);
    for (final t in tasks) {
      await ctl.deleteTask(t.id);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = AppLocalizations.of(context)!;
    // M1：按激活项目过滤(projectScopedCodeTasksProvider),不再展示全量任务。
    final tasks = ref.watch(projectScopedCodeTasksProvider);
    final activeId = ref.watch(activeCodeTaskIdProvider);
    final activeProject = ref.watch(activeCodeProjectProvider);

    // 搜索过滤(标题 / prompt)。
    final q = _query.trim().toLowerCase();
    final visible = q.isEmpty
        ? tasks
        : tasks
            .where((task) =>
                task.title.toLowerCase().contains(q) ||
                task.prompt.toLowerCase().contains(q))
            .toList();
    // 分组:需要注意(input_required / detached 连接断开 / interrupted 中断)置顶,
    // 其余按星标优先、保持原序(needs-attention 归类)。
    bool needsAttention(CodeTask x) =>
        x.status == CodeTaskStatus.inputRequired ||
        x.status == CodeTaskStatus.detached ||
        x.status == CodeTaskStatus.interrupted;
    final attention = visible.where(needsAttention).toList();
    final rest = visible.where((x) => !needsAttention(x)).toList();
    final ordered = [...rest.where((x) => x.starred), ...rest.where((x) => !x.starred)];

    Widget row(CodeTask task) => _TaskRow(
          task: task,
          selected: task.id == activeId,
          onTap: () {
            // 点任务:选中 + 主区切回会话(即便有文件打开)。修「点任务无法切回」。
            ref.read(activeCodeTaskIdProvider.notifier).state = task.id;
            ref.read(mainFocusProvider.notifier).state = MainFocus.session;
          },
        );

    return Container(
      color: BiuTokens.bg,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          // M1: 当前项目分支条(只读)。
          if (activeProject != null)
            BranchBar(
                key: ValueKey('branch_${activeProject.id}'),
                project: activeProject),
          // 新建任务(置顶醒目入口)。无选中任务时高亮(此时主区是
          // NewTaskComposer)。
          Padding(
            padding: const EdgeInsets.fromLTRB(
                BiuTokens.space2, BiuTokens.space3, BiuTokens.space2, 0),
            child: _NewTaskButton(active: activeId == null),
          ),
          // 搜索任务。
          Padding(
            padding: const EdgeInsets.fromLTRB(
                BiuTokens.space3, BiuTokens.space2, BiuTokens.space3, BiuTokens.space2),
            child: SizedBox(
              height: 30,
              child: TextField(
                onChanged: (v) => setState(() => _query = v),
                style: const TextStyle(fontSize: 12.5),
                decoration: InputDecoration(
                  isDense: true,
                  contentPadding: const EdgeInsets.symmetric(vertical: 6),
                  prefixIcon:
                      Icon(Icons.search_rounded, size: 16, color: BiuTokens.textMuted),
                  prefixIconConstraints:
                      const BoxConstraints(minWidth: 30, minHeight: 30),
                  hintText: '搜索任务…',
                  hintStyle: TextStyle(fontSize: 12.5, color: BiuTokens.textMuted),
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
            ),
          ),
          // 任务数 + 新建 + 全部清除。
          Padding(
            padding: const EdgeInsets.fromLTRB(
                BiuTokens.space3, 0, BiuTokens.space3, BiuTokens.space2),
            child: Row(
              children: [
                Text(
                  '${tasks.length} 个任务',
                  style: TextStyle(
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                    color: BiuTokens.textSecondary,
                    letterSpacing: 0.5,
                  ),
                ),
                const Spacer(),
                if (tasks.isNotEmpty)
                  InkWell(
                    onTap: () => _clearAll(tasks),
                    borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
                    child: Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Icon(Icons.delete_outline_rounded,
                              size: 13, color: BiuTokens.textMuted),
                          const SizedBox(width: 3),
                          Text('全部清除',
                              style: TextStyle(
                                  fontSize: 11, color: BiuTokens.textMuted)),
                        ],
                      ),
                    ),
                  ),
              ],
            ),
          ),
          if (visible.isEmpty)
            Padding(
              padding: const EdgeInsets.all(BiuTokens.space3),
              child: Text(
                q.isEmpty ? t.codeNoTaskHint : '无匹配任务',
                style: TextStyle(fontSize: 12, color: BiuTokens.textMuted, height: 1.5),
              ),
            )
          else
            Expanded(
              child: ListView(
                children: [
                  if (attention.isNotEmpty) ...[
                    const _SectionHeader(label: '需要注意', color: Colors.orange),
                    for (final task in attention) row(task),
                    const SizedBox(height: 4),
                  ],
                  for (final task in ordered) row(task),
                ],
              ),
            ),
          const Spacer(),
          // Git footer 占位
          Container(
            decoration: BoxDecoration(
              border: Border(top: BorderSide(color: BiuTokens.borderSubtle)),
            ),
            padding: const EdgeInsets.fromLTRB(
              BiuTokens.space3,
              BiuTokens.space2,
              BiuTokens.space3,
              BiuTokens.space3,
            ),
            child: Row(
              children: [
                Icon(Icons.merge_type_rounded, size: 14, color: BiuTokens.textMuted),
                const SizedBox(width: 6),
                Text('main', style: TextStyle(fontSize: 12, color: BiuTokens.textSecondary)),
                const Spacer(),
                Text(
                  '${tasks.where((t) => t.status == CodeTaskStatus.running).length} running',
                  style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// 置顶「新建任务」按钮:点击取消选中 → 主区显 NewTaskComposer。
/// 无选中任务时(active)高亮,提示当前正处于新建态。
class _NewTaskButton extends ConsumerWidget {
  const _NewTaskButton({required this.active});
  final bool active;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Material(
      color: active ? BiuTokens.purpleSoft : Colors.transparent,
      borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      child: InkWell(
        onTap: () => ref.read(activeCodeTaskIdProvider.notifier).state = null,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        child: Container(
          height: 34,
          padding: const EdgeInsets.symmetric(horizontal: 10),
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
            border: Border.all(
              color: active ? BiuTokens.purple : BiuTokens.borderSubtle,
            ),
          ),
          child: Row(
            children: [
              Icon(Icons.add_rounded,
                  size: 16,
                  color: active ? BiuTokens.purple : BiuTokens.textSecondary),
              const SizedBox(width: 8),
              Text(
                '新建任务',
                style: TextStyle(
                  fontSize: 12.5,
                  fontWeight: FontWeight.w600,
                  color: active ? BiuTokens.purple : BiuTokens.text,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({required this.label, required this.color});
  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(BiuTokens.space3, 6, BiuTokens.space3, 2),
      child: Text(
        label,
        style: TextStyle(
          fontSize: 10.5,
          fontWeight: FontWeight.w700,
          letterSpacing: 0.6,
          color: color,
        ),
      ),
    );
  }
}

/// 任务状态 → 标题下方的状态词。
String _statusWord(CodeTaskStatus s) => switch (s) {
      CodeTaskStatus.queued => '排队中',
      CodeTaskStatus.running => '运行中',
      CodeTaskStatus.paused => '已暂停',
      CodeTaskStatus.inputRequired => '需要确认',
      CodeTaskStatus.done => '已完成',
      CodeTaskStatus.failed => '失败',
      CodeTaskStatus.interrupted => '已中断',
      CodeTaskStatus.detached => '终端连接断开',
    };

/// 状态词颜色:需关注的(需要确认/失败/连接断开)着色,其余走 muted(克制风)。
Color _statusWordColor(CodeTaskStatus s) => switch (s) {
      CodeTaskStatus.inputRequired => Colors.orange,
      CodeTaskStatus.detached => Colors.orange,
      CodeTaskStatus.failed => Colors.red.shade400,
      _ => BiuTokens.textMuted,
    };

/// agent 角标图标(无 svg 资源,用克制的 Icon 占位)。
IconData _agentBadgeIcon(AgentKind a) => switch (a) {
      AgentKind.claudeCode => Icons.auto_awesome,
      AgentKind.codex => Icons.bolt_rounded,
      AgentKind.biu => Icons.smart_toy_outlined,
    };

class _TaskRow extends ConsumerStatefulWidget {
  const _TaskRow({
    required this.task,
    required this.selected,
    required this.onTap,
  });
  final CodeTask task;
  final bool selected;
  final VoidCallback onTap;

  @override
  ConsumerState<_TaskRow> createState() => _TaskRowState();
}

class _TaskRowState extends ConsumerState<_TaskRow> {
  bool _hov = false;

  CodeTask get task => widget.task;
  bool get selected => widget.selected;

  Future<void> _menu(BuildContext context, WidgetRef ref, Offset pos) async {
    final sel = await showMenu<String>(
      context: context,
      position: RelativeRect.fromLTRB(pos.dx, pos.dy, pos.dx, pos.dy),
      items: const [
        PopupMenuItem(value: 'rename', child: Text('重命名')),
        PopupMenuItem(value: 'ai', child: Text('AI 命名')),
        PopupMenuItem(value: 'copy', child: Text('复制 Prompt')),
        PopupMenuItem(value: 'star', child: Text('星标 / 取消')),
        PopupMenuDivider(),
        PopupMenuItem(value: 'delete', child: Text('删除')),
      ],
    );
    if (!context.mounted) return;
    switch (sel) {
      case 'rename':
        await _rename(context, ref);
      case 'ai':
        await _aiName(context, ref);
      case 'copy':
        await Clipboard.setData(ClipboardData(text: task.prompt));
        if (context.mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('已复制 Prompt'), duration: Duration(seconds: 1)),
          );
        }
      case 'star':
        ref.read(codeTasksProvider.notifier).toggleStar(task.id);
      case 'delete':
        await _delete(context, ref);
    }
  }

  Future<void> _rename(BuildContext context, WidgetRef ref) async {
    final ctrl = TextEditingController(text: task.title);
    final name = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('重命名任务', style: TextStyle(fontSize: 15)),
        content: TextField(
          controller: ctrl,
          autofocus: true,
          decoration: const InputDecoration(hintText: '任务标题'),
          onSubmitted: (v) => Navigator.pop(ctx, v.trim()),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('取消')),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, ctrl.text.trim()),
            child: const Text('保存'),
          ),
        ],
      ),
    );
    if (name != null && name.isNotEmpty) {
      ref.read(codeTasksProvider.notifier).renameTask(task.id, name);
    }
  }

  Future<void> _aiName(BuildContext context, WidgetRef ref) async {
    final bridge = ref.read(codeBridgeClientProvider);
    final messenger = ScaffoldMessenger.of(context);
    if (bridge == null) {
      messenger.showSnackBar(
        const SnackBar(content: Text('本地 daemon 未就绪,无法 AI 命名')),
      );
      return;
    }
    messenger.showSnackBar(
      const SnackBar(content: Text('AI 命名中…'), duration: Duration(seconds: 1)),
    );
    try {
      final name = await bridge.generateAgentName(task.prompt);
      if (name.isNotEmpty) {
        ref.read(codeTasksProvider.notifier).renameTask(task.id, name);
      }
    } catch (e) {
      messenger.showSnackBar(SnackBar(content: Text('AI 命名失败: $e')));
    }
  }

  Future<void> _delete(BuildContext context, WidgetRef ref) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('删除「${task.title}」?', style: const TextStyle(fontSize: 15)),
        content: const Text('将删除该任务(含 worktree)。不可撤销。',
            style: TextStyle(fontSize: 13)),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('取消')),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            style: FilledButton.styleFrom(backgroundColor: BiuTokens.error),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (ok == true) {
      await ref.read(codeTasksProvider.notifier).deleteTask(task.id);
    }
  }

  @override
  Widget build(BuildContext context) {
    final myDeviceId =
        ref.watch(settingsControllerProvider).valueOrNull?.codeOriginDeviceId;
    final isRemote = task.originDeviceId != null &&
        myDeviceId != null &&
        task.originDeviceId != myDeviceId;
    final hasWorktree = task.workspace?.branchName != null;
    final bg = selected
        ? BiuTokens.purpleSoft
        : (_hov ? BiuTokens.surfaceMuted : Colors.transparent);

    return MouseRegion(
      onEnter: (_) => setState(() => _hov = true),
      onExit: (_) => setState(() => _hov = false),
      child: GestureDetector(
        onSecondaryTapDown: (d) => _menu(context, ref, d.globalPosition),
        onLongPressStart: (d) => _menu(context, ref, d.globalPosition),
        child: Container(
          margin: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
          child: Material(
            color: bg,
            borderRadius: BorderRadius.circular(6),
            child: InkWell(
              onTap: widget.onTap,
              borderRadius: BorderRadius.circular(6),
              child: Padding(
                padding: const EdgeInsets.fromLTRB(12, 7, 8, 7),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Padding(
                      padding: const EdgeInsets.only(top: 1),
                      child: _StatusDot(status: task.status),
                    ),
                    const SizedBox(width: 9),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                            task.title,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              fontSize: 12.5,
                              fontWeight:
                                  selected ? FontWeight.w600 : FontWeight.w500,
                              color: selected ? BiuTokens.purple : BiuTokens.text,
                            ),
                          ),
                          const SizedBox(height: 1),
                          Row(
                            children: [
                              Text(
                                _statusWord(task.status),
                                style: TextStyle(
                                  fontSize: 11,
                                  color: _statusWordColor(task.status),
                                ),
                              ),
                              // done + worktree → +N/−N diff。
                              if (task.status == CodeTaskStatus.done &&
                                  hasWorktree)
                                _RowDiffStats(task: task),
                            ],
                          ),
                        ],
                      ),
                    ),
                    const SizedBox(width: 6),
                    _trailing(isRemote, hasWorktree),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  /// 尾部:静止态显 worktree/远程/已星标/agent 角标;hover 态显 星标+删除
  /// (agent 角标 hover 隐藏,星标/删除 hover 才出)。
  Widget _trailing(bool isRemote, bool hasWorktree) {
    if (_hov) {
      return Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          _iconBtn(
            task.starred ? Icons.star_rounded : Icons.star_outline_rounded,
            color: task.starred ? Colors.amber : BiuTokens.textMuted,
            tip: task.starred ? '取消星标' : '星标',
            onTap: () =>
                ref.read(codeTasksProvider.notifier).toggleStar(task.id),
          ),
          _iconBtn(
            Icons.delete_outline_rounded,
            color: BiuTokens.textMuted,
            tip: '删除',
            onTap: () => _delete(context, ref),
          ),
        ],
      );
    }
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        if (hasWorktree)
          Tooltip(
            message: task.workspace!.branchName!,
            child: Icon(Icons.merge_type_rounded,
                size: 12, color: BiuTokens.textMuted),
          ),
        if (isRemote && (task.originDeviceLabel?.isNotEmpty ?? false)) ...[
          const SizedBox(width: 4),
          Tooltip(
            message: '来自 ${task.originDeviceLabel}',
            child: Icon(Icons.devices_rounded,
                size: 12, color: BiuTokens.textMuted),
          ),
        ],
        if (task.starred) ...[
          const SizedBox(width: 4),
          Icon(Icons.star_rounded, size: 13, color: Colors.amber),
        ],
        const SizedBox(width: 6),
        Icon(_agentBadgeIcon(task.agent), size: 13, color: BiuTokens.textMuted),
        const SizedBox(width: 2),
      ],
    );
  }

  Widget _iconBtn(IconData icon,
      {required Color color, required String tip, required VoidCallback onTap}) {
    return Tooltip(
      message: tip,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
        child: Padding(
          padding: const EdgeInsets.all(3),
          child: Icon(icon, size: 14, color: color),
        ),
      ),
    );
  }
}

/// 任务卡片内的精简 diff(无边框,行内 +N −N;复用 G3 的 taskDiffStatsProvider)。
class _RowDiffStats extends ConsumerWidget {
  const _RowDiffStats({required this.task});
  final CodeTask task;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final ws = task.workspace;
    if (ws == null) return const SizedBox.shrink();
    final base = (ws.baseBranch?.isNotEmpty ?? false) ? ws.baseBranch! : 'main';
    final stats =
        ref.watch(taskDiffStatsProvider((path: ws.localPath, base: base)));
    final (add, del) = switch (stats) {
      AsyncData(:final value) => (value.additions, value.deletions),
      _ => (0, 0),
    };
    if (add == 0 && del == 0) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(left: 8),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text('+$add',
              style: TextStyle(
                  fontSize: 11,
                  fontWeight: FontWeight.w600,
                  fontFamily: 'SF Mono',
                  color: BiuTokens.green)),
          const SizedBox(width: 5),
          Text('−$del',
              style: TextStyle(
                  fontSize: 11,
                  fontWeight: FontWeight.w600,
                  fontFamily: 'SF Mono',
                  color: Colors.red.shade400)),
        ],
      ),
    );
  }
}

class _StatusDot extends StatelessWidget {
  const _StatusDot({required this.status});
  final CodeTaskStatus status;

  @override
  Widget build(BuildContext context) {
    final (color, icon) = switch (status) {
      CodeTaskStatus.queued => (BiuTokens.textMuted, Icons.schedule),
      CodeTaskStatus.running => (BiuTokens.purple, Icons.play_arrow_rounded),
      CodeTaskStatus.paused => (BiuTokens.textMuted, Icons.pause),
      CodeTaskStatus.inputRequired => (Colors.orange, Icons.priority_high_rounded),
      CodeTaskStatus.done => (BiuTokens.green, Icons.check_rounded),
      CodeTaskStatus.failed => (Colors.red, Icons.close_rounded),
      CodeTaskStatus.interrupted => (BiuTokens.textMuted, Icons.stop_rounded),
      CodeTaskStatus.detached => (Colors.orange, Icons.link_off_rounded),
    };
    return Icon(icon, size: 14, color: color);
  }
}

// ─── 主区：文件编辑 Tab / 任务会话回放 / 对比 ──────────────────

class _MainArea extends ConsumerWidget {
  const _MainArea({required this.focus, this.compareGroup});
  final MainFocus focus;
  final List<CodeTask>? compareGroup;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final hasOpenFiles = ref.watch(openFilesProvider).isEmpty == false;
    final activeTask = ref.watch(activeCodeTaskProvider);

    Widget body;
    if (focus == MainFocus.files && hasOpenFiles) {
      // 聚焦文件且有打开的文件 → 文件编辑 Tab(点任务会切回会话,见 focus 监听)。
      body = const FileEditorTabs();
    } else if (compareGroup != null) {
      body = CompareView(tasks: compareGroup!);
    } else if (activeTask == null) {
      // 无选中任务 → 主区显新建任务页(替代原弹窗)。
      body = const NewTaskComposer();
    } else {
      body = Column(
        children: [
          TaskDetailHeader(
            key: ValueKey('header_${activeTask.id}'),
            task: activeTask,
          ),
          Expanded(
            child: _AgentArea(
              key: ValueKey('agent_${activeTask.id}'),
              task: activeTask,
            ),
          ),
        ],
      );
    }

    return Container(color: BiuTokens.surface, child: body);
  }
}

/// 任务里是否有「可渲染」的结构化事件(正文/工具/审批)。CostUpdate /
/// TaskFinished / AgentStatus / SessionInfo 不计 —— 它们只供详情头/续跑,
/// AgentStream 不会渲成消息,光有这些等于空回放。
bool _hasRenderableEvents(CodeTask task) => task.events.any((e) =>
    e is TextDelta ||
    e is ToolUseStart ||
    e is ToolUseResult ||
    e is PermissionAsk);

/// Agent 内容区 —— **状态驱动**:
///   - 运行中(queued/running/inputRequired/paused)或还没攒到结构化事件
///     → 实时终端 CodeTerminalView(PTY 字节;已落盘,重开经 pty.replayLog 回放)。
///   - 终态(done/failed/interrupted)且有结构化事件
///     → 结构化 markdown 回放 AgentStream。
/// biu 进程内运行无 PTY → 始终结构化流。
class _AgentArea extends StatelessWidget {
  const _AgentArea({super.key, required this.task});
  final CodeTask task;

  @override
  Widget build(BuildContext context) {
    // biu:无终端,只有结构化流。
    if (!_isTerminalAgent(task.agent)) {
      return AgentStream(task: task);
    }
    // detached(终端连接断开、进程仍活):结构化回放(有事件)/ 终端回放(无)+ 顶部
    // 「重新连接」banner。点重连 → status→running → 自动切回实时终端。
    if (task.status == CodeTaskStatus.detached) {
      final body = _hasRenderableEvents(task)
          ? AgentStream(key: ValueKey('session_${task.id}'), task: task)
          : CodeTerminalView(
              key: ValueKey('pty_${task.id}'), ptyId: task.id, finished: true);
      return Column(
        children: [_StatusActionBanner(task: task), Expanded(child: body)],
      );
    }
    // claude/codex:结束 + 有结构化内容 → SessionView 式 markdown 回放。
    final finished = _isFinishedStatus(task.status);
    if (finished && _hasRenderableEvents(task)) {
      final body = AgentStream(key: ValueKey('session_${task.id}'), task: task);
      // 中断任务:结构化回放上方加恢复 banner(⚠️ + 恢复/标记完成)。
      if (task.status == CodeTaskStatus.interrupted) {
        return Column(
          children: [
            _StatusActionBanner(task: task),
            Expanded(child: body),
          ],
        );
      }
      return body;
    }
    // 否则(运行中 / 结束但无结构化)→ 实时终端 + 落盘回放。
    return CodeTerminalView(
      key: ValueKey('pty_${task.id}'),
      ptyId: task.id,
      finished: finished,
    );
  }
}

/// 中断/断连任务顶部的动作 banner:
///   - interrupted(进程已死)→ ⚠️「任务已中断」+「恢复」(--resume 起新会话,无
///     可续跑会话时禁用)。
///   - detached(进程仍活、连接断)→ 🔗「终端连接断开」+「重新连接」(reattach 接回
///     活动终端,不 spawn)。
/// 二者都带「标记完成」。
class _StatusActionBanner extends ConsumerWidget {
  const _StatusActionBanner({required this.task});
  final CodeTask task;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final detached = task.status == CodeTaskStatus.detached;
    final canResume = task.canResume;
    final (icon, title) = detached
        ? (Icons.link_off_rounded, '终端连接断开')
        : (Icons.warning_amber_rounded, '任务已中断');
    return Container(
      constraints: const BoxConstraints(minHeight: 40),
      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 6),
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        border: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
      ),
      child: Row(
        children: [
          Icon(icon, size: 17, color: Colors.orange),
          const SizedBox(width: 8),
          Text(
            title,
            style: TextStyle(
              fontSize: 12.5,
              fontWeight: FontWeight.w700,
              color: BiuTokens.text,
            ),
          ),
          const Spacer(),
          if (detached)
            _bannerBtn(
              icon: Icons.link_rounded,
              label: '重新连接',
              primary: true,
              enabled: true,
              tip: '接回仍在跑的终端(不重启会话)',
              onTap: () => ref.read(codeTasksProvider.notifier).reattach(task.id),
            )
          else
            _bannerBtn(
              icon: Icons.refresh_rounded,
              label: '恢复',
              primary: true,
              enabled: canResume,
              tip: canResume ? '从上次会话继续(--resume)' : '无可续跑的会话(未捕获会话 id)',
              onTap: () => ref.read(codeTasksProvider.notifier).resume(task.id),
            ),
          const SizedBox(width: 6),
          _bannerBtn(
            icon: Icons.check_circle_outline_rounded,
            label: '标记完成',
            primary: false,
            enabled: true,
            onTap: () => ref.read(codeTasksProvider.notifier).markComplete(task.id),
          ),
        ],
      ),
    );
  }

  Widget _bannerBtn({
    required IconData icon,
    required String label,
    required bool primary,
    required bool enabled,
    required VoidCallback onTap,
    String? tip,
  }) {
    final fg = !enabled
        ? BiuTokens.textMuted
        : (primary ? BiuTokens.purple : BiuTokens.green);
    final border = !enabled
        ? BiuTokens.borderSubtle
        : (primary ? BiuTokens.purple : BiuTokens.green).withValues(alpha: 0.4);
    final btn = Opacity(
      opacity: enabled ? 1 : 0.5,
      child: Material(
        color: enabled && !primary
            ? BiuTokens.green.withValues(alpha: 0.06)
            : Colors.transparent,
        borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
        child: InkWell(
          onTap: enabled ? onTap : null,
          borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 5),
            decoration: BoxDecoration(
              borderRadius: BorderRadius.circular(BiuTokens.radiusXs),
              border: Border.all(color: border),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(icon, size: 13, color: fg),
                const SizedBox(width: 5),
                Text(label,
                    style: TextStyle(
                        fontSize: 12, fontWeight: FontWeight.w600, color: fg)),
              ],
            ),
          ),
        ),
      ),
    );
    return tip != null ? Tooltip(message: tip, child: btn) : btn;
  }
}

