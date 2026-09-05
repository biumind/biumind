/// mirror_export — 项目导出（Obsidian 风格 zip）的纯函数部分。
///
/// 从 mirror_page.dart 抽出来，两个目的：
///   1. 单测不依赖 Flutter binding / Repository；
///   2. 收敛「每页 markdown 怎么拼」的唯一实现——正文直接用服务端
///      pages.body_md（字面保留 [[wikilink]]，Obsidian 打开是活链），
///      不走 reader 显示层的 wiki:// 重写（block_to_markdown.dart）。
library;

import 'dart:convert';

import 'package:archive/archive.dart';

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

/// 一页的导出输入：正文与 frontmatter 已按 Path C 规则解析好
/// （body_md 优先、blocks 兜底在调用侧完成），本结构只做打包。
class MirrorExportPage {
  const MirrorExportPage({
    required this.title,
    required this.id,
    required this.updatedAt,
    required this.frontmatter,
    required this.bodyMd,
  });
  final String title;
  final String id;
  final DateTime updatedAt;
  final Map<String, dynamic> frontmatter;
  final String bodyMd;
}

/// 项目名 + 导出时刻 → zip 文件名（原生写盘与 Web 下载共用同一命名）。
String mirrorZipFilename(String projectName, DateTime exportedAt) {
  final ts =
      exportedAt.toIso8601String().replaceAll(':', '-').substring(0, 19);
  return 'biumind-mirror-${safeExportFilename(projectName)}-$ts.zip';
}

/// 打整包 zip（纯 Dart，原生 / Web 两端通用）：
/// 顶部 README.md（项目名 + 导出时刻 + 页面索引）+ 每页一个 .md
/// （frontmatter YAML 头 + body）。返回 zip 字节。
List<int> buildMirrorZip({
  required String projectName,
  required List<MirrorExportPage> pages,
  required DateTime exportedAt,
}) {
  final archive = Archive();

  final readme = StringBuffer()
    ..writeln('# $projectName')
    ..writeln()
    ..writeln('本目录由 BiuMind 知识库导出（${exportedAt.toIso8601String()}）。')
    ..writeln()
    ..writeln('## 页面索引')
    ..writeln();
  for (final page in pages) {
    final filename =
        safeExportFilename(page.title.isEmpty ? page.id : page.title);
    readme.writeln(
        '- [${page.title.isEmpty ? "(未命名)" : page.title}]($filename.md)');
  }
  archive.addFile(_strFile('README.md', readme.toString()));

  for (final page in pages) {
    final md = exportPageMarkdown(
      title: page.title,
      id: page.id,
      updatedAt: page.updatedAt,
      frontmatter: page.frontmatter,
      bodyMd: page.bodyMd,
    );
    final filename =
        safeExportFilename(page.title.isEmpty ? page.id : page.title);
    archive.addFile(_strFile('$filename.md', md));
  }

  return ZipEncoder().encode(archive);
}

ArchiveFile _strFile(String name, String content) {
  final bytes = utf8.encode(content);
  return ArchiveFile(name, bytes.length, bytes);
}
