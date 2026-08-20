// L2 preview generator — 给 artifact 生成缩略图 / diff 摘要 / 文档摘要,
// 让远端设备能"看一眼"这个产物是什么。
//
// 设计文档 docs/BiuMind-Code-Artifacts-Sync-Design.md §4.1。
//
// 产物分类策略:
//   image     → 256×256 jpeg q=75
//   codeFile  → git diff base..HEAD <file> 文本, 截断到 100 KB
//   document  → 第一段 (≤ 500 字) + 字数摘要
//   audio / video / dataset / binary → P2 阶段后续
//
// 整体 budget 200 KB (base64 encode 后体积 +33%, 实际原文 ≤ 150 KB)。
// 失败仅 print 日志, 不阻塞 L1 元数据收集 (preview 是 nice-to-have)。

import 'dart:async';
import 'dart:convert' show base64Encode, utf8;
import 'dart:io';

import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:image/image.dart' as img;

import '../data/code_bridge_client.dart';
import '../domain/artifact.dart';

class PreviewBundle {
  const PreviewBundle({this.summary, this.dataB64, this.mimeType});
  final String? summary;
  final String? dataB64;
  final String? mimeType;

  bool get isEmpty => summary == null && dataB64 == null;
}

class PreviewGenerator {
  PreviewGenerator({
    required this.worktreePath,
    this.bridge,
    this.budgetBytes = 150 * 1024, // base64 后 ≈ 200KB
    this.thumbSize = 256,
    this.jpegQuality = 75,
  });

  /// daemon 连接(codeFile diff 经它,不再 spawn git)。null(单测/无 daemon)时
  /// codeFile 预览回退到文件前 N 行。
  final CodeBridgeClient? bridge;

  final String worktreePath;
  final int budgetBytes;
  final int thumbSize;
  final int jpegQuality;

  /// 给一个 artifact 生成 preview。`baseCommit` 用于 codeFile 跑 git diff。
  /// 任意失败都返回 const PreviewBundle() (全 null) — caller 把空结果当
  /// "未生成", L1 元数据不受影响。
  ///
  /// CSY6: 敏感文件名 (.env / *.pem / id_rsa* / *.key 等) 即便 .gitignore
  /// 没拦, 这里强制跳过 L2 — 只留 summary='(敏感, preview 已跳过)' 让
  /// UI 红色 chip 提示用户。L3 上传走另一道二次确认 (P2.C UI 时接)。
  Future<PreviewBundle> generate(Artifact art, {String? baseCommit}) async {
    if (isSensitivePath(art.relPath)) {
      return const PreviewBundle(summary: '(敏感, preview 已跳过)');
    }
    try {
      switch (art.kind) {
        case ArtifactKind.image:
          // await 必须：不 await 直接 return Future 会逃出 try，
          // 异步异常进不了下面的 catch（lint unawaited_return_in_try_block）。
          return await _imagePreview(art);
        case ArtifactKind.codeFile:
          if (baseCommit == null) return const PreviewBundle();
          return await _codeDiffPreview(art, baseCommit: baseCommit);
        case ArtifactKind.document:
          return await _documentPreview(art);
        case ArtifactKind.audio:
        case ArtifactKind.video:
        case ArtifactKind.dataset:
        case ArtifactKind.binary:
          // 仅给一个体积 summary, 没 dataB64
          return PreviewBundle(summary: _sizeSummary(art));
      }
    } catch (e) {
      // ignore: avoid_print
      print('[code/preview] ${art.relPath} err=$e');
      return const PreviewBundle();
    }
  }

