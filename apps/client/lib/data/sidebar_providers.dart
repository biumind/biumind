// Riverpod providers for the sidebar layout. Mirrors apps_providers.dart:
//   - sidebarClientProvider null when model-relay creds are absent
//   - layoutProvider.family(scope) auto-fetches via SWR + 本地 outbox
//     乐观 merge (设计 §10A.12 离线编辑)
//   - togglePinnedApp / reorderPinnedApp 网络错误时降级写 outbox

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../services/auth_service.dart';
import 'api/_http_helpers.dart' show ApiError;
import 'api/sidebar_client.dart';
import 'apps_providers.dart' show appsBearerProvider;
import 'sidebar_outbox.dart';

final sidebarClientProvider = Provider<SidebarClient?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  // 单 origin: /v1/sidebar/* 由 site nginx 反代到 app_center, 不换端口.
  return SidebarClient(creds.endpoint);
});

final sidebarLayoutProvider = FutureProvider.family<SidebarLayout?, String>(
  (ref, scope) async {
    // select(endpoint): token 轮换不重拉 —— sidebar 常驻可见, 不轮换闪。
    // token 经 ref.read 现读 (轮换后新鲜)。
    ref.watch(sidebarClientProvider.select((c) => c?.baseUrl));
    final client = ref.read(sidebarClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) return null;
    final fetched = await client.get(scope: scope, token: token);

    // 乐观 merge: 本地 outbox 有 pending edit 就用 pending 覆盖 fetched
    // 的 items 显示给 UI (服务端 version 保留, 给后续 flush PUT 用)。
    // 这是 §10A.12 "客户端编辑后立刻乐观应用本地" 的客户端落地。
    final pending = await ref.read(sidebarOutboxProvider).load(scope);
    if (pending == null) return fetched;
    return SidebarLayout(
      scope: fetched.scope,
      items: pending.items,
      version: fetched.version,
      updatedAt: pending.queuedAt,
      updatedByDevice: 'pending-local',
    );
  },
);

/// 切换 install 的 pinned 状态 —— 返回操作后是否 pinned。
///
/// 设计 §10A.3 / §10A.5："固定到侧边栏 / 取消固定" 的核心动作；
/// AppTile 右键菜单、安装 Toast 的 "添加到侧边栏" 按钮都走这条。
///
/// 失败/缺凭据场景返回 null —— 调用方据此显示 "请先在设置中配置 Hub"。
/// 第一次 PUT 遇 409 SidebarConflict 自动 GET 最新 + 重试一次; 第二次
/// 仍冲突或 ApiError 向上抛 (UI 走 humanizeAppsError)。**网络层错误**
/// (断网/超时/DNS) 写 outbox 静默成功, flush 由启动 + Realtime listener
/// 触发 —— 用户编辑不丢 (§10A.12)。
Future<bool?> togglePinnedApp(
  WidgetRef ref, {
  required String installId,
  String scope = 'desktop',
}) async {
  final client = ref.read(sidebarClientProvider);
  final token = ref.read(appsBearerProvider);
  if (client == null || token == null) return null;

  Future<bool?> attempt() async {
    final layout = await ref.read(sidebarLayoutProvider(scope).future);
    if (layout == null) return null;

    final items = [...layout.items];
    final idx = items.indexWhere((i) => i.kind == 'app' && i.ref == installId);
    final nowPinned = idx == -1;
    if (nowPinned) {
      items.add(SidebarItem(kind: 'app', ref: installId));
    } else {
      items.removeAt(idx);
    }
    await putSidebarOrQueue(
      ref: ref,
      client: client,
      token: token,
      scope: scope,
      items: items,
      expectedVersion: layout.version,
    );
    ref.invalidate(sidebarLayoutProvider);
    return nowPinned;
  }

  try {
    return await attempt();
  } on SidebarConflict {
    // 另一设备并发改了 layout —— 拉最新版本后再来一次。
    // 第二次仍冲突说明用户高频并发, 上抛让 UI 提示。
    ref.invalidate(sidebarLayoutProvider);
    return await attempt();
  }
}

/// PUT 结果: 成功落服务端 vs 网络降级到 outbox。
enum SidebarPutResult { ok, queuedOffline }

/// PUT layout, 网络错误降级写 outbox; 返回 [SidebarPutResult] 让调用
/// 方据此选 UX 文案。SidebarConflict / ApiError 上抛 (冲突 retry / UI
/// 错误提示)。
///
/// 成功后清掉同 scope 的 outbox — 服务端已经收下, 之前 pending 不必再
/// flush; 重新 GET 拿到的就是真值。
Future<SidebarPutResult> putSidebarOrQueue({
  required WidgetRef ref,
  required SidebarClient client,
  required String token,
  required String scope,
  required List<SidebarItem> items,
  required int expectedVersion,
}) async {
  try {
    await client.put(
      scope: scope,
      items: items,
      expectedVersion: expectedVersion,
      device: 'desktop-client',
      token: token,
    );
    await ref.read(sidebarOutboxProvider).clear(scope);
    return SidebarPutResult.ok;
  } on SidebarConflict {
    rethrow;
  } on ApiError {
    rethrow;
  } catch (_) {
    // 非 ApiError 的异常 ≈ 网络层 (SocketException / ClientException /
    // TimeoutException) → 落 outbox 后续 flush, 不向上抛。
    await ref.read(sidebarOutboxProvider).save(PendingSidebarEdit(
      scope: scope,
      items: items,
      expectedVersion: expectedVersion,
      queuedAt: DateTime.now().toUtc(),
    ));
    return SidebarPutResult.queuedOffline;
  }
}

