// OrderHistoryPage — 订单历史. W5-10.
//
// GET /v1/subscriptions/orders 列表.
// 状态颜色:
//   succeeded  — 绿
//   pending    — 蓝
//   refunded   — 橙
//   failed/canceled — 红

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../core/layout/phone_nav.dart';
import '../../application/membership_providers.dart';
import '../../domain/order.dart';

class OrderHistoryPage extends ConsumerWidget {
  const OrderHistoryPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final ordersAsync = ref.watch(ordersListProvider);
    return Scaffold(
      appBar: AppBar(
        // 子页头左位 ← (手机形态; 桌面必须为 null, 见 phone_nav.dart)。
        leading: phoneBackLeading(context),
        title: const Text('订单历史'),
      ),
      body: ordersAsync.when(
        data: (orders) {
          if (orders.isEmpty) {
            return const Center(child: Text('暂无订单'));
          }
          return ListView.separated(
            padding: const EdgeInsets.all(16),
            itemBuilder: (ctx, i) => _OrderCard(order: orders[i]),
            separatorBuilder: (_, _) => const SizedBox(height: 8),
            itemCount: orders.length,
          );
        },
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(child: Text('$e')),
      ),
    );
  }
}

class _OrderCard extends StatelessWidget {
  final Order order;
  const _OrderCard({required this.order});

  Color _statusColor(BuildContext c) {
    switch (order.status) {
      case 'succeeded':
        return Colors.green;
      case 'pending':
        return Colors.blue;
      case 'refunded':
        return Colors.orange;
      case 'failed':
      case 'canceled':
        return Colors.red;
      default:
        return Theme.of(c).hintColor;
    }
  }

  String _providerLabel() {
    switch (order.provider) {
      case 'wechat_pay':
        return '微信支付';
      case 'alipay':
        return '支付宝';
      case 'stripe':
        return 'Stripe';
      case 'apple_iap':
        return 'Apple IAP';
      case 'google_play':
        return 'Google Play';
      default:
        return order.provider;
    }
  }

  String _statusLabel() {
    switch (order.status) {
      case 'succeeded':
        return '已支付';
      case 'pending':
        return '待支付';
      case 'refunded':
        return '已退款';
      case 'failed':
        return '失败';
      case 'canceled':
        return '已取消';
      default:
        return order.status;
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final symbol = order.currency == 'USD' ? '\$' : '¥';
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    '$symbol${order.amount.toStringAsFixed(2)}',
                    style: theme.textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w700),
                  ),
                  const SizedBox(height: 2),
                  Text(
                    '${_providerLabel()} · ${order.orderType}',
                    style: theme.textTheme.bodySmall?.copyWith(color: theme.hintColor),
                  ),
                  const SizedBox(height: 4),
                  Text(
                    order.createdAt.toLocal().toString().split('.').first,
                    style: theme.textTheme.bodySmall,
                  ),
                  if (order.providerOrderID.isNotEmpty)
                    SelectableText(
                      order.providerOrderID,
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.hintColor, fontSize: 11,
                      ),
                    ),
                ],
              ),
            ),
            Chip(
              label: Text(_statusLabel(), style: TextStyle(color: _statusColor(context))),
              backgroundColor: _statusColor(context).withValues(alpha: 0.1),
              side: BorderSide(color: _statusColor(context).withValues(alpha: 0.3)),
            ),
          ],
        ),
      ),
    );
  }
}
