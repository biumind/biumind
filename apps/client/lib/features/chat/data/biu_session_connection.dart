// BiuSessionConnection —— Chat 重构 R2 核心中间层。
//
// 把三个独立组件粘起来：
//   - AgentPlaneClient (HTTP)：创建 / 续 token / list environments
//   - BiuClient (WS)        ：Frame stream + send + outbox
//   - ChatRepo (drift)      ：thread / message / blocks / sessions 持久化
//
// 单一职责：**一条 thread 当前活跃 brain session 的完整生命周期**。
//
//   open    → POST /v1/agent/sessions → BiuClient.connect → drain frames
//   resume  → 找 active session → refresh token if expiring → connect
//             with sinceSeq=lastSeenSeq + 1
//   sendUserMessage → 多 turn：往现有 session 推 user frame
//   cancel  → 给 brain 发 SDKControlCancelRequest
//   close   → drain + ChatRepo.finalizeSession
//
// 帧 → blocks 翻译策略（SDK Protocol → ChatRepo upsertBlock）：
//
//   SDKStreamlinedText            → TextBlock（streaming，文本累计 append）
//   SDKAssistantMessage.content   → 整批 ToolUse / Text blocks（closed）
//   SDKToolUseSummary             → ToolResultBlock（完成态）
//   SDKResultSuccess              → finalizeMessage(completed) + finalizeSession
//   SDKResultError                → finalizeMessage(failed) + finalizeSession
//
// **缺省 v1 策略**：streamlined_text 只更新当前 streaming TextBlock；遇到
// AssistantBlock 类型变化（例如 streamlined 切到 tool_use）开新 block。
// 字段级 schema 演化随后续 brain 帧扩展再扩。

import 'dart:async';
import 'dart:convert' show base64Encode;

import 'package:flutter/foundation.dart' show debugPrint;

import '../../../data/agent_plane/agent_plane_client.dart';
import '../../../data/api/biu_client.dart';
import '../../../data/api/sdkproto/v1/control/wrappers.dart';
import '../../../data/api/sdkproto/v1/data/assistant.dart';
import '../../../data/api/sdkproto/v1/data/result.dart';
import '../../../data/api/sdkproto/v1/data/streamlined.dart';
import '../../../data/api/sdkproto/v1/data/tool.dart';
import '../domain/chat_models.dart';
import 'chat_repo.dart';

/// SessionPendingException —— open() 在 brain 返回 state=pending（agent 模式
/// 投给当前离线的设备，Runtime v3 R7）时抛出。不是错误：work 已成功排队，等
/// 设备上线自动派发。ChatController 捕获后停 spinner + 友好提示，不连 WS。
class SessionPendingException implements Exception {
  final String sessionId;
  const SessionPendingException(this.sessionId);
  @override
  String toString() => 'SessionPendingException($sessionId)';
}

/// 高层事件 —— ChatController 监听这个流来更新 UI 状态。
sealed class SessionEvent {
  const SessionEvent();
}

class SessionStarted extends SessionEvent {
  final String sessionId;
  final String assistantMessageId;
  const SessionStarted({required this.sessionId, required this.assistantMessageId});
}

class BlockUpdated extends SessionEvent {
  final String messageId;
  final Block block;
  const BlockUpdated(this.messageId, this.block);
}

class MessageCompleted extends SessionEvent {
  final String messageId;
  final String stopReason;
  final int? inputTokens;
  final int? outputTokens;
  const MessageCompleted({
    required this.messageId,
    required this.stopReason,
    this.inputTokens,
    this.outputTokens,
  });
}

class MessageFailed extends SessionEvent {
  final String messageId;
  final String error;
  const MessageFailed(this.messageId, this.error);
}

/// 用户按 stop 后,brain 端 engine 走完 clean-stop 路径(发 Done{interrupted}
/// + 合成未完成 tool_use 的 tool_result),客户端确认收到。区别于 [MessageFailed]
/// (LLM/网络错): cancelled 是用户主动行为,UI 应该淡灰渲染 "已停止" 而不是
/// 红色错误。
///
/// [latency] 是从用户按 stop 到这一刻的客户端视角端到端时间,涵盖
/// 网络往返 + brain 路由 + engine clean-stop + 帧返回。timeout 兜底
/// 路径下也会带值(就是 _cancelGraceWindow 本身)。null 表示 cancel 不
/// 是用户主动触发的(理论上不会发生,防御 fallback)。
class MessageCancelled extends SessionEvent {
  final String messageId;
  final Duration? latency;
  const MessageCancelled(this.messageId, {this.latency});
}

/// 客户端发出 cancel frame 后到收到最终 Done 之间的中间态 —— ChatController
/// 据此让 UI 显示 "Stopping..." 而不是仍在 streaming。
class SessionCancelling extends SessionEvent {
  const SessionCancelling();
}

class SessionClosed extends SessionEvent {
  final SessionStatus finalStatus;
  const SessionClosed(this.finalStatus);
}

