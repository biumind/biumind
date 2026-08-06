// translateError 单测 — 覆盖 ApiError / SocketException / 未知 code 等路径.

import 'dart:io' show SocketException;

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/api/_http_helpers.dart';
import 'package:biumind/features/creation/data/error_translator.dart';

ApiError _api(int status, {String? code, String? message, String body = ''}) {
  if (body.isEmpty && (code != null || message != null)) {
    final c = code == null ? '' : '"code":"$code"';
    final m = message == null ? '' : '"message":"$message"';
    final inner = [c, m].where((s) => s.isNotEmpty).join(',');
    body = '{"error":{$inner}}';
  }
  return ApiError(path: '/v1/test', status: status, body: body);
}

void main() {
  group('translateError — known codes', () {
    test('insufficient_credits → 积分不足提示', () {
      expect(translateError(_api(402, code: 'insufficient_credits')),
          contains('积分不足'));
    });

    test('type_model_mismatch → 重新选择', () {
      expect(translateError(_api(400, code: 'type_model_mismatch')),
          contains('重新选择'));
    });

    test('MODERATION → 内容审核提示', () {
      expect(translateError(_api(400, code: 'MODERATION')),
          contains('审核'));
    });

    test('UPSTREAM_NOT_FOUND → 任务过期', () {
      expect(translateError(_api(404, code: 'UPSTREAM_NOT_FOUND')),
          contains('过期'));
    });
  });

  group('translateError — status fallback', () {
    test('401 直接提示登录过期', () {
      expect(translateError(_api(401)), contains('登录'));
    });

    test('403 提示无权限', () {
      expect(translateError(_api(403)), contains('权限'));
    });

    test('500+ 提示系统繁忙', () {
      expect(translateError(_api(500)), contains('繁忙'));
    });

    test('429 提示频繁', () {
      expect(translateError(_api(429)), contains('频繁'));
    });

    test('未知 code 退回 server message', () {
      expect(
        translateError(_api(400, code: 'something_new', message: 'try again later')),
        equals('try again later'),
      );
    });
  });

  group('translateError — non-ApiError', () {
    test('SocketException → 网络异常', () {
      expect(translateError(const SocketException('connection refused')),
          contains('网络异常'));
    });

    test('Generic Exception 去掉 "Exception: " 前缀', () {
      expect(
        translateError(Exception('something broke')),
        equals('something broke'),
      );
    });
  });

  group('translateError — robust', () {
    test('非 JSON body 也不崩, 走 status 兜底', () {
      expect(translateError(_api(500, body: 'random html error page')),
          contains('繁忙'));
    });

    test('空 body 走 status 兜底', () {
      expect(translateError(_api(404)), contains('不存在'));
    });
  });
}
