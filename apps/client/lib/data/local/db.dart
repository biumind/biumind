// AppDb — single-file Drift schema for the BiuMind local cache.
//
// Tables:
//   * WikiProjects/WikiPages/WikiBlocks  — local mirror of server entities
//   * WikiOutbox                          — pending writes awaiting upload
//   * NoteNotes/NoteNotebooks/NoteTags/  — 笔记域本地镜像（与 Wiki 独立，
//     NoteNoteTags/NoteOutbox              设计 docs/BiuMind-Notes-Design-Draft.md）
//   * ChatThreadsV2/ChatMessagesV2/      — Chat 重构 v2，brain Agent
//     ChatContentBlocks/ChatSessions       Plane 驱动；ContentBlock 对齐
//                                          SDK Protocol v1
//   * ChatSyncState/ChatOutbox           — P1 同步闭环：下行游标（增量
//                                          hydrate + 墓碑）+ 上行写盒
//   * CodeTasks/CodeTaskOutbox/...       — 编码工作台多端同步
//
// Storage is platform-conditional:
//   * Native (macOS/iOS/Linux/Windows/Android) — sqlite via dart:ffi
//     (drift/native.dart) backed by a file under
//     getApplicationSupportDirectory()/biumind.sqlite
//   * Web — in-memory only for now (P6.3 alpha). Drift has a WASM
//     backend (drift/wasm.dart) that lands in P6.3.5 to give web users
//     real persistence. Until then, web sessions are ephemeral; the
//     model-relay-side store is still the source of truth so refresh on next
//     visit is fine.
//
// The split is done via conditional imports — `db_open_io.dart` brings in
// `drift/native.dart` (which itself transitively imports dart:ffi);
// `db_open_web.dart` returns an in-memory executor with no native deps.

import 'package:drift/drift.dart';

import 'db_open_io.dart' if (dart.library.html) 'db_open_web.dart' as opener;

part 'db.g.dart';

@DataClassName('LocalWikiProject')
class WikiProjects extends Table {
  TextColumn get id => text()();
  TextColumn get name => text()();
  DateTimeColumn get updatedAt => dateTime()();

  @override
  Set<Column> get primaryKey => {id};
}

@DataClassName('LocalWikiPage')
class WikiPages extends Table {
  TextColumn get id => text()();
  TextColumn get projectId => text()();
  TextColumn get title => text().withDefault(const Constant(''))();
  IntColumn get version => integer().withDefault(const Constant(1))();
  TextColumn get parentId => text().nullable()();
  DateTimeColumn get updatedAt => dateTime()();

  @override
  Set<Column> get primaryKey => {id};
}

@DataClassName('LocalWikiBlock')
class WikiBlocks extends Table {
  TextColumn get id => text()();
  TextColumn get pageId => text()();
  RealColumn get position => real()();
  TextColumn get type => text()();
  TextColumn get contentJson => text()();
  IntColumn get version => integer().withDefault(const Constant(1))();
  BoolColumn get deleted => boolean().withDefault(const Constant(false))();
  DateTimeColumn get updatedAt => dateTime()();

  @override
  Set<Column> get primaryKey => {id};
}

/// WikiOutbox — append-only queue of writes that need to be uploaded.
///
/// `op` is one of:
///   create_project | create_page | create_block | update_block | delete_block
///
/// `entityId` is the local UUID assigned at enqueue time. For create ops the
/// repository swaps in the server-assigned id once the upload succeeds.
@DataClassName('OutboxEntry')
class WikiOutbox extends Table {
  IntColumn get id => integer().autoIncrement()();
  TextColumn get op => text()();
  TextColumn get entityId => text()();
  TextColumn get projectId => text().nullable()();
  TextColumn get pageId => text().nullable()();
  TextColumn get payloadJson => text()();
  IntColumn get baseVersion => integer().nullable()();
  IntColumn get attempts => integer().withDefault(const Constant(0))();
  TextColumn get lastError => text().nullable()();
  DateTimeColumn get createdAt => dateTime()();
  DateTimeColumn get nextAttemptAt => dateTime()();
}

// ─── Notes (笔记) tables ─────────────────────────────────────
//
// 笔记是与 Wiki（知识库）完全独立的个人笔记域（设计
// docs/BiuMind-Notes-Design-Draft.md §4 D1/D4）：整篇 markdown 权威，
// 无块层；组织靠单层笔记本 + 标签；软删回收站（trashed/trashedAt）；
// version 乐观锁对齐服务端 If-Match。
//
// O2 决策「先复制后收敛」：不做 outbox 泛化重构，笔记用独立的
// NoteOutbox 表 + NoteOutboxFlusher（去掉 wiki 的 projectId/pageId 列，
// 换成 nullable notebookId），两个版本内再合并。

/// NoteNotebooks —— 单层笔记本（设计 §4 D7-5 明确不做树形嵌套）。
/// 本地无软删列：服务端软删笔记本后 changes 事件直接把本地行删掉，
/// 挂在它下面的笔记由服务端还原逻辑置根。
@DataClassName('LocalNoteNotebook')
class NoteNotebooks extends Table {
  TextColumn get id => text()();
  TextColumn get name => text()();
  RealColumn get position => real().withDefault(const Constant(0.0))();
  DateTimeColumn get updatedAt => dateTime()();
  /// 见 ChatThreadsV2.ownerKey —— 环境 × 账号隔离键，查询必填过滤。
  /// v33（Phase 33）加列；迁移已清空无归属存量行（跨账号泄露源）。
  TextColumn get ownerKey => text().withDefault(const Constant(''))();

  @override
  Set<Column> get primaryKey => {id};
}

