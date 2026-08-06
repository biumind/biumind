// PassthroughWorkspace — 不隔离, adapter 直接用 settings.codeWorkingDir。
//
// 何时使用:
//   1. settings.codeUseWorktree = false (用户主动关闭隔离)
//   2. cwd 不是 git repo (无法创建 worktree)
//   3. 任意失败的 fallback

import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:uuid/uuid.dart';

import '../domain/artifact.dart';
import '../domain/workspace.dart';
import 'preview_generator.dart';

/// 共享的可变预算计数器, 让递归过程跨 frame 修改同一个 int。
class _Budget {
  _Budget(this.value);
  int value;
}

class PassthroughWorkspace implements Workspace {
  PassthroughWorkspace({required this.cwd, required this.taskId, this.reason});

  final String cwd;
  final String taskId;

  /// 为啥用 passthrough (UI 可显示警告 banner)
  final String? reason;

  @override
  WorkspaceRef get ref => WorkspaceRef(
        kind: WorkspaceKind.passthrough,
        id: taskId,
        displayName: 'cwd',
        localPath: cwd,
        createdAt: DateTime.now(),
      );

  @override
  String get effectiveCwd => cwd;

  @override
  Map<String, String> get effectiveEnv => const {};

  @override
  Future<void> setup() async {/* no-op */}

  @override
  Future<void> teardown({bool keepBranch = true}) async {/* no-op */}

  @override
  Future<String> diffSummary() async => '(passthrough — 无隔离)';

  @override
  Future<String> status() async => '(passthrough)';

  /// Passthrough 模式下保守扫: 仅看 cwd 下 mtime > task.createdAt 的文件,
  /// 当成"任务创建出来的产物"。带一堆约束防止误扫敏感目录:
  ///
  /// - 边界: 严格在 cwd 内, 不出此目录 (CSY2)
  /// - 拒绝高风险根目录: HOME / / / /etc / /tmp 等系统级路径不扫
  /// - 跳过常见噪音目录 (.git / node_modules / build / .venv / vendor 等)
  /// - 跳过 dotfile 目录 (.aws / .ssh / .gnupg / .docker 等敏感配置)
  /// - 文件名匹配 isSensitivePath 时仅留 L1 元数据 (sha256 / size),
  ///   preview generator 已经会跳过 (CSY6)
  /// - 限制深度 ≤ 8, 限制单次最多 200 文件 (避免巨大目录卡 UI)
  /// - 限制单文件 ≤ 64 MB 才算 sha256
  ///
  /// 比 LocalGitWorktreeWorkspace 弱: 没法拿 git diff, 所以代码文件
  /// preview 走 "前 N 行 fallback"; .gitignore 不能用, 改硬编码排除清单。
  @override
  Future<List<Artifact>?> collectArtifacts({
    required String taskId,
    required DateTime taskCreatedAt,
  }) async {
    // ── 安全闸门: 危险根目录直接拒绝扫 ────────────────────────
    if (!_safeRoot(cwd)) return const [];

    final root = Directory(cwd);
    if (!await root.exists()) return const [];

    final out = <Artifact>[];
    const uuid = Uuid();
    final now = DateTime.now().toUtc();
    final cwdNormalized = root.absolute.path;
    // cwd = HOME 时收紧扫描深度 (HOME 一般有海量子树, 限制 3 层 + 黑名单
    // 子目录避免漏扫 ~/Library/Caches 这类无关的 OS 缓存)
    final isHome = cwdNormalized == (Platform.environment['HOME'] ?? '');
    final maxDepth = isHome ? 3 : 8;

    // 手动 DFS — 不用 Directory.list(recursive: true), 那货遇到 macOS
    // TCC 保护目录 (Library/Application Support/CallHistoryTransactions
    // 等) 整流抛 PathAccessException, 之前的 e.g. Library 黑名单根本来
    // 不及生效 (list 先 stream 完整子树才被消费方过滤)。这里手写递归,
    // 进入子目录前就 _excludedPath 拦, 单个 entry 抛错只跳本条不阻塞
    // 整批。
    await _scan(
      dir: root,
      relPrefix: '',
      depth: 0,
      maxDepth: maxDepth,
      cwdNormalized: cwdNormalized,
      taskCreatedAtUtc: taskCreatedAt.toUtc(),
      uuid: uuid,
      now: now,
      taskId: taskId,
      out: out,
      remainingBudget: _Budget(200),
    );
    return out;
  }

