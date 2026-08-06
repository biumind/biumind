// CodeTasksController — 内存任务列表 + 流式事件追加 + 状态机推进。
// P1 上 Drift 持久化时把这一层换成 repository pattern。

import 'dart:async' show unawaited;
import 'dart:io' show Platform;

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:uuid/uuid.dart';

import '../../../features/settings/application/settings_controller.dart';
import '../../../services/auth_service.dart';
import '../../../services/login_shell_env.dart';
import '../../../services/settings_repo.dart' show AppSettings;
import '../agent/agent_adapter.dart';
import '../agent/biu_adapter.dart';
import '../agent/bridge_terminal_adapter.dart';
import '../agent/multiplex_adapter.dart';
import '../data/code_bridge_client.dart' show CodeBridgeClient;
import '../data/code_bridge_provider.dart';
import '../data/code_task_artifacts_dao.dart';
import '../data/code_tasks_dao.dart';
import '../domain/code_task.dart';
import '../workspace/workspace_manager.dart';

class CodeTasksController extends StateNotifier<List<CodeTask>> {
  CodeTasksController(
    this._adapter,
    this._workspaces,
    this._dao,
    this._artifacts,
    this._originDeviceId,
    this._originDeviceLabel,
  ) : super(const []) {
    _hydrate();
  }
  final AgentAdapter _adapter;
  final WorkspaceManager _workspaces;
  final CodeTasksDao _dao;
  final CodeTaskArtifactsDao _artifacts;

  /// 来自 settings.codeOriginDeviceId — 仅作任务的「跑在哪台机」本地展示标签。
  /// codeSync 已废弃(D4/Code-I6),不再上云;每个任务都是本机的。
  final String? _originDeviceId;
  final String? _originDeviceLabel;

  final _uuid = const Uuid();

  /// 启动时从 db 拉历史任务进内存。后续仅 controller 写, dao 反向 watch
  /// 不引入 (避免 stream burst rebuild UI)。
  ///
  /// 对账策略(为何是 blanket interrupt、不查 pty.active 细化):daemon(biu serve)
  /// 是 Flutter 的子进程,app 退出时 SIGTERM 同杀(biu_daemon_manager.dart)。故
  /// app 重启 → daemon 重启 → 所有 PTY 必然消失,pty.active 永远返回空集,细化
  /// 等价于 blanket。只有「daemon 比 app 活得久」或「bridge 断后重连」才有细化价值,
  /// 二者分别属 M2 远控 / 自动重连(M0 明确无重连),届时再做,现在做是死代码。
  Future<void> _hydrate() async {
    try {
      final loaded = await _dao.loadAll();
      // 非终态 task (running / queued / inputRequired) 在重启后没真在跑了,
      // 标记为 interrupted 以正确反映现实。
      final fixed = loaded.map((t) {
        if (t.status == CodeTaskStatus.running ||
            t.status == CodeTaskStatus.queued ||
            t.status == CodeTaskStatus.inputRequired) {
          return t.copyWith(
            status: CodeTaskStatus.interrupted,
            completedAt: t.completedAt ?? DateTime.now(),
            errorMessage: t.errorMessage ?? '(restart 时被中断)',
          );
        }
        return t;
      }).toList();
      state = fixed;
      // 把"修正"过的状态写回 db, 避免下次重启再修一遍
      for (final t in fixed) {
        if (t.errorMessage == '(restart 时被中断)') {
          unawaited(_dao.upsert(t));
        }
      }
    } catch (e) {
      // db hydrate 失败 (schema / migration 错) 时退回内存模式, 不阻塞 UI
      state = const [];
    }
  }

  /// 创建任务并立即派给 adapter 跑。返回 task id。
  /// workspace 异步分配 (创建 worktree 几百 ms), 完成后 _runTask spawn agent。
  String createTask({
    required String prompt,
    required AgentKind agent,
    required PermissionMode mode,
    String? compareGroupId,
    String? projectId,
    String? model,
    CodeLaunchMode launchMode = CodeLaunchMode.auto,
    String? baseRef,
  }) {
    final now = DateTime.now();
    final title = _deriveTitle(prompt);
    final task = CodeTask(
      id: _uuid.v4(),
      title: title,
      prompt: prompt,
      agent: agent,
      mode: mode,
      status: CodeTaskStatus.queued,
      events: const [],
      cost: const TaskCost(),
      createdAt: now,
      compareGroupId: compareGroupId,
      originDeviceId: _originDeviceId,
      originDeviceLabel: _originDeviceLabel,
      projectId: projectId,
      updatedAt: now,
      model: model,
    );
    state = [task, ...state];
    unawaited(_dao.upsert(task));
    // 异步: 先 allocate workspace, 再 spawn agent
    _allocateAndRun(task.id, agent, title, launchMode, baseRef);
    return task.id;
  }

