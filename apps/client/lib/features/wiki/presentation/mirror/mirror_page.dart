/// /wiki/p/:pid/mirror —— 项目导出为 Obsidian-style markdown 包。
///
/// 把当前项目所有页面拉一遍 → 每页取服务端权威 body_md（字面保留
/// [[wikilink]]，Obsidian 打开是活链；不走 reader 显示层的 wiki://
/// 重写）+ frontmatter YAML 头（全字段）→ 打 zip → 触发下载 / 写本地。
/// body_md 为空（未回填的老页）时 fallback 到 blocksToMarkdown(blocks)。
///
/// zip 打包是纯 Dart（mirror_export.dart，archive 包两端通用）；落盘
/// 分端：原生写应用文档目录（mirror_download_io.dart），Web 走 Blob +
/// anchor 浏览器下载（mirror_download_web.dart），conditional import
/// 惯例同 data/local/db.dart。
///
/// knowcode 的 mirror 模块支持目录直写 + Obsidian git push +
/// 自定义路径策略；biumind B4.4 简化版仅做"一键 zip 导出"，B4.x 后段
/// 按需补 git / 直写目录 / Web Worker 增量同步。
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../../../core/layout/phone_nav.dart';
import '../../../../data/wiki_providers.dart' show wikiRepositoryProvider;
import '../../application/wiki_controller.dart';
import '../reader/block_to_markdown.dart';
import 'mirror_download_io.dart'
    if (dart.library.html) 'mirror_download_web.dart' as download;
import 'mirror_export.dart';

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

      // 2) 每页取服务端权威 body_md + frontmatter。
      //    body_md 字面保留 [[wikilink]]（Path C 权威正文），Obsidian
      //    打开即是活链；空 body_md（未回填老页 / 空页）fallback 到
      //    blocks 渲染（接受其 wiki:// 重写，聊胜于无）。
      final exportPages = <MirrorExportPage>[];
      for (final page in pages) {
        var body = '';
        var frontmatter = const <String, dynamic>{};
        try {
          final serverPage =
              await repo.client.getPage(widget.projectId, page.id);
          body = serverPage.bodyMd;
          frontmatter = serverPage.frontmatter;
        } on Exception {/* 离线 / 接口失败：走本地 blocks 兜底 */}
        if (body.trim().isEmpty) {
          try {
            await repo.refreshBlocks(widget.projectId, page.id);
          } on Exception {/* 用本地缓存 */}
          final blocks = await repo.watchBlocks(page.id).first;
          body = blocksToMarkdown(blocks);
        }
        exportPages.add(MirrorExportPage(
          title: page.title,
          id: page.id,
          updatedAt: page.updatedAt,
          frontmatter: frontmatter,
          bodyMd: body,
        ));
        if (mounted) setState(() => _processed += 1);
      }

      // 3) 打 zip（纯 Dart，两端通用）→ 落盘：原生写文档目录，
      //    Web 经 Blob + anchor 触发浏览器下载。
      final now = DateTime.now();
      final zipBytes = buildMirrorZip(
        projectName: activeProject.name,
        pages: exportPages,
        exportedAt: now,
      );
      final saved = await download.saveMirrorZip(
        filename: mirrorZipFilename(activeProject.name, now),
        zipBytes: zipBytes,
      );
      if (!mounted) return;
      setState(() {
        _exporting = false;
        _resultPath = saved;
      });
    } on Exception catch (e) {
      if (!mounted) return;
      setState(() {
        _exporting = false;
        _error = '$e';
      });
    }
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
          '桌面 / 移动端写入应用文档目录，Web 端经浏览器下载。',
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
