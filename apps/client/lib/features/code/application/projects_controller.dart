// CodeProjectsController — 编码模块多项目状态(M1)。
//
// 编码模块的 projects / activeProject 状态,用 Riverpod +
// Drift(零云同步,CodeProjectsDao 即 SoT)。ProjectRail / WelcomePage 消费它。

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:uuid/uuid.dart';

import '../data/code_projects_dao.dart';
import '../domain/code_task.dart';
import '../domain/project.dart';
import 'tasks_controller.dart';

class CodeProjectsController extends StateNotifier<List<CodeProject>> {
  CodeProjectsController(this._dao) : super(const []) {
    _hydrate();
  }

  final CodeProjectsDao _dao;
  final _uuid = const Uuid();
  bool _hydrated = false;

  /// hydrate 是否完成 —— UI 启动闪烁(空列表 vs 真无项目)判定用。
  bool get hydrated => _hydrated;

  Future<void> _hydrate() async {
    state = await _dao.loadAll();
    _hydrated = true;
    state = [...state]; // 触发一次通知,让监听 hydrated 的 UI 重建
  }

  /// 添加项目(目录绝对路径)。已存在同 path 的项目 → 直接 touch 并返回它(不重复建)。
  Future<CodeProject> addProjectByPath(String path, {String? name}) async {
    final existing = _byPath(path);
    if (existing != null) {
      await touch(existing.id);
      return existing;
    }
    // 新项目排到最前:取当前最小 sortIndex - 1(sortIndex 升序排列)。
    final minIndex =
        state.isEmpty ? 0 : state.map((p) => p.sortIndex).reduce((a, b) => a < b ? a : b);
    final proj = CodeProject(
      id: _uuid.v4(),
      name: name ?? _basename(path),
      path: path,
      lastOpenedAt: DateTime.now(),
      sortIndex: minIndex - 1,
    );
    await _dao.upsert(proj);
    state = [proj, ...state];
    return proj;
  }

  /// 拖拽重排可见(非隐藏)项目。oldIndex/newIndex 为 railProjects 列表内的下标
  /// (onReorderItem 约定:newIndex 已完成移除项修正)。隐藏项目保持相对序排到末尾,
  /// 整体重写 sortIndex 持久化。
  Future<void> reorderVisible(int oldIndex, int newIndex) async {
    final visible = state.where((p) => !p.hiddenFromRail).toList();
    if (oldIndex < 0 || oldIndex >= visible.length) return;
    final moved = visible.removeAt(oldIndex);
    visible.insert(newIndex.clamp(0, visible.length), moved);
    final hidden = state.where((p) => p.hiddenFromRail).toList();
    final full = [...visible, ...hidden];
    await _dao.reorder(full.map((p) => p.id).toList());
    state = [
      for (var i = 0; i < full.length; i++) full[i].copyWith(sortIndex: i),
    ];
  }

  /// 更新最后打开时间(切到该项目时调)并重排(最近在前)。
  Future<void> touch(String id) async {
    final now = DateTime.now();
    await _dao.touch(id, now);
    state = [
      for (final p in state)
        if (p.id == id) p.copyWith(lastOpenedAt: now) else p,
    ]..sort((a, b) => b.lastOpenedAt.compareTo(a.lastOpenedAt));
  }

  /// 更新项目的当前分支(BranchBar best-effort 从 git.status 刷新后回写)。
  Future<void> setBranch(String id, String branch) async {
    final idx = state.indexWhere((p) => p.id == id);
    if (idx < 0 || state[idx].branch == branch) return;
    final updated = state[idx].copyWith(branch: branch);
    await _dao.upsert(updated);
    state = [
      for (final p in state)
        if (p.id == id) updated else p,
    ];
  }

  /// 从 Rail 隐藏/取消隐藏(不删项目)。
  Future<void> setHidden(String id, bool hidden) async {
    await _dao.setHidden(id, hidden);
    state = [
      for (final p in state)
        if (p.id == id) p.copyWith(hiddenFromRail: hidden) else p,
    ];
  }

  /// 彻底删除项目(任务归属另行处理:M1 不级联删任务)。
  Future<void> remove(String id) async {
    await _dao.deleteById(id);
    state = state.where((p) => p.id != id).toList();
  }

  CodeProject? _byPath(String path) {
    for (final p in state) {
      if (p.path == path) return p;
    }
    return null;
  }

  static String _basename(String path) {
    final cleaned =
        path.endsWith('/') || path.endsWith(r'\') ? path.substring(0, path.length - 1) : path;
    final parts = cleaned.split(RegExp(r'[/\\]'));
    return parts.isEmpty || parts.last.isEmpty ? cleaned : parts.last;
  }
}

// ─── Providers ──────────────────────────────────────────

final codeProjectsControllerProvider =
    StateNotifierProvider<CodeProjectsController, List<CodeProject>>((ref) {
  return CodeProjectsController(ref.watch(codeProjectsDaoProvider));
});

/// 当前激活的项目 id(null = 无项目,显示 WelcomePage)。
final activeCodeProjectIdProvider = StateProvider<String?>((_) => null);

/// 当前激活的项目对象(id 失效时返回 null)。
final activeCodeProjectProvider = Provider<CodeProject?>((ref) {
  final id = ref.watch(activeCodeProjectIdProvider);
  if (id == null) return null;
  for (final p in ref.watch(codeProjectsControllerProvider)) {
    if (p.id == id) return p;
  }
  return null;
});

/// Rail 上可见的项目(排除 hiddenFromRail)。
final railProjectsProvider = Provider<List<CodeProject>>((ref) {
  return ref
      .watch(codeProjectsControllerProvider)
      .where((p) => !p.hiddenFromRail)
      .toList();
});

/// 当前激活项目下的任务(M1 严格按 projectId 过滤,任务必属项目)。
/// 无激活项目 → 空列表。pre-M1 的 null-projectId 老任务不在任何项目下显示。
final projectScopedCodeTasksProvider = Provider<List<CodeTask>>((ref) {
  return scopeTasksToProject(
    ref.watch(codeTasksProvider),
    ref.watch(activeCodeProjectIdProvider),
  );
});

/// 纯过滤:取属于 activeId 项目的任务。activeId==null → 空。提取成顶层函数便于单测
/// (provider 依赖重型 codeTasksProvider,难直接测)。
List<CodeTask> scopeTasksToProject(List<CodeTask> all, String? activeId) {
  if (activeId == null) return const [];
  return all.where((t) => t.projectId == activeId).toList();
}
