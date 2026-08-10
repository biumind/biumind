// ChatController —— Chat 重构 R3。
//
// Riverpod FamilyAsyncNotifier per thread。封装 BiuSessionConnection
// 生命周期，给 UI 提供 sendMessage / cancel / regenerate 入口。
//
// 状态来源：
//
//   - build() 时尝试 BiuSessionConnection.resume() —— 跨设备 / 重启 /
//     网络恢复时拉历史 + 接实时
//   - sendMessage() 没活跃 connection 就 open()，已有就 sendUserMessage()
//   - SessionEvent stream → 更新 ChatState（streaming / completed / failed
//     / cancelled / idle）
//   - thread + messages 数据本身通过 ChatRepo.watchMessages 直接 watch，
//     **不重复在 ChatController state 里冗余存**（避免双源不一致）
//
// 用法（UI 层）：
//
//   final state = ref.watch(chatControllerProvider(threadId));
//   final messages = ref.watch(messagesProvider(threadId));
//   ref.read(chatControllerProvider(threadId).notifier).sendMessage('hi');

import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:logging/logging.dart';
import 'package:uuid/uuid.dart';

import '../../../data/agent_plane/agent_plane_client.dart';
import '../../../data/api/_http_helpers.dart' show ApiError;
import '../../../data/api/biu_client.dart' show BiuTransport;
import '../../../data/api/chat_client.dart';
import '../../../data/api/identity_client.dart' show IdentityApiError;
import '../../../data/outbox/chat_outbox_flusher.dart';
import '../../settings/application/api_keys_providers.dart';
import '../../../data/agent_plane/biu_daemon_manager.dart';
import 'client_side_resolver.dart';
import '../../../data/providers_providers.dart'
    show providersListProvider, modelsListProvider, relayCatalogListProvider;
import '../../../data/wiki_providers.dart' show appDbProvider;
import '../data/chat_model_groups.dart' show chatModelGroupsProvider;
import '../../../services/auth_service.dart';
import '../../creation/application/credits_controller.dart';
import '../data/biu_session_connection.dart';
import '../data/chat_repo.dart';
import '../data/chat_scope.dart';
import '../sync/chat_sync_manager.dart';
import '../domain/chat_models.dart';
import '../domain/thread_title.dart';
import 'chat_preferences.dart';

/// chat 错误的后续动作 —— 错误 banner 据此决定是否给一键修复入口。
/// none=仅文案;reselectModel=「重新选择模型」(模型停用/不存在/无渠道);
/// upgradePlan=「升级会员」(模型被 plan 门禁)。
enum ChatErrorAction { none, reselectModel, upgradePlan }

/// ChatState —— UI 渲染需要的高层状态。具体消息/blocks 不在这里（走
/// messagesProvider）；这里只放 connection 元状态 + 错误信息。
class ChatState {
  /// 当前 connection 是否活跃（streaming 中）。UI 用这个画 spinner +
  /// disabled 输入框 + cancel 按钮。
  final bool isStreaming;

  /// 用户已按 stop 但 brain 的 Done{interrupted} 还没到达 —— 中间态。
  /// Composer 据此把 stop 按钮换成 "Stopping..." 并 disable,防止用户
  /// 重复 spam。F5 实际打断在 brain 端,客户端只能等 result 帧落地。
  /// isCancelling=true 时 isStreaming 仍为 true(请求未真正结束)。
  final bool isCancelling;

  /// 上次错误（result_error / 网络断 / open() 失败 / 等）。用户重发后清空。
  final String? lastError;

  /// lastError 的后续动作(默认 none)。reselectModel 时 banner 给「重新选择
  /// 模型」按钮。随 clearError 一并清回 none。
  final ChatErrorAction lastErrorAction;

  /// 当前 streaming assistant message id（UI 用于高亮 / 滚动到底）。
  /// idle 时 null。
  final String? activeAssistantMessageId;

  const ChatState({
    this.isStreaming = false,
    this.isCancelling = false,
    this.lastError,
    this.lastErrorAction = ChatErrorAction.none,
    this.activeAssistantMessageId,
  });

  ChatState copyWith({
    bool? isStreaming,
    bool? isCancelling,
    String? lastError,
    ChatErrorAction? lastErrorAction,
    String? activeAssistantMessageId,
    bool clearError = false,
    bool clearActiveMessage = false,
  }) {
    return ChatState(
      isStreaming: isStreaming ?? this.isStreaming,
      isCancelling: isCancelling ?? this.isCancelling,
      lastError: clearError ? null : (lastError ?? this.lastError),
      lastErrorAction: clearError
          ? ChatErrorAction.none
          : (lastErrorAction ?? this.lastErrorAction),
      activeAssistantMessageId: clearActiveMessage
          ? null
          : (activeAssistantMessageId ?? this.activeAssistantMessageId),
    );
  }
}

/// 配置入口：UI 装载时通过 override 注入。production main.dart 只 override
/// 一次。测试 ProviderContainer override 自定义 deps。
class ChatControllerDeps {
  final ChatRepo repo;
  final AgentPlaneClient agentPlane;
  final ChatClient chatClient;
  final String brainBaseUrl;
  /// 测试注入的 transport factory；生产留 null。
  final BiuTransport Function(Uri)? transportConnector;

  const ChatControllerDeps({
    required this.repo,
    required this.agentPlane,
    required this.chatClient,
    required this.brainBaseUrl,
    this.transportConnector,
  });
}

