// MaintainDialog — S3 P0-1 ⑤ wiki autonomous-maintenance agent UI.
//
// UX flow:
//   1. User opens dialog from wiki page header (维护 button).
//   2. Picks intensity (fast/standard/deep = 4/8/12 turns) + model (from the
//      global model-relay catalog) + types an instruction.
//   3. "开始维护" → POST /v1/wiki/projects/{pid}/agent/run (SSE). Dialog
//      flips to running mode.
//   4. Live render: tool-step list (AgentActivity, last row pulses) +
//      streaming markdown body. Events come from the SAME BlockEmitter
//      protocol chat uses (ChatStreamEvent), so decode is shared.
//   5. "停止" → POST .../agent/run/cancel (hubCtx is detached, so closing
//      the stream alone wouldn't stop the loop) + drop the subscription.
//   6. done → final summary; error → message + retry.
//
// Why not poll like ResearchDialog: the agent loop streams; there's no task
// row to GET. We consume SSE directly. Cancellation needs a dedicated
// endpoint because agent_run.go detaches hubCtx from the request ctx.

import 'dart:async';

import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:gpt_markdown/gpt_markdown.dart';
import 'package:uuid/uuid.dart';

import '../../../app/theme.dart';
import '../../../core/ui/adaptive_dialog.dart';
import '../../../core/ui/biu_text_field.dart';
import '../../chat/application/chat_preferences.dart' show chatPreferencesProvider;
import '../../../data/api/_http_helpers.dart' show ApiError;
import '../../../data/api/chat_client.dart';
import '../../../data/api/wiki_agent_client.dart';
import '../../../data/providers_providers.dart' show relayCatalogListProvider;
import '../../../data/wiki_providers.dart';
import '../application/maintain_changes.dart';
import 'maintain_changes_panel.dart';
import 'maintain_runs_panel.dart';

/// 测试 seam：替换 SSE 数据源（widget 测试注入假事件流）。
typedef MaintainAgentRunner = Stream<ChatStreamEvent> Function(
  String projectId, {
  required String runId,
  required String instruction,
  required String model,
  required String mode,
});

/// Opens the wiki maintenance dialog. Void result — the agent may
/// create/update many pages; the caller refreshes the page list itself
/// (we don't auto-navigate).
Future<void> showMaintainDialog(
  BuildContext context, {
  required String projectId,
}) {
  return showAdaptiveDialog<void>(
    context: context,
    barrierDismissible: false,
    builder: (_) => MaintainDialog(projectId: projectId),
  );
}

class MaintainDialog extends ConsumerStatefulWidget {
  const MaintainDialog({
    super.key,
    required this.projectId,
    @visibleForTesting this.agentRunner,
    @visibleForTesting this.audit,
  });
  final String projectId;

  /// 测试注入：替换真实 SSE 流 / 审计后端（revisions + restore + deletePage）。
  final MaintainAgentRunner? agentRunner;
  final MaintainAuditClient? audit;

  @override
  ConsumerState<MaintainDialog> createState() => _MaintainDialogState();
}

enum _Phase { form, running, done, error }

class _ToolStep {
  _ToolStep({required this.blockId, required this.name});
  final String blockId;
  final String name;
  int? durationMs;
  bool completed = false;
  bool failed = false;
}

class _MaintainDialogState extends ConsumerState<MaintainDialog> {
  final _instruction = TextEditingController();
  String _mode = 'standard';
  String? _model;
  _Phase _phase = _Phase.form;

  // blockId → tool step (LinkedHashMap via Dart Map insertion order).
  final Map<String, _ToolStep> _tools = {};
  // blockId → accumulated text deltas (text blocks only; thinking ignored).
  final Map<String, StringBuffer> _textBlocks = {};
  String _summary = '';
  String _err = '';

  String? _runId;
  StreamSubscription<ChatStreamEvent>? _sub;
  WikiAgentClient? _client;

