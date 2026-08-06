// BiuAdapter — spawn `biu --headless --json --prompt "..."`，监听 stdout
// JSONL 事件流，解析成统一的 AgentEvent。
//
// biu CLI 输出协议 (apps/cli/biu/internal/headless/headless.go)：
//   { "type": "RUN_STARTED",         "threadId":..., "runId":... }
//   { "type": "TEXT_MESSAGE_START",  "messageId":..., "role":"assistant" }
//   { "type": "TEXT_MESSAGE_CONTENT","messageId":..., "delta":"..." }
//   { "type": "TOOL_CALL_START",     "id":..., "name":..., "input":{...} }
//   { "type": "TOOL_CALL_END",       "id":..., "name":..., "output":..., "is_error":... }
//   { "type": "TEXT_MESSAGE_END",    "messageId":... }
//   { "type": "RUN_FINISHED",        "stop_reason":..., "input_tokens":..., "output_tokens":... }
//   { "type": "RUN_ERROR",           "message":... }
//
// 仅 desktop 可用 (依赖 dart:io Process)。Web / mobile 由 agentAdapterProvider
// 路由到 DummyAdapter。

import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:logging/logging.dart';

import '../domain/code_task.dart';
import 'agent_adapter.dart';
import 'binary_resolver.dart';

final _log = Logger('biumind.code.biu_adapter');

class BiuAdapter implements AgentAdapter {
  BiuAdapter({
    required this.envResolver,
    required this.binaryPathResolver,
    this.workingDirResolver,
  });

  /// spawn 时惰性解析 env / 路径 / cwd —— **不在构造时烘焙**。原因:env 里有
  /// BIUMIND_TOKEN,token 在 app resume 时会刷新;若烘焙进 adapter,
  /// agentAdapterProvider 必须随 token 重建,会连带重建 tasksController →
  /// 任务列表重载 → 正在看的任务被卸载重挂(实测清空终端缓冲 + 视图重置回结构化)。
  /// 惰性化后 provider 与 token 解耦,spawn 时再取最新 token(与 bridge 的
  /// resolveBridge 同思路)。
  final Map<String, String> Function() envResolver;

  /// biu 可执行文件路径解析(默认 `biu` 走 PATH;用户可配绝对路径)。
  final String Function() binaryPathResolver;

  /// 工作目录解析。null = 系统默认。worktree 任务用 task.workspace.localPath 覆盖。
  final String? Function()? workingDirResolver;

  final Map<String, Process> _running = {};

  /// tool_use_id → owner process. 用 PermissionAsk 信号到达时填入,
  /// respondPermission 时按 toolId 找对应进程写 stdin JSON 决策。
  /// SDK 当前是串行 ask, map 基本只 0/1 个 entry, 但多任务并行时要按
  /// toolId 区分。
  final Map<String, Process> _pendingAsks = {};

  @override
  String get name => 'biu';

  @override
  Stream<AgentEvent> run(CodeTask task,
      {bool resume = false, String? resumeSessionId, bool reattach = false}) {
    // biu 进程内 agent 暂不支持续跑/重连(无外部 PTY/会话);忽略 resume/reattach 正常起。
    final controller = StreamController<AgentEvent>();
    _drive(task, controller);
    return controller.stream;
  }

