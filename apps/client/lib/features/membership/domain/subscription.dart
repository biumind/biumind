// Subscription view model — Phase 4 W2-10.
//
// 与 services/identity/internal/api/plans.go subscriptionView JSON 对齐.

import 'plan.dart';

enum SubStatus {
  /// 试用期内.
  trialing,

  /// 正常付费态.
  active,

  /// 扣款失败 retry 中.
  pastDue,

  /// 用户取消 (周期内仍服务).
  canceled,

  /// 周期结束 (服务停止).
  expired,

  /// 无订阅 — 服务端虚拟 free plan 时返回.
  free;

  static SubStatus parse(String s) {
    switch (s) {
      case 'trialing':
        return SubStatus.trialing;
      case 'active':
        return SubStatus.active;
      case 'past_due':
        return SubStatus.pastDue;
      case 'canceled':
        return SubStatus.canceled;
      case 'expired':
        return SubStatus.expired;
      case 'free':
        return SubStatus.free;
      default:
        return SubStatus.free;
    }
  }

  String get label {
    switch (this) {
      case SubStatus.trialing:
        return '试用中';
      case SubStatus.active:
        return '生效中';
      case SubStatus.pastDue:
        return '续费失败 (重试中)';
      case SubStatus.canceled:
        return '已取消';
      case SubStatus.expired:
        return '已过期';
      case SubStatus.free:
        return '免费版';
    }
  }
}

/// QuotaUsage — 本月某 ref_type 的 quota 用量 (W4-8).
class QuotaUsage {
  final int used;
  final int monthly;
  final DateTime? periodStart;
  final DateTime? periodEnd;

  const QuotaUsage({
    required this.used,
    required this.monthly,
    this.periodStart,
    this.periodEnd,
  });

  /// 0 用量 baseline (没拉到数据 / monthly=0 时显示用).
  static const empty = QuotaUsage(used: 0, monthly: 0);

  /// 进度比例 [0, 1]. monthly=0 (free 用户无配额) 时返 0.
  double get progress {
    if (monthly <= 0) return 0;
    final r = used / monthly;
    if (r < 0) return 0;
    if (r > 1) return 1;
    return r;
  }

  bool get exhausted => monthly > 0 && used >= monthly;

  factory QuotaUsage.fromJson(Map<String, dynamic> j) => QuotaUsage(
        used: ((j['used'] ?? 0) as num).toInt(),
        monthly: ((j['monthly'] ?? 0) as num).toInt(),
        periodStart: _parseDate(j['period_start']),
        periodEnd: _parseDate(j['period_end']),
      );
}

class Subscription {
  final String id; // "" if 虚拟 free plan
  final String userId;
  final Plan plan;
  final SubStatus status;
  final DateTime? currentPeriodStart;
  final DateTime? currentPeriodEnd;
  final DateTime? trialEndAt;
  final DateTime? cancelAt;
  final DateTime? canceledAt;
  final String billingCycle; // monthly | yearly | lifetime
  final String stripeCustomerId; // "" if absent
  final String stripeSubscriptionId;
  final bool isActive;

  /// 本月 quota 使用量 — W4-8: key=ref_type (chat_message / aigc_task).
  final Map<String, QuotaUsage> quota;

  const Subscription({
    required this.id,
    required this.userId,
    required this.plan,
    required this.status,
    required this.billingCycle,
    required this.isActive,
    required this.quota,
    this.currentPeriodStart,
    this.currentPeriodEnd,
    this.trialEndAt,
    this.cancelAt,
    this.canceledAt,
    this.stripeCustomerId = '',
    this.stripeSubscriptionId = '',
  });

  factory Subscription.fromJson(Map<String, dynamic> j) => Subscription(
        id: (j['id'] ?? '') as String,
        userId: (j['user_id'] ?? '') as String,
        plan: Plan.fromJson(j['plan'] as Map<String, dynamic>),
        status: SubStatus.parse((j['status'] ?? 'free') as String),
        billingCycle: (j['billing_cycle'] ?? 'lifetime') as String,
        isActive: (j['is_active'] ?? false) as bool,
        currentPeriodStart: _parseDate(j['current_period_start']),
        currentPeriodEnd: _parseDate(j['current_period_end']),
        trialEndAt: _parseDate(j['trial_end_at']),
        cancelAt: _parseDate(j['cancel_at']),
        canceledAt: _parseDate(j['canceled_at']),
        stripeCustomerId: (j['stripe_customer_id'] ?? '') as String,
        stripeSubscriptionId: (j['stripe_subscription_id'] ?? '') as String,
        quota: _parseQuota(j['quota']),
      );

  /// chat_message 的 quota (没有时返 empty).
  QuotaUsage get chatQuota => quota['chat_message'] ?? QuotaUsage.empty;

  /// aigc_task 的 quota.
  QuotaUsage get aigcQuota => quota['aigc_task'] ?? QuotaUsage.empty;

  /// 若用户没有真实订阅 (服务端返虚拟 free plan), id 为空.
  bool get isVirtual => id.isEmpty;
}

DateTime? _parseDate(dynamic v) {
  if (v is String && v.isNotEmpty) {
    return DateTime.tryParse(v);
  }
  return null;
}

Map<String, QuotaUsage> _parseQuota(dynamic v) {
  if (v is Map<String, dynamic>) {
    return v.map(
      (k, val) => MapEntry(k, QuotaUsage.fromJson(Map<String, dynamic>.from(val as Map))),
    );
  }
  return const {};
}