  /// 敏感文件名匹配 (CSY6)。涵盖常见 secret 容器:
  /// - dotenv: .env / .env.local / .env.production
  /// - private keys: *.pem / *.key / id_rsa* / id_ed25519* / id_ecdsa*
  /// - cloud creds: *.aws/credentials / .aws/config / gcloud / azure
  /// - kube/docker: kubeconfig / .kube/config / .dockercfg
  /// - 通用: *_token / *.secret / *.password / .htpasswd
  ///
  /// 命中策略保守宽: 用户在 settings 加 allowlist 明确放行后再生成 preview
  /// (P2.C 实施)。
  static bool isSensitivePath(String relPath) {
    final p = relPath.toLowerCase();
    final base = p.split('/').last;

    // dotenv 家族
    if (base == '.env' || base.startsWith('.env.')) return true;

    // SSH / GPG private keys
    if (base.startsWith('id_rsa') ||
        base.startsWith('id_ed25519') ||
        base.startsWith('id_ecdsa') ||
        base.startsWith('id_dsa')) {
      return true;
    }
    if (base.endsWith('.pem') ||
        base.endsWith('.key') ||
        base.endsWith('.p12') ||
        base.endsWith('.pfx') ||
        base.endsWith('.jks') ||
        base.endsWith('.keystore') ||
        base.endsWith('.gpg')) {
      return true;
    }

    // Cloud credential locations
    if (p.contains('.aws/credentials') ||
        p.contains('.aws/config') ||
        p.contains('.gcloud/') ||
        p.contains('.azure/') ||
        p.contains('.kube/config') ||
        p == 'kubeconfig' ||
        base == 'kubeconfig' ||
        base == '.dockercfg' ||
        base == '.netrc' ||
        base == '.htpasswd') {
      return true;
    }

    // 通用关键字 (启发式; 可能误伤但偏保守)
    if (base.endsWith('.secret') ||
        base.endsWith('.password') ||
        base.contains('_token') ||
        base.contains('-token') ||
        base.contains('apikey') ||
        base.contains('api_key')) {
      return true;
    }

    return false;
  }

  // ─── image ────────────────────────────────────────────

  Future<PreviewBundle> _imagePreview(Artifact art) async {
    final f = File('$worktreePath/${art.relPath}');
    if (!await f.exists()) return const PreviewBundle();
    if (art.sizeBytes > 32 * 1024 * 1024) {
      // 32MB+ 不解码 (内存压力), 仅给体积 summary
      return PreviewBundle(summary: _sizeSummary(art));
    }
    final bytes = await f.readAsBytes();
    final decoded = img.decodeImage(bytes);
    if (decoded == null) return PreviewBundle(summary: _sizeSummary(art));
    final w = decoded.width, h = decoded.height;

    // 等比缩放到 thumbSize 长边
    img.Image thumb;
    if (w >= h) {
      thumb = img.copyResize(decoded, width: thumbSize);
    } else {
      thumb = img.copyResize(decoded, height: thumbSize);
    }
    final jpg = img.encodeJpg(thumb, quality: jpegQuality);
    if (jpg.length > budgetBytes) {
      // 还是太大, 降画质重压一次
      final smaller = img.encodeJpg(thumb, quality: 50);
      if (smaller.length > budgetBytes) {
        return PreviewBundle(summary: '$w×$h ${art.mimeType ?? "image"}');
      }
      return PreviewBundle(
        summary: '$w×$h → ${thumb.width}×${thumb.height}',
        dataB64: base64Encode(smaller),
        mimeType: 'image/jpeg',
      );
    }
    return PreviewBundle(
      summary: '$w×$h → ${thumb.width}×${thumb.height}',
      dataB64: base64Encode(jpg),
      mimeType: 'image/jpeg',
    );
  }

  // ─── code diff ────────────────────────────────────────

  Future<PreviewBundle> _codeDiffPreview(
    Artifact art, {
    required String baseCommit,
  }) async {
    // 已删除 (op=deleted) 文件: git diff 拿不到 + 文件不存在 → 跳过
    if (art.op == ArtifactOp.deleted) {
      return const PreviewBundle(summary: '(deleted)');
    }
    // 无 daemon 连接(单测/离线)→ 退回文件前 N 行预览。
    if (bridge == null) {
      return _firstLinesPreview(art, maxLines: 80);
    }
    String raw;
    try {
      raw = await bridge!.gitRangeFileDiff(worktreePath, baseCommit, art.relPath);
    } catch (_) {
      return const PreviewBundle();
    }
    if (raw.trim().isEmpty) {
      // 可能是 untracked + 未 add 进 git, diff 拿不到 — 用文件前 N 行兜底
      return _firstLinesPreview(art, maxLines: 80);
    }

    // 截断到 budget — 大致按字节, utf8 多字节字符不裁中间
    final encoded = utf8.encode(raw);
    final clipped =
        encoded.length <= budgetBytes ? encoded : encoded.sublist(0, budgetBytes);
    final clippedText = utf8.decode(clipped, allowMalformed: true);
    final summary = _diffStatSummary(raw);
    return PreviewBundle(
      summary: summary,
      dataB64: base64Encode(utf8.encode(clippedText)),
      mimeType: 'text/x-diff',
    );
  }

