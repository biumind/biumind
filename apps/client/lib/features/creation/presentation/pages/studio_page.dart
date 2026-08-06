// 创作工作台 (Studio) — 居中放 GenerationPanel + 下方「我的最近任务」strip.
// 与 inspiration_page 区别: 这页无 Hero, 无公开瀑布流, 专注创作.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme.dart';
import '../../application/tasks_controller.dart';
import '../widgets/creation_card.dart';
import '../widgets/generation_panel.dart';
import 'task_detail_sheet.dart';

class StudioPage extends ConsumerWidget {
  const StudioPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(tasksControllerProvider);
    final recent = state.sortedDesc().take(8).toList();

    return Container(
      color: BiuTokens.bg,
      child: SingleChildScrollView(
        padding: const EdgeInsets.all(24),
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 720),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                const GenerationPanel(),
                if (recent.isNotEmpty) ...[
                  const SizedBox(height: 24),
                  Row(
                    children: [
                      Text(
                        '最近创作',
                        style: TextStyle(
                          fontSize: 14,
                          fontWeight: FontWeight.w600,
                          color: BiuTokens.text,
                        ),
                      ),
                      const Spacer(),
                      TextButton.icon(
                        onPressed: () => context.go('/creation/works'),
                        icon: const Icon(Icons.arrow_forward, size: 14),
                        label: const Text('全部'),
                      ),
                    ],
                  ),
                  const SizedBox(height: 8),
                  SizedBox(
                    height: 140,
                    child: ListView.separated(
                      scrollDirection: Axis.horizontal,
                      itemCount: recent.length,
                      separatorBuilder: (_, i) => const SizedBox(width: 8),
                      itemBuilder: (_, i) => SizedBox(
                        width: 140,
                        child: CreationCard(
                          task: recent[i],
                          onTap: () => showTaskDetailSheet(context, ref, recent[i]),
                        ),
                      ),
                    ),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}