/// chatControllerDepsProvider —— 默认从 appDbProvider + hubCredentialsProvider
/// 拼出真依赖。未登录（creds == null）时 throw，让 router gate 把用户推
/// 到登录页。测试用 overrideWithValue 注 fake 依赖。
///
/// brain HTTP / WS endpoint —— 单 origin: /v1/agent/* /v1/threads/* 等由
/// site nginx 按路径反代到 brain, client 不换端口。
final chatControllerDepsProvider = Provider<ChatControllerDeps>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) {
    throw StateError('chatControllerDepsProvider: 未登录');
  }
  // P0 数据隔离：owner scope 派生不出来（token 非 JWT / 无 sub）视同未登录，
  // 交给 router gate —— ChatRepo 构造必填 scope，不存在「不过滤」的路径。
  final scope = ref.watch(chatOwnerScopeProvider);
  if (scope == null) {
    throw StateError('chatControllerDepsProvider: 无法派生 chat owner scope');
  }
  final db = ref.watch(appDbProvider);
  final brainUri = creds.endpoint;
  // brain HTTP base — agent_plane REST。WS base 把 https→wss / http→ws。
  final wsScheme = brainUri.scheme == 'https' ? 'wss' : 'ws';
  final brainWsBase = brainUri.replace(scheme: wsScheme).toString();
  final brainHttpBase = brainUri.toString();
  // 末尾去掉 / 让上层拼路径不重斜杠。
  String stripSlash(String s) => s.endsWith('/') ? s.substring(0, s.length - 1) : s;
  return ChatControllerDeps(
    repo: ChatRepo(db, scope: scope),
    agentPlane: AgentPlaneClient(
      baseUrl: stripSlash(brainHttpBase),
      // 现读 token (不捕获 build-time creds): chatController 用 ref.read 持本
      // deps 整个 thread 会话, _conn 也持 build-time agentPlane —— 若闭包捕获
      // creds 局部, token 轮换后永远返旧 access token, 致 createSession /
      // refreshSessionToken / listEnvironments 走 stale token 撞 401 (虽
      // 401-hook 自愈但每次 2× round-trip)。现读保证总返最新 token。同款
      // 写法见 chat_sync_manager.dart:135 tokenProvider。
      tokenProvider: () async => ref.read(hubCredentialsProvider)?.bearerToken,
    ),
    chatClient: ChatClient(brainUri, creds.bearerToken),
    brainBaseUrl: stripSlash(brainWsBase),
  );
});

/// ChatThreadOps —— 会话级写操作（删除 / 归档 / 重命名）入口。
///
/// 不挂 per-thread 的 ChatController family：那个 build() 会尝试 resume
/// session（拉历史 + 接实时），为「删一个没打开的会话」实例化它既浪费
/// 又有副作用。这里只持 [ChatControllerDeps] 做上行 + 本地写。
///
/// 上行失败语义（P1.3）：404（他端已删 / direct 会话服务端无此行）静默；
/// 其他失败入队 ChatOutbox 由 flusher 退避重试，不再 debugPrint 一发即丢。
/// 本地写永远执行 —— 本地优先，上行是收敛保证。
class ChatThreadOps {
  const ChatThreadOps(this._deps, [this._outboxFlusher]);

  final ChatControllerDeps _deps;
  final ChatOutboxFlusher? _outboxFlusher;

  /// 删除单个会话：先上行 brain（DELETE /v1/threads/{id}），再本地级联删。
  Future<void> deleteThread(String threadId) => deleteThreads([threadId]);

  /// 批量删除：逐个上行（全部上行完 / 入队完）再一次性 [ChatRepo.deleteThreads]。
  Future<void> deleteThreads(List<String> ids) async {
    for (final id in ids) {
      try {
        await _deps.chatClient.deleteThread(id);
      } catch (e) {
        if (e is IdentityApiError && e.status == 404) {
          // 他端已删 / direct 会话服务端无此行 —— 静默。
        } else {
          await _enqueue(ChatRepo.outboxOpDeleteThread, id, const {}, '$e');
        }
      }
    }
    await _deps.repo.deleteThreads(ids);
  }

  /// 归档：先上行（PATCH archived=true）再写本地 —— 修原
  /// ChatRepo.archiveThread 只写本地、他端不感知的同款 bug。失败处理
  /// 同删除。
  Future<void> archiveThread(String threadId) async {
    try {
      await _deps.chatClient.patchThread(threadId, archived: true);
    } catch (e) {
      if (e is IdentityApiError && e.status == 404) {
        // direct 会话服务端无此行 —— 静默。
      } else {
        await _enqueue(
          ChatRepo.outboxOpArchiveThread,
          threadId,
          const {'archived': true},
          '$e',
        );
      }
    }
    await _deps.repo.archiveThread(threadId);
  }

  /// 重命名：先上行（PATCH title）再写本地。失败处理同删除。
  Future<void> renameThread(String threadId, String title) async {
    try {
      await _deps.chatClient.patchThread(threadId, title: title);
    } catch (e) {
      if (e is IdentityApiError && e.status == 404) {
        // direct 会话服务端无此行 —— 静默。
      } else {
        await _enqueue(
          ChatRepo.outboxOpRenameThread,
          threadId,
          {'title': title},
          '$e',
        );
      }
    }
    await _deps.repo.renameThread(threadId, title);
  }

  /// 上行失败（非 404）入队 + kick flusher 立即首试。入队本身失败只告警
  /// 不阻塞本地写 —— 本地优先语义不变。
  Future<void> _enqueue(
    String op,
    String threadId,
    Map<String, dynamic> payload,
    String error,
  ) async {
    try {
      await _deps.repo
          .enqueueOutbox(op: op, threadId: threadId, payload: payload);
      unawaited(_outboxFlusher?.kick());
      debugPrint('[chat] $op 上行失败已入队重试: $error');
    } catch (e) {
      debugPrint('[chat] $op 上行失败且入队失败（本地仍生效）: $error / $e');
    }
  }
}

/// chatThreadOpsProvider —— UI 删除 / 归档 / 重命名会话的统一入口
/// （带上行同步 + outbox 重试）。flusher 由 ChatSyncManager 按登录态持有；
/// 未登录（无 scope）时 deps provider 已 throw，走不到这里。
final chatThreadOpsProvider = Provider<ChatThreadOps>((ref) {
  return ChatThreadOps(
    ref.watch(chatControllerDepsProvider),
    ref.watch(chatSyncManagerProvider).outboxFlusher,
  );
});

class ChatController extends FamilyAsyncNotifier<ChatState, String> {
  BiuSessionConnection? _conn;
  StreamSubscription<SessionEvent>? _eventSub;
  static const _uuid = Uuid();

  @override
  Future<ChatState> build(String threadId) async {
    final deps = ref.read(chatControllerDepsProvider);
    final thread = await deps.repo.getThread(threadId);
    if (thread == null) {
      // 还没创 thread —— UI 应该先 createThread 再进入这个 controller。
      // 返空 state 让 UI 显示 empty。
      return const ChatState();
    }
    // 尝试 resume：thread 有 active session 时拉起来续帧
    final maybe = await BiuSessionConnection.resume(
      repo: deps.repo,
      agentPlane: deps.agentPlane,
      brainBaseUrl: deps.brainBaseUrl,
      thread: thread,
      transportConnector: deps.transportConnector,
    );
    if (maybe == null) return const ChatState();

    _conn = maybe;
    _bindEvents(maybe.events);
    ref.onDispose(_disposeConnection);
    return ChatState(
      isStreaming: true,
      activeAssistantMessageId: null, // resume 时由 SessionEvent 更新
    );
  }

