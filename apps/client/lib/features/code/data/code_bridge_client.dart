// CodeBridgeClient —— 编码模块桌面 loopback 直连客户端。
//
// 连本地 biu serve 的 /v1/code/ws（端口来自 BiuDaemonManager.state.bridgeUrl，
// 形如 http://127.0.0.1:53827 → ws://127.0.0.1:53827/v1/code/ws）。这是 Code 模块
// 桌面端「真 PTY、低延迟、不绕云端」的承载（Code-Design §5 D7）；与 chat 的
// BiuClient（连云端 brain agent-plane）有意分叉。
//
// 形状对照 Go 的 internal/bridge/code_ws.go：
//   - request(method, params) → 发 code_request、按 request_id 关联 Completer
//     等 code_response（git/fs/pty.open/pty.kill 都走这）
//   - sendInput / resize → 发高频 code_pty_input / code_pty_resize 帧（不走 RPC 信封）
//   - ptyChunks / ptyExits → 订阅 code_pty_chunk / code_pty_exit 出站流
//
// transport seam（CodeTransport）抄 biu_client.dart 的 BiuTransport 模式，测试可
// 注入内存 fake；生产走 WebSocketChannel。

import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import '../../../data/api/sdkproto/v1/code.dart';
import '../domain/agent_detect.dart';
import '../domain/git_models.dart';
import '../domain/hook_status.dart';
import '../domain/project_config.dart';
import '../domain/skill_models.dart';
import '../domain/usage_models.dart';

/// CodeTransport 是与下层连接握手的最小接口（生产 = WS，测试 = 内存 fake）。
abstract class CodeTransport {
  /// 下行帧 stream，每条 element 是 server push 的原始 String（gorilla TextMessage）。
  Stream<dynamic> get frames;

  /// 上行发一条已 jsonEncode 的 String。
  void send(String data);

  /// 主动关。idempotent。
  Future<void> close();
}

/// requestTimeout 是单条 code_request 等响应的上限。git/fs 是本机操作，通常亚秒级；
/// pty.open 也只是拉起进程。超时返回失败的 CodeResponse 而非永久挂起。
const _requestTimeout = Duration(seconds: 30);

class CodeBridgeClient {
  /// 本地 bridge 的 HTTP base（http://127.0.0.1:port），来自 daemon 的 BIU_BRIDGE_URL。
  final String bridgeUrl;

  /// 自定义 connector —— 测试注入 fake。生产留 null 走 WebSocketChannel。
  final CodeTransport Function(Uri uri)? connector;

  CodeBridgeClient({required this.bridgeUrl, this.connector});

  CodeTransport? _transport;
  StreamSubscription<dynamic>? _readSub;
  int _reqCounter = 0;
  final _pending = <String, Completer<CodeResponse>>{};
  final _chunkController = StreamController<CodePtyChunk>.broadcast();
  final _exitController = StreamController<CodePtyExit>.broadcast();
  final _sessionController = StreamController<CodeSessionEvent>.broadcast();
  bool _closed = false;

  /// 服务端推来的 PTY 输出（按 ptyId 自行过滤）。
  Stream<CodePtyChunk> get ptyChunks => _chunkController.stream;

  /// 服务端推来的 PTY 退出通知。
  Stream<CodePtyExit> get ptyExits => _exitController.stream;

  /// 服务端推来的结构化会话事件（M3,从 agent JSONL 解析;按 taskId 自行过滤）。
  Stream<CodeSessionEvent> get sessionEvents => _sessionController.stream;

  /// 建立 WS 连接。幂等：已连则直接返回。
  Future<void> connect() async {
    if (_transport != null || _closed) return;
    final wsUrl = bridgeUrl
        .replaceFirst('https://', 'wss://')
        .replaceFirst('http://', 'ws://');
    final uri = Uri.parse('$wsUrl/v1/code/ws');
    final t = (connector ?? _defaultConnector)(uri);
    _transport = t;
    _readSub = t.frames.listen(
      _onFrame,
      onError: (Object e, StackTrace s) => _onDone(),
      onDone: _onDone,
    );
  }