  Future<void> _scan({
    required Directory dir,
    required String relPrefix,
    required int depth,
    required int maxDepth,
    required String cwdNormalized,
    required DateTime taskCreatedAtUtc,
    required Uuid uuid,
    required DateTime now,
    required String taskId,
    required List<Artifact> out,
    required _Budget remainingBudget,
  }) async {
    if (remainingBudget.value <= 0) return;
    if (depth > maxDepth) return;
    List<FileSystemEntity> children;
    try {
      children = await dir.list(followLinks: false).toList();
    } catch (_) {
      // EPERM / EACCES 等读不了的 — 整个目录跳, 不影响其他兄弟
      return;
    }
    for (final ent in children) {
      if (remainingBudget.value <= 0) return;
      final name = ent.uri.pathSegments
          .lastWhere((s) => s.isNotEmpty, orElse: () => '');
      if (name.isEmpty) continue;
      final rel = relPrefix.isEmpty ? name : '$relPrefix/$name';
      if (_excludedPath(rel)) continue;

      // 边界守护: 不出 cwd
      final abs = ent.absolute.path;
      if (!abs.startsWith(cwdNormalized)) continue;

      late FileStat stat;
      try {
        stat = await ent.stat();
      } catch (_) {
        continue;
      }
      if (stat.type == FileSystemEntityType.directory) {
        await _scan(
          dir: Directory(abs),
          relPrefix: rel,
          depth: depth + 1,
          maxDepth: maxDepth,
          cwdNormalized: cwdNormalized,
          taskCreatedAtUtc: taskCreatedAtUtc,
          uuid: uuid,
          now: now,
          taskId: taskId,
          out: out,
          remainingBudget: remainingBudget,
        );
        continue;
      }
      if (stat.type != FileSystemEntityType.file) continue;
      // mtime 过滤
      if (!stat.modified.toUtc().isAfter(taskCreatedAtUtc)) continue;

      String sha = '';
      if (stat.size <= 64 * 1024 * 1024) {
        try {
          final bytes = await File(abs).readAsBytes();
          sha = sha256.convert(bytes).toString();
        } catch (_) {/* keep sha empty */}
      }
      out.add(_buildArtifact(
        taskId: taskId,
        relPath: rel,
        size: stat.size,
        sha: sha,
        uuid: uuid,
        createdAt: now,
      ));
      remainingBudget.value -= 1;
    }
  }

  /// 危险根目录黑名单. 用户配 cwd = / / /etc 等都被拦。
  /// HOME 不一刀切 ban — mtime 过滤已经把老文件全 skip, 敏感子目录
  /// (.aws/.ssh/.gnupg/.docker/.kube/.gcloud/.azure) 在 _excludedPath
  /// 里跳, 加 maxDepth=3 限深, 风险可控。
  static bool _safeRoot(String path) {
    final p = path.replaceFirst(RegExp(r'/+$'), '');
    if (p.isEmpty || p == '/') return false;
    const banned = ['/etc', '/usr', '/sys', '/proc', '/root', '/dev'];
    for (final b in banned) {
      if (p == b || p.startsWith('$b/')) return false;
    }
    return true;
  }