/// NoteNotes —— 笔记本体。contentMd 是整篇 markdown（无块层）。
/// trashed/trashedAt 是服务端 deleted_at 的本地投影（回收站视图）；
/// 物理删除（purge）的事件直接把行删掉。
@DataClassName('LocalNote')
class NoteNotes extends Table {
  TextColumn get id => text()();
  TextColumn get notebookId => text().nullable()();
  TextColumn get title => text().withDefault(const Constant(''))();
  TextColumn get contentMd => text().withDefault(const Constant(''))();
  BoolColumn get isTodo => boolean().withDefault(const Constant(false))();
  DateTimeColumn get todoCompletedAt => dateTime().nullable()();
  RealColumn get position => real().withDefault(const Constant(0.0))();
  IntColumn get version => integer().withDefault(const Constant(1))();
  BoolColumn get trashed => boolean().withDefault(const Constant(false))();
  DateTimeColumn get trashedAt => dateTime().nullable()();
  /// 归档时间（转入知识库后服务端置位）。null = 未归档。归档笔记不进
  /// 默认列表（对齐服务端 GET /v1/notes 默认排除归档）。
  DateTimeColumn get archivedAt => dateTime().nullable()();
  /// 转入知识库后对应的 wiki page id，null = 未转入。编辑器用它显示
  /// 「已转入知识库」只读提示条。
  TextColumn get promotedPageId => text().nullable()();
  DateTimeColumn get updatedAt => dateTime()();
  /// 见 ChatThreadsV2.ownerKey —— 环境 × 账号隔离键，查询必填过滤。
  /// v33（Phase 33）加列；迁移已清空无归属存量行（跨账号泄露源）。
  TextColumn get ownerKey => text().withDefault(const Constant(''))();

  @override
  Set<Column> get primaryKey => {id};
}

/// NoteTags —— 个人标签（服务端 `scope_key='personal:<uid>'` 单用户，
/// 本地不必再存 scope）。创建幂等，flush 成功后 rekey。
@DataClassName('LocalNoteTag')
class NoteTags extends Table {
  TextColumn get id => text()();
  TextColumn get name => text()();
  /// 见 ChatThreadsV2.ownerKey —— 环境 × 账号隔离键，查询必填过滤。
  /// v33（Phase 33）加列；迁移已清空无归属存量行（跨账号泄露源）。
  TextColumn get ownerKey => text().withDefault(const Constant(''))();

  @override
  Set<Column> get primaryKey => {id};
}

/// NoteNoteTags —— 笔记×标签关联（整组替换语义，对齐服务端
/// PUT /v1/notes/{id}/tags）。
class NoteNoteTags extends Table {
  TextColumn get noteId => text()();
  TextColumn get tagId => text()();
  /// 见 ChatThreadsV2.ownerKey —— 环境 × 账号隔离键，查询必填过滤。
  /// v33（Phase 33）加列；迁移已清空无归属存量行（跨账号泄露源）。
  TextColumn get ownerKey => text().withDefault(const Constant(''))();

  @override
  Set<Column> get primaryKey => {noteId, tagId};
}

/// NoteOutbox —— 笔记域离线写盒（WikiOutbox 的复制改造版，O2）。
///
/// `op` is one of:
///   create_notebook | update_notebook | delete_notebook |
///   create_note | update_note | trash_note | restore_note | purge_note |
///   create_tag | set_note_tags
///
/// `entityId` 是 enqueue 时的本地 id；create_* op flush 成功后由
/// flusher rekey 成服务端 uuid 并改写引用它的待冲刷 op。
@DataClassName('NoteOutboxEntry')
class NoteOutbox extends Table {
  IntColumn get id => integer().autoIncrement()();
  TextColumn get op => text()();
  TextColumn get entityId => text()();
  TextColumn get notebookId => text().nullable()();
  TextColumn get payloadJson => text()();
  IntColumn get baseVersion => integer().nullable()();
  IntColumn get attempts => integer().withDefault(const Constant(0))();
  TextColumn get lastError => text().nullable()();
  DateTimeColumn get createdAt => dateTime()();
  DateTimeColumn get nextAttemptAt => dateTime()();
  /// 见 ChatThreadsV2.ownerKey —— 环境 × 账号隔离键，查询必填过滤。
  /// flusher 只 flush 当前登录 scope 的 op（v33 Phase 33 加列），杜绝
  /// 「下个账号登录后把上个账号未冲刷的写盒 flush 进自己账号」的串写。
  TextColumn get ownerKey => text().withDefault(const Constant(''))();
}

// ─── Chat tables: ChatThreads / ChatMessages / MessageReactions /
// ChatOutbox(v1) 删除于 2026-05-31（Chat 重构 R7）。Drift v2 表迁移到
// ChatThreadsV2 / ChatMessagesV2 / ChatContentBlocks / ChatSessions。
// v32 重建的 ChatOutbox 是 P1.3 上行写盒（scope 隔离），与 v1 老表无 schema
// 延续关系。

// CodeTaskOutbox / CodeSyncCursors 已随 codeSync 废弃移除(D4/Code-I6,schema v24
// DROP)。编码任务 100% 本地,Drift 即唯一真相源,无 outbox / 无云同步。

/// 编码工作台任务持久化 — 重启 app 后任务列表 + 流式事件 + 状态全部恢复。
/// 支持读 watch (StateNotifier 监听 DAO 流) + 写 upsert (event-by-event 立即落库)。
@DataClassName('LocalCodeTask')
class CodeTasks extends Table {
  TextColumn get id => text()();
  TextColumn get title => text()();
  TextColumn get prompt => text()();
  TextColumn get agent => text()(); // 'biu' / 'claudeCode' / 'codex'
  TextColumn get mode => text()(); // 'ask' / 'autoEdit' / 'fullAccess'
  TextColumn get status => text()(); // CodeTaskStatus.name
  /// AgentEvent[] JSON, 流式事件全列表. 每次 event 来 in-memory 拼好后 upsert.
  TextColumn get eventsJson => text().withDefault(const Constant('[]'))();
  RealColumn get costUsd => real().withDefault(const Constant(0.0))();
  IntColumn get inputTokens => integer().withDefault(const Constant(0))();
  IntColumn get outputTokens => integer().withDefault(const Constant(0))();
  DateTimeColumn get createdAt => dateTime()();
  DateTimeColumn get completedAt => dateTime().nullable()();
  TextColumn get errorMessage => text().nullable()();

