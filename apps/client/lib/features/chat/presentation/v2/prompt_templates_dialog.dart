// PromptTemplatesDialogV2 —— system prompt 模板管理。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 模板）。
//
// 行为：
//   * Header + ListView 列已存模板；空状态提示
//   * 每行：name + content 摘要 + "应用 / 编辑 / 删除" action
//   * "应用" 仅当传入 onApply 时显示（即调用方提供了当前 thread 上下文）
//   * "新建" 触发 _editTemplateDialog（name + content 多行输入）

import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/ui/adaptive_dialog.dart';
import '../../../../l10n/app_localizations.dart';
import '../../application/prompt_template_store.dart';

Future<void> showPromptTemplatesDialog(
  BuildContext context, {

  /// 非空 → 列表里出"应用"按钮，传入选中模板的 content。
  /// null → 仅管理不应用。
  void Function(PromptTemplate t)? onApply,
}) {
  return showAdaptiveDialog<void>(
    context: context,
    builder: (_) => _PromptTemplatesDialog(onApply: onApply),
  );
}

class _PromptTemplatesDialog extends ConsumerWidget {
  const _PromptTemplatesDialog({this.onApply});
  final void Function(PromptTemplate t)? onApply;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final templates = ref.watch(promptTemplatesProvider);
    return AdaptiveDialogFrame(
      maxWidth: 720,
      maxHeight: 600,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
            decoration: BoxDecoration(
              border: Border(
                bottom: BorderSide(color: theme.colorScheme.outlineVariant),
              ),
            ),
            child: Row(
              children: [
                Icon(
                  Icons.bookmark_outline,
                  size: 18,
                  color: theme.colorScheme.onSurfaceVariant,
                ),
                const SizedBox(width: 8),
                Text(
                  l.chatV2TemplatesTitle,
                  style: theme.textTheme.titleMedium?.copyWith(
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const Spacer(),
                TextButton.icon(
                  icon: const Icon(Icons.add, size: 16),
                  label: Text(l.chatV2TemplatesNew),
                  onPressed: () => _showEditDialog(context, ref),
                ),
                IconButton(
                  icon: const Icon(Icons.close, size: 18),
                  tooltip: l.chatV2ArchivedClose,
                  onPressed: () => Navigator.of(context).pop(),
                ),
              ],
            ),
          ),
          Expanded(
            child: templates.isEmpty
                ? Center(
                    child: Padding(
                      padding: const EdgeInsets.all(32),
                      child: Text(
                        l.chatV2TemplatesEmpty,
                        textAlign: TextAlign.center,
                        style: theme.textTheme.bodyMedium?.copyWith(
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                      ),
                    ),
                  )
                : ListView.separated(
                    padding: const EdgeInsets.symmetric(vertical: 6),
                    itemCount: templates.length,
                    separatorBuilder: (_, _) => Divider(
                      height: 1,
                      color: theme.colorScheme.outlineVariant.withValues(
                        alpha: 0.5,
                      ),
                    ),
                    itemBuilder: (_, i) =>
                        _TemplateRow(template: templates[i], onApply: onApply),
                  ),
          ),
        ],
      ),
    );
  }
}

class _TemplateRow extends ConsumerWidget {
  const _TemplateRow({required this.template, this.onApply});
  final PromptTemplate template;
  final void Function(PromptTemplate t)? onApply;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  template.name,
                  style: theme.textTheme.bodyMedium?.copyWith(
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  template.content,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: theme.textTheme.bodySmall?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
              ],
            ),
          ),
          if (onApply != null)
            TextButton.icon(
              icon: const Icon(Icons.check, size: 14),
              label: Text(l.chatV2TemplatesApply),
              style: TextButton.styleFrom(visualDensity: VisualDensity.compact),
              onPressed: () {
                Navigator.of(context).pop();
                onApply!(template);
              },
            ),
          IconButton(
            icon: const Icon(Icons.edit_outlined, size: 16),
            tooltip: l.chatV2TemplatesEdit,
            visualDensity: VisualDensity.compact,
            onPressed: () => _showEditDialog(context, ref, existing: template),
          ),
          IconButton(
            icon: const Icon(Icons.delete_outline, size: 16),
            color: theme.colorScheme.error,
            tooltip: l.chatV2TemplatesDelete,
            visualDensity: VisualDensity.compact,
            onPressed: () => _confirmDelete(context, ref, template),
          ),
        ],
      ),
    );
  }
}

Future<void> _confirmDelete(
  BuildContext context,
  WidgetRef ref,
  PromptTemplate t,
) async {
  final l = AppLocalizations.of(context)!;
  final ok = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text(l.chatV2TemplatesDeleteTitle),
      content: Text(l.chatV2TemplatesDeleteBody(t.name)),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(false),
          child: Text(l.chatV2DialogCancel),
        ),
        TextButton(
          style: TextButton.styleFrom(
            foregroundColor: Theme.of(ctx).colorScheme.error,
          ),
          onPressed: () => Navigator.of(ctx).pop(true),
          child: Text(l.chatV2DialogDelete),
        ),
      ],
    ),
  );
  if (ok != true) return;
  await ref.read(promptTemplatesProvider.notifier).remove(t.id);
}

Future<void> _showEditDialog(
  BuildContext context,
  WidgetRef ref, {
  PromptTemplate? existing,
}) async {
  final l = AppLocalizations.of(context)!;
  final nameCtrl = TextEditingController(text: existing?.name ?? '');
  final contentCtrl = TextEditingController(text: existing?.content ?? '');
  final saved = await showDialog<bool>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text(
        existing == null
            ? l.chatV2TemplatesEditDialogNew
            : l.chatV2TemplatesEditDialogEdit,
      ),
      content: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 480),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            TextField(
              controller: nameCtrl,
              autofocus: true,
              decoration: InputDecoration(
                labelText: l.chatV2TemplatesNameLabel,
                hintText: l.chatV2TemplatesNameHint,
              ),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: contentCtrl,
              minLines: 6,
              maxLines: 14,
              decoration: InputDecoration(
                labelText: l.chatV2TemplatesContentLabel,
                hintText: l.chatV2TemplatesContentHint,
                border: const OutlineInputBorder(),
              ),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(false),
          child: Text(l.chatV2DialogCancel),
        ),
        FilledButton(
          onPressed: () => Navigator.of(ctx).pop(true),
          child: Text(l.chatV2DialogSave),
        ),
      ],
    ),
  );
  final name = nameCtrl.text.trim();
  final content = contentCtrl.text.trim();
  nameCtrl.dispose();
  contentCtrl.dispose();
  if (saved != true || name.isEmpty || content.isEmpty) return;
  final notifier = ref.read(promptTemplatesProvider.notifier);
  if (existing == null) {
    await notifier.create(name: name, content: content);
  } else {
    await notifier.update(existing.id, name: name, content: content);
  }
}
