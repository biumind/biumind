// InThreadSearch —— Cmd+F 线程内搜索状态机。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md（v2 补）。
//
// state：
//   * open: 是否显示搜索栏
//   * query: 当前查询字符串（trim 后；空 = 没命中也没高亮）
//   * hits: 命中的 messageId 列表（按消息顺序）
//   * currentIndex: 当前定位到第几个命中（0-based）；空时 -1
//
// 输入：
//   * open() / close() 切换显示
//   * setQuery(q): 重新计算 hits + reset currentIndex 到 0
//   * setMessages(msgs): 消息列表变了（streaming 增量 / 切 thread）→ 刷新 hits
//   * next() / prev(): 切换 currentIndex
//
// 不存消息内容本身 —— 调用方提供消息列表，hits 只放 messageId。
//
// per-thread family：每个 thread 自己的搜索态，不共享。

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../domain/chat_models.dart';

class InThreadSearchState {
  const InThreadSearchState({
    this.open = false,
    this.query = '',
    this.hits = const [],
    this.currentIndex = -1,
  });

  final bool open;
  final String query;
  final List<String> hits;
  final int currentIndex;

  String? get currentMessageId =>
      currentIndex < 0 || currentIndex >= hits.length
          ? null
          : hits[currentIndex];

  bool messageHasHit(String id) => hits.contains(id);

  InThreadSearchState copyWith({
    bool? open,
    String? query,
    List<String>? hits,
    int? currentIndex,
  }) {
    return InThreadSearchState(
      open: open ?? this.open,
      query: query ?? this.query,
      hits: hits ?? this.hits,
      currentIndex: currentIndex ?? this.currentIndex,
    );
  }
}

/// 在 messages 里按 query（大小写不敏感）查 assembledText 命中的消息 id。
/// 空 query 返回空列表。
List<String> computeHits(List<Message> messages, String query) {
  final q = query.trim().toLowerCase();
  if (q.isEmpty) return const [];
  final hits = <String>[];
  for (final m in messages) {
    if (m.role == MessageRole.toolResult) continue;
    final text = m.assembledText.toLowerCase();
    if (text.contains(q)) hits.add(m.id);
  }
  return hits;
}

class InThreadSearchNotifier extends StateNotifier<InThreadSearchState> {
  InThreadSearchNotifier() : super(const InThreadSearchState());

  /// 缓存最近一次 setMessages 传来的消息列表，让 setQuery 不需要再让调用方
  /// 传一次。每次 messages 流更新时会再次推进。
  List<Message> _messages = const [];

  void open() {
    state = state.copyWith(open: true);
  }

  void close() {
    state = const InThreadSearchState();
  }

  void toggle() {
    if (state.open) {
      close();
    } else {
      open();
    }
  }

  void setMessages(List<Message> msgs) {
    _messages = msgs;
    if (state.query.isEmpty) return;
    final hits = computeHits(msgs, state.query);
    final idx = hits.isEmpty
        ? -1
        : (state.currentMessageId != null &&
                hits.contains(state.currentMessageId)
            // 命中列表里还有当前 hit → 保持位置
            ? hits.indexOf(state.currentMessageId!)
            : 0);
    state = state.copyWith(hits: hits, currentIndex: idx);
  }

  void setQuery(String q) {
    final hits = computeHits(_messages, q);
    state = state.copyWith(
      query: q,
      hits: hits,
      currentIndex: hits.isEmpty ? -1 : 0,
    );
  }

  void next() {
    if (state.hits.isEmpty) return;
    final n = (state.currentIndex + 1) % state.hits.length;
    state = state.copyWith(currentIndex: n);
  }

  void prev() {
    if (state.hits.isEmpty) return;
    final n = state.currentIndex <= 0
        ? state.hits.length - 1
        : state.currentIndex - 1;
    state = state.copyWith(currentIndex: n);
  }
}

final inThreadSearchProvider = StateNotifierProvider.family<
    InThreadSearchNotifier, InThreadSearchState, String>(
  (ref, threadId) => InThreadSearchNotifier(),
);