/// daemon 通过 brain 反向问"这个工具能不能用"。ChatController 据此弹
/// ApprovalCard;auto 模式 BiuSessionConnection 自己已经 respond,这个
/// 事件不会发出。
///
/// [respond] 由 ChatController/UI 调用一次回应:behavior='allow' 让
/// daemon 继续执行;'deny' 拒绝。重复调用会被 second 静默丢弃。
class PermissionRequested extends SessionEvent {
  final String requestId;
  final String toolName;
  final String toolUseId;
  final Map<String, dynamic> input;
  final String? reason;
  /// 应答闭包 —— 内部已经包了 sendControlResponse + 单次保护。
  /// allow=true → behavior 'allow'。
  final void Function({required bool allow}) respond;

  const PermissionRequested({
    required this.requestId,
    required this.toolName,
    required this.toolUseId,
    required this.input,
    this.reason,
    required this.respond,
  });
}

/// BiuSessionConnection 把一条 thread 的执行流封装成对象。同 thread 同一
/// 时刻最多一个 active connection。caller（ChatController）持有引用，
/// dispose 时调 close。
class BiuSessionConnection {
  final ChatRepo repo;
  final AgentPlaneClient agentPlane;
  final String brainBaseUrl;

  // ── 内部状态 ───────────────────────────────────────────────
  final Thread thread;
  Session _session;
  final BiuClient _ws;
  final _events = StreamController<SessionEvent>.broadcast();

  /// 当前正在 stream 的 assistant message id（用于把 frame route 到对的行）。
  String? _activeAssistantMessageId;

  /// 当前 streaming 的 text block —— streamlined_text delta 累加进同一块。
  /// 收到 result 帧 / 别的 block 类型时关闭这块（state=closed）。
  String? _activeTextBlockId;
  final StringBuffer _textBuffer = StringBuffer();
  int _nextBlockIndex = 0;

  /// session frame seq 计数（bus 不暴露真 seq，简化用本地计数 ack 上报）。
  int _localSeq = 0;

  /// 帧串行化链 —— 见 [_scheduleFrame]。Stream.listen 对 async 回调不提供
  /// 顺序保证,这条单调 Future 链把每帧 _onFrame 串成严格 FIFO。
  Future<void> _frameChain = Future<void>.value();

  /// token refresh timer
  Timer? _tokenRefreshTimer;

  /// dispose 标记 —— 防 close 之后还有 frame 进来污染状态
  bool _closed = false;

  /// cancel() 已发出但还在等 brain 的 Done{interrupted} —— 中间态。
  /// 在这个窗口里 _onResultSuccess 收到 stop_reason="interrupted" 才
  /// 算 clean-stop。重复 cancel() 此刻是 no-op。
  bool _cancelling = false;

  /// cancel() 第一次按下的时间戳。Done{interrupted} 落地或 timeout
  /// 兜底时算 delta 让 ChatController / dev tools 看到客户端视角的
  /// 端到端 cancel 延迟。重复按 stop 不重置(跟 brain ChatRunner /
  /// daemon Worker 同语义)。
  DateTime? _cancelRequestedAt;

  /// cancel 兜底 timer —— brain Done{interrupted} 在 timeout 内没到就强
  /// 制按 cancelled 关 connection。覆盖网络抖 / 跨 broker 转发延迟 / 旧
  /// brain 不发 Done{interrupted} 的部署。
  Timer? _cancelTimeoutTimer;

  /// cancel() 等 Done{interrupted} 的最长时间。比典型的 ToolUse 取消窗口
  /// 略长(批量并行工具 cancel 完整走完到合成 tool_result + Done 大概 1~2s),
  /// 留够安全 margin。超过这个时间不指望服务端再回 — 客户端按 cancelled
  /// 关闭。
  static const Duration _cancelGraceWindow = Duration(seconds: 3);

  StreamSubscription? _frameSub;

  BiuSessionConnection._({
    required this.repo,
    required this.agentPlane,
    required this.brainBaseUrl,
    required this.thread,
    required Session session,
    required BiuClient ws,
  })  : _session = session,
        _ws = ws;

  /// 监听帧事件流（broadcast）。多 listener OK。
  Stream<SessionEvent> get events => _events.stream;

  /// 当前 brain session 的 ID。
  String get sessionId => _session.sessionId;

  /// thread 当前 mode（chat / agent / task）。
  ThreadMode get mode => thread.mode;

  // ─── Open / Resume ────────────────────────────────────────

