/// Transport-agnostic bridge controller for the docproc (document parsing)
/// bundle. Mirrors the editor bridge wiring:
///   * platform view calls [onIncomingMessage] for every bundle message
///   * controller calls the [DocprocSend] provided at attach time to push
///     ping / parse / cancel into the bundle
///
/// 生命周期：platform view 挂载后 bundle 自发 `ready`；[parse] 在 ready
/// 未到时排队等待（[readyTimeout] 超时判引擎不可用）。每个 parse 任务以
/// uuid 关联，result / error 按 id 路由，[parseTimeout] 兜底。
library;

import 'dart:async';
import 'dart:convert' show base64Encode;

import 'package:flutter/foundation.dart';
import 'package:uuid/uuid.dart';

import '../platform/platform_caps.dart';
import 'docproc_bridge_protocol.dart';

typedef DocprocSend = Future<void> Function(DocprocMessage message);

class DocprocBridgeController {
  DocprocBridgeController({
    PlatformCaps? caps,
    this.parseTimeout = const Duration(seconds: 120),
    this.readyTimeout = const Duration(seconds: 30),
  }) : _caps = caps ?? PlatformCaps.detect();

  final PlatformCaps _caps;

  /// 单个 parse 任务等待 result / error 的上限（大 PDF 解析可能数十秒）。
  final Duration parseTimeout;

  /// 等待 bundle `ready` 的上限（引擎白屏 / 产物缺失时快速失败）。
  final Duration readyTimeout;

  /// bundle 上报的解析进度（phase: load / extract）。
  void Function(String id, String phase, int percent)? onProgress;

  /// ready 握手拿到的 bundle 版本与支持格式（诊断 / parse_meta 用）。
  String? bundleVersion;
  List<String> formats = const <String>[];

  DocprocSend? _send;
  Completer<void>? _readyCompleter;
  final Map<String, Completer<DocprocResult>> _pending = {};
  final Map<String, Timer> _timers = {};

  static const _uuid = Uuid();

  bool get _isReady => _readyCompleter?.isCompleted ?? false;

  /// Called by the platform view once its transport is connected.
  void attach(DocprocSend send) {
    _send = send;
    _readyCompleter ??= Completer<void>();
    // 引擎可能晚于 bundle 加载完成才 attach（iframe 复用等场景），
    // 主动 ping 让 bundle 重发 ready。
    unawaited(_send?.call(pingMessage()));
  }

  void detach() {
    _send = null;
    // 引擎被销毁：ready 复位，在途任务立即失败而不是干等超时。
    if (_isReady) _readyCompleter = Completer<void>();
    for (final entry in _pending.entries) {
      if (!entry.value.isCompleted) {
        entry.value.completeError(
          const DocprocException(
            code: 'cancelled',
            message: 'docproc 引擎已销毁',
          ),
        );
      }
    }
    for (final timer in _timers.values) {
      timer.cancel();
    }
    _pending.clear();
    _timers.clear();
  }

  /// 等待 bundle `ready`；超时抛 [DocprocException]（code: timeout）。
  Future<void> ensureReady() async {
    _guardPlatform();
    final completer = _readyCompleter ??= Completer<void>();
    if (completer.isCompleted) return;
    await completer.future.timeout(
      readyTimeout,
      onTimeout: () => throw const DocprocException(
        code: 'timeout',
        message: '等待 docproc bundle ready 超时',
        retryable: true,
      ),
    );
  }

  /// 本机解析一个文件：读字节 → base64 → bundle → [DocprocResult]。
  ///
  /// 失败抛 [DocprocException]（code 见类注释）；调用方据此决定是否
  /// 回退云端路径。
  Future<DocprocResult> parse({
    required String fileName,
    required Uint8List bytes,
    String? mimeHint,
  }) async {
    await ensureReady();
    final send = _send;
    if (send == null) {
      throw const DocprocException(
        code: 'cancelled',
        message: 'docproc 引擎未挂载',
      );
    }
    final id = _uuid.v4();
    final completer = Completer<DocprocResult>();
    _pending[id] = completer;
    _timers[id] = Timer(parseTimeout, () {
      final c = _pending.remove(id);
      _timers.remove(id);
      if (c != null && !c.isCompleted) {
        c.completeError(
          const DocprocException(
            code: 'timeout',
            message: '本机解析超时',
            retryable: true,
          ),
        );
      }
    });
    await send(
      parseMessage(
        id: id,
        fileName: fileName,
        mimeHint: mimeHint,
        dataBase64: base64Encode(bytes),
      ),
    );
    return completer.future;
  }

  /// 取消在途解析：通知 bundle 放弃，本地 future 立即以 cancelled 失败。
  void cancel(String id) {
    final c = _pending.remove(id);
    _timers.remove(id)?.cancel();
    unawaited(_send?.call(cancelMessage(id)));
    if (c != null && !c.isCompleted) {
      c.completeError(
        const DocprocException(code: 'cancelled', message: '已取消'),
      );
    }
  }

  /// Dispatch an incoming wire message from the bundle. The platform
  /// view is responsible for parsing and calling this.
  void onIncomingMessage(DocprocMessage msg) {
    if (msg.v != kDocprocProtocolVersion) {
      debugPrint(
        '[docproc-bridge] protocol version mismatch: '
        'host v=$kDocprocProtocolVersion, bundle v=${msg.v}',
      );
      return;
    }
    switch (msg.type) {
      case 'ready':
        _onReady(msg);
      case 'progress':
        _onProgress(msg);
      case 'result':
        _onResult(msg);
      case 'error':
        _onError(msg);
      default:
        debugPrint('[docproc-bridge] unknown message type: ${msg.type}');
    }
  }

  void _onReady(DocprocMessage msg) {
    bundleVersion = msg.payload['version'] as String?;
    formats = (msg.payload['formats'] as List? ?? const [])
        .map((e) => e.toString())
        .toList();
    final completer = _readyCompleter ??= Completer<void>();
    if (!completer.isCompleted) completer.complete();
  }

  void _onProgress(DocprocMessage msg) {
    final id = msg.id;
    if (id == null) return;
    final phase = msg.payload['phase'];
    final percent = msg.payload['percent'];
    if (phase is String && percent is num) {
      onProgress?.call(id, phase, percent.toInt());
    }
  }

  void _onResult(DocprocMessage msg) {
    final id = msg.id;
    if (id == null) return;
    final c = _pending.remove(id);
    _timers.remove(id)?.cancel();
    if (c == null || c.isCompleted) return;
    c.complete(DocprocResult.fromJson(msg.payload));
  }

  void _onError(DocprocMessage msg) {
    final id = msg.id;
    if (id == null) return;
    final c = _pending.remove(id);
    _timers.remove(id)?.cancel();
    if (c == null || c.isCompleted) return;
    c.completeError(
      DocprocException(
        code: (msg.payload['code'] as String?) ?? 'corrupt',
        message: (msg.payload['message'] as String?) ?? '未知错误',
        retryable: msg.payload['retryable'] == true,
      ),
    );
  }

  void _guardPlatform() {
    if (!_caps.hasLocalDocproc) {
      throw UnsupportedError(
        '当前平台不支持本机文档解析（hasLocalDocproc=false），请走云端路径',
      );
    }
  }
}