  /// 一次给 N 个 agent 派同一个 prompt — 创建一个 compareGroupId 把这些 task
  /// 关联起来, UI 主区"任务对比" Tab 并排显示。
  /// 返回 (groupId, [taskIds])。
  ({String groupId, List<String> taskIds}) createCompareGroup({
    required String prompt,
    required Set<AgentKind> agents,
    required PermissionMode mode,
    String? projectId,
    CodeLaunchMode launchMode = CodeLaunchMode.auto,
    String? baseRef,
  }) {
    final groupId = _uuid.v4();
    final taskIds = <String>[];
    for (final agent in agents) {
      taskIds.add(createTask(
        prompt: prompt,
        agent: agent,
        mode: mode,
        compareGroupId: groupId,
        projectId: projectId,
        launchMode: launchMode,
        baseRef: baseRef,
      ));
    }
    return (groupId: groupId, taskIds: taskIds);
  }

  Future<void> _allocateAndRun(String taskId, AgentKind agent, String title,
      [CodeLaunchMode launchMode = CodeLaunchMode.auto, String? baseRef]) async {
    try {
      final ws = await _workspaces.allocate(
        taskId: taskId,
        agent: agent,
        promptFirstLine: title,
        launchMode: launchMode,
        baseRef: baseRef,
      );
      _patch(taskId, (t) => t.copyWith(workspace: ws.ref));
    } catch (e) {
      _patch(
        taskId,
        (t) => t.copyWith(
          status: CodeTaskStatus.failed,
          completedAt: DateTime.now(),
          errorMessage: 'workspace allocation failed: $e',
        ),
      );
      return;
    }
    _runTask(taskId);
  }

  void _runTask(String taskId) {
    _patch(taskId, (t) => t.copyWith(status: CodeTaskStatus.running));
    final task = state.firstWhere((t) => t.id == taskId);
    _adapter.run(task).listen(
      (event) => _onEvent(taskId, event),
      onError: (Object e) => _patch(
        taskId,
        (t) => t.copyWith(
          status: CodeTaskStatus.failed,
          completedAt: DateTime.now(),
          errorMessage: e.toString(),
        ),
      ),
    );
  }

  void _onEvent(String taskId, AgentEvent event) {
    _patch(taskId, (t) {
      // agent_status 是纯状态信号(hook 驱动 running↔input_required):不进事件列表
      // (不渲染成消息),只在非终态时推进状态。终态已定(done/failed/interrupted)则忽略,
      // 避免 watcher 收尾时的迟到帧覆盖终态。
      if (event is AgentStatus) {
        if (t.status == CodeTaskStatus.done ||
            t.status == CodeTaskStatus.failed ||
            t.status == CodeTaskStatus.interrupted) {
          return t;
        }
        return t.copyWith(
          status: event.status == 'input_required'
              ? CodeTaskStatus.inputRequired
              : CodeTaskStatus.running,
        );
      }
      final newEvents = [...t.events, event];
      // 状态机推进
      if (event is TaskFinished) {
        final status = switch (event.reason) {
          'canceled' => CodeTaskStatus.interrupted,
          'error' => CodeTaskStatus.failed,
          _ => CodeTaskStatus.done,
        };
        // 任务结束 → 异步收集 artifact 元数据 (L1)。仅 done / interrupted
        // 也跑 (用户可能想看跑了一半的产出); failed 跳过避免误传半成品。
        if (status != CodeTaskStatus.failed) {
          unawaited(_collectArtifacts(taskId));
        }
        return t.copyWith(
          events: newEvents,
          status: status,
          completedAt: DateTime.now(),
          errorMessage: event.errorMessage,
        );
      }
      if (event is CostUpdate) {
        return t.copyWith(
          events: newEvents,
          cost: TaskCost(
            usd: event.totalUsd,
            inputTokens: event.inputTokens,
            outputTokens: event.outputTokens,
            contextTokens: event.contextTokens,
            contextWindow: event.contextWindow,
          ),
        );
      }
      if (event is PermissionAsk) {
        return t.copyWith(
          events: newEvents,
          status: CodeTaskStatus.inputRequired,
        );
      }
      return t.copyWith(events: newEvents);
    });
  }

