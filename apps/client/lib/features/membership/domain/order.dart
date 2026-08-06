// Order — W5-10 订单历史视图模型.
// 与 services/identity/internal/api/subscriptions.go orderView 对齐.

class Order {
  final String id;
  final String provider; // stripe / wechat_pay / alipay / apple_iap / google_play
  final String orderType; // subscription / one_time / topup / refund
  final double amount;
  final String currency;
  final String status; // pending / succeeded / failed / refunded / canceled
  final String providerOrderID;
  final String failureMessage;
  final double refundedAmount;
  final DateTime createdAt;
  final DateTime? paidAt;

  const Order({
    required this.id,
    required this.provider,
    required this.orderType,
    required this.amount,
    required this.currency,
    required this.status,
    required this.providerOrderID,
    required this.failureMessage,
    required this.refundedAmount,
    required this.createdAt,
    this.paidAt,
  });

  factory Order.fromJson(Map<String, dynamic> j) => Order(
        id: (j['id'] ?? '') as String,
        provider: (j['provider'] ?? '') as String,
        orderType: (j['order_type'] ?? '') as String,
        amount: ((j['amount'] ?? 0) as num).toDouble(),
        currency: (j['currency'] ?? 'USD') as String,
        status: (j['status'] ?? 'pending') as String,
        providerOrderID: (j['provider_order_id'] ?? '') as String,
        failureMessage: (j['failure_message'] ?? '') as String,
        refundedAmount: ((j['refunded_amount'] ?? 0) as num).toDouble(),
        createdAt: DateTime.tryParse((j['created_at'] ?? '') as String) ??
            DateTime.fromMillisecondsSinceEpoch(0),
        paidAt: DateTime.tryParse((j['paid_at'] ?? '') as String),
      );

  bool get isSucceeded => status == 'succeeded';
  bool get isPending => status == 'pending';
  bool get isRefunded => status == 'refunded';
}