  /// 路径排除规则. 命中即跳。
  static bool _excludedPath(String rel) {
    final segs = rel.split('/');
    for (final s in segs) {
      if (s.isEmpty) continue;
      // 跳常见噪音 + 敏感子目录
      const skipDirs = {
        '.git', '.hg', '.svn',
        'node_modules', '.pnpm-store', 'bower_components',
        '.venv', 'venv', '__pycache__', '.pytest_cache',
        'target', 'build', 'dist',
        // 'out' 不加: AI agent 经常把生成图 / 文档写到 out/, 收 artifact
        // 比省噪音重要。Rust 写 target/, Go 写 bin/, JS 写 dist/.
        '.dart_tool', '.gradle', '.idea', '.vscode',
        'vendor',
        // 敏感配置目录
        '.aws', '.ssh', '.gnupg', '.docker', '.kube', '.gcloud', '.azure',
        // 用户级缓存 / OS 元数据 (cwd=HOME 时尤其重要)
        'Library', 'Applications',
        '.cache', '.local', '.config',
        '.npm', '.pnpm', '.yarn', '.cargo', '.rustup', '.pub-cache',
        '.android', '.swiftpm', '.cocoapods',
        '.dartServer', '.continue', '.vscode-server',
        'Trash', '.Trash', '.Trashes',
        // AI 工具本地态
        '.biu', '.claude', '.trae-cn', '.agents', '.cursor',
        // 其他常见后台 app 私有目录 (任务期间常被静默写)
        '.beacon', '.workbuddy', '.copilot', '.windsurfp',
        // 用户级目录: Documents/Desktop/Downloads 是个人文件不算"任务产物";
        // Movies/Music/Pictures/Public 是媒体目录, 同理。用户想把 task 产物
        // 放这些目录可改 cwd 进具体子文件夹。
        'Documents', 'Desktop', 'Downloads',
        'Movies', 'Music', 'Pictures', 'Public',
      };
      if (skipDirs.contains(s)) return true;
    }
    if (rel == '.biu-meta.json') return true;
    // 文件级别噪音
    final base = rel.split('/').last;
    if (base == '.DS_Store' || base == 'Thumbs.db' || base == 'desktop.ini') {
      return true;
    }
    return false;
  }

  Artifact _buildArtifact({
    required String taskId,
    required String relPath,
    required int size,
    required String sha,
    required Uuid uuid,
    required DateTime createdAt,
  }) {
    final mime = _mimeFromPath(relPath);
    final kind = _kindFromMime(mime, relPath);
    return Artifact(
      id: uuid.v4(),
      taskId: taskId,
      kind: kind,
      relPath: relPath,
      mimeType: mime,
      sizeBytes: size,
      sha256: sha,
      op: ArtifactOp.created,
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
      'pdf' => 'application/pdf',
      'md' || 'markdown' => 'text/markdown',
      'txt' => 'text/plain',
      'csv' => 'text/csv',
      'json' => 'application/json',
      'mp3' => 'audio/mpeg',
      'wav' => 'audio/wav',
      'mp4' => 'video/mp4',
      _ => null,
    };
  }

  static ArtifactKind _kindFromMime(String? mime, String rel) {
    if (PreviewGenerator.isSensitivePath(rel)) return ArtifactKind.binary;
    if (mime != null) {
      if (mime.startsWith('image/')) return ArtifactKind.image;
      if (mime.startsWith('audio/')) return ArtifactKind.audio;
      if (mime.startsWith('video/')) return ArtifactKind.video;
      if (mime == 'text/csv') return ArtifactKind.dataset;
      if (mime == 'application/pdf' ||
          mime.startsWith('text/markdown') ||
          mime == 'text/plain') {
        return ArtifactKind.document;
      }
    }
    // 简单的代码扩展名判断
    final ext = rel.toLowerCase().split('.').last;
    const codeExt = {
      'dart', 'go', 'py', 'js', 'jsx', 'ts', 'tsx', 'rs', 'java', 'kt',
      'swift', 'cpp', 'c', 'h', 'hpp', 'rb', 'php', 'sh', 'sql', 'yaml',
      'yml', 'toml',
    };
    if (codeExt.contains(ext)) return ArtifactKind.codeFile;
    return ArtifactKind.binary;
  }
}
