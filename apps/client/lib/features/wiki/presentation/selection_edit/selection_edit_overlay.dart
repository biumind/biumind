// SelectionEditOverlay — S3 P1-6 inline Edit overlay shown atop the wiki
// editor when the user has a non-empty selection.
//
// Flow (phase A, Edit only):
//   selection non-empty → show instruction TextField + "Edit" button
//   Edit tapped         → POST /selection-edit → diff preview
//   Accept              → EditorBridgeController.replaceSelection (PM
//                         tr.replaceWith) → autosave; overlay closes when
//                         the selection collapses after the replace.
//   Reject              → clear the diff, keep the instruction
//   Regenerate          → re-run Edit with the same instruction
//
// Ask (KB top5 + [1][2] citations) lands in phase B.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:gpt_markdown/gpt_markdown.dart';

import '../../../../app/theme.dart';
import '../../../../core/editor/editor_bridge_controller.dart';
import '../../application/wiki_controller.dart';
import '../../../../data/api/selection_edit_client.dart';
import '../../../../data/wiki_providers.dart';
import 'word_diff_view.dart';

class SelectionEditOverlay extends ConsumerStatefulWidget {
  const SelectionEditOverlay({
    super.key,
    required this.selection,
    required this.controller,
    required this.projectId,
    required this.pageId,
  });

  final EditorSelection selection;
  final EditorBridgeController controller;
  final String projectId;
  final String pageId;

  @override
  ConsumerState<SelectionEditOverlay> createState() =>
      _SelectionEditOverlayState();
}

class _SelectionEditOverlayState extends ConsumerState<SelectionEditOverlay> {
  final _instruction = TextEditingController();
  bool _busy = false;
  String? _replacement;
  String? _err;
  // S3 P1-6 phase B: Ask 模式（edit | ask）。
  String _mode = 'edit';
  String? _askAnswer;
  List<SelectionCitation> _askCitations = const [];

  @override
  void dispose() {
    _instruction.dispose();
    super.dispose();
  }

  SelectionEditClient? _client() {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return null;
    return SelectionEditClient(repo.client.baseUrl, repo.client.bearerToken);
  }

  Future<void> _runEdit() async {
    final instr = _instruction.text.trim();
    if (instr.isEmpty || _busy) return;
    final c = _client();
    if (c == null) {
      setState(() => _err = '请先在「设置」中登录');
      return;
    }
    setState(() {
      _busy = true;
      _err = null;
    });
    try {
      final replacement = await c.edit(
        widget.projectId,
        widget.pageId,
        selection: widget.selection.text,
        before: widget.selection.before,
        after: widget.selection.after,
        instruction: instr,
      );
      if (!mounted) return;
      setState(() {
        _replacement = replacement;
        _busy = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _err = e.toString();
        _busy = false;
      });
    }
  }

  Future<void> _accept() async {
    final replacement = _replacement;
    if (replacement == null) return;
    // replaceSelection 内部 PM tr.replaceWith + TOCTOU 校验。dispatch 触发
    // markdownUpdated → autosave 落库。替换后选区失效 → host 收 selectionChanged
    // (empty) → overlay 关闭。
    await widget.controller.replaceSelection(
      markdown: replacement,
      from: widget.selection.from,
      to: widget.selection.to,
      expectedText: widget.selection.text,
    );
  }

  // ─── Ask (phase B) ──────────────────────────────────────────

  Future<void> _runAsk() async {
    final instr = _instruction.text.trim();
    if (instr.isEmpty || _busy) return;
    final c = _client();
    if (c == null) {
      setState(() => _err = '请先在「设置」中登录');
      return;
    }
    setState(() {
      _busy = true;
      _err = null;
    });
    try {
      final res = await c.ask(
        widget.projectId,
        widget.pageId,
        selection: widget.selection.text,
        before: widget.selection.before,
        after: widget.selection.after,
        instruction: instr,
      );
      if (!mounted) return;
      setState(() {
        _askAnswer = res.answer;
        _askCitations = res.citations;
        _busy = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _err = e.toString();
        _busy = false;
      });
    }
  }

