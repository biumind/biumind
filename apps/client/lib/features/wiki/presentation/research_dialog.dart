// ResearchDialog — kicks off a deep-research task and tracks progress.
//
// UX flow:
//   1. User opens dialog from wiki page header.
//   2. Types topic (required) + optional refined queries (one per line).
//   3. Click "Run Research" → POST /research, dialog flips into status mode.
//   4. Polls GET /research/{id} every 2s, updating the displayed phase
//      (searching → synthesizing → saving → done | error).
//   5. On done: shows "Open page" button → caller can navigate.
//   6. On error: shows error message + "Close".

import 'dart:async';

import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/ui/adaptive_dialog.dart';
import '../../../core/ui/biu_text_field.dart';
import '../../../data/api/research_client.dart';
import '../../../data/wiki_providers.dart';

/// Opens the research dialog. Returns the resulting page id (when the
/// task succeeded and the user chose to open it), or null otherwise.
Future<String?> showResearchDialog(
  BuildContext context, {
  required String projectId,
}) {
  // barrierDismissible:false —— 防误触中断研究轮询; 宽屏透传 showDialog,
  // 手机映射 sheet 的 isDismissible/enableDrag (同样不可滑关/点遮罩关)。
  return showAdaptiveDialog<String?>(
    context: context,
    barrierDismissible: false,
    builder: (_) => ResearchDialog(projectId: projectId),
  );
}

class ResearchDialog extends ConsumerStatefulWidget {
  const ResearchDialog({super.key, required this.projectId});
  final String projectId;

  @override
  ConsumerState<ResearchDialog> createState() => _ResearchDialogState();
}

class _ResearchDialogState extends ConsumerState<ResearchDialog> {
  final _topic = TextEditingController();
  final _queries = TextEditingController();

  ResearchTask? _task;
  Timer? _poll;
  String? _err;
  bool _starting = false;

  @override
  void dispose() {
    _topic.dispose();
    _queries.dispose();
    _poll?.cancel();
    super.dispose();
  }

  ResearchClient? _client() {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return null;
    // Reuse the wiki client's baseUrl + bearer; ResearchClient and
    // WikiClient hit the same brain origin.
    return ResearchClient(repo.client.baseUrl, repo.client.bearerToken);
  }

  Future<void> _start() async {
    final topic = _topic.text.trim();
    if (topic.isEmpty) return;
    final queries = _queries.text
        .split('\n')
        .map((s) => s.trim())
        .where((s) => s.isNotEmpty)
        .toList(growable: false);

    final c = _client();
    if (c == null) {
      setState(() => _err = '请先在「设置」中登录');
      return;
    }
    setState(() {
      _starting = true;
      _err = null;
    });
    try {
      final t = await c.startTask(
        widget.projectId,
        topic: topic,
        queries: queries,
      );
      if (!mounted) return;
      setState(() {
        _task = t;
        _starting = false;
      });
      _startPolling();
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _err = e.toString();
        _starting = false;
      });
    }
  }

  void _startPolling() {
    _poll?.cancel();
    _poll = Timer.periodic(const Duration(seconds: 2), (_) async {
      final c = _client();
      final id = _task?.id;
      if (c == null || id == null) return;
      try {
        final t = await c.getTask(widget.projectId, id);
        if (!mounted) return;
        setState(() => _task = t);
        if (!t.isRunning) {
          _poll?.cancel();
        }
      } catch (e) {
        if (!mounted) return;
        setState(() => _err = e.toString());
        _poll?.cancel();
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final t = _task;
    return AlertDialog(
      title: const Text('Deep Research'),
      content: SizedBox(
        width: 560,
        child: t == null ? _buildForm() : _buildStatus(t),
      ),
      actions: t == null
          ? [
              TextButton(
                onPressed: _starting
                    ? null
                    : () => Navigator.of(context).pop(null),
                child: const Text('取消'),
              ),
              FilledButton(
                onPressed: _starting ? null : _start,
                child: _starting
                    ? const SizedBox(
                        width: 14,
                        height: 14,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('开始研究'),
              ),
            ]
          : _statusActions(t),
    );
  }

  Widget _buildForm() {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          '输入研究话题, LLM 会先 web 搜索, 再合成一个带 [[wikilink]] '
          '交叉引用的 markdown 页面落到当前项目.',
          style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
        ),
        const SizedBox(height: BiuTokens.space3),
        BiuTextField(
          controller: _topic,
          autofocus: true,
          labelText: '话题',
          hintText: '例如：Mixture-of-Experts in 2025',
        ),
        const SizedBox(height: BiuTokens.space3),
        BiuTextField(
          controller: _queries,
          maxLines: 4,
          minLines: 2,
          labelText: '细化查询 (可选, 每行一个)',
          hintText: 'MoE routing efficiency\nMoE inference cost\n…',
        ),
        if (_err != null) ...[
          const SizedBox(height: BiuTokens.space2),
          Text(_err!, style: const TextStyle(color: BiuTokens.error)),
        ],
      ],
    );
  }

  Widget _buildStatus(ResearchTask t) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            Text(
              '话题：',
              style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
            ),
            Expanded(
              child: Text(
                t.topic,
                style: const TextStyle(
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                ),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ],
        ),
        const SizedBox(height: BiuTokens.space3),
        _PhaseRow(status: t.status),
        const SizedBox(height: BiuTokens.space3),
        if (t.webResults.isNotEmpty) _ResultList(hits: t.webResults),
        if (t.synthesis.isNotEmpty) ...[
          const SizedBox(height: BiuTokens.space3),
          _SynthesisPreview(text: t.synthesis),
        ],
        if (t.isError) ...[
          const SizedBox(height: BiuTokens.space2),
          Text(
            '错误：${t.error ?? "未知错误"}',
            style: const TextStyle(color: BiuTokens.error),
          ),
        ],
      ],
    );
  }

  List<Widget> _statusActions(ResearchTask t) {
    if (t.isDone && t.pageId != null) {
      return [
        TextButton(
          onPressed: () => Navigator.of(context).pop(null),
          child: const Text('关闭'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(context).pop(t.pageId),
          child: const Text('打开页面'),
        ),
      ];
    }
    return [
      TextButton(
        onPressed: () => Navigator.of(context).pop(null),
        child: Text(t.isRunning ? '后台运行' : '关闭'),
      ),
    ];
  }
}