  /// WorkspaceRef.toJson() 序列化。null = 任务还在 allocate / passthrough 模式。
  TextColumn get workspaceJson => text().nullable()();

  /// 对比组关联. 同 prompt 派给多 agent 时这些 task 共享同一 id。
  TextColumn get compareGroupId => text().nullable()();

  /// 任务跑在哪台机器 (CSY4)。本机创建 = 本机 codeOriginDeviceId;
  /// 远端 pull / Realtime 拉来的任务 = 对方 device id。
  TextColumn get originDeviceId => text().nullable()();
  TextColumn get originDeviceLabel => text().nullable()();

  /// 所属项目 (M1 多项目)。指向 CodeProjects.id。nullable —— 老任务(单 workspace
  /// 时代)无项目归属;M1 迁移后新任务必带。
  TextColumn get projectId => text().nullable()();

  /// 最后更新时间 (M1)。TaskList 按它排序/显示
  /// "n 分钟前"。nullable —— 老行无,读时回退 createdAt。
  DateTimeColumn get updatedAt => dateTime().nullable()();

  /// 用户为本任务选的模型 id(M4,schema v23)。null = agent 默认。仅本地持久化
  /// (codeSync 已废弃 D4/Code-I6,不进同步)。
  TextColumn get model => text().nullable()();

  /// 星标(CORE-2,schema v26)。仅本地。
  BoolColumn get starred => boolean().withDefault(const Constant(false))();

  @override
  Set<Column> get primaryKey => {id};
}

/// 编码模块的项目(代码仓库)— M1 多项目。落 Drift(零云同步,Drift 即 SoT)。
/// 命名 code_projects 与 code_tasks 一致、避开 wiki_projects 歧义。
@DataClassName('LocalCodeProject')
class CodeProjects extends Table {
  TextColumn get id => text()();
  TextColumn get name => text()();

  /// 仓库绝对路径。
  TextColumn get path => text()();

  /// 当前分支(展示用,nullable —— 非 git 目录或未解析)。
  TextColumn get branch => text().nullable()();

  /// 最后打开时间(ms epoch)。WelcomePage "最近" 排序用。
  IntColumn get lastOpenedAt => integer().withDefault(const Constant(0))();

  /// 从左 Rail 隐藏。不删项目、只隐藏。
  BoolColumn get hiddenFromRail =>
      boolean().withDefault(const Constant(false))();

  /// 头像底色(ProjectRail 头像;null = 由 name 哈希生成)。
  TextColumn get avatarColor => text().nullable()();

  /// 手动排序位次(M1 拖拽排序)。小在前;同值回退 lastOpenedAt。默认 0。
  IntColumn get sortIndex => integer().withDefault(const Constant(0))();

  @override
  Set<Column> get primaryKey => {id};
}

/// SSE Last-Event-ID 续传游标 (v2-4) — 重启 app 后立即从上次 cursor 续接,
/// 避免后台 30s+ 漏事件. RealtimeHub 实例间用 [scope] 区分 (e.g.
/// 'aigc.tasks' / 'code.tasks' / 'skills.events'); 服务端 ledger 全局
/// ID ordering, 续连时 last-event-id header 配合 topic filter 能取到正确
/// 的 replay 范围.
@DataClassName('LocalSseCursor')
class SseCursors extends Table {
  TextColumn get scope => text()(); // RealtimeHub 实例标识, e.g. 'aigc.tasks'
  TextColumn get lastEventId => text()();
  DateTimeColumn get updatedAt => dateTime()();
  @override
  Set<Column> get primaryKey => {scope};
}

/// AIGC 创作任务持久化 (v2-1) — 重启 app 后任务列表 + 状态恢复.
///
/// 跟 CodeTasks 区分: aigc 任务是图/视频/数字人生成, 走 services/aigc, NATS
/// + dashscope/volcengine; outputs 在服务端 worker 跑完后通过 SSE 推回, 客户端
/// 不持久化产物 url (cas:sha 是云端引用, 不存本地). 仅持久化 task 元数据 +
/// outputs JSON 供重启秒回 UI.
///
/// outputs_json 是 `List<TaskOutput>` JSON, params_json 是 `Map<String, dynamic>`
/// JSON. 状态机字段 status / progress 跟 CreationTask 一致.
@DataClassName('LocalAigcTask')
class AigcTasks extends Table {
  TextColumn get id => text()(); // task uuid (server) 或 client tempId
  TextColumn get userId => text()();
  TextColumn get type => text()(); // image / video / digital_human / hotparse
  TextColumn get modelCode => text()();
  TextColumn get providerCode => text().nullable()();
  TextColumn get status => text()(); // TaskStatus.name
  IntColumn get progress => integer().withDefault(const Constant(0))();
  TextColumn get prompt => text()();
  TextColumn get negativePrompt => text().nullable()();
  TextColumn get paramsJson => text().withDefault(const Constant('{}'))();
  TextColumn get outputsJson => text().withDefault(const Constant('[]'))();
  IntColumn get costCredits => integer().withDefault(const Constant(0))();
  IntColumn get refundedCredits => integer().withDefault(const Constant(0))();
  BoolColumn get isPublic => boolean().withDefault(const Constant(false))();
  TextColumn get errorCode => text().nullable()();
  TextColumn get errorMessage => text().nullable()();
  TextColumn get localTempId => text().nullable()();
  DateTimeColumn get createdAt => dateTime()();
  DateTimeColumn get queuedAt => dateTime().nullable()();
  DateTimeColumn get startedAt => dateTime().nullable()();
  DateTimeColumn get completedAt => dateTime().nullable()();
  DateTimeColumn get updatedAt => dateTime()();

  @override
  Set<Column> get primaryKey => {id};
}

