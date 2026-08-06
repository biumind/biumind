// Creation 模块顶层壳 — 在 biumind 顶层 _AppShell 内部再嵌套一层.
//
// 布局 (桌面 ≥ 768px):
//   ┌──────────────┬──────────────────────────────┐
//   │ NavRail 220px│  ShellRoute child            │
//   │ 灵感         │                              │
//   │ 中心         │                              │
//   │ 作品         │                              │
//   │ 广场         │                              │
//   │ 积分         │                              │
//   └──────────────┴──────────────────────────────┘
//
// 移动 (<768px): NavRail 隐藏, 顶部 SegmentedTabBar.
//
// 当前页 highlight 由 GoRouter loc.path startsWith 判断.

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme.dart';
import '../../../l10n/app_localizations.dart';
import '../application/credits_controller.dart';
import '../application/tasks_controller.dart';
import '../domain/creation_task.dart';
import 'widgets/active_task_banner.dart';
import 'widgets/connection_banner.dart';

class CreationShell extends ConsumerStatefulWidget {
  const CreationShell({super.key, required this.child});
  final Widget child;

  static const _mobileBreakpoint = 768.0;

  @override
  ConsumerState<CreationShell> createState() => _CreationShellState();
}

class _CreationShellState extends ConsumerState<CreationShell> {
  StreamSubscription<TaskNotification>? _notifSub;
  /// 终态浮条消息 (完成/失败/退还). _onNotification 设置, 2s 后清空.
  TerminalMessage? _terminal;
  Timer? _terminalTimer;

  @override
  void initState() {
    super.initState();
    // 订阅 controller.notifications, 终态切换时弹 SnackBar.
    // 在 build 第一帧后才能拿到 controller (provider 创建时机).
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      final controller = ref.read(tasksControllerProvider.notifier);
      _notifSub = controller.notifications.listen(_onNotification);
    });
  }

  @override
  void dispose() {
    _notifSub?.cancel();
    _terminalTimer?.cancel();
    super.dispose();
  }

  void _onNotification(TaskNotification n) {
    if (!mounted) return;
    // 任何任务终态切换都刷新积分余额: completed (扣费已 settle) /
    // refunded (失败退还) / failed (release hold). 5min 缓存里 stale 的
    // 数字立即被新数据替换,sidebar chip + 会员中心当场刷新.
    ref.invalidate(creditsBalanceProvider);
    // R2 任务2: 终态改顶部 ActiveTaskBanner 浮条 + Haptic (替代 SnackBar).
    final TerminalMessage term;
    switch (n.kind) {
      case TaskNotificationKind.completed:
        term = const TerminalMessage(
          color: BiuTokens.green,
          icon: Icons.auto_awesome,
          text: '生成完成 ✨',
        );
        HapticFeedback.mediumImpact();
      case TaskNotificationKind.failed:
        term = TerminalMessage(
          color: BiuTokens.error,
          icon: Icons.error_outline,
          text: n.errorMessage?.isNotEmpty == true
              ? '生成失败: ${n.errorMessage}'
              : '生成失败',
        );
        HapticFeedback.heavyImpact();
      case TaskNotificationKind.refunded:
        term = TerminalMessage(
          color: BiuTokens.green,
          icon: Icons.undo,
          text: '已退还 ${n.credits} 积分'
              '${n.errorMessage != null && n.errorMessage!.isNotEmpty ? ' · ${n.errorMessage}' : ''}',
        );
        HapticFeedback.selectionClick();
    }
    setState(() => _terminal = term);
    _terminalTimer?.cancel();
    _terminalTimer = Timer(const Duration(seconds: 2), () {
      if (mounted) setState(() => _terminal = null);
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = AppLocalizations.of(context)!;
    final loc = GoRouterState.of(context).uri.path;
    final width = MediaQuery.of(context).size.width;
    final isMobile = width < CreationShell._mobileBreakpoint;
    // R2 任务2: 顶部进度浮条数据源 — active 任务 (按 createdAt desc).
    final tasksState = ref.watch(tasksControllerProvider);
    final activeTasks = tasksState.activeIds
        .map((id) => tasksState.tasks[id])
        .whereType<CreationTask>()
        .toList();

    // W7 IA 重设计: 「积分充值」从这里移除, 统一到「会员中心」(/membership).
    // CreditIndicator 点击 + 设置页 + 头像下拉为新入口.
    final items = <_CreationNavItem>[
      _CreationNavItem(
          Icons.lightbulb_outline, t.creationInspiration, '/creation'),
      _CreationNavItem(
          Icons.palette_outlined, t.creationStudio, '/creation/center'),
      _CreationNavItem(
          Icons.image_outlined, t.creationWorks, '/creation/works'),
      _CreationNavItem(
          Icons.collections_outlined, t.creationGallery, '/creation/gallery'),
    ];

    return Scaffold(
      backgroundColor: BiuTokens.bg,
      body: Column(
        children: [
          const ConnectionBanner(),
          ActiveTaskBanner(
            activeTasks: activeTasks,
            terminal: _terminal,
            onCancel: activeTasks.isEmpty
                ? null
                : () => ref
                    .read(tasksControllerProvider.notifier)
                    .cancel(activeTasks.first.id),
          ),
          Expanded(
            child: isMobile
                ? Column(
                    children: [
                      _MobileTabStrip(items: items, currentPath: loc),
                      Container(height: 1, color: BiuTokens.borderSubtle),
                      Expanded(child: widget.child),
                    ],
                  )
                : Row(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      _DesktopNavRail(items: items, currentPath: loc),
                      Container(width: 1, color: BiuTokens.borderSubtle),
                      Expanded(child: widget.child),
                    ],
                  ),
          ),
        ],
      ),
    );
  }
}

