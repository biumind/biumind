// Background token refresher.
//
// 服务端的 access_token TTL 由 IDENTITY_ACCESS_TTL 决定(dev 24h / prod 1h)。
// 用户体验问题:"我用着用着突然 401 被踢"。为防这点,后台 timer 周期性检查:
//
//   * 读 settings.tokenExpiresAt 与 settings.accessTtlSeconds
//   * 提前 ttl × 0.1(下限 30s)触发 refresh
//   * 调 TokenManager.refreshNow(三态:ok / expired / transient)
//   * transient(网络错)保留 token,下次 tick 再试
//   * expired(refresh_token 真死了)由 TokenManager 走 signOut + UI 弹窗
//
// 比例化(A4)替代 A0 写死的 5min margin / 1min tick:服务端真要把 access TTL
// 改回 5min 或者拉到 24h,客户端无需改代码就跟着自适应。
// 详见 BiuMind-Identity-Session-Design §3.5、Dev-Plan A4。
//
// 取舍:
//   margin = ttl × 0.1   太短客户端容易撞 401,太长频繁刷;10% 在 24h TTL
//                        下 = 144min 余量,1h TTL 下 = 6min 余量,都合理。
//   tick   = ttl × 0.05  比 margin 短,让 refresh 在进入 margin 后能尽快
//                        在下个 tick 跑完(否则可能错过几分钟才 fire)。
//   floor  = 30s         tick 下限,防 ttl 异常小时 CPU 烧。

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:logging/logging.dart';

import '../features/settings/application/settings_controller.dart';
import 'token_manager.dart';

final _log = Logger('biumind.token_refresher');

/// fallback 静态值 — 当 settings 没存 accessTtlSeconds(老安装、未登录、
/// 服务端老协议未返)时使用。对应 1h TTL × 0.1 的 margin。
const Duration _fallbackMargin = Duration(minutes: 5);
const Duration _fallbackTick = Duration(minutes: 1);

/// tick 下限 — 防 ttl 异常小(如服务端误配 30s)导致 CPU 烧。
const Duration _minTick = Duration(seconds: 30);

/// 最小 margin — 防服务端 ttl 极小时 margin 算出来 < tick,refresher
/// 永远来不及 fire。
const Duration _minMargin = Duration(seconds: 30);

/// 暴露给单测的纯函数 — 给定 access_ttl_seconds 推导 (margin, tick)。
/// 0 / null 输入时返 fallback 值。
({Duration margin, Duration tick}) computeRefreshCadence(int? accessTtlSeconds) {
  if (accessTtlSeconds == null || accessTtlSeconds <= 0) {
    return (margin: _fallbackMargin, tick: _fallbackTick);
  }
  final ttl = Duration(seconds: accessTtlSeconds);
  final margin = ttl ~/ 10;
  final tick = ttl ~/ 20;
  return (
    margin: margin < _minMargin ? _minMargin : margin,
    tick: tick < _minTick ? _minTick : tick,
  );
}

class TokenRefresher {
  TokenRefresher(this._ref);
  final Ref _ref;
  Timer? _timer;
  Duration _activeTick = _fallbackTick;

  void start() {
    _restartTimer(_fallbackTick);
    // Tick 一次马上跑 — 刚启动的 app 如果 token 已过期不用等 30 s。
    _tick();
  }

  void stop() {
    _timer?.cancel();
    _timer = null;
  }

  void _restartTimer(Duration tick) {
    if (_activeTick == tick && _timer != null) return;
    _timer?.cancel();
    _activeTick = tick;
    _timer = Timer.periodic(tick, (_) => _tick());
  }

  Future<void> _tick() async {
    final settings = _ref.read(settingsControllerProvider).valueOrNull;
    if (settings == null) return;

    final expiryRaw = settings.tokenExpiresAt;
    final refreshTok = settings.refreshToken;
    final identityUrl = settings.identityUrl;

    if (expiryRaw == null ||
        expiryRaw.isEmpty ||
        refreshTok == null ||
        refreshTok.isEmpty ||
        identityUrl == null ||
        identityUrl.isEmpty) {
      return; // Not signed in.
    }

    // 比例化:每个 tick 用最新的 accessTtlSeconds 重算 cadence,服务端改
    // TTL 后下一次 refresh 写回 settings,refresher 自动跟上。
    final cadence = computeRefreshCadence(settings.accessTtlSeconds);
    if (cadence.tick != _activeTick) {
      _log.fine('refresh cadence change: tick ${_activeTick.inSeconds}s '
          '→ ${cadence.tick.inSeconds}s, margin ${cadence.margin.inSeconds}s');
      _restartTimer(cadence.tick);
    }

    final expiry = DateTime.tryParse(expiryRaw);
    if (expiry == null) return;
    final until = expiry.difference(DateTime.now().toUtc());
    if (until > cadence.margin) return; // Plenty of life left.

    _log.info('proactive refresh (expires in ${until.inSeconds}s, '
        'margin ${cadence.margin.inSeconds}s)');
    // 复用 TokenManager — 共享 inflight 锁,避免与"业务 401 触发的刷新"撞车
    await _ref.read(tokenManagerProvider).refreshNow();
  }
}

/// Boots a single TokenRefresher for the app's lifetime. Reading the
/// provider once (e.g. in main.dart) starts it; auto-disposes on
/// container teardown.
final tokenRefresherProvider = Provider<TokenRefresher>((ref) {
  final r = TokenRefresher(ref);
  r.start();
  ref.onDispose(r.stop);
  return r;
});

/// Convenience: read this in a top-level widget so the refresher
/// actually boots (Riverpod providers are lazy).
final tokenRefresherStarterProvider = Provider<void>((ref) {
  ref.watch(tokenRefresherProvider);
  // Re-watch settings so the refresher learns about new tokens (e.g.
  // after first sign-in) without a restart. The watchdog timer then
  // picks the new expiry up on its next tick.
  ref.watch(settingsControllerProvider);
});
