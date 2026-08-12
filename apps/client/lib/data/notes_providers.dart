// Riverpod providers for the Notes data stack.
//
// AppDb → NotesDao → NotesRepository (with NotesClient)
//       → NoteOutboxFlusher (auto-start) + NotesSyncPoller (auto-start)。
// 镜像 wiki_providers 的接线：全部 provider 跟随 hubCredentialsProvider
// 重建，切工作区/登出时整栈重置。AppDb 单例复用 wiki_providers 里的
// appDbProvider（跨域共享同一 sqlite 文件）。
//
// flusher ↔ poller 协调（设计决策 N1：简单即可）：flusher 成功冲刷后
// 回调 poller.kick() 拉一轮 changes，把事件流里的回声/他端变更及时
// 落进 Drift。poller provider 里完成这个接线（flusher 先建好，poller
// 持有 flusher 引用挂回调，避免循环依赖）。

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../features/chat/data/chat_scope.dart' show chatOwnerScopeProvider;
import '../services/auth_service.dart';
import 'api/notes_client.dart' as api;
import 'local/notes_dao.dart';
import 'notes_repository.dart';
import 'notes_sync.dart';
import 'outbox/note_outbox_flusher.dart';
import 'wiki_providers.dart' show appDbProvider;

/// 当前登录态的 notes DAO；未登录 / 无法派生 scope 时为 null。
///
/// scope 直接复用 chat 的 ownerKey（[chatOwnerScopeProvider]）—— 同一登录
/// 态下所有本地域共用一把「环境 × 账号」隔离键（sha256(环境)+":"+userId），
/// 笔记与 chat 切换账号 / 环境时行为一致。
final notesDaoProvider = Provider<NotesDao?>((ref) {
  final scope = ref.watch(chatOwnerScopeProvider);
  if (scope == null) return null;
  return NotesDao(ref.watch(appDbProvider), scope: scope);
});

/// Repository — null when no hub credentials are configured, or when the
/// owner scope can't be derived (e.g. token 非 JWT）。无 scope 则不读写
/// 本地库，避免跨账号串写（见 notes_dao.dart 顶部 P0 隔离说明）。
final notesRepositoryProvider = Provider<NotesRepository?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  final dao = ref.watch(notesDaoProvider);
  if (dao == null) return null;
  final client = api.NotesClient(creds.endpoint, creds.bearerToken);
  final repo = NotesRepository(
    dao: dao,
    client: client,
  );
  // 历史 'local-<uuid>' 占位笔记恢复：对没有待冲刷 create op 的孤儿行
  // 补一条 create_note 让其同步上服务端。fire-and-forget（只入本地
  // outbox，不触网），失败不阻塞 repository 可用；provider 随 credentials
  // 重建时才重跑，正常会话内只跑一次。
  unawaited(repo.recoverOrphanedLocalNotes());
  return repo;
});

/// Outbox flusher — auto-starts when a repository becomes available, stops
/// (and disposes) when credentials are cleared.
final noteOutboxFlusherProvider = Provider<NoteOutboxFlusher?>((ref) {
  final repo = ref.watch(notesRepositoryProvider);
  if (repo == null) return null;
  final flusher = NoteOutboxFlusher(dao: repo.dao, client: repo.client);
  // 409 三方合并「无冲突」时把合并文当新本地编辑写回（base 已 = remote，
  // 入队 update_note baseVersion=remoteVersion，下轮 flush 落库）。
  flusher.onAutoMergeResolved = (id, md) => repo.updateNote(id, contentMd: md);
  flusher.start();
  ref.onDispose(flusher.dispose);
  return flusher;
});

/// Changes 轮询器 —— N1 用轮询代替 WS（服务端 N0 未做 /v1/notes/sync）。
/// 启动后立刻拉一轮全量增量（since 游标续传），之后每 15s 一轮；
/// flusher 冲刷成功也会踢它一脚。
final notesSyncPollerProvider = Provider<NotesSyncPoller?>((ref) {
  final repo = ref.watch(notesRepositoryProvider);
  if (repo == null) return null;
  final poller = NotesSyncPoller(
    db: ref.watch(appDbProvider),
    repository: repo,
  )..start();
  // flusher 成功 flush 后触发一次 pull（协调放这里，避免 flusher 反向
  // 依赖 poller 造成 provider 循环）。
  ref.watch(noteOutboxFlusherProvider)?.onFlushSuccess = poller.kick;
  ref.onDispose(poller.dispose);
  return poller;
});

/// Live count of pending note outbox entries — UI shows it as a
/// "syncing" badge. 无登录态（dao null）时恒为空流。
final notesPendingWriteCountProvider = StreamProvider<int>((ref) {
  final dao = ref.watch(notesDaoProvider);
  if (dao == null) return const Stream.empty();
  return dao.watchOutboxCount();
});