  /// 本 run 写工具改动聚合（BiuMind-Agent-Experience-Design §1.2 P1）。
  MaintainChangeTracker _tracker = MaintainChangeTracker();

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    // Pre-select the user's chat default model once (ref is available here;
    // not in initState). Actual selection validated against the catalog in
    // _buildForm (a retired default is dropped).
    if (_model == null) {
      final dm = ref.read(chatPreferencesProvider).defaultModel;
      if (dm != null && dm.isNotEmpty) _model = dm;
    }
  }

  @override
  void dispose() {
    _instruction.dispose();
    _sub?.cancel();
    super.dispose();
  }

  WikiAgentClient? _ensureClient() {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return null;
    return _client ??= WikiAgentClient(
      repo.client.baseUrl,
      repo.client.bearerToken,
    );
  }

  MaintainAuditClient? _ensureAudit() {
    if (widget.audit != null) return widget.audit;
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return null;
    return WikiMaintainAuditClient(repo.client);
  }

  String _joinedText() =>
      _textBlocks.values.map((b) => b.toString()).join('\n\n').trim();

  Future<void> _start() async {
    final instr = _instruction.text.trim();
    if (instr.isEmpty) return;
    final runner = widget.agentRunner;
    final c = runner == null ? _ensureClient() : null;
    if (runner == null && c == null) {
      setState(() => _err = '请先在「设置」中登录');
      return;
    }
    if (_model == null || _model!.isEmpty) {
      setState(() => _err = '请选择模型');
      return;
    }
    setState(() {
      _phase = _Phase.running;
      _err = '';
      _tools.clear();
      _textBlocks.clear();
      _summary = '';
    });
    _tracker = MaintainChangeTracker();
    _runId = const Uuid().v4();
    final stream = runner != null
        ? runner(
            widget.projectId,
            runId: _runId!,
            instruction: instr,
            model: _model!,
            mode: _mode,
          )
        : c!.runAgentStream(
            widget.projectId,
            runId: _runId!,
            instruction: instr,
            model: _model!,
            mode: _mode,
          );
    _sub = stream.listen(
      _onEvent,
      onError: (Object e) {
        if (!mounted) return;
        setState(() {
          _phase = _Phase.error;
          _err = e.toString();
        });
      },
      onDone: () {
        // Stream closed. If no terminal event flipped the phase, treat it
        // as done (best-effort — server may have shut cleanly without
        // message.done on some error paths; summary falls back to joined text).
        if (!mounted) return;
        if (_phase == _Phase.running) {
          setState(() {
            _phase = _Phase.done;
            _summary = _summary.isEmpty ? _joinedText() : _summary;
          });
        }
      },
      cancelOnError: true,
    );
  }

  void _onEvent(ChatStreamEvent ev) {
    if (!mounted) return;
    switch (ev) {
      case ChatBlockCreate(:final blockId, :final type):
        if (type == 'text') {
          _textBlocks.putIfAbsent(blockId, () => StringBuffer());
        }
      case ChatBlockDelta(:final blockId, :final delta):
        final buf = _textBlocks[blockId];
        if (buf != null) {
          buf.write(delta);
          setState(() {});
        }
      case ChatBlockComplete():
        setState(() {}); // refresh to close any trailing state
      case ChatToolCreated(:final blockId, :final name, :final input):
        _tools[blockId] = _ToolStep(blockId: blockId, name: name);
        // input not rendered in the compact step list; 写工具的 input 进改动
        // 清单（读工具/评论工具被 tracker 忽略）。
        _tracker.onToolCreated(blockId, name, input);
        setState(() {});
      case ChatToolCompleted(:final blockId, :final result, :final durationMs):
        _tracker.onToolCompleted(blockId, result);
        final step = _tools[blockId];
        if (step != null) {
          step.durationMs = durationMs;
          step.completed = true;
          setState(() {});
        }
      case ChatBlockError(:final blockId, :final message):
        if (blockId != null) {
          // tool failure → mark that step failed; loop feeds error back to
          // the model and continues, so this is NOT terminal.
          final step = _tools[blockId];
          if (step != null) {
            step.failed = true;
          }
          setState(() {});
        } else {
          // top-level agent failure → terminal.
          setState(() {
            _phase = _Phase.error;
            _err = message;
          });
          _sub?.cancel();
        }
      case ChatStreamError(:final message):
        setState(() {
          _phase = _Phase.error;
          _err = message;
        });
        _sub?.cancel();
      case ChatMessageDone():
        setState(() {
          _phase = _Phase.done;
          _summary = _joinedText();
        });
        _sub?.cancel();
        // semantic contradiction 扫描由服务端在 agent run 成功后自动触发
        // （brain wiki/api triggerSemanticScan，全 mode 覆盖），客户端不再
        // 补调 /reviews/scan —— 避免双跑，也覆盖客户端崩溃/关窗的场景。
        // 用户仍可手动从审阅队列触发重扫。
      case ChatDone():
        // legacy terminal event (wiki path dual-emits done + message.done).
        setState(() {
          _phase = _Phase.done;
          _summary = _summary.isEmpty ? _joinedText() : _summary;
        });
        _sub?.cancel();
      case ChatUserMessage() ||
            ChatAssistantPlaceholder() ||
            ChatDelta() ||
            ChatStop():
        // Not emitted on the wiki agent path (no thread/placeholder), or
        // legacy duplicates of v2 block events — ignore.
        break;
    }
  }

  Future<void> _cancel() async {
    _sub?.cancel();
    final c = _client;
    final id = _runId;
    if (c != null && id != null) {
      try {
        await c.cancelRun(widget.projectId, id);
      } on ApiError {
        // 404 run_not_found = already finished; nothing to stop.
      } catch (_) {
        // best-effort — network blip shouldn't strand the UI.
      }
    }
    if (!mounted) return;
    setState(() {
      if (_phase == _Phase.running) {
        _phase = _Phase.done;
        _summary = _summary.isEmpty ? _joinedText() : _summary;
      }
    });
  }

  bool _canStart() {
    if (_instruction.text.trim().isEmpty) return false;
    final catalog = ref.read(relayCatalogListProvider).valueOrNull;
    final models = catalog?.where((m) => m.mode == 'chat').toList() ?? [];
    return models.isNotEmpty && _model != null && _model!.isNotEmpty;
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('维护 (agent)'),
      content: SizedBox(
        width: 580,
        child: switch (_phase) {
          _Phase.form => _buildForm(),
          _Phase.running => _buildRunning(),
          _Phase.done || _Phase.error => _buildResult(),
        },
      ),
      actions: _buildActions(),
    );
  }

  Widget _buildForm() {
    final catalog = ref.watch(relayCatalogListProvider);
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          'LLM agent 自主整理当前项目：读取页面/源 → 补全 / 合并 / 修复 → 维护 '
          '[[wikilink]] 反链。所有写操作自动快照到版本历史，可回滚。',
          style: TextStyle(fontSize: 11, color: BiuTokens.textMuted, height: 1.5),
        ),
        const SizedBox(height: BiuTokens.space3),
        BiuTextField(
          controller: _instruction,
          autofocus: true,
          maxLines: 5,
          minLines: 3,
          labelText: '维护指令',
          hintText: '例如：整理这个项目的知识，补全缺失页，合并明显重复',
        ),
        const SizedBox(height: BiuTokens.space3),
        Row(
          crossAxisAlignment: CrossAxisAlignment.center,
          children: [
            Text('强度', style: TextStyle(fontSize: 12, color: BiuTokens.textMuted)),
            const SizedBox(width: BiuTokens.space2),
            DropdownButton<String>(
              value: _mode,
              items: const [
                DropdownMenuItem(value: 'fast', child: Text('快速 (4 轮)')),
                DropdownMenuItem(value: 'standard', child: Text('标准 (8 轮)')),
                DropdownMenuItem(value: 'deep', child: Text('深度 (12 轮)')),
              ],
              onChanged: (v) => setState(() => _mode = v ?? 'standard'),
            ),
            const SizedBox(width: BiuTokens.space3),
            Text('模型', style: TextStyle(fontSize: 12, color: BiuTokens.textMuted)),
            const SizedBox(width: BiuTokens.space2),
            Expanded(child: _buildModelPicker(catalog)),
          ],
        ),
        if (_err.isNotEmpty) ...[
          const SizedBox(height: BiuTokens.space2),
          Text(_err, style: const TextStyle(color: BiuTokens.error, fontSize: 12)),
        ],
      ],
    );
  }

  Widget _buildModelPicker(AsyncValue catalog) {
    return catalog.when(
      loading: () => const SizedBox(
        width: 14,
        height: 14,
        child: CircularProgressIndicator(strokeWidth: 2),
      ),
      error: (e, _) => Text(
        '模型加载失败：$e',
        style: const TextStyle(fontSize: 11, color: BiuTokens.error),
      ),
      data: (all) {
        final models = (all as List)
            .where((m) => (m as dynamic).mode == 'chat')
            .toList();
        if (models.isEmpty) {
          return Text(
            '无可用 chat 模型，请先在设置配置 provider',
            style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
          );
        }
        final codes = models.map((m) => (m as dynamic).code as String).toSet();
        final valid = (_model != null && codes.contains(_model)) ? _model : null;
        return DropdownButton<String>(
          isExpanded: true,
          value: valid,
          hint: const Text('选择模型'),
          items: [
            for (final m in models)
              DropdownMenuItem(
                value: (m as dynamic).code as String,
                child: Text(
                  ((m as dynamic).displayName as String).isEmpty
                      ? (m as dynamic).code as String
                      : (m as dynamic).displayName as String,
                ),
              ),
          ],
          onChanged: (v) => setState(() => _model = v),
        );
      },
    );
  }

  Widget _buildRunning() {
    final text = _joinedText();
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (_tools.isNotEmpty) ...[
          _AgentActivity(steps: _tools.values.toList()),
          const SizedBox(height: BiuTokens.space3),
        ],
        ConstrainedBox(
          constraints: const BoxConstraints(maxHeight: 320),
          child: SingleChildScrollView(
            child: text.isEmpty
                ? Row(
                    children: [
                      const SizedBox(
                        width: 12,
                        height: 12,
                        child: CircularProgressIndicator(strokeWidth: 1.5),
                      ),
                      const SizedBox(width: 8),
                      Text(
                        'agent 思考中…',
                        style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
                      ),
                    ],
                  )
                : GptMarkdown(
                    text,
                    style: TextStyle(
                      color: BiuTokens.text,
                      fontSize: 13,
                      height: 1.6,
                    ),
                  ),
          ),
        ),
      ],
    );
  }

  Widget _buildResult() {
    final changes = _tracker.changes;
    final audit = _ensureAudit();
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (_tools.isNotEmpty) ...[
          _AgentActivity(steps: _tools.values.toList()),
          const SizedBox(height: BiuTokens.space3),
        ],
        if (_phase != _Phase.error &&
            changes.isNotEmpty &&
            audit != null) ...[
          MaintainChangesPanel(
            projectId: widget.projectId,
            changes: changes,
            audit: audit,
            runId: _runId,
          ),
          const SizedBox(height: BiuTokens.space3),
        ],
        ConstrainedBox(
          constraints: const BoxConstraints(maxHeight: 320),
          child: SingleChildScrollView(
            child: _phase == _Phase.error
                ? Text(
                    _err.isEmpty ? '未知错误' : _err,
                    style: const TextStyle(color: BiuTokens.error, fontSize: 12),
                  )
                : (_summary.isEmpty
                    ? Text(
                        '（agent 未输出摘要）',
                        style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
                      )
                    : GptMarkdown(
                        _summary,
                        style: TextStyle(
                          color: BiuTokens.text,
                          fontSize: 13,
                          height: 1.6,
                        ),
                      )),
          ),
        ),
      ],
    );
  }

  List<Widget> _buildActions() {
    return switch (_phase) {
      _Phase.form => [
          // P2 run 持久化：历史 run 回看（改动清单 + 安全 undo）。
          if (_ensureAudit() != null)
            TextButton(
              onPressed: () => showMaintainRunsHistory(
                context,
                projectId: widget.projectId,
                audit: _ensureAudit()!,
              ),
              child: const Text('历史运行'),
            ),
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: _canStart() ? _start : null,
            child: const Text('开始维护'),
          ),
        ],
      _Phase.running => [
          TextButton(
            onPressed: _cancel,
            child: const Text('停止'),
          ),
        ],
      _Phase.done => [
          FilledButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('关闭'),
          ),
        ],
      _Phase.error => [
          TextButton(
            onPressed: () => setState(() => _phase = _Phase.form),
            child: const Text('重试'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('关闭'),
          ),
        ],
    };
  }
}

