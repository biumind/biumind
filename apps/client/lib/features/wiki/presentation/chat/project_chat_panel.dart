// ProjectChatPanel —— Wiki 项目工作区内嵌的对话面板。
//
// V2 重构后只是 ThreadsShellPage 的包装：传 projectId 让 sidebar 按
// project_id 过滤 thread；title 显项目名。所有底层流程（创建 / 发送 /
// 渲染）走 V2 ChatPageV2 + brain Agent Plane，跟全局 /chat 同源。
//
// 两处挂载：
//   - ProjectBrowserPage 无选中页面时的 detail（显式传 projectName）；
//   - /wiki/p/:pid/chat 路由直挂（projectName 不传，从 wikiController
//     按 projectId 反查，查不到交给 ThreadsShellPage 的 "对话" 兜底）。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../chat/presentation/v2/threads_shell_page.dart';
import '../../application/wiki_controller.dart';

class ProjectChatPanel extends ConsumerWidget {
  const ProjectChatPanel({
    super.key,
    required this.projectId,
    this.projectName,
  });

  final String projectId;

  /// sidebar header 标题用的项目名。null（路由直挂场景）时从
  /// wikiController 反查；查不到则 ThreadsShellPage 自己兜底 "对话"。
  final String? projectName;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    var title = projectName;
    if (title == null) {
      final state = ref.watch(wikiControllerProvider).valueOrNull;
      if (state != null) {
        for (final p in state.projects) {
          if (p.id == projectId) {
            title = p.name;
            break;
          }
        }
      }
    }
    return ThreadsShellPage(
      projectId: projectId,
      title: title,
    );
  }
}