  /// 创建新 session：POST /v1/agent/sessions → BiuClient.connect。
  /// 同时在 ChatRepo 持久化 user message + (placeholder) assistant message。
  static Future<BiuSessionConnection> open({
    required ChatRepo repo,
    required AgentPlaneClient agentPlane,
    required String brainBaseUrl,
    required Thread thread,
    required String userPrompt,
    required String userMessageId,
    required String assistantMessageId,
    String? model,
    /// 图片附件（image/* mime）；本地写成 ImageBlock 挂到 user message 之后
    /// 让 UI 立刻渲染，同时 base64 编码后透传给 brain createSession 喂 vision
    /// 模型。非视觉模型场景上层 composer 应该禁用 attach 按钮 —— 真送了图
    /// 给非 vision 模型上游会 400，brain 不再过滤。
    List<AttachmentInput> attachments = const [],
    /// 测试注入：自定义 BiuTransport connector。生产留 null 走默认
    /// WebSocketChannel.connect。
    BiuTransport Function(Uri)? transportConnector,
    /// client-side BYOK 信号（B2）：命中时透传 brain → daemon。daemon 据此从
    /// loopback 内存 store 取 key 建 engine 直连上游（不经 relay）。
    String? clientSideRecordId,
    String? clientSideBaseUrl,
    String? clientSideProtocol,
  }) async {
    // 1. 用户消息先写本地（让 UI 立刻渲染，不等 brain 创会话）
    await repo.appendMessage(
      id: userMessageId,
      threadId: thread.id,
      role: MessageRole.user,
      status: MessageStatus.pending,
    );
    await repo.upsertBlock(
      TextBlock(
        id: '${userMessageId}_b0',
        index: 0,
        state: BlockState.closed,
        text: userPrompt,
      ),
      messageId: userMessageId,
    );
    // 1b. 图片附件作为 ImageBlock 写到 user message（index ≥ 1）。
    for (var i = 0; i < attachments.length; i++) {
      final att = attachments[i];
      await repo.upsertBlock(
        ImageBlock(
          id: '${userMessageId}_b${i + 1}',
          index: i + 1,
          state: BlockState.closed,
          mimeType: att.mimeType,
          data: base64Encode(att.bytes),
        ),
        messageId: userMessageId,
      );
    }

    // 2. POST /v1/agent/sessions —— "biumind-default" 是 client 模型选择器
    //    里"BiuMind 官方"的占位 id，不是 brain 那边能识别的真模型名。空给
    //    brain，让 ChatRunner fallback 到 claude-sonnet-4-6（chat_runner.go
    //    第 121 行）。
    final realModel = _stripPlaceholderModel(model ?? thread.model);
    // 图片附件透传给 brain — 仅 chat 模式 vision 模型生效。base64 编码 +
    // mime_type 跟 brain ChatImageInput / SingleTurnInput 字段对齐。
    final imagePayloads = attachments
        .where((a) => a.mimeType.startsWith('image/'))
        .map((a) => ChatImageInput(
              mimeType: a.mimeType,
              data: base64Encode(a.bytes),
            ))
        .toList(growable: false);
    // Runtime v3 §8.2 翻案:历史不再由客户端带入。brain 现在把对话轮落库到
    // chat.messages 并**服务端组装**多轮上下文(chat + agent 两模式统一),
    // brain 是对话真相源。客户端本地 Drift 仍持久化消息供渲染,但不再回传
    // history(避免 N 端各切一套 + 跨设备断裂;截断/预算集中在 brain)。
    final resp = await agentPlane.createSession(
      mode: thread.mode.name,
      environmentId: thread.environmentId,
      threadId: thread.id,
      model: realModel,
      providerId: thread.providerId,
      systemPrompt: thread.systemPrompt,
      prompt: userPrompt,
      poolTag: thread.poolTag,
      workdir: thread.workdir,
      runtimeEnvMode: thread.runtimeEnvMode,
      backend: thread.backend,
      images: imagePayloads.isEmpty ? null : imagePayloads,
      // 方案3：透传 client 预生成的 message id，brain 落库用之作 PK
      // → 本地 message.id == brain chat.messages.id，编辑/删除上行直连。
      userMessageId: userMessageId,
      assistantMessageId: assistantMessageId,
      clientSideRecordId: clientSideRecordId,
      clientSideBaseUrl: clientSideBaseUrl,
      clientSideProtocol: clientSideProtocol,
    );

    // Runtime v3 R7：目标 agent 设备当前离线，brain 已把 work 排进
    // agent_pending_work、返回 state=pending，等设备上线再派发到一个**新**
    // environment。此时没有 live WS 可连（强连只会无限转圈 + 连到永远不来帧
    // 的 subject）。收尾 user message、把 session mirror 成 pending（resume()
    // 只取 status=active，天然跳过它，不会误连死 WS），抛 SessionPendingException
    // 让 controller 停 spinner 并友好提示「已排队」。
    if (resp.state == 'pending') {
      await repo.finalizeMessage(userMessageId,
          status: MessageStatus.completed);
      await repo.persistSession(Session(
        sessionId: resp.sessionId,
        threadId: thread.id,
        mode: thread.mode,
        sessionToken: resp.sessionToken,
        tokenExpiresAt: resp.expiresAt ??
            DateTime.now().add(const Duration(minutes: 30)),
        status: SessionStatus.pending,
        createdAt: DateTime.now(),
      ));
      throw SessionPendingException(resp.sessionId);
    }

    // 3. 持久化 session 行（brain 已经创了 row；客户端 mirror）
    final session = Session(
      sessionId: resp.sessionId,
      threadId: thread.id,
      mode: thread.mode,
      sessionToken: resp.sessionToken,
      tokenExpiresAt:
          resp.expiresAt ?? DateTime.now().add(const Duration(minutes: 30)),
      status: SessionStatus.active,
      createdAt: DateTime.now(),
    );
    await repo.persistSession(session);

    // 4. 把 user message 状态升到 streaming（已经送达 brain），并占位
    //    assistant message —— SDK frame 来了往这条上挂 blocks。
    await repo.finalizeMessage(
      userMessageId,
      status: MessageStatus.completed,
    );
    await repo.appendMessage(
      id: assistantMessageId,
      threadId: thread.id,
      role: MessageRole.assistant,
      status: MessageStatus.streaming,
      sessionId: resp.sessionId,
      model: model ?? thread.model,
    );

    // 5. WS connect
    final ws = BiuClient(
      brainBaseUrl: brainBaseUrl,
      connector: transportConnector,
      onTokenExpired: () async {
        // 默认行为：BiuClient 内部检测到 401 / policy 时触发；
        // 这里暂留 null（_TokenRefresher 主动定时 refresh）。
        return null;
      },
    );
    debugPrint('[biu_session] ws connect session=${resp.sessionId}'
        ' mode=${session.mode}');
    await ws.connect(
      sessionId: resp.sessionId,
      sessionToken: resp.sessionToken,
    );
    debugPrint('[biu_session] ws connected session=${resp.sessionId}');

    final c = BiuSessionConnection._(
      repo: repo,
      agentPlane: agentPlane,
      brainBaseUrl: brainBaseUrl,
      thread: thread,
      session: session,
      ws: ws,
    );
    c._activeAssistantMessageId = assistantMessageId;
    c._listenFrames();
    c._scheduleTokenRefresh();
    c._events.add(SessionStarted(
      sessionId: resp.sessionId,
      assistantMessageId: assistantMessageId,
    ));
    return c;
  }

