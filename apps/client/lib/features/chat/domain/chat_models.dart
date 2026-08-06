// Chat 重构 R1 —— 领域类型，跟 drift Local* 数据类一一映射但解耦 UI。
//
// **不复用 sdkproto/v1 的 ContentBlock 类型** —— 那个类型字段丰富但 nullable
// 多，UI 层每次 ?? '' 太多。这里给 chat 模块自己一个干净的 sealed-like
// 形态，转换函数集中在 chat_repo.dart 里。

import 'dart:convert';
import 'dart:typed_data';

/// Thread mode：跟 brain agent_plane router.go 三态对齐。
enum ThreadMode {
  chat,
  agent,
  task;

  static ThreadMode fromName(String s) {
    switch (s) {
      case 'agent':
        return ThreadMode.agent;
      case 'task':
        return ThreadMode.task;
      case 'chat':
      default:
        return ThreadMode.chat;
    }
  }

  String get name => switch (this) {
        ThreadMode.chat => 'chat',
        ThreadMode.agent => 'agent',
        ThreadMode.task => 'task',
      };
}

/// Agent 工具调用的自治程度。client 拦截 SDKControlRequest{can_use_tool}
/// 时按这个字段决定立即应答 or 弹 ApprovalCard。
///
/// 跟 brain migration 00038 chat.threads.auto_approve check 约束保持一致。
enum AutoApproveMode {
  /// 全自动 allow（PermissionMode.bypassPermissions）—— 适合可信 workdir 自动跑。
  auto,

  /// 命中已批准规则 → allow；否则弹 client 询问。规则持久化复用
  /// settings.PersistPermissionUpdate(AddRules) 现成路径。
  whitelist,

  /// 每次都弹 ApprovalCard 让用户确认。默认；安全优先。
  manual;

  static AutoApproveMode fromName(String s) {
    switch (s) {
      case 'auto':
        return AutoApproveMode.auto;
      case 'whitelist':
        return AutoApproveMode.whitelist;
      case 'manual':
      default:
        return AutoApproveMode.manual;
    }
  }

  String get name => switch (this) {
        AutoApproveMode.auto => 'auto',
        AutoApproveMode.whitelist => 'whitelist',
        AutoApproveMode.manual => 'manual',
      };
}

/// Message role —— 跟 SDK Protocol AnthropicMessage.role 对齐。
/// 'user' / 'assistant' / 'tool_result' / 'system'.
enum MessageRole {
  user,
  assistant,
  toolResult,
  system;

  static MessageRole fromName(String s) {
    switch (s) {
      case 'assistant':
        return MessageRole.assistant;
      case 'tool_result':
        return MessageRole.toolResult;
      case 'system':
        return MessageRole.system;
      case 'user':
      default:
        return MessageRole.user;
    }
  }

  String get name => switch (this) {
        MessageRole.user => 'user',
        MessageRole.assistant => 'assistant',
        MessageRole.toolResult => 'tool_result',
        MessageRole.system => 'system',
      };
}

/// Message lifecycle status. UI 用这个画 status icon / spinner / 重试按钮。
enum MessageStatus {
  pending,    // 用户输入了但还没建 brain session（offline 或网络抖）
  streaming,  // brain 在推 frame，blocks 增量到达
  completed,  // result_success 帧到了
  failed,     // result_error 或 网络断了无法 resume
  cancelled;  // 用户主动 cancel

  static MessageStatus fromName(String s) {
    switch (s) {
      case 'pending':
        return MessageStatus.pending;
      case 'streaming':
        return MessageStatus.streaming;
      case 'failed':
        return MessageStatus.failed;
      case 'cancelled':
        return MessageStatus.cancelled;
      case 'completed':
      default:
        return MessageStatus.completed;
    }
  }

  String get name => switch (this) {
        MessageStatus.pending => 'pending',
        MessageStatus.streaming => 'streaming',
        MessageStatus.completed => 'completed',
        MessageStatus.failed => 'failed',
        MessageStatus.cancelled => 'cancelled',
      };
}

