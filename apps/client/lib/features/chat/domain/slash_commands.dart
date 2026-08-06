// Slash commands —— composer 输入 `/` 时弹出的命令列表。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md。
//
// 每个 SlashCommand 只描述（id / icon / label / hint），副作用由 ComposerV2
// 内部 dispatcher 处理 —— 让 domain 保持纯净、可单测。

import 'package:flutter/material.dart';

class SlashCommand {
  final String id;
  final IconData icon;
  final String label;
  final String hint;

  const SlashCommand({
    required this.id,
    required this.icon,
    required this.label,
    required this.hint,
  });
}

const List<SlashCommand> kSlashCommands = [
  SlashCommand(
    id: 'new',
    icon: Icons.add_comment_outlined,
    label: '/new',
    hint: '新建对话',
  ),
  SlashCommand(
    id: 'clear',
    icon: Icons.clear,
    label: '/clear',
    hint: '清空输入',
  ),
  SlashCommand(
    id: 'note',
    icon: Icons.note_add_outlined,
    label: '/note',
    hint: '把最近一条回复存为笔记',
  ),
  SlashCommand(
    id: 'help',
    icon: Icons.help_outline,
    label: '/help',
    hint: '查看可用命令',
  ),
];

/// 从 composer 当前文本解析斜杠命令片段。
/// 返回 (commandName, hasArgs)，或 null（不在斜杠模式）。
class ParsedSlash {
  final String name;
  /// true = 已经敲了空格 / 参数；菜单这时让位给输入。
  final List<String> args;

  const ParsedSlash({required this.name, required this.args});
}

ParsedSlash? parseSlash(String text) {
  if (text.isEmpty || text[0] != '/') return null;
  final parts = text.substring(1).split(RegExp(r'\s+'));
  if (parts.isEmpty) return const ParsedSlash(name: '', args: []);
  return ParsedSlash(
    name: parts.first,
    args: parts.length > 1 ? parts.sublist(1).where((p) => p.isNotEmpty).toList() : const [],
  );
}

/// 按当前敲入的命令名前缀过滤。空 = 全列。
List<SlashCommand> filterSlashCommands(String name) {
  if (name.isEmpty) return kSlashCommands;
  final lower = name.toLowerCase();
  return kSlashCommands
      .where((c) => c.id.toLowerCase().startsWith(lower))
      .toList(growable: false);
}
