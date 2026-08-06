// SDK Protocol v1 Dart roundtrip test —— 验证关键 variant 能正确 encode/decode，
// dispatcher 选对类型。
//
// 不测全部 80+ class（go 端已经在 sdkproto/v1/*_test.go 里覆盖），
// 只测 Dart 端 ServiceFrame.fromJson dispatcher 是否正确路由。

import 'dart:convert';

import 'package:biumind/data/api/sdkproto/v1/common.dart';
import 'package:biumind/data/api/sdkproto/v1/control/wrappers.dart';
import 'package:biumind/data/api/sdkproto/v1/data/result.dart';
import 'package:biumind/data/api/sdkproto/v1/data/system.dart';
import 'package:biumind/data/api/sdkproto/v1/data/user.dart';
import 'package:biumind/data/api/sdkproto/v1/lifecycle.dart';
import 'package:biumind/data/api/sdkproto/v1/service.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('ServiceFrame dispatcher', () {
    test('user message → SDKUserMessage', () {
      final json = jsonDecode('''
        {
          "type": "user",
          "message": { "role": "user", "content": "hi" },
          "uuid": "u1",
          "session_id": "s1"
        }
      ''') as Map<String, dynamic>;
      final frame = ServiceFrame.fromJson(json);
      expect(frame, isA<SDKUserMessage>());
      final um = frame as SDKUserMessage;
      expect(um.uuid, 'u1');
      expect(um.sessionId, 's1');
      expect(um.message.role, 'user');
    });

    test('result success vs error → 不同子类', () {
      final success = jsonDecode(jsonEncode({
        'type': 'result',
        'subtype': 'success',
        'duration_ms': 1000,
        'duration_api_ms': 800,
        'is_error': false,
        'num_turns': 3,
        'result': 'done',
        'stop_reason': 'end_turn',
        'total_cost_usd': 0.01,
        'usage': {},
        'modelUsage': {},
        'permission_denials': [],
        'uuid': 'r1',
        'session_id': 's1',
      })) as Map<String, dynamic>;
      expect(ServiceFrame.fromJson(success), isA<SDKResultSuccess>());

      final error = jsonDecode(jsonEncode({
        'type': 'result',
        'subtype': 'error_max_turns',
        'duration_ms': 5000,
        'duration_api_ms': 4000,
        'is_error': true,
        'num_turns': 10,
        'total_cost_usd': 0.05,
        'usage': {},
        'modelUsage': {},
        'permission_denials': [],
        'errors': [],
        'uuid': 'r2',
        'session_id': 's1',
      })) as Map<String, dynamic>;
      expect(ServiceFrame.fromJson(error), isA<SDKResultError>());
    });

    test('control_request → SDKControlRequest with subtype', () {
      final json = jsonDecode('''
        {
          "type": "control_request",
          "request_id": "r1",
          "request": {
            "subtype": "interrupt"
          }
        }
      ''') as Map<String, dynamic>;
      final frame = ServiceFrame.fromJson(json);
      expect(frame, isA<SDKControlRequest>());
      expect((frame as SDKControlRequest).subtype, 'interrupt');
    });

    test('control_response success', () {
      final json = jsonDecode('''
        {
          "type": "control_response",
          "response": {
            "subtype": "success",
            "request_id": "r1",
            "response": { "totalTokens": 1234 }
          }
        }
      ''') as Map<String, dynamic>;
      final frame = ServiceFrame.fromJson(json);
      expect(frame, isA<SDKControlResponse>());
      final resp = frame as SDKControlResponse;
      expect(resp.response.isSuccess, true);
      expect(resp.response.requestId, 'r1');
    });

    test('lifecycle keep_alive', () {
      final frame = ServiceFrame.fromJson({'type': 'keep_alive', 'ts': 123456});
      expect(frame, isA<KeepAlive>());
    });

    test('lifecycle biumind.session_paused', () {
      final frame = ServiceFrame.fromJson({
        'type': 'biumind.session_paused',
        'session_id': 's1',
        'reason': 'rate_limited',
      });
      expect(frame, isA<SessionPaused>());
      expect((frame as SessionPaused).reason, 'rate_limited');
    });

    test('system init → SDKSystemInit', () {
      final frame = ServiceFrame.fromJson({
        'type': 'system',
        'subtype': 'init',
        'agents': [],
        'apiKeySource': 'env',
        'betas': [],
        'claude_code_version': '2.0.0',
        'cwd': '/tmp',
        'tools': ['Bash'],
        'mcp_servers': [],
        'model': 'claude-3-7',
        'permissionMode': 'default',
        'slash_commands': [],
        'output_style': 'default',
        'uuid': 'i1',
        'session_id': 's1',
      });
      expect(frame, isA<SDKSystemInit>());
    });

    test('unknown type throws', () {
      expect(
        () => ServiceFrame.fromJson({'type': 'totally_made_up'}),
        throwsArgumentError,
      );
    });
  });

  group('SDKMessage roundtrip', () {
    test('SDKUserMessage encode → decode 字段保留', () {
      final original = SDKUserMessage(
        message: AnthropicMessage(role: 'user', content: 'hello'),
        uuid: 'u1',
        sessionId: 's1',
      );
      final json = original.toJson();
      final decoded = SDKUserMessage.fromJson(jsonDecode(jsonEncode(json)));
      expect(decoded.uuid, 'u1');
      expect(decoded.sessionId, 's1');
    });
  });
}
