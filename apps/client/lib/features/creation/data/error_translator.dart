// Backend 错误码 → 用户友好中文消息.
//
// 服务端 (services/aigc + services/identity credits) 返回结构:
//   { "error": { "code": "<short_code>", "message": "<en/raw>" } }
//
// 客户端 _http_helpers.apiRequest 把 4xx/5xx 抛成 ApiError(status, body).
// translate() 把它转成「积分不足，去充值」这种用户能直接看懂的句子.
//
// 设计原则:
//   - 已知 code 直翻; 不存在的 code 退回 server message; 都没有就「系统繁忙,
//     请稍后重试」; 网络层 (SocketException) 单独提示「网络异常,请检查连接」.
//   - 永远不暴露 stack trace / internal message.
//   - i18n MVP 走中文硬编码; 后续移到 .arb.

import 'dart:convert';
import 'dart:io' show SocketException;

import '../../../data/api/_http_helpers.dart';

/// 后端 -> 中文消息字典. 优先精确匹配 code; 没匹配走通用兜底.
const Map<String, String> _codeMap = {
  // ── 认证 / 授权 ──
  'missing_bearer': '请先登录',
  'invalid_token': '登录已过期, 请重新登录',
  'no_claims': '请先登录',
  'bad_subject': '账号信息异常, 请重新登录',
  'forbidden': '没有权限执行该操作',
  'authz_unavailable': '权限服务暂时不可用, 请稍后重试',
  'verifier_not_wired': '服务端配置异常, 请联系管理员',

  // ── 积分 ──
  'insufficient_credits': '积分不足, 请先充值',
  'credits_unavailable': '积分服务暂时不可用',
  'billing_not_wired': '积分服务未配置',
  'billing_failed': '扣减积分失败, 请稍后重试',
  'billing_bad_request': '积分计算异常, 请检查参数',
  'option_not_found': '套餐不存在',
  'option_disabled': '该套餐已下架',
  'bad_option_id': '套餐 ID 无效',
  'not_active': '账号未激活',

  // ── AIGC 提交 ──
  'bad_type': '不支持的生成类型',
  'bad_model': '不支持的模型',
  'bad_prompt': '提示词不合法',
  'model_not_found': '模型不存在',
  'model_disabled': '该模型已下架',
  'type_model_mismatch': '所选模型与类型不匹配, 请重新选择',
  'create_task': '创建任务失败, 请稍后重试',

  // ── 数字人 / 通用资源 ──
  'name_required': '名称不能为空',
  'name_too_long': '名称太长, 不能超过 64 字',
  'bad_id': 'ID 格式无效',
  'bad_json': '请求格式错误',
  'bad_config': '配置格式错误',
  'bad_request': '请求参数有误',
  'not_found': '对应数据不存在',

  // ── 上游 provider (worker → service 透传) ──
  'UPSTREAM_NOT_FOUND': '任务已过期或不存在',
  'UPSTREAM_EMPTY': '上游模型未返回结果, 请重试',
  'UPSTREAM_FAILED': '上游模型异常, 请重试',
  'MODERATION': '内容触发审核, 请修改提示词',
  'RATE_LIMIT': '调用过于频繁, 请稍后再试',
  'CANCELLED': '任务已取消',
  'LOST_OUTCOME': '任务结果丢失, 请重新提交',

  // ── 通用 ──
  'internal': '系统繁忙, 请稍后重试',
};

/// translate: 把任意 error 翻成可读消息. 不抛异常.
String translateError(Object error) {
  if (error is SocketException) {
    return '网络异常, 请检查连接后重试';
  }
  if (error is ApiError) {
    return _translateApiError(error);
  }
  // 兜底: 直接 toString — 有时是 plaintext "rate limit" 等已经可读的字符串.
  final s = error.toString();
  // 去掉 dart 默认前缀 "Exception: " 让消息更短.
  if (s.startsWith('Exception: ')) {
    return s.substring(11);
  }
  return s.isEmpty ? '系统繁忙, 请稍后重试' : s;
}

String _translateApiError(ApiError e) {
  // 401 短路: 提示 + 上层自动 refresh
  if (e.status == 401) return '登录已过期, 请重新登录';
  if (e.status == 403) return '没有权限执行该操作';

  // 解 body
  String? code;
  String? message;
  if (e.body.isNotEmpty) {
    try {
      final parsed = jsonDecode(e.body);
      if (parsed is Map<String, dynamic>) {
        final err = parsed['error'];
        if (err is Map<String, dynamic>) {
          code = err['code'] as String?;
          message = err['message'] as String?;
        }
      }
    } catch (_) {/* 非 JSON: 退回 status */}
  }

  if (code != null && _codeMap.containsKey(code)) {
    return _codeMap[code]!;
  }
  // 没匹配: 优先用 server message (中文场景下后端可能直翻), 否则按 status 兜底.
  if (message != null && message.isNotEmpty) {
    return message;
  }
  if (e.status >= 500) return '系统繁忙, 请稍后重试';
  if (e.status == 429) return '请求过于频繁, 请稍后再试';
  if (e.status == 404) return '资源不存在';
  if (e.status >= 400) return '请求参数有误';
  return '操作失败, 请稍后重试';
}
