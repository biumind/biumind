// Test Plan / Subscription parsing + MembershipClient 路径构造.
//
// 不联实服务;通过验证 fromJson 反序列化精确度覆盖客户端契约.

import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/membership/domain/plan.dart';
import 'package:biumind/features/membership/domain/subscription.dart';

void main() {
  group('Plan.fromJson', () {
    test('完整字段', () {
      final p = Plan.fromJson(jsonDecode('''
{
  "id": "uuid-pro",
  "code": "pro",
  "name": "Pro",
  "description": "个人专业版",
  "sort_order": 1,
  "price_currency": "USD",
  "price_monthly": 19.0,
  "price_yearly": 190.0,
  "monthly_credits": 10000,
  "benefits": {
    "hub_rpm": 600,
    "hub_tpm": 500000,
    "sandbox_daily": 100,
    "sandbox_concurrent": 5,
    "memory_quota": 5000,
    "brain_projects": 50
  },
  "is_current": true
}
'''));
      expect(p.id, 'uuid-pro');
      expect(p.code, PlanCode.pro);
      expect(p.priceMonthly, 19.0);
      expect(p.benefits.hubRpm, 600);
      expect(p.benefits.brainProjects, 50);
      expect(p.isCurrent, true);
    });

    test('缺字段 → 默认 0 / empty', () {
      final p = Plan.fromJson(jsonDecode('''
{
  "id": "x",
  "code": "free",
  "name": "Free",
  "price_currency": "CNY"
}
'''));
      expect(p.priceMonthly, 0.0);
      // PlanLimits 是值对象, 用字段对比
      expect(p.benefits.hubRpm, 0);
      expect(p.benefits.brainProjects, 0);
      expect(p.isCurrent, false);
    });

    test('未知 code 降级 free', () {
      final p = Plan.fromJson(jsonDecode('''
{"id":"x","code":"custom-tier","name":"X"}
'''));
      expect(p.code, PlanCode.free);
    });

    test('label 中文', () {
      expect(PlanCode.pro.label, '专业版');
      expect(PlanCode.team.label, '团队版');
      expect(PlanCode.enterprise.label, '企业版');
    });
  });

  group('Subscription.fromJson', () {
    test('真实订阅 active 状态', () {
      final s = Subscription.fromJson(jsonDecode('''
{
  "id": "sub-1",
  "user_id": "uid-1",
  "plan": {"id":"p","code":"team","name":"Team","price_currency":"USD"},
  "status": "active",
  "current_period_start": "2026-06-01T00:00:00Z",
  "current_period_end": "2026-07-01T00:00:00Z",
  "billing_cycle": "yearly",
  "stripe_customer_id": "cus_x",
  "stripe_subscription_id": "sub_y",
  "is_active": true,
  "quota": {
    "chat_message": {"used": 1500, "monthly": 5000, "period_start": "2026-06-01T00:00:00Z", "period_end": "2026-07-01T00:00:00Z"},
    "aigc_task": {"used": 0, "monthly": 1000, "period_start": "2026-06-01T00:00:00Z", "period_end": "2026-07-01T00:00:00Z"}
  }
}
'''));
      expect(s.id, 'sub-1');
      expect(s.status, SubStatus.active);
      expect(s.plan.code, PlanCode.team);
      expect(s.billingCycle, 'yearly');
      expect(s.isActive, true);
      expect(s.isVirtual, false);
      expect(s.currentPeriodStart, isNotNull);
      expect(s.chatQuota.used, 1500);
      expect(s.chatQuota.monthly, 5000);
      expect(s.chatQuota.progress, closeTo(0.3, 0.001));
    });

    test('虚拟 free plan (id 为空)', () {
      final s = Subscription.fromJson(jsonDecode('''
{
  "id": "",
  "user_id": "uid-2",
  "plan": {"id":"p","code":"free","name":"Free","price_currency":"USD"},
  "status": "free",
  "billing_cycle": "lifetime",
  "is_active": true,
  "quota": {}
}
'''));
      expect(s.isVirtual, true);
      expect(s.status, SubStatus.free);
      expect(s.isActive, true);
      expect(s.currentPeriodStart, null);
    });

    test('past_due 解析正确', () {
      final s = Subscription.fromJson(jsonDecode('''
{"id":"x","user_id":"u","plan":{"id":"p","code":"pro","name":"Pro","price_currency":"USD"},
 "status":"past_due","billing_cycle":"monthly","is_active":true,"usage_this_month":{}}
'''));
      expect(s.status, SubStatus.pastDue);
      expect(s.status.label, '续费失败 (重试中)');
    });
  });

  group('PlanLimits', () {
    test('完整字段', () {
      final l = PlanLimits.fromJson({
        'hub_rpm': 600,
        'hub_tpm': 500000,
        'sandbox_daily': 100,
        'sandbox_concurrent': 5,
        'memory_quota': 5000,
        'brain_projects': 50,
      });
      expect(l.hubRpm, 600);
      expect(l.hubTpm, 500000);
      expect(l.sandboxConcurrent, 5);
      expect(l.brainProjects, 50);
    });

    test('缺字段 → 0', () {
      final l = PlanLimits.fromJson({});
      expect(l.hubRpm, 0);
      expect(l.brainProjects, 0);
    });
  });
}
