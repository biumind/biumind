// 任务详情 sheet — 我的作品 / 画廊 tap 卡片时弹出.
//
// 显示完整 prompt + 模型 + 参数 + 输出图; 提供「做同款」/「公开/私有」/
// 「删除」按钮. 公开画廊 (非自己作品) 只显示「做同款」.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme.dart';
import '../../../../l10n/app_localizations.dart';
import '../../application/aigc_providers.dart';
import '../../application/generation_form_controller.dart';
import '../../application/tasks_controller.dart';
import '../../domain/ai_model.dart';
import '../../domain/creation_task.dart';
import '../widgets/hotparse_result_view.dart';
import '../widgets/output_thumbnail.dart';

Future<void> showTaskDetailSheet(
  BuildContext context,
  WidgetRef ref,
  CreationTask task, {
  bool ownedByCurrentUser = true,
}) {
  return showModalBottomSheet(
    context: context,
    isScrollControlled: true,
    backgroundColor: BiuTokens.surface,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(BiuTokens.radiusLg)),
    ),
    builder: (_) => DraggableScrollableSheet(
      initialChildSize: 0.7,
      maxChildSize: 0.95,
      minChildSize: 0.4,
      expand: false,
      builder: (_, scrollCtrl) => _DetailContent(
        task: task,
        owned: ownedByCurrentUser,
        scrollController: scrollCtrl,
      ),
    ),
  );
}

class _DetailContent extends ConsumerWidget {
  const _DetailContent({
    required this.task,
    required this.owned,
    required this.scrollController,
  });

  final CreationTask task;
  final bool owned;
  final ScrollController scrollController;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final t = AppLocalizations.of(context)!;
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 8, 20, 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Center(
            child: Container(
              width: 36,
              height: 4,
              decoration: BoxDecoration(
                color: BiuTokens.border,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
          const SizedBox(height: 12),
          Expanded(
            child: ListView(
              controller: scrollController,
              children: [
                if (task.type == 'hotparse') ...[
                  // 爆款解析: 渲染拆解结果 (文案/钩子/分镜/标签/转写) + 逐分镜「生成同款」。
                  HotparseResultView(
                    task: task,
                    onMakeSimilar: (p) => _makeSimilar(context, ref, task, [p]),
                    onMakeAll: (ps) => _makeSimilar(context, ref, task, ps),
                  ),
                  const SizedBox(height: 8),
                ] else ...[
                  if (task.outputs.isNotEmpty)
                    ClipRRect(
                      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
                      child: AspectRatio(
                        aspectRatio: 1,
                        child: OutputThumbnail(output: task.outputs.first),
                      ),
                    ),
                  if (task.outputs.length > 1) ...[
                    const SizedBox(height: 8),
                    SizedBox(
                      height: 64,
                      child: ListView.separated(
                        scrollDirection: Axis.horizontal,
                        itemCount: task.outputs.length,
                        separatorBuilder: (_, i) => const SizedBox(width: 6),
                        itemBuilder: (_, i) => ClipRRect(
                          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
                          child: SizedBox(
                            width: 64,
                            height: 64,
                            child: OutputThumbnail(output: task.outputs[i]),
                          ),
                        ),
                      ),
                    ),
                  ],
                  const SizedBox(height: 16),
                  _Section(title: 'Prompt', body: task.prompt),
                  if (task.negativePrompt != null && task.negativePrompt!.isNotEmpty)
                    _Section(title: 'Negative', body: task.negativePrompt!),
                ],
                const SizedBox(height: 8),
                _MetaRow(label: '模型', value: task.modelCode),
                _MetaRow(label: '类型', value: task.type),
                if (task.params.isNotEmpty)
                  _MetaRow(
                    label: '参数',
                    value: task.params.entries
                        .map((e) => '${e.key}=${e.value}')
                        .join(' · '),
                  ),
                if (task.costCredits > 0)
                  _MetaRow(label: '消耗', value: '${task.costCredits} 积分'),
                if (task.refundedCredits > 0)
                  _MetaRow(
                      label: '退款',
                      value: '${task.refundedCredits} 积分',
                      tone: BiuTokens.green),
              ],
            ),
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              Expanded(
                child: FilledButton.icon(
                  icon: const Icon(Icons.auto_awesome, size: 16),
                  label: Text(t.creationActionMakeSimilar),
                  onPressed: () {
                    ref.read(generationFormControllerProvider.notifier).syncFromTask(
                          type: GenerationType.fromWire(task.type),
                          modelCode: task.modelCode,
                          prompt: task.prompt,
                          negativePrompt: task.negativePrompt ?? '',
                          params: task.params,
                          isPublic: task.isPublic,
                        );
                    Navigator.of(context).pop();
                    context.go('/creation/center');
                  },
                ),
              ),
              if (owned) ...[
                const SizedBox(width: 8),
                IconButton(
                  onPressed: () async {
                    await ref
                        .read(tasksControllerProvider.notifier)
                        .setVisibility(task.id, !task.isPublic);
                    if (context.mounted) Navigator.of(context).pop();
                  },
                  icon: Icon(task.isPublic ? Icons.public : Icons.lock_outline),
                  tooltip: task.isPublic
                      ? t.creationActionPrivate
                      : t.creationActionPublic,
                ),
                IconButton(
                  onPressed: () async {
                    await ref.read(tasksControllerProvider.notifier).delete(task.id);
                    if (context.mounted) Navigator.of(context).pop();
                  },
                  icon: Icon(Icons.delete_outline, color: BiuTokens.error),
                  tooltip: t.creationActionDelete,
                ),
              ],
            ],
          ),
        ],
      ),
    );
  }
}

