// Custom rule editor — replaces the generic FormLayout with a
// chip-based UI that fits the actual shape of a watch rule
// (lists of keywords, multi-select sources, severity SegmentedButton,
// cooldown slider).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../../app/theme.dart';
import '../models.dart';
import '../providers.dart';
import 'rule_recipe_editor.dart';

Future<bool?> showRuleEditorSheet(
  BuildContext context,
  WidgetRef ref, {
  Rule? initial,
}) {
  return showDialog<bool>(
    context: context,
    builder: (ctx) => _RuleEditorDialog(initial: initial),
  );
}

class _RuleEditorDialog extends ConsumerStatefulWidget {
  const _RuleEditorDialog({this.initial});
  final Rule? initial;
  @override
  ConsumerState<_RuleEditorDialog> createState() => _RuleEditorDialogState();
}

class _RuleEditorDialogState extends ConsumerState<_RuleEditorDialog> {
  late final TextEditingController _nameCtrl;
  late final TextEditingController _nlCtrl;
  late final TextEditingController _semanticCtrl;
  late List<String> _matchAny;
  late List<String> _matchAll;
  late List<String> _exclude;
  late Set<String> _sources;
  late String _severity;
  late int _cooldownSec;
  late List<RuleRecipe> _recipes;
  bool _busy = false;
  bool _aiBusy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    final r = widget.initial;
    _nameCtrl = TextEditingController(text: r?.name ?? '');
    _nlCtrl = TextEditingController();
    _semanticCtrl = TextEditingController(text: r?.semanticQuery ?? '');
    _matchAny = List.of(r?.matchAny ?? const []);
    _matchAll = List.of(r?.matchAll ?? const []);
    _exclude = List.of(r?.exclude ?? const []);
    _sources = Set.of(r?.sources ?? const ['*']);
    _severity = r?.onHitBadge ?? 'warn';
    _cooldownSec = r?.cooldownSec ?? 1800;
    _recipes = (r?.actions ?? const [])
        .map(RuleRecipe.fromJson)
        .toList();
  }

  @override
  void dispose() {
    _nameCtrl.dispose();
    _nlCtrl.dispose();
    _semanticCtrl.dispose();
    super.dispose();
  }

  Future<void> _runAI() async {
    final text = _nlCtrl.text.trim();
    if (text.isEmpty) {
      setState(() => _error = '请先输入一句话描述');
      return;
    }
    final actions = ref.read(rssActionsProvider);
    if (actions == null) {
      setState(() => _error = '尚未登录');
      return;
    }
    setState(() {
      _aiBusy = true;
      _error = null;
    });
    try {
      final draft = await actions.rulesFromNL(text);
      if (!mounted) return;
      setState(() {
        // Only overwrite empty fields by default, but the user just
        // clicked "AI 解析" — replace wholesale, they can edit after.
        if (draft.name.isNotEmpty) _nameCtrl.text = draft.name;
        _matchAny = List.of(draft.matchAny);
        _matchAll = List.of(draft.matchAll);
        _exclude = List.of(draft.exclude);
        _severity = draft.onHitBadge;
        _cooldownSec = draft.cooldownSec.clamp(0, 7200);
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = 'AI 解析失败：$e');
    } finally {
      if (mounted) setState(() => _aiBusy = false);
    }
  }

  Future<void> _save() async {
    final actions = ref.read(rssActionsProvider);
    if (actions == null) return;
    if (_nameCtrl.text.trim().isEmpty) {
      setState(() => _error = '请输入规则名称');
      return;
    }
    if (_matchAny.isEmpty && _matchAll.isEmpty) {
      setState(() => _error = '至少需要填写一个“任一关键词”或“全部关键词”');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final recipeJSON = _recipes.map((r) => r.toJson()).toList();
      final semanticQuery = _semanticCtrl.text.trim();
      if (widget.initial == null) {
        await actions.rulesCreate(
          name: _nameCtrl.text.trim(),
          matchAny: _matchAny,
          matchAll: _matchAll,
          exclude: _exclude,
          sources: _sources.toList(),
          onHitBadge: _severity,
          cooldownSec: _cooldownSec,
          semanticQuery: semanticQuery.isEmpty ? null : semanticQuery,
          actions: recipeJSON,
        );
      } else {
        await actions.rulesUpdate(
          id: widget.initial!.id,
          name: _nameCtrl.text.trim(),
          onHitBadge: _severity,
          cooldownSec: _cooldownSec,
          semanticQuery: semanticQuery,
          actions: recipeJSON,
        );
      }
      ref.refreshRules();
      ref.refreshHits();
      if (!mounted) return;
      Navigator.of(context).pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _busy = false;
        _error = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final boards = ref.watch(boardsProvider).valueOrNull ?? const [];
    return AlertDialog(
      title: Text(widget.initial == null ? '新建雷达规则' : '编辑雷达规则'),
      content: SizedBox(
        width: 560,
        child: SingleChildScrollView(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            mainAxisSize: MainAxisSize.min,
            children: [
              _AIPanel(
                controller: _nlCtrl,
                busy: _aiBusy || _busy,
                onRun: _runAI,
              ),
              const SizedBox(height: BiuTokens.space4),
              TextField(
                controller: _nameCtrl,
                enabled: !_busy,
                decoration: const InputDecoration(
                  labelText: '规则名称',
                  hintText: '例如：OpenAI 新模型',
                ),
              ),
              const SizedBox(height: BiuTokens.space4),
              _ChipsField(
                label: '任一关键词命中（any）',
                hint: '输入关键词后按回车',
                values: _matchAny,
                enabled: !_busy,
                onChanged: (v) => setState(() => _matchAny = v),
              ),
              const SizedBox(height: BiuTokens.space4),
              _ChipsField(
                label: '所有关键词都要（all）',
                hint: '输入关键词后按回车',
                values: _matchAll,
                enabled: !_busy,
                onChanged: (v) => setState(() => _matchAll = v),
              ),
              const SizedBox(height: BiuTokens.space4),
              _ChipsField(
                label: '排除',
                hint: '命中以下关键词时不触发',
                values: _exclude,
                enabled: !_busy,
                onChanged: (v) => setState(() => _exclude = v),
              ),
              const SizedBox(height: BiuTokens.space4),
              _SectionLabel('数据源'),
              const SizedBox(height: BiuTokens.space2),
              _SourcesPicker(
                sources: _sources,
                boards: boards,
                enabled: !_busy,
                onChanged: (v) => setState(() => _sources = v),
              ),
              const SizedBox(height: BiuTokens.space4),
              _SectionLabel('命中级别'),
              const SizedBox(height: BiuTokens.space2),
              SegmentedButton<String>(
                segments: const [
                  ButtonSegment(value: 'info', label: Text('提示')),
                  ButtonSegment(value: 'warn', label: Text('警告')),
                  ButtonSegment(value: 'error', label: Text('紧急')),
                ],
                selected: {_severity},
                onSelectionChanged: _busy
                    ? null
                    : (s) => setState(() => _severity = s.first),
              ),
              const SizedBox(height: BiuTokens.space4),
              _SectionLabel('冷却时间：${_cooldownLabel(_cooldownSec)}'),
              Slider(
                min: 0,
                max: 7200,
                divisions: 24,
                value: _cooldownSec.toDouble().clamp(0, 7200),
                onChanged: _busy
                    ? null
                    : (v) => setState(() => _cooldownSec = v.round()),
              ),
              const SizedBox(height: BiuTokens.space4),
              _SectionLabel('语义查询 (可选, 留空走关键词)'),
              const SizedBox(height: BiuTokens.space2),
              TextField(
                controller: _semanticCtrl,
                enabled: !_busy,
                decoration: const InputDecoration(
                  hintText: '例如: 任何关于 EU AI 监管的内容',
                  isDense: true,
                ),
              ),
              const SizedBox(height: BiuTokens.space4),
              RuleRecipeEditor(
                initial: _recipes,
                onChanged: (v) => setState(() => _recipes = v),
              ),
              if (_error != null) ...[
                const SizedBox(height: BiuTokens.space2),
                Text(_error!,
                    style: const TextStyle(
                        color: BiuTokens.error, fontSize: 13)),
              ],
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: _busy ? null : () => Navigator.of(context).pop(false),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: _busy ? null : _save,
          child: _busy
              ? const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('保存'),
        ),
      ],
    );
  }

  String _cooldownLabel(int sec) {
    if (sec == 0) return '不限制（每次都触发）';
    if (sec < 60) return '$sec 秒';
    if (sec < 3600) return '${(sec / 60).round()} 分钟';
    return '${(sec / 3600).toStringAsFixed(1)} 小时';
  }
}

class _SectionLabel extends StatelessWidget {
  const _SectionLabel(this.text);
  final String text;
  @override
  Widget build(BuildContext context) {
    return Text(text,
        style: TextStyle(
            fontSize: 13,
            fontWeight: FontWeight.w500,
            color: BiuTokens.textSecondary));
  }
}

class _ChipsField extends StatefulWidget {
  const _ChipsField({
    required this.label,
    required this.hint,
    required this.values,
    required this.onChanged,
    this.enabled = true,
  });

  final String label;
  final String hint;
  final List<String> values;
  final ValueChanged<List<String>> onChanged;
  final bool enabled;

  @override
  State<_ChipsField> createState() => _ChipsFieldState();
}

class _ChipsFieldState extends State<_ChipsField> {
  final _ctrl = TextEditingController();
  final _focusNode = FocusNode();

  @override
  void dispose() {
    _ctrl.dispose();
    _focusNode.dispose();
    super.dispose();
  }

  void _commit() {
    final v = _ctrl.text.trim();
    if (v.isEmpty) return;
    if (widget.values.contains(v)) {
      _ctrl.clear();
      return;
    }
    widget.onChanged([...widget.values, v]);
    _ctrl.clear();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _SectionLabel(widget.label),
        const SizedBox(height: BiuTokens.space2),
        Container(
          padding: const EdgeInsets.all(BiuTokens.space2),
          decoration: BoxDecoration(
            color: BiuTokens.surface,
            borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
            border: Border.all(color: BiuTokens.border),
          ),
          child: Wrap(
            spacing: BiuTokens.space2,
            runSpacing: BiuTokens.space1,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              ...widget.values.map((v) => Chip(
                    label: Text(v, style: const TextStyle(fontSize: 12)),
                    onDeleted: widget.enabled
                        ? () {
                            final next = [...widget.values]..remove(v);
                            widget.onChanged(next);
                          }
                        : null,
                  )),
              SizedBox(
                width: 180,
                child: TextField(
                  controller: _ctrl,
                  focusNode: _focusNode,
                  enabled: widget.enabled,
                  decoration: InputDecoration(
                    isDense: true,
                    border: InputBorder.none,
                    enabledBorder: InputBorder.none,
                    focusedBorder: InputBorder.none,
                    hintText: widget.hint,
                    filled: false,
                    contentPadding: const EdgeInsets.symmetric(
                        horizontal: 8, vertical: 6),
                  ),
                  onSubmitted: (_) {
                    _commit();
                    _focusNode.requestFocus();
                  },
                ),
              ),
            ],
          ),
        ),
      ],
    );
  }
}