/// 编码任务产物 — 元数据 (L1) + 可选 L2 preview / L3 cloud_file_id。
/// 设计文档 docs/BiuMind-Code-Artifacts-Sync-Design.md §3.1。
@DataClassName('LocalCodeTaskArtifact')
class CodeTaskArtifacts extends Table {
  TextColumn get id => text()();
  TextColumn get taskId => text()();
  TextColumn get kind => text()(); // codeFile / image / ...
  TextColumn get relPath => text()();
  TextColumn get mimeType => text().nullable()();
  IntColumn get sizeBytes => integer().withDefault(const Constant(0))();
  TextColumn get sha256 => text()();
  TextColumn get op => text()();

  /// L2 preview — 默认 null, 由 preview generator 填。
  TextColumn get previewSummary => text().nullable()();
  TextColumn get previewDataB64 => text().nullable()();
  TextColumn get previewMimeType => text().nullable()();

  // cloudFileId / cloudUploadedAt(L3 云上传)已随 artifacts-sync 废弃移除
  // (D4/Code-I4/Code-I6,schema v25 DROP COLUMN)。产物 100% 本地。

  DateTimeColumn get createdAt => dateTime()();

  @override
  Set<Column> get primaryKey => {id};
}

// ─── Chat 重构 v2 schema ─────────────────────────────────────
//
// BiuClient (brain Agent Plane) 驱动的 chat 重构。字段直接复刻 SDK
// Protocol v1 ContentBlock 形态（packages/go-sdk/biu/sdkproto/v1/data/
// sdk_message.dart）—— 收 frame 即所见即所得，无需 ChunkV2 ↔ proto
// 翻译损耗。R7 cleanup 删了老 chat_* 表 + 老 Dart 代码。

/// ChatThreadsV2 —— 重构后的 thread 元数据。`mode` 决定走哪条 brain 路径。
@DataClassName('LocalChatThreadV2')
class ChatThreadsV2 extends Table {
  TextColumn get id => text()(); // ULID
  TextColumn get title => text().withDefault(const Constant(''))();
  /// 'chat' | 'agent' | 'task'
  TextColumn get mode => text()();
  /// agent / task mode 选的 worker; chat mode 必空
  TextColumn get environmentId => text().nullable()();
  /// task mode 路由用 pool tag; chat / agent 必空
  TextColumn get poolTag => text().nullable()();
  TextColumn get model => text().nullable()();
  /// 指定走哪个 chat.providers.provider_id slug(biumind_cloud / anthropic / ...)。
  /// null = 老语义,brain 自己挑 active provider。picker 选模型时一并设上,
  /// 保证同 model id 多 provider 时切换准确。
  TextColumn get providerId => text().nullable()();
  TextColumn get systemPrompt => text().nullable()();
  /// 关联到的 wiki project；null = 全局对话。Wiki 工作区内嵌 chat 面板
  /// 用这个过滤；按 (project_id, updated_at desc) 分组排序。
  TextColumn get projectId => text().nullable()();
  /// Agent / task 模式下 daemon 跑工具的工作目录。chat 模式必空。
  /// brain 投递 work payload 时透传，daemon worker.go chdir + 写到
  /// biumindkit Options.Cwd / PermissionUpdate.AddDirectories。
  TextColumn get workdir => text().nullable()();
  /// Agent 工具调用自治程度: 'auto' / 'whitelist' / 'manual' (default).
  /// client 拦截 SDKControlRequest{can_use_tool} 时按此字段决定立即应答
  /// or 弹 ApprovalCard。chat 模式无意义但字段共用,简单。
  TextColumn get autoApprove =>
      text().withDefault(const Constant('manual'))();
  /// 工具执行环境 (Runtime v3 轴 B): 'none' | 'local' | 'cloud'。决定工具在
  /// 哪落地执行,与 mode(轴 A:loop 在哪)正交。chat 恒 'none'；agent 默认
  /// 'local'(本机 daemon),可选 'cloud'(云沙箱,R5 落地);task 恒 'cloud'。
  /// createSession 透传给 brain → agent_sessions.runtime_env_mode。
  TextColumn get runtimeEnvMode =>
      text().withDefault(const Constant('none'))();
  /// Agent loop backend (Runtime v3 R3/Q3): 'biumindkit'(默认) | 'claude-cli'
  /// | 'codex-cli'。biumindkit=内建 loop;claude-cli=外部 Claude Code CLI 当
  /// backend(CLI 自执行工具,用用户自己的 ~/.claude 订阅,不计 biumind 额度)。
  /// 仅 agent 模式有意义;chat/task 恒 biumindkit。createSession 透传给 brain。
  TextColumn get backend =>
      text().withDefault(const Constant('biumindkit'))();
  BoolColumn get pinned => boolean().withDefault(const Constant(false))();
  BoolColumn get archived => boolean().withDefault(const Constant(false))();
  DateTimeColumn get createdAt => dateTime()();
  DateTimeColumn get updatedAt => dateTime()();
  /// 下行同步的精确比较基准: 服务端 thread.updated_at 的微秒整数
  /// (RFC3339Nano 解析后 microsecondsSinceEpoch 原样写入,~1.75e15 在
  /// web double 安全范围内,无需 int64)。updatedAt 列被 Drift 截断到秒,
  /// 无法区分同一秒内的多次服务端更新(user/assistant 同秒落库),故另存
  /// 此列。null = 本机产生、从未从服务端同步过的会话。
  IntColumn get remoteUpdatedAtUs => integer().nullable()();
  /// P0 数据隔离（docs/BiuMind-Local-Data-Isolation-Design.md §2）：scope 列 =
  /// sha256(normalize(identityUrl)) + ":" + JWT sub，「环境 × 账号」复合键。
  /// 所有查询强制按此列过滤；'' 为非法值（查询永不匹配，写入必填当前 scope）。
  TextColumn get ownerKey => text().withDefault(const Constant(''))();
  @override
  Set<Column> get primaryKey => {id};
}

