// 应用中心 HTTP 错误统一映射到用户友好文案。
//
// 之前各页面 catch (e) 直接 `e.toString()` 作 SnackBar 内容, 用户看到
// "ApiError 409 /v1/apps/installs: {\"error\":\"version_conflict\"}",
// 没法理解。统一走 humanizeAppsError(): 按 HTTP status 路由到 i18n
// 文案, 4xx 4xx 5xx 各类有专属语句, 不识别的 fallback appsErrUnknown
// 仍带原始消息以便排错。

import 'dart:convert';

import 'package:flutter/widgets.dart';

import '../../../data/api/_http_helpers.dart';
import '../../../l10n/app_localizations.dart';

/// 把任意异常映射成可展示给用户的中文/英文文案。
String humanizeAppsError(BuildContext context, Object error) {
  final l10n = AppLocalizations.of(context)!;
  if (error is ApiError) {
    final parsed = _ServerError.parse(error.body);
    final detail = parsed.message;
    switch (error.status) {
      case 400:
      case 422:
        return l10n.appsErrValidation(detail.isEmpty ? '${error.status}' : detail);
      case 401:
        return l10n.appsErrUnauthorized;
      case 403:
        // 403 现在还区分子语义: not_installed / install_disabled / permission_denied
        // 都是 invoke 鉴权链拦下的 (invoke 路径 P0 修复后的输出); 给单独
        // 文案让用户知道"为什么被拦"。
        switch (parsed.code) {
          case 'not_installed':
            return l10n.appsErrNotInstalled;
          case 'install_disabled':
            return l10n.appsErrInstallDisabled;
        }
        return l10n.appsErrForbidden;
      case 404:
      case 410:
        return l10n.appsErrNotFound;
      case 409:
        return l10n.appsErrConflict;
      case 429:
        return l10n.appsErrRateLimit;
      default:
        if (error.status >= 500) {
          // app 业务错误被服务端包成 invoke_failed 走 500 (api.go
          // handleInvoke default branch); message 里有 "tasks: tsk-x
          // not found" 之类的人话, 比 "服务暂时不可用 (500)" 有用得多。
          // strip "<app>: " 前缀让消息纯净。
          if (parsed.code == 'invoke_failed' && detail.isNotEmpty) {
            return l10n.appsErrUnknown(_stripAppPrefix(detail));
          }
          return l10n.appsErrServer('${error.status}');
        }
        return l10n.appsErrUnknown(detail.isEmpty ? '${error.status}' : detail);
    }
  }
  // 网络层 / 解析层错误（无 ApiError 包裹）—— 多半是连接异常或超时。
  final raw = error.toString();
  if (_looksLikeNetworkError(raw)) return l10n.appsErrNetwork;
  return l10n.appsErrUnknown(raw);
}

/// 解析 server 错误 body 的 nested 结构。app_center 用
/// `{"error": {"code": "...", "message": "..."}}`; 其他服务可能 flat
/// `{"error": "..."}` 或 `{"message": "..."}` — 一并兼容。
class _ServerError {
  final String code;
  final String message;
  const _ServerError({required this.code, required this.message});

  static _ServerError parse(String body) {
    if (body.isEmpty) return const _ServerError(code: '', message: '');
    try {
      final decoded = jsonDecode(body);
      if (decoded is Map) {
        // app_center 风格: nested error 对象
        final err = decoded['error'];
        if (err is Map) {
          final code = err['code'];
          final msg = err['message'];
          return _ServerError(
            code: code is String ? code : '',
            message: msg is String && msg.isNotEmpty
                ? msg
                : (code is String ? code : ''),
          );
        }
        // 兼容 flat 风格
        if (err is String && err.isNotEmpty) {
          return _ServerError(code: '', message: err);
        }
        final m = decoded['message'] ?? decoded['detail'];
        if (m is String && m.isNotEmpty) {
          return _ServerError(code: '', message: m);
        }
      }
    } catch (_) {/* not json */}
    // 截断原始 body 防止 SnackBar 撑爆。
    final truncated =
        body.length > 120 ? '${body.substring(0, 120)}…' : body;
    return _ServerError(code: '', message: truncated);
  }
}

/// 去掉 app 业务错误前缀 `rss:` / `tasks:` 等; 用户看到的应该是事实
/// 而不是命名空间。
String _stripAppPrefix(String msg) {
  final colon = msg.indexOf(':');
  if (colon <= 0 || colon > 24) return msg;
  // 仅 lowercase + alpha 才认作 app 前缀, 防止 "Failed: ..." 这种被砍。
  final prefix = msg.substring(0, colon);
  for (final c in prefix.codeUnits) {
    if (c < 0x61 || c > 0x7a) {
      // not lowercase a-z
      if (c != 0x5f && (c < 0x30 || c > 0x39)) return msg;
    }
  }
  return msg.substring(colon + 1).trimLeft();
}

bool _looksLikeNetworkError(String s) {
  final lower = s.toLowerCase();
  return lower.contains('socketexception') ||
      lower.contains('connection refused') ||
      lower.contains('connection closed') ||
      lower.contains('timeout') ||
      lower.contains('network is unreachable') ||
      lower.contains('failed host lookup') ||
      lower.contains('handshake');
}
