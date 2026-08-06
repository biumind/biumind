// 主区 Agent Tab 的流式事件渲染。
// 把 events 列表按时间序遍历：
//   - 连续 TextDelta 合并成一段文字
//   - ToolUseStart 跟后续同 toolId 的 ToolUseResult 配对成 ToolCallCard
//   - PermissionAsk → 紫色边框 inline 卡片
//   - CostUpdate → 尾部小字
//   - TaskFinished → 完成 chip

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../../chat/markdown/pipeline.dart';
import '../../agent/agent_adapter.dart';
import '../../application/tasks_controller.dart';
import '../../domain/code_task.dart';
import 'tool_call_card.dart';

class AgentStream extends StatefulWidget {
  const AgentStream({super.key, required this.task});
  final CodeTask task;

  @override
  State<AgentStream> createState() => _AgentStreamState();
}

class _AgentStreamState extends State<AgentStream> {
  final _scrollCtrl = ScrollController();
  int _lastEventCount = 0;

  @override
  void didUpdateWidget(covariant AgentStream old) {
    super.didUpdateWidget(old);
    if (widget.task.events.length != _lastEventCount) {
      _lastEventCount = widget.task.events.length;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (_scrollCtrl.hasClients) {
          _scrollCtrl.animateTo(
            _scrollCtrl.position.maxScrollExtent,
            duration: const Duration(milliseconds: 180),
            curve: Curves.easeOut,
          );
        }
      });
    }
  }

  @override
  void dispose() {
    _scrollCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final blocks = _buildBlocks(widget.task);
    return ListView(
      controller: _scrollCtrl,
      padding: const EdgeInsets.fromLTRB(20, 16, 20, 24),
      children: [
        _UserPromptBubble(prompt: widget.task.prompt),
        const SizedBox(height: 12),
        for (final b in blocks) ...[b, const SizedBox(height: 6)],
        if (widget.task.cost.usd > 0) _CostFooter(cost: widget.task.cost),
        if (widget.task.status == CodeTaskStatus.done ||
            widget.task.status == CodeTaskStatus.failed ||
            widget.task.status == CodeTaskStatus.interrupted)
          _StatusFooter(task: widget.task),
      ],
    );
  }

  /// 把事件列表合并成连续 widget block 列表
  List<Widget> _buildBlocks(CodeTask task) {
    final blocks = <Widget>[];
    final pendingTools = <String, ToolUseStart>{};

    final textBuf = StringBuffer();
    void flushText() {
      if (textBuf.isEmpty) return;
      blocks.add(_AssistantText(text: textBuf.toString()));
      textBuf.clear();
    }

    for (final ev in task.events) {
      switch (ev) {
        case TextDelta():
          textBuf.write(ev.text);
        case ToolUseStart():
          flushText();
          pendingTools[ev.toolId] = ev;
          // 占位 card（result=null → loading）
          blocks.add(_ToolCallSlot(toolId: ev.toolId, start: ev, result: null));
        case ToolUseResult():
          flushText();
          // 找到对应的 slot 并替换 — 简化实现：在最后渲染时根据 events 重新组装
          // 这里就 append 一个；下面通过 _coalesce 处理
          blocks.add(_ToolCallSlot(
            toolId: ev.toolId,
            start: pendingTools[ev.toolId],
            result: ev,
          ));
        case PermissionAsk():
          flushText();
          blocks.add(_PermissionCard(ask: ev, task: task));
        case CostUpdate():
        case TaskFinished():
        case AgentStatus():
        case SessionInfo():
          // 这些不在消息体渲染(状态/会话 id 另作他用,详情头或续跑)
          break;
      }
    }
    flushText();

    return _coalesceTools(blocks);
  }

  /// 把同一 toolId 的 (start placeholder + result block) 合并成一个 ToolCallCard
  List<Widget> _coalesceTools(List<Widget> raw) {
    final out = <Widget>[];
    final byTool = <String, ToolCallCard>{};
    for (final w in raw) {
      if (w is _ToolCallSlot) {
        final id = w.toolId;
        if (w.result == null && w.start != null) {
          final card = ToolCallCard(start: w.start!, result: null);
          byTool[id] = card;
          out.add(KeyedSubtree(key: ValueKey('tool_$id'), child: card));
        } else if (w.result != null) {
          // 把已存在的 placeholder 替换；如不存在（事件乱序）就直接 append
          final start = w.start ??
              ToolUseStart(
                ts: w.result!.ts,
                toolId: id,
                name: '?',
                args: const {},
              );
          final newCard = ToolCallCard(start: start, result: w.result);
          final idx = out.indexWhere(
            (x) => x is KeyedSubtree && x.key == ValueKey('tool_$id'),
          );
          if (idx >= 0) {
            out[idx] = KeyedSubtree(key: ValueKey('tool_$id'), child: newCard);
          } else {
            out.add(KeyedSubtree(key: ValueKey('tool_$id'), child: newCard));
          }
          byTool[id] = newCard;
        }
      } else {
        out.add(w);
      }
    }
    return out;
  }
}

