// 任务工作区域抽象 — 让 adapter 不感知本地 worktree vs 云端 sandbox。
//
// 三种实现:
//   - PassthroughWorkspace      cwd 不是 git repo / 用户关闭隔离 → 直接用
//                               settings.codeWorkingDir, 无隔离
//   - LocalGitWorktreeWorkspace P0+ 默认 → git worktree + 独立分支
//   - CloudSandboxWorkspace     P5 远期 → BiuMind sandbox 服务起 K8s pod
//
// adapter 永远只通过 [Workspace.effectiveCwd] / [Workspace.effectiveEnv]
// 与文件系统打交道, 实现切换零代码改动 (云端时 effectiveCwd 是本地代理
// 挂载点, 路径形态跟本地一致)。

import 'package:flutter/foundation.dart';

import 'artifact.dart';

enum WorkspaceKind { passthrough, localGitWorktree, cloudSandbox }

/// Workspace 的不可变快照 — 持久化进 task / 跨 session 序列化都用这个。
@immutable
class WorkspaceRef {
  const WorkspaceRef({
    required this.kind,
    required this.id,
    required this.displayName,
    required this.localPath,
    this.branchName,
    this.baseCommit,
    this.baseBranch,
    required this.createdAt,
  });

  final WorkspaceKind kind;
  final String id;

  /// 用户可见标识 — local: 分支名 / cloud: pod 名 / passthrough: 'cwd'
  final String displayName;

  /// adapter spawn 子进程时的 cwd (绝对路径)。永远是 adapter 当下能用的本地
  /// 路径 (云端时是 FUSE / sshfs 挂载点)。
  final String localPath;

  /// LocalGitWorktree 时填: 自动创建的分支名 (e.g. biu/claude-a3f81e2c)
  final String? branchName;

  /// LocalGitWorktree 时填: worktree 创建瞬间 base 提交的 sha (用于 diff 对比)
  final String? baseCommit;

  /// LocalGitWorktree 时填: 创建时主 repo 的当前分支 (用户后续切回 main 后, 仍
  /// 能知道这个 worktree 是从哪 fork 出来的)
  final String? baseBranch;

  final DateTime createdAt;

  Map<String, dynamic> toJson() => {
        'kind': kind.name,
        'id': id,
        'display_name': displayName,
        'local_path': localPath,
        if (branchName != null) 'branch_name': branchName,
        if (baseCommit != null) 'base_commit': baseCommit,
        if (baseBranch != null) 'base_branch': baseBranch,
        'created_at': createdAt.toIso8601String(),
      };

  factory WorkspaceRef.fromJson(Map<String, dynamic> j) => WorkspaceRef(
        kind: WorkspaceKind.values.firstWhere(
          (k) => k.name == j['kind'],
          orElse: () => WorkspaceKind.passthrough,
        ),
        id: j['id'] as String,
        displayName: j['display_name'] as String,
        localPath: j['local_path'] as String,
        branchName: j['branch_name'] as String?,
        baseCommit: j['base_commit'] as String?,
        baseBranch: j['base_branch'] as String?,
        createdAt:
            DateTime.tryParse(j['created_at'] as String? ?? '') ?? DateTime.now(),
      );
}

/// 运行时 workspace 接口 — 持有 ref + 提供生命周期 + 状态查询能力。
abstract class Workspace {
  WorkspaceRef get ref;

  /// adapter spawn 子进程时的 cwd
  String get effectiveCwd => ref.localPath;

  /// adapter 注入子进程的额外 env (在 BiuAdapter 等的 extraEnv 之上叠加)
  Map<String, String> get effectiveEnv => const {};

  /// 创建 / 恢复 — 必须 idempotent (重启 app 后同一 ref 调一次 setup() 不重新创建)
  Future<void> setup();

  /// 释放。keepBranch=true 默认只关闭运行时资源, 不删数据。
  Future<void> teardown({bool keepBranch = true});

  /// 摘要 (UI 状态条 / 任务列表用)
  Future<String> diffSummary();

  /// `git status` 等
  Future<String> status();

  /// 任务结束时收集产物 (L1 元数据)。返回 null 表示该 workspace 类型
  /// 不支持收集 (如 passthrough — 共享用户 tree 时扫文件风险太高,
  /// 跑到 ~/.aws/credentials 这类敏感路径会出事)。
  ///
  /// 设计: docs/BiuMind-Code-Artifacts-Sync-Design.md §4.1
  Future<List<Artifact>?> collectArtifacts({
    required String taskId,
    required DateTime taskCreatedAt,
  }) async => null;
}