  /// 恢复已有 session（跨设备 / 重启 / 网络恢复）。返回 null 表示
  /// thread 没有 active session，调用方应该 open() 创新会话或啥都不做。
  ///
  /// 内部行为：
  ///   - 检查 token 是否快过期 → AgentPlaneClient.refreshSessionToken
  ///   - BiuClient.connect 带 sinceSeq=lastSeenSeq（>0 时启 replay）
  static Future<BiuSessionConnection?> resume({
    required ChatRepo repo,
    required AgentPlaneClient agentPlane,
    required String brainBaseUrl,
    required Thread thread,
    BiuTransport Function(Uri)? transportConnector,
  }) async {
    final initial = await repo.activeSession(thread.id);
    if (initial == null) return null;
    var session = initial;

    // token 快过期 → 续
    if (session.tokenExpiringSoon) {
      try {
        final r = await agentPlane.refreshSessionToken(session.sessionId);
        await repo.updateSessionToken(
          session.sessionId,
          token: r.sessionToken,
          expiresAt:
              r.expiresAt ?? DateTime.now().add(const Duration(minutes: 30)),
        );
        session = Session(
          sessionId: session.sessionId,
          threadId: session.threadId,
          mode: session.mode,
          sessionToken: r.sessionToken,
          tokenExpiresAt:
              r.expiresAt ?? DateTime.now().add(const Duration(minutes: 30)),
          lastSeenSeq: session.lastSeenSeq,
          status: session.status,
          createdAt: session.createdAt,
          closedAt: session.closedAt,
        );
      } catch (_) {
        // refresh 失败 —— 继续用旧 token 试，brain 会 401 时 BiuClient 关
      }
    }

    final ws = BiuClient(
      brainBaseUrl: brainBaseUrl,
      connector: transportConnector,
    );
    debugPrint('[biu_session] ws resume session=${session.sessionId}'
        ' since_seq=${session.lastSeenSeq}');
    await ws.connect(
      sessionId: session.sessionId,
      sessionToken: session.sessionToken,
      sinceSeq: session.lastSeenSeq,
    );
    debugPrint('[biu_session] ws resumed session=${session.sessionId}');

    final c = BiuSessionConnection._(
      repo: repo,
      agentPlane: agentPlane,
      brainBaseUrl: brainBaseUrl,
      thread: thread,
      session: session,
      ws: ws,
    );
    // resume 时 active assistant message 是历史里最后一条 streaming 的
    final msgs = await c.repo.watchMessages(thread.id).first;
    final lastStreaming = msgs.lastWhere(
      (m) => m.role == MessageRole.assistant && m.status == MessageStatus.streaming,
      orElse: () => Message(
        id: '',
        threadId: thread.id,
        role: MessageRole.assistant,
        status: MessageStatus.completed,
        seq: 0,
        createdAt: DateTime.now(),
      ),
    );
    if (lastStreaming.id.isNotEmpty) {
      c._activeAssistantMessageId = lastStreaming.id;
    }
    c._listenFrames();
    c._scheduleTokenRefresh();
    return c;
  }

  // ─── Send / cancel ────────────────────────────────────────