/// 「(一键)同款」: 把分镜 prompt 直接发起 image 生成, 带 parent_sha 血缘
/// (lineage_op=hotparse_remix), 让爆款拆解 → 同款素材进同一条 DAG。
/// 无可用图像模型时回退到「预填第一个分镜 + 跳创作台让用户选模型」。
Future<void> _makeSimilar(
  BuildContext context,
  WidgetRef ref,
  CreationTask task,
  List<String> prompts,
) async {
  if (prompts.isEmpty) return;
  final raw = await ref.read(aigcModelsProvider('image').future);
  final models = raw.whereType<AiModel>().toList();

  // 回退: 无图像模型 → 预填表单让用户在创作台选模型再生成。
  if (models.isEmpty) {
    ref.read(generationFormControllerProvider.notifier).syncFromTask(
          type: GenerationType.image,
          modelCode: '',
          prompt: prompts.first,
        );
    if (context.mounted) {
      Navigator.of(context).pop();
      context.go('/creation/center');
    }
    return;
  }

  final modelCode = models.first.code;
  final parentSha = _hotparseSha(task);
  final tasks = ref.read(tasksControllerProvider.notifier);
  for (final p in prompts) {
    await tasks.submit(
      type: 'image',
      modelCode: modelCode,
      prompt: p,
      params: const {},
      parentSha: parentSha,
      lineageOp: 'hotparse_remix',
    );
  }
  if (context.mounted) {
    Navigator.of(context).pop();
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text('已发起 ${prompts.length} 个同款生成'),
        duration: const Duration(seconds: 2),
      ),
    );
  }
}

/// 取爆款任务的拆解结果 output sha (作为同款生成的血缘 parent_sha)。
String? _hotparseSha(CreationTask task) {
  for (final o in task.outputs) {
    if (o.kind == 'hotparse' && o.sha256.isNotEmpty) return o.sha256;
  }
  return null;
}

class _Section extends StatelessWidget {
  const _Section({required this.title, required this.body});
  final String title;
  final String body;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            title,
            style: TextStyle(
              fontSize: 11,
              fontWeight: FontWeight.w600,
              letterSpacing: 0.5,
              color: BiuTokens.textMuted,
            ),
          ),
          const SizedBox(height: 4),
          SelectableText(
            body,
            style: TextStyle(
              fontSize: 13,
              color: BiuTokens.text,
              height: 1.5,
            ),
          ),
        ],
      ),
    );
  }
}

class _MetaRow extends StatelessWidget {
  const _MetaRow({required this.label, required this.value, this.tone});
  final String label;
  final String value;
  final Color? tone;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 64,
            child: Text(
              label,
              style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
            ),
          ),
          Expanded(
            child: Text(
              value,
              style: TextStyle(
                fontSize: 12,
                color: tone ?? BiuTokens.text,
              ),
            ),
          ),
        ],
      ),
    );
  }
}
