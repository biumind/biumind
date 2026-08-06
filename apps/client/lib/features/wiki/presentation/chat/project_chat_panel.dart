// ProjectChatPanel —— Wiki 项目工作区内嵌的对话面板。
//
// V2 重构后只是 ThreadsShellPage 的包装：传 projectId 让 sidebar 按
// project_id 过滤 thread；title 显项目名。所有底层流程（创建 / 发送 /
// 渲染）走 V2 ChatPageV2 + brain Agent Plane，跟全局 /chat 同源。

import 'package:flutter/material.dart';

import '../../../chat/presentation/v2/threads_shell_page.dart';

class ProjectChatPanel extends StatelessWidget {
  const ProjectChatPanel({
    super.key,
    required this.projectId,
    required this.projectName,
  });

  final String projectId;
  final String projectName;

  @override
  Widget build(BuildContext context) {
    return ThreadsShellPage(
      projectId: projectId,
      title: projectName,
    );
  }
}