class _SourcesPicker extends StatelessWidget {
  const _SourcesPicker({
    required this.sources,
    required this.boards,
    required this.onChanged,
    this.enabled = true,
  });

  final Set<String> sources;
  final List<Board> boards;
  final ValueChanged<Set<String>> onChanged;
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    final items = <(String, String)>[
      ('*', '默认（全部数据源）'),
      ('rss', 'RSS 收件箱'),
      for (final b in boards) ('boards:${b.id}', b.name),
    ];
    return Wrap(
      spacing: BiuTokens.space2,
      runSpacing: BiuTokens.space2,
      children: items.map((entry) {
        final selected = sources.contains(entry.$1);
        return FilterChip(
          label: Text(entry.$2, style: const TextStyle(fontSize: 12)),
          selected: selected,
          onSelected: !enabled
              ? null
              : (v) {
                  final next = Set<String>.of(sources);
                  if (v) {
                    if (entry.$1 == '*') {
                      next.clear();
                      next.add('*');
                    } else {
                      next.remove('*');
                      next.add(entry.$1);
                    }
                  } else {
                    next.remove(entry.$1);
                    if (next.isEmpty) next.add('*');
                  }
                  onChanged(next);
                },
        );
      }).toList(),
    );
  }
}

class _AIPanel extends StatelessWidget {
  const _AIPanel({
    required this.controller,
    required this.busy,
    required this.onRun,
  });
  final TextEditingController controller;
  final bool busy;
  final VoidCallback onRun;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final tint = scheme.primary.withValues(alpha: 0.08);
    return Container(
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: tint,
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        border: Border.all(color: scheme.primary.withValues(alpha: 0.18)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              Icon(Icons.auto_awesome, size: 18, color: scheme.primary),
              const SizedBox(width: BiuTokens.space2),
              Text(
                '用自然语言描述',
                style: TextStyle(
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                  color: scheme.primary,
                ),
              ),
              const Spacer(),
              FilledButton.tonalIcon(
                onPressed: busy ? null : onRun,
                icon: busy
                    ? const SizedBox(
                        width: 14, height: 14,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.auto_fix_high, size: 16),
                label: Text(busy ? '解析中…' : 'AI 解析'),
                style: FilledButton.styleFrom(
                  visualDensity: VisualDensity.compact,
                  textStyle: const TextStyle(fontSize: 12),
                ),
              ),
            ],
          ),
          const SizedBox(height: BiuTokens.space2),
          TextField(
            controller: controller,
            enabled: !busy,
            minLines: 2,
            maxLines: 3,
            maxLength: 200,
            textInputAction: TextInputAction.done,
            onSubmitted: busy ? null : (_) => onRun(),
            decoration: const InputDecoration(
              isDense: true,
              hintText:
                  '例如：凡是 OpenAI / Anthropic 发布新模型的事都通知我，不要招聘信息',
              border: OutlineInputBorder(),
              counterText: '',
            ),
          ),
        ],
      ),
    );
  }
}
