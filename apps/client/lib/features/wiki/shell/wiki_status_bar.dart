// Wiki 模块底部状态栏 —— Phase 0 简化版。
//
// 仅显示：sync 状态点（idle/synced/syncing/error）+ 当前项目名（如有）
// + 命令面板提示（暂未接入，先静态展示 ⌘K）。
//
// 后续 Phase 1+ 会把 knowcode 原版的 activity 计数 / sources parsing
// 计数 / 命令面板回调一起补上。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/layout/form_factor.dart';
import '../../../core/ui/biu_pulse_dot.dart';
import '../application/wiki_controller.dart';
import '../presentation/activity/activity_drawer.dart';
import '../presentation/activity/activity_provider.dart';
import 'wiki_command_palette.dart';
import 'wiki_tokens.dart';

class WikiStatusBar extends ConsumerWidget {
  const WikiStatusBar({super.key, required this.projectId});

  final String projectId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final stateAsync = ref.watch(wikiControllerProvider);
    final activeProject = stateAsync.valueOrNull?.activeProject;
    final lastError = stateAsync.valueOrNull?.lastError;
    final hasError = lastError != null;
    final loading = stateAsync.isLoading;

    final (Color dot, String label, bool live) = switch ((loading, hasError)) {
      (true, _) => (BiuTokens.textMuted, '同步中', false),
      (_, true) => (BiuTokens.error, '同步失败', false),
      _ => (BiuTokens.success, '已同步', true),
    };

    String? projectName;
    if (projectId.isNotEmpty) {
      projectName = activeProject?.name;
      if (projectName == null && projectId.length >= 8) {
        projectName = projectId.substring(0, 8);
      }
    }

    return Container(
      height: WikiTokens.statusBarHeight,
      color: BiuTokens.bg,
      padding: const EdgeInsets.symmetric(
        horizontal: WikiTokens.space3,
        vertical: 4,
      ),
      child: Row(
        children: <Widget>[
          _Chip(
            leading: _Dot(color: dot, live: live),
            label: label,
            color: BiuTokens.textSecondary,
          ),
          if (projectName != null) ...<Widget>[
            const SizedBox(width: WikiTokens.space1),
            _Chip(
              leadingIcon: Icons.book_outlined,
              label: projectName,
              color: BiuTokens.textSecondary,
            ),
          ],
          if (projectId.isNotEmpty) ...<Widget>[
            const SizedBox(width: WikiTokens.space1),
            _ActivityChip(projectId: projectId),
          ],
          const Spacer(),
          // ⌘K 提示是键盘专属 affordance，手机形态隐藏（状态栏本身保留）。
          if (projectId.isNotEmpty && !isPhoneLayout(context))
            _CmdHint(projectId: projectId),
        ],
      ),
    );
  }
}

class _ActivityChip extends ConsumerWidget {
  const _ActivityChip({required this.projectId});
  final String projectId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final running = ref.watch(activityFeedCountProvider(projectId));
    final color = running > 0 ? BiuTokens.purple : BiuTokens.textSecondary;
    return _Chip(
      leadingIcon: Icons.bolt,
      label: running > 0 ? '活动 ($running)' : '活动',
      color: color,
      onTap: () => ActivityDrawer.show(context, projectId: projectId),
    );
  }
}

class _Chip extends StatelessWidget {
  const _Chip({
    this.leading,
    this.leadingIcon,
    required this.label,
    required this.color,
    this.onTap,
  });
  final Widget? leading;
  final IconData? leadingIcon;
  final String label;
  final Color color;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final inner = Padding(
      padding: const EdgeInsets.symmetric(
        horizontal: WikiTokens.space2,
        vertical: 2,
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: <Widget>[
          if (leading != null) ...<Widget>[
            leading!,
            const SizedBox(width: 6),
          ] else if (leadingIcon != null) ...<Widget>[
            Icon(leadingIcon, size: 12, color: color),
            const SizedBox(width: 5),
          ],
          ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 240),
            child: Text(
              label,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                color: color,
                fontSize: WikiTokens.fontXs,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
        ],
      ),
    );
    if (onTap == null) return inner;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(4),
      child: inner,
    );
  }
}

class _Dot extends StatelessWidget {
  const _Dot({required this.color, this.live = false});
  final Color color;

  /// true 时用 BiuPulseDot(2.4s 呼吸光圈)— 同步成功时表示连接 alive
  final bool live;

  @override
  Widget build(BuildContext context) {
    if (live) {
      return BiuPulseDot(color: color, size: 8, maxRingRadius: 4);
    }
    return Container(
      width: 8,
      height: 8,
      decoration: BoxDecoration(color: color, shape: BoxShape.circle),
    );
  }
}

class _CmdHint extends StatelessWidget {
  const _CmdHint({required this.projectId});
  final String projectId;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () =>
          WikiCommandPalette.show(context, projectId: projectId),
      borderRadius: BorderRadius.circular(4),
      child: Padding(
        padding: const EdgeInsets.symmetric(
          horizontal: WikiTokens.space2,
          vertical: 2,
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: <Widget>[
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
              decoration: BoxDecoration(
                color: BiuTokens.surfaceMuted,
                borderRadius: BorderRadius.circular(4),
                border: Border.all(color: BiuTokens.borderSubtle),
              ),
              child: Text(
                '⌘K',
                style: TextStyle(
                  color: BiuTokens.textSecondary,
                  fontSize: WikiTokens.fontXs,
                ),
              ),
            ),
            const SizedBox(width: 6),
            Text(
              '命令面板',
              style: TextStyle(
                color: BiuTokens.textMuted,
                fontSize: WikiTokens.fontXs,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
