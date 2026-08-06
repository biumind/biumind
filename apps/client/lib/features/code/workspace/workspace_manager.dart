// WorkspaceManager — 任务 → workspace 的分配 / 销毁 / 孤儿扫描。
//
// 职责:
//   - allocate(taskId, agent, prompt) → 创建 LocalGitWorktree (优先) /
//     PassthroughWorkspace (fallback)
//   - release(taskId) → teardown (默认 keepBranch=true 不删数据)
//   - purge(taskId) → 强制清理 worktree + 删分支 + 删目录
//   - 孤儿扫描 (P1 加): 启动时遍历 worktrees 目录, db 里没的标记 dangling

import 'dart:async';
import 'dart:io';

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:logging/logging.dart';
import 'package:uuid/uuid.dart';

import '../../../services/login_shell_env.dart';
import '../../../services/settings_repo.dart' show AppSettings;
import '../../settings/application/settings_controller.dart';
import '../data/code_bridge_client.dart';
import '../data/code_bridge_provider.dart';
import '../domain/code_task.dart' show AgentKind, CodeLaunchMode;
import '../domain/workspace.dart';
import 'local_git_worktree.dart';
import 'passthrough_workspace.dart';

final _log = Logger('biumind.code.workspace_manager');

class WorkspaceManager {
  WorkspaceManager(this._ref) : _uuid = const Uuid();
  final Ref _ref;
  final Uuid _uuid;

  /// 已 setup 的 workspace, key = task_id。重启 app 后从 db 恢复重建。
  final Map<String, Workspace> _active = {};

  /// 分配一个 workspace 给指定任务。
  ///
  /// 优先尝试 LocalGitWorktree, 失败 (cwd 不是 git repo / settings 关闭隔离) 时
  /// fallback PassthroughWorkspace。setup() 已在内部跑完, caller 直接拿到 ready
  /// 状态的 workspace。
  Future<Workspace> allocate({
    required String taskId,
    required AgentKind agent,
    required String promptFirstLine,
    CodeLaunchMode launchMode = CodeLaunchMode.auto,
    String? baseRef,
  }) async {
    final settings =
        _ref.read(settingsControllerProvider).valueOrNull ?? const AppSettings();
    final shellEnv = _ref.read(loginShellEnvProvider).valueOrNull;
    final pe = kIsWeb ? const <String, String>{} : Platform.environment;

    final cwd = (settings.codeWorkingDir != null && settings.codeWorkingDir!.isNotEmpty)
        ? settings.codeWorkingDir!
        : (pe['HOME'] ?? '.');

    // 本任务是否要 worktree 隔离:auto 跟随全局设置;local/worktree 为 per-task 覆盖。
    final wantWorktree = switch (launchMode) {
      CodeLaunchMode.local => false,
      CodeLaunchMode.worktree => true,
      CodeLaunchMode.auto => settings.codeUseWorktree,
    };
    if (!wantWorktree) {
      return _passthrough(taskId, cwd,
          reason: launchMode == CodeLaunchMode.local
              ? '本任务选择「本地直跑」, 在 cwd 直接执行'
              : '用户已在 settings 关闭任务隔离');
    }

    // Web / 不支持 spawn 的平台 → passthrough
    if (kIsWeb) {
      return _passthrough(taskId, cwd, reason: 'Web 端暂不支持 worktree 隔离');
    }

    // M4-E:worktree 的 git 操作经 daemon bridge(不再 spawn git)。daemon 未起 →
    // 无法建隔离 → passthrough(daemon 通常与 UI 并行启动,这里是边界兜底)。
    final bridge = _ref.read(codeBridgeClientProvider);
    if (bridge == null) {
      return _passthrough(taskId, cwd,
          reason: '本地 daemon 未就绪, 任务隔离不可用(任务跑在 cwd 直接执行)');
    }

    // cwd 不是 git repo → passthrough + 在 UI 警告
    final repoRoot = await _findGitRepoRoot(bridge, cwd);
    if (repoRoot == null) {
      return _passthrough(
        taskId,
        cwd,
        reason: '$cwd 不是 git repository, 任务跑在 cwd 直接执行（可能互相覆盖）',
      );
    }

    // ── 走 LocalGitWorktree 主路径 ──
    final shortId = _shortId();
    final agentSlug = agent.name.toLowerCase();
    final preferredBranch = 'biu/$agentSlug-$shortId';

    final home = pe['HOME'] ?? shellEnv?.env['HOME'] ?? '.';
    final worktreePath = '$home/.biumind/code/worktrees/$taskId';

    final ws = LocalGitWorktreeWorkspace(
      bridge: bridge,
      taskId: taskId,
      shortId: shortId,
      agent: agentSlug,
      repoRoot: repoRoot,
      worktreePath: worktreePath,
      preferredBranchName: preferredBranch,
      baseRef: baseRef,
      metadata: {
        'prompt_first_line': promptFirstLine,
      },
    );

    try {
      await ws.setup();
      _active[taskId] = ws;
      return ws;
    } catch (e, st) {
      _log.warning('LocalGitWorktree setup failed, fallback passthrough', e, st);
      return _passthrough(
        taskId,
        cwd,
        reason: 'worktree 创建失败: $e (任务跑在 cwd 直接执行)',
      );
    }
  }

  /// 释放 — 默认保留数据。
  Future<void> release(String taskId, {bool keepBranch = true}) async {
    final ws = _active.remove(taskId);
    if (ws == null) return;
    try {
      await ws.teardown(keepBranch: keepBranch);
    } catch (e) {
      _log.warning('teardown failed (ignored): $e');
    }
  }

  /// 强制清理 — 删 worktree + 删分支 + 删目录。
  Future<void> purge(String taskId) async {
    final ws = _active.remove(taskId);
    if (ws is LocalGitWorktreeWorkspace) {
      await ws.purge();
    }
  }

  Workspace? lookup(String taskId) => _active[taskId];

  // ─── 内部 ───────────────────────────────────────────────

  Workspace _passthrough(String taskId, String cwd, {String? reason}) {
    final ws = PassthroughWorkspace(taskId: taskId, cwd: cwd, reason: reason);
    _active[taskId] = ws;
    return ws;
  }

  /// 经 daemon 找 git repo 根(git rev-parse --show-toplevel)。null = 不在 repo 内
  /// 或 daemon 报错。
  Future<String?> _findGitRepoRoot(CodeBridgeClient bridge, String startPath) async {
    try {
      final root = await bridge.gitRepoRoot(startPath);
      return root.isEmpty ? null : root;
    } catch (_) {
      return null;
    }
  }

  String _shortId() {
    final raw = _uuid.v4().replaceAll('-', '');
    return raw.substring(0, 8);
  }
}

final workspaceManagerProvider = Provider<WorkspaceManager>((ref) {
  return WorkspaceManager(ref);
});