  /// 发一条 user message。没活跃 connection → 创新 brain session；
  /// 有活跃 connection → 推到现 session（多 turn，v1 brain 端待支持）。
  ///
  /// [attachments]：图片附件（image/*）；新会话场景下会作为 ImageBlock 挂到
  /// user message 之后让 UI 立刻渲染。多 turn 场景目前忽略 attachments
  /// （brain 协议未透传），UI 一律按 text-only 走。
  Future<void> sendMessage(String text, {String? userMessageId, String? assistantMessageId, List<AttachmentInput> attachments = const []}) async {
    final hasText = text.trim().isNotEmpty;
    final hasAttachments = attachments.isNotEmpty;
    if (!hasText && !hasAttachments) return;
    final deps = ref.read(chatControllerDepsProvider);
    final thread = await deps.repo.getThread(arg);
    if (thread == null) {
      state = AsyncValue.data(state.value!.copyWith(
        lastError: 'thread not found',
        lastErrorAction: ChatErrorAction.none,
      ));
      return;
    }
    final umId = userMessageId ?? _uuid.v4();
    final amId = assistantMessageId ?? _uuid.v4();

    final c = _conn;
    if (c != null && !state.value!.isStreaming.isFalse) {
      // 多 turn：往现有 session 推
      try {
        await c.sendUserMessage(text, userMessageId: umId);
      } catch (e) {
        state = AsyncValue.data(state.value!.copyWith(
          lastError: e.toString(),
          lastErrorAction: ChatErrorAction.none,
        ));
      }
      return;
    }

    // 新 session：thread 第一条消息时顺手把 title 从首句 prompt 推出来。
    // 后续 sidebar / cross-thread 搜索看到的就是真标题而不是"新对话"。
    // 用户在 Settings 里关掉 autoRenameEnabled → 跳过。
    final prefs = ref.read(chatPreferencesProvider);
    if (prefs.autoRenameEnabled && thread.title.trim().isEmpty && hasText) {
      final title = titleFromPrompt(text);
      if (title.isNotEmpty) {
        try {
          await deps.repo.renameThread(thread.id, title);
        } catch (_) {/* 失败不阻塞发送 —— 标题不是关键路径 */}
      }
    }

    state = AsyncValue.data((state.value ?? const ChatState()).copyWith(
      isStreaming: true,
      activeAssistantMessageId: amId,
      clearError: true,
    ));
    try {
      // B2: client-side BYOK 分流 —— 本地有匹配 key → 走本机 daemon agent 模式
      // （完整 tool loop），不再 DirectSessionController 纯对话。key 经 loopback
      // 推 daemon 内存，不经 brain。
      final useModel = thread.model ?? '';
      final target = await _resolveDirectTarget(deps, thread.providerId, useModel);
      if (target != null) {
        await _runClientSideViaDaemon(
          deps: deps,
          thread: thread,
          prompt: text,
          userMessageId: umId,
          assistantMessageId: amId,
          target: target,
          attachments: attachments,
        );
        return;
      }
      final conn = await _openWithSelfHeal(
        deps: deps,
        thread: thread,
        userPrompt: text,
        userMessageId: umId,
        assistantMessageId: amId,
        attachments: attachments,
      );
      await _disposeConnection();
      _conn = conn;
      _bindEvents(conn.events);
      ref.onDispose(_disposeConnection);
    } catch (e) {
      state = AsyncValue.data(state.value!.copyWith(
        isStreaming: false,
        lastError: _humanizeOpenError(e),
        lastErrorAction: ChatErrorAction.none,
        clearActiveMessage: true,
      ));
    }
  }

  /// P5: 解析 client-side 直连目标. identity 列表加载失败或无匹配 → null (走 cloud).
  Future<ClientSideTarget?> _resolveDirectTarget(
      ChatControllerDeps deps, String? providerId, String model) async {
    if (model.isEmpty) return null;
    try {
      final keys = await ref.read(apiKeysListProvider.future);
      return resolveClientSide(keys, providerId, model);
    } catch (_) {
      return null;
    }
  }

  /// B2: client-side BYOK 命中 → 走本机 daemon agent 模式（完整 tool loop）。
  /// 覆盖 DirectSessionController（退役）：key 经 loopback 推 daemon 内存 +
  /// 切 agent 模式 + 本机 daemon env_id + createSession 带 client-side 信号 →
  /// daemon 用本地 key 建 engine 直连上游，不经 model-relay。daemon 不可用 →
  /// 报错引导（无 DirectSessionController 降级，client-side 需 daemon 跑 loop）。
  Future<void> _runClientSideViaDaemon({
    required ChatControllerDeps deps,
    required Thread thread,
    required String prompt,
    required String userMessageId,
    required String assistantMessageId,
    required ClientSideTarget target,
    required List<AttachmentInput> attachments,
  }) async {
    final daemonEnvId = ref.read(biuDaemonStateProvider).valueOrNull?.daemonEnvId;
    if (daemonEnvId == null || daemonEnvId.isEmpty) {
      state = AsyncValue.data(state.value!.copyWith(
        isStreaming: false,
        lastError: '本机 daemon 未启动，client-side BYOK 需要 daemon 运行 tool loop（请确认桌面端 daemon 已就绪）',
        lastErrorAction: ChatErrorAction.none,
        clearActiveMessage: true,
      ));
      return;
    }
    // client-side = 要 tool loop = agent 模式 + 本机 daemon env_id（定向投 work
    // 到本机；daemon 命中时调 identity 取 key 本机直连）。
    await deps.repo.setThreadMode(
        thread.id, ThreadMode.agent, environmentId: daemonEnvId);
    final fresh = await deps.repo.getThread(thread.id) ?? thread;
    try {
      final conn = await _openWithSelfHeal(
        deps: deps,
        thread: fresh,
        userPrompt: prompt,
        userMessageId: userMessageId,
        assistantMessageId: assistantMessageId,
        attachments: attachments,
        clientSideRecordId: target.recordId,
        clientSideBaseUrl: target.baseUrl,
        clientSideProtocol: target.protocol,
      );
      await _disposeConnection();
      _conn = conn;
      _bindEvents(conn.events);
      ref.onDispose(_disposeConnection);
    } catch (e) {
      if (!state.hasError) {
        state = AsyncValue.data(state.value!.copyWith(
          isStreaming: false,
          lastError: '本机直连上游失败: $e（需桌面 daemon 运行 + 可达上游）',
          lastErrorAction: ChatErrorAction.none,
          clearActiveMessage: true,
        ));
      }
    }
  }

