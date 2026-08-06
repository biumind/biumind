// FilesClient — Brain Files REST 客户端 (artifacts L3 / chat 附件 / 任意 source)。
//
// 路径都打 model-relay :7001 (model-relay 反代到 Brain), 跟 CodeTasksClient 同源。
//
// 支持两条上传路径:
//   1. multipart 代理: POST /v1/files/upload 一次性, brain 转发字节, 简单
//   2. presign 直传: POST /v1/files/presign-upload → PUT MinIO →
//      POST /v1/files/finalize 三步, brain 不碰字节, 推荐给聊天附件用
//
// download 走 http.Client.send 流式拉, caller 拿 byte stream 自己写文件。

import 'dart:async';
import 'dart:convert';
import 'dart:io' show File;

import 'package:crypto/crypto.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:http/http.dart' as http;
import 'package:http_parser/http_parser.dart';

import '../../../services/auth_service.dart';

class FilesClient {
  FilesClient(this.baseUrl, this.bearerToken);
  final Uri baseUrl;
  final String bearerToken;

  /// 上传文件原文 (内存 bytes)。小文件路径; 大文件用 [uploadFile]
  /// streaming 版本不占双倍内存。返回 server 分配的 file_id。
  Future<UploadResult> uploadBytes({
    required List<int> bytes,
    required String filename,
    String contentType = 'application/octet-stream',
    String source = 'code-artifact',
    Map<String, dynamic>? metadata,
    void Function(int sent, int total)? onProgress,
  }) async {
    final mp = http.MultipartFile.fromBytes(
      'file',
      bytes,
      filename: filename,
      contentType: _parseMime(contentType),
    );
    return _uploadMultipart(
      mp: mp,
      contentLength: bytes.length,
      source: source,
      metadata: metadata,
      onProgress: onProgress,
    );
  }

  /// 流式上传 — 不读整文件进内存, multipart body 直接从磁盘 read stream。
  /// 推荐 ≥10 MB 的文件用这个。
  Future<UploadResult> uploadFile({
    required File file,
    String? filename,
    String contentType = 'application/octet-stream',
    String source = 'code-artifact',
    Map<String, dynamic>? metadata,
    void Function(int sent, int total)? onProgress,
  }) async {
    final length = await file.length();
    final mp = http.MultipartFile(
      'file',
      file.openRead(),
      length,
      filename: filename ?? file.uri.pathSegments.last,
      contentType: _parseMime(contentType),
    );
    return _uploadMultipart(
      mp: mp,
      contentLength: length,
      source: source,
      metadata: metadata,
      onProgress: onProgress,
    );
  }

  Future<UploadResult> _uploadMultipart({
    required http.MultipartFile mp,
    required int contentLength,
    required String source,
    Map<String, dynamic>? metadata,
    void Function(int sent, int total)? onProgress,
  }) async {
    final url = baseUrl.replace(path: '/v1/files/upload');
    final req = http.MultipartRequest('POST', url);
    req.headers['Authorization'] = 'Bearer $bearerToken';
    req.fields['source'] = source;
    if (metadata != null) {
      req.fields['metadata'] = jsonEncode(metadata);
    }
    req.files.add(mp);
    onProgress?.call(0, contentLength);

    final streamed = await req.send();
    final body = await streamed.stream.bytesToString();
    if (streamed.statusCode < 200 || streamed.statusCode >= 300) {
      throw FilesApiError(streamed.statusCode, body);
    }
    onProgress?.call(contentLength, contentLength);
    final j = jsonDecode(body) as Map<String, dynamic>;
    return UploadResult(
      fileId: j['id']?.toString() ?? '',
      sha256: j['sha256']?.toString() ?? '',
      sizeBytes: (j['size_bytes'] as num?)?.toInt() ?? 0,
      mimeType: j['mime_type']?.toString(),
      deduped: j['deduped'] == true,
    );
  }

  // ─── Presign 直传路径 ───────────────────────────────────────