// ─── 内部 placeholder（合并前的占位） ───────────────────

class _ToolCallSlot extends StatelessWidget {
  const _ToolCallSlot({
    required this.toolId,
    required this.start,
    required this.result,
  });
  final String toolId;
  final ToolUseStart? start;
  final ToolUseResult? result;
  @override
  Widget build(BuildContext context) => const SizedBox.shrink();
}

// ─── 用户消息气泡 ──────────────────────────────────────

class _UserPromptBubble extends StatelessWidget {
  const _UserPromptBubble({required this.prompt});
  final String prompt;

  @override
  Widget build(BuildContext context) {
    return Align(
      alignment: Alignment.centerRight,
      child: Container(
        constraints: const BoxConstraints(maxWidth: 600),
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color: BiuTokens.purple,
          borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        ),
        child: SelectableText(
          prompt,
          style: const TextStyle(color: Colors.white, fontSize: 13, height: 1.5),
        ),
      ),
    );
  }
}

// ─── Assistant 文本段 ──────────────────────────────────

/// assistant 输出按 markdown 渲染(标题/表格/代码块/列表),复用 chat 的
/// ChatMarkdownView(gpt_markdown 流水线,支持不完整/流式 markdown)——
/// 富文本回放。
class _AssistantText extends StatelessWidget {
  const _AssistantText({required this.text});
  final String text;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: ChatMarkdownView(text: text),
    );
  }
}

// ─── Cost / Status footer ─────────────────────────────

class _CostFooter extends StatelessWidget {
  const _CostFooter({required this.cost});
  final TaskCost cost;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: Text(
        'in ${cost.inputTokens}  ·  out ${cost.outputTokens}  ·  \$${cost.usd.toStringAsFixed(4)}',
        style: TextStyle(
          fontSize: 11,
          fontFamily: 'SF Mono',
          color: BiuTokens.textMuted,
        ),
        textAlign: TextAlign.center,
      ),
    );
  }
}

class _StatusFooter extends StatelessWidget {
  const _StatusFooter({required this.task});
  final CodeTask task;
  @override
  Widget build(BuildContext context) {
    final (label, color, icon) = switch (task.status) {
      CodeTaskStatus.done => (
        'Completed',
        BiuTokens.green,
        Icons.check_circle_outline_rounded
      ),
      CodeTaskStatus.failed => (
        task.errorMessage ?? 'Failed',
        Colors.red,
        Icons.error_outline_rounded
      ),
      CodeTaskStatus.interrupted => (
        'Canceled',
        BiuTokens.textMuted,
        Icons.stop_circle_outlined
      ),
      _ => ('', BiuTokens.textMuted, Icons.info_outline),
    };
    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(icon, size: 14, color: color),
          const SizedBox(width: 6),
          Text(label, style: TextStyle(fontSize: 11.5, color: color)),
        ],
      ),
    );
  }
}

// ─── Permission ask inline card ───────────────────────

class _PermissionCard extends ConsumerWidget {
  const _PermissionCard({required this.ask, required this.task});
  final PermissionAsk ask;
  final CodeTask task;

  Future<void> _decide(
    BuildContext context,
    WidgetRef ref,
    PermissionDecision d,
  ) async {
    final ctl = ref.read(codeTasksProvider.notifier);
    try {
      await ctl.submitPermissionDecision(task, ask.toolId, d);
    } catch (e) {
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('approve failed: $e')),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // codeSync 已废弃 → 无跨设备任务,审批恒在本机直接处理。
    return Container(
      margin: const EdgeInsets.symmetric(vertical: 4),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: Colors.orange.shade50,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        border: Border.all(color: Colors.orange.shade300),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Icon(Icons.priority_high_rounded, size: 16, color: Colors.orange.shade700),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  'Allow ${ask.name}?',
                  style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600),
                ),
              ),
            ],
          ),
          const SizedBox(height: 4),
          Text(
            ask.args.toString(),
            style: TextStyle(
              fontSize: 11.5,
              fontFamily: 'SF Mono',
              color: BiuTokens.textSecondary,
            ),
            maxLines: 3,
            overflow: TextOverflow.ellipsis,
          ),
          const SizedBox(height: 10),
          Row(
            children: [
              FilledButton.tonal(
                onPressed: () => _decide(context, ref, PermissionDecision.allow),
                style: FilledButton.styleFrom(
                  visualDensity: VisualDensity.compact,
                ),
                child: const Text('Allow'),
              ),
              const SizedBox(width: 8),
              OutlinedButton(
                onPressed: () => _decide(context, ref, PermissionDecision.allowOnce),
                style: OutlinedButton.styleFrom(
                  visualDensity: VisualDensity.compact,
                ),
                child: const Text('Allow once'),
              ),
              const SizedBox(width: 8),
              TextButton(
                onPressed: () => _decide(context, ref, PermissionDecision.deny),
                style: TextButton.styleFrom(
                  visualDensity: VisualDensity.compact,
                ),
                child: const Text('Deny'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}