  /// 跑 BiuSessionConnection.open;若 brain 返 404 environment_not_found
  /// (Agent 模式选了一台已经被 brain janitor GC 的 stale daemon),自动:
  ///   1. 失效 agentEnvironmentsProvider 拉最新 daemon 列表
  ///   2. 找一台在线 biu_daemon → setThreadMode(agent, env=newId) 重试
  ///   3. 没在线 daemon → 降级 setThreadMode(chat) 重试
  /// 如此用户感受到的是"我重发就行",而不是反复看到 stale env 404。
  Future<BiuSessionConnection> _openWithSelfHeal({
    required ChatControllerDeps deps,
    required Thread thread,
    required String userPrompt,
    required String userMessageId,
    required String assistantMessageId,
    required List<AttachmentInput> attachments,
    String? clientSideRecordId,
    String? clientSideBaseUrl,
    String? clientSideProtocol,
  }) async {
    try {
      return await BiuSessionConnection.open(
        repo: deps.repo,
        agentPlane: deps.agentPlane,
        brainBaseUrl: deps.brainBaseUrl,
        thread: thread,
        userPrompt: userPrompt,
        userMessageId: userMessageId,
        assistantMessageId: assistantMessageId,
        attachments: attachments,
        transportConnector: deps.transportConnector,
        clientSideRecordId: clientSideRecordId,
        clientSideBaseUrl: clientSideBaseUrl,
        clientSideProtocol: clientSideProtocol,
      );
    } catch (e) {
      if (!_isStaleEnvironmentError(e) || thread.mode != ThreadMode.agent) {
        rethrow;
      }
      // 自愈第一步:清掉第一次 BiuSessionConnection.open 已经写到本地 DB
      // 的 user message + 占位 assistant message + 它们的 blocks。
      // 不清的话 retry 时同 id 再 INSERT 撞 UNIQUE 约束(SqliteException 1555)。
      // deleteMessages 走 cascade 会顺手清 chat_content_blocks 行。
      await deps.repo.deleteMessages([userMessageId, assistantMessageId]);
      // 自愈第二步：刷新 environment 列表
      ref.invalidate(agentEnvironmentsProvider);
      final envs = await ref.read(agentEnvironmentsProvider.future);
      final online = envs
          .where((env) => env.workerKind == 'biu_daemon' && env.isOnline)
          .toList();
      if (online.isEmpty) {
        // 没在线 daemon → 降级到 chat 模式,让用户的话至少能跟模型聊
        await deps.repo.setThreadMode(thread.id, ThreadMode.chat);
      } else {
        await deps.repo.setThreadMode(
          thread.id,
          ThreadMode.agent,
          environmentId: online.first.environmentId,
        );
      }
      // 重读最新 thread 后再开一次。失败这次不再 self-heal,直接抛。
      final fresh = await deps.repo.getThread(thread.id) ?? thread;
      return await BiuSessionConnection.open(
        repo: deps.repo,
        agentPlane: deps.agentPlane,
        brainBaseUrl: deps.brainBaseUrl,
        thread: fresh,
        userPrompt: userPrompt,
        userMessageId: userMessageId,
        assistantMessageId: assistantMessageId,
        attachments: attachments,
        transportConnector: deps.transportConnector,
        clientSideRecordId: clientSideRecordId,
        clientSideBaseUrl: clientSideBaseUrl,
        clientSideProtocol: clientSideProtocol,
      );
    }
  }

  /// brain agent_plane router 在 environment 不存在时返 404 not_found,
  /// body 形状: {"error":{"code":"not_found","message":"agentplane: environment not found"}}。
  /// 跨用户访问 / 真删了 / janitor 清理 都走这条分支。
  static bool _isStaleEnvironmentError(Object e) {
    if (e is! ApiError) return false;
    if (e.status != 404) return false;
    return e.body.contains('environment not found') ||
        e.body.contains('"not_found"');
  }

  /// 把裸的 ApiError JSON 转成对人友好的中文短句,塞进 ChatState.lastError。
  /// 没识别到的 e 就用 e.toString() 兜底(跟之前行为一致)。
  static String _humanizeOpenError(Object e) {
    // Runtime v3 R7：不是错误——work 已排队，等离线设备上线自动开始。
    if (e is SessionPendingException) {
      return '目标设备当前离线；任务已排队，设备上线后会自动开始。';
    }
    if (e is ApiError) {
      if (e.status == 404 && e.body.contains('environment not found')) {
        return 'Agent 工作机已离线;已自动切回纯对话,请重发。';
      }
      if (e.status == 503 && e.body.contains('no_runtime_available')) {
        return '云端 runtime 暂不可用,请稍后再试。';
      }
      if (e.status == 409 && e.body.contains('environment_offline')) {
        return 'Agent 工作机离线中,请稍后再试。';
      }
    }
    return e.toString();
  }

  /// 流式失败(MessageFailed)→ 友好中文提示 + 后续动作。
  ///
  /// model-relay 在 resolve 失败时把 error.code 细分成 model_disabled /
  /// model_not_found / model_hidden_for_plan / model_no_channel /
  /// model_credential_unavailable / channel_quota_exhausted(见
  /// services/model-relay/internal/api/messages.go + router/resolver.go)。
  /// code 嵌在 engine 包过的错误串里
  /// (`provider stream: anthropic-engine: 502: {"error":{"code":"..."}} ...`),
  /// 故用正则提取 error.code —— 这是后端稳定契约字段,比匹配自由文本
  /// (status=disabled)健壮。识别不到就原样兜底、无动作。
  static ({String message, ChatErrorAction action}) _classifyStreamError(
      String raw) {
    final m = RegExp(r'"code"\s*:\s*"([a-z_]+)"').firstMatch(raw);
    switch (m?.group(1)) {
      case 'model_disabled':
        return (
          message: '当前模型已被停用,请重新选择一个可用模型。',
          action: ChatErrorAction.reselectModel,
        );
      case 'model_not_found':
        return (
          message: '当前模型不存在或已下架,请重新选择模型。',
          action: ChatErrorAction.reselectModel,
        );
      case 'model_no_channel':
        return (
          message: '当前模型暂无可用渠道,请稍后再试或换个模型。',
          action: ChatErrorAction.reselectModel,
        );
      case 'model_hidden_for_plan':
        return (
          message: '当前模型需要更高的会员套餐才能使用。',
          action: ChatErrorAction.upgradePlan,
        );
      case 'model_credential_unavailable':
        return (
          message: '模型凭据不可用,请联系管理员检查渠道配置。',
          action: ChatErrorAction.none,
        );
      case 'channel_quota_exhausted':
        return (
          message: '请求过于频繁(渠道额度暂时耗尽),请稍后再试。',
          action: ChatErrorAction.none,
        );
      default:
        return (message: raw, action: ChatErrorAction.none);
    }
  }