  /// 给 brain 发用户消息（多 turn 场景 —— v1 chat 模式 brain 端不持续会话，
  /// 多 turn 客户端要新建 session；这里目前只发但不期望 brain 真处理）。
  /// 占位实现：未来 brain 加 multi-turn session 时启用。
  Future<void> sendUserMessage(String text, {required String userMessageId}) async {
    if (_closed) {
      throw StateError('BiuSessionConnection.sendUserMessage: closed');
    }
    await repo.appendMessage(
      id: userMessageId,
      threadId: thread.id,
      role: MessageRole.user,
      status: MessageStatus.pending,
      sessionId: _session.sessionId,
    );
    await repo.upsertBlock(
      TextBlock(
        id: '${userMessageId}_b0',
        index: 0,
        state: BlockState.closed,
        text: text,
      ),
      messageId: userMessageId,
    );
    _ws.sendUserText(text, userMessageUuid: userMessageId);
    await repo.finalizeMessage(
      userMessageId,
      status: MessageStatus.completed,
    );
  }

  /// 给 brain 发取消请求,等服务端走完 clean-stop 路径(F5 之后 brain
  /// emit Done{interrupted} + 合成 tool_result),然后 finalize。
  ///
  /// 跟旧实现的差异:
  ///   - 不立刻关 WS。brain 走 engine 的 isInterrupt 路径需要时间投递最终
  ///     Done 帧,过早 close 导致这帧到达时连接已经没了,UI 状态不一致。
  ///   - 引入 cancelling 中间态(SessionCancelling 事件),ChatController 据
  ///     此让 Composer 显示 "Stopping...",防止用户重复 spam stop 按钮。
  ///   - 兜底 timer:_cancelGraceWindow 内 Done{interrupted} 没到就强制
  ///     按 cancelled 关。覆盖网络断 / 旧 brain 部署没有 F5 等情况。
  ///
  /// 重复调用是 no-op(_cancelling 守护)。
  Future<void> cancel() async {
    if (_closed || _cancelling) return;
    _cancelling = true;
    _cancelRequestedAt = DateTime.now();
    _events.add(const SessionCancelling());
    try {
      _ws.send(SDKControlCancelRequest(
        requestId: 'cancel-${DateTime.now().millisecondsSinceEpoch}',
      ));
    } catch (_) {
      // WS 已经断了 —— 直接走 timeout 兜底,不重连重发。
    }
    _cancelTimeoutTimer?.cancel();
    _cancelTimeoutTimer = Timer(_cancelGraceWindow, _forceCancelTimeout);
  }

  /// cancel grace window 内 Done{interrupted} 没到的兜底。直接按 cancelled
  /// finalize 当前 message + 关连接。
  Future<void> _forceCancelTimeout() async {
    _cancelTimeoutTimer = null;
    if (_closed) return;
    final latency = _consumeCancelLatency();
    final msgId = _activeAssistantMessageId;
    if (msgId != null) {
      await repo.finalizeMessage(
        msgId,
        status: MessageStatus.cancelled,
      );
      _logCancelLatency(latency, fallback: true);
      _events.add(MessageCancelled(msgId, latency: latency));
    }
    await _internalClose(SessionStatus.cancelled);
  }

  /// _consumeCancelLatency 算 _cancelRequestedAt 到现在的差,并清掉时间
  /// 戳防 double-emit。返 null 表示这次结束不是用户 cancel 触发的(自然
  /// 结束 / 网络错等);UI 不该把那当 cancel latency 来展示。
  Duration? _consumeCancelLatency() {
    final at = _cancelRequestedAt;
    _cancelRequestedAt = null;
    if (at == null) return null;
    return DateTime.now().difference(at);
  }

  /// _logCancelLatency 让 dev console 立刻看到客户端视角的 cancel 时长 ——
  /// 跟 server-side 的 agent_cancel_latency_seconds 对账,差值就是网络
  /// + UI 重绘段。fallback=true 标记 timeout 兜底路径。
  /// debugPrint 在 release build 自动 noop,不污染生产 log。
  void _logCancelLatency(Duration? d, {bool fallback = false}) {
    if (d == null) return;
    final tag = fallback ? '[fallback]' : '[clean]';
    debugPrint('[biu/cancel] $tag client-side latency: ${d.inMilliseconds}ms');
  }

  /// 主动关 connection（thread 切走 / app 退出 / 等）。会 finalize active
  /// message 状态成 failed（除非已经 completed）。
  Future<void> close() async {
    if (_closed) return;
    final status = _activeAssistantMessageId == null
        ? SessionStatus.completed
        : SessionStatus.failed;
    await _internalClose(status);
  }

  Future<void> _internalClose(SessionStatus status) async {
    if (_closed) return;
    _closed = true;
    _tokenRefreshTimer?.cancel();
    _cancelTimeoutTimer?.cancel();
    _cancelTimeoutTimer = null;
    await _frameSub?.cancel();
    await _ws.close();
    await repo.finalizeSession(_session.sessionId, status: status);
    _events.add(SessionClosed(status));
    if (!_events.isClosed) await _events.close();
  }

  // ─── Frame pump ───────────────────────────────────────────

