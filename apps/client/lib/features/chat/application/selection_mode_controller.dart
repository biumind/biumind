// SelectionMode —— chat 列表多选状态机。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md P0-3。
//
// 入选时：每条消息行渲染前置 Checkbox，点击 row 切换选中；底部出操作 bar
// (复制 / 导出 MD / 删除 / 取消)。退出时清空。
// 单一全局态：同一时间只在一个 thread 上多选；切换 thread 自动 exit。

import 'package:flutter_riverpod/flutter_riverpod.dart';

class SelectionModeState {
  const SelectionModeState({
    this.active = false,
    this.threadId,
    this.ids = const {},
  });

  final bool active;
  final String? threadId;
  final Set<String> ids;

  bool contains(String id) => ids.contains(id);
  int get count => ids.length;

  SelectionModeState copyWith({
    bool? active,
    String? threadId,
    Set<String>? ids,
    bool clearThread = false,
  }) {
    return SelectionModeState(
      active: active ?? this.active,
      threadId: clearThread ? null : (threadId ?? this.threadId),
      ids: ids ?? this.ids,
    );
  }
}

class SelectionModeNotifier extends StateNotifier<SelectionModeState> {
  SelectionModeNotifier() : super(const SelectionModeState());

  void enter(String threadId) {
    state = SelectionModeState(active: true, threadId: threadId, ids: const {});
  }

  void exit() {
    state = const SelectionModeState();
  }

  void toggle(String id) {
    final cur = state.ids;
    final next = {...cur};
    if (cur.contains(id)) {
      next.remove(id);
    } else {
      next.add(id);
    }
    state = state.copyWith(ids: next);
  }

  void selectAll(Iterable<String> ids) {
    state = state.copyWith(ids: ids.toSet());
  }

  void clearSelection() {
    state = state.copyWith(ids: const {});
  }

  void onThreadChanged(String? newThreadId) {
    if (!state.active) return;
    if (state.threadId == newThreadId) return;
    state = const SelectionModeState();
  }
}

final selectionModeProvider =
    StateNotifierProvider<SelectionModeNotifier, SelectionModeState>(
  (ref) => SelectionModeNotifier(),
);
