// PermissionAuditTap —— auto 权限模式审计行（设计 §7-4 客户端半）的
// WS 旁路 tee。
//
// 背景：Go sdkbridge 把 PermissionAuditEvent 映射成 SDK
// system/local_command_output 帧发给客户端（'[auto] …' /
// '[auto-guard] …'）。BiuSessionConnection._onFrame 的块管线不消费
// system 帧（switch 无该 variant 分支，静默丢弃），且该文件归并行任务
// 不可改。最窄挂载点因此落在 transport 层：包一层 connector，下行帧原样
// 透传给 BiuClient，命中 local_command_output 时 tee 出 (uuid, content)
// 回调给 ChatController 写 SystemBlock。
//
// 性能：每帧只做一次 contains 预筛，命中才 jsonDecode —— 流式高频帧
// （streamlined_text）零解析开销。

import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart' show debugPrint;

import '../../../data/api/biu_client.dart';

/// 包一层 BiuTransport connector：下行帧透传 + tee 出
/// system/local_command_output（回调拿 uuid + content，调用方决定怎么
/// 落库，避免这层引 sdkproto 类型）。
///
/// [inner] 为 null（生产）时回落 BiuClient 的默认 WS connector。
BiuTransport Function(Uri) auditTeeConnector({
  BiuTransport Function(Uri)? inner,
  required void Function(String uuid, String content) onLocalCommandOutput,
}) {
  return (uri) => _AuditTeeTransport(
        (inner ?? BiuClient.defaultConnector)(uri),
        onLocalCommandOutput,
      );
}

class _AuditTeeTransport implements BiuTransport {
  _AuditTeeTransport(this._inner, this._onLine);

  final BiuTransport _inner;
  final void Function(String uuid, String content) _onLine;
  final StreamController<dynamic> _frames = StreamController<dynamic>.broadcast();
  StreamSubscription<dynamic>? _sub;

  @override
  Stream<dynamic> get frames {
    // 懒订阅：首次有人 listen 才接 inner（WS channel stream 单订阅，
    // 提前 listen 会跟 BiuClient 抢）。
    _sub ??= _inner.frames.listen((raw) {
      if (raw is String && raw.contains('"local_command_output"')) {
        try {
          final json = jsonDecode(raw);
          if (json is Map<String, dynamic>) {
            _onLine(
              (json['uuid'] as String?) ?? '',
              (json['content'] as String?) ?? '',
            );
          }
        } catch (e) {
          debugPrint('[audit_tee] local_command_output parse failed: $e');
        }
      }
      if (!_frames.isClosed) _frames.add(raw);
    }, onError: (Object e, StackTrace st) {
      if (!_frames.isClosed) _frames.addError(e, st);
    }, onDone: () {
      if (!_frames.isClosed) _frames.close();
    });
    return _frames.stream;
  }

  @override
  void send(String data) => _inner.send(data);

  @override
  Future<void> close() async {
    await _sub?.cancel();
    await _inner.close();
    if (!_frames.isClosed) await _frames.close();
  }
}
