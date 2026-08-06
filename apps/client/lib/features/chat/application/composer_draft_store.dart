// ComposerDraftStore —— 每个 thread 自己的 composer 草稿。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 per-thread 草稿）。
//
// 行为：
//   * 每个 threadId 独立草稿，SharedPreferences key biu.chat.draft.{threadId}
//   * load(threadId) 异步取草稿，无 → 返空字符串
//   * save(threadId, text) 立即写入；caller 自己在 onTextChanged 里 debounce
//   * clear(threadId) 删除该 thread 的草稿（发送成功后调用）
//
// 不引入 Riverpod state —— composer 自己 await 即可，开销可忽略。

import 'package:shared_preferences/shared_preferences.dart';

class ComposerDraftStore {
  static String _key(String threadId) => 'biu.chat.draft.$threadId';

  static Future<String> load(String threadId) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      return prefs.getString(_key(threadId)) ?? '';
    } catch (_) {
      return '';
    }
  }

  static Future<void> save(String threadId, String text) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      if (text.isEmpty) {
        await prefs.remove(_key(threadId));
      } else {
        await prefs.setString(_key(threadId), text);
      }
    } catch (_) {/* fail silent — 本地缓存丢了不致命 */}
  }

  static Future<void> clear(String threadId) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.remove(_key(threadId));
    } catch (_) {}
  }

  /// 列所有 thread 当前草稿。返回 `Map<threadId, content>`。
  /// 草稿索引侧栏用：用户能快速看到"哪些 thread 我还有半成品"。
  static Future<Map<String, String>> listAll() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final result = <String, String>{};
      const prefix = 'biu.chat.draft.';
      for (final key in prefs.getKeys()) {
        if (!key.startsWith(prefix)) continue;
        final tid = key.substring(prefix.length);
        if (tid.isEmpty) continue;
        final v = prefs.getString(key);
        if (v == null || v.isEmpty) continue;
        result[tid] = v;
      }
      return result;
    } catch (_) {
      return const {};
    }
  }
}
