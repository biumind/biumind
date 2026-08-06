/// Review type 视觉映射 —— 5 类细粒度图标 + 颜色 + 中文 label。
///
/// biumind 现版 [WikiReview.kind] 是"哪个 worker 产出"（dedup/lint/sweep
/// /merge/suggestion），粒度比较粗。本 helper 从 [WikiReview.payload]
/// 里挑 `review_type`（worker 写入约定）→ 投到 5 类视觉表达，让队列
/// 用户一眼看出"是什么类型的问题"。
///
/// 5 类（与 knowcode review-view.tsx::typeConfig 对齐）：
///   - contradiction   矛盾    黄
///   - duplicate       重复    蓝
///   - missing-page    缺页    紫
///   - confirm         确认    黑
///   - suggestion      建议    绿
library;

import 'package:flutter/material.dart';

import '../../../app/theme.dart';

class ReviewTypeStyle {
  const ReviewTypeStyle({
    required this.icon,
    required this.color,
    required this.label,
  });
  final IconData icon;
  final Color color;
  final String label;
}

/// 从 review.payload 中尝试拿 review_type；缺失时返回 null（UI 不显示徽章）。
String? extractReviewType(Map<String, dynamic> payload) {
  final v = payload['review_type'];
  if (v is String && v.isNotEmpty) return v;
  // 兼容老格式：payload['type']
  final v2 = payload['type'];
  if (v2 is String && v2.isNotEmpty) return v2;
  return null;
}

ReviewTypeStyle reviewTypeStyleFor(String reviewType) {
  switch (reviewType) {
    case 'contradiction':
      return const ReviewTypeStyle(
        icon: Icons.warning_amber_rounded,
        color: NamedPalette.amber,
        label: '矛盾',
      );
    case 'duplicate':
      return const ReviewTypeStyle(
        icon: Icons.content_copy_outlined,
        color: NamedPalette.blue,
        label: '重复',
      );
    case 'missing-page':
    case 'missing_page':
      return const ReviewTypeStyle(
        icon: Icons.help_outline,
        color: NamedPalette.purple,
        label: '缺页',
      );
    case 'confirm':
      return ReviewTypeStyle(
        icon: Icons.chat_bubble_outline,
        color: BiuTokens.text,
        label: '确认',
      );
    case 'suggestion':
      return const ReviewTypeStyle(
        icon: Icons.lightbulb_outline,
        color: NamedPalette.emerald,
        label: '建议',
      );
    default:
      return ReviewTypeStyle(
        icon: Icons.fact_check_outlined,
        color: BiuTokens.textSecondary,
        label: reviewType,
      );
  }
}

/// 一个紧凑的徽章 widget（icon + label）。Reviews 卡片头部用。
class ReviewTypeBadge extends StatelessWidget {
  const ReviewTypeBadge({super.key, required this.reviewType});
  final String reviewType;

  @override
  Widget build(BuildContext context) {
    final style = reviewTypeStyleFor(reviewType);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      decoration: BoxDecoration(
        color: style.color.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(style.icon, size: 11, color: style.color),
          const SizedBox(width: 4),
          Text(
            style.label,
            style: TextStyle(
              color: style.color,
              fontSize: 10,
              fontWeight: FontWeight.w600,
            ),
          ),
        ],
      ),
    );
  }
}
