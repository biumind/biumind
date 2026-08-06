// W6-13 客户端 — 6 widget / model 测试.

import 'package:flutter/material.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter/services.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/membership/domain/coupon.dart';
import 'package:biumind/features/membership/domain/referral.dart';
import 'package:biumind/features/membership/presentation/widgets/referral_share_sheet.dart';

Future<void> _pump(WidgetTester tester, Widget child) async {
  await tester.pumpWidget(MaterialApp(home: Scaffold(body: child)));
}

void main() {
  // ─── 模型 (3) ────────────────────────────

  test('CouponRedeemResult.fromJson — credits_grant', () {
    final r = CouponRedeemResult.fromJson({
      'redemption_id': 'r1', 'coupon_code': 'GIFT500',
      'kind': 'credits_grant', 'credits_granted': 500,
    });
    expect(r.creditsGranted, 500);
    expect(r.summary(), contains('500'));
    expect(r.summary(), contains('30 天'));
  });

  test('CouponRedeemResult.summary — 4 类券文案', () {
    expect(
      const CouponRedeemResult(
        redemptionID: 'x', couponCode: 'X', kind: 'amount_off',
        discountCents: 1000,
      ).summary(),
      contains('10.00'),
    );
    expect(
      const CouponRedeemResult(
        redemptionID: 'x', couponCode: 'X', kind: 'percent_off',
        discountCents: 500,
      ).summary(),
      contains('5.00'),
    );
    expect(
      const CouponRedeemResult(
        redemptionID: 'x', couponCode: 'X', kind: 'trial_extend',
        trialExtraDays: 7,
      ).summary(),
      contains('7 天'),
    );
  });

  test('ReferralStats.fromJson — invite + stats', () {
    final s = ReferralStats.fromJson({
      'invite_code': 'ABCD1234',
      'stats': {
        'Total': 5, 'Rewarded': 3, 'Pending': 1, 'Reverted': 1,
      },
    });
    expect(s.inviteCode, 'ABCD1234');
    expect(s.total, 5);
    expect(s.rewarded, 3);
    expect(s.pending, 1);
    expect(s.reverted, 1);
  });

  // ─── widgets (3) ────────────────────────

  testWidgets('ReferralShareSheet 显示邀请码 + 链接', (tester) async {
    await _pump(tester, const ReferralShareSheet(
      inviteCode: 'CODE1234', inviterUserID: 'u-uuid',
    ));
    expect(find.text('CODE1234'), findsOneWidget);
    expect(find.textContaining('ref=CODE1234'), findsOneWidget);
  });

  testWidgets('ReferralShareSheet 复制邀请码 → snackbar', (tester) async {
    // mock clipboard binding
    String? clipboardText;
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') {
        clipboardText = (call.arguments as Map)['text'] as String?;
      }
      return null;
    });

    await _pump(tester, const ReferralShareSheet(
      inviteCode: 'PARTYBOSS', inviterUserID: 'u',
    ));
    // 第一个复制按钮 = 邀请码
    final copyButtons = find.byIcon(Icons.copy);
    expect(copyButtons, findsAtLeast(1));
    await tester.tap(copyButtons.first);
    await tester.pump();
    expect(clipboardText, 'PARTYBOSS');
  });

  testWidgets('ReferralShareSheet 自定义 baseURL 拼链接', (tester) async {
    await _pump(tester, const ReferralShareSheet(
      inviteCode: 'XYZ', inviterUserID: 'u',
      inviteBaseURL: 'https://staging.biumind.com/invite?campaign=a',
    ));
    expect(
      find.textContaining('campaign=a&ref=XYZ&inviter=u'),
      findsOneWidget,
    );
  });
}