class Thread {
  final String id;
  final String title;
  final ThreadMode mode;
  final String? environmentId;
  final String? poolTag;
  final String? model;
  /// 指定走哪个 chat.providers.provider_id slug(biumind_cloud / anthropic / ...)。
  /// null = 老语义,brain 自己挑 active provider。picker 选模型时一并设上,
  /// 保证同 model id 多 provider 时切换准确。
  final String? providerId;
  final String? systemPrompt;
  /// 关联到的 wiki project；null = 全局 /chat 对话。
  final String? projectId;
  /// Agent / task 模式下 daemon 跑工具的工作目录。chat 模式必空。
  /// daemon worker.go 收到 work payload 后 chdir + 注入 biumindkit Options.Cwd。
  final String? workdir;
  /// Agent 工具调用自治程度（默认 manual）。chat 模式无意义但字段共用。
  final AutoApproveMode autoApprove;
  /// 工具执行环境 (Runtime v3 轴 B): 'none' | 'local' | 'cloud'。与 mode 正交。
  /// chat 恒 'none'；agent 默认 'local'(可选 'cloud',R5 落地);task 恒 'cloud'。
  final String runtimeEnvMode;
  /// Agent loop backend (Runtime v3 R3/Q3): 'biumindkit'(默认) | 'claude-cli'
  /// | 'codex-cli'。仅 agent 模式有意义。claude-cli=外部 Claude Code 当 backend
  /// (用你的订阅,不计 biumind 额度)。
  final String backend;
  final bool pinned;
  final bool archived;
  final DateTime createdAt;
  final DateTime updatedAt;

  Thread({
    required this.id,
    required this.title,
    required this.mode,
    this.environmentId,
    this.poolTag,
    this.model,
    this.providerId,
    this.systemPrompt,
    this.projectId,
    this.workdir,
    this.autoApprove = AutoApproveMode.manual,
    this.runtimeEnvMode = 'none',
    this.backend = 'biumindkit',
    this.pinned = false,
    this.archived = false,
    required this.createdAt,
    required this.updatedAt,
  });
}

/// Block —— 跟 SDK Protocol ContentBlock 对齐，但用 Dart-friendly sealed class
/// 风格让 switch case 完整。所有派生类共享 [id] / [index]（在 message 里
/// 的位置）/ [state]（streaming|closed）。
sealed class Block {
  final String id;
  final int index;
  final BlockState state;

  const Block({required this.id, required this.index, required this.state});
}

enum BlockState {
  streaming,  // text delta 还在拼 / tool_use 还在收 input
  closed;     // 完成态

  static BlockState fromName(String s) {
    switch (s) {
      case 'streaming':
        return BlockState.streaming;
      case 'closed':
      default:
        return BlockState.closed;
    }
  }

  String get name => switch (this) {
        BlockState.streaming => 'streaming',
        BlockState.closed => 'closed',
      };
}

class TextBlock extends Block {
  final String text;
  const TextBlock({
    required super.id,
    required super.index,
    required super.state,
    required this.text,
  });
}

class ToolUseBlock extends Block {
  final String toolUseId;
  final String toolName;
  final Map<String, dynamic>? input;
  const ToolUseBlock({
    required super.id,
    required super.index,
    required super.state,
    required this.toolUseId,
    required this.toolName,
    this.input,
  });
}

class ToolResultBlock extends Block {
  final String toolResultId; // tool_use_id 反向引用
  final bool isError;
  /// 嵌套 ContentBlock —— Anthropic 的 tool_result 可包文本 / 图等。
  /// 这里先简化用 String 内容；多模态 tool_result 落地再扩。
  final String content;
  const ToolResultBlock({
    required super.id,
    required super.index,
    required super.state,
    required this.toolResultId,
    this.isError = false,
    required this.content,
  });
}

class ImageBlock extends Block {
  final String mimeType;
  final String data; // base64
  const ImageBlock({
    required super.id,
    required super.index,
    required super.state,
    required this.mimeType,
    required this.data,
  });
}

class Message {
  final String id;
  final String threadId;
  final MessageRole role;
  final MessageStatus status;
  final String? sessionId;
  final String? stopReason;
  final String? model;
  final int? inputTokens;
  final int? outputTokens;
  final int seq;
  final String? errorMessage;
  final DateTime createdAt;
  final DateTime? completedAt;
  final List<Block> blocks;

  Message({
    required this.id,
    required this.threadId,
    required this.role,
    required this.status,
    this.sessionId,
    this.stopReason,
    this.model,
    this.inputTokens,
    this.outputTokens,
    required this.seq,
    this.errorMessage,
    required this.createdAt,
    this.completedAt,
    this.blocks = const [],
  });