  /// 清除 ChatState.lastError —— UI 错误 banner dismiss 用。
  /// 不动 isStreaming / activeAssistantMessageId 等其它字段。
  void clearError() {
    final cur = state.value ?? const ChatState();
    if (cur.lastError == null) return;
    state = AsyncValue.data(cur.copyWith(clearError: true));
  }

  /// 取消当前 streaming —— 给 brain 发 cancel 控制帧,然后等服务端的
  /// Done{interrupted}(F5/d186112 之后存在)把 connection 干净结束。
  ///
  /// UI 行为:
  ///   - 立即标 isCancelling=true → Composer 显示 "Stopping..." 防 spam
  ///   - 不立刻 clearActiveMessage / isStreaming=false:那俩留给真正的
  ///     SessionClosed 事件触发,这样 stop 期间用户看到的还是当前
  ///     streaming 中的 message,不会突然消失。
  ///
  /// idempotent:重复调用直接落到 BiuSessionConnection.cancel() 的
  /// _cancelling 守护,二次发帧会被吞掉。
  Future<void> cancel() async {
    final c = _conn;
    if (c == null) return;
    final cur = state.value ?? const ChatState();
    if (cur.isCancelling) return;
    state = AsyncValue.data(cur.copyWith(isCancelling: true));
    await c.cancel();
    // 真正的 idle / clearActiveMessage 由 SessionClosed 事件处理
    // (_bindEvents 里的 listener)。这里不动 isStreaming —— 让 UI 在 stop
    // 落地前继续显示生成中的消息,只是按钮禁用。
  }

  /// 重新生成某条 assistant message。
  ///
  /// 语义：找到目标 assistant 之前最近的一条 user message，截断它（含）
  /// 之后的所有消息，用那条 user 文本作 prompt 重开 session。brain 端
  /// chat session single-shot 无 server-side 回退能力，故 regenerate =
  /// 新 brain session + 客户端截断历史。
  Future<void> regenerate(String assistantMessageId) async {
    final messages = await _snapshotMessages();
    if (messages == null) return;
    final targetIdx = messages.indexWhere((m) => m.id == assistantMessageId);
    if (targetIdx <= 0) return; // 找不到 / 是第一条没法 regenerate
    // 目标前最近一条 user message
    for (var i = targetIdx - 1; i >= 0; i--) {
      if (messages[i].role == MessageRole.user) {
        await _resendFromUser(messages[i], messages);
        return;
      }
    }
  }

  /// 从某条 user message 重新生成 —— 截断该 user（不含）之后的所有消息，
  /// 用该 user 文本作 prompt 重开 session。与 [regenerate] 对称：后者以
  /// assistant 为锚往前找 user，本方法以 user 自身为锚。
  ///
  /// UI 上对应「用户消息 → 重新生成」按钮（不动文本，纯重发）。
  Future<void> regenerateFromUserMessage(String userMessageId) async {
    final messages = await _snapshotMessages();
    if (messages == null) return;
    final idx = messages.indexWhere((m) => m.id == userMessageId);
    if (idx < 0) return;
    final target = messages[idx];
    if (target.role != MessageRole.user) return;
    await _resendFromUser(target, messages);
  }

  /// 编辑某条 message 的文本（user / assistant 通用）。
  ///
  /// 行为：纯本地改写 —— 保留原 message 的非 TextBlock（tool_use /
  /// tool_result / image），把所有 TextBlock 合并为一个新 TextBlock 写入
  /// [newText]。不自动重新生成（动作正交：要重发走 regenerate*）。
  ///
  /// assistant 编辑后的新文本会随该 thread 历史进入后续 brain session
  /// 上下文（与 regenerate 读本地 repo 一致）。user 编辑只改本地文本，
  /// 若想让新文本生效需手动重新生成。
  Future<void> editMessageText(String messageId, String newText) async {
    final deps = ref.read(chatControllerDepsProvider);
    final messages = await deps.repo.watchMessages(arg).first;
    final idx = messages.indexWhere((m) => m.id == messageId);
    if (idx < 0) return;
    final target = messages[idx];
    final rebuilt = <Block>[];
    var textInserted = false;
    for (final b in target.blocks) {
      if (b is TextBlock) {
        if (!textInserted) {
          // 复用首个 TextBlock 的 id/index，落库 replaceBlocks 走 upsert
          rebuilt.add(TextBlock(
            id: b.id,
            index: b.index,
            state: BlockState.closed,
            text: newText,
          ));
          textInserted = true;
        }
        // 其余 TextBlock 并入首个（assembledText 已拼接）
      } else {
        rebuilt.add(b);
      }
    }
    if (!textInserted) {
      // 原 message 无 TextBlock（纯 tool/image）—— 新增一个 text block
      rebuilt.insert(
        0,
        TextBlock(
          id: '${target.id}_text',
          index: 0,
          state: BlockState.closed,
          text: newText,
        ),
      );
    }
    await deps.repo.replaceBlocks(messageId, rebuilt);
    // 方案3 上行同步：message.id == brain id（client 生成透传），patchMessage
    // 直接命中。best-effort —— direct(P5) 模式消息不进 brain，404 仅告警，
    // 本地仍正确（direct 跨设备本就不同步）。
    try {
      await deps.chatClient.patchMessage(arg, messageId, content: newText);
    } catch (e) {
      debugPrint('[chat] editMessageText 上行失败（可能 direct 消息）: $e');
    }
  }

