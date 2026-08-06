// LocalGitWorktreeWorkspace — 真 git worktree 实现(M4-E:git 操作全走 daemon bridge)。
//
// 创建/销毁/diff/状态/变更枚举的 **git 调用全部经 CodeBridgeClient** 打到本地 biu serve
// 的 biumindkit/code/git(不再 spawn git;满足 Code-Design D1)。文件 IO(.biu-meta.json
// 读写、artifact 的 sha256/size、preview 生成的兜底读)仍走 dart:io —— 那是本机文件操作,
// 不是 git spawn,且 worktree 永远在本地。
//
// daemon 必需:bridge 在 WorkspaceManager.allocate 处已判非空才会构造本类;daemon 未起时
// 上层回退 PassthroughWorkspace。
//
// 失败处理:
//   - cwd 不是 git repo → 由 WorkspaceManager 先 gitRepoRoot 探测,失败走 Passthrough
//   - 分支同名已存在 → daemon 内加 -2 -3 ... 后缀(git.CreateWorktree)
//   - worktree 路径已存在(重启恢复)→ 读 .biu-meta.json idempotent 复用

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:logging/logging.dart';
import 'package:uuid/uuid.dart';

import '../data/code_bridge_client.dart';
import '../domain/artifact.dart';
import '../domain/workspace.dart';
import 'preview_generator.dart';

final _log = Logger('biumind.code.workspace');

class LocalGitWorktreeWorkspace implements Workspace {
  LocalGitWorktreeWorkspace({
    required this.bridge,
    required this.taskId,
    required this.shortId,
    required this.agent,
    required this.repoRoot,
    required this.worktreePath,
    required this.preferredBranchName,
    required this.metadata,
    this.baseRef,
    Map<String, String>? extraEnv,
  }) : _extraEnv = extraEnv ?? const {};

  /// 桌面 loopback 直连 daemon —— 所有 git 操作经它(不再 spawn git)。
  final CodeBridgeClient bridge;

  final String taskId;
  final String shortId;

  /// 'biu' / 'claude' / 'codex' — 用于分支命名
  final String agent;

  /// 主 git repo 的根目录(git worktree add 的 project_path)
  final String repoRoot;

  /// worktree 物理路径
  final String worktreePath;

  /// 期望的分支名(实际可能由 daemon 加 -N 后缀)
  final String preferredBranchName;

  /// worktree fork 的基 ref(分支/commit)。null = daemon 用当前 HEAD(G6)。
  final String? baseRef;

  /// 写入 .biu-meta.json 的元数据(prompt 首行 / agent / 创建时间)
  final Map<String, dynamic> metadata;

  final Map<String, String> _extraEnv;

  WorkspaceRef? _ref;
  bool _ready = false;

  @override
  WorkspaceRef get ref {
    if (_ref == null) {
      throw StateError('LocalGitWorktreeWorkspace.setup() 未完成, ref 不可用');
    }
    return _ref!;
  }

  @override
  String get effectiveCwd => worktreePath;

  @override
  Map<String, String> get effectiveEnv => _extraEnv;

  @override
  Future<void> setup() async {
    if (_ready) return;

    final wtDir = Directory(worktreePath);
    final metaFile = File('$worktreePath/.biu-meta.json');

    // ── idempotent 路径: 已存在的 worktree 直接复用 ──
    if (await wtDir.exists() && await metaFile.exists()) {
      try {
        final raw = await metaFile.readAsString();
        final m = jsonDecode(raw) as Map<String, dynamic>;
        _ref = WorkspaceRef(
          kind: WorkspaceKind.localGitWorktree,
          id: taskId,
          displayName: (m['branch'] as String?) ?? preferredBranchName,
          localPath: worktreePath,
          branchName: m['branch'] as String?,
          baseCommit: m['base_commit'] as String?,
          baseBranch: m['base_branch'] as String?,
          createdAt: DateTime.tryParse(m['created_at'] as String? ?? '') ??
              DateTime.now(),
        );
        _ready = true;
        _log.info('worktree resumed: $worktreePath');
        return;
      } catch (e) {
        _log.warning('failed to read existing meta, recreating: $e');
        // fall through, 重建
      }
    }

    // ── 创建(git 操作在 daemon 内:rev-parse base + 分支冲突后缀 + worktree add)──
    final created = await bridge.gitCreateWorktree(
      repoRoot,
      worktreePath,
      preferredBranchName,
      baseRef: baseRef,
    );

    // 写元数据(本机文件,dart:io)
    final fullMeta = <String, dynamic>{
      'task_id': taskId,
      'short_id': shortId,
      'agent': agent,
      'branch': created.branch,
      'base_commit': created.baseCommit,
      'base_branch': created.baseBranch,
      'created_at': DateTime.now().toIso8601String(),
      ...metadata,
    };
    await metaFile.writeAsString(jsonEncode(fullMeta));

    _ref = WorkspaceRef(
      kind: WorkspaceKind.localGitWorktree,
      id: taskId,
      displayName: created.branch,
      localPath: worktreePath,
      branchName: created.branch,
      baseCommit: created.baseCommit,
      baseBranch: created.baseBranch,
      createdAt: DateTime.now(),
    );
    _ready = true;
    _log.info(
        'worktree created: $worktreePath (branch=${created.branch}, base=${created.baseCommit})');
  }

