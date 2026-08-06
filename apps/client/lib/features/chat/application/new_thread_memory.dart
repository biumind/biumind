// NewThreadMemory —— 记 NewThreadDialog 上次输入的字段，下次打开预填。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 NewThreadDialog
// 字段记忆）。
//
// 当前记忆字段：
//   * systemPrompt：上次输入的 system prompt 文本（chat 模式）
//   * poolTag：上次输入的 pool tag（task 模式）
//
// title / mode / model 不记 —— title 应当每次新建是空白；mode / model 走
// chat_preferences.dart 的"默认值"路径。
//
// SharedPreferences 单一 JSON key 存这两个字段，体积可控。

import 'dart:convert';

import 'package:shared_preferences/shared_preferences.dart';

const _kKey = 'biu.chat.new_thread.memory';

class NewThreadMemory {
  const NewThreadMemory({this.systemPrompt = '', this.poolTag = ''});
  final String systemPrompt;
  final String poolTag;

  Map<String, dynamic> toJson() => {
        'systemPrompt': systemPrompt,
        'poolTag': poolTag,
      };

  static NewThreadMemory fromJson(Map<String, dynamic> j) {
    return NewThreadMemory(
      systemPrompt: (j['systemPrompt'] as String?) ?? '',
      poolTag: (j['poolTag'] as String?) ?? '',
    );
  }
}

class NewThreadMemoryStore {
  static Future<NewThreadMemory> load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString(_kKey);
      if (raw == null) return const NewThreadMemory();
      final decoded = jsonDecode(raw);
      if (decoded is Map<String, dynamic>) {
        return NewThreadMemory.fromJson(decoded);
      }
    } catch (_) {/* corrupt → 起默认 */}
    return const NewThreadMemory();
  }

  static Future<void> save(NewThreadMemory m) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_kKey, jsonEncode(m.toJson()));
    } catch (_) {}
  }
}