  /// 删除单条 message：本地 [ChatRepo.deleteMessages] + 上行 brain 同步。
  /// 上行 best-effort（direct 模式 404 仅告警，本地仍删）。regenerate 截断
  /// 也走此，使截断的删除同步 brain（跨设备一致，修原裸 SQL 删不同步的债）。
  Future<void> deleteMessage(String messageId) async {
    final deps = ref.read(chatControllerDepsProvider);
    try {
      await deps.chatClient.deleteMessage(arg, messageId);
    } catch (e) {
      debugPrint('[chat] deleteMessage 上行失败（可能 direct 消息）: $e');
    }
    await deps.repo.deleteMessages([messageId]);
  }

  /// 删除会话：本地级联删 + best-effort 上行 brain（跨设备一致）。
  /// 实现落在 [ChatThreadOps]（UI 经 chatThreadOpsProvider 调用，不为
  /// 删除实例化 per-thread controller）；已持有本 controller 的场景
  /// 直接调亦可。
  Future<void> deleteThread(String threadId) =>
      ref.read(chatThreadOpsProvider).deleteThread(threadId);

  /// 批量删除：逐个上行，全部上行完再一次性本地删。
  Future<void> deleteThreads(List<String> ids) =>
      ref.read(chatThreadOpsProvider).deleteThreads(ids);

  /// 拉一次当前 thread 消息快照；thread 不存在返回 null。
  Future<List<Message>?> _snapshotMessages() async {
    final deps = ref.read(chatControllerDepsProvider);
    final thread = await deps.repo.getThread(arg);
    if (thread == null) return null;
    return deps.repo.watchMessages(arg).first;
  }

  /// 截断 [pivot]（不含）之后的所有 message + 它们 blocks，再用 pivot
  /// 文本作 prompt 重开 session。regenerate / regenerateFromUserMessage 共用。
  ///
  /// 逐条走 [deleteMessage]：上行 brain 同步 + 本地删（含 blocks/reactions）。
  /// 截断同步到 brain → 跨设备 regenerate 一致。
  Future<void> _resendFromUser(Message pivot, List<Message> messages) async {
    final toDelete = messages.sublist(messages.indexOf(pivot) + 1);
    for (final m in toDelete) {
      await deleteMessage(m.id);
    }
    final prompt = pivot.assembledText;
    if (prompt.isEmpty) return;
    await sendMessage(prompt);
  }

  // ─── Internal ──────────────────────────────────────────────

  void _bindEvents(Stream<SessionEvent> events) {
    _eventSub?.cancel();
    _eventSub = events.listen((event) {
      final cur = state.value ?? const ChatState();
      switch (event) {
        case SessionStarted(:final assistantMessageId):
          state = AsyncValue.data(cur.copyWith(
            isStreaming: true,
            isCancelling: false,
            activeAssistantMessageId: assistantMessageId,
            clearError: true,
          ));
        case BlockUpdated():
          // block 变化由 messagesProvider 驱动 UI；这里 controller state
          // 不变（流式中保持 isStreaming=true）
          break;
        case SessionCancelling():
          // BiuSessionConnection 已经发出 cancel frame —— 同步给 UI 进入
          // 中间态(stop 按钮 disable + "Stopping..." 文案)。activeMessage
          // 保留,让用户看到当前正在停的那条 message。
          state = AsyncValue.data(cur.copyWith(isCancelling: true));
        case MessageCompleted():
          state = AsyncValue.data(cur.copyWith(
            isStreaming: false,
            isCancelling: false,
            clearActiveMessage: true,
          ));
          // LLM 调用结束 — 让侧边栏 + 会员中心刷新余额. 之前 5 分钟缓存
          // 不主动失效, UI 看不到扣费变化, 用户误以为没扣.
          ref.invalidate(creditsBalanceProvider);
        case MessageCancelled():
          // 用户按 stop -> brain 走完 clean-stop -> 客户端落地。区分于
          // MessageFailed:不写 lastError,不弹错误 toast。
          state = AsyncValue.data(cur.copyWith(
            isStreaming: false,
            isCancelling: false,
            clearActiveMessage: true,
          ));
          // 取消也可能产生部分扣费 (Settle 部分 actual_amount), 同样刷新.
          ref.invalidate(creditsBalanceProvider);
        case MessageFailed(:final error):
          final cls = _classifyStreamError(error);
          state = AsyncValue.data(cur.copyWith(
            isStreaming: false,
            isCancelling: false,
            lastError: cls.message,
            lastErrorAction: cls.action,
            clearActiveMessage: true,
          ));
          // 失败路径 model-relay 端走 release_on_failure (退还 hold),
          // 余额理论上不变但还是刷新一下兜底, 避免 stale 5min 缓存.
          ref.invalidate(creditsBalanceProvider);
        case SessionClosed():
          state = AsyncValue.data(cur.copyWith(
            isStreaming: false,
            isCancelling: false,
            clearActiveMessage: true,
          ));
        case PermissionRequested():
          // 仅 manual / whitelist 模式才会到这里(auto 模式 BiuSessionConnection
          // 自己已应答)。投递到 pendingApprovalsProvider 让 UI 弹 ApprovalCard。
          // ChatController 这层不改 ChatState —— streaming 状态在 daemon 等
          // 答复期间继续保持 isStreaming=true,UI 流不被打断。
          ref.read(pendingApprovalsProvider.notifier).add(arg, event);
      }
    });
  }

  Future<void> _disposeConnection() async {
    await _eventSub?.cancel();
    _eventSub = null;
    final c = _conn;
    _conn = null;
    if (c != null) await c.close();
  }
}

/// chatControllerProvider —— 主入口。每个 threadId 独立实例。
final chatControllerProvider =
    AsyncNotifierProviderFamily<ChatController, ChatState, String>(
  ChatController.new,
);

/// messagesProvider —— UI watch 这个看消息列表 + blocks。跟 ChatController
/// 解耦，避免双源。
final messagesProvider =
    StreamProviderFamily<List<Message>, String>((ref, threadId) {
  final deps = ref.watch(chatControllerDepsProvider);
  return deps.repo.watchMessages(threadId);
});

/// threadProvider —— UI watch 单条 thread 元数据（mode / title / pinned）。
final threadProvider = StreamProviderFamily<Thread?, String>((ref, threadId) {
  final deps = ref.watch(chatControllerDepsProvider);
  return deps.repo.watchThread(threadId);
});