  void _listenFrames() {
    _frameSub = _ws.frames.listen(_scheduleFrame, onError: (Object e, _) async {
      if (_activeAssistantMessageId != null) {
        await repo.finalizeMessage(
          _activeAssistantMessageId!,
          status: MessageStatus.failed,
          errorMessage: e.toString(),
        );
        _events.add(MessageFailed(_activeAssistantMessageId!, e.toString()));
      }
    }, onDone: () async {
      // WS 自然 close —— 通常意味着 brain 推完 result 帧后服务端关。
      // 先等帧链排空:result_success 可能还排在队列里没处理完,不等就会
      // 误判 streaming 未结束而 finalize=failed。
      await _frameChain;
      // 如果 active message 还是 streaming，说明意外断；finalize=failed。
      if (_activeAssistantMessageId != null && !_closed) {
        final m = await repo.getMessage(_activeAssistantMessageId!);
        if (m?.status == MessageStatus.streaming) {
          await repo.finalizeMessage(
            _activeAssistantMessageId!,
            status: MessageStatus.failed,
            errorMessage: 'connection closed before result',
          );
        }
      }
    });
  }

  /// 把每帧处理排进 [_frameChain] 串行执行。
  ///
  /// 为什么必须串行:`Stream.listen` 不会 await async 回调,每个 [_onFrame]
  /// 跑到首个 await(updateLastSeenSeq / upsertBlock)就挂起,下一帧回调随即
  /// 插进来改共享状态(_textBuffer / _activeTextBlockId / _nextBlockIndex)。
  /// 后果:① streamlined_text delta 写进 buffer 的顺序被打乱 → 串字;
  /// ② 每帧 upsertBlock/BlockUpdated 携带的是写时 buffer 快照,旧(更短)
  /// 快照若后完成就覆盖新快照 → UI 截断丢字。两者就是线上"回复不连贯、
  /// 像 token 丢失"的根因(brain 侧 transcript 落库完整可证:乱序只在客户端)。
  ///
  /// 单调 Future 链保证第 N 帧的 _onFrame 完整 await 完再处理第 N+1 帧。
  /// catchError 兜住单帧异常,避免链断掉后续所有帧静默停摆。
  void _scheduleFrame(Object frame) {
    _frameChain = _frameChain.then((_) => _onFrame(frame)).catchError(
      (Object e, StackTrace st) {
        debugPrint('[biu_session] frame processing error: $e');
      },
    );
  }

  Future<void> _onFrame(Object frame) async {
    if (_closed) return;
    _localSeq++;
    // 持久化 lastSeenSeq —— 不等到 close，每 N 帧 flush 减少阻塞
    if (_localSeq % 10 == 0) {
      await repo.updateLastSeenSeq(_session.sessionId, _localSeq);
    }
    // 流式 text/progress 帧高频,不打印逐帧 — 之前每帧一行,大对话 500+
    // 行 streamlined_text 把 terminal 刷掉了。现在只打非流帧 + 流帧每
    // 50 帧一次(进度提示),其他正常 switch dispatch 不再 log。
    final isFrequent = frame is SDKStreamlinedText ||
        frame is SDKPartialAssistantMessage ||
        frame is SDKToolProgress;
    if (!isFrequent || _localSeq % 50 == 0) {
      debugPrint('[biu_session] frame seq=$_localSeq type=${frame.runtimeType}');
    }

    switch (frame) {
      case SDKStreamlinedText():
        await _onStreamlinedText(frame);
      case SDKAssistantMessage():
        await _onAssistantMessage(frame);
      case SDKPartialAssistantMessage():
        // partial 当前不渲染（streamlined_text 已经覆盖增量场景）
        break;
      case SDKToolUseSummary():
        await _onToolResult(frame);
      case SDKToolProgress():
        // tool_progress 标 in-flight；当前 ToolUseBlock 已经在 streaming
        // 状态由 AssistantMessage 创建，progress 帧只是维护"还在跑"
        break;
      case SDKResultSuccess():
        await _onResultSuccess(frame);
      case SDKResultError():
        await _onResultError(frame);
      case SDKControlRequest():
        _onControlRequest(frame);
    }
  }

  /// 处理 daemon 通过 brain 反向发来的 control request。
  /// 当前唯一处理的子类型是 can_use_tool;其它(initialize/set_model 等)
  /// daemon → client 链路上不该出现,记 debug 日志即可。
  void _onControlRequest(SDKControlRequest req) {
    if (req.subtype != 'can_use_tool') {
      debugPrint('[biu_session] ignoring control_request subtype=${req.subtype}');
      return;
    }
    final toolName = (req.request['tool_name'] as String?) ?? '';
    final toolUseId = (req.request['tool_use_id'] as String?) ?? '';
    final reason = req.request['decision_reason'] as String?;
    final input = (req.request['input'] is Map<String, dynamic>)
        ? req.request['input'] as Map<String, dynamic>
        : <String, dynamic>{};

    // 按 thread.autoApprove 分流:
    //   * auto       → 立即应答 allow,不打扰用户
    //   * whitelist  → 暂时未实现规则匹配,fall through 到 manual
    //   * manual     → 通过 _events 抛 PermissionRequested 事件,UI/Controller
    //                  弹 ApprovalCard;用户决策后调 respond()。
    if (thread.autoApprove == AutoApproveMode.auto) {
      _sendPermissionResult(req.requestId, allow: true);
      return;
    }

    // 单次保护:防止 UI 回调 / timeout 两条路径都触发 send。
    var sent = false;
    void respond({required bool allow}) {
      if (sent || _closed) return;
      sent = true;
      _sendPermissionResult(req.requestId, allow: allow);
    }

    _events.add(PermissionRequested(
      requestId: req.requestId,
      toolName: toolName,
      toolUseId: toolUseId,
      input: input,
      reason: reason,
      respond: respond,
    ));
  }

