// Git 领域模型 —— 与 biumindkit/code/git 的 Go 结构 1:1 对齐(snake_case JSON)。
// 纯数据 + fromJson,不依赖 Flutter / drift,便于单测。

/// 工作区里一个文件的变更项(对应 Go git.FileChange)。同一文件若既有暂存又有
/// 未暂存改动,会出现两条(staged=true / false 各一)。
class GitFileChange {
  const GitFileChange({
    required this.path,
    required this.status,
    required this.staged,
  });

  final String path;

  /// 单字符状态:M(改)/A(增)/D(删)/R(改名)/C(复制)/?(未跟踪)。
  final String status;
  final bool staged;

  bool get isUntracked => status == '?';

  factory GitFileChange.fromJson(Map<String, dynamic> j) => GitFileChange(
        path: j['path'] as String? ?? '',
        status: j['status'] as String? ?? '',
        staged: j['staged'] as bool? ?? false,
      );
}

/// 一条分支(本地或远程)。对应 Go git.Branch。
class GitBranch {
  const GitBranch({
    required this.name,
    required this.current,
    required this.remote,
  });

  final String name;
  final bool current;

  /// 远程分支的 remote 名(如 "origin");本地分支为空。
  final String remote;

  bool get isRemote => remote.isNotEmpty;

  factory GitBranch.fromJson(Map<String, dynamic> j) => GitBranch(
        name: j['name'] as String? ?? '',
        current: j['current'] as bool? ?? false,
        remote: j['remote'] as String? ?? '',
      );
}

/// 历史里的一条提交摘要。对应 Go git.Commit。
class GitCommit {
  const GitCommit({
    required this.hash,
    required this.shortHash,
    required this.author,
    required this.date,
    required this.message,
    required this.refs,
  });

  final String hash;
  final String shortHash;
  final String author;

  /// 相对时间(如 "3 hours ago")。
  final String date;
  final String message;
  final List<String> refs;

  factory GitCommit.fromJson(Map<String, dynamic> j) => GitCommit(
        hash: j['hash'] as String? ?? '',
        shortHash: j['short_hash'] as String? ?? '',
        author: j['author'] as String? ?? '',
        date: j['date'] as String? ?? '',
        message: j['message'] as String? ?? '',
        refs: (j['refs'] as List<dynamic>?)?.cast<String>() ?? const [],
      );
}

/// 某次提交里改动的一个文件 + 增删行数。对应 Go git.CommitFile。
class GitCommitFile {
  const GitCommitFile({
    required this.path,
    required this.status,
    required this.additions,
    required this.deletions,
  });

  final String path;
  final String status;
  final int additions;
  final int deletions;

  factory GitCommitFile.fromJson(Map<String, dynamic> j) => GitCommitFile(
        path: j['path'] as String? ?? '',
        status: j['status'] as String? ?? '',
        additions: (j['additions'] as num?)?.toInt() ?? 0,
        deletions: (j['deletions'] as num?)?.toInt() ?? 0,
      );
}

/// 单次提交的完整元数据 + 文件级 numstat。对应 Go git.CommitDetail。
class GitCommitDetail {
  const GitCommitDetail({
    required this.hash,
    required this.shortHash,
    required this.author,
    required this.date,
    required this.message,
    required this.files,
    required this.totalAdditions,
    required this.totalDeletions,
  });

  final String hash;
  final String shortHash;
  final String author;
  final String date;
  final String message;
  final List<GitCommitFile> files;
  final int totalAdditions;
  final int totalDeletions;

  factory GitCommitDetail.fromJson(Map<String, dynamic> j) => GitCommitDetail(
        hash: j['hash'] as String? ?? '',
        shortHash: j['short_hash'] as String? ?? '',
        author: j['author'] as String? ?? '',
        date: j['date'] as String? ?? '',
        message: j['message'] as String? ?? '',
        files: (j['files'] as List<dynamic>?)
                ?.map((e) => GitCommitFile.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
        totalAdditions: (j['total_additions'] as num?)?.toInt() ?? 0,
        totalDeletions: (j['total_deletions'] as num?)?.toInt() ?? 0,
      );
}

/// 相对上游的领先/落后提交数。对应 Go git.RemoteCounts。
class GitRemoteCounts {
  const GitRemoteCounts({
    required this.ahead,
    required this.behind,
    required this.branch,
  });

  final int ahead;
  final int behind;
  final String branch;

  static const empty = GitRemoteCounts(ahead: 0, behind: 0, branch: '');

  factory GitRemoteCounts.fromJson(Map<String, dynamic> j) => GitRemoteCounts(
        ahead: (j['ahead'] as num?)?.toInt() ?? 0,
        behind: (j['behind'] as num?)?.toInt() ?? 0,
        branch: j['branch'] as String? ?? '',
      );
}

/// CreateWorktree 的返回(对应 Go git.WorktreeCreated)。
class WorktreeCreated {
  const WorktreeCreated({
    required this.worktreePath,
    required this.branch,
    required this.baseBranch,
    required this.baseCommit,
  });

  final String worktreePath;
  final String branch;
  final String baseBranch;
  final String baseCommit;

  factory WorktreeCreated.fromJson(Map<String, dynamic> j) => WorktreeCreated(
        worktreePath: j['worktree_path'] as String? ?? '',
        branch: j['branch'] as String? ?? '',
        baseBranch: j['base_branch'] as String? ?? '',
        baseCommit: j['base_commit'] as String? ?? '',
      );
}

/// diff --name-status 的一项(对应 Go git.NameStatus)。
class GitNameStatus {
  const GitNameStatus({required this.status, required this.path});
  final String status;
  final String path;

  factory GitNameStatus.fromJson(Map<String, dynamic> j) => GitNameStatus(
        status: j['status'] as String? ?? '',
        path: j['path'] as String? ?? '',
      );
}

/// 图片预览(对应 Go fs.ImagePreview),data_url 直接喂 Image.memory/网络无关。
class FileImagePreview {
  const FileImagePreview({
    required this.dataUrl,
    required this.mimeType,
    required this.byteLength,
  });

  final String dataUrl;
  final String mimeType;
  final int byteLength;

  factory FileImagePreview.fromJson(Map<String, dynamic> j) => FileImagePreview(
        dataUrl: j['data_url'] as String? ?? '',
        mimeType: j['mime_type'] as String? ?? '',
        byteLength: (j['byte_length'] as num?)?.toInt() ?? 0,
      );
}

/// 目录项(对应 Go fs.Entry)。
class FsEntry {
  const FsEntry({required this.name, required this.isDir, required this.size});

  final String name;
  final bool isDir;
  final int size;

  factory FsEntry.fromJson(Map<String, dynamic> j) => FsEntry(
        name: j['name'] as String? ?? '',
        isDir: j['is_dir'] as bool? ?? false,
        size: (j['size'] as num?)?.toInt() ?? 0,
      );
}

/// 文件名搜索的一项(对应 Go fs.SearchResult)。
class FileSearchResult {
  const FileSearchResult({
    required this.path,
    required this.name,
    required this.dir,
    required this.extension,
  });

  final String path;
  final String name;
  final String dir;
  final String extension;

  factory FileSearchResult.fromJson(Map<String, dynamic> j) => FileSearchResult(
        path: j['path'] as String? ?? '',
        name: j['name'] as String? ?? '',
        dir: j['dir'] as String? ?? '',
        extension: j['extension'] as String? ?? '',
      );
}