  /// 发一条 RPC 请求并等响应（按 request_id 关联）。连接未建或已关 → 返回失败响应。
  Future<CodeResponse> request(String method,
      [Map<String, dynamic>? params]) async {
    if (_transport == null) {
      return CodeResponse(
          requestId: '', ok: false, error: 'code bridge not connected');
    }
    final id = 'c${_reqCounter++}';
    final completer = Completer<CodeResponse>();
    _pending[id] = completer;
    final frame = CodeRequest(requestId: id, method: method, params: params);
    _transport!.send(jsonEncode(frame.toJson()));
    return completer.future.timeout(_requestTimeout, onTimeout: () {
      _pending.remove(id);
      return CodeResponse(requestId: id, ok: false, error: 'request timeout');
    });
  }

  /// 发 PTY 输入字节（键盘/粘贴）。
  void sendInput(String ptyId, Uint8List data) {
    _transport?.send(jsonEncode(
        CodePtyInput(ptyId: ptyId, data: data).toJson()));
  }

  /// 发终端尺寸变更。
  void resize(String ptyId, int cols, int rows) {
    _transport?.send(jsonEncode(
        CodePtyResize(ptyId: ptyId, cols: cols, rows: rows).toJson()));
  }

  // ── 便捷封装 ──────────────────────────────────────────

  /// 拉起 PTY 进程，返回服务端分配的 pty_id（失败抛 CodeBridgeException）。
  Future<String> openPty({
    required String cmd,
    List<String>? args,
    String? cwd,
    int? cols,
    int? rows,
  }) async {
    final resp = await request('pty.open', {
      'cmd': cmd,
      'args': ?args,
      'cwd': ?cwd,
      'cols': ?cols,
      'rows': ?rows,
    });
    if (!resp.ok) throw CodeBridgeException('pty.open', resp.error);
    final id = resp.result?['pty_id'] as String?;
    if (id == null || id.isEmpty) {
      throw CodeBridgeException('pty.open', 'missing pty_id');
    }
    return id;
  }

  /// 拉起外部编码 agent(claude/codex)在 PTY 里跑;pty_id = taskId。biu 不走这
  /// (进程内)。失败抛 CodeBridgeException(如 binary 未找到 / agent=biu)。
  Future<void> runTask({
    required String taskId,
    required String agentType,
    required String permissionMode,
    required String prompt,
    String? model,
    String? cwd,
    bool resume = false,
    String? sessionId,
    int? cols,
    int? rows,
  }) async {
    final resp = await request('code.runTask', {
      'task_id': taskId,
      'agent_type': agentType,
      'permission_mode': permissionMode,
      'prompt': prompt,
      'model': ?model,
      'cwd': ?cwd,
      if (resume) 'resume': true,
      'session_id': ?sessionId,
      'cols': ?cols,
      'rows': ?rows,
    });
    if (!resp.ok) throw CodeBridgeException('code.runTask', resp.error);
  }

  /// 回放该任务落盘的 PTY 历史(base64 解码为原始字节)。重开终端时喂进 xterm,
  /// 让原始终端跨重启存活。无日志返回空。
  Future<Uint8List> ptyReplayLog(String ptyId) async {
    final resp = await request('pty.replayLog', {'pty_id': ptyId});
    if (!resp.ok) throw CodeBridgeException('pty.replayLog', resp.error);
    final b64 = resp.result?['data_b64'] as String? ?? '';
    if (b64.isEmpty) return Uint8List(0);
    return base64Decode(b64);
  }

  /// 杀掉 PTY 进程。
  Future<void> killPty(String ptyId) async {
    final resp = await request('pty.kill', {'pty_id': ptyId});
    if (!resp.ok) throw CodeBridgeException('pty.kill', resp.error);
  }

  /// 当前 daemon 仍在跑的 PTY id 列表(= 活动任务)。启动对账用:本地存盘 status 是
  /// running 但不在此集 → 进程已死(interrupted);在此集 → 进程还活(detached,可重连)。
  Future<List<String>> liveTasks() async {
    final resp = await request('pty.active', const {});
    if (!resp.ok) throw CodeBridgeException('pty.active', resp.error);
    final ids = resp.result?['pty_ids'] as List<dynamic>? ?? const [];
    return ids.cast<String>();
  }

