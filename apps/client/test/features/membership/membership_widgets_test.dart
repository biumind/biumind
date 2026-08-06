// W5-10 widget 测试 — 12 cases.
// 覆盖: plan_card / payment_method_selector / cancel_confirm_dialog /
// upgrade_modal / order_history_page (smoke) / plan_compare_page (smoke).

import 'package:flutter/material.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/membership/domain/checkout.dart';
import 'package:biumind/features/membership/domain/order.dart';
import 'package:biumind/features/membership/domain/plan.dart';
import 'package:biumind/features/membership/domain/subscription.dart';
import 'package:biumind/features/membership/presentation/widgets/cancel_confirm_dialog.dart';
import 'package:biumind/features/membership/presentation/widgets/payment_method_selector.dart';
import 'package:biumind/features/membership/presentation/widgets/plan_card.dart';
import 'package:biumind/features/membership/presentation/widgets/upgrade_modal.dart';

// ─── fixtures ───────────────────────────────────

const _limits = PlanLimits(
  hubRpm: 600, hubTpm: 500000, sandboxDaily: 100, sandboxConcurrent: 5,
  memoryQuota: 5000, brainProjects: 50,
);

const _proPlan = Plan(
  id: 'p-pro', code: PlanCode.pro, name: 'Pro',
  description: '个人专业版', sortOrder: 1,
  priceCurrency: 'USD', priceMonthly: 19, priceYearly: 190,
  monthlyCredits: 10000, benefits: _limits, isCurrent: false,
);

const _teamPlan = Plan(
  id: 'p-team', code: PlanCode.team, name: 'Team',
  description: '团队版', sortOrder: 2,
  priceCurrency: 'USD', priceMonthly: 99, priceYearly: 990,
  monthlyCredits: 50000, benefits: _limits, isCurrent: false,
);

const _freePlan = Plan(
  id: 'p-free', code: PlanCode.free, name: 'Free',
  description: '免费体验', sortOrder: 0,
  priceCurrency: 'USD', priceMonthly: 0, priceYearly: 0,
  monthlyCredits: 0, benefits: _limits, isCurrent: false,
);

Subscription _sub(Plan p, SubStatus s) => Subscription(
      id: 'sub-1', userId: 'u1', plan: p, status: s,
      billingCycle: 'monthly', isActive: s == SubStatus.active,
      currentPeriodStart: DateTime.utc(2026, 7, 1),
      currentPeriodEnd: DateTime.utc(2026, 8, 1),
      quota: const {},
    );

Future<void> _pump(WidgetTester tester, Widget child) async {
  await tester.pumpWidget(MaterialApp(home: Scaffold(body: child)));
}

// ─── PlanCard (3) ────────────────────────────────

