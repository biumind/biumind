/// Wiki 项目源文件列表页 —— /wiki/p/:pid/sources。
///
/// 当前 B2.4 基础版：列表 + 删除 + parse_status 状态徽标 + 右上角
/// "上传"按钮（暂占位，B2.5 接 file_picker → /v1/files/upload → POST sources）。
///
/// 数据通路：sourcesListProvider(projectId) FutureProvider → WikiClient
/// 直拉 brain；不走 Drift（B2.3 决策：sources 简化先无本地缓存）。
library;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme.dart';
import '../../../../core/layout/phone_nav.dart';
import '../../../../data/api/wiki_client.dart' show WikiSource;
import '../../../../data/wiki_providers.dart'
    show sourcesListProvider, wikiRepositoryProvider;
import '../connectors/import_dialog.dart';

class SourcesPage extends ConsumerStatefulWidget {
  const SourcesPage({super.key, required this.projectId});

  final String projectId;

  @override
  ConsumerState<SourcesPage> createState() => _SourcesPageState();
}

class _SourcesPageState extends ConsumerState<SourcesPage> {
  String get projectId => widget.projectId;

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(sourcesListProvider(projectId));
    return Column(
      children: [
        _Header(
          onUpload: () =>
              ImportDialog.show(context, projectId: projectId),
          onRefresh: () => ref.invalidate(sourcesListProvider(projectId)),
        ),
        Divider(height: 1, color: BiuTokens.borderSubtle),
        Expanded(
          child: async.when(
            loading: () => const Center(child: CircularProgressIndicator()),
            error: (e, _) => _ErrorView(message: e.toString()),
            data: (rows) {
              if (rows.isEmpty) return const _EmptyView();
              return ListView.separated(
                padding: const EdgeInsets.symmetric(vertical: 8),
                itemCount: rows.length,
                separatorBuilder: (_, _) => Divider(
                  height: 1,
                  color: BiuTokens.borderSubtle.withValues(alpha: 0.6),
                ),
                itemBuilder: (_, i) => _SourceRow(
                  source: rows[i],
                  onDelete: () => _confirmDelete(
                    context,
                    ref,
                    projectId: projectId,
                    source: rows[i],
                  ),
                  onIngest: () => _startIngest(rows[i]),
                ),
              );
            },
          ),
        ),
      ],
    );
  }

  void _toast(String msg) {
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
  }

  /// 触发 ingest task 后跳到 SSE 进度页。Phase 1 合并后 ingest_tasks.source_id
  /// FK 指向统一 wiki_sources，直接传 sourceId；worker 反查 internal_api 取
  /// extracted_text 作 LLM 输入。upload 行 extracted_text 待 Phase 3 parser
  /// 填充（未跑完时 worker 报 "no content" fail，符合预期，不再用 raw_text
  /// 占位产垃圾页）。
  Future<void> _startIngest(WikiSource src) async {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) {
      _toast('未配置后端凭证');
      return;
    }
    try {
      final task = await repo.client.createIngestTask(
        projectId,
        sourceId: src.id,
        title: src.filename,
      );
      if (!mounted) return;
      context.go(
        '/wiki/p/$projectId/ingest/tasks/${task.taskId}',
      );
    } on Exception catch (e) {
      if (!mounted) return;
      _toast('启动解析失败：$e');
    }
  }

  Future<void> _confirmDelete(
    BuildContext context,
    WidgetRef ref, {
    required String projectId,
    required WikiSource source,
  }) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('删除源文件'),
        content: Text(
          '${source.filename}\n'
          '删除后页面引用关系不会自动清除（B4 dedup 模块处理级联）。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('取消'),
          ),
          FilledButton.tonal(
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(ctx).colorScheme.errorContainer,
              foregroundColor: Theme.of(ctx).colorScheme.onErrorContainer,
            ),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return;
    try {
      await repo.client.deleteSource(projectId, source.id);
      ref.invalidate(sourcesListProvider(projectId));
    } on Exception catch (e) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('删除失败：$e')),
      );
    }
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.onUpload, required this.onRefresh});
  final VoidCallback onUpload;
  final VoidCallback onRefresh;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 48,
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Row(
        children: [
          // 子页头左位 ← (手机形态; 桌面 shrink 不占位, §3.3)。
          const PhoneBackButton(),
          Icon(Icons.upload_file_outlined,
              size: 16, color: BiuTokens.textSecondary),
          const SizedBox(width: 8),
          Text(
            '源文件',
            style: TextStyle(
              color: BiuTokens.text,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const Spacer(),
          IconButton(
            tooltip: '刷新',
            onPressed: onRefresh,
            icon: const Icon(Icons.refresh, size: 16),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
          ),
          const SizedBox(width: 4),
          FilledButton.icon(
            onPressed: onUpload,
            icon: const Icon(Icons.upload, size: 14),
            label: const Text('上传'),
            style: FilledButton.styleFrom(
              backgroundColor: BiuTokens.green,
              padding:
                  const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
              textStyle:
                  const TextStyle(fontSize: 12, fontWeight: FontWeight.w600),
              minimumSize: const Size(0, 32),
            ),
          ),
        ],
      ),
    );
  }
}