class _CreationNavItem {
  final IconData icon;
  final String label;
  final String path;
  const _CreationNavItem(this.icon, this.label, this.path);
}

/// 桌面 220px NavRail.
class _DesktopNavRail extends StatelessWidget {
  const _DesktopNavRail({required this.items, required this.currentPath});
  final List<_CreationNavItem> items;
  final String currentPath;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 220,
      color: BiuTokens.surfaceMuted,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const SizedBox(height: 12),
          for (final it in items)
            _NavTile(
              item: it,
              active: _isActive(currentPath, it.path),
            ),
          const Spacer(),
        ],
      ),
    );
  }
}

class _NavTile extends StatelessWidget {
  const _NavTile({required this.item, required this.active});
  final _CreationNavItem item;
  final bool active;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      child: Material(
        color: active ? BiuTokens.purpleSoft : Colors.transparent,
        borderRadius: BorderRadius.circular(6),
        child: InkWell(
          borderRadius: BorderRadius.circular(6),
          onTap: () => context.go(item.path),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
            child: Row(
              children: [
                Icon(item.icon,
                    size: 18,
                    color: active ? BiuTokens.purple : BiuTokens.textMuted),
                const SizedBox(width: 10),
                Expanded(
                  child: Text(
                    item.label,
                    style: TextStyle(
                      color: active ? BiuTokens.text : BiuTokens.textMuted,
                      fontWeight:
                          active ? FontWeight.w600 : FontWeight.normal,
                      fontSize: 14,
                    ),
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// 移动 SegmentedTabBar (横滚, 5 段).
class _MobileTabStrip extends StatelessWidget {
  const _MobileTabStrip({required this.items, required this.currentPath});
  final List<_CreationNavItem> items;
  final String currentPath;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 44,
      child: ListView.separated(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        scrollDirection: Axis.horizontal,
        itemBuilder: (_, i) {
          final it = items[i];
          final active = _isActive(currentPath, it.path);
          return TextButton.icon(
            onPressed: () => context.go(it.path),
            icon: Icon(it.icon, size: 16),
            label: Text(it.label),
            style: TextButton.styleFrom(
              foregroundColor: active ? BiuTokens.purple : BiuTokens.textMuted,
              backgroundColor:
                  active ? BiuTokens.purpleSoft : Colors.transparent,
            ),
          );
        },
        separatorBuilder: (_, _) => const SizedBox(width: 4),
        itemCount: items.length,
      ),
    );
  }
}

/// active 判断: /creation 仅在 loc==/creation 时高亮 (避免与 /creation/center 冲突).
/// 其他子路径: startsWith.
bool _isActive(String loc, String itemPath) {
  if (itemPath == '/creation') {
    return loc == '/creation' || loc == '/creation/';
  }
  return loc == itemPath || loc.startsWith('$itemPath/');
}