  /// 释放 worktree。
  ///   keepBranch=true (默认): no-op, 用户日后能继续用 (UI 显示但不在跑)
  ///   keepBranch=false:        worktree remove + branch -D (经 daemon)
  @override
  Future<void> teardown({bool keepBranch = true}) async {
    if (keepBranch) return;
    await purge();
  }

  /// 强制清理 worktree + 删分支 + 删目录(git 操作经 daemon)。
  Future<void> purge() async {
    final branch = _ref?.branchName ?? preferredBranchName;
    try {
      await bridge.gitRemoveWorktree(repoRoot, worktreePath, branch);
    } catch (e) {
      _log.warning('worktree remove failed (ignored): $e');
    }
    // worktree remove --force 通常已删目录, 残留时兜底删一遍(本机 dart:io)。
    try {
      final dir = Directory(worktreePath);
      if (await dir.exists()) await dir.delete(recursive: true);
    } catch (_) {}
    _log.info('worktree purged: $worktreePath');
  }

  @override
  Future<String> diffSummary() async {
    final base = _ref?.baseBranch ?? _ref?.baseCommit;
    if (base == null || base.isEmpty) return '';
    try {
      final stats = await bridge.gitWorktreeDiffStats(worktreePath, base);
      if (stats.additions == 0 && stats.deletions == 0) return '';
      return '+${stats.additions} -${stats.deletions}';
    } catch (e) {
      return 'diff error: $e';
    }
  }

  @override
  Future<String> status() async {
    try {
      final files = await bridge.gitStatusFiles(worktreePath);
      // 还原 `git status --short` 风格的两列摘要(够用,本方法当前无热调用方)。
      return files
          .map((f) => '${f.staged ? f.status : ' '}${f.staged ? ' ' : f.status} ${f.path}')
          .join('\n');
    } catch (e) {
      return 'status error: $e';
    }
  }

  /// 收集 L1 元数据。两条路径合并(git 枚举经 daemon,文件 hash/preview 走本机):
  /// 1. changedFiles(base..HEAD) — tracked file 的 add/modify/delete
  /// 2. listUntracked — 新增 untracked 但未被 .gitignore 忽略的文件
  ///
  /// CSY 不变量:
  /// - CSY2: 只扫 worktree 内 (daemon 的 git -C worktreePath 保证)
  /// - CSY3: --exclude-standard 自动尊重 .gitignore + .git/info/exclude
  /// - CSY5: relPath 用 / 分隔, 永远相对 worktree
  ///
  /// 其他工具忽略: .biu-meta.json (我们自己写的元数据)。
  @override
  Future<List<Artifact>?> collectArtifacts({
    required String taskId,
    required DateTime taskCreatedAt,
  }) async {
    final base = _ref?.baseCommit;
    if (base == null || base.isEmpty) return const [];

    final out = <Artifact>[];
    final seen = <String>{}; // dedup by relPath
    const uuid = Uuid();
    final now = DateTime.now().toUtc();

    // ── 1. changedFiles base..HEAD ────────────────────────
    try {
      final changed = await bridge.gitChangedFiles(worktreePath, base);
      for (final c in changed) {
        final relPath = c.path;
        if (_shouldExclude(relPath)) continue;
        if (!seen.add(relPath)) continue;
        final op = switch (c.status.isEmpty ? ' ' : c.status[0]) {
          'A' => ArtifactOp.created,
          'D' => ArtifactOp.deleted,
          _ => ArtifactOp.modified,
        };
        final art = await _artifactFromRelPath(
          taskId: taskId,
          relPath: relPath,
          op: op,
          uuid: uuid,
          createdAt: now,
          allowMissing: op == ArtifactOp.deleted,
        );
        if (art != null) out.add(art);
      }
    } catch (e) {
      _log.fine('changedFiles failed: $e');
    }

    // ── 2. untracked 新文件 ────────────────────────────────
    try {
      final untracked = await bridge.gitListUntracked(worktreePath);
      for (final relPath in untracked) {
        if (_shouldExclude(relPath)) continue;
        if (!seen.add(relPath)) continue;
        final art = await _artifactFromRelPath(
          taskId: taskId,
          relPath: relPath,
          op: ArtifactOp.created,
          uuid: uuid,
          createdAt: now,
        );
        if (art != null) out.add(art);
      }
    } catch (e) {
      _log.fine('listUntracked failed: $e');
    }

    // ── 3. L2 preview 生成 ─────────────────────────────────
    // 单 artifact 失败不 abort 整批 (生成 preview 是 nice-to-have)。
    final gen = PreviewGenerator(bridge: bridge, worktreePath: worktreePath);
    final withPreview = <Artifact>[];
    for (final art in out) {
      final p = await gen.generate(art, baseCommit: base);
      if (p.isEmpty) {
        withPreview.add(art);
      } else {
        withPreview.add(art.copyWith(
          previewSummary: p.summary,
          previewDataB64: p.dataB64,
          previewMimeType: p.mimeType,
        ));
      }
    }
    return withPreview;
  }

