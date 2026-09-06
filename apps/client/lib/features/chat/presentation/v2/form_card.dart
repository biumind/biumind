// FormCard —— chat 模式 agent 提问表单（elicitation, mode=form;
// agent-ask-form P1-b）在消息流底部浮起的 inline 表单卡。
//
// 数据流（与 ApprovalCardV2 同款）:
//   brain chat 模式引擎 AskUserQuestion
//   → ChatRunner 发 control_request{elicitation, requested_schema}
//   → BiuSessionConnection 收帧 emit ElicitationRequested event
//   → ChatController 投到 pendingElicitationsProvider
//   → 本 widget watch 该 provider 渲染卡片
//   → 用户提交 / 跳过 / 取消
//   → req.respond(action, content) 沿 WS 发 SDKControlResponse 回 brain
//   → resolve(threadId, requestId, ...) 把卡片锁定为已答展示
//
// 渲染位置:ApprovalCardV2 之下、composer 之上(chat_page_v2)。
//
// 一期边界（设计 §2.3）:卡片不落库,读回历史看不到表单;mode=url 不支持
// (BiuSessionConnection 收到即回 decline);不应答时服务端 5min 超时兜底。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../l10n/app_localizations.dart';
import '../../application/chat_controller.dart';

/// 从 requested_schema 解出的表单描述。优先读 `x-biumind-question` 展示
/// 元数据（带 option 描述）;缺失时降级从 JSON Schema 主体抠
/// title / properties.answer.enum（label 没有描述）。
class FormSpec {
  final String question;
  final String header;
  final bool multiSelect;
  final List<({String label, String description})> options;

  const FormSpec({
    required this.question,
    required this.header,
    required this.multiSelect,
    required this.options,
  });

  /// 无 options → 自由文本框（schema 驱动表单的通用形态;AskUserQuestion
  /// 工具恒有 2-4 个 options,这条路是给未来的纯文本提问留的）。
  bool get freeTextOnly => options.isEmpty;

  static FormSpec? parse(Map<String, dynamic> schema, String fallbackMessage) {
    final ext = schema['x-biumind-question'];
    if (ext is Map) {
      final question = (ext['question'] as String?) ?? fallbackMessage;
      final rawOptions = ext['options'];
      final options = <({String label, String description})>[];
      if (rawOptions is List) {
        for (final o in rawOptions) {
          if (o is Map && o['label'] is String) {
            options.add((
              label: o['label'] as String,
              description: (o['description'] as String?) ?? '',
            ));
          }
        }
      }
      return FormSpec(
        question: question,
        header: (ext['header'] as String?) ?? '',
        multiSelect: ext['multi_select'] == true,
        options: options,
      );
    }
    // 降级:纯 JSON Schema 形状。
    final question = (schema['title'] as String?) ?? fallbackMessage;
    if (question.isEmpty) return null;
    final props = schema['properties'];
    final answer = (props is Map) ? props['answer'] : null;
    if (answer is! Map) return null;
    final multi = answer['type'] == 'array';
    final enumSrc = multi ? answer['items'] : answer;
    final labels = <({String label, String description})>[];
    if (enumSrc is Map && enumSrc['enum'] is List) {
      for (final v in enumSrc['enum'] as List) {
        if (v is String) labels.add((label: v, description: ''));
      }
    }
    return FormSpec(
      question: question,
      header: '',
      multiSelect: multi,
      options: labels,
    );
  }
}

class FormCard extends ConsumerWidget {
  const FormCard({super.key, required this.threadId});

  final String threadId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final items = ref.watch(pendingElicitationsProvider).forThread(threadId);
    if (items.isEmpty) return const SizedBox.shrink();
    // 同时只渲染最早一条;引擎逐题串行触发,队列通常长度=1。
    final item = items.first;
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 4),
      child: item.answered
          ? _AnsweredCard(item: item)
          : _PendingCard(threadId: threadId, item: item),
    );
  }
}

/// 已答锁定卡 —— 只读展示用户的选择 / 跳过 / 取消。
class _AnsweredCard extends StatelessWidget {
  const _AnsweredCard({required this.item});