// ─── widgets ───────────────────────────────────────────────────

class _AgentActivity extends StatelessWidget {
  const _AgentActivity({required this.steps});
  final List<_ToolStep> steps;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(BiuTokens.space2),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            '工具调用 (${steps.length})',
            style: TextStyle(
              fontSize: 10,
              fontWeight: FontWeight.w600,
              color: BiuTokens.textMuted,
            ),
          ),
          const SizedBox(height: 4),
          for (var i = 0; i < steps.length; i++)
            _ToolRow(step: steps[i], isLast: i == steps.length - 1),
        ],
      ),
    );
  }
}

class _ToolRow extends StatelessWidget {
  const _ToolRow({required this.step, required this.isLast});
  final _ToolStep step;
  final bool isLast;

  @override
  Widget build(BuildContext context) {
    final Widget icon;
    if (step.failed) {
      icon = Icon(Icons.error_outline, size: 12, color: BiuTokens.error);
    } else if (step.completed) {
      icon = Icon(Icons.check_circle, size: 12, color: BiuTokens.purple);
    } else if (isLast) {
      icon = const SizedBox(
        width: 12,
        height: 12,
        child: CircularProgressIndicator(strokeWidth: 1.5),
      );
    } else {
      icon = Icon(Icons.circle_outlined, size: 12, color: BiuTokens.textDisabled);
    }
    final label = (step.completed && step.durationMs != null)
        ? '${step.name} · ${step.durationMs}ms'
        : step.name;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        children: [
          icon,
          const SizedBox(width: 6),
          Expanded(
            child: Text(
              label,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                fontSize: 11,
                color: step.failed ? BiuTokens.error : BiuTokens.text,
                fontWeight: (isLast && !step.completed && !step.failed)
                    ? FontWeight.w600
                    : FontWeight.w400,
              ),
            ),
          ),
        ],
      ),
    );
  }
}