/// 尝试把 outbox 里 [scope] 的 pending edit PUT 出去。
///
/// - 没 pending → no-op 返回 false
/// - PUT 200 → clear outbox + invalidate provider, 返回 true
/// - PUT 409 → 服务端已有更新, 当作"被覆盖"清 outbox + invalidate (UI
///   会通过 sidebarChangeNoticesProvider 收到 reload 通知); 返回 false
/// - PUT 仍是网络错误 → 保留 outbox 等下次 flush
/// - ApiError (其他状态) → 同样保留 outbox, 但日志可见
///
/// 调用时机: app 启动一次 + Realtime listener start() / reconnect。
Future<bool> flushSidebarOutbox(
  Ref ref, {
  String scope = 'desktop',
}) async {
  final outbox = ref.read(sidebarOutboxProvider);
  final pending = await outbox.load(scope);
  if (pending == null) return false;

  final client = ref.read(sidebarClientProvider);
  final token = ref.read(appsBearerProvider);
  if (client == null || token == null) return false;

  try {
    await client.put(
      scope: pending.scope,
      items: pending.items,
      expectedVersion: pending.expectedVersion,
      device: 'desktop-client',
      token: token,
    );
    await outbox.clear(scope);
    ref.invalidate(sidebarLayoutProvider);
    return true;
  } on SidebarConflict {
    // 服务端已经被其他设备改写。本地 pending 失效 — 清掉, 让用户在
    // 新版本基础上重新决定 (Realtime listener 会触发 reload 通知)。
    await outbox.clear(scope);
    ref.invalidate(sidebarLayoutProvider);
    return false;
  } catch (_) {
    // 还是网络错误 — 保留 pending 等下次 flush。
    return false;
  }
}

/// 判断指定 install 是否已固定到 sidebar。基于已缓存的 layout，
/// 不会触发新请求；layout 未就绪时返回 false。
///
/// 注意: 仅在 widget build 上下文内调用 (订阅 layout 变化触发 rebuild)。
/// callback / Future 上下文请用 [isAppPinnedNow]。
bool isAppPinned(WidgetRef ref, String installId, {String scope = 'desktop'}) {
  final layout = ref.watch(sidebarLayoutProvider(scope)).valueOrNull;
  if (layout == null) return false;
  return layout.items.any((i) => i.kind == 'app' && i.ref == installId);
}

/// 同 [isAppPinned] 但不订阅 —— 给 callback / async 上下文用。
bool isAppPinnedNow(WidgetRef ref, String installId, {String scope = 'desktop'}) {
  final layout = ref.read(sidebarLayoutProvider(scope)).valueOrNull;
  if (layout == null) return false;
  return layout.items.any((i) => i.kind == 'app' && i.ref == installId);
}

/// 侧边栏 pinned app 行右键快捷菜单的语义动作 (设计 §10A.3 表格)。
enum SidebarReorder { top, up, down, bottom }

/// 在 pinned apps 子序列内调整 [installId] 的位置。app 之间相对顺序变,
/// 不影响 system 项位置 (仍占用原 index)。
///
/// 返回 true = 真的改了; false = 无操作 (已经在边上); null = 缺凭据。
Future<bool?> reorderPinnedApp(
  WidgetRef ref, {
  required String installId,
  required SidebarReorder action,
  String scope = 'desktop',
}) async {
  final client = ref.read(sidebarClientProvider);
  final token = ref.read(appsBearerProvider);
  if (client == null || token == null) return null;

  Future<bool?> attempt() async {
    final layout = await ref.read(sidebarLayoutProvider(scope).future);
    if (layout == null) return null;

    final items = [...layout.items];
    final appIdx = <int>[];
    for (var i = 0; i < items.length; i++) {
      if (items[i].kind == 'app') appIdx.add(i);
    }
    final appRows = [for (final i in appIdx) items[i]];
    final myIdx = appRows.indexWhere((r) => r.ref == installId);
    if (myIdx == -1) return null;

    var newIdx = myIdx;
    switch (action) {
      case SidebarReorder.top:
        newIdx = 0;
      case SidebarReorder.bottom:
        newIdx = appRows.length - 1;
      case SidebarReorder.up:
        newIdx = (myIdx - 1).clamp(0, appRows.length - 1);
      case SidebarReorder.down:
        newIdx = (myIdx + 1).clamp(0, appRows.length - 1);
    }
    if (newIdx == myIdx) return false;

    final moved = appRows.removeAt(myIdx);
    appRows.insert(newIdx, moved);
    for (var k = 0; k < appIdx.length; k++) {
      items[appIdx[k]] = appRows[k];
    }

    await putSidebarOrQueue(
      ref: ref,
      client: client,
      token: token,
      scope: scope,
      items: items,
      expectedVersion: layout.version,
    );
    ref.invalidate(sidebarLayoutProvider);
    return true;
  }

  try {
    return await attempt();
  } on SidebarConflict {
    ref.invalidate(sidebarLayoutProvider);
    return await attempt();
  }
}