  Future<void> _drive(CodeTask task, StreamController<AgentEvent> out) async {
    DateTime now() => DateTime.now();
    void emit(AgentEvent e) {
      if (out.isClosed) return;
      out.add(e);
    }

    // 惰性取最新 env / 路径 / cwd(token 刷新后这里拿到的是新 token)。在 try 外
    // 解析,让 catch 也能引用 binaryPath 报错。
    final extraEnv = envResolver();
    final binaryPath = binaryPathResolver();
    final workingDir = workingDirResolver?.call();
    Process? process;
    try {
      // spawn 前先静态解析 binary 路径, 避免 Process.start 在 binary 不存在
      // 时落入 native crash 路径 (macOS hardened runtime)
      final resolved = await resolveBinary(
        binaryPath,
        extraSearchPath: extraEnv['PATH'],
      );
      if (resolved == null) {
        emit(TaskFinished(
          ts: now(),
          reason: 'error',
          errorMessage: 'biu binary not found in PATH: $binaryPath. '
              '请用 `task cli:install` 安装到 ~/.local/bin/biu, '
              '或在 settings → 编码工作台 配置绝对路径',
        ));
        await out.close();
        return;
      }

      final args = [
        '--headless',
        '--json',
        '--prompt', task.prompt,
        '--permission-policy', _mapPermissionPolicy(task.mode),
        '--permission-mode', _mapPermissionMode(task.mode),
        // 用户为本任务选的模型(M4);null/空 → 不传,biu 用 config 默认。
        if (task.model != null && task.model!.isNotEmpty) ...[
          '--model',
          task.model!,
        ],
      ];
      // task.workspace.localPath 优先 (worktree 隔离), 否则用 adapter 的
      // workingDir (passthrough 模式 / settings.codeWorkingDir)
      final effectiveCwd = task.workspace?.localPath ?? workingDir;
      _log.info('spawn $resolved ${args.join(" ")} (cwd=$effectiveCwd)');
      process = await Process.start(
        resolved,
        args,
        workingDirectory: effectiveCwd,
        environment: extraEnv.isEmpty ? null : extraEnv,
        includeParentEnvironment: true,
        runInShell: false,
      );
      _running[task.id] = process;

      // stderr → 收集 (失败诊断用)
      final stderrBuf = StringBuffer();
      process.stderr.transform(utf8.decoder).listen(stderrBuf.write);

      // stdout → JSONL → AgentEvent
      final lines = process.stdout
          .transform(utf8.decoder)
          .transform(const LineSplitter());

      // pending text accumulator: biu 的 TEXT_MESSAGE_CONTENT 是按 token delta 流；
      // 我们直接转 TextDelta，让 UI 端自己拼接。
      await for (final line in lines) {
        if (out.isClosed) break;
        if (line.trim().isEmpty) continue;
        Map<String, dynamic>? ev;
        try {
          final raw = jsonDecode(line);
          if (raw is Map<String, dynamic>) ev = raw;
        } catch (_) {
          // 非 JSON 行 (info 日志 / warning) 忽略
          continue;
        }
        if (ev == null) continue;

        final type = ev['type'] as String?;
        final ts = DateTime.tryParse(ev['ts'] as String? ?? '') ?? now();

        switch (type) {
          case 'RUN_STARTED':
          case 'TEXT_MESSAGE_START':
          case 'TEXT_MESSAGE_END':
            // 这些事件 GUI 不需要单独渲染（开始/结束由 task.status 表达）
            break;

          case 'TEXT_MESSAGE_CONTENT':
            final delta = ev['delta'] as String? ?? '';
            if (delta.isNotEmpty) emit(TextDelta(ts: ts, text: delta));

          case 'TOOL_CALL_START':
            emit(ToolUseStart(
              ts: ts,
              toolId: (ev['id'] ?? '').toString(),
              name: (ev['name'] ?? '').toString(),
              args: (ev['input'] is Map)
                  ? (ev['input'] as Map).cast<String, dynamic>()
                  : const {},
            ));

          case 'TOOL_CALL_END':
            emit(ToolUseResult(
              ts: ts,
              toolId: (ev['id'] ?? '').toString(),
              result: ev['output']?.toString() ?? '',
              isError: ev['is_error'] == true,
            ));

          case 'RUN_FINISHED':
            // biu 此处给 token 量但暂无 USD cost；P1 让 biu emit cost 字段
            final inputTokens = (ev['input_tokens'] as num?)?.toInt() ?? 0;
            final outputTokens = (ev['output_tokens'] as num?)?.toInt() ?? 0;
            if (inputTokens > 0 || outputTokens > 0) {
              emit(CostUpdate(
                ts: ts,
                totalUsd: 0,
                inputTokens: inputTokens,
                outputTokens: outputTokens,
              ));
            }
            emit(TaskFinished(
              ts: ts,
              reason: (ev['stop_reason'] ?? 'end_turn').toString(),
            ));

          case 'RUN_ERROR':
            emit(TaskFinished(
              ts: ts,
              reason: 'error',
              errorMessage: (ev['message'] ?? 'biu run error').toString(),
            ));

          case 'PERMISSION_ASK':
            // biu --permission-policy=stdin-json 时, 引擎在调敏感工具前
            // 暂停并 emit 此事件; 我们映射成 PermissionAsk 让 GUI 弹卡。
            // respondPermission 按 tool_use_id 找回 process 写 stdin。
            final toolUseId = (ev['tool_use_id'] ?? '').toString();
            if (toolUseId.isNotEmpty) {
              _pendingAsks[toolUseId] = process;
            }
            emit(PermissionAsk(
              ts: ts,
              toolId: toolUseId,
              name: (ev['name'] ?? '').toString(),
              args: (ev['input'] is Map)
                  ? (ev['input'] as Map).cast<String, dynamic>()
                  : const {},
            ));

          default:
            // 未知事件类型忽略（forward-compatible）
            break;
        }
      }

      final exit = await process.exitCode;
      _running.remove(task.id);
      if (!out.isClosed) {
        // 如果 biu 没发 RUN_FINISHED 就退出（异常退出），补一个 TaskFinished
        if (exit != 0) {
          final stderr = stderrBuf.toString().trim();
          emit(TaskFinished(
            ts: now(),
            reason: 'error',
            errorMessage: stderr.isEmpty
                ? 'biu exited with code $exit'
                : 'biu exit $exit: ${_truncate(stderr, 240)}',
          ));
        }
      }
    } on ProcessException catch (e) {
      _running.remove(task.id);
      emit(TaskFinished(
        ts: now(),
        reason: 'error',
        errorMessage: 'biu binary not found or unable to spawn: '
            '${e.message} (path=$binaryPath). '
            '请确认 biu 已安装，或在 settings 配置 BIUMIND_BIU_PATH。',
      ));
    } catch (e, st) {
      _log.warning('biu adapter error', e, st);
      _running.remove(task.id);
      emit(TaskFinished(
        ts: now(),
        reason: 'error',
        errorMessage: e.toString(),
      ));
    } finally {
      await out.close();
    }
  }

