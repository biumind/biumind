// 子页「返回按钮」手机形态测试 — 导航设计 §3.3 C 级清单 (N0 批次:
// apps / membership / settings)。
//
// 覆盖两类头部机制:
//   * PageScaffold.leading (自绘 header Row 首子项) — AppDetailPage /
//     AppSettingsPage 实测; AppViewHost / SidebarCustomizePage 同机制
//     (见文件末尾说明, 未单测)。
//   * AppBar.leading = phoneBackLeading(context) — membership 6 页 +
//     DeviceManagementPage 全量实测。
//
// 断言: 手机 (390×844) 出现 Icons.arrow_back; 桌面 (1200×800) 不出现
// (PhoneBackButton 桌面 shrink / phoneBackLeading 桌面 null)。
//
// 后端依赖全部 null 容错: hubCredentialsProvider override 为 null 后,
// appsClient / membershipClient / devicesClient 均为 null, 各 provider
// 走空值分支, 无网络。

import 'package:biumind/app/theme/theme.dart';
import 'package:biumind/data/apps_providers.dart';
import 'package:biumind/features/apps/presentation/app_detail_page.dart';
import 'package:biumind/features/apps/presentation/app_settings_page.dart';
import 'package:biumind/features/membership/presentation/pages/checkout_page.dart';
import 'package:biumind/features/membership/presentation/pages/coupon_redeem_page.dart';
import 'package:biumind/features/membership/presentation/pages/membership_center_page.dart';
import 'package:biumind/features/membership/presentation/pages/order_history_page.dart';
import 'package:biumind/features/membership/presentation/pages/plan_compare_page.dart';
import 'package:biumind/features/membership/presentation/pages/referral_page.dart';
import 'package:biumind/features/settings/presentation/device_management_page.dart';
import 'package:biumind/services/auth_service.dart';
import 'package:biumind/services/settings_repo.dart';
import 'package:flutter/material.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

List<Override> _overrides() => [
      // 内存 settings — 不落盘; 空 AppSettings → identityUrl null →
      // membershipClient null → 各 membership provider 走空值分支。
      settingsRepoProvider.overrideWithValue(InMemorySettingsRepo()),
      // 显式 null — 防 CI 环境 BIUMIND_MODEL_RELAY_URL / BIUMIND_TOKEN
      // 环境变量 fallback 让 hubCredentialsProvider 返回非 null 打真网络。
      hubCredentialsProvider.overrideWithValue(null),
    ];

Widget _wrap(Widget home, {List<Override> extra = const []}) {
  return ProviderScope(
    overrides: [..._overrides(), ...extra],
    child: MaterialApp(
    localizationsDelegates: const [
      AppLocalizations.delegate,
      GlobalMaterialLocalizations.delegate,
      GlobalWidgetsLocalizations.delegate,
      GlobalCupertinoLocalizations.delegate,
    ],
    supportedLocales: AppLocalizations.supportedLocales,
      theme: buildTheme(
        palette: PaletteId.inkblueOrange,
        mode: Brightness.light,
        fontSize: FontSize.small,
      ),
      home: home,
    ),
  );
}

void _setView(WidgetTester tester, Size size) {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.reset);
}

/// 手动帧代替 pumpAndSettle — provider 异步 resolve 期间 loading 态的
/// CircularProgressIndicator 永不 settle。
Future<void> _pumpFrames(WidgetTester tester) async {
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 100));
  await tester.pump(const Duration(milliseconds: 100));
}

const _phone = Size(390, 844);
const _desktop = Size(1200, 800);

