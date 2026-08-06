// CodeProjectPage — 编码模块根页(M1)。
//
// 布局:左 ProjectRail(48px)+ 右主体。无激活项目 → WelcomePage;有项目 →
// 现有 CodeWorkbenchPage(任务面板已按 projectScopedCodeTasksProvider 过滤)。
//
// 路由 /code 指向本页(替代直接挂 CodeWorkbenchPage)。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../application/projects_controller.dart';
import 'code_workbench_page.dart';
import 'project_rail.dart';
import 'timeline_view.dart';
import 'welcome_page.dart';

/// 无激活项目时的落地区视图:项目欢迎页 / 跨项目时间线。
enum _NoProjectTab { welcome, timeline }

final _noProjectTabProvider =
    StateProvider<_NoProjectTab>((_) => _NoProjectTab.welcome);

class CodeProjectPage extends ConsumerWidget {
  const CodeProjectPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final activeProject = ref.watch(activeCodeProjectProvider);

    return Row(
      children: [
        const ProjectRail(),
        const VerticalDivider(width: 1, thickness: 1),
        Expanded(
          child: activeProject == null
              ? const _NoProjectArea()
              // key 绑 project id —— 切项目时重建工作台状态(tab/split 等),
              // 避免上个项目的 UI 态串到下一个。M1 简单可靠;keep-alive 优化留后。
              : CodeWorkbenchPage(key: ValueKey(activeProject.id)),
        ),
      ],
    );
  }
}

/// 无项目时:顶部 [项目 | 时间线] 切换 + 对应视图。
class _NoProjectArea extends ConsumerWidget {
  const _NoProjectArea();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final tab = ref.watch(_noProjectTabProvider);
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
          child: Align(
            alignment: Alignment.centerLeft,
            child: SegmentedButton<_NoProjectTab>(
              segments: const [
                ButtonSegment(
                    value: _NoProjectTab.welcome,
                    icon: Icon(Icons.grid_view_rounded),
                    label: Text('项目')),
                ButtonSegment(
                    value: _NoProjectTab.timeline,
                    icon: Icon(Icons.timeline_rounded),
                    label: Text('时间线')),
              ],
              selected: {tab},
              onSelectionChanged: (s) =>
                  ref.read(_noProjectTabProvider.notifier).state = s.first,
            ),
          ),
        ),
        Expanded(
          child: tab == _NoProjectTab.welcome
              ? const CodeWelcomePage()
              : const CodeTimelineView(),
        ),
      ],
    );
  }
}
