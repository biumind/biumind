// PinSuggestionRepo — 记录 "添加到侧边栏" 提示的去抖时间戳。
//
// 设计 §10A.3 "Toast 快捷固定"：用户错过/dismiss 一次后 7 天内不再
// 对同一 identifier 再提示，避免烦扰；7 天后允许再提示一次。
//
// 复用 FlutterSecureStorage 仅因为项目其他偏好都走它；这条数据本身
// 不敏感，存明文 LocalStorage / Keychain 都行。失败一律静默吞掉
// (再次提示一两次没真正损害)。

import 'dart:convert';

import 'package:flutter/foundation.dart' show debugPrint;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';

class PinSuggestionRepo {
  PinSuggestionRepo({FlutterSecureStorage? storage})
      : _storage = storage ?? const FlutterSecureStorage();
  final FlutterSecureStorage _storage;
  static const _key = 'biumind.pin_suggestion_dismissed';
  static const Duration ttl = Duration(days: 7);

  /// 是否对该 identifier 提示。已 dismiss 且 < 7 天 → false。
  Future<bool> shouldShow(String identifier) async {
    final map = await _read();
    final ts = map[identifier];
    if (ts == null) return true;
    final dismissedAt = DateTime.tryParse(ts);
    if (dismissedAt == null) return true;
    return DateTime.now().difference(dismissedAt) > ttl;
  }

  /// 标记当前时刻已"看到/操作过"该 identifier 的提示。下次 < 7 天
  /// 不再提示。
  Future<void> dismiss(String identifier) async {
    final map = await _read();
    map[identifier] = DateTime.now().toUtc().toIso8601String();
    await _write(map);
  }

  Future<Map<String, String>> _read() async {
    try {
      final raw = await _storage.read(key: _key);
      if (raw == null || raw.isEmpty) return {};
      final j = jsonDecode(raw);
      if (j is! Map) return {};
      return {for (final e in j.entries) e.key.toString(): e.value.toString()};
    } catch (e) {
      debugPrint('PinSuggestionRepo._read failed: $e');
      return {};
    }
  }

  Future<void> _write(Map<String, String> map) async {
    try {
      await _storage.write(key: _key, value: jsonEncode(map));
    } catch (e) {
      debugPrint('PinSuggestionRepo._write failed: $e');
    }
  }
}

final pinSuggestionRepoProvider =
    Provider<PinSuggestionRepo>((_) => PinSuggestionRepo());
