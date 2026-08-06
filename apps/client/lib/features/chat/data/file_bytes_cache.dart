// FileBytesCache — 按 file_id 缓存 chat 附件字节, 渲染层调。
//
// 设计文档: docs/BiuMind-Chat-Attachments-MinIO-Design.md §4.7。
//
// 两层缓存:
//   memory: LRU, 默认 32 项 / 64 MB; 命中 → O(1) 返字节
//   disk  : applicationCacheDirectory/biumind/files/<id>; 跨重启保留
//
// fetch 顺序: memory → disk → GET /v1/files/{id} (Bearer, 走 model-relay)。
// 写顺序: 先 disk 再 memory — 失败 disk 不影响内存命中, 反过来也行。
//
// 不做主动失效: file 内容是不可变的 (sha256 索引), 缓存永远是对的。
// 用户登出 → memory 清; disk 留着, 下次同 user 还能用 (不同 user 拉
// 不同文件, 不会冲突)。

import 'dart:async';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:http/http.dart' as http;
import 'package:path_provider/path_provider.dart';

import '../../../services/auth_service.dart';

/// 单条缓存元数据。
class _Entry {
  _Entry({required this.bytes, required this.lastAccess});
  Uint8List bytes;
  DateTime lastAccess;
}

class FileBytesCache {
  FileBytesCache({
    required this.endpoint,
    required this.bearerToken,
    this.maxItems = 32,
    this.maxBytes = 64 * 1024 * 1024,
    http.Client? httpClient,
  }) : _http = httpClient ?? http.Client();

  final Uri endpoint;
  final String bearerToken;
  final int maxItems;
  final int maxBytes;
  final http.Client _http;

  final Map<String, _Entry> _memory = {};
  // 防止同一 fileId 同时多次拉
  final Map<String, Future<Uint8List>> _inflight = {};
  Directory? _diskDir;

  Future<Directory> _ensureDir() async {
    if (_diskDir != null) return _diskDir!;
    final base = await getApplicationCacheDirectory();
    final dir = Directory('${base.path}/biumind/files');
    if (!await dir.exists()) {
      await dir.create(recursive: true);
    }
    _diskDir = dir;
    return dir;
  }

  /// 取字节: 缓存命中即返, 否则从 server 拉 + 入缓存。
  /// 失败抛 Exception, 调用方负责显示占位图。
  Future<Uint8List> getOrFetch(String fileId) async {
    final mem = _memory[fileId];
    if (mem != null) {
      mem.lastAccess = DateTime.now();
      return mem.bytes;
    }

    final inflight = _inflight[fileId];
    if (inflight != null) return inflight;

    final fut = _fetchAndCache(fileId);
    _inflight[fileId] = fut;
    try {
      return await fut;
    } finally {
      _inflight.remove(fileId);
    }
  }

  Future<Uint8List> _fetchAndCache(String fileId) async {
    // 1. disk
    try {
      final dir = await _ensureDir();
      final f = File('${dir.path}/$fileId');
      if (await f.exists()) {
        final bytes = await f.readAsBytes();
        _putMemory(fileId, bytes);
        return bytes;
      }
    } catch (_) {/* disk 失败不阻塞, 走网络 */}

    // 2. server
    final url = endpoint.replace(path: '/v1/files/$fileId');
    final resp = await _http.get(url, headers: {
      'Authorization': 'Bearer $bearerToken',
    });
    if (resp.statusCode < 200 || resp.statusCode >= 300) {
      throw Exception('files GET $fileId: ${resp.statusCode}');
    }
    final bytes = resp.bodyBytes;
    _putMemory(fileId, bytes);
    unawaited(_writeDisk(fileId, bytes));
    return bytes;
  }

  void _putMemory(String fileId, Uint8List bytes) {
    _memory[fileId] =
        _Entry(bytes: bytes, lastAccess: DateTime.now());
    _evictMemoryIfNeeded();
  }

  void _evictMemoryIfNeeded() {
    while (_memory.length > maxItems || _totalBytes() > maxBytes) {
      // 找最旧的 key 删
      String? oldestKey;
      DateTime? oldest;
      for (final e in _memory.entries) {
        if (oldest == null || e.value.lastAccess.isBefore(oldest)) {
          oldest = e.value.lastAccess;
          oldestKey = e.key;
        }
      }
      if (oldestKey == null) break;
      _memory.remove(oldestKey);
    }
  }

  int _totalBytes() {
    var s = 0;
    for (final e in _memory.values) {
      s += e.bytes.length;
    }
    return s;
  }

  Future<void> _writeDisk(String fileId, Uint8List bytes) async {
    try {
      final dir = await _ensureDir();
      final f = File('${dir.path}/$fileId');
      await f.writeAsBytes(bytes, flush: false);
    } catch (_) {/* disk 失败可以容忍, 内存层已经命中 */}
  }

  void clearMemory() {
    _memory.clear();
  }
}

final fileBytesCacheProvider = Provider<FileBytesCache?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  return FileBytesCache(
    endpoint: creds.endpoint,
    bearerToken: creds.bearerToken,
  );
});
