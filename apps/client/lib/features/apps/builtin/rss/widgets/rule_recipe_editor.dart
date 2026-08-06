// M9.3 RuleEditor "动作配方"区. 4 张可添加的卡片 (notify/wiki/task/skill),
// 单击添加 → 展开内联表单. 序列化为 actions[] (List<Map>) 提交给
// rules_create / rules_update.
//
// 简化:
//   - 同 type 多次添加合法 (例如 2 个 notify 推不同 channel)
//   - 内联表单字段最少: notify=channels, wiki=page_path, task=due_offset_days,
//     skill=skill_id. 所有 field 留空则用后端默认.
//   - 拖拽排序留 v2 polish; 当前用 ↑↓ 按钮.
//
// 设计取舍: 不做 Form 抽象, 直接内联. 4 个 type 写死, 加新 type 修这个文件.

import 'package:flutter/material.dart';
import '../../../../../app/theme.dart';

class RuleRecipe {
  RuleRecipe({required this.type, this.config = const {}});
  String type;
  Map<String, dynamic> config;

  Map<String, dynamic> toJson() => {
        'type': type,
        if (config.isNotEmpty) 'config': config,
      };

  static RuleRecipe fromJson(Map<String, dynamic> j) => RuleRecipe(
        type: (j['type'] as String?) ?? 'notify',
        config: (j['config'] as Map?)?.cast<String, dynamic>() ?? const {},
      );
}

class RuleRecipeEditor extends StatefulWidget {
  const RuleRecipeEditor({
    super.key,
    required this.initial,
    required this.onChanged,
  });

  final List<RuleRecipe> initial;
  final ValueChanged<List<RuleRecipe>> onChanged;

  @override
  State<RuleRecipeEditor> createState() => _RuleRecipeEditorState();
}

class _RuleRecipeEditorState extends State<RuleRecipeEditor> {
  late List<RuleRecipe> _items;

  @override
  void initState() {
    super.initState();
    _items = List.of(widget.initial);
  }

  void _emit() => widget.onChanged(List.of(_items));

  void _add(String type) {
    setState(() {
      _items.add(RuleRecipe(type: type, config: {}));
    });
    _emit();
  }

  void _remove(int i) {
    setState(() => _items.removeAt(i));
    _emit();
  }

  void _move(int i, int delta) {
    final j = i + delta;
    if (j < 0 || j >= _items.length) return;
    setState(() {
      final tmp = _items[i];
      _items[i] = _items[j];
      _items[j] = tmp;
    });
    _emit();
  }

  void _patch(int i, Map<String, dynamic> patch) {
    setState(() {
      _items[i].config = {..._items[i].config, ...patch};
    });
    _emit();
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('命中后做什么', style: Theme.of(context).textTheme.titleSmall),
        const SizedBox(height: 4),
        Text(
          '可添加 4 类动作: 通知 / 沉到 Wiki / 建任务 / 跑 Skill. 顺序执行, 单条失败不阻塞后续.',
          style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
        ),
        const SizedBox(height: BiuTokens.space2),
        if (_items.isEmpty)
          Padding(
            padding: const EdgeInsets.all(BiuTokens.space3),
            child: Text(
              '尚未添加任何动作 — 默认仅推一个 badge 通知',
              style: TextStyle(fontSize: 12, color: BiuTokens.textSecondary),
            ),
          )
        else
          ..._items.asMap().entries.map((e) =>
              _RecipeCard(
                index: e.key,
                total: _items.length,
                recipe: e.value,
                onRemove: () => _remove(e.key),
                onMoveUp: () => _move(e.key, -1),
                onMoveDown: () => _move(e.key, 1),
                onConfigChange: (patch) => _patch(e.key, patch),
              )),
        const SizedBox(height: BiuTokens.space2),
        Row(
          children: [
            for (final t in const ['notify', 'wiki', 'task', 'skill']) ...[
              _AddPill(type: t, onAdd: () => _add(t)),
              const SizedBox(width: 6),
            ],
          ],
        ),
      ],
    );
  }
}

class _AddPill extends StatelessWidget {
  const _AddPill({required this.type, required this.onAdd});
  final String type;
  final VoidCallback onAdd;

  static const _labels = {
    'notify': '+ 通知',
    'wiki': '+ Wiki',
    'task': '+ 任务',
    'skill': '+ Skill',
  };

  @override
  Widget build(BuildContext context) {
    return OutlinedButton(
      onPressed: onAdd,
      style: OutlinedButton.styleFrom(
        visualDensity: VisualDensity.compact,
        textStyle: const TextStyle(fontSize: 11),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
      ),
      child: Text(_labels[type] ?? type),
    );
  }
}

class _RecipeCard extends StatelessWidget {
  const _RecipeCard({
    required this.index,
    required this.total,
    required this.recipe,
    required this.onRemove,
    required this.onMoveUp,
    required this.onMoveDown,
    required this.onConfigChange,
  });

