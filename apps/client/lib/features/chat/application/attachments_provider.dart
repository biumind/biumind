// ComposerAttachments —— 当前 thread 的 composer 附件（图片）状态机。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 附件 UI）。
//
// 模型：
//   * Attachment(id, name, mime, bytes, status, error)
//   * status: 'ready' | 'failed'（同步从内存数据创建，无网络上传环节，
//     bytes 已在内存里 → 默认 'ready'；保留 status 字段为后续接 brain
//     时支持 uploading 留位置）
//
// 存储边界：
//   * 仅内存 —— 切 thread / 关页面后清空。重要内容用户应当尽快发送。
//   * 没有 SharedPreferences 持久化（base64 图片体积大，过夜留太重）。

import 'dart:typed_data';

import 'package:flutter_riverpod/flutter_riverpod.dart';

enum AttachmentStatus { ready, failed }

class Attachment {
  Attachment({
    required this.id,
    required this.name,
    required this.mime,
    required this.bytes,
    this.status = AttachmentStatus.ready,
    this.error,
  });

  final String id;
  final String name;
  /// MIME type, eg 'image/png'，'image/jpeg'。当前只支持 image/*。
  final String mime;
  final Uint8List bytes;
  final AttachmentStatus status;
  final String? error;

  int get sizeBytes => bytes.length;
  bool get isImage => mime.startsWith('image/');
}

class ComposerAttachmentsNotifier extends StateNotifier<List<Attachment>> {
  ComposerAttachmentsNotifier() : super(const []);

  void add(Attachment a) {
    state = [...state, a];
  }

  void remove(String id) {
    state = state.where((a) => a.id != id).toList(growable: false);
  }

  void clear() {
    if (state.isEmpty) return;
    state = const [];
  }

  /// 是否有正在上传的（未来 brain 端真支持时用）。当前 sync 添加不会出现。
  bool get hasInFlight =>
      state.any((a) => a.status != AttachmentStatus.ready && a.status != AttachmentStatus.failed);
}

/// per-thread family —— 切 thread 不会串草稿，发送清掉只清当前 thread。
final composerAttachmentsProvider = StateNotifierProvider.autoDispose
    .family<ComposerAttachmentsNotifier, List<Attachment>, String>(
  (ref, threadId) => ComposerAttachmentsNotifier(),
);