// ── widgets ───────────────────────────────────────────────────

class _PhaseRow extends StatelessWidget {
  const _PhaseRow({required this.status});
  final String status;

  static const _phases = [
    ('queued', '排队'),
    ('searching', 'Web 搜索'),
    ('synthesizing', 'LLM 合成'),
    ('saving', '保存页面'),
    ('done', '完成'),
  ];

  int _currentIndex() {
    final idx = _phases.indexWhere((p) => p.$1 == status);
    return idx < 0 ? 0 : idx;
  }

  @override
  Widget build(BuildContext context) {
    final cur = _currentIndex();
    final isError = status == 'error';
    return Row(
      children: [
        for (var i = 0; i < _phases.length; i++) ...[
          if (i > 0)
            Expanded(
              child: Container(
                height: 2,
                color: i <= cur && !isError
                    ? BiuTokens.purple
                    : BiuTokens.borderSubtle,
              ),
            ),
          Container(
            padding: const EdgeInsets.symmetric(
              horizontal: BiuTokens.space2,
              vertical: 4,
            ),
            decoration: BoxDecoration(
              color: isError && i == cur
                  ? BiuTokens.errorSoft
                  : (i <= cur ? BiuTokens.purpleSoft : BiuTokens.surfaceMuted),
              borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
            ),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                if (i == cur && !isError && status != 'done')
                  const SizedBox(
                    width: 10,
                    height: 10,
                    child: CircularProgressIndicator(strokeWidth: 1.5),
                  )
                else if (i < cur || status == 'done')
                  Icon(Icons.check_circle, size: 12, color: BiuTokens.purple)
                else if (i == cur && isError)
                  const Icon(
                    Icons.error_outline,
                    size: 12,
                    color: BiuTokens.error,
                  )
                else
                  Container(
                    width: 8,
                    height: 8,
                    decoration: BoxDecoration(
                      color: BiuTokens.textDisabled,
                      shape: BoxShape.circle,
                    ),
                  ),
                const SizedBox(width: 4),
                Text(
                  _phases[i].$2,
                  style: TextStyle(
                    fontSize: 10,
                    fontWeight: i == cur ? FontWeight.w700 : FontWeight.w500,
                    color: isError && i == cur
                        ? BiuTokens.error
                        : (i <= cur ? BiuTokens.purple : BiuTokens.textMuted),
                  ),
                ),
              ],
            ),
          ),
        ],
      ],
    );
  }
}

class _ResultList extends StatelessWidget {
  const _ResultList({required this.hits});
  final List<ResearchHit> hits;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'Web 结果 (${hits.length})',
            style: TextStyle(
              fontSize: 11,
              fontWeight: FontWeight.w600,
              color: BiuTokens.textMuted,
            ),
          ),
          const SizedBox(height: 4),
          for (final h in hits.take(5))
            Padding(
              padding: const EdgeInsets.only(bottom: 2),
              child: Text(
                '· ${h.title}',
                style: TextStyle(fontSize: 11, color: BiuTokens.text),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
            ),
          if (hits.length > 5)
            Text(
              '… 还有 ${hits.length - 5} 条',
              style: TextStyle(fontSize: 10, color: BiuTokens.textMuted),
            ),
        ],
      ),
    );
  }
}

class _SynthesisPreview extends StatelessWidget {
  const _SynthesisPreview({required this.text});
  final String text;

  @override
  Widget build(BuildContext context) {
    final preview = text.length > 600
        ? '${text.substring(0, 600).trim()}…'
        : text;
    return Container(
      padding: const EdgeInsets.all(BiuTokens.space3),
      constraints: const BoxConstraints(maxHeight: 200),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: SingleChildScrollView(
        child: Text(
          preview,
          style: TextStyle(fontSize: 11, color: BiuTokens.text, height: 1.5),
        ),
      ),
    );
  }
}
