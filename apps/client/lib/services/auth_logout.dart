// auth_logout — 用户登出时本地数据清理编排.
//
// 设计: signOut 有 4 条调用路径 (3 UI + token_manager 自动踢人). 清理下沉到
// purgeUserData, settingsController.signOut 在 clearTokens 前调, 4 路径统一.
//
// 范围 (见 plan + 代码事实):
//   ✅ 缓存性质 DAO (云端 SoT): wiki.wipe / aigc.deleteAll / sse.clearAll
//   ✅ sidebar outbox 待提交编辑 (不清则下个用户 flush 会把上个用户的编辑 PUT
//      进自己账号)
//   ❌ code DAO 不清 — code_projects_dao.dart:3 「零云同步: Drift 是唯一
//      真相源」, 清了永久丢用户代码. 手动清走设置页「清空本地数据」.
//   ❌ rss 不清 — rss_cache_dao.dart:6 按 scopeId=JWT sub 隔离, 切账号天然
//      不串 (where scopeId.equals), 不清也安全.
//   ❌ chat / notes 五表不清 — P0 数据隔离后每行带 ownerKey
//      (sha256(环境)+":"+userId) scope 列, ChatRepo / NotesDao 构造绑定
//      scope、全部读写强制过滤 (chat_repo.dart / notes_dao.dart), 下个账号
//      天然不可见; 保留支持切回账号秒开 (设计文档
//      docs/BiuMind-Local-Data-Isolation-Design.md §1 公理 3).
//      notes 复用 chat 的 chatOwnerScopeProvider, 同一登录态共一把隔离键
//      (v33 Phase 33).
//   🔒 installationId / deviceId / identityUrl / UI prefs 保留 — clearTokens
//      语义已对 (C3 跨登出持久).
//
// provider 缓存 (creditsBalance / threads / biuDaemonManager / fileBytesCache
// 等) **不手动 invalidate**: clearTokens 改 settings state → hubCredentials
// (creds) null → 所有依赖 creds 的 provider 自动 rebuild + 旧实例 dispose
// (daemon ref.onDispose → SIGTERM; fileBytesCache 旧实例 memory 随 GC 清).
// 且在 settingsController 的 ref 里 invalidate 依赖 settingsController 的
// provider 会触发 CircularDependencyError — 响应式已覆盖, 无需也禁止手清.

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/local/wiki_dao.dart' show WikiDao;
import '../data/sse/sse_cursors_dao.dart' show SseCursorsDao;
import '../data/sidebar_outbox.dart' show sidebarOutboxProvider;
import '../data/wiki_providers.dart' show appDbProvider;
import '../features/creation/data/aigc_tasks_dao.dart' show AigcTasksDao;

/// 登出时清本地用户数据 (Drift / SecureStorage 缓存).
///
/// 在 [SettingsController.signOut] 的 clearTokens **之前**调. provider 缓存
/// 由 clearTokens 触发的响应式自动失效 (creds null → 依赖链 rebuild), 不在
/// 此手动清 (会触发 CircularDependencyError).
Future<void> purgeUserData(Ref ref) async {
  final db = ref.read(appDbProvider);

  // wiki 本地镜像 (文档 / 页面 / 图谱) — brain 是 SoT, 清了重新 sync.
  await WikiDao(db).wipe();
  // AIGC 任务 / 作品本地表 — aigc 服务 SoT.
  await AigcTasksDao(db).deleteAll();
  // Realtime SSE last-event-id cursor (多 scope 共用表) — 防下个用户续接
  // 用上个用户的 cursor.
  await SseCursorsDao(db).clearAll();
  // sidebar 待提交编辑 (desktop scope) — 不清则下个用户 flush 把上个用户
  // 的 sidebar 编辑 PUT 进自己账号 (定制是桌面功能, 手机端 R1.6 移除, 故
  // 只 'desktop' scope)。P2 起 outbox storage key 带 ownerKey namespace,
  // 这里 clear 落的是当前账号的 pending; switchAccount 路径不经 purge,
  // 靠 namespace 天然隔离。
  await ref.read(sidebarOutboxProvider).clear('desktop');
}