  /// 任务完成后扫 worktree 拿 artifact 元数据 (L1) → dao + outbox。
  /// 设计文档 docs/BiuMind-Code-Artifacts-Sync-Design.md §4。
  ///
  /// 不阻塞 controller 状态推进, 失败仅日志 (产物收集是 nice-to-have, 不应
  /// 让任务流程退化)。
  Future<void> _collectArtifacts(String taskId) async {
    try {
      final ws = _workspaces.lookup(taskId);
      if (ws == null) return;
      final task = state.firstWhere(
        (t) => t.id == taskId,
        orElse: () =>
            throw StateError('task $taskId not in state during collect'),
      );
      final arts = await ws.collectArtifacts(
        taskId: taskId,
        taskCreatedAt: task.createdAt,
      );
      if (arts == null || arts.isEmpty) return;
      for (final a in arts) {
        await _artifacts.upsert(a); // 本地产物元数据 (L1);云同步已废弃
      }
    } catch (e) {
      // intentionally non-fatal
      // ignore: avoid_print
      print('[code/collectArtifacts] task=$taskId err=$e');
    }
  }

  Future<void> cancel(String taskId) async {
    await _adapter.cancel(taskId);
  }

  Future<void> respondPermission(
    String taskId,
    String toolId,
    PermissionDecision d,
  ) async {
    await _adapter.respondPermission(toolId, d);
    _patch(taskId, (t) => t.copyWith(status: CodeTaskStatus.running));
  }

  /// 审批回写。codeSync 已废弃 → 无跨设备任务,恒走本机 respondPermission。
  /// (UI 保留调用点;跨设备远控将由 Runtime v3 agent-plane 在 M6 实现,不复用旧 sync。)
  Future<void> submitPermissionDecision(
    CodeTask task,
    String toolId,
    PermissionDecision d,
  ) async {
    await respondPermission(task.id, toolId, d);
  }

  /// 停止任务。同上,恒走本机 adapter.cancel。
  Future<void> submitCancel(CodeTask task) async {
    await cancel(task.id);
  }

  /// 手动标记任务完成。用户认为任务实质已结束、
  /// 续跑中断/暂停/等输入的任务(G5)。用持久化的
  /// session id 让 agent --resume 续上原会话(不重发 prompt),复用原工作区
  /// (task.workspace 已持久化)。无 session id(不可续跑)则忽略。
  void resume(String taskId) {
    final idx = state.indexWhere((t) => t.id == taskId);
    if (idx < 0) return;
    final task = state[idx];
    final sid = task.resumeSessionId;
    if (sid == null) return;
    _patch(taskId, (t) => t.copyWith(status: CodeTaskStatus.running));
    final running = state.firstWhere((t) => t.id == taskId);
    _adapter.run(running, resume: true, resumeSessionId: sid).listen(
      (event) => _onEvent(taskId, event),
      onError: (Object e) => _patch(
        taskId,
        (t) => t.copyWith(
          status: CodeTaskStatus.failed,
          completedAt: DateTime.now(),
          errorMessage: e.toString(),
        ),
      ),
    );
  }

  /// 重新连接一个 detached 任务 —— 后端 daemon 幸存、PTY 仍在跑(热重启/崩溃重连)。
  /// 不 spawn 新进程,把活动终端输出重绑到当前连接(「重新连接」)。若后端
  /// 报进程其实已退,adapter 归一化为失败,上层退回「恢复」续跑。
  void reattach(String taskId) {
    final idx = state.indexWhere((t) => t.id == taskId);
    if (idx < 0) return;
    _patch(taskId, (t) => t.copyWith(status: CodeTaskStatus.running));
    final running = state.firstWhere((t) => t.id == taskId);
    _adapter
        .run(running, reattach: true, resumeSessionId: running.resumeSessionId)
        .listen(
      (event) => _onEvent(taskId, event),
      onError: (Object e) => _patch(
        taskId,
        (t) => t.copyWith(
          status: CodeTaskStatus.failed,
          completedAt: DateTime.now(),
          errorMessage: e.toString(),
        ),
      ),
    );
  }

