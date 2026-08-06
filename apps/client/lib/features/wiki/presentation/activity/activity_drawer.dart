/// Right-side overlay drawer for the Activity Feed.
///
/// 通过 `showGeneralDialog` 弹出，slide-from-right 过渡。Esc 关闭。
///
/// 布局（320px 宽，全高）：
///   ┌─────────────────────────┐
///   │ Activity              ⟳ × │
///   ├─────────────────────────┤
///   │ Running (n)             │
///   │   [TaskCard]            │
///   ├─────────────────────────┤
///   │ Recent (m)              │
///   │   [CollapsedRow] (<2s)  │
///   │   [TaskCard]    (>2s/失败) │
///   └─────────────────────────┘
library;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import 'activity_collapsed_row.dart';
import 'activity_provider.dart';
import 'activity_state.dart';
import 'activity_task_card.dart';

class ActivityDrawer {
  ActivityDrawer._();

  static Future<void> show(BuildContext context, {required String projectId}) {
    return showGeneralDialog<void>(
      context: context,
      barrierLabel: 'Activity feed',
      barrierDismissible: true,
      barrierColor: Colors.black.withValues(alpha: 0.30),
      transitionDuration: const Duration(milliseconds: 200),
      pageBuilder: (ctx, _, _) => _DrawerSurface(projectId: projectId),
      transitionBuilder: (ctx, anim, _, child) {
        final offset = Tween<Offset>(
          begin: const Offset(1, 0),
          end: Offset.zero,
        ).animate(CurvedAnimation(parent: anim, curve: Curves.easeOutCubic));
        return SlideTransition(position: offset, child: child);
      },
    );
  }
}

class _DrawerSurface extends ConsumerStatefulWidget {
  const _DrawerSurface({required this.projectId});
  final String projectId;

  @override
  ConsumerState<_DrawerSurface> createState() => _DrawerSurfaceState();
}

class _DrawerSurfaceState extends ConsumerState<_DrawerSurface> {
  @override
  void initState() {
    super.initState();
    // 抽屉打开时主动 refresh 一次：长时间没看的话首页缓存可能过期。
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      ref
          .read(activityFeedProvider(widget.projectId).notifier)
          .refresh();
    });
  }

  @override
  Widget build(BuildContext context) {
    final running = ref.watch(activityFeedRunningProvider(widget.projectId));
    final recent = ref.watch(activityFeedRecentProvider(widget.projectId));
    final loading = ref
        .watch(activityFeedProvider(widget.projectId))
        .maybeWhen(loading: () => true, orElse: () => false);

    return CallbackShortcuts(
      bindings: <ShortcutActivator, VoidCallback>{
        const SingleActivator(LogicalKeyboardKey.escape): () =>
            Navigator.of(context).maybePop(),
      },
      child: Focus(
        autofocus: true,
        child: Align(
          alignment: Alignment.centerRight,
          child: Material(
            color: BiuTokens.surface,
            elevation: 8,
            child: SizedBox(
              width: 320,
              height: double.infinity,
              child: SafeArea(
                child: Column(
                  children: <Widget>[
                    _Header(
                      loading: loading,
                      onRefresh: () => ref
                          .read(activityFeedProvider(widget.projectId)
                              .notifier)
                          .refresh(),
                      onClose: () => Navigator.of(context).maybePop(),
                    ),
                    Expanded(
                      child: ListView(
                        padding: const EdgeInsets.symmetric(vertical: 8),
                        children: <Widget>[
                          _SectionTitle(label: '进行中', count: running.length),
                          if (running.isEmpty)
                            const _EmptyHint(text: '暂无进行中的任务'),
                          for (final task in running)
                            ActivityTaskCard(
                              key: ValueKey('run-${task.id}'),
                              task: task,
                              projectId: widget.projectId,
                            ),
                          if (recent.isNotEmpty) ...<Widget>[
                            const SizedBox(height: 8),
                            _SectionTitle(label: '最近', count: recent.length),
                            for (final task in recent) _recentRow(task),
                          ],
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  /// 终态短任务收成单行；失败任务永远走完整卡片。
  Widget _recentRow(ActivityTask task) {
    if (task.status == ActivityStatus.failed) {
      return ActivityTaskCard(
        key: ValueKey('rec-${task.id}'),
        task: task,
        projectId: widget.projectId,
      );
    }
    if (task.isCollapsedRecent) {
      return ActivityCollapsedRow(
        key: ValueKey('rec-${task.id}'),
        task: task,
      );
    }
    return ActivityTaskCard(
      key: ValueKey('rec-${task.id}'),
      task: task,
      projectId: widget.projectId,
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({
    required this.loading,
    required this.onRefresh,
    required this.onClose,
  });
  final bool loading;
  final VoidCallback onRefresh;
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 44,
      padding: const EdgeInsets.symmetric(horizontal: 12),
      decoration: BoxDecoration(
        border: Border(
          bottom: BorderSide(color: BiuTokens.borderSubtle),
        ),
      ),
      child: Row(
        children: <Widget>[
          Text(
            '活动',
            style: TextStyle(
              color: BiuTokens.text,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const Spacer(),
          if (loading)
            const SizedBox(
              width: 14,
              height: 14,
              child: CircularProgressIndicator(strokeWidth: 1.5),
            )
          else
            IconButton(
              icon: const Icon(Icons.refresh, size: 16),
              color: BiuTokens.textMuted,
              tooltip: '刷新',
              onPressed: onRefresh,
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
            ),
          IconButton(
            icon: const Icon(Icons.close, size: 18),
            color: BiuTokens.textMuted,
            tooltip: '关闭',
            onPressed: onClose,
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
          ),
        ],
      ),
    );
  }
}

class _SectionTitle extends StatelessWidget {
  const _SectionTitle({required this.label, required this.count});
  final String label;
  final int count;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 4),
      child: Text(
        '$label ($count)',
        style: TextStyle(
          color: BiuTokens.textMuted,
          fontSize: 11,
          fontWeight: FontWeight.w500,
          letterSpacing: 0.5,
        ),
      ),
    );
  }
}

class _EmptyHint extends StatelessWidget {
  const _EmptyHint({required this.text});
  final String text;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      child: Text(
        text,
        style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
      ),
    );
  }
}
