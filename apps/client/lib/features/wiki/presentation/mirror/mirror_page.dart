/// /wiki/p/:pid/mirror —— 项目导出为 Obsidian-style markdown 包。
///
/// 把当前项目所有页面拉一遍 → 每页转 markdown（含 frontmatter YAML
/// 头）→ 打 zip → 触发下载 / 写本地 Downloads。
///
/// knowcode 的 mirror 模块支持目录直写 + Obsidian git push +
/// 自定义路径策略；biumind B4.4 简化版仅做"一键 zip 导出"，B4.x 后段
/// 按需补 git / 直写目录 / Web Worker 增量同步。
library;

import 'dart:async';
import 'dart:convert';
import 'dart:io' show File;

import 'package:archive/archive.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:path/path.dart' as p;
import 'package:path_provider/path_provider.dart';

import '../../../../app/theme.dart';
import '../../../../core/layout/phone_nav.dart';
import '../../../../data/wiki_providers.dart' show wikiRepositoryProvider;
import '../../application/wiki_controller.dart';
import '../reader/block_to_markdown.dart';

class MirrorPage extends ConsumerStatefulWidget {
  const MirrorPage({super.key, required this.projectId});
  final String projectId;

  @override
  ConsumerState<MirrorPage> createState() => _MirrorPageState();
}

class _MirrorPageState extends ConsumerState<MirrorPage> {
  bool _exporting = false;
  String? _error;
  String? _resultPath;
  int _pageCount = 0;
  int _processed = 0;

  Future<void> _export() async {
    setState(() {
      _exporting = true;
      _error = null;
      _resultPath = null;
      _processed = 0;
    });
    final repo = ref.read(wikiRepositoryProvider);
    final state = ref.read(wikiControllerProvider).valueOrNull;
    if (repo == null || state == null) {
      setState(() {
        _exporting = false;
        _error = '未配置后端凭证';
      });
      return;
    }
    final activeProject = state.projects
        .where((p) => p.id == widget.projectId)
        .firstOrNull ??
        state.activeProject;
    if (activeProject == null) {
      setState(() {
        _exporting = false;
        _error = '当前项目未激活';
      });
      return;
    }
    try {
      // 1) 拉项目页面快照（先 refresh，再 watch first）
      try {
        await repo.refreshPages(widget.projectId);
      } on Exception {/* 忽略：用本地缓存 */}
      final pages = await repo.watchPages(widget.projectId).first;
      setState(() => _pageCount = pages.length);

      final archive = Archive();

      // 顶部 README.md：项目名 + 索引
      final readme = StringBuffer()
        ..writeln('# ${activeProject.name}')
        ..writeln()
        ..writeln('本目录由 BiuMind 知识库导出（${DateTime.now().toIso8601String()}）。')
        ..writeln()
        ..writeln('## 页面索引')
        ..writeln();
      for (final page in pages) {
        final filename = _safeFilename(page.title.isEmpty ? page.id : page.title);
        readme.writeln('- [${page.title.isEmpty ? "(未命名)" : page.title}]($filename.md)');
      }
      archive.addFile(_strFile('README.md', readme.toString()));

      // 2) 每页拉 blocks → markdown
      for (final page in pages) {
        try {
          await repo.refreshBlocks(widget.projectId, page.id);
        } on Exception {/* 用本地缓存 */}
        final blocks = await repo.watchBlocks(page.id).first;
        final body = blocksToMarkdown(blocks);
        final fm = _frontmatterYaml(page.title, page.id, page.updatedAt);
        final filename = _safeFilename(page.title.isEmpty ? page.id : page.title);
        archive.addFile(
          _strFile('$filename.md', '$fm\n$body'),
        );
        if (mounted) setState(() => _processed += 1);
      }

      // 3) 打 zip → 写本地（仅原生）
      final zipBytes = ZipEncoder().encode(archive);
      if (kIsWeb) {
        // Web 端暂不接 download API（需要 web package），先复制到剪贴板
        // 提示用户「需要原生客户端导出 zip」。
        await Clipboard.setData(ClipboardData(
          text: 'biumind-export-${activeProject.name}-zip 字节数=${zipBytes.length}',
        ));
        if (!mounted) return;
        setState(() {
          _exporting = false;
          _error = 'Web 端暂未支持 zip 下载（B4.x 后段补 dart:html anchor 路径）';
        });
        return;
      }
      final dir = await getApplicationDocumentsDirectory();
      final ts = DateTime.now()
          .toIso8601String()
          .replaceAll(':', '-')
          .substring(0, 19);
      final outPath = p.join(
        dir.path,
        'biumind-mirror-${_safeFilename(activeProject.name)}-$ts.zip',
      );
      await File(outPath).writeAsBytes(zipBytes);
      if (!mounted) return;
      setState(() {
        _exporting = false;
        _resultPath = outPath;
      });
    } on Exception catch (e) {
      if (!mounted) return;
      setState(() {
        _exporting = false;
        _error = '$e';
      });
    }
  }

