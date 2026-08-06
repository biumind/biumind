// TokenManager — 集中处理 access_token 刷新逻辑。
//
// 三个职责：
//   refreshNow()    立即刷新；并发 caller 共享同一次 inflight Future
//   handle401()     业务请求 401 时调，返回新 token (retry 用) 或 null
//   refresh 失败 → 视失败类型决定是否 signOut
//
// 与 [TokenRefresher] 的关系：refresher 是后台 30s tick 的 proactive
// 刷新（提前 60s 余量）；TokenManager 是 reactive — 业务 401 / 请求前
// 预检 / AppLifecycle resume 等"事件触发"刷新。两者共享 inflight 锁
// 避免双重刷新。
//
// **三态返回**(BiuMind-Identity-Session-Design §3.5):
//   ok        — 刷新成功
//   expired   — refresh_token 真的死了 (Identity 返 401/403);必须 signOut
//   transient — 网络错 / 5xx / TLS / DNS / Identity 不可达;**保留 token**,
//               让上层显示离线状态 / 业务侧重试,绝不踢人
//
// 历史背景:之前 refreshNow 返回 bool,handle401 在 false 时一律强制
// signOut — 网络抖动 / Identity 偶尔重启就把用户踢回登录页,dev 体验
// 极差。三态化后只有真 401/403 才 signOut。

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:logging/logging.dart';

import '../data/api/identity_client.dart';
import '../features/settings/application/settings_controller.dart';
import 'settings_repo.dart';

final _log = Logger('biumind.token_manager');

/// 刷新结果三态。详见 file-level doc。
enum RefreshOutcome {
  /// 刷新成功,新 token 已写回 settings。
  ok,

  /// refresh_token 真的失效了 (Identity 返 401/403),已自动 signOut。
  expired,

  /// 瞬时错误 (网络 / 5xx / Identity 不可达),保留旧 token,下次 timer / 401 重试。
  transient,
}

/// 客户端 ↔ Identity 的连通状态(B1 离线 grace,Design §3.5)。
///
/// 状态机驱动 UI 的 OfflineBadge 与写操作 gate:
///
///   online           — 一切正常
///   reconnecting     — refresh transient 失败但 access_token 还在有效期内,
///                      业务请求仍能用,后台 refresher 正在重试。UI 标黄
///                      "重连中..." 但不禁用写操作。
///   offlineWithCache — refresh transient 失败 + access_token 已过期 →
///                      authed 请求会 401。UI 标灰 "离线 — 历史可读",
///                      写操作禁用,只读缓存内容。
enum ConnectivityState { online, reconnecting, offlineWithCache }

/// 全局连通状态。TokenManager 在 _doRefresh 成功/失败时更新;UI 监听
/// 渲染 OfflineBadge + 决定写按钮 enabled。
final connectivityStateProvider = StateProvider<ConnectivityState>(
  (_) => ConnectivityState.online,
);

/// 测试 seam — 给 TokenManager 注入假的 refresh 实现。生产代码用默认值。
typedef RefreshFn =
    Future<IdentityAuthResult> Function(
      String identityUrl,
      String refreshToken,
    );

Future<IdentityAuthResult> _defaultRefresh(String url, String token) =>
    IdentityClient(Uri.parse(url)).refresh(token);

class TokenManager {
  TokenManager(this._ref, {RefreshFn? refreshFn})
    : _refreshFn = refreshFn ?? _defaultRefresh;
  final Ref _ref;
  final RefreshFn _refreshFn;

  /// 单次刷新 inflight 锁。多个并发 caller 共享同一个 Future，避免短时间
  /// 内向 Identity 发出多次 refresh 请求。
  Future<RefreshOutcome>? _inflight;

  /// 立即刷新 access_token。
  Future<RefreshOutcome> refreshNow() {
    final inflight = _inflight;
    if (inflight != null) return inflight;
    final fut = _doRefresh();
    _inflight = fut;
    fut.whenComplete(() {
      if (identical(_inflight, fut)) _inflight = null;
    });
    return fut;
  }

