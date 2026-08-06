// 编码任务产物 (Artifact) 领域模型 — L1 元数据 + 可选 L2 preview。
//
// 100% 本地(D4 / Code-I4):产物不上云。原 L3 cloud 上传(artifacts-sync)已废弃
// 移除(Code-I6)。仅 Drift 本地持久化,供产物面板展示。

import 'package:flutter/foundation.dart';

/// Artifact 大类 — 决定 L2 preview 生成策略 + UI 渲染模板。
enum ArtifactKind {
  codeFile,   // .dart .go .py 等; preview = git diff text
  image,      // .png .jpg .webp; preview = 256×256 缩略图
  document,   // .md .txt .pdf .docx; preview = 第一段
  audio,      // .mp3 .wav; preview = waveform (P2)
  video,      // .mp4 .mov; preview = 首帧缩略图 (P2)
  dataset,    // .csv .xlsx .parquet; preview = 行列摘要 (P2)
  binary,     // 兜底 — preview = null
}

extension ArtifactKindLabel on ArtifactKind {
  String get label => switch (this) {
        ArtifactKind.codeFile => 'code',
        ArtifactKind.image => 'image',
        ArtifactKind.document => 'document',
        ArtifactKind.audio => 'audio',
        ArtifactKind.video => 'video',
        ArtifactKind.dataset => 'dataset',
        ArtifactKind.binary => 'binary',
      };
}

/// 文件级别变更类型 (跟 git diff status / fs scan 对齐)。
enum ArtifactOp { created, modified, deleted }

extension ArtifactOpLabel on ArtifactOp {
  String get label => switch (this) {
        ArtifactOp.created => 'create',
        ArtifactOp.modified => 'modify',
        ArtifactOp.deleted => 'delete',
      };
}

@immutable
class Artifact {
  const Artifact({
    required this.id,
    required this.taskId,
    required this.kind,
    required this.relPath,
    required this.sizeBytes,
    required this.sha256,
    required this.op,
    required this.createdAt,
    this.mimeType,
    this.previewSummary,
    this.previewDataB64,
    this.previewMimeType,
  });

  /// uuid v4
  final String id;
  final String taskId;
  final ArtifactKind kind;

  /// 相对 worktree 的 POSIX 路径 (CSY5 — 不存绝对路径)。
  final String relPath;

  /// MIME 类型 (推断, 可能 null — 比如未知二进制)。
  final String? mimeType;

  final int sizeBytes;

  /// 内容哈希 — 跨设备 dedup 主键 (server 端按 user_id+sha256 去重 cloud_file_id)。
  final String sha256;
  final ArtifactOp op;

  // ─── L2 preview (可选) ─────────────────────────────────
  /// 一行摘要, 列表用 ("+12 -3" / "256×256 jpeg" / "1200 字")。
  final String? previewSummary;

  /// base64 编码的预览主体 (缩略图 jpeg / 截断的 diff text)。≤ 200 KB。
  /// L2 整体未生成时为 null (P1.B 阶段普遍 null, P2 才填)。
  final String? previewDataB64;
  final String? previewMimeType;

  final DateTime createdAt;

  /// 本地展示层级 (1 = 仅 L1 元数据; 2 = 含 L2 preview)。产物面板 chip 用。
  /// 云上传(L3)已废弃,不再有第 3 级。
  int get previewLevel => previewDataB64 != null ? 2 : 1;

  Artifact copyWith({
    String? previewSummary,
    String? previewDataB64,
    String? previewMimeType,
  }) =>
      Artifact(
        id: id,
        taskId: taskId,
        kind: kind,
        relPath: relPath,
        sizeBytes: sizeBytes,
        sha256: sha256,
        op: op,
        createdAt: createdAt,
        mimeType: mimeType,
        previewSummary: previewSummary ?? this.previewSummary,
        previewDataB64: previewDataB64 ?? this.previewDataB64,
        previewMimeType: previewMimeType ?? this.previewMimeType,
      );
}