  final ElicitationItem item;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final statusText = switch (item.answeredAction) {
      'accept' => l.chatV2FormAnswered(item.answerSummary ?? ''),
      'decline' => l.chatV2FormSkipped,
      _ => l.chatV2FormCancelled,
    };
    return Material(
      color: theme.colorScheme.surface,
      borderRadius: BorderRadius.circular(10),
      child: Container(
        width: double.infinity,
        padding: const EdgeInsets.fromLTRB(14, 10, 14, 10),
        decoration: BoxDecoration(
          border: Border.all(
            color: theme.colorScheme.outlineVariant,
            width: 1,
          ),
          borderRadius: BorderRadius.circular(10),
        ),
        child: Row(
          children: [
            Icon(Icons.check_circle_outline,
                size: 16, color: theme.colorScheme.onSurfaceVariant),
            const SizedBox(width: 6),
            Expanded(
              child: Text(
                statusText,
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _PendingCard extends ConsumerStatefulWidget {
  const _PendingCard({required this.threadId, required this.item});

  final String threadId;
  final ElicitationItem item;

  @override
  ConsumerState<_PendingCard> createState() => _PendingCardState();
}

class _PendingCardState extends ConsumerState<_PendingCard> {
  final Set<String> _selected = {};
  final TextEditingController _textController = TextEditingController();

  @override
  void dispose() {
    _textController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final req = widget.item.request;
    final spec = FormSpec.parse(req.schema, req.message);
    // schema 解析不动 → 只给跳过/取消,不渲染表单本体(防误导性提交;
    // decline 一样让服务端 soft error 出局)。
    final canSubmit = spec != null &&
        (spec.freeTextOnly
            ? _textController.text.trim().isNotEmpty
            : _selected.isNotEmpty);

    return Material(
      color: theme.colorScheme.surface,
      elevation: 2,
      borderRadius: BorderRadius.circular(10),
      child: Container(
        padding: const EdgeInsets.fromLTRB(14, 12, 14, 12),
        decoration: BoxDecoration(
          border: Border.all(
            color: theme.colorScheme.primary.withValues(alpha: 0.3),
            width: 1.2,
          ),
          borderRadius: BorderRadius.circular(10),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Row(
              children: [
                Icon(Icons.quiz_outlined,
                    size: 16, color: theme.colorScheme.primary),
                const SizedBox(width: 6),
                if (spec != null && spec.header.isNotEmpty)
                  Container(
                    padding:
                        const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                    decoration: BoxDecoration(
                      color: theme.colorScheme.primaryContainer,
                      borderRadius: BorderRadius.circular(6),
                    ),
                    child: Text(
                      spec.header,
                      style: theme.textTheme.labelSmall?.copyWith(
                        color: theme.colorScheme.onPrimaryContainer,
                      ),
                    ),
                  ),
                if (spec != null && spec.multiSelect) ...[
                  const SizedBox(width: 6),
                  Text(
                    l.chatV2FormMultiHint,
                    style: theme.textTheme.labelSmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                  ),
                ],
              ],
            ),
            const SizedBox(height: 6),
            Text(
              spec?.question ?? req.message,
              style: theme.textTheme.bodyMedium?.copyWith(
                fontWeight: FontWeight.w600,
              ),
            ),
            if (spec != null && !spec.freeTextOnly) ...[
              const SizedBox(height: 8),
              Wrap(
                spacing: 8,
                runSpacing: 6,
                children: [
                  for (final opt in spec.options)
                    spec.multiSelect
                        ? FilterChip(
                            label: Text(opt.label),
                            tooltip: opt.description,
                            selected: _selected.contains(opt.label),
                            onSelected: (on) => setState(() {
                              on
                                  ? _selected.add(opt.label)
                                  : _selected.remove(opt.label);
                            }),
                          )
                        : ChoiceChip(
                            label: Text(opt.label),
                            tooltip: opt.description,
                            selected: _selected.contains(opt.label),
                            onSelected: (on) {
                              // 单选语义:忽略取消选中,新选替换旧选。
                              if (!on) return;
                              setState(() {
                                _selected
                                  ..clear()
                                  ..add(opt.label);
                              });
                            },
                          ),
                ],
              ),
            ],
            if (spec != null && spec.freeTextOnly) ...[
              const SizedBox(height: 8),
              TextField(
                controller: _textController,
                minLines: 1,
                maxLines: 3,
                decoration: InputDecoration(
                  hintText: l.chatV2FormTextHint,
                  isDense: true,
                  border: const OutlineInputBorder(),
                ),
                onChanged: (_) => setState(() {}),
              ),
            ],
            const SizedBox(height: 10),
            Row(
              children: [
                // 取消 — 用户连默认都不想让 agent 用。
                TextButton(
                  onPressed: () => _resolve('cancel'),
                  style: TextButton.styleFrom(
                    foregroundColor: theme.colorScheme.onSurfaceVariant,
                    visualDensity: VisualDensity.compact,
                  ),
                  child: Text(l.commonCancel),
                ),
                const SizedBox(width: 8),
                // 跳过 — agent 收到 decline,自己挑默认继续。
                TextButton(
                  onPressed: () => _resolve('decline'),
                  style: TextButton.styleFrom(
                    visualDensity: VisualDensity.compact,
                  ),
                  child: Text(l.chatV2FormSkip),
                ),
                const Spacer(),
                FilledButton.icon(
                  onPressed:
                      canSubmit ? () => _submit(spec) : null,
                  icon: const Icon(Icons.check, size: 14),
                  label: Text(l.chatV2FormSubmit),
                  style: FilledButton.styleFrom(
                    visualDensity: VisualDensity.compact,
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  void _submit(FormSpec? spec) {
    if (spec == null) return;
    final Map<String, dynamic> content;
    final String summary;
    if (spec.freeTextOnly) {
      final text = _textController.text.trim();
      if (text.isEmpty) return;
      content = {'answer': text};
      summary = text;
    } else if (spec.multiSelect) {
      if (_selected.isEmpty) return;
      content = {'answer': _selected.toList()};
      summary = _selected.join('、');
    } else {
      if (_selected.isEmpty) return;
      content = {'answer': _selected.first};
      summary = _selected.first;
    }
    widget.item.request.respond('accept', content);
    ref.read(pendingElicitationsProvider.notifier).resolve(
          widget.threadId,
          widget.item.request.requestId,
          action: 'accept',
          summary: summary,
        );
  }

  void _resolve(String action) {
    widget.item.request.respond(action);
    ref.read(pendingElicitationsProvider.notifier).resolve(
          widget.threadId,
          widget.item.request.requestId,
          action: action,
        );
  }
}