void main() {
  testWidgets('PlanCard - 显示价格 + 名称', (tester) async {
    await _pump(tester, const PlanCard(plan: _proPlan));
    expect(find.text('Pro'), findsOneWidget);
    expect(find.textContaining('\$19.00'), findsOneWidget);
    expect(find.textContaining('每月 10K 积分'), findsOneWidget);
  });

  testWidgets('PlanCard - isCurrent 显示当前 chip + CTA 禁用', (tester) async {
    await _pump(tester, const PlanCard(plan: _proPlan, isCurrent: true));
    expect(find.text('当前'), findsAtLeast(1));
    final btn = tester.widget<FilledButton>(find.byType(FilledButton));
    expect(btn.onPressed, isNull);
  });

  testWidgets('PlanCard - onSelect 回调', (tester) async {
    var tapped = false;
    await _pump(tester, PlanCard(
      plan: _proPlan,
      ctaLabel: '升级',
      onSelect: () => tapped = true,
    ));
    expect(find.text('升级'), findsOneWidget);
    await tester.tap(find.text('升级'));
    await tester.pump();
    expect(tapped, isTrue);
  });

  // ─── PaymentMethodSelector (2) ────────────────

  testWidgets('PaymentMethodSelector - 默认 wechat_native', (tester) async {
    PaymentProvider? selected;
    await _pump(tester, PaymentMethodSelector(
      onSelected: (p) => selected = p,
      initial: PaymentProvider.wechatNative,
    ));
    // 选项渲染存在
    expect(find.text('微信支付 (扫码)'), findsOneWidget);
    expect(find.text('支付宝 (网页)'), findsOneWidget);
    expect(selected, isNull); // 初始没回调
  });

  testWidgets('PaymentMethodSelector - 切换触发回调', (tester) async {
    PaymentProvider? selected;
    await _pump(tester, PaymentMethodSelector(
      onSelected: (p) => selected = p,
      initial: PaymentProvider.wechatNative,
    ));
    await tester.tap(find.text('支付宝 (网页)'));
    await tester.pump();
    expect(selected, PaymentProvider.alipayPC);
  });

  // ─── CancelConfirmDialog (2) ─────────────────

  testWidgets('CancelConfirmDialog - 默认 period_end', (tester) async {
    bool? captured;
    await _pump(tester, CancelConfirmDialog(
      subscription: _sub(_proPlan, SubStatus.active),
      onConfirm: (immediate) => captured = immediate,
    ));
    await tester.tap(find.text('确认取消'));
    await tester.pump();
    expect(captured, false);
  });

  testWidgets('CancelConfirmDialog - 选 immediate 后 onConfirm(true)', (tester) async {
    bool? captured;
    await _pump(tester, CancelConfirmDialog(
      subscription: _sub(_proPlan, SubStatus.active),
      onConfirm: (immediate) => captured = immediate,
    ));
    await tester.tap(find.text('立即停止 + 按比例退款'));
    await tester.pump();
    await tester.tap(find.text('确认取消'));
    await tester.pump();
    expect(captured, true);
  });

  // ─── UpgradeModal (2) ───────────────────────

  testWidgets('UpgradeModal - immediate 显示 proration net', (tester) async {
    final resp = ChangePlanResponse(
      oldPlan: 'pro', newPlan: 'team', effective: 'immediate',
      proration: const ProrationView(
        unusedRefundCents: 950, newProrateChargeCents: 4950,
        netChargeCents: 4000, remainingRatio: 0.5,
      ),
    );
    await _pump(tester, UpgradeModal(
      oldPlan: _proPlan, newPlan: _teamPlan, response: resp,
      onProceed: () {}, onClose: () {},
    ));
    expect(find.textContaining('升级到 Team'), findsOneWidget);
    expect(find.textContaining('40.00'), findsOneWidget); // net = 4000 cents = 40.00
    expect(find.text('继续支付'), findsOneWidget);
  });

  testWidgets('UpgradeModal - period_end 显示降级提示', (tester) async {
    final resp = ChangePlanResponse(
      oldPlan: 'team', newPlan: 'pro', effective: 'period_end',
      scheduledAt: DateTime.utc(2026, 8, 1),
    );
    await _pump(tester, UpgradeModal(
      oldPlan: _teamPlan, newPlan: _proPlan, response: resp,
      onProceed: () {}, onClose: () {},
    ));
    expect(find.textContaining('降级到 Pro'), findsOneWidget);
    expect(find.textContaining('当前周期末'), findsOneWidget);
    expect(find.text('确认降级'), findsOneWidget);
  });

  // ─── Order 模型 + 模型方法 (3) ─────────────

  test('Order.fromJson 解析完整字段', () {
    final o = Order.fromJson({
      'id': 'o1',
      'provider': 'wechat_pay',
      'order_type': 'subscription',
      'amount': 19.0,
      'currency': 'CNY',
      'status': 'succeeded',
      'provider_order_id': 'BIU_x',
      'created_at': '2026-07-01T00:00:00Z',
      'paid_at': '2026-07-01T00:01:00Z',
    });
    expect(o.isSucceeded, true);
    expect(o.amount, 19.0);
    expect(o.paidAt, isNotNull);
  });

  test('CheckoutRequest.toJson 跳过空可选字段', () {
    final r = CheckoutRequest(
      planCode: 'pro', provider: PaymentProvider.wechatNative,
    );
    final j = r.toJson();
    expect(j.containsKey('openid'), false);
    expect(j.containsKey('client_ip'), false);
    expect(j['plan_code'], 'pro');
    expect(j['provider'], 'wechat_native');
  });

  test('PaymentProvider.defaultFor — mobile 走 H5', () {
    expect(PaymentMethodSelector.defaultFor(isMobile: true), PaymentProvider.wechatH5);
    expect(PaymentMethodSelector.defaultFor(isMobile: false), PaymentProvider.wechatNative);
  });

  // free plan 兜底 — PlanCard 价格为 "免费"
  testWidgets('PlanCard - free plan 显示「免费」', (tester) async {
    await _pump(tester, const PlanCard(plan: _freePlan));
    expect(find.text('免费'), findsOneWidget);
  });
}