  /// 仅当 access_token 接近过期 (或已过期) 时才刷新，否则 noop。
  ///
  /// app resume 时调用 (替代盲调 refreshNow) —— 避免 macOS App Nap / iOS
  /// background 把后台 refresher timer 暂停后, resume 一律盲刷的过度触发。
  /// 后台 TokenRefresher 已在管常规轮换 (提前 60s margin)；这里只兜底
  /// "resume 时 token 真的快没了 / 已过期" 的场景。
  ///
  /// [threshold] 默认 5min：距过期 <5min 或已过期才刷；否则判定 token 仍
  /// 可用, 返回 ok 不发请求。覆盖 App Nap 长时间后台 → token 漂过期的场景
  /// (那时 remaining ≤ 0 < threshold → 触发刷新)。
  Future<RefreshOutcome> refreshIfNearExpiry({
    Duration threshold = const Duration(minutes: 5),
  }) {
    final settings = _ref.read(settingsControllerProvider).valueOrNull;
    final expiryRaw = settings?.tokenExpiresAt;
    // 没 token / 未登录 / 无过期信息 —— 交给 refreshNow 走它的判空逻辑。
    if (expiryRaw == null || expiryRaw.isEmpty) return refreshNow();
    final expiry = DateTime.tryParse(expiryRaw)?.toUtc();
    if (expiry == null) return refreshNow();
    final remaining = expiry.difference(DateTime.now().toUtc());
    if (remaining > threshold) {
      // 离过期还早, 后台 refresher 在管 —— 不刷。
      return Future.value(RefreshOutcome.ok);
    }
    return refreshNow();
  }

  Future<RefreshOutcome> _doRefresh() async {
    final settings = _ref.read(settingsControllerProvider).valueOrNull;
    if (settings == null) return RefreshOutcome.transient;

    final identityUrl = settings.identityUrl;
    final memoryRt = settings.refreshToken;
    if (identityUrl == null ||
        identityUrl.isEmpty ||
        memoryRt == null ||
        memoryRt.isEmpty) {
      _log.fine('no identity URL / refresh_token — nothing to refresh');
      return RefreshOutcome.transient;
    }

    // compare-and-use: 磁盘上的 refresh_token 可能比内存新 — 桌面多实例
    // (正式版 + flutter run 共用同一份 settings 存储) 时, 别的实例已轮换,
    // 内存里的旧 rt 刷新必 401。disk 非空且与内存不同 → 用 disk 的 rt。
    var refreshTok = memoryRt;
    try {
      final disk = await _ref.read(settingsRepoProvider).load();
      final diskRt = disk.refreshToken;
      if (diskRt != null && diskRt.isNotEmpty && diskRt != refreshTok) {
        _log.info(
          'disk refresh_token differs from memory — '
          'using disk (rotated by another instance)',
        );
        refreshTok = diskRt;
      }
    } catch (_) {
      /* repo 读失败按内存值继续 */
    }

    try {
      _log.info('refreshing access token');
      final r = await _refreshFn(identityUrl, refreshTok);
      await _ref
          .read(settingsControllerProvider.notifier)
          .applyRefreshed(
            accessToken: r.accessToken,
            refreshToken: r.refreshToken,
            tokenExpiresAt: r.expiresAt.toIso8601String(),
            accessTtlSeconds: r.expiresInSeconds,
            refreshTokenExpiresAt: r.refreshExpiresAt?.toIso8601String(),
            sessionId: r.sessionId.isEmpty ? null : r.sessionId,
          );
      _log.info('access token refreshed; expires ${r.expiresAt}');
      _setConnectivity(ConnectivityState.online);
      return RefreshOutcome.ok;
    } on IdentityApiError catch (e) {
      _log.warning('refresh failed (${e.status}): ${e.body}');
      // refresh_token 真死了 → 强制下线 + 通知 UI 弹"会话过期"对话框
      if (e.status == 401 || e.status == 403) {
        // 区分 reuse detection (token 被盗或并发竞争) 与普通 expired:
        // server 给 'token_reuse' code 时 UI 应该提示用户改密。
        final reason = e.code == 'token_reuse'
            ? SessionExpiredReason.tokenReuse
            : SessionExpiredReason.expired;
        // compare-and-clear: signOut 内部先对盘 — 若别的实例已写入新凭证
        // 则收编磁盘值(不清盘), 返回 false。只有真的清盘了才 bump 计数器
        // 弹"会话过期"对话框 — 收编路径说明会话其实还活着。
        final signedOut = await _ref
            .read(settingsControllerProvider.notifier)
            .signOut(compareAndClear: true);
        if (signedOut) _markSessionExpired(reason);
        return RefreshOutcome.expired;
      }
      // 其他 4xx (例如 400 bad_request) 当 transient — 通常是协议变更
      // 或 Identity 配置问题,不是用户的会话死了
      _setTransientConnectivity();
      return RefreshOutcome.transient;
    } catch (e, st) {
      // 网络 / 5xx / TLS / DNS 错误：保留旧 token,下次 timer / 401 重试
      _log.warning('refresh transient error', e, st);
      _setTransientConnectivity();
      return RefreshOutcome.transient;
    }
  }

