// DraftHistory + ComposerInject —— 输入框附属状态机。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md P0-6。
//
// DraftHistory：
//   * 每次成功发送后 push(text)；空白 / 与栈顶相同时跳过；超过 maxItems 截断。
//   * 浏览模式：cursor=null 表示未浏览；按 ↑ 进入浏览，cursor=0 = 最近一条。
//   * ↓ 减小 cursor；从 0 退出浏览并清空 text。
//   * 任何由用户主动改写 text（不是从 history 注入）应当 resetCursor()。
//
// ComposerInject：
//   * 一次性信号 —— 任意位置（消息 hover bar、citation 卡）调 inject(text)，
//     ComposerV2 监听后把文本插入光标处并 consume()。
//
// 持久化：SharedPreferences 单一全局栈，不分 thread。

import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

const _kPrefsKey = 'biu.chat.composer.draft_history';
const _kMaxItems = 20;

class DraftHistoryState {
  const DraftHistoryState({
    this.history = const [],
    this.cursor,
    this.loaded = false,
  });

  /// 最近一条在 index 0。
  final List<String> history;
  /// 当前浏览到的 index；null = 未浏览。
  final int? cursor;
  final bool loaded;

  DraftHistoryState copyWith({
    List<String>? history,
    int? cursor,
    bool resetCursor = false,
    bool? loaded,
  }) {
    return DraftHistoryState(
      history: history ?? this.history,
      cursor: resetCursor ? null : (cursor ?? this.cursor),
      loaded: loaded ?? this.loaded,
    );
  }
}

class DraftHistoryNotifier extends StateNotifier<DraftHistoryState> {
  DraftHistoryNotifier() : super(const DraftHistoryState()) {
    _load();
  }

  Future<void> _load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString(_kPrefsKey);
      if (raw == null) {
        state = state.copyWith(loaded: true);
        return;
      }
      final decoded = jsonDecode(raw);
      if (decoded is List) {
        final list = decoded.whereType<String>().toList();
        state = state.copyWith(history: list, loaded: true);
      } else {
        state = state.copyWith(loaded: true);
      }
    } catch (_) {
      state = state.copyWith(loaded: true);
    }
  }

  Future<void> _persist(List<String> list) async {
    try {
      final prefs = await SharedPreferences.getInstance();
      await prefs.setString(_kPrefsKey, jsonEncode(list));
    } catch (_) {/* fail silent */}
  }

  void push(String text) {
    final t = text.trim();
    if (t.isEmpty) return;
    final cur = state.history;
    if (cur.isNotEmpty && cur.first == t) {
      state = state.copyWith(resetCursor: true);
      return;
    }
    final next = <String>[t, ...cur.where((e) => e != t)];
    final trimmed =
        next.length > _kMaxItems ? next.sublist(0, _kMaxItems) : next;
    state = state.copyWith(history: trimmed, resetCursor: true);
    _persist(trimmed);
  }

  void resetCursor() {
    if (state.cursor != null) {
      state = state.copyWith(resetCursor: true);
    }
  }

  /// ↑：返回新文本（null = 没有更老的可显示）。
  String? prev() {
    final hist = state.history;
    if (hist.isEmpty) return null;
    final cur = state.cursor;
    final next = cur == null ? 0 : (cur + 1).clamp(0, hist.length - 1);
    if (next == cur) return null;
    state = state.copyWith(cursor: next);
    return hist[next];
  }

  /// ↓：返回新文本；空字符串 = 退出浏览（清空输入框）；null = 没在浏览。
  String? next() {
    final cur = state.cursor;
    if (cur == null) return null;
    if (cur == 0) {
      state = state.copyWith(resetCursor: true);
      return '';
    }
    final n = cur - 1;
    state = state.copyWith(cursor: n);
    return state.history[n];
  }

  Future<void> clear() async {
    state = state.copyWith(history: const [], resetCursor: true);
    await _persist(const []);
  }
}

final draftHistoryProvider =
    StateNotifierProvider<DraftHistoryNotifier, DraftHistoryState>(
  (ref) => DraftHistoryNotifier(),
);

/// 引用回复 / 外部注入消息到 composer 的一次性信号。
class ComposerInjectNotifier extends StateNotifier<String?> {
  ComposerInjectNotifier() : super(null);

  void inject(String text) {
    if (text.isEmpty) return;
    // 同字符串再次 inject 时 listener 不重新触发；先清掉再设。
    state = null;
    state = text;
  }

  void consume() {
    if (state != null) state = null;
  }
}

final composerInjectProvider =
    StateNotifierProvider<ComposerInjectNotifier, String?>(
  (ref) => ComposerInjectNotifier(),
);