/// threadsProvider —— 全局对话列表（project_id IS NULL）。/chat 主侧边栏用。
final threadsProvider = StreamProvider<List<Thread>>((ref) {
  final deps = ref.watch(chatControllerDepsProvider);
  return deps.repo.watchThreads();
});

/// projectThreadsProvider —— 按 projectId 过滤的 thread 列表。Wiki 项目
/// 内嵌 chat 面板用。传 '' 等价于 null（兼容路由参数）。
final projectThreadsProvider =
    StreamProviderFamily<List<Thread>, String>((ref, projectId) {
  final deps = ref.watch(chatControllerDepsProvider);
  return deps.repo.watchThreads(projectId: projectId.isEmpty ? null : projectId);
});

/// threadStatsProvider —— Hero 副标题用，watch 全部 thread + completed
/// message 数。autoDispose 让 Hero 关掉就释放；threadsProvider 任一更新
/// 时这个会被 invalidated（依赖同一 db）—— 实际上 watch 的是不同 stream，
/// 这里用 FutureProvider 一次性 read 即可（Hero 一次性渲染，不需要实时）。
final threadStatsProvider = FutureProvider.autoDispose<({int threadCount, int messageCount})>((ref) {
  final deps = ref.watch(chatControllerDepsProvider);
  return deps.repo.threadStats();
});

/// 最近 N 天活跃统计 —— Hero 周报 chip 用。family 接 days 参数让 Hero
/// 在 7 / 30 之间切换。autoDispose 让退出 Hero 释放。
final recentStatsProvider =
    FutureProvider.autoDispose.family<({int messages, int activeThreads, int days}), int>(
  (ref, days) {
    final deps = ref.watch(chatControllerDepsProvider);
    return deps.repo.recentStats(days: days);
  },
);

/// 连续活跃天数 streak —— Hero 副标题 chip 用。autoDispose。
final dailyStreakProvider = FutureProvider.autoDispose<int>((ref) {
  final deps = ref.watch(chatControllerDepsProvider);
  return deps.repo.dailyStreak();
});

/// 最近用过的模型（distinct + ORDER BY 最后使用时间）。Hero 模型货架用。
final recentModelsProvider =
    FutureProvider.autoDispose<List<({String code, DateTime lastUsed})>>(
  (ref) {
    final deps = ref.watch(chatControllerDepsProvider);
    return deps.repo.recentModels(limit: 5);
  },
);

/// agentEnvironmentsProvider —— NewThreadDialog / Composer ModeChip 拉
/// 当前用户的 daemon 列表（含 online/offline 状态）。
///
/// 单次 FutureProvider.autoDispose；上层负责在合适时机 invalidate（dialog
/// 打开 / chip 点击 / 错误自愈）。曾试过 Timer.periodic+invalidateSelf
/// 周期刷新，会让 dialog 的 ref.watch 卡在 AsyncLoading 永远转圈 —— 推测
/// 是 invalidate 跟新 watcher 注册的时序 race；改回单次 + 手动刷新更可靠。
final agentEnvironmentsProvider = FutureProvider.autoDispose((ref) async {
  final log = Logger('biumind.agentplane.envs');
  log.info('agentEnvironmentsProvider: build entry');
  final deps = ref.watch(chatControllerDepsProvider);
  log.info('agentEnvironmentsProvider: deps resolved, calling listEnvironments');

  // brain 在开发循环里 (make build-images + docker compose up -d) 经常
  // 被 graceful-recreate; 重启窗口里 in-flight 请求会拿到
  // "Connection closed before full header was received" / EOF。
  // 这种纯 transient 错误用一次指数退避 retry 兜住,业务可见的"加载失败"
  // 留给真不可恢复的情况(JWT 失效 / 服务彻底宕)。
  const maxAttempts = 3;
  Object? lastErr;
  StackTrace? lastStack;
  for (int attempt = 1; attempt <= maxAttempts; attempt++) {
    try {
      final list = await deps.agentPlane.listEnvironments();
      log.info('agentEnvironmentsProvider: got ${list.length} envs (attempt $attempt)');
      return list;
    } catch (e, s) {
      lastErr = e;
      lastStack = s;
      if (attempt == maxAttempts || !_isTransientNetworkError(e)) {
        log.warning('agentEnvironmentsProvider: list failed (attempt $attempt/$maxAttempts, fatal)', e, s);
        rethrow;
      }
      final backoffMs = 300 * (1 << (attempt - 1)); // 300ms, 600ms
      log.info('agentEnvironmentsProvider: transient error attempt $attempt/$maxAttempts, retry in ${backoffMs}ms — $e');
      await Future<void>.delayed(Duration(milliseconds: backoffMs));
    }
  }
  // 不该到这里 (上面 rethrow 兜底);防御性。
  Error.throwWithStackTrace(lastErr ?? StateError('unreachable'),
      lastStack ?? StackTrace.current);
});

/// 判断是不是一次性网络抖动 — 仅这些情况才 retry。其他错(401 / 5xx
/// 业务错)直接 rethrow,让 UI 显示真错。
bool _isTransientNetworkError(Object e) {
  final s = e.toString();
  return s.contains('Connection closed before full header was received') ||
      s.contains('Connection reset') ||
      s.contains('Connection refused') ||
      s.contains('Software caused connection abort') ||
      s.contains('SocketException') ||
      s.contains('TimeoutException') ||
      // ApiError(status: 0) 是我们自己的客户端 timeout 包装。
      // ApiError.toString() = "ApiError 0 /path: body",所以这里既匹
      // 配字面 "ApiError 0" 也匹配 body 里塞的 "request timed out"
      // —— 之前只查 "status: 0" 永远 miss(toString 里没这串),timeout
      // 直接被当 fatal,UI 30s 转圈后红色报错而不是退避重试。
      s.contains('ApiError 0 ') ||
      s.contains('request timed out');
}

