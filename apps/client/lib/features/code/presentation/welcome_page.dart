// WelcomePage — 无激活项目时的编码模块落地页(M1)。
//
// 提供「添加项目」入口 + 最近打开列表(点选切换)。空态引导首次添加。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../application/projects_controller.dart';
import '../domain/project.dart';
import 'project_rail.dart' show avatarColorFor, pickAndAddProject;

class CodeWelcomePage extends ConsumerWidget {
  const CodeWelcomePage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final projects = ref.watch(railProjectsProvider);
    final theme = Theme.of(context);

    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 480),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Icon(Icons.terminal_rounded,
                size: 56, color: theme.colorScheme.primary),
            const SizedBox(height: 16),
            Text('BiuMind Code',
                textAlign: TextAlign.center,
                style: theme.textTheme.headlineSmall),
            const SizedBox(height: 8),
            Text(
              '选择一个代码仓库开始 —— 派 AI 工程师并发干活,你做 PM 审批。',
              textAlign: TextAlign.center,
              style: theme.textTheme.bodyMedium
                  ?.copyWith(color: theme.colorScheme.onSurfaceVariant),
            ),
            const SizedBox(height: 24),
            FilledButton.icon(
              onPressed: () => pickAndAddProject(ref),
              icon: const Icon(Icons.create_new_folder_outlined),
              label: const Text('添加项目'),
            ),
            if (projects.isNotEmpty) ...[
              const SizedBox(height: 32),
              Align(
                alignment: Alignment.centerLeft,
                child: Text('最近',
                    style: theme.textTheme.labelLarge
                        ?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
              ),
              const SizedBox(height: 8),
              ...projects.take(8).map((p) => _RecentTile(project: p)),
            ],
          ],
        ),
      ),
    );
  }
}

class _RecentTile extends ConsumerWidget {
  const _RecentTile({required this.project});
  final CodeProject project;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 8),
      leading: CircleAvatar(
        radius: 16,
        backgroundColor: avatarColorFor(project),
        child: Text(
          project.name.isEmpty ? '?' : project.name.characters.first.toUpperCase(),
          style: const TextStyle(color: Colors.white, fontSize: 13),
        ),
      ),
      title: Text(project.name, maxLines: 1, overflow: TextOverflow.ellipsis),
      subtitle: Text(project.path,
          maxLines: 1, overflow: TextOverflow.ellipsis),
      onTap: () {
        ref.read(activeCodeProjectIdProvider.notifier).state = project.id;
        ref.read(codeProjectsControllerProvider.notifier).touch(project.id);
      },
    );
  }
}