  /// 第 1 步: 申请 presigned PUT URL。返回签名信息和预占位的 file_id。
  /// 拿到后调用 [putToMinio] 直传字节, 再调 [finalize] 升级到 ready。
  /// 一般用 [uploadViaPresign] 一次走完。
  ///
  /// 失败抛 FilesApiError; 4xx 包含 server 给的具体 error code:
  ///   mime_not_allowed / too_large / bad_size 等 (参见 brain
  ///   api_presign.go)。
  Future<PresignedUploadInfo> presignUpload({
    required String filename,
    required String mime,
    required int size,
    String source = 'chat-attachment',
    Map<String, dynamic>? metadata,
  }) async {
    final url = baseUrl.replace(path: '/v1/files/presign-upload');
    final body = <String, dynamic>{
      'filename': filename,
      'mime': mime,
      'size': size,
      'source': source,
      'metadata': ?metadata,
    };
    final resp = await http.post(
      url,
      headers: {
        'Authorization': 'Bearer $bearerToken',
        'Content-Type': 'application/json',
      },
      body: jsonEncode(body),
    );
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw FilesApiError(resp.statusCode, resp.body);
    }
    return PresignedUploadInfo.fromJson(
        jsonDecode(resp.body) as Map<String, dynamic>);
  }

  /// 第 2 步: 直传字节到 MinIO 的预签名 URL。
  /// 不带 Authorization (presigned URL 自带签名)。Content-Type 必须
  /// 与 [presignUpload] 时声明的一致, 否则 MinIO 拒签。
  ///
  /// 抛 FilesApiError on 4xx/5xx (XML 错误体直接进 body)。
  Future<void> putToMinio({
    required PresignedUploadInfo signed,
    required List<int> bytes,
  }) async {
    final headers = <String, String>{...signed.headers};
    final resp = await http.put(
      Uri.parse(signed.uploadUrl),
      headers: headers,
      body: bytes,
    );
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw FilesApiError(resp.statusCode, resp.body);
    }
  }

  /// 第 3 步: 通知 server 字节已传完, 提交 sha256 + 真实 size。
  /// server 验对象存在 + 大小匹配 + dedup (用户内同 sha256 已有 ready
  /// 行 → 复用), 然后把 pending 升级到 ready。
  ///
  /// dedup 命中时 [UploadResult.deduped] = true, fileId 是已有的对象 id
  /// (调用方刚 PUT 的对象已被 server 删, 不要再用本地 file_id)。
  Future<UploadResult> finalize({
    required String fileId,
    required String sha256Hex,
    required int size,
  }) async {
    final url = baseUrl.replace(path: '/v1/files/finalize');
    final resp = await http.post(
      url,
      headers: {
        'Authorization': 'Bearer $bearerToken',
        'Content-Type': 'application/json',
      },
      body: jsonEncode({
        'file_id': fileId,
        'sha256': sha256Hex,
        'size': size,
      }),
    );
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw FilesApiError(resp.statusCode, resp.body);
    }
    final j = jsonDecode(resp.body) as Map<String, dynamic>;
    return UploadResult(
      // finalize 用 file_id 字段 (multipart 路径用 id, 兼容两种)
      fileId: (j['file_id'] ?? j['id'])?.toString() ?? '',
      sha256: j['sha256']?.toString() ?? '',
      sizeBytes: (j['size_bytes'] as num?)?.toInt() ?? 0,
      mimeType: j['mime_type']?.toString(),
      deduped: j['deduped'] == true,
    );
  }

  /// presign + PUT + finalize 一站式. 内存 bytes 路径 (≤ 8MB 推荐)。
  /// sha256 在客户端算; size mismatch / dedup 都在 finalize 阶段处理。
  ///
  /// 失败时尽可能不留 dangling pending row — presign 已成功但 PUT/finalize
  /// 失败的, 主动 DELETE /v1/files/{id} 清; 网络断了清不了的, server GC
  /// ticker 会按 pending 超时回收。
  Future<UploadResult> uploadViaPresign({
    required List<int> bytes,
    required String filename,
    required String mime,
    String source = 'chat-attachment',
    Map<String, dynamic>? metadata,
  }) async {
    final shaHex = sha256.convert(bytes).toString();
    final signed = await presignUpload(
      filename: filename,
      mime: mime,
      size: bytes.length,
      source: source,
      metadata: metadata,
    );
    try {
      await putToMinio(signed: signed, bytes: bytes);
      return await finalize(
        fileId: signed.fileId,
        sha256Hex: shaHex,
        size: bytes.length,
      );
    } catch (e) {
      // 失败收尾 — 撤销 server 端 pending row。失败本身不阻塞清理。
      unawaited(_softDeletePending(signed.fileId));
      rethrow;
    }
  }

  Future<void> _softDeletePending(String fileId) async {
    try {
      final url = baseUrl.replace(path: '/v1/files/$fileId');
      await http.delete(url,
          headers: {'Authorization': 'Bearer $bearerToken'});
    } catch (_) {
      // 忽略 — server GC ticker 兜底。
    }
  }

  /// 流式下载到本地 file. 返回写入字节数。
  /// 失败抛 FilesApiError。
  Future<int> downloadToFile({
    required String fileId,
    required File target,
    void Function(int received, int total)? onProgress,
  }) async {
    final url = baseUrl.replace(path: '/v1/files/$fileId');
    final req = http.Request('GET', url)
      ..headers['Authorization'] = 'Bearer $bearerToken';
    final streamed = await http.Client().send(req);
    if (streamed.statusCode < 200 || streamed.statusCode >= 300) {
      final body = await streamed.stream.bytesToString();
      throw FilesApiError(streamed.statusCode, body);
    }
    final total = streamed.contentLength ?? -1;
    final sink = target.openWrite();
    int written = 0;
    try {
      await for (final chunk in streamed.stream) {
        sink.add(chunk);
        written += chunk.length;
        onProgress?.call(written, total);
      }
    } finally {
      await sink.close();
    }
    return written;
  }

  static MediaType? _parseMime(String s) {
    final parts = s.split('/');
    if (parts.length != 2) return null;
    return MediaType(parts[0], parts[1]);
  }
}



