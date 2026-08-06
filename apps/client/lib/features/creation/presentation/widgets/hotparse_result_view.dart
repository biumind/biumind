// HotparseResultView — 爆款解析结果展示.
//
// 渲染 kind=="hotparse" output 的 metadata(worker LLM 拆解结果):
//   { copywriting, hooks[], scenes[]{index,description,prompt,duration_hint_s},
//     tags[], transcript }
//
// 每个分镜带「生成同款」按钮 → onMakeSimilar(scene.prompt):由调用方把该 prompt
// 喂回 image/video 生成链(复用 generation_form syncFromTask + 跳创作台)。

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../../../app/theme.dart';
import '../../domain/creation_task.dart';

class HotparseResultView extends StatelessWidget {
  const HotparseResultView({
    super.key,
    required this.task,
    required this.onMakeSimilar,
    this.onMakeAll,
  });

  final CreationTask task;

  /// 点某个分镜「生成同款」时回调,入参是该分镜可直接生成的 prompt。
  final void Function(String scenePrompt) onMakeSimilar;

  /// 「一键全部同款」回调,入参是所有分镜的 prompt 列表。null 时隐藏批量按钮。
  final void Function(List<String> scenePrompts)? onMakeAll;

  Map<String, dynamic>? get _meta {
    for (final o in task.outputs) {
      if (o.kind == 'hotparse' && o.metadata != null) return o.metadata;
    }
    return null;
  }

  @override
  Widget build(BuildContext context) {
    final m = _meta;
    if (m == null) {
      // 还在解析中 / 失败 / 旧数据无 metadata。
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 12),
        child: Text(
          task.status == TaskStatus.failed
              ? '解析失败:${task.errorMessage ?? task.errorCode ?? "未知错误"}'
              : '正在解析视频…',
          style: TextStyle(fontSize: 13, color: BiuTokens.textSecondary),
        ),
      );
    }

    final copywriting = (m['copywriting'] as String?)?.trim() ?? '';
    final hooks = _strList(m['hooks']);
    final scenes = (m['scenes'] as List?) ?? const [];
    final tags = _strList(m['tags']);
    final transcript = (m['transcript'] as String?)?.trim() ?? '';

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (copywriting.isNotEmpty) ...[
          _Header(title: '文案', onCopy: () => _copy(context, copywriting)),
          _Card(child: SelectableText(copywriting, style: _bodyStyle)),
          const SizedBox(height: 14),
        ],
        if (hooks.isNotEmpty) ...[
          const _Header(title: '钩子'),
          ...hooks.map((h) => _Bullet(text: h)),
          const SizedBox(height: 14),
        ],
        if (scenes.isNotEmpty) ...[
          _ScenesHeader(
            count: scenes.length,
            onMakeAll: onMakeAll == null
                ? null
                : () => onMakeAll!(_scenePrompts(scenes)),
          ),
          for (var i = 0; i < scenes.length; i++)
            if (scenes[i] is Map<String, dynamic>)
              _SceneTile(
                scene: scenes[i] as Map<String, dynamic>,
                fallbackIndex: i + 1,
                onMakeSimilar: onMakeSimilar,
              ),
          const SizedBox(height: 14),
        ],
        if (tags.isNotEmpty) ...[
          const _Header(title: '标签'),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: tags
                .map((t) => Chip(
                      label: Text('#$t', style: const TextStyle(fontSize: 12)),
                      visualDensity: VisualDensity.compact,
                      materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                      backgroundColor: BiuTokens.surfaceMuted,
                      side: BorderSide(color: BiuTokens.borderSubtle),
                    ))
                .toList(),
          ),
          const SizedBox(height: 14),
        ],
        if (transcript.isNotEmpty)
          _TranscriptSection(transcript: transcript, onCopy: () => _copy(context, transcript)),
      ],
    );
  }

  static List<String> _scenePrompts(List<dynamic> scenes) => scenes
      .whereType<Map<String, dynamic>>()
      .map((s) => (s['prompt'] as String?)?.trim() ?? '')
      .where((p) => p.isNotEmpty)
      .toList();

  static List<String> _strList(dynamic v) =>
      (v is List) ? v.map((e) => e.toString().trim()).where((s) => s.isNotEmpty).toList() : <String>[];

  static void _copy(BuildContext context, String text) {
    Clipboard.setData(ClipboardData(text: text));
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('已复制'), duration: Duration(seconds: 1)),
    );
  }
}

const _bodyStyle = TextStyle(fontSize: 13, height: 1.5);

