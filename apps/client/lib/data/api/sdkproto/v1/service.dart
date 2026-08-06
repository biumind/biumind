// 顶层 frame factory —— peek type 字段后 dispatch 到具体类型。
//
// Dart 端 BiuClient 收到 WS 帧时 → JSON decode → ServiceFrame.fromJson →
// 按 inner 的具体类型分支处理（is SDKMessage、is SDKControlRequest 等）。
//
// 不强制区分 Stdin / Stdout，因为协议层差异最终是字段集合差异，dispatcher 是同一个。

import 'package:flutter/foundation.dart' show debugPrint;

import 'control/wrappers.dart';
import 'data/assistant.dart';
import 'data/post_turn.dart';
import 'data/result.dart';
import 'data/sdk_message.dart';
import 'data/streamlined.dart';
import 'data/system.dart';
import 'data/tool.dart';
import 'data/user.dart';
import 'lifecycle.dart';

class ServiceFrame {
  /// 把任意 WS 帧 JSON 解析成具体类型实例。
  /// 返回类型可能是：SDKMessage / SDKControlRequest / SDKControlResponse /
  /// SDKControlCancelRequest / Lifecycle。调用方用 is/as 分支处理。
  static Object fromJson(Map<String, dynamic> json) {
    final t = json['type'] as String? ?? '';
    switch (t) {
      // 控制平面 wrapper
      case 'control_request':
        return SDKControlRequest.fromJson(json);
      case 'control_response':
        return SDKControlResponse.fromJson(json);
      case 'control_cancel_request':
        return SDKControlCancelRequest.fromJson(json);

      // BiuMind lifecycle
      case 'keep_alive':
      case 'update_environment_variables':
      case 'biumind.session_desynced':
      case 'biumind.session_paused':
      case 'biumind.session_resumed':
      case 'biumind.session_primary_promoted':
        return Lifecycle.fromJson(json);

      // 数据平面
      case 'user':
        return SDKUserMessage.fromJson(json);
      case 'assistant':
        return SDKAssistantMessage.fromJson(json);
      case 'stream_event':
        return SDKPartialAssistantMessage.fromJson(json);
      case 'result':
        final isError = json['is_error'] == true;
        return isError
            ? SDKResultError.fromJson(json)
            : SDKResultSuccess.fromJson(json);
      case 'auth_status':
        return SDKAuthStatus.fromJson(json);
      case 'rate_limit_event':
        return SDKRateLimitEvent.fromJson(json);
      case 'prompt_suggestion':
        return SDKPromptSuggestion.fromJson(json);
      case 'tool_progress':
        return SDKToolProgress.fromJson(json);
      case 'tool_use_summary':
        return SDKToolUseSummary.fromJson(json);
      case 'streamlined_text':
        return SDKStreamlinedText.fromJson(json);
      case 'streamlined_tool_use_summary':
        return SDKStreamlinedToolUseSummary.fromJson(json);
      case 'system':
        return _systemFromJson(json);
      default:
        // 协议级偏差(SDK 升级早于客户端 / 类型字符串拼写错): debug 模式
        // 下打日志方便看到原始 JSON; 仍 throw 保留老语义。
        debugPrint('[sdkproto] unknown frame type=$t keys=${json.keys.toList()}');
        throw ArgumentError('unknown frame type: $t');
    }
  }

  /// 把 SDKMessage / Lifecycle / SDKControlRequest 等转为 JSON。
  /// 调用方传入要发送的具体 instance；这里只是统一入口，避免散落 toJson。
  static Map<String, dynamic> toJson(Object frame) {
    if (frame is SDKMessage) {
      // 各 SDKMessage 子类实现自己的 toJson —— 用 dynamic 反射调用。
      return (frame as dynamic).toJson() as Map<String, dynamic>;
    }
    if (frame is SDKControlRequest) return frame.toJson();
    if (frame is SDKControlResponse) return frame.toJson();
    if (frame is SDKControlCancelRequest) return frame.toJson();
    if (frame is Lifecycle) return frame.toJson();
    throw ArgumentError('not a serializable frame: ${frame.runtimeType}');
  }

  static SDKMessage _systemFromJson(Map<String, dynamic> json) {
    final subtype = json['subtype'] as String? ?? '';
    switch (subtype) {
      case 'init':
        return SDKSystemInit.fromJson(json);
      case 'status':
        return SDKSystemStatus.fromJson(json);
      case 'compact_boundary':
        return SDKCompactBoundary.fromJson(json);
      case 'api_retry':
        return SDKAPIRetry.fromJson(json);
      case 'local_command_output':
        return SDKLocalCommandOutput.fromJson(json);
      case 'hook_started':
        return SDKHookStarted.fromJson(json);
      case 'hook_progress':
        return SDKHookProgress.fromJson(json);
      case 'hook_response':
        return SDKHookResponse.fromJson(json);
      case 'files_persisted':
        return SDKFilesPersisted.fromJson(json);
      case 'task_notification':
        return SDKTaskNotification.fromJson(json);
      case 'task_started':
        return SDKTaskStarted.fromJson(json);
      case 'task_progress':
        return SDKTaskProgress.fromJson(json);
      case 'session_state_changed':
        return SDKSessionStateChanged.fromJson(json);
      case 'elicitation_complete':
        return SDKElicitationComplete.fromJson(json);
      case 'post_turn_summary':
        return SDKPostTurnSummary.fromJson(json);
      default:
        throw ArgumentError('unknown system subtype: $subtype');
    }
  }
}