/// upload endpoint 返回结构, 跟 brain api.go::objectOut 字段对齐。
class UploadResult {
  const UploadResult({
    required this.fileId,
    required this.sha256,
    required this.sizeBytes,
    this.mimeType,
    required this.deduped,
  });
  final String fileId;
  final String sha256;
  final int sizeBytes;
  final String? mimeType;

  /// true = server 命中 sha256 dedup, 没真上传, 直接复用已有 object_key。
  /// 用户能看到一个 toast "已秒传 (重复内容)"。
  final bool deduped;
}

class FilesApiError implements Exception {
  FilesApiError(this.status, this.body);
  final int status;
  final String body;
  @override
  String toString() => 'FilesApiError $status: $body';
}

/// presign-upload 端点返回的签名信息。
/// uploadUrl 在 expiresAt 之前持有者都能 PUT, 所以拿到后立即用、
/// 不要存盘。headers 必须原样带到 PUT (Content-Type 锁签名)。
class PresignedUploadInfo {
  const PresignedUploadInfo({
    required this.fileId,
    required this.uploadUrl,
    required this.headers,
    required this.objectKey,
  });

  factory PresignedUploadInfo.fromJson(Map<String, dynamic> j) =>
      PresignedUploadInfo(
        fileId: j['file_id']?.toString() ?? '',
        uploadUrl: j['upload_url']?.toString() ?? '',
        headers: ((j['headers'] as Map?) ?? const {})
            .map((k, v) => MapEntry(k.toString(), v.toString())),
        objectKey: j['object_key']?.toString() ?? '',
      );

  final String fileId;
  final String uploadUrl;
  final Map<String, String> headers;
  final String objectKey;
}

final filesClientProvider = Provider<FilesClient?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  return FilesClient(creds.endpoint, creds.bearerToken);
});