  /// bridge 就绪后对账(用 active task ids 归一化):传入 daemon 仍在跑的
  /// task id 集。本地标 interrupted 但进程其实还活的任务 → 升级为 detached(可重连);
  /// 不在集中的保持 interrupted。controller 不直接依赖 bridge,live 集由调用方查好传入。
  void reconcileLiveTasks(Set<String> liveIds) {
    if (liveIds.isEmpty) return;
    for (final t in state) {
      if (t.status == CodeTaskStatus.interrupted && liveIds.contains(t.id)) {
        _patch(t.id, (x) => x.copyWith(status: CodeTaskStatus.detached));
      }
    }
  }

  /// 但 agent 仍卡在 input_required/paused 时,直接收口到 done。
  /// 仅改本地状态,不动 adapter(进程若仍在跑,由用户另行 cancel)。
  void markComplete(String taskId) {
    _patch(taskId, (t) {
      if (t.status == CodeTaskStatus.done) return t;
      return t.copyWith(
        status: CodeTaskStatus.done,
        completedAt: t.completedAt ?? DateTime.now(),
      );
    });
  }

  /// 重命名任务标题。空标题忽略。
  void renameTask(String taskId, String title) {
    final t = title.trim();
    if (t.isEmpty) return;
    _patch(taskId, (task) => task.copyWith(title: t));
  }

  /// 切换星标。
  void toggleStar(String taskId) {
    _patch(taskId, (t) => t.copyWith(starred: !t.starred));
  }

  void _patch(String taskId, CodeTask Function(CodeTask) updater) {
    CodeTask? updated;
    state = [
      for (final t in state)
        if (t.id == taskId)
          (updated = updater(t))
        else
          t,
    ];
    // 同步写 db — fire-and-forget, sqlite 写吞吐够 (千次/秒)
    if (updated != null) {
      unawaited(_dao.upsert(updated));
    }
  }

  /// 把 prompt 第一句 / 前 50 字作为标题
  static String _deriveTitle(String prompt) {
    final s = prompt.trim();
    if (s.isEmpty) return '(untitled)';
    final firstLine = s.split('\n').first.trim();
    if (firstLine.length <= 50) return firstLine;
    return '${firstLine.substring(0, 47)}…';
  }
}

// ─── Providers ───────────────────────────────────────────

final agentAdapterProvider = Provider<AgentAdapter>((ref) {
  // Web / 移动 不支持 spawn binary → 走 Dummy 演示
  if (kIsWeb) return DummyAdapter();
  if (!Platform.isMacOS && !Platform.isLinux && !Platform.isWindows) {
    return DummyAdapter();
  }

  // 测试 / 演示模式：BIUMIND_AGENT=dummy 强制 dummy
  final pe = Platform.environment;
  if (pe['BIUMIND_AGENT'] == 'dummy') return DummyAdapter();

  String resolve(String? userVal, String envKey, String defaultVal) {
    if (userVal != null && userVal.isNotEmpty) return userVal;
    final env = pe[envKey];
    if (env != null && env.isNotEmpty) return env;
    return defaultVal;
  }

  // ⚠️ 关键:本 provider **不 watch** 任何易变 provider(token / settings / bridge)。
  // 历史:watch(codeBridgeClientProvider) 曾致 bridge 连断 → 重建 adapter → 重建
  // tasksController → _hydrate 把运行中任务误标 interrupted;后又发现 watch
  // hubCredentials/settings 在 app resume(token 刷新)时同样重建 adapter →
  // 重建 tasksController → 任务列表重载 → 正在看的任务卸载重挂(清空终端缓冲 +
  // 视图重置回结构化)。根治:env / 路径 / cwd / bridge **全部 spawn 时 ref.read
  // 惰性取最新**,provider 与易变状态彻底解耦,生命周期稳定。
  Map<String, String> resolveEnv() {
    final shellEnv = ref.read(loginShellEnvProvider).valueOrNull;
    final creds = ref.read(hubCredentialsProvider);
    return <String, String>{
      ...pe,
      if (shellEnv != null) ...shellEnv.env,
      if (creds != null) ...{
        'BIUMIND_TOKEN': creds.bearerToken,
        'BIUMIND_MODEL_RELAY_URL': creds.endpoint.toString(),
      },
    };
  }

  String resolveCwd() {
    final settings =
        ref.read(settingsControllerProvider).valueOrNull ?? const AppSettings();
    return resolve(settings.codeWorkingDir, 'BIUMIND_BIU_CWD',
        resolveEnv()['HOME'] ?? '.');
  }

  String resolveBiuPath() {
    final settings =
        ref.read(settingsControllerProvider).valueOrNull ?? const AppSettings();
    return resolve(settings.codeBiuPath, 'BIUMIND_BIU_PATH', 'biu');
  }

  CodeBridgeClient? resolveBridge() => ref.read(codeBridgeClientProvider);

  return MultiplexAgentAdapter(factory: (kind) {
    switch (kind) {
      case AgentKind.biu:
        // biu = 进程内 SDK Protocol,产结构化事件(agent_stream 渲染),不走 PTY。
        return BiuAdapter(
          binaryPathResolver: resolveBiuPath,
          workingDirResolver: resolveCwd,
          envResolver: resolveEnv,
        );
      case AgentKind.claudeCode:
        return BridgeTerminalAdapter(
            resolveClient: resolveBridge, agentType: 'claude');
      case AgentKind.codex:
        return BridgeTerminalAdapter(
            resolveClient: resolveBridge, agentType: 'codex');
    }
  });
});

