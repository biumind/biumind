// Plan / PlanLimits — Phase 4 W2-10.
//
// 客户端 view model, 与 services/identity/internal/api/plans.go planView
// JSON 字段集对齐. 用于「升级」页面 / 设置-会员状态 等地方.

class PlanLimits {
  /// 每分钟请求数上限 (model-relay LLM 限流).
  final int hubRpm;

  /// 每分钟 token 数上限.
  final int hubTpm;

  /// 沙箱每日创建上限.
  final int sandboxDaily;

  /// 沙箱并发数上限.
  final int sandboxConcurrent;

  /// 每个项目记忆条目上限.
  final int memoryQuota;

  /// 项目数上限.
  final int brainProjects;

  const PlanLimits({
    required this.hubRpm,
    required this.hubTpm,
    required this.sandboxDaily,
    required this.sandboxConcurrent,
    required this.memoryQuota,
    required this.brainProjects,
  });

  factory PlanLimits.fromJson(Map<String, dynamic> j) => PlanLimits(
        hubRpm: (j['hub_rpm'] ?? 0) as int,
        hubTpm: (j['hub_tpm'] ?? 0) as int,
        sandboxDaily: (j['sandbox_daily'] ?? 0) as int,
        sandboxConcurrent: (j['sandbox_concurrent'] ?? 0) as int,
        memoryQuota: (j['memory_quota'] ?? 0) as int,
        brainProjects: (j['brain_projects'] ?? 0) as int,
      );

  static const empty = PlanLimits(
    hubRpm: 0, hubTpm: 0,
    sandboxDaily: 0, sandboxConcurrent: 0,
    memoryQuota: 0, brainProjects: 0,
  );
}

/// 套餐档位. 与服务端 billing.Plan 字面量对齐.
enum PlanCode {
  free,
  pro,
  team,
  enterprise;

  static PlanCode parse(String s) {
    switch (s) {
      case 'free':
        return PlanCode.free;
      case 'pro':
        return PlanCode.pro;
      case 'team':
        return PlanCode.team;
      case 'enterprise':
        return PlanCode.enterprise;
      default:
        return PlanCode.free;
    }
  }

  String get wireValue {
    switch (this) {
      case PlanCode.free:
        return 'free';
      case PlanCode.pro:
        return 'pro';
      case PlanCode.team:
        return 'team';
      case PlanCode.enterprise:
        return 'enterprise';
    }
  }

  String get label {
    switch (this) {
      case PlanCode.free:
        return '免费版';
      case PlanCode.pro:
        return '专业版';
      case PlanCode.team:
        return '团队版';
      case PlanCode.enterprise:
        return '企业版';
    }
  }
}

class Plan {
  final String id;
  final PlanCode code;
  final String name;
  final String description;
  final int sortOrder;
  final String priceCurrency; // USD | CNY
  final double priceMonthly;
  final double priceYearly;
  final int monthlyCredits;
  final PlanLimits benefits;

  /// true = 用户当前已订阅此 plan (服务端高亮).
  final bool isCurrent;

  /// Stripe price ID, 升级时调用 Stripe Checkout 用. 可空.
  final String? stripePriceMonthly;
  final String? stripePriceYearly;

  const Plan({
    required this.id,
    required this.code,
    required this.name,
    required this.description,
    required this.sortOrder,
    required this.priceCurrency,
    required this.priceMonthly,
    required this.priceYearly,
    required this.monthlyCredits,
    required this.benefits,
    required this.isCurrent,
    this.stripePriceMonthly,
    this.stripePriceYearly,
  });

  factory Plan.fromJson(Map<String, dynamic> j) => Plan(
        id: (j['id'] ?? '') as String,
        code: PlanCode.parse((j['code'] ?? 'free') as String),
        name: (j['name'] ?? '') as String,
        description: (j['description'] ?? '') as String,
        sortOrder: (j['sort_order'] ?? 0) as int,
        priceCurrency: (j['price_currency'] ?? 'USD') as String,
        priceMonthly: ((j['price_monthly'] ?? 0) as num).toDouble(),
        priceYearly: ((j['price_yearly'] ?? 0) as num).toDouble(),
        monthlyCredits: (j['monthly_credits'] ?? 0) as int,
        benefits: PlanLimits.fromJson(
          (j['benefits'] as Map<String, dynamic>?) ?? const {},
        ),
        isCurrent: (j['is_current'] ?? false) as bool,
        stripePriceMonthly: j['stripe_price_monthly'] as String?,
        stripePriceYearly: j['stripe_price_yearly'] as String?,
      );
}