  /// transient 失败时按 access token 是否还有效区分:
  ///   - 还在有效期 → reconnecting (业务请求仍能用)
  ///   - 已过期    → offlineWithCache (业务请求会 401, 进只读模式)
  void _setTransientConnectivity() {
    final settings = _ref.read(settingsControllerProvider).valueOrNull;
    final expiryRaw = settings?.tokenExpiresAt;
    final expiry = expiryRaw == null ? null : DateTime.tryParse(expiryRaw);
    final accessExpired =
        expiry == null || expiry.isBefore(DateTime.now().toUtc());
    _setConnectivity(
      accessExpired
          ? ConnectivityState.offlineWithCache
          : ConnectivityState.reconnecting,
    );
  }

  void _setConnectivity(ConnectivityState s) {
    final cur = _ref.read(connectivityStateProvider.notifier);
    if (cur.state != s) cur.state = s;
  }

  /// 业务请求收到 401 时调用。
  ///
  /// 流程：
  ///   1. 触发 refreshNow() (inflight 共享)
  ///   2. ok       → 读取 settings 拿最新 access_token,返回给 caller 做 retry
  ///   3. expired  → _doRefresh 已经 signOut,返回 null
  ///   4. transient → 保留 token,返回 null;业务侧拿到 null 应该显示错误
  ///                  但**不**导致 router redirect (因为 token 还在 storage)
  ///
  /// 关键决策:transient 时**不** signOut。之前的实现碰到 transient 一律踢人,
  /// 导致 Identity 偶尔重启 / 网络抖动 = 频繁登出。详见 Design §3.5。
  Future<String?> handle401() async {
    final outcome = await refreshNow();
    switch (outcome) {
      case RefreshOutcome.ok:
        return _ref.read(settingsControllerProvider).valueOrNull?.accessToken;
      case RefreshOutcome.expired:
        // _doRefresh 已经 signOut,UI 通过 sessionExpiredCount 监听弹窗
        return null;
      case RefreshOutcome.transient:
        // 业务请求 401 + refresh transient 失败 = 网络问题,不是会话死了
        // 保留 token,让用户在网络恢复后继续用
        _log.info(
          'refresh transient after 401 — keeping token, caller should retry later',
        );
        return null;
    }
  }

  void _markSessionExpired(SessionExpiredReason reason) {
    // 写 reason 在 bump 计数器**之前**,确保 UI 监听到上升沿时
    // 已能读到最新原因。
    _ref.read(sessionExpiredReasonProvider.notifier).state = reason;
    final c = _ref.read(sessionExpiredCountProvider.notifier);
    c.state = c.state + 1;
  }
}

/// 会话过期原因。UI 按此区分对话框文案:
///   - expired:   普通过期(refresh_token 到期 / 用户长期不用 / 改密被踢)
///                文案: "会话过期, 请重新登录"
///   - tokenReuse:reuse detection 命中(server 返 'token_reuse')。
///                文案: "会话被吊销 — 检测到可疑活动, 建议立即修改密码"
enum SessionExpiredReason { expired, tokenReuse }

/// 最新会话过期原因。每次 _markSessionExpired 写一次,UI 在 dialog
/// 显示时读取。多次连续过期(实际不会,但兜底)按最后一次的原因渲染。
final sessionExpiredReasonProvider = StateProvider<SessionExpiredReason>(
  (_) => SessionExpiredReason.expired,
);

/// 会话过期次数计数器。每次 token 刷新失败 → 强制下线时 +1。
/// UI 监听这个值的"上升沿"弹一次对话框 (用户主动 signOut 不 bump)。
final sessionExpiredCountProvider = StateProvider<int>((_) => 0);

final tokenManagerProvider = Provider<TokenManager>((ref) => TokenManager(ref));