  /// 给 UI 一个折叠展示用的 plain text（所有 TextBlock 拼起来）。
  String get assembledText {
    final sb = StringBuffer();
    for (final b in blocks) {
      if (b is TextBlock) {
        if (sb.isNotEmpty) sb.write('\n');
        sb.write(b.text);
      }
    }
    return sb.toString();
  }
}

class Session {
  final String sessionId;
  final String threadId;
  final ThreadMode mode;
  final String sessionToken;
  final DateTime tokenExpiresAt;
  final int lastSeenSeq;
  final SessionStatus status;
  final DateTime createdAt;
  final DateTime? closedAt;

  Session({
    required this.sessionId,
    required this.threadId,
    required this.mode,
    required this.sessionToken,
    required this.tokenExpiresAt,
    this.lastSeenSeq = 0,
    required this.status,
    required this.createdAt,
    this.closedAt,
  });

  bool get isActive => status == SessionStatus.active;
  bool get tokenExpiringSoon =>
      tokenExpiresAt.difference(DateTime.now()) < const Duration(minutes: 5);
}

enum SessionStatus {
  active,
  // pending：agent 模式投给当前离线的设备，brain 已排队(agent_pending_work)，
  // 等设备上线自动派发(Runtime v3 R7)。客户端不应连 WS、应渲染"已排队"。
  pending,
  // paused：会话被挂起(DB 态，早于 R7 即存在)。
  paused,
  completed,
  failed,
  cancelled;

  static SessionStatus fromName(String s) {
    switch (s) {
      case 'pending':
        return SessionStatus.pending;
      case 'paused':
        return SessionStatus.paused;
      case 'completed':
        return SessionStatus.completed;
      case 'failed':
        return SessionStatus.failed;
      case 'cancelled':
        return SessionStatus.cancelled;
      case 'active':
      default:
        return SessionStatus.active;
    }
  }

  String get name => switch (this) {
        SessionStatus.active => 'active',
        SessionStatus.pending => 'pending',
        SessionStatus.paused => 'paused',
        SessionStatus.completed => 'completed',
        SessionStatus.failed => 'failed',
        SessionStatus.cancelled => 'cancelled',
      };
}

/// 跨会话搜索命中的一条结果 —— ChatRepo.searchMessages 返。
/// snippet 是文本里命中片段的前后 30 字预览。
class MessageSearchHit {
  final String messageId;
  final String threadId;
  final String threadTitle;
  final MessageRole role;
  final int seq;
  final DateTime createdAt;
  final String snippet;

  const MessageSearchHit({
    required this.messageId,
    required this.threadId,
    required this.threadTitle,
    required this.role,
    required this.seq,
    required this.createdAt,
    required this.snippet,
  });
}

/// 收藏消息（star）跨 thread 列表的一条 —— Pinned messages 侧栏用。
class StarredMessageHit {
  final String messageId;
  final String threadId;
  final String threadTitle;
  final MessageRole role;
  final String snippet;
  final DateTime starredAt;

  const StarredMessageHit({
    required this.messageId,
    required this.threadId,
    required this.threadTitle,
    required this.role,
    required this.snippet,
    required this.starredAt,
  });
}

/// AttachmentInput —— sendMessage 时传入的附件（图片）二进制 + 类型。
/// BiuSessionConnection.open 把这些写成 ImageBlock 挂到 user message 之后。
/// 当前 brain agent_plane 还不收多模态 ContentBlock；本地落库 + UI 渲染优先，
/// brain 接通后只需要扩 createSession 协议即可不改 UI。
class AttachmentInput {
  final String mimeType;
  final Uint8List bytes;
  const AttachmentInput({required this.mimeType, required this.bytes});
}

/// JSON helpers —— ChatRepo 内部 / 外部都用得上。
Map<String, dynamic>? decodeJsonMap(String? s) {
  if (s == null || s.isEmpty) return null;
  try {
    final v = jsonDecode(s);
    return v is Map<String, dynamic> ? v : null;
  } catch (_) {
    return null;
  }
}

String? encodeJsonMap(Map<String, dynamic>? m) {
  if (m == null) return null;
  return jsonEncode(m);
}