  /// 把一个**仍在跑**的 PTY 的输出/退出重绑到当前连接(断线重连,不 spawn 新进程)。
  /// 返回 true 表示该 PTY 仍活并已重挂;false 表示进程已退(应退回 --resume)。
  Future<bool> reattachTask({
    required String taskId,
    required String agentType,
    String? cwd,
    String? sessionId,
  }) async {
    final resp = await request('pty.reattach', {
      'task_id': taskId,
      'agent_type': agentType,
      'cwd': ?cwd,
      'session_id': ?sessionId,
    });
    if (!resp.ok) throw CodeBridgeException('pty.reattach', resp.error);
    return resp.result?['alive'] as bool? ?? false;
  }

  /// git status（返回原始 result map：branch / staged / modified / untracked /
  /// conflicts / clean）。M0 兼容；新 UI 用 gitStatusFiles。
  Future<Map<String, dynamic>> gitStatus(String cwd) async {
    final resp = await request('git.status', {'cwd': cwd});
    if (!resp.ok) throw CodeBridgeException('git.status', resp.error);
    return resp.result ?? const {};
  }

  // ── Git（M4 全套，typed）──────────────────────────────────

  /// 逐文件工作区状态（staged + unstaged 可各一条）。
  Future<List<GitFileChange>> gitStatusFiles(String cwd) async {
    final resp = await request('git.statusFiles', {'cwd': cwd});
    if (!resp.ok) throw CodeBridgeException('git.statusFiles', resp.error);
    final list = resp.result?['files'] as List<dynamic>? ?? const [];
    return list
        .map((e) => GitFileChange.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// 本地 + 远程分支。
  Future<List<GitBranch>> gitListBranches(String cwd) async {
    final resp = await request('git.listBranches', {'cwd': cwd});
    if (!resp.ok) throw CodeBridgeException('git.listBranches', resp.error);
    final list = resp.result?['branches'] as List<dynamic>? ?? const [];
    return list
        .map((e) => GitBranch.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<void> gitCreateBranch(String cwd, String name,
      {bool checkout = true}) async {
    final resp = await request('git.createBranch',
        {'cwd': cwd, 'name': name, 'checkout': checkout});
    if (!resp.ok) throw CodeBridgeException('git.createBranch', resp.error);
  }

  Future<void> gitCheckoutBranch(String cwd, String name) async {
    final resp =
        await request('git.checkoutBranch', {'cwd': cwd, 'name': name});
    if (!resp.ok) throw CodeBridgeException('git.checkoutBranch', resp.error);
  }

  Future<void> gitDeleteBranch(String cwd, String name,
      {bool force = false}) async {
    final resp = await request(
        'git.deleteBranch', {'cwd': cwd, 'name': name, 'force': force});
    if (!resp.ok) throw CodeBridgeException('git.deleteBranch', resp.error);
  }

  /// 提交历史。limit/skip 分页。
  Future<List<GitCommit>> gitLog(String cwd,
      {int limit = 50, int skip = 0}) async {
    final resp =
        await request('git.log', {'cwd': cwd, 'limit': limit, 'skip': skip});
    if (!resp.ok) throw CodeBridgeException('git.log', resp.error);
    final list = resp.result?['commits'] as List<dynamic>? ?? const [];
    return list
        .map((e) => GitCommit.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<GitCommitDetail> gitCommitDetail(String cwd, String hash) async {
    final resp = await request('git.commitDetail', {'cwd': cwd, 'hash': hash});
    if (!resp.ok) throw CodeBridgeException('git.commitDetail', resp.error);
    return GitCommitDetail.fromJson(resp.result ?? const {});
  }

  /// 某提交的整体 diff。
  Future<String> gitShowDiff(String cwd, String hash) async {
    final resp = await request('git.showDiff', {'cwd': cwd, 'hash': hash});
    if (!resp.ok) throw CodeBridgeException('git.showDiff', resp.error);
    return resp.result?['diff'] as String? ?? '';
  }

  /// 某提交里单文件的 diff。
  Future<String> gitShowFileDiff(String cwd, String hash, String path) async {
    final resp = await request(
        'git.showFileDiff', {'cwd': cwd, 'hash': hash, 'path': path});
    if (!resp.ok) throw CodeBridgeException('git.showFileDiff', resp.error);
    return resp.result?['diff'] as String? ?? '';
  }

  /// 工作区单文件 diff。staged=true 看暂存区。
  Future<String> gitFileDiff(String cwd, String path,
      {bool staged = false}) async {
    final resp = await request(
        'git.fileDiff', {'cwd': cwd, 'path': path, 'staged': staged});
    if (!resp.ok) throw CodeBridgeException('git.fileDiff', resp.error);
    return resp.result?['diff'] as String? ?? '';
  }

  Future<void> gitStage(String cwd, List<String> paths) async {
    final resp = await request('git.stage', {'cwd': cwd, 'paths': paths});
    if (!resp.ok) throw CodeBridgeException('git.stage', resp.error);
  }

  Future<void> gitUnstage(String cwd, List<String> paths) async {
    final resp = await request('git.unstage', {'cwd': cwd, 'paths': paths});
    if (!resp.ok) throw CodeBridgeException('git.unstage', resp.error);
  }

  Future<void> gitStageAll(String cwd) async {
    final resp = await request('git.stageAll', {'cwd': cwd});
    if (!resp.ok) throw CodeBridgeException('git.stageAll', resp.error);
  }

  Future<void> gitUnstageAll(String cwd) async {
    final resp = await request('git.unstageAll', {'cwd': cwd});
    if (!resp.ok) throw CodeBridgeException('git.unstageAll', resp.error);
  }

  /// 提交暂存区，返回 git 输出。
  Future<String> gitCommit(String cwd, String message) async {
    final resp = await request('git.commit', {'cwd': cwd, 'message': message});
    if (!resp.ok) throw CodeBridgeException('git.commit', resp.error);
    return resp.result?['output'] as String? ?? '';
  }

  Future<void> gitDiscardFile(String cwd, String path,
      {required bool untracked}) async {
    final resp = await request(
        'git.discardFile', {'cwd': cwd, 'path': path, 'untracked': untracked});
    if (!resp.ok) throw CodeBridgeException('git.discardFile', resp.error);
  }

  Future<void> gitDiscardAll(String cwd) async {
    final resp = await request('git.discardAll', {'cwd': cwd});
    if (!resp.ok) throw CodeBridgeException('git.discardAll', resp.error);
  }

  /// 推送，返回 git 输出。branch 为空时裸 push（当前上游）。
  Future<String> gitPush(String cwd, {String? branch}) async {
    final resp = await request('git.push', {'cwd': cwd, 'branch': ?branch});
    if (!resp.ok) throw CodeBridgeException('git.push', resp.error);
    return resp.result?['output'] as String? ?? '';
  }

  Future<String> gitPull(String cwd) async {
    final resp = await request('git.pull', {'cwd': cwd});
    if (!resp.ok) throw CodeBridgeException('git.pull', resp.error);
    return resp.result?['output'] as String? ?? '';
  }

  Future<GitRemoteCounts> gitRemoteCounts(String cwd, {String? branch}) async {
    final resp =
        await request('git.remoteCounts', {'cwd': cwd, 'branch': ?branch});
    if (!resp.ok) throw CodeBridgeException('git.remoteCounts', resp.error);
    return GitRemoteCounts.fromJson(resp.result ?? const {});
  }

  /// AI 生成 commit message（daemon 内走 model-relay，满足 I6）。
  Future<String> gitGenerateCommitMessage(String cwd) async {
    final resp = await request('git.generateCommitMessage', {'cwd': cwd});
    if (!resp.ok) {
      throw CodeBridgeException('git.generateCommitMessage', resp.error);
    }
    return resp.result?['message'] as String? ?? '';
  }

  // ── worktree 生命周期 + 迁移所需读方法（M4-E）─────────────

  /// 仓库工作树根（git rev-parse --show-toplevel）。非 git 目录抛异常。
  Future<String> gitRepoRoot(String cwd) async {
    final resp = await request('git.repoRoot', {'cwd': cwd});
    if (!resp.ok) throw CodeBridgeException('git.repoRoot', resp.error);
    return resp.result?['root'] as String? ?? '';
  }

  /// 新建任务 worktree（git 操作全在 daemon 内：rev-parse + 分支冲突后缀 + add）。
  Future<WorktreeCreated> gitCreateWorktree(
      String projectPath, String worktreePath, String preferredBranch,
      {String? baseRef}) async {
    final resp = await request('git.createWorktree', {
      'project_path': projectPath,
      'worktree_path': worktreePath,
      'preferred_branch': preferredBranch,
      'base_ref': ?baseRef,
    });
    if (!resp.ok) throw CodeBridgeException('git.createWorktree', resp.error);
    return WorktreeCreated.fromJson(resp.result ?? const {});
  }

  /// 移除 worktree（--force）+ 可选删分支。
  Future<void> gitRemoveWorktree(
      String projectPath, String worktreePath, String branch) async {
    final resp = await request('git.removeWorktree', {
      'project_path': projectPath,
      'worktree_path': worktreePath,
      'branch': branch,
    });
    if (!resp.ok) throw CodeBridgeException('git.removeWorktree', resp.error);
  }

  /// 把 worktree 分支合并回主 repo 的 base 分支(daemon 内 checkout base + merge)。
  /// 返回 git 输出。冲突/失败时 daemon 报错,前端透传。
  Future<String> gitMergeWorktree(
      String projectPath, String worktreePath, String branch, String baseBranch) async {
    final resp = await request('git.mergeWorktree', {
      'project_path': projectPath,
      'worktree_path': worktreePath,
      'branch': branch,
      'base_branch': baseBranch,
    });
    if (!resp.ok) throw CodeBridgeException('git.mergeWorktree', resp.error);
    return resp.result?['output'] as String? ?? '';
  }

  /// worktree 工作树相对 base merge-base 的增删行数。
  Future<({int additions, int deletions})> gitWorktreeDiffStats(
      String worktreePath, String baseBranch) async {
    final resp = await request('git.worktreeDiffStats',
        {'worktree_path': worktreePath, 'base_branch': baseBranch});
    if (!resp.ok) throw CodeBridgeException('git.worktreeDiffStats', resp.error);
    final r = resp.result ?? const {};
    return (
      additions: (r['additions'] as num?)?.toInt() ?? 0,
      deletions: (r['deletions'] as num?)?.toInt() ?? 0,
    );
  }

  /// baseRef..HEAD 的 name-status 列表（worktree artifact 采集）。
  Future<List<GitNameStatus>> gitChangedFiles(String cwd, String baseRef) async {
    final resp =
        await request('git.changedFiles', {'cwd': cwd, 'base_ref': baseRef});
    if (!resp.ok) throw CodeBridgeException('git.changedFiles', resp.error);
    final list = resp.result?['files'] as List<dynamic>? ?? const [];
    return list
        .map((e) => GitNameStatus.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// 未跟踪且未忽略的文件（ls-files --others --exclude-standard）。
  Future<List<String>> gitListUntracked(String cwd) async {
    final resp = await request('git.listUntracked', {'cwd': cwd});
    if (!resp.ok) throw CodeBridgeException('git.listUntracked', resp.error);
    return (resp.result?['files'] as List<dynamic>?)?.cast<String>() ?? const [];
  }

  /// baseRef..HEAD 范围内单文件 diff（preview 生成）。
  Future<String> gitRangeFileDiff(String cwd, String baseRef, String path) async {
    final resp = await request(
        'git.rangeFileDiff', {'cwd': cwd, 'base_ref': baseRef, 'path': path});
    if (!resp.ok) throw CodeBridgeException('git.rangeFileDiff', resp.error);
    return resp.result?['diff'] as String? ?? '';
  }

  /// 读文件（返回 content / size / truncated）。
  Future<Map<String, dynamic>> fsRead(String path, {int? maxBytes}) async {
    final resp = await request('fs.read', {
      'path': path,
      'max_bytes': ?maxBytes,
    });
    if (!resp.ok) throw CodeBridgeException('fs.read', resp.error);
    return resp.result ?? const {};
  }

  /// 当前活跃 PTY 的 id 列表。Flutter 启动时拉一次做对账：status=running/pending
  /// 但不在此集的任务标 interrupted（daemon 重启后存盘 status 会过时）。
  Future<List<String>> ptyActive() async {
    final resp = await request('pty.active');
    if (!resp.ok) throw CodeBridgeException('pty.active', resp.error);
    final ids = resp.result?['pty_ids'] as List<dynamic>?;
    return ids?.cast<String>() ?? const [];
  }

  /// 列目录（返回 entries）。
  Future<Map<String, dynamic>> fsList(String path) async {
    final resp = await request('fs.list', {'path': path});
    if (!resp.ok) throw CodeBridgeException('fs.list', resp.error);
    return resp.result ?? const {};
  }

  /// 列目录（typed，目录优先 + 名称排序）。
  Future<List<FsEntry>> fsListEntries(String path) async {
    final resp = await request('fs.list', {'path': path});
    if (!resp.ok) throw CodeBridgeException('fs.list', resp.error);
    final list = resp.result?['entries'] as List<dynamic>? ?? const [];
    return list.map((e) => FsEntry.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// 读文件文本内容（typed）。
  Future<({String content, int size, bool truncated})> fsReadFile(String path,
      {int? maxBytes}) async {
    final r = await fsRead(path, maxBytes: maxBytes);
    return (
      content: r['content'] as String? ?? '',
      size: (r['size'] as num?)?.toInt() ?? 0,
      truncated: r['truncated'] as bool? ?? false,
    );
  }

  /// 写文件内容（覆盖）。root = 项目根（路径越界校验）。
  Future<void> fsWrite(String root, String path, String content) async {
    final resp = await request(
        'fs.write', {'root': root, 'path': path, 'content': content});
    if (!resp.ok) throw CodeBridgeException('fs.write', resp.error);
  }

  /// 写二进制内容（图片附件等,自动建父目录）。bytes 经 base64 过线。
  Future<void> fsWriteBytes(String root, String path, List<int> bytes) async {
    final resp = await request('fs.writeBytes',
        {'root': root, 'path': path, 'data': base64Encode(bytes)});
    if (!resp.ok) throw CodeBridgeException('fs.writeBytes', resp.error);
  }

  Future<void> fsCreateFile(String root, String path) async {
    final resp = await request('fs.createFile', {'root': root, 'path': path});
    if (!resp.ok) throw CodeBridgeException('fs.createFile', resp.error);
  }

  Future<void> fsCreateDirectory(String root, String path) async {
    final resp =
        await request('fs.createDirectory', {'root': root, 'path': path});
    if (!resp.ok) throw CodeBridgeException('fs.createDirectory', resp.error);
  }

  /// 永久删除文件/目录（root 内 + 非受保护段）。
  Future<void> fsDelete(String root, String path) async {
    final resp = await request('fs.delete', {'root': root, 'path': path});
    if (!resp.ok) throw CodeBridgeException('fs.delete', resp.error);
  }

  /// 图片预览（base64 data URL）。
  Future<FileImagePreview> fsImagePreview(String root, String path) async {
    final resp =
        await request('fs.imagePreview', {'root': root, 'path': path});
    if (!resp.ok) throw CodeBridgeException('fs.imagePreview', resp.error);
    return FileImagePreview.fromJson(resp.result ?? const {});
  }

  /// 工程文件清单（尊重 .gitignore）。
  Future<List<String>> fsListProjectFiles(String root) async {
    final resp = await request('fs.listProjectFiles', {'root': root});
    if (!resp.ok) throw CodeBridgeException('fs.listProjectFiles', resp.error);
    return (resp.result?['files'] as List<dynamic>?)?.cast<String>() ?? const [];
  }

  /// 文件名模糊查找（Cmd+P 式）。
  Future<List<FileSearchResult>> fsSearch(String root, String query,
      {List<String>? extensions, int? limit}) async {
    final resp = await request('fs.search', {
      'root': root,
      'query': query,
      'extensions': ?extensions,
      'limit': ?limit,
    });
    if (!resp.ok) throw CodeBridgeException('fs.search', resp.error);
    final list = resp.result?['results'] as List<dynamic>? ?? const [];
    return list
        .map((e) => FileSearchResult.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  // ── 用量(M5)──────────────────────────────────────────

  /// 读 Claude(订阅 5h/7d)+ Codex(app-server RPC)用量快照。按需、无缓存;
  /// 任一源失败落 unavailable(不抛)。daemon 内详见 code/usage 包。
  Future<UsageSnapshot> readUsageSnapshot() async {
    final resp = await request('usage.read');
    if (!resp.ok) throw CodeBridgeException('usage.read', resp.error);
    return UsageSnapshot.fromJson(resp.result ?? const {});
  }

  /// 自动检测 claude/codex/biu 的二进制路径 + 版本(扫 PATH + 候选目录)。
  /// 返回 agentType → 结果。daemon 内详见 agent.detect / agent/detect.go。
  Future<Map<String, AgentDetectResult>> detectAgents() async {
    final resp = await request('agent.detect');
    if (!resp.ok) throw CodeBridgeException('agent.detect', resp.error);
    final agents = resp.result?['agents'] as Map<String, dynamic>? ?? const {};
    return agents.map((k, v) =>
        MapEntry(k, AgentDetectResult.fromJson(v as Map<String, dynamic>)));
  }

  /// 用 prompt 经 model-relay(daemon 侧注入的 commitGen 缝)生成任务短标题。
  /// daemon 无 provider → 抛异常,调用方回退 prompt 截断。详见 agent.generateName。
  Future<String> generateAgentName(String prompt) async {
    final resp = await request('agent.generateName', {'prompt': prompt});
    if (!resp.ok) throw CodeBridgeException('agent.generateName', resp.error);
    return resp.result?['name'] as String? ?? '';
  }

  // ── hook 安装/状态(PERI-1)──────────────────────────────────────────────

  /// 当前 hook 安装状态(node / 脚本 / claude+codex 是否已注入)。详见 hooks.status。
  Future<HookInstallStatus> hooksStatus() async {
    final resp = await request('hooks.status');
    if (!resp.ok) throw CodeBridgeException('hooks.status', resp.error);
    return HookInstallStatus.fromJson(resp.result ?? const {});
  }

  /// 每个 agent 的 hook 就绪态(node + 安装 + 版本门槛)。详见 hooks.readiness。
  Future<List<HookAgentReadiness>> hooksReadiness() async {
    final resp = await request('hooks.readiness');
    if (!resp.ok) throw CodeBridgeException('hooks.readiness', resp.error);
    final agents = resp.result?['agents'] as List<dynamic>? ?? const [];
    return agents
        .map((e) => HookAgentReadiness.fromJson((e as Map).cast<String, dynamic>()))
        .toList(growable: false);
  }

  /// 幂等安装 hook(写脚本 + claude-settings + 注入 codex config.toml marker 区)。
  Future<HookInstallStatus> hooksInstall() async {
    final resp = await request('hooks.install');
    if (!resp.ok) throw CodeBridgeException('hooks.install', resp.error);
    return HookInstallStatus.fromJson(resp.result ?? const {});
  }

  /// 卸载 hook(删 claude-settings + 移除 codex config.toml 的 biu marker 区)。
  Future<void> hooksUninstall() async {
    final resp = await request('hooks.uninstall');
    if (!resp.ok) throw CodeBridgeException('hooks.uninstall', resp.error);
  }

  // ── 项目级配置(PERI-2)──────────────────────────────────────────────────

  /// 读 .biu/config.toml(缺失/坏 → 默认)。详见 config.read / projcfg。
  Future<ProjectConfig> configRead(String cwd) async {
    final resp = await request('config.read', {'cwd': cwd});
    if (!resp.ok) throw CodeBridgeException('config.read', resp.error);
    return ProjectConfig.fromJson(resp.result ?? const {});
  }

  /// 写 .biu/config.toml(建目录 + 原子写)。
  Future<void> configWrite(String cwd, ProjectConfig config) async {
    final resp =
        await request('config.write', {'cwd': cwd, 'config': config.toJson()});
    if (!resp.ok) throw CodeBridgeException('config.write', resp.error);
  }

  // ── 项目级 Skills 安装(PERI-3)────────────────────────────────────────────

  /// 列 ~/.biumind/skills 下可安装的 skill。详见 skills.listHub。
  Future<List<HubSkill>> skillsListHub() async {
    final resp = await request('skills.listHub');
    if (!resp.ok) throw CodeBridgeException('skills.listHub', resp.error);
    final list = resp.result?['skills'] as List<dynamic>? ?? const [];
    return list
        .map((e) => HubSkill.fromJson((e as Map).cast<String, dynamic>()))
        .toList(growable: false);
  }

  /// 扫项目 .claude/skills + .codex/skills 反推已安装 skill 及健康度。
  Future<List<SkillInstallation>> skillsInstallations(String cwd) async {
    final resp = await request('skills.installations', {'cwd': cwd});
    if (!resp.ok) throw CodeBridgeException('skills.installations', resp.error);
    final list = resp.result?['installations'] as List<dynamic>? ?? const [];
    return list
        .map((e) => SkillInstallation.fromJson((e as Map).cast<String, dynamic>()))
        .toList(growable: false);
  }

  /// 把 hub skill symlink 进项目的 `.claude/skills` 或 `.codex/skills`(agent: claude | codex)。
  Future<void> skillsInstall(String cwd, String name, String agent) async {
    final resp = await request(
        'skills.install', {'cwd': cwd, 'name': name, 'agent': agent});
    if (!resp.ok) throw CodeBridgeException('skills.install', resp.error);
  }

  /// 移除项目内某 agent 的 skill symlink(仅删 symlink,不删真实目录)。
  Future<void> skillsUninstall(String cwd, String name, String agent) async {
    final resp = await request(
        'skills.uninstall', {'cwd': cwd, 'name': name, 'agent': agent});
    if (!resp.ok) throw CodeBridgeException('skills.uninstall', resp.error);
  }

  Future<void> close() async {
    _closed = true;
    await _readSub?.cancel();
    _readSub = null;
    await _transport?.close();
    _transport = null;
    // 唤醒所有挂起请求，避免调用方永久等待。
    for (final c in _pending.values) {
      if (!c.isCompleted) {
        c.complete(
            CodeResponse(requestId: '', ok: false, error: 'client closed'));
      }
    }
    _pending.clear();
    if (!_chunkController.isClosed) await _chunkController.close();
    if (!_exitController.isClosed) await _exitController.close();
    if (!_sessionController.isClosed) await _sessionController.close();
  }

  // ── 内部 ──────────────────────────────────────────────

  void _onFrame(dynamic raw) {
    if (raw is! String) return; // gorilla 用 TextMessage；Binary drop
    try {
      final json = jsonDecode(raw) as Map<String, dynamic>;
      final frame = CodeFrame.fromJson(json);
      switch (frame) {
        case CodeResponse resp:
          final c = _pending.remove(resp.requestId);
          if (c != null && !c.isCompleted) c.complete(resp);
        case CodePtyChunk chunk:
          if (!_chunkController.isClosed) _chunkController.add(chunk);
        case CodePtyExit exit:
          if (!_exitController.isClosed) _exitController.add(exit);
        case CodeSessionEvent ev:
          if (!_sessionController.isClosed) _sessionController.add(ev);
        default:
          // code_request / code_pty_input / code_pty_resize 是入站方向，
          // 不该从 server 收到；忽略。
          break;
      }
    } catch (e, stack) {
      debugPrint('CodeBridgeClient parse frame failed: $e\nraw=$raw\n$stack');
      // 一帧解析失败不拆连接
    }
  }

  void _onDone() {
    _readSub = null;
    _transport = null;
    // M0 不做自动重连（PTY 是实时流，重连后需 re-attach，留 M2/M3）。
    // 唤醒挂起请求。
    for (final c in _pending.values) {
      if (!c.isCompleted) {
        c.complete(CodeResponse(
            requestId: '', ok: false, error: 'connection closed'));
      }
    }
    _pending.clear();
  }

  static CodeTransport _defaultConnector(Uri uri) =>
      _WSChannelTransport(WebSocketChannel.connect(uri));
}

/// CodeBridgeException 表示某个 code RPC 返回了失败响应。
class CodeBridgeException implements Exception {
  final String method;
  final String? error;
  CodeBridgeException(this.method, this.error);
  @override
  String toString() => 'CodeBridgeException($method): ${error ?? "unknown"}';
}

class _WSChannelTransport implements CodeTransport {
  final WebSocketChannel _ch;
  _WSChannelTransport(this._ch);

  @override
  Stream<dynamic> get frames => _ch.stream;

  @override
  void send(String data) => _ch.sink.add(data);

  @override
  Future<void> close() async => _ch.sink.close();
}
