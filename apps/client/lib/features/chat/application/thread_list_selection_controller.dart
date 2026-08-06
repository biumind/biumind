// ThreadListSelection —— 对话列表（线程级）批量选择状态机。
//
// 与 selection_mode_controller.dart 的区别：那个是 thread *内* 的消息多选；
// 这个是 sidebar 对话*列表*的线程多选（批量删除）。语义不同，故独立 provider，
// 不复用，避免两套交互互相串台。
//
// 入选时：sidebar header 切换为选择工具条（退出 / 全选 / 已选 N），每个
// ThreadTile 前置 Checkbox，点击切换选中而非导航；底部出批量操作条（删除）。
// 退出 / 完成删除后清空。单一全局态。

import 'package:flutter_riverpod/flutter_riverpod.dart';

class ThreadListSelectionState {
  const ThreadListSelectionState({
    this.active = false,
    this.ids = const {},
  });

  final bool active;
  final Set<String> ids;

  bool contains(String id) => ids.contains(id);
  int get count => ids.length;

  ThreadListSelectionState copyWith({bool? active, Set<String>? ids}) {
    return ThreadListSelectionState(
      active: active ?? this.active,
      ids: ids ?? this.ids,
    );
  }
}

class ThreadListSelectionNotifier
    extends StateNotifier<ThreadListSelectionState> {
  ThreadListSelectionNotifier() : super(const ThreadListSelectionState());

  void enter() {
    state = const ThreadListSelectionState(active: true, ids: {});
  }

  void exit() {
    state = const ThreadListSelectionState();
  }

  void toggle(String id) {
    final next = {...state.ids};
    if (!next.remove(id)) next.add(id);
    state = state.copyWith(ids: next);
  }

  /// 全选给定 id 集合（当前可见 / 过滤后的线程）。
  void selectAll(Iterable<String> ids) {
    state = state.copyWith(ids: ids.toSet());
  }

  void clearSelection() {
    state = state.copyWith(ids: const {});
  }
}

final threadListSelectionProvider = StateNotifierProvider<
    ThreadListSelectionNotifier, ThreadListSelectionState>(
  (ref) => ThreadListSelectionNotifier(),
);