  Widget _buildAsk() {
    if (_askAnswer != null) {
      return Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          ConstrainedBox(
            constraints: const BoxConstraints(maxHeight: 260),
            child: SingleChildScrollView(
              child: GptMarkdown(
                _askAnswer!,
                style: TextStyle(
                  fontSize: 12,
                  height: 1.5,
                  color: BiuTokens.text,
                ),
              ),
            ),
          ),
          if (_askCitations.isNotEmpty) ...[
            const SizedBox(height: 4),
            Wrap(
              spacing: 4,
              runSpacing: 4,
              children: [
                for (final c in _askCitations) _citationChip(c),
              ],
            ),
          ],
          const SizedBox(height: BiuTokens.space2),
          Align(
            alignment: Alignment.centerRight,
            child: TextButton(
              onPressed: _busy
                  ? null
                  : () => setState(() {
                        _askAnswer = null;
                        _askCitations = const [];
                      }),
              child: const Text('再问'),
            ),
          ),
        ],
      );
    }
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            Icon(Icons.help_outline, size: 14, color: BiuTokens.purple),
            const SizedBox(width: 4),
            Text(
              'AI 提问（基于本页知识库）',
              style: TextStyle(
                fontSize: 11,
                fontWeight: FontWeight.w600,
                color: BiuTokens.text,
              ),
            ),
          ],
        ),
        const SizedBox(height: BiuTokens.space2),
        TextField(
          controller: _instruction,
          autofocus: true,
          minLines: 1,
          maxLines: 3,
          style: const TextStyle(fontSize: 12),
          decoration: InputDecoration(
            isDense: true,
            hintText: '例：这段和 X 概念啥关系？',
            hintStyle: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
            border: OutlineInputBorder(
              borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
              borderSide: BorderSide(color: BiuTokens.borderSubtle),
            ),
            contentPadding: const EdgeInsets.symmetric(
              horizontal: BiuTokens.space2,
              vertical: 6,
            ),
          ),
          onSubmitted: (_) => _runAsk(),
        ),
        if (_err != null) ...[
          const SizedBox(height: 4),
          Text(_err!, style: TextStyle(fontSize: 10, color: BiuTokens.error)),
        ],
        const SizedBox(height: BiuTokens.space2),
        Align(
          alignment: Alignment.centerRight,
          child: FilledButton.icon(
            onPressed: _busy ? null : _runAsk,
            icon: _busy
                ? const SizedBox(
                    width: 12,
                    height: 12,
                    child: CircularProgressIndicator(strokeWidth: 1.5),
                  )
                : const Icon(Icons.send, size: 14),
            label: const Text('提问'),
          ),
        ),
      ],
    );
  }

  Widget _citationChip(SelectionCitation c) {
    return InkWell(
      onTap: c.pageId.isEmpty
          ? null
          : () => ref
              .read(wikiControllerProvider.notifier)
              .selectPageById(c.pageId),
      child: Container(
        constraints: const BoxConstraints(maxWidth: 160),
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
        decoration: BoxDecoration(
          color: BiuTokens.purple.withValues(alpha: 0.08),
          borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
          border: Border.all(color: BiuTokens.purple.withValues(alpha: 0.3)),
        ),
        child: Text(
          '[${c.n}] ${c.title}',
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: TextStyle(
            fontSize: 10,
            color: BiuTokens.purple,
            fontWeight: FontWeight.w600,
          ),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Material(
      elevation: 6,
      borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      color: Theme.of(context).colorScheme.surface,
      child: Container(
        width: 360,
        padding: const EdgeInsets.all(BiuTokens.space2),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            SegmentedButton<String>(
              segments: const [
                ButtonSegment(value: 'edit', label: Text('改写')),
                ButtonSegment(value: 'ask', label: Text('提问')),
              ],
              selected: {_mode},
              onSelectionChanged: (s) => setState(() {
                _mode = s.first;
                _err = null;
              }),
            ),
            const SizedBox(height: BiuTokens.space2),
            switch (_mode) {
              'ask' => _buildAsk(),
              _ => _replacement == null
                  ? _buildComposer()
                  : _buildDiff(_replacement!),
            },
          ],
        ),
      ),
    );
  }

  Widget _buildComposer() {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Row(
          children: [
            Icon(Icons.edit_note, size: 14, color: BiuTokens.purple),
            const SizedBox(width: 4),
            Text(
              'AI 改写选区',
              style: TextStyle(
                fontSize: 11,
                fontWeight: FontWeight.w600,
                color: BiuTokens.text,
              ),
            ),
          ],
        ),
        const SizedBox(height: BiuTokens.space2),
        TextField(
          controller: _instruction,
          autofocus: true,
          minLines: 1,
          maxLines: 3,
          style: const TextStyle(fontSize: 12),
          decoration: InputDecoration(
            isDense: true,
            hintText: '例：更简洁 / 翻译成英文 / 加示例',
            hintStyle: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
            border: OutlineInputBorder(
              borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
              borderSide: BorderSide(color: BiuTokens.borderSubtle),
            ),
            contentPadding: const EdgeInsets.symmetric(
              horizontal: BiuTokens.space2,
              vertical: 6,
            ),
          ),
          onSubmitted: (_) => _runEdit(),
        ),
        if (_err != null) ...[
          const SizedBox(height: 4),
          Text(
            _err!,
            style: TextStyle(fontSize: 10, color: BiuTokens.error),
          ),
        ],
        const SizedBox(height: BiuTokens.space2),
        Align(
          alignment: Alignment.centerRight,
          child: FilledButton.icon(
            onPressed: _busy ? null : _runEdit,
            icon: _busy
                ? const SizedBox(
                    width: 12,
                    height: 12,
                    child: CircularProgressIndicator(strokeWidth: 1.5),
                  )
                : const Icon(Icons.auto_fix_high, size: 14),
            label: const Text('改写'),
          ),
        ),
      ],
    );
  }

  Widget _buildDiff(String replacement) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          '预览改动（绿=新增 红=删除）',
          style: TextStyle(
            fontSize: 10,
            fontWeight: FontWeight.w600,
            color: BiuTokens.textMuted,
          ),
        ),
        const SizedBox(height: 4),
        ConstrainedBox(
          constraints: const BoxConstraints(maxHeight: 240),
          child: SingleChildScrollView(
            child: WordDiffView(
              before: widget.selection.text,
              after: replacement,
            ),
          ),
        ),
        const SizedBox(height: BiuTokens.space2),
        Row(
          mainAxisAlignment: MainAxisAlignment.end,
          children: [
            TextButton(
              onPressed: _busy
                  ? null
                  : () => setState(() {
                        _replacement = null;
                        _err = null;
                      }),
              child: const Text('取消'),
            ),
            const SizedBox(width: BiuTokens.space2),
            TextButton(
              onPressed: _busy ? null : _runEdit,
              child: const Text('重生成'),
            ),
            const SizedBox(width: BiuTokens.space2),
            FilledButton(
              onPressed: _accept,
              child: const Text('接受'),
            ),
          ],
        ),
      ],
    );
  }
}