/// ChatMessagesV2 —— per-message metadata，blocks 在 chat_content_blocks。
@DataClassName('LocalChatMessageV2')
class ChatMessagesV2 extends Table {
  /// = SDK frame uuid 或客户端生成的 ULID（user message）
  TextColumn get id => text()();
  TextColumn get threadId => text()();
  /// 'user' | 'assistant' | 'tool_result' | 'system'
  TextColumn get role => text()();
  /// 'pending' | 'streaming' | 'completed' | 'failed' | 'cancelled'
  TextColumn get status => text()();
  /// brain session this message belongs to; user 消息 mid-session 转新 session 时也会更新
  TextColumn get sessionId => text().nullable()();
  /// end_turn | tool_use | max_tokens | stop_sequence | error
  TextColumn get stopReason => text().nullable()();
  TextColumn get model => text().nullable()();
  IntColumn get inputTokens => integer().nullable()();
  IntColumn get outputTokens => integer().nullable()();
  /// 同 thread 内顺序，用于排序 + cursor pagination
  IntColumn get seq => integer()();
  TextColumn get errorMessage => text().nullable()();
  DateTimeColumn get createdAt => dateTime()();
  DateTimeColumn get completedAt => dateTime().nullable()();
  /// 见 ChatThreadsV2.ownerKey —— 环境 × 账号隔离键，查询必填过滤。
  TextColumn get ownerKey => text().withDefault(const Constant(''))();
  @override
  Set<Column> get primaryKey => {id};
}

/// ChatContentBlocks —— 一条 message 的内容块列表。字段直接对齐 SDK
/// Protocol v1 ContentBlock。一对多：messageId × index 唯一。
@DataClassName('LocalChatContentBlock')
class ChatContentBlocks extends Table {
  TextColumn get id => text()(); // ULID
  TextColumn get messageId => text()();
  /// 0-based 在 message.content 数组里的位置
  IntColumn get blockIndex => integer()();
  /// 'text' | 'tool_use' | 'tool_result' | 'image'
  TextColumn get type => text()();
  /// type=text
  TextColumn get textContent => text().nullable()();
  /// type=tool_use
  TextColumn get toolUseId => text().nullable()();
  TextColumn get toolUseName => text().nullable()();
  TextColumn get toolUseInputJson => text().nullable()();
  /// type=tool_result
  TextColumn get toolResultId => text().nullable()();
  BoolColumn get toolResultIsError => boolean().nullable()();
  /// JSON 字符串 —— ContentBlock[] 嵌套结构
  TextColumn get toolResultContentJson => text().nullable()();
  /// type=image
  TextColumn get imageMimeType => text().nullable()();
  TextColumn get imageData => text().nullable()();
  /// streaming 时 block 状态：'streaming'（text delta 还在拼）| 'closed'
  TextColumn get state => text().withDefault(const Constant('closed'))();
  DateTimeColumn get createdAt => dateTime()();
  DateTimeColumn get updatedAt => dateTime()();
  /// 见 ChatThreadsV2.ownerKey —— 环境 × 账号隔离键，查询必填过滤。
  TextColumn get ownerKey => text().withDefault(const Constant(''))();
  @override
  Set<Column> get primaryKey => {id};
}

/// MessageReactionsV2 —— 用户对单条消息的反馈（👍 / 👎 / ⭐）。
/// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md P0-1。
/// 现阶段仅本地，不同步到 brain；未来 RLHF / 收藏夹同步可基于此表扩展。
/// (messageId, kind) 唯一 —— toggle 时直接 delete row 而不是 set false。
@DataClassName('LocalMessageReactionV2')
class MessageReactionsV2 extends Table {
  IntColumn get id => integer().autoIncrement()();
  TextColumn get messageId => text()();
  TextColumn get threadId => text()();
  /// 'like' | 'dislike' | 'star'
  TextColumn get kind => text()();
  DateTimeColumn get createdAt => dateTime()();
  /// 见 ChatThreadsV2.ownerKey —— 环境 × 账号隔离键，查询必填过滤。
  TextColumn get ownerKey => text().withDefault(const Constant(''))();
}

/// ChatSyncState —— per-scope 下行同步游标（P1.2 增量 hydrate + 墓碑收敛，
/// 设计 docs/BiuMind-Local-Data-Isolation-Design.md §4）。
/// scope = ChatRepo 同款 ownerKey（sha256(环境)+":"+userId），主键即隔离键。
/// 两个游标都存服务端时间戳的 RFC3339Nano 字符串原样回传，不做本地解析
/// 比较（服务端是时钟权威，本地只透传）。
@DataClassName('LocalChatSyncState')
class ChatSyncState extends Table {
  TextColumn get scope => text()();
  /// GET /v1/threads?updated_after= 的游标（本轮见到的最大 updated_at）。
  /// null = 下次全量 hydrate（首跑 / desync 清游标后）。
  TextColumn get threadsCursor => text().nullable()();
  /// GET /v1/chat/tombstones?since= 的游标（服务端回包的 next_since）。
  /// null = 从 epoch0 首跑（服务端墓碑只保留 30 天，天然有界）。
  TextColumn get tombstoneSince => text().nullable()();

  @override
  Set<Column> get primaryKey => {scope};
}

/// ChatOutbox —— chat 上行写盒（P1.3，照搬 WikiOutbox 范式）：删除 / 归档 /
/// 重命名的上行失败时入队，由 ChatOutboxFlusher 指数退避重试，404 丢 op
/// （目标已不存在 = 幂等收敛）。
///
/// `op` is one of:
///   delete_thread | archive_thread | rename_thread
///
/// `threadId` 是目标会话 id（服务端 thread id = 本地 id，同源）。
/// `payloadJson`：archive_thread 带 {"archived": true}，rename_thread 带
/// {"title": "..."}，delete_thread 空 `{}`。
/// scope = ownerKey；flusher 只 flush 当前登录 scope 的 op。登出不清表 ——
/// 切回账号后续传（P0 scope 隔离保证他账号不可见）。
@DataClassName('ChatOutboxEntry')
class ChatOutbox extends Table {
  IntColumn get id => integer().autoIncrement()();
  TextColumn get scope => text()();
  TextColumn get op => text()();
  TextColumn get threadId => text()();
  TextColumn get payloadJson => text().withDefault(const Constant('{}'))();
  IntColumn get attempts => integer().withDefault(const Constant(0))();
  TextColumn get lastError => text().nullable()();
  DateTimeColumn get createdAt => dateTime()();
  DateTimeColumn get nextAttemptAt => dateTime()();
}

