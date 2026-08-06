// PendingScroll —— 跨会话搜索 / 引用回链等场景下，要求 MessageListV2 切到
// 某条 thread 后再 ensureVisible 到指定 messageId 的"挂起请求"。
//
// 模型：
//   * StateProvider 单值：当前是否有挂起请求 + 目标 (threadId, messageId)
//   * MessageListV2 build 中 ref.listen，threadId 匹配时滚到 message + 一闪
//     高亮 + 调 consume() 清空
//
// 之所以做单值（而不是 per-thread family）：跨会话跳转一次只有一个目标，
// 而且消息列表组件 ref.watch 单值开销可忽略。

import 'package:flutter_riverpod/flutter_riverpod.dart';

class PendingScroll {
  const PendingScroll({required this.threadId, required this.messageId});
  final String threadId;
  final String messageId;
}

class PendingScrollNotifier extends StateNotifier<PendingScroll?> {
  PendingScrollNotifier() : super(null);

  void request(String threadId, String messageId) {
    // 重置后再设让 ref.listen 在请求相同目标时也能触发。
    state = null;
    state = PendingScroll(threadId: threadId, messageId: messageId);
  }

  void consume() {
    if (state != null) state = null;
  }
}

final pendingScrollProvider =
    StateNotifierProvider<PendingScrollNotifier, PendingScroll?>(
  (ref) => PendingScrollNotifier(),
);
