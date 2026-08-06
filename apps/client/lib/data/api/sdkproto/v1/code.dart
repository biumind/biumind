// 编码模块（BiuMind Code）专属 WS 帧 —— Dart 镜像。
//
// 对端是 Go 的 packages/go-sdk/biu/sdkproto/v1/code.go；字段 json tag 必须严格
// 一致（snake_case）。两类帧：
//   - code_request / code_response：通用 RPC 信封（method 分发 git.status /
//     fs.read / pty.open / ...）。params / result 是任意 JSON 对象 → Map。
//   - code_pty_chunk / code_pty_input / code_pty_resize / code_pty_exit：PTY
//     字节流 + 控制。Go 端 []byte 在 JSON 里是 base64 字符串，故 data 用
//     base64Decode/Encode 作 JsonKey 转换器，Dart 侧直接拿到 Uint8List。

import 'dart:convert';
import 'dart:typed_data';

import 'package:json_annotation/json_annotation.dart';

part 'code.g.dart';

/// CodeFrame 是编码模块 7 个帧的公共标记 + fromJson 分发入口。
abstract class CodeFrame {
  String get type;
  Map<String, dynamic> toJson();

  static CodeFrame fromJson(Map<String, dynamic> json) {
    final t = json['type'] as String? ?? '';
    switch (t) {
      case 'code_request':
        return CodeRequest.fromJson(json);
      case 'code_response':
        return CodeResponse.fromJson(json);
      case 'code_pty_chunk':
        return CodePtyChunk.fromJson(json);
      case 'code_pty_input':
        return CodePtyInput.fromJson(json);
      case 'code_pty_resize':
        return CodePtyResize.fromJson(json);
      case 'code_pty_exit':
        return CodePtyExit.fromJson(json);
      case 'code_session_event':
        return CodeSessionEvent.fromJson(json);
      default:
        throw ArgumentError('unknown code frame type: $t');
    }
  }
}

/// 客户端 → 服务端的通用 RPC 信封。
@JsonSerializable(includeIfNull: false, anyMap: true)
class CodeRequest implements CodeFrame {
  @override
  final String type;
  @JsonKey(name: 'request_id')
  final String requestId;
  final String method;
  final Map<String, dynamic>? params;

  CodeRequest({
    this.type = 'code_request',
    required this.requestId,
    required this.method,
    this.params,
  });

  factory CodeRequest.fromJson(Map<String, dynamic> json) =>
      _$CodeRequestFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$CodeRequestToJson(this);
}

/// 服务端 → 客户端对某 CodeRequest 的应答。
@JsonSerializable(includeIfNull: false, anyMap: true)
class CodeResponse implements CodeFrame {
  @override
  final String type;
  @JsonKey(name: 'request_id')
  final String requestId;
  final bool ok;
  final Map<String, dynamic>? result;
  final String? error;

  CodeResponse({
    this.type = 'code_response',
    required this.requestId,
    required this.ok,
    this.result,
    this.error,
  });

  factory CodeResponse.fromJson(Map<String, dynamic> json) =>
      _$CodeResponseFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$CodeResponseToJson(this);
}

/// 服务端 → 客户端的 PTY 原始输出字节（JSON 里是 base64 字符串）。
@JsonSerializable(includeIfNull: false, anyMap: true)
class CodePtyChunk implements CodeFrame {
  @override
  final String type;
  @JsonKey(name: 'pty_id')
  final String ptyId;
  @JsonKey(name: 'data', fromJson: base64Decode, toJson: base64Encode)
  final Uint8List data;

  CodePtyChunk({
    this.type = 'code_pty_chunk',
    required this.ptyId,
    required this.data,
  });

  factory CodePtyChunk.fromJson(Map<String, dynamic> json) =>
      _$CodePtyChunkFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$CodePtyChunkToJson(this);
}

/// 客户端 → 服务端的 PTY 输入字节（键盘/粘贴）。
@JsonSerializable(includeIfNull: false, anyMap: true)
class CodePtyInput implements CodeFrame {
  @override
  final String type;
  @JsonKey(name: 'pty_id')
  final String ptyId;
  @JsonKey(name: 'data', fromJson: base64Decode, toJson: base64Encode)
  final Uint8List data;

  CodePtyInput({
    this.type = 'code_pty_input',
    required this.ptyId,
    required this.data,
  });

  factory CodePtyInput.fromJson(Map<String, dynamic> json) =>
      _$CodePtyInputFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$CodePtyInputToJson(this);
}

/// 客户端 → 服务端的终端尺寸变更。服务端会把 cols/rows 钳到 [2,10000]。
@JsonSerializable(includeIfNull: false, anyMap: true)
class CodePtyResize implements CodeFrame {
  @override
  final String type;
  @JsonKey(name: 'pty_id')
  final String ptyId;
  final int cols;
  final int rows;

  CodePtyResize({
    this.type = 'code_pty_resize',
    required this.ptyId,
    required this.cols,
    required this.rows,
  });

  factory CodePtyResize.fromJson(Map<String, dynamic> json) =>
      _$CodePtyResizeFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$CodePtyResizeToJson(this);
}

/// 服务端 → 客户端的进程退出通知。exitCode=0 正常；error 非空表示 wait 自身出错。
@JsonSerializable(includeIfNull: false, anyMap: true)
class CodePtyExit implements CodeFrame {
  @override
  final String type;
  @JsonKey(name: 'pty_id')
  final String ptyId;
  @JsonKey(name: 'exit_code')
  final int exitCode;
  final String? error;

  CodePtyExit({
    this.type = 'code_pty_exit',
    required this.ptyId,
    required this.exitCode,
    this.error,
  });

  factory CodePtyExit.fromJson(Map<String, dynamic> json) =>
      _$CodePtyExitFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$CodePtyExitToJson(this);
}

/// 服务端 → 客户端的结构化会话事件（M3）。event 是单条 AgentEvent JSON
/// （type=text_delta/tool_use_start/tool_use_result/cost_update/...），由 daemon
/// 解析 agent 自写的 JSONL 会话文件得到；按 taskId demux 到对应任务的结构化视图。
@JsonSerializable(includeIfNull: false, anyMap: true)
class CodeSessionEvent implements CodeFrame {
  @override
  final String type;
  @JsonKey(name: 'task_id')
  final String taskId;
  final Map<String, dynamic> event;

  CodeSessionEvent({
    this.type = 'code_session_event',
    required this.taskId,
    required this.event,
  });

  factory CodeSessionEvent.fromJson(Map<String, dynamic> json) =>
      _$CodeSessionEventFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$CodeSessionEventToJson(this);
}
