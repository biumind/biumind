// SelectionActionBarV2 —— P0-3 多选模式底部操作 bar。
// 选中后浮起：复制（合并 markdown） / 导出 MD / 删除 / 全选 / 取消。

import 'dart:convert' show utf8;

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../../l10n/app_localizations.dart';
import '../../application/chat_controller.dart';
import '../../application/selection_mode_controller.dart';
import '../../domain/chat_models.dart';

class SelectionActionBarV2 extends ConsumerWidget {
  const SelectionActionBarV2({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final mode = ref.watch(selectionModeProvider);
    if (!mode.active) return const SizedBox.shrink();
    final tid = mode.threadId;
    if (tid == null) return const SizedBox.shrink();
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final count = mode.count;
    return Material(
      elevation: 4,
      color: theme.colorScheme.surface,
      child: Container(
        decoration: BoxDecoration(
          border: Border(top: BorderSide(color: theme.colorScheme.outlineVariant)),
        ),
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        child: Row(
          children: [
            Text(
              l.chatV2SelectionSelectedCount(count),
              style: theme.textTheme.bodyMedium?.copyWith(
                fontWeight: FontWeight.w500,
              ),
            ),
            const SizedBox(width: 12),
            TextButton.icon(
              icon: const Icon(Icons.select_all, size: 16),
              label: Text(l.chatV2SelectionSelectAll),
              onPressed: () => _selectAll(ref, tid),
            ),
            const Spacer(),
            TextButton.icon(
              icon: const Icon(Icons.copy_outlined, size: 16),
              label: Text(l.chatV2SelectionCopy),
              onPressed: count == 0 ? null : () => _copy(context, ref, tid),
            ),
            TextButton.icon(
              icon: const Icon(Icons.translate, size: 16),
              label: Text(l.chatV2SelectionTranslate),
              onPressed: count == 0
                  ? null
                  : () => _translate(context, ref, tid),
            ),
            TextButton.icon(
              icon: const Icon(Icons.download_outlined, size: 16),
              label: Text(l.chatV2SelectionExportMd),
              onPressed: count == 0 ? null : () => _export(context, ref, tid),
            ),
            TextButton.icon(
              icon: const Icon(Icons.delete_outline, size: 16),
              label: Text(l.chatV2SelectionDelete),
              style: TextButton.styleFrom(
                foregroundColor: theme.colorScheme.error,
              ),
              onPressed: count == 0 ? null : () => _delete(context, ref),
            ),
            TextButton(
              onPressed: () =>
                  ref.read(selectionModeProvider.notifier).exit(),
              child: Text(l.chatV2SelectionCancel),
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _selectAll(WidgetRef ref, String threadId) async {
    final msgs = await ref.read(messagesProvider(threadId).future);
    ref.read(selectionModeProvider.notifier).selectAll(
          msgs
              .where((m) =>
                  m.status == MessageStatus.completed &&
                  m.role != MessageRole.toolResult)
              .map((m) => m.id),
        );
  }

  Future<void> _copy(
      BuildContext context, WidgetRef ref, String threadId) async {
    final messenger = ScaffoldMessenger.of(context);
    final l = AppLocalizations.of(context)!;
    final mode = ref.read(selectionModeProvider);
    final msgs = await ref.read(messagesProvider(threadId).future);
    final selected = msgs.where((m) => mode.contains(m.id)).toList()
      ..sort((a, b) => a.seq.compareTo(b.seq));
    final buf = StringBuffer();
    for (var i = 0; i < selected.length; i++) {
      final m = selected[i];
      final tag = switch (m.role) {
        MessageRole.user => '👤 User',
        MessageRole.assistant => '🤖 Assistant',
        MessageRole.system => '⚙️ System',
        MessageRole.toolResult => '🔧 Tool',
      };
      buf.writeln('### $tag');
      buf.writeln();
      buf.writeln(m.assembledText.trim());
      if (i < selected.length - 1) {
        buf.writeln();
        buf.writeln('---');
        buf.writeln();
      }
    }
    await Clipboard.setData(ClipboardData(text: buf.toString()));
    messenger.showSnackBar(SnackBar(
      content: Text(l.chatV2SelectionCopiedCount(selected.length)),
      duration: const Duration(seconds: 2),
    ));
  }

  Future<void> _translate(
      BuildContext context, WidgetRef ref, String threadId) async {
    final messenger = ScaffoldMessenger.of(context);
    final l = AppLocalizations.of(context)!;
    final mode = ref.read(selectionModeProvider);
    final msgs = await ref.read(messagesProvider(threadId).future);
    final selected = msgs.where((m) => mode.contains(m.id)).toList()
      ..sort((a, b) => a.seq.compareTo(b.seq));
    if (selected.isEmpty) return;
    // 拼成纯文本，每条之间空行隔开。
    final text = selected
        .map((m) => m.assembledText.trim())
        .where((s) => s.isNotEmpty)
        .join('\n\n');
    if (text.isEmpty) return;
    // Google Translate URL 长度上限 ~5000 字符；超出截断 + toast 提示。
    var src = text;
    var truncated = false;
    if (src.length > 4500) {
      src = src.substring(0, 4500);
      truncated = true;
    }
    final url = Uri.parse(
      'https://translate.google.com/?sl=auto&tl=zh-CN&text=${Uri.encodeComponent(src)}&op=translate',
    );
    try {
      await launchUrl(url, mode: LaunchMode.externalApplication);
      if (truncated) {
        messenger.showSnackBar(SnackBar(
          content: Text(l.chatV2SelectionTruncated),
          duration: const Duration(seconds: 2),
        ));
      }
    } catch (e) {
      messenger.showSnackBar(SnackBar(
          content: Text(l.chatV2SelectionTranslateFailed('$e'))));
    }
  }

  Future<void> _export(
      BuildContext context, WidgetRef ref, String threadId) async {
    final messenger = ScaffoldMessenger.of(context);
    final l = AppLocalizations.of(context)!;
    try {
      final mode = ref.read(selectionModeProvider);
      final msgs = await ref.read(messagesProvider(threadId).future);
      final selected = msgs.where((m) => mode.contains(m.id)).toList()
        ..sort((a, b) => a.seq.compareTo(b.seq));
      final thread = await ref
          .read(chatControllerDepsProvider)
          .repo
          .getThread(threadId);
      final md = _renderThreadMd(thread, selected, l);
      final ts = DateTime.now();
      String two(int n) => n.toString().padLeft(2, '0');
      final stamp =
          '${ts.year}${two(ts.month)}${two(ts.day)}-${two(ts.hour)}${two(ts.minute)}${two(ts.second)}';
      final base = (thread?.title.trim().isNotEmpty == true)
          ? thread!.title.trim()
          : 'biumind-chat';
      final sanitized =
          base.replaceAll(RegExp(r'[\\/:*?"<>|\n\r\t]'), '-');
      final clipped = sanitized.length > 60
          ? sanitized.substring(0, 60)
          : sanitized;
      final filename = '$clipped-$stamp.md';
      final loc = await getSaveLocation(
        suggestedName: filename,
        acceptedTypeGroups: const [
          XTypeGroup(label: 'Markdown', extensions: ['md']),
        ],
      );
      if (loc == null) return;
      final file = XFile.fromData(
        Uint8List.fromList(utf8.encode(md)),
        name: filename,
        mimeType: 'text/markdown',
      );
      await file.saveTo(loc.path);
      messenger.showSnackBar(SnackBar(
        content: Text(l.chatV2SelectionExportedCount(selected.length)),
        duration: const Duration(seconds: 2),
      ));
    } catch (e) {
      messenger.showSnackBar(
          SnackBar(content: Text(l.chatV2ExportFailed('$e'))));
    }
  }

  Future<void> _delete(BuildContext context, WidgetRef ref) async {
    final l = AppLocalizations.of(context)!;
    final mode = ref.read(selectionModeProvider);
    final count = mode.count;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(l.chatV2SelectionDeleteTitle),
        content: Text(l.chatV2SelectionDeleteBody(count)),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: Text(l.chatV2DialogCancel),
          ),
          TextButton(
            style: TextButton.styleFrom(
              foregroundColor: Theme.of(ctx).colorScheme.error,
            ),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: Text(l.chatV2DialogDelete),
          ),
        ],
      ),
    );
    if (ok != true) return;
    final repo = ref.read(chatControllerDepsProvider).repo;
    await repo.deleteMessages(mode.ids);
    ref.read(selectionModeProvider.notifier).exit();
  }

  String _renderThreadMd(
      Thread? thread, List<Message> messages, AppLocalizations l) {
    final buf = StringBuffer();
    final title = thread?.title.trim().isNotEmpty == true
        ? thread!.title
        : l.chatV2SelectionMdUnnamed;
    buf.writeln('# $title');
    buf.writeln();
    buf.write('> **Model**: ');
    buf.writeln(thread?.model ?? l.chatV2SelectionMdModelUnset);
    buf.write('> **Messages**: ');
    buf.writeln(messages.length);
    buf.writeln();
    buf.writeln('---');
    buf.writeln();
    for (final m in messages) {
      final tag = switch (m.role) {
        MessageRole.user => '👤 User',
        MessageRole.assistant =>
          m.model != null ? '🤖 Assistant · ${m.model}' : '🤖 Assistant',
        MessageRole.system => '⚙️ System',
        MessageRole.toolResult => '🔧 Tool',
      };
      buf.writeln('## $tag');
      buf.writeln();
      final text = m.assembledText.trim();
      buf.writeln(text.isEmpty ? '_(empty)_' : text);
      buf.writeln();
      buf.writeln('---');
      buf.writeln();
    }
    return buf.toString();
  }
}
