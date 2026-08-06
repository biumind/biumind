// 主区打开的文件 Tab 状态(点文件树/搜索结果 → 主区开编辑器 Tab)。
//
// 文件树(右栏)与搜索弹窗往这里 push 打开请求,主区的 FileEditorTabs 读它渲染
// Tab 栏 + 当前文件内容。有文件打开 → 主区显编辑器;清空 → 回到任务会话回放。
//
// 仅持开文件的绝对路径列表 + 当前激活路径,不持内容(内容由各 Tab 视图自取)。
// 切项目时由工作台调 clear() 清掉跨项目残留 Tab。

import 'package:flutter_riverpod/flutter_riverpod.dart';

/// 主区聚焦目标:会话回放 / 文件编辑器。点任务 → session;开/点文件 → files。
/// 显式 provider(而非靠 active 变化推断),以便「重选同一文件」也能切回文件区。
enum MainFocus { session, files }

final mainFocusProvider =
    StateProvider<MainFocus>((ref) => MainFocus.session);

class OpenFilesState {
  const OpenFilesState({this.paths = const [], this.active});

  /// 打开的文件绝对路径,按打开先后排序。
  final List<String> paths;

  /// 当前激活(主区显示)的文件路径;null = 无打开文件。
  final String? active;

  bool get isEmpty => paths.isEmpty;
}

class OpenFilesController extends StateNotifier<OpenFilesState> {
  OpenFilesController() : super(const OpenFilesState());

  /// 打开(或聚焦已打开的)文件。
  void open(String path) {
    if (state.paths.contains(path)) {
      state = OpenFilesState(paths: state.paths, active: path);
      return;
    }
    state = OpenFilesState(paths: [...state.paths, path], active: path);
  }

  /// 关闭一个 Tab。关的是激活 Tab 时,激活就近的相邻 Tab。
  void close(String path) {
    final idx = state.paths.indexOf(path);
    if (idx < 0) return;
    final next = [...state.paths]..removeAt(idx);
    String? active = state.active;
    if (state.active == path) {
      if (next.isEmpty) {
        active = null;
      } else {
        active = next[idx.clamp(0, next.length - 1)];
      }
    }
    state = OpenFilesState(paths: next, active: active);
  }

  void setActive(String path) {
    if (!state.paths.contains(path)) return;
    state = OpenFilesState(paths: state.paths, active: path);
  }

  void clear() => state = const OpenFilesState();
}

final openFilesProvider =
    StateNotifierProvider<OpenFilesController, OpenFilesState>(
        (ref) => OpenFilesController());