  Future<PreviewBundle> _firstLinesPreview(
    Artifact art, {
    int maxLines = 50,
  }) async {
    final f = File('$worktreePath/${art.relPath}');
    if (!await f.exists()) return const PreviewBundle();
    try {
      final txt = await f.readAsString();
      final lines = txt.split('\n');
      final taken = lines.take(maxLines).join('\n');
      final encoded = utf8.encode(taken);
      final clipped = encoded.length <= budgetBytes
          ? encoded
          : encoded.sublist(0, budgetBytes);
      return PreviewBundle(
        summary: '${lines.length} lines',
        dataB64: base64Encode(clipped),
        mimeType: 'text/plain',
      );
    } catch (_) {
      return const PreviewBundle();
    }
  }

  // ─── document ─────────────────────────────────────────

  Future<PreviewBundle> _documentPreview(Artifact art) async {
    final f = File('$worktreePath/${art.relPath}');
    if (!await f.exists()) return const PreviewBundle();
    final mime = art.mimeType ?? '';
    // PDF / docx 等需要解析依赖, P2 后续。markdown / 纯文本走文本 preview。
    if (!mime.startsWith('text/') && mime != 'application/json') {
      return PreviewBundle(summary: _sizeSummary(art));
    }
    try {
      final txt = await f.readAsString();
      // 第一段: 第一个 \n\n 之前的内容, 截到 500 字
      final firstPara = _firstParagraph(txt, maxChars: 500);
      final wordCount = txt.trim().isEmpty ? 0 : txt.trim().split(RegExp(r'\s+')).length;
      final encoded = utf8.encode(firstPara);
      final clipped = encoded.length <= budgetBytes
          ? encoded
          : encoded.sublist(0, budgetBytes);
      return PreviewBundle(
        summary: '$wordCount 词',
        dataB64: base64Encode(clipped),
        mimeType: 'text/plain',
      );
    } catch (_) {
      return PreviewBundle(summary: _sizeSummary(art));
    }
  }

  // ─── helpers ──────────────────────────────────────────

  @visibleForTesting
  static String sizeSummary(Artifact art) => _sizeSummary(art);

  static String _sizeSummary(Artifact art) {
    final b = art.sizeBytes;
    if (b < 1024) return '$b B';
    if (b < 1024 * 1024) return '${(b / 1024).toStringAsFixed(1)} KB';
    if (b < 1024 * 1024 * 1024) {
      return '${(b / (1024 * 1024)).toStringAsFixed(1)} MB';
    }
    return '${(b / (1024 * 1024 * 1024)).toStringAsFixed(2)} GB';
  }

  /// 从 git diff 输出尾部找 +/- 行计数, 拼成 "+12 -3" 风格摘要。
  /// 没找到时返回 "diff" 兜底。
  @visibleForTesting
  static String diffStatSummary(String diff) => _diffStatSummary(diff);

  static String _diffStatSummary(String diff) {
    int adds = 0, dels = 0;
    for (final line in diff.split('\n')) {
      if (line.startsWith('+++') || line.startsWith('---')) continue;
      if (line.startsWith('+')) adds++;
      if (line.startsWith('-')) dels++;
    }
    if (adds == 0 && dels == 0) return 'diff';
    return '+$adds -$dels';
  }

  @visibleForTesting
  static String firstParagraph(String txt, {int maxChars = 500}) =>
      _firstParagraph(txt, maxChars: maxChars);

  static String _firstParagraph(String txt, {int maxChars = 500}) {
    final clean = txt.trimLeft();
    final sep = clean.indexOf('\n\n');
    final para = sep < 0 ? clean : clean.substring(0, sep);
    if (para.length <= maxChars) return para;
    return '${para.substring(0, maxChars)}…';
  }
}
