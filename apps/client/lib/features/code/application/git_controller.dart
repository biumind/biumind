// GitController —— 编码模块 Git 面板的状态 + 动作(M4-C)。
//
// 真相源是本地 git 仓库;所有操作经 CodeBridgeClient 打到 daemon 的 biumindkit/code/git
// (git -C <cwd>)。无 daemon / 无活动项目 → disabled 态(UI 给提示)。
//
// 设计:一次 refresh 拉 statusFiles + 当前分支 + remoteCounts;选中文件按需拉 diff;
// 历史(log)惰性加载,选中提交拉 commitDetail。动作(stage/commit/push...)后自动 refresh。

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/code_bridge_client.dart';
import '../data/code_bridge_provider.dart';
import '../domain/git_models.dart';
import 'projects_controller.dart';

/// Git 面板状态。不可变,copyWith 更新。
class GitState {
  const GitState({
    this.staged = const [],
    this.unstaged = const [],
    this.branch = '',
    this.counts = GitRemoteCounts.empty,
    this.loading = false,
    this.error,
    this.selectedPath,
    this.selectedStaged = false,
    this.diff,
    this.diffLoading = false,
    this.committing = false,
    this.generatingMsg = false,
    this.pushing = false,
    this.history = const [],
    this.historyLoading = false,
    this.selectedCommit,
    this.commitDetail,
    this.commitDiff,
  });

  final List<GitFileChange> staged;
  final List<GitFileChange> unstaged;
  final String branch;
  final GitRemoteCounts counts;
  final bool loading;
  final String? error;

  /// 当前在 diff 视图选中的工作区文件 + 是否看暂存区版本。
  final String? selectedPath;
  final bool selectedStaged;
  final String? diff;
  final bool diffLoading;

  final bool committing;
  final bool generatingMsg;
  final bool pushing;

  /// 历史(惰性)。
  final List<GitCommit> history;
  final bool historyLoading;
  final String? selectedCommit; // hash
  final GitCommitDetail? commitDetail;
  final String? commitDiff;

  bool get clean => staged.isEmpty && unstaged.isEmpty;
  bool get hasStaged => staged.isNotEmpty;

  GitState copyWith({
    List<GitFileChange>? staged,
    List<GitFileChange>? unstaged,
    String? branch,
    GitRemoteCounts? counts,
    bool? loading,
    Object? error = _sentinel,
    Object? selectedPath = _sentinel,
    bool? selectedStaged,
    Object? diff = _sentinel,
    bool? diffLoading,
    bool? committing,
    bool? generatingMsg,
    bool? pushing,
    List<GitCommit>? history,
    bool? historyLoading,
    Object? selectedCommit = _sentinel,
    Object? commitDetail = _sentinel,
    Object? commitDiff = _sentinel,
  }) {
    return GitState(
      staged: staged ?? this.staged,
      unstaged: unstaged ?? this.unstaged,
      branch: branch ?? this.branch,
      counts: counts ?? this.counts,
      loading: loading ?? this.loading,
      error: error == _sentinel ? this.error : error as String?,
      selectedPath:
          selectedPath == _sentinel ? this.selectedPath : selectedPath as String?,
      selectedStaged: selectedStaged ?? this.selectedStaged,
      diff: diff == _sentinel ? this.diff : diff as String?,
      diffLoading: diffLoading ?? this.diffLoading,
      committing: committing ?? this.committing,
      generatingMsg: generatingMsg ?? this.generatingMsg,
      pushing: pushing ?? this.pushing,
      history: history ?? this.history,
      historyLoading: historyLoading ?? this.historyLoading,
      selectedCommit: selectedCommit == _sentinel
          ? this.selectedCommit
          : selectedCommit as String?,
      commitDetail: commitDetail == _sentinel
          ? this.commitDetail
          : commitDetail as GitCommitDetail?,
      commitDiff:
          commitDiff == _sentinel ? this.commitDiff : commitDiff as String?,
    );
  }

  static const _sentinel = Object();
}

class GitController extends StateNotifier<GitState> {
  GitController({required this.bridge, required this.cwd})
      : super(const GitState());

  final CodeBridgeClient? bridge;
  final String? cwd;

  bool get _ready => bridge != null && cwd != null && cwd!.isNotEmpty;

  /// 拉工作区状态 + 分支 + ahead/behind。动作后内部也调它。
  Future<void> refresh() async {
    if (!_ready) {
      state = state.copyWith(
          error: 'daemon 未就绪或未选择项目', loading: false, staged: const [], unstaged: const []);
      return;
    }
    state = state.copyWith(loading: true, error: null);
    try {
      final files = await bridge!.gitStatusFiles(cwd!);
      final staged = files.where((f) => f.staged).toList();
      final unstaged = files.where((f) => !f.staged).toList();
      // 分支 + counts 容错:非 git 仓 / 无上游不致命。
      String branch = '';
      var counts = GitRemoteCounts.empty;
      try {
        final st = await bridge!.gitStatus(cwd!);
        branch = st['branch'] as String? ?? '';
        counts = await bridge!.gitRemoteCounts(cwd!);
      } catch (_) {/* 非致命 */}
      state = state.copyWith(
        staged: staged,
        unstaged: unstaged,
        branch: branch,
        counts: counts,
        loading: false,
      );
      // 当前选中文件若还在改动里,刷新它的 diff;否则清掉。
      final sel = state.selectedPath;
      if (sel != null && files.any((f) => f.path == sel)) {
        await selectFile(sel, state.selectedStaged);
      } else if (sel != null) {
        state = state.copyWith(selectedPath: null, diff: null);
      }
    } catch (e) {
      state = state.copyWith(loading: false, error: _msg(e));
    }
  }

