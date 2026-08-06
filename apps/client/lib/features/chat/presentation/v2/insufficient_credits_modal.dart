// InsufficientCreditsModal — chat 触发 402 时的余额不足提示弹窗.
//
// 调用方在 catch ApiError(402, "insufficient_credits") 时:
//   await showInsufficientCreditsModal(context);
//
// 用户点击「立即充值」跳 /settings/credits.
//
// 设计: docs/BiuMind-Billing-Redesign.md §7.2.

import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

Future<void> showInsufficientCreditsModal(
  BuildContext context, {
  int? required,
  int? available,
}) {
  return showDialog<void>(
    context: context,
    builder: (dctx) => AlertDialog(
      icon: Icon(Icons.account_balance_wallet_outlined,
          size: 32, color: Theme.of(dctx).colorScheme.primary),
      title: const Text('余额不足'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (required != null && available != null)
            Text('本次需要 $required 积分, 当前余额 $available 积分.')
          else
            const Text('当前余额无法完成本次请求, 请充值或启用 BYOK 自带 Key.'),
          const SizedBox(height: 12),
          const Text(
            '提示: 在「设置 → 模型服务」录入自己的上游 Key, 平台不再扣费.',
            style: TextStyle(fontSize: 12, color: Colors.grey),
            textAlign: TextAlign.center,
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(dctx),
          child: const Text('取消'),
        ),
        TextButton(
          onPressed: () {
            Navigator.pop(dctx);
            // 跳设置 → API Keys (BYOK 替代充值)
            dctx.go('/settings');
          },
          child: const Text('录入 BYOK'),
        ),
        FilledButton(
          onPressed: () {
            Navigator.pop(dctx);
            dctx.go('/membership');
          },
          child: const Text('立即充值'),
        ),
      ],
    ),
  );
}
