// SlashMenuV2 —— composer 输入 `/` 时弹出的统一菜单：内置命令 + 技能。
// 内置上下键选择 + Enter 触发；不响应 Tab（让 TextField 默认行为）。
// selectedIndex 跨 commands + skills 合并计数（commands 在前、skills 紧随
// 其后）；副作用由调用方传 onPickCommand / onPickSkill 回调处理。

import 'package:flutter/material.dart';

import '../../../../data/api/skill_client.dart' show Skill;
import '../../domain/slash_commands.dart';

class SlashMenuV2 extends StatelessWidget {
  const SlashMenuV2({
    super.key,
    required this.commands,
    required this.skills,
    required this.selectedIndex,
    required this.onPickCommand,
    required this.onPickSkill,
  });

  final List<SlashCommand> commands;
  final List<Skill> skills;

  /// 当前键盘 / 鼠标 hover 高亮的项；按 commands → skills 顺序计数；-1 = 无。
  final int selectedIndex;
  final ValueChanged<SlashCommand> onPickCommand;
  final ValueChanged<Skill> onPickSkill;

  @override
  Widget build(BuildContext context) {
    final total = commands.length + skills.length;
    if (total == 0) return const SizedBox.shrink();
    final theme = Theme.of(context);
    return Material(
      elevation: 2,
      borderRadius: BorderRadius.circular(8),
      color: theme.colorScheme.surface,
      child: Container(
        decoration: BoxDecoration(
          borderRadius: BorderRadius.circular(8),
          border: Border.all(color: theme.colorScheme.outlineVariant),
        ),
        constraints: const BoxConstraints(maxHeight: 240),
        child: ListView.builder(
          shrinkWrap: true,
          padding: const EdgeInsets.symmetric(vertical: 4),
          itemCount: total,
          itemBuilder: (_, i) {
            if (i < commands.length) {
              final c = commands[i];
              return _SlashRow(
                icon: c.icon,
                label: c.label,
                hint: c.hint,
                highlighted: i == selectedIndex,
                onTap: () => onPickCommand(c),
              );
            }
            final s = skills[i - commands.length];
            return _SlashRow(
              icon: Icons.auto_awesome_outlined,
              label: '/${s.identifier}',
              hint: s.description,
              highlighted: i == selectedIndex,
              onTap: () => onPickSkill(s),
            );
          },
        ),
      ),
    );
  }
}

class _SlashRow extends StatelessWidget {
  const _SlashRow({
    required this.icon,
    required this.label,
    required this.hint,
    required this.highlighted,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final String hint;
  final bool highlighted;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return InkWell(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        color: highlighted
            ? theme.colorScheme.primaryContainer.withValues(alpha: 0.4)
            : null,
        child: Row(
          children: [
            Icon(icon, size: 16, color: theme.colorScheme.primary),
            const SizedBox(width: 8),
            Text(
              label,
              style: theme.textTheme.bodyMedium?.copyWith(
                fontFamily: 'monospace',
                fontWeight: FontWeight.w500,
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Text(
                hint,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
