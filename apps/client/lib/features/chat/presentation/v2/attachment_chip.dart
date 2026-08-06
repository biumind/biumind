// AttachmentChipV2 —— composer 输入框上方一排显示已附加的图片缩略 + 删除。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 附件 UI）。
//
// 缩略图直接 Image.memory(bytes)，size 限制 56x56。文件名 + 大小（KB/MB）
// 在缩略图右侧；右上角小 ✕ 删除该项。

import 'package:flutter/material.dart';

import '../../../../l10n/app_localizations.dart';
import '../../application/attachments_provider.dart';

class AttachmentChipV2 extends StatelessWidget {
  const AttachmentChipV2({
    super.key,
    required this.attachment,
    required this.onRemove,
  });

  final Attachment attachment;
  final VoidCallback onRemove;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final failed = attachment.status == AttachmentStatus.failed;
    return Container(
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerLow,
        border: Border.all(
          color: failed
              ? theme.colorScheme.error
              : theme.colorScheme.outlineVariant,
        ),
        borderRadius: BorderRadius.circular(8),
      ),
      padding: const EdgeInsets.fromLTRB(4, 4, 8, 4),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          ClipRRect(
            borderRadius: BorderRadius.circular(6),
            child: attachment.isImage
                ? Image.memory(
                    attachment.bytes,
                    width: 36,
                    height: 36,
                    fit: BoxFit.cover,
                    errorBuilder: (_, _, _) => _GenericIcon(
                      icon: Icons.broken_image_outlined,
                      tone: theme.colorScheme.error,
                    ),
                  )
                : _GenericIcon(
                    icon: Icons.insert_drive_file_outlined,
                    tone: theme.colorScheme.primary,
                  ),
          ),
          const SizedBox(width: 8),
          Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              ConstrainedBox(
                constraints: const BoxConstraints(maxWidth: 140),
                child: Text(
                  attachment.name,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: theme.textTheme.labelMedium,
                ),
              ),
              Text(
                _fmtSize(attachment.sizeBytes),
                style: theme.textTheme.labelSmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                ),
              ),
              if (failed && attachment.error != null)
                Text(
                  attachment.error!,
                  style: theme.textTheme.labelSmall?.copyWith(
                    color: theme.colorScheme.error,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
            ],
          ),
          const SizedBox(width: 4),
          IconButton(
            icon: const Icon(Icons.close, size: 14),
            tooltip: AppLocalizations.of(context)!.chatV2AttachRemove,
            onPressed: onRemove,
            visualDensity: VisualDensity.compact,
            padding: EdgeInsets.zero,
            constraints:
                const BoxConstraints(minWidth: 24, minHeight: 24),
          ),
        ],
      ),
    );
  }

  static String _fmtSize(int bytes) {
    if (bytes < 1024) return '${bytes}B';
    if (bytes < 1024 * 1024) return '${(bytes / 1024).toStringAsFixed(1)}KB';
    return '${(bytes / 1024 / 1024).toStringAsFixed(1)}MB';
  }
}

class _GenericIcon extends StatelessWidget {
  const _GenericIcon({required this.icon, required this.tone});
  final IconData icon;
  final Color tone;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 36,
      height: 36,
      color: tone.withValues(alpha: 0.08),
      alignment: Alignment.center,
      child: Icon(icon, size: 18, color: tone),
    );
  }
}