class _SourceRow extends StatelessWidget {
  const _SourceRow({
    required this.source,
    required this.onDelete,
    required this.onIngest,
  });
  final WikiSource source;
  final VoidCallback onDelete;
  final VoidCallback onIngest;

  @override
  Widget build(BuildContext context) {
    final s = source;
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
      child: Row(
        children: [
          Icon(_iconFor(s.mime), size: 18, color: BiuTokens.textSecondary),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  s.filename.isEmpty ? s.relPath : s.filename,
                  style: TextStyle(
                    color: BiuTokens.text,
                    fontSize: 13,
                    fontWeight: FontWeight.w500,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                const SizedBox(height: 2),
                Row(
                  children: [
                    _StatusBadge(status: s.parseStatus),
                    const SizedBox(width: 8),
                    if (s.byteSize > 0)
                      Text(
                        _formatBytes(s.byteSize),
                        style: TextStyle(
                          color: BiuTokens.textMuted,
                          fontSize: 11,
                        ),
                      ),
                    if (s.relPath != s.filename && s.relPath.isNotEmpty) ...[
                      const SizedBox(width: 8),
                      Expanded(
                        child: Text(
                          s.relPath,
                          style: TextStyle(
                            color: BiuTokens.textMuted,
                            fontSize: 11,
                          ),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                    ],
                  ],
                ),
                if (s.parseError != null && s.parseError!.isNotEmpty) ...[
                  const SizedBox(height: 4),
                  Text(
                    s.parseError!,
                    style: TextStyle(
                      color: BiuTokens.error,
                      fontSize: 11,
                    ),
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ],
            ),
          ),
          IconButton(
            tooltip: '解析（创建 ingest task）',
            onPressed: onIngest,
            icon: const Icon(Icons.bolt, size: 16),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
          ),
          IconButton(
            tooltip: '删除',
            onPressed: onDelete,
            icon: const Icon(Icons.delete_outline, size: 16),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
          ),
        ],
      ),
    );
  }

  IconData _iconFor(String? mime) {
    if (mime == null) return Icons.description_outlined;
    if (mime.startsWith('image/')) return Icons.image_outlined;
    if (mime.contains('pdf')) return Icons.picture_as_pdf_outlined;
    if (mime.contains('markdown') || mime.contains('text/')) {
      return Icons.text_snippet_outlined;
    }
    if (mime.contains('html')) return Icons.html;
    return Icons.description_outlined;
  }

  String _formatBytes(int b) {
    if (b < 1024) return '$b B';
    if (b < 1024 * 1024) return '${(b / 1024).toStringAsFixed(1)} KB';
    if (b < 1024 * 1024 * 1024) {
      return '${(b / 1024 / 1024).toStringAsFixed(1)} MB';
    }
    return '${(b / 1024 / 1024 / 1024).toStringAsFixed(1)} GB';
  }
}

class _StatusBadge extends StatelessWidget {
  const _StatusBadge({required this.status});
  final String status;

  @override
  Widget build(BuildContext context) {
    final (label, color) = switch (status) {
      'queued' => ('排队中', BiuTokens.textMuted),
      'processing' => ('解析中', BiuTokens.purple),
      'done' => ('已就绪', BiuTokens.success),
      'error' => ('失败', BiuTokens.error),
      _ => (status, BiuTokens.textMuted),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 10,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

class _EmptyView extends StatelessWidget {
  const _EmptyView();
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.upload_file_outlined,
                size: 48, color: BiuTokens.textMuted),
            const SizedBox(height: 16),
            Text(
              '暂无源文件',
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: 14,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 6),
            Text(
              '上传 PDF / Markdown / HTML 等文档，自动 LLM 解析为 wiki 页面',
              textAlign: TextAlign.center,
              style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}

class _ErrorView extends StatelessWidget {
  const _ErrorView({required this.message});
  final String message;
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.error_outline, size: 48, color: BiuTokens.error),
            const SizedBox(height: 16),
            SelectableText(
              message,
              textAlign: TextAlign.center,
              style: TextStyle(color: BiuTokens.error, fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }
}
