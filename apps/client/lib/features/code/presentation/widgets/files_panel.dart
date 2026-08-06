// Files Tab — 显示当前任务的 artifact 列表 (L1 元数据 + L2 preview)。点击行查看
// preview (base64 jpeg / diff text)。产物 100% 本地(D4/Code-I4):L3 云上传/下载
// (artifacts-sync)已废弃移除(Code-I6),本面板仅本地展示。

import 'dart:convert' show base64Decode;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../data/code_task_artifacts_dao.dart';
import '../../domain/artifact.dart';
import '../../domain/code_task.dart';

class FilesPanel extends ConsumerWidget {
  const FilesPanel({super.key, required this.task});
  final CodeTask task;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final dao = ref.watch(codeTaskArtifactsDaoProvider);
    return StreamBuilder<List<Artifact>>(
      stream: dao.watchByTask(task.id),
      builder: (context, snap) {
        final list = snap.data ?? const <Artifact>[];
        if (list.isEmpty) {
          return _EmptyFiles(
            running: task.status == CodeTaskStatus.running ||
                task.status == CodeTaskStatus.queued,
          );
        }
        return ListView.separated(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 16),
          itemCount: list.length,
          separatorBuilder: (_, _) => const SizedBox(height: 6),
          itemBuilder: (ctx, i) => _ArtifactRow(art: list[i], task: task),
        );
      },
    );
  }
}

class _EmptyFiles extends StatelessWidget {
  const _EmptyFiles({required this.running});
  final bool running;
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(
            running ? Icons.hourglass_top_rounded : Icons.folder_open_rounded,
            size: 36,
            color: BiuTokens.textMuted,
          ),
          const SizedBox(height: 10),
          Text(
            running ? '任务运行中, 完成后会自动收集产物' : '未生成产物',
            style: TextStyle(fontSize: 13, color: BiuTokens.textSecondary),
          ),
          const SizedBox(height: 4),
          Text(
            '任务 done 时自动扫 worktree → 本地收集产物元数据',
            style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
          ),
        ],
      ),
    );
  }
}

class _ArtifactRow extends StatelessWidget {
  const _ArtifactRow({required this.art, required this.task});
  final Artifact art;
  final CodeTask task;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: BiuTokens.bg,
      borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      child: InkWell(
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        onTap: art.previewDataB64 == null
            ? null
            : () => _showPreview(context, art),
        child: Container(
          padding: const EdgeInsets.symmetric(
            horizontal: BiuTokens.space3,
            vertical: 8,
          ),
          decoration: BoxDecoration(
            border: Border.all(color: BiuTokens.borderSubtle),
            borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
          ),
          child: Row(
            children: [
              _KindIcon(kind: art.kind, op: art.op),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      art.relPath,
                      style: const TextStyle(
                        fontSize: 12.5,
                        fontFamily: 'SF Mono',
                        fontWeight: FontWeight.w500,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                    const SizedBox(height: 2),
                    Row(
                      children: [
                        Text(
                          _formatSize(art.sizeBytes),
                          style: TextStyle(
                            fontSize: 10.5,
                            fontFamily: 'SF Mono',
                            color: BiuTokens.textMuted,
                          ),
                        ),
                        const SizedBox(width: 8),
                        Text(
                          art.sha256.isEmpty
                              ? art.op.label
                              : '${art.op.label} · ${art.sha256.substring(0, 7)}',
                          style: TextStyle(
                            fontSize: 10.5,
                            fontFamily: 'SF Mono',
                            color: BiuTokens.textMuted,
                          ),
                          overflow: TextOverflow.ellipsis,
                        ),
                        if (art.previewSummary != null) ...[
                          const SizedBox(width: 8),
                          Flexible(
                            child: Text(
                              art.previewSummary!,
                              style: TextStyle(
                                fontSize: 10.5,
                                color: BiuTokens.textSecondary,
                              ),
                              overflow: TextOverflow.ellipsis,
                            ),
                          ),
                        ],
                      ],
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 8),
              if (_isSensitive(art)) ...[
                const _SensitiveChip(),
                const SizedBox(width: 6),
              ],
              _PreviewLevelChip(level: art.previewLevel),
            ],
          ),
        ),
      ),
    );
  }

  static bool _isSensitive(Artifact a) =>
      a.previewSummary != null && a.previewSummary!.contains('敏感');

  void _showPreview(BuildContext context, Artifact art) {
    showDialog<void>(
      context: context,
      builder: (_) => _PreviewDialog(art: art),
    );
  }

  static String _formatSize(int bytes) {
    if (bytes <= 0) return '0 B';
    if (bytes < 1024) return '$bytes B';
    if (bytes < 1024 * 1024) return '${(bytes / 1024).toStringAsFixed(1)} KB';
    if (bytes < 1024 * 1024 * 1024) {
      return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MB';
    }
    return '${(bytes / (1024 * 1024 * 1024)).toStringAsFixed(2)} GB';
  }
}

