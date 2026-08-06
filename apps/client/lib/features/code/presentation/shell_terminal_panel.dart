// ShellTerminalPanel — 独立 shell 终端面板(「终端」Tab 的内容)。
//
// 与任务无关:在当前项目目录里开交互 shell(zsh/bash),每项目可多开,
// 进 Tab 自动开第一个。顶部一条 shell 子 Tab(Shell 1 / 2 …)+ 关闭 × + 新建 +;
// 下方 IndexedStack 让后台 shell 保活(切 shell 不丢输出)。
//
// 渲染复用 CodeTerminalView(ptyId);会话生命周期在 ShellTerminalController。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../application/projects_controller.dart';
import '../application/shell_terminal_controller.dart';
import '../data/code_bridge_provider.dart';
import 'terminal_view.dart';

class ShellTerminalPanel extends ConsumerStatefulWidget {
  const ShellTerminalPanel({super.key});

  @override
  ConsumerState<ShellTerminalPanel> createState() => _ShellTerminalPanelState();
}

class _ShellTerminalPanelState extends ConsumerState<ShellTerminalPanel> {
  int _selected = 0;
  bool _kickedOff = false; // 本面板实例是否已触发过自动开 shell(防重复 / 竞态)
  bool _openFailed = false; // 上次开 shell 失败(bridge 未连上)→ 显示重试

  Future<void> _openShell(String projectId, String cwd) async {
    if (_openFailed) setState(() => _openFailed = false);
    final session =
        await ref.read(shellTerminalControllerProvider.notifier).open(projectId, cwd);
    if (!mounted) return;
    if (session == null) {
      // bridge 未连上 → 标记失败,UI 给重试(不无限自动重试,防 spam)。
      setState(() => _openFailed = true);
      return;
    }
    final list = ref.read(shellTerminalControllerProvider)[projectId] ?? const [];
    setState(() => _selected = list.length - 1);
  }

  Future<void> _closeShell(String projectId, String sessionId) async {
    await ref
        .read(shellTerminalControllerProvider.notifier)
        .close(projectId, sessionId);
    if (!mounted) return;
    final list = ref.read(shellTerminalControllerProvider)[projectId] ?? const [];
    setState(() => _selected = _selected.clamp(0, (list.length - 1).clamp(0, 999)));
  }

  @override
  Widget build(BuildContext context) {
    final project = ref.watch(activeCodeProjectProvider);
    final bridgeReady = ref.watch(codeBridgeClientProvider) != null;

    if (project == null) {
      return _Hint(
        icon: Icons.folder_off_outlined,
        text: '打开一个项目以使用终端',
      );
    }
    if (!bridgeReady) {
      return _Hint(
        icon: Icons.cloud_off_rounded,
        text: '本地 daemon 未就绪 —— 终端不可用\n登录后桌面端会自动启动 biu serve',
      );
    }

    final shells = ref.watch(shellTerminalControllerProvider
        .select((m) => m[project.id] ?? const <ShellSession>[]));

    // 进 Tab 自动开第一个 shell(本实例仅触发一次;已有 shell 则不开)。
    if (shells.isEmpty && !_kickedOff) {
      _kickedOff = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _openShell(project.id, project.path);
      });
    }

    if (shells.isEmpty) {
      // 自动开进行中 / 失败待重试 / 用户关光了所有 shell。
      return Column(
        children: [
          _ShellTabBar(
            shells: const [],
            selected: 0,
            onSelect: (_) {},
            onClose: (_) {},
            onNew: () => _openShell(project.id, project.path),
          ),
          Expanded(
            child: _openFailed
                ? _Hint(
                    icon: Icons.error_outline_rounded,
                    text: '终端连接失败 —— 本地 daemon 可能未就绪\n点右上角 + 重试')
                : const _Hint(icon: Icons.terminal_rounded, text: '正在打开终端…'),
          ),
        ],
      );
    }

    final selected = _selected.clamp(0, shells.length - 1);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _ShellTabBar(
          shells: shells,
          selected: selected,
          onSelect: (i) => setState(() => _selected = i),
          onClose: (i) => _closeShell(project.id, shells[i].id),
          onNew: () => _openShell(project.id, project.path),
        ),
        Expanded(
          // IndexedStack: 所有 shell 同时挂载(后台保活),只显示选中那个。
          child: IndexedStack(
            index: selected,
            children: [
              for (final s in shells)
                CodeTerminalView(key: ValueKey('shell_${s.id}'), ptyId: s.ptyId),
            ],
          ),
        ),
      ],
    );
  }
}

class _ShellTabBar extends StatelessWidget {
  const _ShellTabBar({
    required this.shells,
    required this.selected,
    required this.onSelect,
    required this.onClose,
    required this.onNew,
  });

  final List<ShellSession> shells;
  final int selected;
  final ValueChanged<int> onSelect;
  final ValueChanged<int> onClose;
  final VoidCallback onNew;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 32,
      decoration: BoxDecoration(
        color: BiuTokens.bg,
        border: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
      ),
      child: Row(
        children: [
          Expanded(
            child: ListView.builder(
              scrollDirection: Axis.horizontal,
              itemCount: shells.length,
              itemBuilder: (ctx, i) => _ShellTab(
                label: shells[i].label,
                selected: i == selected,
                onTap: () => onSelect(i),
                onClose: () => onClose(i),
              ),
            ),
          ),
          Tooltip(
            message: '新建终端',
            child: InkWell(
              onTap: onNew,
              child: const SizedBox(
                width: 34,
                height: 32,
                child: Icon(Icons.add_rounded, size: 16),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _ShellTab extends StatelessWidget {
  const _ShellTab({
    required this.label,
    required this.selected,
    required this.onTap,
    required this.onClose,
  });

  final String label;
  final bool selected;
  final VoidCallback onTap;
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.only(left: 12, right: 6),
        decoration: BoxDecoration(
          border: Border(
            bottom: BorderSide(
              color: selected ? BiuTokens.purple : Colors.transparent,
              width: 2,
            ),
          ),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              Icons.terminal_rounded,
              size: 13,
              color: selected ? BiuTokens.purple : BiuTokens.textSecondary,
            ),
            const SizedBox(width: 6),
            Text(
              label,
              style: TextStyle(
                fontSize: 12,
                fontFamily: 'SF Mono',
                fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
                color: selected ? BiuTokens.text : BiuTokens.textSecondary,
              ),
            ),
            const SizedBox(width: 4),
            InkWell(
              onTap: onClose,
              borderRadius: BorderRadius.circular(4),
              child: Padding(
                padding: const EdgeInsets.all(3),
                child: Icon(Icons.close_rounded,
                    size: 12, color: BiuTokens.textMuted),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Hint extends StatelessWidget {
  const _Hint({required this.icon, required this.text});
  final IconData icon;
  final String text;
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 32, color: BiuTokens.textMuted),
          const SizedBox(height: 10),
          Text(
            text,
            textAlign: TextAlign.center,
            style: TextStyle(
                fontSize: 12.5, color: BiuTokens.textSecondary, height: 1.5),
          ),
        ],
      ),
    );
  }
}