  /// 工具排除清单 (CSY3 之外的额外保险)。
  bool _shouldExclude(String rel) {
    if (rel == '.biu-meta.json') return true;
    if (rel.startsWith('.git/')) return true;
    return false;
  }

  /// 给 relPath 算 sha256 / size / kind / mime, 包成 Artifact。
  /// 文件不存在 (delete op) 时跳过 hash, 仅留元数据。
  Future<Artifact?> _artifactFromRelPath({
    required String taskId,
    required String relPath,
    required ArtifactOp op,
    required Uuid uuid,
    required DateTime createdAt,
    bool allowMissing = false,
  }) async {
    final f = File('$worktreePath/$relPath');
    int size = 0;
    String sha = '';
    if (await f.exists()) {
      try {
        size = await f.length();
        if (size <= 64 * 1024 * 1024) {
          // <= 64MB 直接 sha256; 更大不算 (P2 改成 streaming)
          final bytes = await f.readAsBytes();
          sha = sha256.convert(bytes).toString();
        }
      } catch (e) {
        _log.fine('hash fail $relPath: $e');
      }
    } else if (!allowMissing) {
      return null;
    }
    final mime = _mimeFromPath(relPath);
    final kind = _kindFromPathAndMime(relPath, mime, isCodeDiff: op != ArtifactOp.created || _looksLikeCode(relPath));
    return Artifact(
      id: uuid.v4(),
      taskId: taskId,
      kind: kind,
      relPath: relPath,
      mimeType: mime,
      sizeBytes: size,
      sha256: sha,
      op: op,
      createdAt: createdAt,
    );
  }

  static String? _mimeFromPath(String p) {
    final ext = p.toLowerCase().split('.').last;
    return switch (ext) {
      'png' => 'image/png',
      'jpg' || 'jpeg' => 'image/jpeg',
      'gif' => 'image/gif',
      'webp' => 'image/webp',
      'svg' => 'image/svg+xml',
      'pdf' => 'application/pdf',
      'md' || 'markdown' => 'text/markdown',
      'txt' => 'text/plain',
      'csv' => 'text/csv',
      'json' => 'application/json',
      'mp3' => 'audio/mpeg',
      'wav' => 'audio/wav',
      'mp4' => 'video/mp4',
      'mov' => 'video/quicktime',
      'xlsx' =>
        'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet',
      _ => null,
    };
  }

  static bool _looksLikeCode(String p) {
    final ext = p.toLowerCase().split('.').last;
    const codeExt = {
      'dart', 'go', 'py', 'js', 'jsx', 'ts', 'tsx', 'rs', 'java', 'kt',
      'swift', 'cpp', 'c', 'h', 'hpp', 'rb', 'php', 'sh', 'sql', 'yaml',
      'yml', 'toml', 'lua', 'scala', 'cs', 'm', 'r', 'jl',
    };
    return codeExt.contains(ext);
  }

  static ArtifactKind _kindFromPathAndMime(
    String relPath,
    String? mime, {
    required bool isCodeDiff,
  }) {
    if (mime != null) {
      if (mime.startsWith('image/')) return ArtifactKind.image;
      if (mime.startsWith('audio/')) return ArtifactKind.audio;
      if (mime.startsWith('video/')) return ArtifactKind.video;
      if (mime == 'text/csv' || mime.contains('spreadsheet')) {
        return ArtifactKind.dataset;
      }
      if (mime == 'application/pdf' ||
          mime.startsWith('text/markdown') ||
          mime == 'text/plain') {
        return ArtifactKind.document;
      }
    }
    if (isCodeDiff) return ArtifactKind.codeFile;
    return ArtifactKind.binary;
  }
}