class _Header extends StatelessWidget {
  const _Header({required this.title, this.onCopy});
  final String title;
  final VoidCallback? onCopy;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 6),
      child: Row(
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
          if (onCopy != null) ...[
            const Spacer(),
            InkWell(
              onTap: onCopy,
              child: Icon(Icons.copy_rounded, size: 14, color: BiuTokens.textMuted),
            ),
          ],
        ],
      ),
    );
  }
}

class _ScenesHeader extends StatelessWidget {
  const _ScenesHeader({required this.count, this.onMakeAll});
  final int count;
  final VoidCallback? onMakeAll;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 6),
      child: Row(
        children: [
          Text(
            '分镜 ($count)',
            style: TextStyle(
              fontSize: 11,
              fontWeight: FontWeight.w600,
              letterSpacing: 0.5,
              color: BiuTokens.textMuted,
            ),
          ),
          const Spacer(),
          if (onMakeAll != null)
            TextButton.icon(
              onPressed: onMakeAll,
              icon: const Icon(Icons.auto_awesome_motion, size: 14),
              label: const Text('一键全部同款', style: TextStyle(fontSize: 12)),
              style: TextButton.styleFrom(
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 0),
                minimumSize: const Size(0, 28),
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
            ),
        ],
      ),
    );
  }
}

class _Card extends StatelessWidget {
  const _Card({required this.child});
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: child,
    );
  }
}

class _Bullet extends StatelessWidget {
  const _Bullet({required this.text});
  final String text;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(top: 6, right: 6),
            child: Container(
              width: 4,
              height: 4,
              decoration: BoxDecoration(
                color: BiuTokens.purple,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
          Expanded(child: SelectableText(text, style: _bodyStyle)),
        ],
      ),
    );
  }
}

class _SceneTile extends StatelessWidget {
  const _SceneTile({
    required this.scene,
    required this.fallbackIndex,
    required this.onMakeSimilar,
  });
  final Map<String, dynamic> scene;
  final int fallbackIndex;
  final void Function(String) onMakeSimilar;

  @override
  Widget build(BuildContext context) {
    final index = (scene['index'] as num?)?.toInt() ?? fallbackIndex;
    final desc = (scene['description'] as String?)?.trim() ?? '';
    final prompt = (scene['prompt'] as String?)?.trim() ?? '';
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        border: Border.all(color: BiuTokens.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
                decoration: BoxDecoration(
                  color: BiuTokens.purpleSoft,
                  borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
                ),
                child: Text('分镜 $index',
                    style: TextStyle(
                        fontSize: 11, fontWeight: FontWeight.w600, color: BiuTokens.purple)),
              ),
              const Spacer(),
              if (prompt.isNotEmpty)
                TextButton.icon(
                  onPressed: () => onMakeSimilar(prompt),
                  icon: const Icon(Icons.auto_awesome, size: 14),
                  label: const Text('生成同款', style: TextStyle(fontSize: 12)),
                  style: TextButton.styleFrom(
                    padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 0),
                    minimumSize: const Size(0, 28),
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
                ),
            ],
          ),
          if (desc.isNotEmpty) ...[
            const SizedBox(height: 4),
            Text(desc, style: TextStyle(fontSize: 13, color: BiuTokens.text, height: 1.4)),
          ],
          if (prompt.isNotEmpty) ...[
            const SizedBox(height: 4),
            SelectableText(prompt,
                style: TextStyle(fontSize: 12, color: BiuTokens.textSecondary, height: 1.4)),
          ],
        ],
      ),
    );
  }
}

class _TranscriptSection extends StatefulWidget {
  const _TranscriptSection({required this.transcript, required this.onCopy});
  final String transcript;
  final VoidCallback onCopy;

  @override
  State<_TranscriptSection> createState() => _TranscriptSectionState();
}

class _TranscriptSectionState extends State<_TranscriptSection> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        InkWell(
          onTap: () => setState(() => _expanded = !_expanded),
          child: Row(
            children: [
              Text('转写全文',
                  style: TextStyle(
                      fontSize: 11,
                      fontWeight: FontWeight.w600,
                      letterSpacing: 0.5,
                      color: BiuTokens.textMuted)),
              Icon(_expanded ? Icons.expand_less : Icons.expand_more,
                  size: 16, color: BiuTokens.textMuted),
              const Spacer(),
              InkWell(
                onTap: widget.onCopy,
                child: Icon(Icons.copy_rounded, size: 14, color: BiuTokens.textMuted),
              ),
            ],
          ),
        ),
        if (_expanded) ...[
          const SizedBox(height: 6),
          _Card(child: SelectableText(widget.transcript, style: _bodyStyle)),
        ],
      ],
    );
  }
}
