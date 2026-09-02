/// mirror_export — 项目导出（Obsidian 风格 zip）的纯函数部分。
///
/// 从 mirror_page.dart 抽出来，两个目的：
///   1. 单测不依赖 Flutter binding / Repository；
///   2. 收敛「每页 markdown 怎么拼」的唯一实现——正文直接用服务端
///      pages.body_md（字面保留 [[wikilink]]，Obsidian 打开是活链），
///      不走 reader 显示层的 wiki:// 重写（block_to_markdown.dart）。
library;

import 'dart:convert';

/// 拼一页的导出文件内容：frontmatter YAML 头 + 空行 + body。
///
/// [frontmatter] 是服务端 pages.frontmatter 原样 map（type / tags /
/// related / origin / sources / 任意自定义键），有多少写多少；
/// id / title / updated_at 由导出器统一写在前排，frontmatter 里的
/// 同名键跳过（page 行的 title 是权威值）。
String exportPageMarkdown({
  required String title,
  required String id,
  required DateTime updatedAt,
  required Map<String, dynamic> frontmatter,
  required String bodyMd,
}) {
  final fields = <String, dynamic>{
    'id': id,
    'title': title,
    'updated_at': updatedAt.toIso8601String(),
  };
  for (final entry in frontmatter.entries) {
    if (fields.containsKey(entry.key)) continue;
    if (entry.value == null) continue;
    fields[entry.key] = entry.value;
  }
  return '${frontmatterToYaml(fields)}\n$bodyMd';
}

/// Map → YAML frontmatter 块（含首尾 `---` 行，末尾带换行）。
///
/// 只承诺标量 + 标量列表的合法 YAML（Obsidian 能解析 type/tags/related）；
/// 嵌套 map 等复杂值降级为 inline JSON（YAML 是 JSON 超集，flow 语法合法）。
String frontmatterToYaml(Map<String, dynamic> fields) {
  final buf = StringBuffer('---\n');
  for (final entry in fields.entries) {
    buf
      ..write(entry.key)
      ..write(': ')
      ..write(_yamlValue(entry.value))
      ..write('\n');
  }
  buf.write('---\n');
  return buf.toString();
}

String _yamlValue(Object? v) {
  return switch (v) {
    String s => _yamlString(s),
    num n => n.toString(),
    bool b => b.toString(),
    List l => '[${l.map(_yamlValue).join(', ')}]',
    // 嵌套 map / 其他对象：inline JSON（合法 YAML flow 语法）。
    _ => jsonEncode(v),
  };
}

String _yamlString(String v) {
  final escaped = v.replaceAll(r'\', r'\\').replaceAll('"', r'\"');
  return '"$escaped"';
}

/// 页面标题 → 文件名安全串（跨平台禁字符替换为 '-'，空白折叠）。
String safeExportFilename(String input) {
  if (input.isEmpty) return 'untitled';
  final cleaned = input
      .replaceAll(RegExp(r'[\/\\:*?"<>|]'), '-')
      .replaceAll(RegExp(r'\s+'), ' ')
      .trim();
  return cleaned.isEmpty ? 'untitled' : cleaned;
}