void main() {
  group('PageScaffold.leading 机制 (apps)', () {
    testWidgets('AppDetailPage 手机: 头部出现 ←', (tester) async {
      _setView(tester, _phone);
      await tester.pumpWidget(_wrap(
        const Scaffold(body: AppDetailPage(identifier: 'demo')),
        extra: [
          // 固定 manifest — 直接进 data 分支渲 _Body, 不依赖 null-client
          // 空 manifest 的边角渲染。
          manifestProvider.overrideWith((ref, identifier) async =>
              const <String, dynamic>{
                'title': 'Demo App',
                'version': '1.0.0',
                'description': 'demo',
              }),
        ],
      ));
      await _pumpFrames(tester);

      expect(find.text('Demo App'), findsOneWidget);
      expect(find.byIcon(Icons.arrow_back), findsOneWidget);
    });

    testWidgets('AppDetailPage 桌面: 头部无 ← (shrink 不占位)', (tester) async {
      _setView(tester, _desktop);
      await tester.pumpWidget(_wrap(
        const Scaffold(body: AppDetailPage(identifier: 'demo')),
        extra: [
          manifestProvider.overrideWith((ref, identifier) async =>
              const <String, dynamic>{
                'title': 'Demo App',
                'version': '1.0.0',
                'description': 'demo',
              }),
        ],
      ));
      await _pumpFrames(tester);

      expect(find.text('Demo App'), findsOneWidget);
      expect(find.byIcon(Icons.arrow_back), findsNothing);
    });

    testWidgets('AppSettingsPage 手机: 头部出现 ←', (tester) async {
      // installationsProvider null-client → 空列表 → 空态文案; 头部仍在。
      _setView(tester, _phone);
      await tester.pumpWidget(_wrap(const Scaffold(body: AppSettingsPage())));
      await _pumpFrames(tester);
      expect(find.byIcon(Icons.arrow_back), findsOneWidget);
    });

    testWidgets('AppSettingsPage 桌面: 头部无 ←', (tester) async {
      _setView(tester, _desktop);
      await tester.pumpWidget(_wrap(const Scaffold(body: AppSettingsPage())));
      await _pumpFrames(tester);
      expect(find.byIcon(Icons.arrow_back), findsNothing);
    });
  });

  group('AppBar.leading = phoneBackLeading 机制 (membership / settings)', () {
    // 全部自带 Scaffold + AppBar, 无自定义 leading; 空凭据下各 provider
    // 走空值分支 (空列表 / 未登录 banner / 空态), 页面可直接构造。
    final pages = <String, Widget>{
      'MembershipCenterPage': const MembershipCenterPage(),
      'PlanComparePage': const PlanComparePage(),
      'OrderHistoryPage': const OrderHistoryPage(),
      'CheckoutPage': const CheckoutPage(),
      'CouponRedeemPage': const CouponRedeemPage(),
      'ReferralPage': const ReferralPage(currentUserID: 'u1'),
      'DeviceManagementPage': const DeviceManagementPage(),
    };

    for (final entry in pages.entries) {
      testWidgets('${entry.key} 手机: AppBar 出现 ←', (tester) async {
        _setView(tester, _phone);
        await tester.pumpWidget(_wrap(entry.value));
        await _pumpFrames(tester);
        expect(find.byIcon(Icons.arrow_back), findsOneWidget);
      });

      testWidgets('${entry.key} 桌面: AppBar 无 ← (leading 为 null)',
          (tester) async {
        _setView(tester, _desktop);
        await tester.pumpWidget(_wrap(entry.value));
        await _pumpFrames(tester);
        expect(find.byIcon(Icons.arrow_back), findsNothing);
      });
    }
  });
}

// 未单测页面说明:
//   * AppViewHost — 需要 installation + manifest + ViewSpec + ActionRunner
//     整套 fixture 才能走到 _ViewRenderer 的 PageScaffold; 其 leading 注入
//     与 AppDetailPage 同一行代码路径 (PageScaffold.leading), 由上面
//     AppDetailPage 用例覆盖该机制。
//   * SidebarCustomizePage — 同理 (sidebarLayoutProvider / Realtime
//     listener 重 fixture), PageScaffold.leading 机制已覆盖。