/// ChatSessions —— 一条 thread 当前 / 历史的 brain session。多 turn 可能
/// 是多条 session 串起来。lastSeenSeq 给 S9-1 跨设备 resume 用。
@DataClassName('LocalChatSession')
class ChatSessions extends Table {
  /// brain agent_sessions.session_id（外部 PK）
  TextColumn get sessionId => text()();
  TextColumn get threadId => text()();
  /// 'chat' | 'agent' | 'task'，跟 thread.mode 一致；冗余便于查询
  TextColumn get mode => text()();
  /// 30min session_token；过期前 5min 由 BiuSessionConnection 自动 refresh
  TextColumn get sessionToken => text()();
  DateTimeColumn get tokenExpiresAt => dateTime()();
  /// 客户端已经 ack 过的最大 stream seq；resume 时给 brain ?since_seq=N
  IntColumn get lastSeenSeq => integer().withDefault(const Constant(0))();
  /// 'active' | 'completed' | 'failed' | 'cancelled'
  TextColumn get status => text()();
  DateTimeColumn get createdAt => dateTime()();
  DateTimeColumn get closedAt => dateTime().nullable()();
  /// 见 ChatThreadsV2.ownerKey —— 环境 × 账号隔离键，查询必填过滤。
  TextColumn get ownerKey => text().withDefault(const Constant(''))();
  @override
  Set<Column> get primaryKey => {sessionId};
}

/// RssFeedsCache — RSS 订阅源本地镜像 (M10.1 离线缓存).
///
/// 设计: 存服务端返回的原始 JSON payload (payloadJson), 不逐列镜像 —
/// Entry/Feed 模型加字段时不必跟着改 schema. 索引列只放查询/隔离/TTL
/// 必需的 (id / scopeId / cachedAt). scopeId = JWT sub (用户隔离, 防止
/// 切账号串数据).
@DataClassName('LocalRssFeed')
class RssFeedsCache extends Table {
  TextColumn get id => text()();
  TextColumn get scopeId => text()();
  TextColumn get payloadJson => text()();
  DateTimeColumn get cachedAt => dateTime()();

  @override
  Set<Column> get primaryKey => {id};
}

/// RssEntriesCache — RSS 条目本地镜像. fetchedAt 单列出来供排序 + TTL
/// 裁剪 (30d / 总数 1000 上限); feedId 供按源过滤.
@DataClassName('LocalRssEntry')
class RssEntriesCache extends Table {
  TextColumn get id => text()();
  TextColumn get scopeId => text()();
  TextColumn get feedId => text()();
  TextColumn get payloadJson => text()();
  DateTimeColumn get fetchedAt => dateTime().nullable()();
  DateTimeColumn get cachedAt => dateTime()();

  @override
  Set<Column> get primaryKey => {id};
}

@DriftDatabase(
  tables: [
    WikiProjects, WikiPages, WikiBlocks, WikiOutbox,
    NoteNotebooks, NoteNotes, NoteTags, NoteNoteTags, NoteOutbox,
    CodeTasks, CodeProjects, CodeTaskArtifacts,
    ChatThreadsV2, ChatMessagesV2, ChatContentBlocks, ChatSessions,
    MessageReactionsV2,
    AigcTasks,
    SseCursors,
    RssFeedsCache, RssEntriesCache,
    ChatSyncState, ChatOutbox,
  ],
)
class AppDb extends _$AppDb {
  AppDb() : super(opener.openDatabase());
  AppDb.executor(super.e);

  /// In-memory factory for tests. Routes through the IO opener which
  /// has a [memoryExecutor] entry point — keeps tests independent of
  /// the platform binding.
  factory AppDb.memory() => AppDb.executor(opener.memoryExecutor());

  @override
  int get schemaVersion => 33;