/// availableChatModelsProvider —— NewThreadDialog / 设置默认模型 的扁平
/// **chat** 模型列表。
///
/// 数据源是**用户面** providersListProvider + modelsListProvider(brain
/// /v1/providers + /v1/providers/{id}/models),跟聊天 composer 的 picker
/// 同源 —— 普通用户也能拿到(不再走需要 models:read 的 admin-only
/// /v1/admin/models,那条路非 admin 会拿到空列表)。
///
/// 过滤 m.type=='chat' && enabled,排除 embedding / rerank / audio_* /
/// image / video(它们不能作会话 LLM)。包含官方 + 用户 BYOK provider,
/// 故每项带 providerId 供路由消歧(同 model code 可能在多个 provider 下)。
/// 创作面板走独立 aigcModelsProvider,不受影响。
class AvailableChatModel {
  final String code; // model_id
  final String displayName;
  final String providerId; // provider slug,thread.providerId 路由用
  final String providerDisplayName;
  final bool isOfficial;
  // P5: client-side BYOK (本机直连). 驱动 routeKey source 去重.
  final bool isClientSide;
  const AvailableChatModel({
    required this.code,
    required this.displayName,
    required this.providerId,
    required this.providerDisplayName,
    required this.isOfficial,
    this.isClientSide = false,
  });

  /// dropdown 唯一值 —— 同 code 可能在多个 provider 下,单用 code 会让
  /// DropdownButton 报"exactly one item with value"。用 source+providerId+code 复合 (o=official/s=server/c=client 防双配撞值)。
  String get routeKey =>
      '${isOfficial ? 'o' : (isClientSide ? 'c' : 's')}|$providerId|$code';

  /// 下拉/列表展示名。官方 provider 只显模型名;BYOK provider 追加
  /// provider 名消歧(否则两个同名 code 用户分不清)。
  String get label =>
      isOfficial ? displayName : '$displayName · $providerDisplayName';
}

final availableChatModelsProvider =
    FutureProvider<List<AvailableChatModel>>((ref) async {
  // P3: chat 模型来自 chatModelGroupsProvider (official brain + identity
  // BYOK, 按用户持 key 过滤), 不再直接拍平 brain providersListProvider。
  final groups = await ref.watch(chatModelGroupsProvider.future);
  return [
    for (final g in groups)
      for (final m in g.models)
        AvailableChatModel(
          code: m.code,
          displayName: m.displayName,
          providerId: g.providerId,
          providerDisplayName: g.displayName,
          isOfficial: g.isOfficial,
          isClientSide: g.isClientSide,
        ),
  ];
});

/// availableTtsModelsProvider —— 消息「朗读」设置用的扁平 **TTS** 模型列表。
/// P6: official TTS 直读 model-relay (mode=='audio_speech') + BYOK 从 brain
/// per-user (custom tts 上游)。空列表 = 无 TTS 模型, UI 据此提示去渠道加。
final availableTtsModelsProvider =
    FutureProvider<List<AvailableChatModel>>((ref) async {
  final out = <AvailableChatModel>[];
  // official TTS — model-relay global catalog.
  final relay = await ref.watch(relayCatalogListProvider.future);
  for (final m in relay.where((m) => m.mode == 'audio_speech')) {
    out.add(AvailableChatModel(
      code: m.code,
      displayName: m.displayName.isEmpty ? m.code : m.displayName,
      providerId: 'biumind-official',
      providerDisplayName: 'BiuMind Cloud',
      isOfficial: true,
    ));
  }
  // BYOK TTS — brain per-user (custom tts 上游 refresh).
  out.addAll(await _flattenModelsByType(ref, 'tts'));
  return out;
});

/// 把所有 enabled provider 下指定 type 的 enabled 模型拍平成用户面列表。
Future<List<AvailableChatModel>> _flattenModelsByType(
    Ref ref, String type) async {
  final providers = await ref.watch(providersListProvider.future);
  final out = <AvailableChatModel>[];
  for (final p in providers.where((p) => p.enabled)) {
    final models = await ref.watch(modelsListProvider(p.id).future);
    for (final m in models.where((m) => m.enabled && m.type == type)) {
      out.add(AvailableChatModel(
        code: m.modelId,
        displayName: m.displayName.isEmpty ? m.modelId : m.displayName,
        providerId: p.providerId,
        providerDisplayName:
            p.displayName.isEmpty ? p.providerId : p.displayName,
        isOfficial: p.isOfficial,
      ));
    }
  }
  return out;
}

// ── permission approval queue ──────────────────────────────

/// PendingApprovalsState —— 每条 thread 当前**未应答**的 PermissionRequest 队列。
/// 同一时刻一条 thread 通常只有 1 条 pending(daemon engine 串行触发工具),
/// 但 List 防御 race。
class PendingApprovalsState {
  final Map<String, List<PermissionRequested>> byThread;
  const PendingApprovalsState({this.byThread = const {}});

  List<PermissionRequested> forThread(String threadId) =>
      byThread[threadId] ?? const [];
}

/// PendingApprovalsController —— BiuSessionConnection 通过 ChatController
/// 投递 PermissionRequested 到这里;ApprovalCard widget 监听本 provider
/// 渲染 inline confirm。用户决策后调 [resolve] 把请求从队列摘掉(应答的
/// 实际 send 由 PermissionRequested.respond 闭包内部处理)。
class PendingApprovalsController extends Notifier<PendingApprovalsState> {
  @override
  PendingApprovalsState build() => const PendingApprovalsState();

  void add(String threadId, PermissionRequested req) {
    final next = Map<String, List<PermissionRequested>>.from(state.byThread);
    next[threadId] = [...(next[threadId] ?? const []), req];
    state = PendingApprovalsState(byThread: next);
  }

  /// 调用方应答完(req.respond 已经走过)后调,把这条从队列摘掉让 UI 不再显。
  void resolve(String threadId, String requestId) {
    final list = state.byThread[threadId];
    if (list == null) return;
    final filtered =
        list.where((r) => r.requestId != requestId).toList(growable: false);
    final next = Map<String, List<PermissionRequested>>.from(state.byThread);
    if (filtered.isEmpty) {
      next.remove(threadId);
    } else {
      next[threadId] = filtered;
    }
    state = PendingApprovalsState(byThread: next);
  }

  /// thread 关闭时清空它的所有 pending。
  void clearThread(String threadId) {
    if (!state.byThread.containsKey(threadId)) return;
    final next = Map<String, List<PermissionRequested>>.from(state.byThread);
    next.remove(threadId);
    state = PendingApprovalsState(byThread: next);
  }
}

final pendingApprovalsProvider =
    NotifierProvider<PendingApprovalsController, PendingApprovalsState>(
  PendingApprovalsController.new,
);

// ── helpers ─────────────────────────────────────────────────

extension on bool {
  /// 让 if-condition 阅读更顺：state.isStreaming.isFalse 表示"不在流式中"。
  bool get isFalse => !this;
}