  final int index;
  final int total;
  final RuleRecipe recipe;
  final VoidCallback onRemove;
  final VoidCallback onMoveUp;
  final VoidCallback onMoveDown;
  final ValueChanged<Map<String, dynamic>> onConfigChange;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(bottom: BiuTokens.space2),
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                decoration: BoxDecoration(
                  color: _typeColor(recipe.type).withValues(alpha: 0.15),
                  borderRadius: BorderRadius.circular(4),
                ),
                child: Text(
                  '${index + 1}. ${_typeLabel(recipe.type)}',
                  style: TextStyle(
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                    color: _typeColor(recipe.type),
                  ),
                ),
              ),
              const Spacer(),
              IconButton(
                visualDensity: VisualDensity.compact,
                tooltip: '上移',
                onPressed: index > 0 ? onMoveUp : null,
                icon: const Icon(Icons.arrow_upward, size: 14),
              ),
              IconButton(
                visualDensity: VisualDensity.compact,
                tooltip: '下移',
                onPressed: index < total - 1 ? onMoveDown : null,
                icon: const Icon(Icons.arrow_downward, size: 14),
              ),
              IconButton(
                visualDensity: VisualDensity.compact,
                tooltip: '删除',
                onPressed: onRemove,
                icon: const Icon(Icons.close, size: 14),
              ),
            ],
          ),
          const SizedBox(height: 4),
          _ConfigForm(recipe: recipe, onChange: onConfigChange),
        ],
      ),
    );
  }

  static Color _typeColor(String t) {
    switch (t) {
      case 'notify':
        return Colors.blueAccent;
      case 'wiki':
        return Colors.purpleAccent;
      case 'task':
        return Colors.orangeAccent;
      case 'skill':
        return Colors.greenAccent;
    }
    return BiuTokens.textSecondary;
  }

  static String _typeLabel(String t) {
    switch (t) {
      case 'notify':
        return '🔔 通知';
      case 'wiki':
        return '📚 沉 Wiki';
      case 'task':
        return '✅ 建任务';
      case 'skill':
        return '🛠 跑 Skill';
    }
    return t;
  }
}

class _ConfigForm extends StatelessWidget {
  const _ConfigForm({required this.recipe, required this.onChange});
  final RuleRecipe recipe;
  final ValueChanged<Map<String, dynamic>> onChange;

  @override
  Widget build(BuildContext context) {
    switch (recipe.type) {
      case 'notify':
        return _NotifyForm(recipe: recipe, onChange: onChange);
      case 'wiki':
        return _WikiForm(recipe: recipe, onChange: onChange);
      case 'task':
        return _TaskForm(recipe: recipe, onChange: onChange);
      case 'skill':
        return _SkillForm(recipe: recipe, onChange: onChange);
    }
    return Text('未知类型: ${recipe.type}',
        style: TextStyle(fontSize: 11, color: Colors.redAccent));
  }
}

class _NotifyForm extends StatelessWidget {
  const _NotifyForm({required this.recipe, required this.onChange});
  final RuleRecipe recipe;
  final ValueChanged<Map<String, dynamic>> onChange;

  @override
  Widget build(BuildContext context) {
    final channels = (recipe.config['channels'] as List?)?.cast<String>() ?? [];
    return TextFormField(
      initialValue: channels.join(','),
      decoration: const InputDecoration(
        labelText: '推送通道 (逗号分隔, 留空=仅 badge)',
        hintText: 'feishu_xxx, bark_yyy',
        isDense: true,
      ),
      style: const TextStyle(fontSize: 12),
      onChanged: (v) {
        final list = v
            .split(',')
            .map((s) => s.trim())
            .where((s) => s.isNotEmpty)
            .toList();
        onChange({'channels': list});
      },
    );
  }
}

class _WikiForm extends StatelessWidget {
  const _WikiForm({required this.recipe, required this.onChange});
  final RuleRecipe recipe;
  final ValueChanged<Map<String, dynamic>> onChange;

  @override
  Widget build(BuildContext context) {
    return TextFormField(
      initialValue: recipe.config['page_path'] as String?,
      decoration: const InputDecoration(
        labelText: 'Wiki 页面路径 (留空=信息流/雷达/{rule_name})',
        hintText: '信息流/雷达/AI 监控',
        isDense: true,
      ),
      style: const TextStyle(fontSize: 12),
      onChanged: (v) => onChange({'page_path': v.trim()}),
    );
  }
}

class _TaskForm extends StatelessWidget {
  const _TaskForm({required this.recipe, required this.onChange});
  final RuleRecipe recipe;
  final ValueChanged<Map<String, dynamic>> onChange;

  @override
  Widget build(BuildContext context) {
    final due = recipe.config['due_offset_days'] as int? ?? 3;
    final priority = recipe.config['priority'] as String? ?? 'normal';
    return Row(
      children: [
        Expanded(
          flex: 2,
          child: TextFormField(
            initialValue: '$due',
            keyboardType: TextInputType.number,
            decoration: const InputDecoration(
              labelText: '截止 (天后)',
              isDense: true,
            ),
            style: const TextStyle(fontSize: 12),
            onChanged: (v) =>
                onChange({'due_offset_days': int.tryParse(v.trim()) ?? 3}),
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          flex: 3,
          child: DropdownButtonFormField<String>(
            initialValue: priority,
            decoration:
                const InputDecoration(labelText: '优先级', isDense: true),
            style: const TextStyle(fontSize: 12, color: Colors.black87),
            items: const [
              DropdownMenuItem(value: 'low', child: Text('低')),
              DropdownMenuItem(value: 'normal', child: Text('普通')),
              DropdownMenuItem(value: 'high', child: Text('高')),
            ],
            onChanged: (v) => onChange({'priority': v ?? 'normal'}),
          ),
        ),
      ],
    );
  }
}

class _SkillForm extends StatelessWidget {
  const _SkillForm({required this.recipe, required this.onChange});
  final RuleRecipe recipe;
  final ValueChanged<Map<String, dynamic>> onChange;

  @override
  Widget build(BuildContext context) {
    return TextFormField(
      initialValue: recipe.config['skill_id'] as String?,
      decoration: const InputDecoration(
        labelText: 'Skill ID (UUID)',
        hintText: 'xxxxxxxx-xxxx-...',
        isDense: true,
      ),
      style: const TextStyle(fontSize: 12),
      onChanged: (v) => onChange({'skill_id': v.trim()}),
    );
  }
}