  ArchiveFile _strFile(String name, String content) {
    final bytes = utf8.encode(content);
    return ArchiveFile(name, bytes.length, bytes);
  }

  String _frontmatterYaml(String title, String id, DateTime updatedAt) {
    final buf = StringBuffer()
      ..writeln('---')
      ..writeln('id: $id')
      ..writeln('title: ${_yamlString(title)}')
      ..writeln('updated_at: ${updatedAt.toIso8601String()}')
      ..writeln('---');
    return buf.toString();
  }

  String _yamlString(String v) {
    final escaped = v.replaceAll('"', r'\"');
    return '"$escaped"';
  }

  String _safeFilename(String input) {
    if (input.isEmpty) return 'untitled';
    final cleaned = input
        .replaceAll(RegExp(r'[\/\\:*?"<>|]'), '-')
        .replaceAll(RegExp(r'\s+'), ' ')
        .trim();
    return cleaned.isEmpty ? 'untitled' : cleaned;
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        _Header(
          exporting: _exporting,
          onExport: _exporting ? null : _export,
        ),
        Divider(height: 1, color: BiuTokens.borderSubtle),
        Expanded(child: _Body(
          exporting: _exporting,
          error: _error,
          resultPath: _resultPath,
          pageCount: _pageCount,
          processed: _processed,
        )),
      ],
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.exporting, required this.onExport});
  final bool exporting;
  final VoidCallback? onExport;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 48,
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Row(
        children: [
          // 子页头左位 ← (手机形态; 桌面 shrink 不占位, §3.3)。
          const PhoneBackButton(),
          Icon(Icons.folder_copy_outlined,
              size: 16, color: BiuTokens.textSecondary),
          const SizedBox(width: 8),
          Text(
            '镜像 / 导出',
            style: TextStyle(
              color: BiuTokens.text,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const Spacer(),
          FilledButton.icon(
            onPressed: onExport,
            icon: exporting
                ? const SizedBox(
                    width: 14,
                    height: 14,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: Colors.white,
                    ),
                  )
                : const Icon(Icons.archive_outlined, size: 14),
            label: Text(exporting ? '导出中…' : '导出为 zip'),
            style: FilledButton.styleFrom(
              backgroundColor: BiuTokens.green,
              padding:
                  const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
              textStyle: const TextStyle(fontSize: 12),
              minimumSize: const Size(0, 32),
            ),
          ),
        ],
      ),
    );
  }
}

class _Body extends StatelessWidget {
  const _Body({
    required this.exporting,
    required this.error,
    required this.resultPath,
    required this.pageCount,
    required this.processed,
  });
  final bool exporting;
  final String? error;
  final String? resultPath;
  final int pageCount;
  final int processed;

  @override
  Widget build(BuildContext context) {
    if (error != null) {
      return _State(
        icon: Icons.error_outline,
        color: BiuTokens.error,
        title: '导出失败',
        body: error!,
      );
    }
    if (resultPath != null) {
      return _State(
        icon: Icons.check_circle_outline,
        color: BiuTokens.success,
        title: '已导出 $pageCount 页',
        body: resultPath!,
        action: TextButton.icon(
          onPressed: () => Clipboard.setData(ClipboardData(text: resultPath!)),
          icon: const Icon(Icons.copy, size: 14),
          label: const Text('复制路径'),
        ),
      );
    }
    if (exporting) {
      final pct = pageCount == 0 ? null : (processed / pageCount).clamp(0.0, 1.0);
      return Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            const SizedBox(
              width: 32,
              height: 32,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
            const SizedBox(height: 16),
            Text(
              pageCount == 0
                  ? '正在拉取页面列表…'
                  : '已处理 $processed / $pageCount 页',
              style: TextStyle(color: BiuTokens.text, fontSize: 13),
            ),
            const SizedBox(height: 8),
            ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 240),
              child: ClipRRect(
                borderRadius: BorderRadius.circular(2),
                child: LinearProgressIndicator(
                  value: pct,
                  minHeight: 4,
                  backgroundColor: BiuTokens.surfaceMuted,
                  color: BiuTokens.green,
                ),
              ),
            ),
          ],
        ),
      );
    }
    return _State(
      icon: Icons.folder_copy_outlined,
      color: BiuTokens.textMuted,
      title: '导出 Obsidian 风格 markdown 包',
      body: '每页一个 .md 文件 + frontmatter YAML 头；附带 README.md 索引；'
          '当前仅支持桌面 / 移动原生客户端，Web 端导出待 B4.x 接入。',
    );
  }
}

class _State extends StatelessWidget {
  const _State({
    required this.icon,
    required this.color,
    required this.title,
    required this.body,
    this.action,
  });
  final IconData icon;
  final Color color;
  final String title;
  final String body;
  final Widget? action;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 48, color: color),
            const SizedBox(height: 12),
            Text(
              title,
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: 14,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 6),
            ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 360),
              child: SelectableText(
                body,
                textAlign: TextAlign.center,
                style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
              ),
            ),
            if (action != null) ...[
              const SizedBox(height: 12),
              action!,
            ],
          ],
        ),
      ),
    );
  }
}
