// ShareMessageModal v2 —— 把单条消息渲染成 PNG 卡片。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md P0-2。
//
// 设计取舍：
//   * RepaintBoundary + RenderRepaintBoundary.toImage 出 PNG，纯 Flutter API。
//   * 卡片不渲染完整 markdown —— 表格 / 代码块在像素卡里难读。简易剥离 markdown
//     标记后展示纯文本，保留段落与列表项视觉。
//   * 两个出口：保存 PNG（file_selector）+ 复制图片（pasteboard.writeImage）。
//     Web 端 pasteboard 不可用 → 自动隐藏复制按钮。

import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:file_selector/file_selector.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart' hide showAdaptiveDialog;
import 'package:flutter/rendering.dart';
import 'package:pasteboard/pasteboard.dart';

import '../../../../app/theme/brand.dart';
import '../../../../core/ui/adaptive_dialog.dart';

Future<void> showShareMessageDialog(
  BuildContext context, {
  required String content,
  required String? model,
  required DateTime createdAt,
}) {
  return showAdaptiveDialog<void>(
    context: context,
    transparentBackground: true,
    showDragHandle: false,
    builder: (_) => _ShareMessageDialog(
      content: content,
      model: model,
      createdAt: createdAt,
    ),
  );
}

class _ShareMessageDialog extends StatefulWidget {
  const _ShareMessageDialog({
    required this.content,
    required this.model,
    required this.createdAt,
  });

  final String content;
  final String? model;
  final DateTime createdAt;

  @override
  State<_ShareMessageDialog> createState() => _ShareMessageDialogState();
}

class _ShareMessageDialogState extends State<_ShareMessageDialog> {
  final _cardKey = GlobalKey();
  bool _busy = false;

  Future<Uint8List?> _capture() async {
    final boundary =
        _cardKey.currentContext?.findRenderObject() as RenderRepaintBoundary?;
    if (boundary == null) return null;
    final image = await boundary.toImage(pixelRatio: 3.0);
    final byteData = await image.toByteData(format: ui.ImageByteFormat.png);
    return byteData?.buffer.asUint8List();
  }