final codeTasksProvider =
    StateNotifierProvider<CodeTasksController, List<CodeTask>>((ref) {
  // 只 select 稳定字段(设备标识,启动后不变),**不 watch 整个 settings** ——
  // 否则 token 刷新(每次 app resume)会重建本 controller → 任务列表重载 →
  // 正在看的任务卸载重挂(清空终端 + 视图重置)。agentAdapterProvider 已解耦易变态。
  final device = ref.watch(settingsControllerProvider.select((s) {
    final v = s.valueOrNull;
    return (v?.codeOriginDeviceId, v?.codeOriginDeviceLabel);
  }));
  return CodeTasksController(
    ref.watch(agentAdapterProvider),
    ref.watch(workspaceManagerProvider),
    ref.watch(codeTasksDaoProvider),
    ref.watch(codeTaskArtifactsDaoProvider),
    device.$1,
    device.$2,
  );
});

/// 任务管理操作 — UI 右键菜单 / 详情面板用。
extension CodeTasksControllerActions on CodeTasksController {
  Future<void> purgeWorkspace(String taskId) async {
    await _workspaces.purge(taskId);
  }

  /// 删除任务 — 同步删 db + 清理 worktree (默认强删, 任务历史用户已经决定不要)。
  Future<void> deleteTask(String taskId, {bool purgeWorktree = true}) async {
    if (purgeWorktree) {
      try {
        await _workspaces.purge(taskId);
      } catch (_) {/* worktree 已被手动删 / git 报错时忽略 */}
    }
    await _dao.deleteById(taskId);
    state = state.where((t) => t.id != taskId).toList();
  }

}

/// 主区当前显示的任务 id（null = 空状态）
final activeCodeTaskIdProvider = StateProvider<String?>((_) => null);

/// 当前激活任务（便于 UI 单点 watch）
final activeCodeTaskProvider = Provider<CodeTask?>((ref) {
  final id = ref.watch(activeCodeTaskIdProvider);
  if (id == null) return null;
  final tasks = ref.watch(codeTasksProvider);
  for (final t in tasks) {
    if (t.id == id) return t;
  }
  return null;
});

/// 当前激活任务所属的对比组成员 (按创建时间正序排列)。
/// 没有对比组 / 任务不在组里 → 返回 null (主区不显示对比 Tab)。
final activeCompareGroupProvider = Provider<List<CodeTask>?>((ref) {
  final active = ref.watch(activeCodeTaskProvider);
  if (active?.compareGroupId == null) return null;
  final groupId = active!.compareGroupId!;
  final tasks = ref
      .watch(codeTasksProvider)
      .where((t) => t.compareGroupId == groupId)
      .toList()
    ..sort((a, b) => a.createdAt.compareTo(b.createdAt));
  if (tasks.length < 2) return null;
  return tasks;
});