  /// 发 SDKControlResponse{success/error} 回 brain;brain 走 maybeRoutePermissionResponse
  /// 把它路由到 daemon control queue,daemon worker.answerPermission 唤醒
  /// askPermission goroutine。
  void _sendPermissionResult(String requestId, {required bool allow}) {
    if (_closed) return;
    final body = ControlResponseBody(
      subtype: 'success',
      requestId: requestId,
      response: <String, dynamic>{
        'behavior': allow ? 'allow' : 'deny',
      },
    );
    final resp = SDKControlResponse(response: body);
    try {
      _ws.send(resp);
    } catch (e) {
      // send 失败说明 WS 断了 —— daemon 端会自动 timeout(30s)然后 deny,
      // 不需要在这里重试。仅 log。
      debugPrint('[biu_session] sendPermissionResult failed: $e');
    }
  }

  Future<void> _onStreamlinedText(SDKStreamlinedText f) async {
    final msgId = _activeAssistantMessageId;
    if (msgId == null) return;
    if (_activeTextBlockId == null) {
      _activeTextBlockId = '${msgId}_t$_nextBlockIndex';
      _textBuffer.clear();
    }
    _textBuffer.write(f.text);
    final block = TextBlock(
      id: _activeTextBlockId!,
      index: _nextBlockIndex,
      state: BlockState.streaming,
      text: _textBuffer.toString(),
    );
    await repo.upsertBlock(block, messageId: msgId);
    _events.add(BlockUpdated(msgId, block));
  }

  Future<void> _onAssistantMessage(SDKAssistantMessage f) async {
    // assembled 后的完整 turn。两条路径都可能给我们文本：
    //   - streamlined_text 流式增量 → 已经累计写入 _activeTextBlockId
    //   - assistant_message.content[*].type=text → 同份文本的"权威副本"
    //
    // 两条都写就重复渲染。策略：如果当前 turn 已经有 streamlined text
    // block（_activeTextBlockId != null），SKIP content 里的 text 项，
    // 只写 tool_use / image 等非文本块；否则（runtime 没发 streamlined）
    // 才把 text 项写入兜底。
    final msgId = _activeAssistantMessageId;
    if (msgId == null) return;
    final hadStreamedText = _activeTextBlockId != null;
    await _closeActiveTextBlock();
    final content = f.message.content;
    if (content is! List) return;
    var idx = _nextBlockIndex;
    for (final raw in content) {
      if (raw is! Map<String, dynamic>) continue;
      final type = raw['type'] as String?;
      Block? b;
      switch (type) {
        case 'text':
          if (hadStreamedText) continue; // streamlined_text 已写过，跳过避免重复
          b = TextBlock(
            id: '${msgId}_a$idx',
            index: idx,
            state: BlockState.closed,
            text: (raw['text'] as String?) ?? '',
          );
        case 'tool_use':
          b = ToolUseBlock(
            id: '${msgId}_a$idx',
            index: idx,
            state: BlockState.closed,
            toolUseId: (raw['id'] as String?) ?? '',
            toolName: (raw['name'] as String?) ?? '',
            input: raw['input'] is Map<String, dynamic>
                ? raw['input'] as Map<String, dynamic>
                : null,
          );
      }
      if (b != null) {
        await repo.upsertBlock(b, messageId: msgId);
        _events.add(BlockUpdated(msgId, b));
        idx++;
      }
    }
    _nextBlockIndex = idx;
  }

  Future<void> _onToolResult(SDKToolUseSummary f) async {
    final msgId = _activeAssistantMessageId;
    if (msgId == null) return;
    await _closeActiveTextBlock();
    // 在 SDK Protocol 里 tool_result 通常是新的 user-role message，不是 assistant
    // 内部 block。这里简化：当 brain 推 tool_use_summary 帧时，作为本
    // assistant message 末尾的"工具结果展示用"块挂上（state=closed）。
    final tuId = f.precedingToolUseIds.isNotEmpty
        ? f.precedingToolUseIds.first
        : '';
    final block = ToolResultBlock(
      id: '${msgId}_tr$_nextBlockIndex',
      index: _nextBlockIndex,
      state: BlockState.closed,
      toolResultId: tuId,
      content: f.summary,
    );
    await repo.upsertBlock(block, messageId: msgId);
    _events.add(BlockUpdated(msgId, block));
    _nextBlockIndex++;
  }

