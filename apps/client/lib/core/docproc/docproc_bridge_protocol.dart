// Docproc bridge protocol v1 — Dart side. Mirrors
// apps/client/docproc-web/src/bridge/protocol.ts. Both definitions MUST
// evolve in lockstep; protocol changes that drop or rename fields require
// bumping the version constant.
//
// 消息形态与 editor bridge 相同（{type, v, id?, payload}），但两个 bundle
// 相互独立，不共享代码（设计文档 §2.3：解析逻辑只在 TS 一处，双实现漂移
// 是前车之鉴 —— 协议也一样各自演化）。

const int kDocprocProtocolVersion = 1;

/// TS bundle 上报的错误码（协议约定）：unsupported / encrypted / corrupt /
/// oom。Dart 侧合成的补充码：timeout（等待 result 超时）、cancelled
/// （host 主动取消 / 引擎 detach）。
class DocprocException implements Exception {
  const DocprocException({
    required this.code,
    required this.message,
    this.retryable = false,
  });

  final String code;
  final String message;

  /// true = 值得重试或自动转云端兜底（如 oom / timeout）。
  final bool retryable;

  @override
  String toString() => 'DocprocException($code): $message';
}

/// `result` 消息 payload 的解析结果（对齐 TS ResultPayload）。
class DocprocResult {
  const DocprocResult({
    required this.text,
    required this.format,
    required this.parserVersion,
    this.pageCount,
    this.warnings = const <String>[],
  });

  factory DocprocResult.fromJson(Map<String, dynamic> j) => DocprocResult(
        text: (j['text'] as String?) ?? '',
        format: (j['format'] as String?) ?? 'txt',
        pageCount: (j['pageCount'] as num?)?.toInt(),
        parserVersion: (j['parserVersion'] as String?) ?? 'unknown',
        warnings: (j['warnings'] as List? ?? const [])
            .map((e) => e.toString())
            .toList(),
      );

  final String text;

  /// pdf / docx / html / md / txt（支持清单见 [docprocWebSupportsFormat]）
  final String format;
  final int? pageCount;

  /// bundle 版本常量（如 'docproc-web@0.1.0'），落 parse_meta.version。
  final String parserVersion;
  final List<String> warnings;
}

/// docproc-web bundle 实际支持解析的格式判定 —— 镜像
/// docproc-web/src/parsers/detect.ts 的 EXT_TO_FORMAT / MIME_TO_FORMAT
/// （扩展名优先，mimeHint 兜底），两边 MUST 同步演化。
///
/// 用途：队列在调度时按本函数 gate —— 不支持的格式（xlsx/pptx/epub 等）
/// 跳过本机 parse 直接走云端上传，避免空转一次 parse 再 fallback
/// （镜像任务会先 PATCH failed 再被云端接管，activity 卡片闪烁）。
bool docprocWebSupportsFormat(String filename, String? mime) {
  final dot = filename.lastIndexOf('.');
  if (dot >= 0 && dot < filename.length - 1) {
    final ext = filename.substring(dot + 1).toLowerCase();
    if (_kDocprocWebExts.contains(ext)) return true;
  }
  if (mime != null) {
    final m = mime.split(';').first.trim().toLowerCase();
    if (_kDocprocWebMimes.contains(m)) return true;
  }
  return false;
}

const _kDocprocWebExts = <String>{
  'pdf',
  'docx',
  'html',
  'htm',
  'md',
  'markdown',
  'txt',
  'text',
};

const _kDocprocWebMimes = <String>{
  'application/pdf',
  'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  'text/html',
  'text/markdown',
  'text/x-markdown',
  'text/plain',
};

class DocprocMessage {
  const DocprocMessage({
    required this.type,
    required this.payload,
    this.id,
    this.v = kDocprocProtocolVersion,
  });

  factory DocprocMessage.fromJson(Map<String, dynamic> json) {
    final type = json['type'];
    final v = json['v'];
    final payload = json['payload'];
    if (type is! String || v is! num || payload is! Map) {
      throw const FormatException('docproc message: missing required fields');
    }
    final id = json['id'];
    return DocprocMessage(
      type: type,
      v: v.toInt(),
      id: id is String ? id : null,
      payload: Map<String, dynamic>.from(payload),
    );
  }

  final String type;
  final int v;
  final String? id;
  final Map<String, dynamic> payload;

  Map<String, dynamic> toJson() {
    final out = <String, dynamic>{'type': type, 'v': v, 'payload': payload};
    if (id != null) out['id'] = id;
    return out;
  }
}

// Outbound (Host → Bundle) constructors.

/// WebView 就绪握手；bundle 收到后重发 ready。
DocprocMessage pingMessage() =>
    const DocprocMessage(type: 'ping', payload: <String, dynamic>{});

/// P1 整文件 base64；>50MB 在调用侧（import_dialog）已拒绝走本机。
DocprocMessage parseMessage({
  required String id,
  required String fileName,
  required String dataBase64,
  String? mimeHint,
}) {
  return DocprocMessage(
    type: 'parse',
    id: id,
    payload: {
      'id': id,
      'fileName': fileName,
      'mimeHint': ?mimeHint,
      'dataBase64': dataBase64,
    },
  );
}

DocprocMessage cancelMessage(String id) =>
    DocprocMessage(type: 'cancel', id: id, payload: {'id': id});