  @override
  Future<void> respondPermission(String toolId, PermissionDecision decision) async {
    final process = _pendingAsks.remove(toolId);
    if (process == null) {
      _log.warning('respondPermission: no pending ask for toolId=$toolId');
      return;
    }
    // PermissionDecision.allow 是 "Allow" 按钮 — 持久化记忆 (engine 后
    // 续相同 tool+args 调用自动放行); allowOnce 是 "Allow once" — 仅本
    // 次。biu stdinJSONPolicy 的 wire 格式: "always" / "allow_once" /
    // "deny", 对应 SDK 的 PermAlways / PermAllow / PermDeny。
    final decStr = switch (decision) {
      PermissionDecision.allow => 'always',
      PermissionDecision.allowOnce => 'allow_once',
      PermissionDecision.deny => 'deny',
    };
    final line = jsonEncode({
      'tool_use_id': toolId,
      'decision': decStr,
    });
    try {
      process.stdin.writeln(line);
      await process.stdin.flush();
    } catch (e) {
      _log.warning('respondPermission stdin write failed: $e');
    }
  }

  @override
  Future<void> cancel(String taskId) async {
    final p = _running.remove(taskId);
    if (p == null) return;
    if (Platform.isWindows) {
      p.kill();
    } else {
      p.kill(ProcessSignal.sigterm);
    }
  }

  /// PermissionMode → biu 的 --permission-policy 值 (callback policy)。
  /// 引擎在 mode=acceptEdits 时已经把 read-only / 非破坏性 edit 自动放行,
  /// 只有真正需要询问的 (Bash / 写操作) 才回调 policy。所以 ask/autoEdit
  /// 都用 stdin-json: 真出现 ask 时 GUI 弹 PermissionCard, 没 ask 时
  /// callback 不会被触发。fullAccess 不需要 stdin 通道, 直接 allow。
  static String _mapPermissionPolicy(PermissionMode m) => switch (m) {
        PermissionMode.ask => 'stdin-json',
        PermissionMode.autoEdit => 'stdin-json',
        PermissionMode.fullAccess => 'allow',
      };

  /// PermissionMode → biu 引擎的 --permission-mode 值。
  ///   ask        → default     (engine 在所有破坏性工具上 ask)
  ///   autoEdit   → acceptEdits (read-only + 非破坏性 edit auto-allow,
  ///                             Bash / 删 / git push 等仍 ask)
  ///   fullAccess → bypassPermissions (engine 全放行, 不会触发 callback)
  static String _mapPermissionMode(PermissionMode m) => switch (m) {
        PermissionMode.ask => 'default',
        PermissionMode.autoEdit => 'acceptEdits',
        PermissionMode.fullAccess => 'bypassPermissions',
      };

  static String _truncate(String s, int max) =>
      s.length <= max ? s : '${s.substring(0, max)}…';

  /// 探测 biu binary 是否可用（运行 `biu version`，0 = 可用）。
  static Future<bool> isInstalled([String binaryPath = 'biu']) async {
    try {
      final r = await Process.run(binaryPath, ['version'], runInShell: false);
      return r.exitCode == 0;
    } catch (_) {
      return false;
    }
  }
}