class _KindIcon extends StatelessWidget {
  const _KindIcon({required this.kind, required this.op});
  final ArtifactKind kind;
  final ArtifactOp op;

  @override
  Widget build(BuildContext context) {
    final (icon, color) = switch (kind) {
      ArtifactKind.codeFile => (Icons.code_rounded, BiuTokens.purple),
      ArtifactKind.image => (Icons.image_outlined, Colors.teal),
      ArtifactKind.document => (Icons.description_outlined, Colors.blue),
      ArtifactKind.audio => (Icons.audio_file_outlined, Colors.orange),
      ArtifactKind.video => (Icons.movie_outlined, Colors.deepPurple),
      ArtifactKind.dataset => (Icons.table_chart_outlined, Colors.green),
      ArtifactKind.binary => (Icons.insert_drive_file_outlined, BiuTokens.textMuted),
    };
    final opColor = switch (op) {
      ArtifactOp.created => BiuTokens.green,
      ArtifactOp.modified => BiuTokens.purple,
      ArtifactOp.deleted => Colors.red,
    };
    return Stack(
      clipBehavior: Clip.none,
      children: [
        Icon(icon, size: 18, color: color),
        Positioned(
          right: -3,
          bottom: -3,
          child: Container(
            width: 8,
            height: 8,
            decoration: BoxDecoration(
              color: opColor,
              shape: BoxShape.circle,
              border: Border.all(color: BiuTokens.bg, width: 1),
            ),
          ),
        ),
      ],
    );
  }
}

class _SensitiveChip extends StatelessWidget {
  const _SensitiveChip();
  @override
  Widget build(BuildContext context) {
    return Tooltip(
      message: '文件名匹配敏感模式 (.env / *.pem / id_rsa* 等), L2 preview 已跳过',
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
        decoration: BoxDecoration(
          color: Colors.red.withValues(alpha: 0.12),
          borderRadius: BorderRadius.circular(3),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.lock_outline_rounded, size: 10, color: Colors.red.shade700),
            const SizedBox(width: 3),
            Text(
              '敏感',
              style: TextStyle(
                fontSize: 10,
                fontWeight: FontWeight.w600,
                color: Colors.red.shade700,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _PreviewLevelChip extends StatelessWidget {
  const _PreviewLevelChip({required this.level});
  final int level;

  @override
  Widget build(BuildContext context) {
    final (label, color) = switch (level) {
      2 => ('L2 ◔', BiuTokens.purple),
      _ => ('L1 ·', BiuTokens.textMuted),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(3),
      ),
      child: Text(
        label,
        style: TextStyle(
          fontSize: 10,
          fontFamily: 'SF Mono',
          fontWeight: FontWeight.w600,
          color: color,
        ),
      ),
    );
  }
}

/// Dialog 显示 L2 preview (base64 jpeg / 截断的 diff text)。P2.B 后续才会
/// 生成 preview 数据; 目前点开几乎都是空 (这个 widget 是为后续接口预留)。
class _PreviewDialog extends StatelessWidget {
  const _PreviewDialog({required this.art});
  final Artifact art;

  @override
  Widget build(BuildContext context) {
    Widget body;
    final raw = art.previewDataB64;
    if (raw == null) {
      body = const Center(child: Text('无 preview'));
    } else if ((art.previewMimeType ?? '').startsWith('image/')) {
      try {
        body = InteractiveViewer(
          child: Image.memory(base64Decode(raw)),
        );
      } catch (_) {
        body = const Center(child: Text('preview 损坏'));
      }
    } else {
      // 文本类 (diff / 文档摘要) — 直接 utf8 显示
      String text = '';
      try {
        text = String.fromCharCodes(base64Decode(raw));
      } catch (_) {
        text = '(无法解码)';
      }
      body = SingleChildScrollView(
        padding: const EdgeInsets.all(12),
        child: SelectableText(
          text,
          style: const TextStyle(fontSize: 12, fontFamily: 'SF Mono', height: 1.5),
        ),
      );
    }

    return Dialog(
      child: SizedBox(
        width: 720,
        height: 540,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
              decoration: BoxDecoration(
                border: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
              ),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      art.relPath,
                      style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  IconButton(
                    onPressed: () => Navigator.of(context).pop(),
                    icon: const Icon(Icons.close_rounded, size: 18),
                  ),
                ],
              ),
            ),
            Expanded(child: body),
          ],
        ),
      ),
    );
  }
}