  @override
  MigrationStrategy get migration => MigrationStrategy(
        onCreate: (m) => m.createAll(),
        onUpgrade: (m, from, to) async {
          if (from < 2) {
            // Phase 2: chat tables —— 已删除（R7 cleanup），from < 12 时 DROP。
          }
          if (from < 3) {
            // Phase 3: 编码工作台任务持久化
            await m.createTable(codeTasks);
          }
          if (from < 4) {
            // Phase 4: 编码任务多端同步 outbox + cursor —— 已随 codeSync 废弃
            // (v24 DROP)。这两张表的 Drift 实体已删,故老库升级到这里不再建;
            // 已建过的(version 4-23)由下面 from < 24 块 DROP。
          }
          if (from < 5) {
            // Phase 5: 跨端只读 UI — task 上挂 origin device id + label
            await m.addColumn(codeTasks, codeTasks.originDeviceId);
            await m.addColumn(codeTasks, codeTasks.originDeviceLabel);
          }
          if (from < 6) {
            // Phase 6: artifacts L1 — 任务产物元数据本地表
            await m.createTable(codeTaskArtifacts);
          }
          // Phase 7-9 (chat_threads.execution_mode / model_params_json /
          // message_reactions) 已随 R7 整体丢弃；老库升级到 12 时 from < 12
          // 块统一 DROP。
          if (from < 10) {
            // Phase 10: Chat 重构 v2 —— BiuClient 驱动的新 chat 表。
            // additive：老 chat_threads / chat_messages / chat_outbox /
            // message_reactions 仍存在，让 WIP 老代码继续编译；R7 cleanup
            // 阶段会 drop 老表。
            await m.createTable(chatThreadsV2);
            await m.createTable(chatMessagesV2);
            await m.createTable(chatContentBlocks);
            await m.createTable(chatSessions);
          }
          if (from >= 10 && from < 11) {
            // Phase 11: ChatThreadsV2.project_id —— Wiki 项目内嵌 chat
            // 面板用这个过滤 thread。null 表示全局对话（/chat 路由）。
            //
            // **下界 from >= 10**：from < 10 时上面的 createTable(chatThreadsV2)
            // 已经用当前 schema 建了带 project_id 列的表；这里再 addColumn
            // 会报 duplicate column。只有真的从 schemaVersion=10 升上来的
            // db 才需要补这一列。
            await m.addColumn(chatThreadsV2, chatThreadsV2.projectId);
          }
          if (from < 12) {
            // Phase 12: R7 cleanup —— DROP 老 chat 表。用 IF EXISTS 防止
            // 极老 / 半失败 db 表不存在导致整个 migration 卡住。
            await m.database.customStatement('DROP TABLE IF EXISTS chat_threads');
            await m.database.customStatement('DROP TABLE IF EXISTS chat_messages');
            await m.database.customStatement('DROP TABLE IF EXISTS message_reactions');
            await m.database.customStatement('DROP TABLE IF EXISTS chat_outbox');
          }
          if (from < 13) {
            // Phase 13: Chat UI Benchmark P0-1 —— 消息收藏 / Reaction 表 v2。
            // 表名 message_reactions_v2 避开历史 message_reactions 名（v12 已 DROP）。
            await m.createTable(messageReactionsV2);
          }
          if (from < 14) {
            // Phase 14: ChatThreadsV2 + workdir / autoApprove —— 让 Agent
            // 模式的"行内可切换"UX 有数据层支撑。后端对应 brain
            // migration 00038_chat_threads_workdir_approve.sql。
            await m.addColumn(chatThreadsV2, chatThreadsV2.workdir);
            await m.addColumn(chatThreadsV2, chatThreadsV2.autoApprove);
          }
          if (from < 15) {
            // Phase 15: ChatThreadsV2.providerId —— 让 picker 选模型时一
            // 并锁 provider 路由(Anthropic 的 claude-sonnet-4-6 跟 BiuMind
            // Cloud 的同名 model 不再混用)。后端对应 brain migration
            // 00039_chat_threads_provider_id.sql。
            await m.addColumn(chatThreadsV2, chatThreadsV2.providerId);
          }
          if (from < 16) {
            // Phase 16: AIGC 创作任务持久化 (v2-1) — 重启 app 后任务列表
            // + 状态恢复. 不持久化产物 url (cas:sha 是云端引用), 仅元数据 +
            // outputs JSON.
            await m.createTable(aigcTasks);
          }
          if (from < 17) {
            // Phase 17: SSE Last-Event-ID 续传游标 (v2-4) — RealtimeHub
            // 重启后从 cursor 续接, 配合服务端 sse.go 的 last-event-id replay.
            await m.createTable(sseCursors);
          }
          if (from < 18) {
            // Phase 18: ChatThreadsV2.runtimeEnvMode —— 工具执行环境(轴 B,
            // none/local/cloud),与 mode 正交。后端对应 brain migration
            // 00040_chat_runtime_env_mode.sql(agent_sessions.runtime_env_mode)。
            await m.addColumn(chatThreadsV2, chatThreadsV2.runtimeEnvMode);
          }
          if (from < 19) {
            // Phase 19: ChatThreadsV2.backend —— agent loop backend
            // (biumindkit/claude-cli/codex-cli)。Runtime v3 R3/Q3 外部 CLI
            // backend。后端对应 brain migration 00041_agent_sessions_backend.sql。
            await m.addColumn(chatThreadsV2, chatThreadsV2.backend);
          }
          if (from < 20) {
            // Phase 20: 编码模块 M1 多项目 —— code_projects 表 + code_tasks 挂
            // project_id / updated_at。零云同步,Drift 即 SoT(无对应后端 migration)。
            await m.createTable(codeProjects);
            await m.addColumn(codeTasks, codeTasks.projectId);
            await m.addColumn(codeTasks, codeTasks.updatedAt);
          }
          if (from < 21) {
            // Phase 21: code_projects.sort_index —— ProjectRail 拖拽排序持久化。
            await m.addColumn(codeProjects, codeProjects.sortIndex);
          }
          if (from < 22) {
            // Phase 22: RSS 离线缓存 (v3 M10.1) — feeds/entries 本地镜像,
            // 杀进程重启后秒显上次列表. 纯本地 cache, 后端 SoT 不变;
            // 无对应后端 migration.
            await m.createTable(rssFeedsCache);
            await m.createTable(rssEntriesCache);
          }
          if (from < 23) {
            // Phase 23: 编码任务 per-task 模型选择 (M4)。code_tasks.model 可空列。
            // 仅本地 (codeSync 已废弃 D4/Code-I6,无对应后端 migration)。
            await m.addColumn(codeTasks, codeTasks.model);
          }
          if (from < 24) {
            // Phase 24: codeSync 死代码清理 (D4/Code-I6) —— DROP 编码同步 outbox +
            // cursor 表。编码任务 100% 本地,无云同步。IF EXISTS 防极老 db 卡住。
            await m.database.customStatement('DROP TABLE IF EXISTS code_task_outbox');
            await m.database.customStatement('DROP TABLE IF EXISTS code_sync_cursors');
          }
          if (from < 25) {
            // Phase 25: artifacts-sync 收尾 (D4/Code-I4/Code-I6) —— 删产物 L3 云上传字段。
            // 产物 100% 本地。SQLite ≥3.35 支持 DROP COLUMN(sqlite3_flutter_libs 远超此版)。
            await m.database
                .customStatement('ALTER TABLE code_task_artifacts DROP COLUMN cloud_file_id');
            await m.database
                .customStatement('ALTER TABLE code_task_artifacts DROP COLUMN cloud_uploaded_at');
          }
          if (from < 26) {
            // Phase 26: 任务星标 (CORE-2)。code_tasks.starred 带默认 false。仅本地。
            await m.addColumn(codeTasks, codeTasks.starred);
          }
          if (from < 27) {
            // Phase 27: 多端聊天同步 (P0/P1) —— chat_threads_v2.remote_updated_at_us
            // 存服务端 updated_at 的微秒整数,做精确的增量比较基准(updatedAt 列
            // 被 Drift 截断到秒,同秒多次服务端更新会漏拉)。仅本地,后端对应
            // brain threadOut RFC3339Nano 序列化。
            await m.addColumn(chatThreadsV2, chatThreadsV2.remoteUpdatedAtUs);
          }
          if (from < 28) {
            // Phase 28: 笔记功能 N1 (docs/BiuMind-Notes-Design-Draft.md §5) ——
            // 个人笔记域本地镜像 + 独立 outbox（O2 先复制后收敛）。
            // 后端对应 brain migration 00052–00055（note_notebooks /
            // note_notes / note_tags+note_note_tags / note_attachments；
            // 附件 N2 才做，本地暂不落表）。
            await m.createTable(noteNotebooks);
            await m.createTable(noteNotes);
            await m.createTable(noteTags);
            await m.createTable(noteNoteTags);
            await m.createTable(noteOutbox);
          }
          if (from < 29 && from >= 28) {
            // Phase 29: 笔记 N3 归档/转知识库 —— note_notes 加 archived_at +
            // promoted_page_id（可空）。归档笔记不进默认列表（对齐服务端
            // GET /v1/notes 默认排除归档）。
            // 仅 v28 老库需要 ALTER；from < 28 时上面的 createTable 已按
            // 当前表结构（含这两列）建表，再 addColumn 会 duplicate column。
            await m.addColumn(noteNotes, noteNotes.archivedAt);
            await m.addColumn(noteNotes, noteNotes.promotedPageId);
          }
          if (from < 30) {
            // Phase 30: P0 本地数据隔离（docs/BiuMind-Local-Data-Isolation-Design.md
            // §3）—— chat 五表加 ownerKey scope 列，并清空全部存量行。
            //
            // 清空原因：存量行没有归属信息（本身就是跨账号泄露源），禁止「猜
            // 归属」；服务端有权威副本，清空后下一次全量 hydrate 无感恢复。
            //
            // 下界防 duplicate column：chatThreadsV2/chatMessagesV2/
            // chatContentBlocks/chatSessions 在 from < 10 时走 createTable 已
            // 含 ownerKey 列（当前 schema）；messageReactionsV2 同理于 from < 13。
            if (from >= 10) {
              await m.addColumn(chatThreadsV2, chatThreadsV2.ownerKey);
              await m.addColumn(chatMessagesV2, chatMessagesV2.ownerKey);
              await m.addColumn(chatContentBlocks, chatContentBlocks.ownerKey);
              await m.addColumn(chatSessions, chatSessions.ownerKey);
            }
            if (from >= 13) {
              await m.addColumn(messageReactionsV2, messageReactionsV2.ownerKey);
            }
            await m.database.customStatement('DELETE FROM chat_threads_v2');
            await m.database.customStatement('DELETE FROM chat_messages_v2');
            await m.database.customStatement('DELETE FROM chat_content_blocks');
            await m.database.customStatement('DELETE FROM chat_sessions');
            await m.database.customStatement('DELETE FROM message_reactions_v2');
          }
          if (from < 31) {
            // Phase 31: P1.2 增量 hydrate + 墓碑收敛（设计文档 §4）——
            // per-scope 下行同步游标表（threadsCursor / tombstoneSince）。
            // 纯新表，无存量数据迁移。
            await m.createTable(chatSyncState);
          }
          if (from < 32) {
            // Phase 32: P1.3 chat 上行 outbox（删除/归档/重命名失败重试）。
            // 纯新表；注意这是重建的 chat_outbox（v12 DROP 过 v1 老表，
            // 表名相同但 schema 无关，IF EXISTS 已保证老表不在）。
            await m.createTable(chatOutbox);
          }
          if (from < 33) {
            // Phase 33: 笔记域 P0 本地数据隔离（对齐 Phase 30 给 chat 做的
            // ownerKey 隔离，docs/BiuMind-Local-Data-Isolation-Design.md §2/§3）。
            // note_notes / note_notebooks / note_tags / note_note_tags /
            // note_outbox 五表加 ownerKey scope 列，并清空全部存量行。
            //
            // 根因：笔记表此前无任何用户隔离（chat 在 Phase 30 已修，笔记被
            // 漏掉），本地持久化的笔记不按账号过滤、登出也不清——重新部署 +
            // 重新注册登录后，桌面端会把上一账号/上一套部署的笔记直接展示给
            // 新账号（跨账号泄露）。存量行没有归属信息（本身就是泄露源），
            // 禁止「猜归属」；服务端有权威副本，清空后下一次 hydrate 无感恢复。
            //
            // 下界防 duplicate column：from < 28 时 Phase 28 的 createTable
            // 已按当前 schema（含 ownerKey）建表，再 addColumn 会报重复。
            if (from >= 28) {
              await m.addColumn(noteNotes, noteNotes.ownerKey);
              await m.addColumn(noteNotebooks, noteNotebooks.ownerKey);
              await m.addColumn(noteTags, noteTags.ownerKey);
              await m.addColumn(noteNoteTags, noteNoteTags.ownerKey);
              await m.addColumn(noteOutbox, noteOutbox.ownerKey);
            }
            await m.database.customStatement('DELETE FROM note_notes');
            await m.database.customStatement('DELETE FROM note_notebooks');
            await m.database.customStatement('DELETE FROM note_tags');
            await m.database.customStatement('DELETE FROM note_note_tags');
            await m.database.customStatement('DELETE FROM note_outbox');
          }
        },
      );
}