  Future<void> _savePng() async {
    setState(() => _busy = true);
    final messenger = ScaffoldMessenger.of(context);
    try {
      final bytes = await _capture();
      if (bytes == null) {
        messenger.showSnackBar(const SnackBar(content: Text('截图失败')));
        return;
      }
      final filename =
          'biumind-chat-${widget.createdAt.millisecondsSinceEpoch}.png';
      final loc = await getSaveLocation(
        suggestedName: filename,
        acceptedTypeGroups: const [
          XTypeGroup(label: 'PNG', extensions: ['png']),
        ],
      );
      if (loc == null) return;
      final file = XFile.fromData(bytes, name: filename, mimeType: 'image/png');
      await file.saveTo(loc.path);
      messenger.showSnackBar(
        const SnackBar(
          content: Text('已保存 PNG'),
          duration: Duration(seconds: 2),
        ),
      );
      if (mounted) Navigator.of(context).pop();
    } catch (e) {
      messenger.showSnackBar(SnackBar(content: Text('保存失败: $e')));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _copyImage() async {
    setState(() => _busy = true);
    final messenger = ScaffoldMessenger.of(context);
    try {
      final bytes = await _capture();
      if (bytes == null) {
        messenger.showSnackBar(const SnackBar(content: Text('截图失败')));
        return;
      }
      await Pasteboard.writeImage(bytes);
      messenger.showSnackBar(
        const SnackBar(
          content: Text('已复制图片到剪贴板'),
          duration: Duration(seconds: 2),
        ),
      );
      if (mounted) Navigator.of(context).pop();
    } catch (e) {
      messenger.showSnackBar(SnackBar(content: Text('复制失败: $e')));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return AdaptiveDialogFrame(
      maxWidth: 560,
      maxHeight: double.infinity,
      insetPadding: const EdgeInsets.all(20),
      backgroundColor: Colors.transparent,
      phonePadding: const EdgeInsets.symmetric(horizontal: 16),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          RepaintBoundary(
            key: _cardKey,
            child: _ShareCard(
              content: widget.content,
              model: widget.model,
              createdAt: widget.createdAt,
            ),
          ),
          const SizedBox(height: 12),
          Container(
            decoration: BoxDecoration(
              color: theme.colorScheme.surface,
              borderRadius: BorderRadius.circular(8),
            ),
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
            child: Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                TextButton(
                  onPressed: _busy ? null : () => Navigator.of(context).pop(),
                  child: const Text('取消'),
                ),
                const SizedBox(width: 8),
                if (!kIsWeb)
                  TextButton.icon(
                    icon: const Icon(Icons.copy_outlined, size: 16),
                    label: const Text('复制图片'),
                    onPressed: _busy ? null : _copyImage,
                  ),
                const SizedBox(width: 8),
                FilledButton.icon(
                  icon: const Icon(Icons.download_outlined, size: 16),
                  label: const Text('保存 PNG'),
                  onPressed: _busy ? null : _savePng,
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _ShareCard extends StatelessWidget {
  const _ShareCard({
    required this.content,
    required this.model,
    required this.createdAt,
  });

  final String content;
  final String? model;
  final DateTime createdAt;

  @override
  Widget build(BuildContext context) {
    // 分享卡用 BiuBrand 静态色 — 不跟用户主题切,导出 PNG 跨用户认知一致。
    return Container(
      decoration: BoxDecoration(
        color: BiuBrand.shareSurface,
        borderRadius: BorderRadius.circular(12),
        boxShadow: const [
          BoxShadow(
            color: BiuBrand.shareShadow,
            blurRadius: 24,
            offset: Offset(0, 6),
          ),
        ],
      ),
      padding: const EdgeInsets.all(20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              Container(
                width: 28,
                height: 28,
                decoration: BoxDecoration(
                  gradient: const LinearGradient(
                    colors: BiuBrand.logoGradient,
                    begin: Alignment.topLeft,
                    end: Alignment.bottomRight,
                  ),
                  borderRadius: BorderRadius.circular(8),
                ),
                alignment: Alignment.center,
                child: const Text(
                  'B',
                  style: TextStyle(
                    color: Colors.white,
                    fontWeight: FontWeight.w700,
                    fontSize: 15,
                  ),
                ),
              ),
              const SizedBox(width: 8),
              const Text(
                'BiuMind',
                style: TextStyle(
                  fontSize: 15,
                  fontWeight: FontWeight.w600,
                  color: BiuBrand.shareTextStrong,
                ),
              ),
              const Spacer(),
              if (model != null && model!.isNotEmpty)
                Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 3,
                  ),
                  decoration: BoxDecoration(
                    color: BiuBrand.shareSurfaceMid,
                    borderRadius: BorderRadius.circular(999),
                  ),
                  child: Text(
                    model!,
                    style: const TextStyle(
                      fontSize: 11,
                      color: BiuBrand.shareTextMuted,
                      fontWeight: FontWeight.w500,
                    ),
                  ),
                ),
            ],
          ),
          const SizedBox(height: 16),
          ConstrainedBox(
            constraints: const BoxConstraints(maxHeight: 700),
            child: SingleChildScrollView(
              physics: const NeverScrollableScrollPhysics(),
              child: Text(
                _stripMarkdown(content),
                style: const TextStyle(
                  fontSize: 14.5,
                  height: 1.65,
                  color: BiuBrand.shareTextStrong,
                ),
              ),
            ),
          ),
          const SizedBox(height: 16),
          Container(height: 1, color: BiuBrand.shareDivider),
          const SizedBox(height: 8),
          Row(
            children: [
              Text(
                _fmtTime(createdAt),
                style: const TextStyle(
                  fontSize: 11,
                  color: BiuBrand.shareTextHint,
                ),
              ),
              const Spacer(),
              const Text(
                'biumind.app',
                style: TextStyle(
                  fontSize: 11,
                  color: BiuBrand.shareTextHint,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }

  String _fmtTime(DateTime dt) {
    final l = dt.toLocal();
    String two(int n) => n.toString().padLeft(2, '0');
    return '${l.year}-${two(l.month)}-${two(l.day)} ${two(l.hour)}:${two(l.minute)}';
  }

  /// 极简 markdown 剥离：去掉 #/`/* 等强格式，保留段落与列表项视觉。
  String _stripMarkdown(String s) {
    var t = s;
    t = t.replaceAllMapped(
      RegExp(r'```[a-zA-Z0-9_-]*\n([\s\S]*?)```', multiLine: true),
      (m) => m.group(1) ?? '',
    );
    t = t.replaceAllMapped(RegExp(r'`([^`]+)`'), (m) => m.group(1) ?? '');
    t = t.replaceAllMapped(RegExp(r'^#{1,6}\s+', multiLine: true), (_) => '');
    t = t.replaceAllMapped(RegExp(r'\*\*([^*]+)\*\*'), (m) => m.group(1) ?? '');
    t = t.replaceAllMapped(RegExp(r'\*([^*]+)\*'), (m) => m.group(1) ?? '');
    t = t.replaceAllMapped(
      RegExp(r'\[([^\]]+)\]\([^)]+\)'),
      (m) => m.group(1) ?? '',
    );
    return t.trim();
  }
}