  Future<void> _onResultSuccess(SDKResultSuccess f) async {
    final msgId = _activeAssistantMessageId;
    if (msgId == null) return;
    await _closeActiveTextBlock();
    int? input, output;
    final usage = f.usage;
    if (usage is Map) {
      input = (usage['input_tokens'] as num?)?.toInt();
      output = (usage['output_tokens'] as num?)?.toInt();
    }
    // brain F5 之后 stop_reason="interrupted" 是用户按 stop 走 clean-stop
    // 路径回来的;UI 应该当 cancelled 渲染(灰态),而不是 completed 的正
    // 常收尾。也避免 ChatController 把它当成功 finalize 拒绝重发等场景。
    final isInterrupted = f.stopReason == 'interrupted';
    final msgStatus = isInterrupted
        ? MessageStatus.cancelled
        : MessageStatus.completed;
    final sessStatus = isInterrupted
        ? SessionStatus.cancelled
        : SessionStatus.completed;
    await repo.finalizeMessage(
      msgId,
      status: msgStatus,
      stopReason: f.stopReason,
      inputTokens: input,
      outputTokens: output,
    );
    if (isInterrupted) {
      // clean-stop 路径 —— 客户端视角的端到端延迟 = 用户按 stop 到现在,
      // 包括 WS 往返 + brain 路由 + engine clean-stop + 帧返回。跟服务端的
      // agent_cancel_latency_seconds 对账差值就是网络 + UI 段。
      final latency = _consumeCancelLatency();
      _logCancelLatency(latency);
      _events.add(MessageCancelled(msgId, latency: latency));
    } else {
      _events.add(MessageCompleted(
        messageId: msgId,
        stopReason: f.stopReason ?? 'end_turn',
        inputTokens: input,
        outputTokens: output,
      ));
    }
    await repo.updateLastSeenSeq(_session.sessionId, _localSeq);
    // cancelling 等到的就是这条 Done{interrupted} —— 取消 timer 让兜底
    // 不再触发,正常关闭。
    _cancelTimeoutTimer?.cancel();
    _cancelTimeoutTimer = null;
    await _internalClose(sessStatus);
  }

  Future<void> _onResultError(SDKResultError f) async {
    final msgId = _activeAssistantMessageId;
    if (msgId == null) return;
    await _closeActiveTextBlock();
    final errMsg = f.errors.isNotEmpty ? f.errors.first.toString() : f.subtype;
    await repo.finalizeMessage(
      msgId,
      status: MessageStatus.failed,
      errorMessage: errMsg,
    );
    _events.add(MessageFailed(msgId, errMsg));
    await _internalClose(SessionStatus.failed);
  }

  Future<void> _closeActiveTextBlock() async {
    if (_activeTextBlockId == null) return;
    final msgId = _activeAssistantMessageId;
    if (msgId != null) {
      final block = TextBlock(
        id: _activeTextBlockId!,
        index: _nextBlockIndex,
        state: BlockState.closed,
        text: _textBuffer.toString(),
      );
      await repo.upsertBlock(block, messageId: msgId);
      _events.add(BlockUpdated(msgId, block));
    }
    _activeTextBlockId = null;
    _textBuffer.clear();
    _nextBlockIndex++;
  }

  // ─── Token refresh ─────────────────────────────────────────

  void _scheduleTokenRefresh() {
    final remaining = _session.tokenExpiresAt.difference(DateTime.now());
    final refreshIn = remaining - const Duration(minutes: 5);
    if (refreshIn <= Duration.zero) {
      // 已经过期 / 快过期 —— 立即 refresh
      _refreshTokenNow();
      return;
    }
    _tokenRefreshTimer?.cancel();
    _tokenRefreshTimer = Timer(refreshIn, _refreshTokenNow);
  }

  Future<void> _refreshTokenNow() async {
    if (_closed) return;
    try {
      final r = await agentPlane.refreshSessionToken(_session.sessionId);
      final newExp =
          r.expiresAt ?? DateTime.now().add(const Duration(minutes: 30));
      await repo.updateSessionToken(_session.sessionId,
          token: r.sessionToken, expiresAt: newExp);
      await _ws.refreshToken(r.sessionToken);
      _session = Session(
        sessionId: _session.sessionId,
        threadId: _session.threadId,
        mode: _session.mode,
        sessionToken: r.sessionToken,
        tokenExpiresAt: newExp,
        lastSeenSeq: _session.lastSeenSeq,
        status: _session.status,
        createdAt: _session.createdAt,
        closedAt: _session.closedAt,
      );
      _scheduleTokenRefresh();
    } catch (_) {
      // refresh 失败 —— 1 分钟后再试
      _tokenRefreshTimer = Timer(const Duration(minutes: 1), _refreshTokenNow);
    }
  }
}

/// 把 client 占位 model id（'biumind-default' = 让 brain 自己选官方模型）
/// 转成 brain 接受的形式。chat_page_v2 / new_thread_dialog 的 picker 用
/// `biumind-default` 表示"官方默认"；brain ChatRunner 收到空 model 时
/// fallback 到 claude-sonnet-4-6。
String? _stripPlaceholderModel(String? m) {
  if (m == null || m.isEmpty || m == 'biumind-default') return null;
  return m;
}
