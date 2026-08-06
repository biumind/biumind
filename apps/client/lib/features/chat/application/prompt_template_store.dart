// PromptTemplateStore —— system prompt 模板收藏。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 模板）。
//
// 用户常用的 system prompt（Flutter 架构师 / 译者 / debug 助手 / ...）
// 收藏到本地 SharedPreferences，应用到当前 thread 的 systemPrompt。
//
// 持久化：单一 JSON 数组在 key `biu.chat.prompt_templates`。
//   [{"id":"...","name":"...","content":"..."}, ...]
//
// 不同步到 brain（个人偏好），不同设备间不共享 —— 类似 draft history。

import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:uuid/uuid.dart';

const _kPrefsKey = 'biu.chat.prompt_templates';

class PromptTemplate {
  const PromptTemplate({
    required this.id,
    required this.name,
    required this.content,
  });
  final String id;
  final String name;
  final String content;

  Map<String, dynamic> toJson() => {
        'id': id,
        'name': name,
        'content': content,
      };

  static PromptTemplate? fromJson(Map<String, dynamic> j) {
    final id = j['id'] as String?;
    final name = j['name'] as String?;
    final content = j['content'] as String?;
    if (id == null || name == null || content == null) return null;
    return PromptTemplate(id: id, name: name, content: content);
  }

  PromptTemplate copyWith({String? name, String? content}) =>
      PromptTemplate(
        id: id,
        name: name ?? this.name,
        content: content ?? this.content,
      );
}

class PromptTemplateNotifier
    extends StateNotifier<List<PromptTemplate>> {
  PromptTemplateNotifier() : super(const []) {
    _load();
  }

  static const _uuid = Uuid();

  Future<void> _load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString(_kPrefsKey);
      if (raw == null) return;
      final decoded = jsonDecode(raw);
      if (decoded is! List) return;
      final list = <PromptTemplate>[];
      for (final e in decoded) {
        if (e is Map<String, dynamic>) {
          final t = PromptTemplate.fromJson(e);
          if (t != null) list.add(t);
        }
      }
      state = list;
    } catch (_) {/* corrupted prefs → 起空列表 */}
  }

  Future<void> _persist(List<PromptTemplate> list) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(
        _kPrefsKey,
        jsonEncode(list.map((t) => t.toJson()).toList()),
      );
    } catch (_) {/* fail silent */}
  }

  /// 新建一个模板，返回新 id。
  Future<String> create({required String name, required String content}) async {
    final id = _uuid.v4();
    final next = [
      ...state,
      PromptTemplate(id: id, name: name, content: content),
    ];
    state = next;
    await _persist(next);
    return id;
  }

  /// 更新已有模板（按 id）。
  Future<void> update(String id,
      {String? name, String? content}) async {
    final next = state
        .map((t) => t.id == id ? t.copyWith(name: name, content: content) : t)
        .toList(growable: false);
    state = next;
    await _persist(next);
  }

  Future<void> remove(String id) async {
    final next = state.where((t) => t.id != id).toList(growable: false);
    state = next;
    await _persist(next);
  }
}

final promptTemplatesProvider =
    StateNotifierProvider<PromptTemplateNotifier, List<PromptTemplate>>(
  (ref) => PromptTemplateNotifier(),
);