  /// 选中工作区文件并拉它的 diff。
  Future<void> selectFile(String path, bool staged) async {
    if (!_ready) return;
    state = state.copyWith(
        selectedPath: path, selectedStaged: staged, diffLoading: true);
    try {
      final isUntracked = state.unstaged
          .followedBy(state.staged)
          .any((f) => f.path == path && f.isUntracked);
      // 未跟踪文件没有暂存 diff,强制 staged=false 走 --no-index 回退。
      final d = await bridge!
          .gitFileDiff(cwd!, path, staged: staged && !isUntracked);
      state = state.copyWith(diff: d, diffLoading: false);
    } catch (e) {
      state = state.copyWith(diff: '加载 diff 失败: ${_msg(e)}', diffLoading: false);
    }
  }

  Future<void> stage(List<String> paths) => _do(() => bridge!.gitStage(cwd!, paths));
  Future<void> unstage(List<String> paths) =>
      _do(() => bridge!.gitUnstage(cwd!, paths));
  Future<void> stageAll() => _do(() => bridge!.gitStageAll(cwd!));
  Future<void> unstageAll() => _do(() => bridge!.gitUnstageAll(cwd!));

  Future<void> discardFile(GitFileChange f) =>
      _do(() => bridge!.gitDiscardFile(cwd!, f.path, untracked: f.isUntracked));
  Future<void> discardAll() => _do(() => bridge!.gitDiscardAll(cwd!));

  /// 提交。成功返回 true;失败置 error 返回 false(UI 据此保留输入框内容)。
  Future<bool> commit(String message) async {
    if (!_ready || message.trim().isEmpty) return false;
    state = state.copyWith(committing: true, error: null);
    try {
      await bridge!.gitCommit(cwd!, message);
      state = state.copyWith(committing: false);
      await refresh();
      return true;
    } catch (e) {
      state = state.copyWith(committing: false, error: _msg(e));
      return false;
    }
  }

  /// AI 生成 commit message(走 model-relay)。返回生成文本;失败置 error 返回 null。
  Future<String?> generateCommitMessage() async {
    if (!_ready) return null;
    state = state.copyWith(generatingMsg: true, error: null);
    try {
      final msg = await bridge!.gitGenerateCommitMessage(cwd!);
      state = state.copyWith(generatingMsg: false);
      return msg;
    } catch (e) {
      state = state.copyWith(generatingMsg: false, error: _msg(e));
      return null;
    }
  }

  /// 推送。成功返回 git 输出;失败置 error 返回 null。
  Future<String?> push({String? branch}) async {
    if (!_ready) return null;
    state = state.copyWith(pushing: true, error: null);
    try {
      final out = await bridge!.gitPush(cwd!, branch: branch);
      state = state.copyWith(pushing: false);
      await refresh();
      return out;
    } catch (e) {
      state = state.copyWith(pushing: false, error: _msg(e));
      return null;
    }
  }

  Future<String?> pull() async {
    if (!_ready) return null;
    state = state.copyWith(pushing: true, error: null);
    try {
      final out = await bridge!.gitPull(cwd!);
      state = state.copyWith(pushing: false);
      await refresh();
      return out;
    } catch (e) {
      state = state.copyWith(pushing: false, error: _msg(e));
      return null;
    }
  }

  /// 惰性加载历史(打开 History 视图时调)。
  Future<void> loadHistory({int limit = 50}) async {
    if (!_ready) return;
    state = state.copyWith(historyLoading: true, error: null);
    try {
      final log = await bridge!.gitLog(cwd!, limit: limit);
      state = state.copyWith(history: log, historyLoading: false);
    } catch (e) {
      state = state.copyWith(historyLoading: false, error: _msg(e));
    }
  }

  /// 选中历史提交,拉详情 + 整体 diff。
  Future<void> selectCommit(String hash) async {
    if (!_ready) return;
    state = state.copyWith(
        selectedCommit: hash, commitDetail: null, commitDiff: null);
    try {
      final detail = await bridge!.gitCommitDetail(cwd!, hash);
      final diff = await bridge!.gitShowDiff(cwd!, hash);
      // 选中可能在 await 期间变了,丢弃过期结果。
      if (state.selectedCommit != hash) return;
      state = state.copyWith(commitDetail: detail, commitDiff: diff);
    } catch (e) {
      if (state.selectedCommit != hash) return;
      state = state.copyWith(commitDiff: '加载提交失败: ${_msg(e)}');
    }
  }

  /// 跑一个动作后 refresh。失败置 error。
  Future<void> _do(Future<void> Function() action) async {
    if (!_ready) return;
    try {
      await action();
      await refresh();
    } catch (e) {
      state = state.copyWith(error: _msg(e));
    }
  }

  static String _msg(Object e) =>
      e is CodeBridgeException ? (e.error ?? e.method) : e.toString();
}

/// 随活动项目 + bridge 重建;初始自动 refresh。
final gitControllerProvider =
    StateNotifierProvider.autoDispose<GitController, GitState>((ref) {
  final bridge = ref.watch(codeBridgeClientProvider);
  final project = ref.watch(activeCodeProjectProvider);
  final c = GitController(bridge: bridge, cwd: project?.path);
  if (bridge != null && project != null) {
    // 构造后下一 microtask 拉一次(避免在 build 期间改 state)。
    Future.microtask(c.refresh);
  }
  return c;
});
