// ThreadSettingsSheet —— Thread 元信息 + system prompt 编辑底部 sheet。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 模型参数 sheet）。
//
// v1 暴露：
//   * system prompt 多行编辑（带"清空"按钮）
//   * 只读元信息：模式 / 模型 / 创建时间 / 更新时间
//
// 后续可扩 temperature / top_p / max_tokens（需要 schema 加列 + brain 端透传）。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../l10n/app_localizations.dart';
import '../../application/chat_controller.dart';
import '../../domain/chat_models.dart';
import '../../domain/greeting.dart';
import 'prompt_templates_dialog.dart';

Future<void> showThreadSettingsSheet(
  BuildContext context, {
  required String threadId,
}) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    showDragHandle: true,
    builder: (_) => _ThreadSettingsSheet(threadId: threadId),
  );
}

class _ThreadSettingsSheet extends ConsumerStatefulWidget {
  const _ThreadSettingsSheet({required this.threadId});
  final String threadId;

  @override
  ConsumerState<_ThreadSettingsSheet> createState() =>
      _ThreadSettingsSheetState();
}

class _ThreadSettingsSheetState
    extends ConsumerState<_ThreadSettingsSheet> {
  final _ctrl = TextEditingController();
  Thread? _thread;
  bool _loading = true;
  bool _dirty = false;
  bool _saving = false;

  @override
  void initState() {
    super.initState();
    _load();
    _ctrl.addListener(() {
      final v = _ctrl.text;
      final original = _thread?.systemPrompt ?? '';
      final next = v != original;
      if (next != _dirty) setState(() => _dirty = next);
    });
  }

  Future<void> _load() async {
    final repo = ref.read(chatControllerDepsProvider).repo;
    final t = await repo.getThread(widget.threadId);
    if (!mounted) return;
    setState(() {
      _thread = t;
      _ctrl.text = t?.systemPrompt ?? '';
      _loading = false;
    });
  }

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    setState(() => _saving = true);
    try {
      final repo = ref.read(chatControllerDepsProvider).repo;
      final v = _ctrl.text.trim();
      await repo.setSystemPrompt(widget.threadId, v.isEmpty ? null : v);
      if (mounted) Navigator.of(context).pop();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(
            content: Text(
                AppLocalizations.of(context)!.chatV2SettingsSheetSaveFailed('$e'))));
      }
    } finally {
      if (mounted) setState(() => _saving = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final t = _thread;
    return Padding(
      padding: EdgeInsets.fromLTRB(
        24, 0, 24, MediaQuery.of(context).viewInsets.bottom + 24,
      ),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxHeight: 600),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Text(
                  l.chatV2SettingsSheetTitle,
                  style: theme.textTheme.titleMedium?.copyWith(
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const Spacer(),
                if (_dirty && !_saving)
                  FilledButton.icon(
                    onPressed: _save,
                    icon: const Icon(Icons.check, size: 16),
                    label: Text(l.chatV2DialogSave),
                  )
                else if (_saving)
                  const SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  ),
              ],
            ),
            const SizedBox(height: 16),
            if (_loading)
              const Padding(
                padding: EdgeInsets.symmetric(vertical: 32),
                child: Center(child: CircularProgressIndicator()),
              )
            else if (t == null)
              Text(
                l.chatV2SettingsSheetNotFound,
                style: theme.textTheme.bodyMedium?.copyWith(
                  color: theme.colorScheme.error,
                ),
              )
            else
              Flexible(
                child: SingleChildScrollView(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      _MetaRow(
                          label: l.chatV2SettingsSheetMode,
                          value: _modeLabel(t.mode)),
                      _MetaRow(
                          label: l.chatV2SettingsSheetModel,
                          value: t.model ?? l.chatV2SettingsSheetModelDefault),
                      _MetaRow(
                          label: l.chatV2SettingsSheetCreated,
                          value: relativeTime(t.createdAt)),
                      _MetaRow(
                          label: l.chatV2SettingsSheetUpdated,
                          value: relativeTime(t.updatedAt)),
                      const SizedBox(height: 16),
                      Row(
                        children: [
                          Text(
                            'System Prompt',
                            style: theme.textTheme.titleSmall?.copyWith(
                              fontWeight: FontWeight.w600,
                            ),
                          ),
                          const Spacer(),
                          TextButton.icon(
                            onPressed: () => showPromptTemplatesDialog(
                              context,
                              onApply: (tpl) {
                                _ctrl.text = tpl.content;
                              },
                            ),
                            icon: const Icon(Icons.bookmark_outline,
                                size: 14),
                            label: Text(l.chatV2SettingsSheetFromTemplate),
                            style: TextButton.styleFrom(
                              visualDensity: VisualDensity.compact,
                              padding: const EdgeInsets.symmetric(
                                  horizontal: 8),
                            ),
                          ),
                          if (_ctrl.text.isNotEmpty)
                            TextButton.icon(
                              onPressed: () => _ctrl.clear(),
                              icon:
                                  const Icon(Icons.clear, size: 14),
                              label: Text(l.chatV2SettingsSheetClear),
                              style: TextButton.styleFrom(
                                visualDensity: VisualDensity.compact,
                                padding: const EdgeInsets.symmetric(
                                    horizontal: 8),
                              ),
                            ),
                        ],
                      ),
                      const SizedBox(height: 6),
                      Text(
                        l.chatV2SettingsSheetHint,
                        style: theme.textTheme.labelSmall?.copyWith(
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                      ),
                      const SizedBox(height: 8),
                      TextField(
                        controller: _ctrl,
                        minLines: 5,
                        maxLines: 14,
                        decoration: InputDecoration(
                          hintText: l.chatV2SettingsSheetPromptHint,
                          border: OutlineInputBorder(
                            borderRadius: BorderRadius.circular(8),
                          ),
                          contentPadding:
                              const EdgeInsets.symmetric(
                                  horizontal: 12, vertical: 10),
                          isDense: true,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }

  static String _modeLabel(ThreadMode m) => switch (m) {
        ThreadMode.chat => 'Chat',
        ThreadMode.agent => 'Agent',
        ThreadMode.task => 'Task',
      };
}

class _MetaRow extends StatelessWidget {
  const _MetaRow({required this.label, required this.value});
  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        children: [
          SizedBox(
            width: 60,
            child: Text(
              label,
              style: theme.textTheme.labelSmall?.copyWith(
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
          ),
          Expanded(
            child: Text(
              value,
              style: theme.textTheme.bodyMedium,
            ),
          ),
        ],
      ),
    );
  }
}
