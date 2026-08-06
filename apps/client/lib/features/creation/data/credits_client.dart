// CreditsClient — services/identity 积分 endpoint thin wrapper.
//
//   GET  /v1/identity/me/credits/balance         (Bearer)
//   GET  /v1/identity/me/credits/logs            (Bearer)
//   GET  /v1/identity/me/credits/packages        (Bearer)
//   GET  /v1/credits/recharge-options                       (公开)
//   POST /v1/identity/me/credits/recharge        (Bearer)   v1 mock
//
// 所有方法用 _http_helpers.apiRequest, 自动 401 retry. 与 AigcClient 同模式.

import '../../../data/api/_http_helpers.dart';

class CreditsBalance {
  /// permanentBalance / timeLimitedBalance / total 单位 = millicents (毫分).
  /// 1 积分 = 1000 millicents = 1 cent. 这是 identity 服务 ledger 的内部单位
  /// (避免浮点精度).
  ///
  /// UI 展示用 `permanentCredits` / `timeLimitedCredits` / `totalCredits`
  /// getter,自动 ~/ 1000 转整数积分. 历史 bug: UI 直接渲染 millicents 当
  /// "积分" 显示,数字虚高 1000 倍 (用户看到 99,991,855 实际只是 99,991 积分).
  final int permanentBalance;
  final int timeLimitedBalance;
  final int total;
  final DateTime? timeLimitedEarliestExpires;
  final DateTime updatedAt;

  const CreditsBalance({
    required this.permanentBalance,
    required this.timeLimitedBalance,
    required this.total,
    required this.updatedAt,
    this.timeLimitedEarliestExpires,
  });

  /// 永久积分 (整数). millicents → 积分 (向下取整,避免显示 1.5 积分这种).
  int get permanentCredits => permanentBalance ~/ 1000;

  /// 时效积分 (整数).
  int get timeLimitedCredits => timeLimitedBalance ~/ 1000;

  /// 总积分 (整数). 用 sum/1000 不是 (perm+tl)/1000 防小数累加误差.
  int get totalCredits => total ~/ 1000;

  factory CreditsBalance.fromJson(Map<String, dynamic> j) => CreditsBalance(
        permanentBalance: (j['permanent_balance'] as num?)?.toInt() ?? 0,
        timeLimitedBalance: (j['time_limited_balance'] as num?)?.toInt() ?? 0,
        total: (j['total'] as num?)?.toInt() ?? 0,
        timeLimitedEarliestExpires:
            _parseDate(j['time_limited_earliest_expires']),
        updatedAt: _parseDate(j['updated_at']) ?? DateTime.now().toUtc(),
      );

  /// 空账户占位 (新用户 / 未登录).
  static CreditsBalance empty() => CreditsBalance(
        permanentBalance: 0,
        timeLimitedBalance: 0,
        total: 0,
        updatedAt: DateTime.now().toUtc(),
      );
}

class RechargeOption {
  final String id;
  final String displayName;
  final int creditsAmount;
  final String kind; // 'permanent' | 'time_limited'
  final int priceMicroCny;
  final int validDays;
  final int sortOrder;

  const RechargeOption({
    required this.id,
    required this.displayName,
    required this.creditsAmount,
    required this.kind,
    required this.priceMicroCny,
    this.validDays = 0,
    this.sortOrder = 0,
  });

  factory RechargeOption.fromJson(Map<String, dynamic> j) => RechargeOption(
        id: j['id'] as String? ?? '',
        displayName: j['display_name'] as String? ?? '',
        creditsAmount: (j['credits_amount'] as num?)?.toInt() ?? 0,
        kind: j['kind'] as String? ?? 'permanent',
        priceMicroCny: (j['price_micro_cny'] as num?)?.toInt() ?? 0,
        validDays: (j['valid_days'] as num?)?.toInt() ?? 0,
        sortOrder: (j['sort_order'] as num?)?.toInt() ?? 0,
      );

  /// price_micro_cny 是 ¥ × 1_000_000. 显示给用户用 ¥X.X.
  double get priceCny => priceMicroCny / 1000000.0;
}

class CreditsClient {
  final Uri identityBaseUrl;
  final String? Function() bearerProvider;

  CreditsClient({required this.identityBaseUrl, required this.bearerProvider});

  String? get _token => bearerProvider();

  Uri _uri(String path) {
    final base = identityBaseUrl.toString().replaceAll(RegExp(r'/+$'), '');
    return Uri.parse('$base$path');
  }

  Future<CreditsBalance> fetchBalance() async {
    final resp = await apiRequest(
      method: 'GET',
      url: _uri('/v1/identity/me/credits/balance'),
      bearerToken: _token,
    );
    return CreditsBalance.fromJson(resp);
  }

  Future<List<RechargeOption>> fetchRechargeOptions() async {
    final resp = await apiRequest(
      method: 'GET',
      url: _uri('/v1/credits/recharge-options'),
      bearerToken: null,
    );
    final list = (resp['options'] as List?) ?? const [];
    return list
        .whereType<Map<String, dynamic>>()
        .map(RechargeOption.fromJson)
        .toList();
  }

  /// v1 mock: 直接返回新 balance. v2 接真支付时返 payment_intent_url.
  Future<CreditsBalance> recharge(String optionId, {String? idempotencyKey}) async {
    final body = <String, dynamic>{'option_id': optionId};
    if (idempotencyKey != null) {
      body['idempotency_key'] = idempotencyKey;
    }
    final resp = await apiRequest(
      method: 'POST',
      url: _uri('/v1/identity/me/credits/recharge'),
      bearerToken: _token,
      body: body,
    );
    final bal = (resp['balance'] as Map?)?.cast<String, dynamic>() ?? const {};
    return CreditsBalance.fromJson(bal);
  }
}

DateTime? _parseDate(dynamic v) {
  if (v is String && v.isNotEmpty) {
    try {
      return DateTime.parse(v);
    } catch (_) {
      return null;
    }
  }
  return null;
}
