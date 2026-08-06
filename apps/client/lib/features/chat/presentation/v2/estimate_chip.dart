// EstimateChip — chat composer 发送前的「约 N-M 积分」chip.
//
// 显示规则:
//   - content 长度 < 20 字符 → 隐藏 (避免每打几个字都 ping 一次)
//   - 600ms debounce 后调 /v1/chat/estimate
//   - BYOK 命中 → 显示「0 积分 · BYOK」绿底
//   - pricing 缺失 / 网络错 → 隐藏
//
// 设计: docs/BiuMind-Billing-Redesign.md §7.1.

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../application/billing_estimate_providers.dart';
import '../../data/billing_estimate_client.dart';

class EstimateChip extends ConsumerStatefulWidget {
  const EstimateChip({
    super.key,
    required this.model,
    required this.content,
  });

  final String model;
  final String content;

  @override
  ConsumerState<EstimateChip> createState() => _EstimateChipState();
}

class _EstimateChipState extends ConsumerState<EstimateChip> {
  Timer? _debounce;
  ChatEstimate? _estimate;
  bool _loading = false;

  static const _debounceDuration = Duration(milliseconds: 600);
  static const _minTriggerLength = 20;

  @override
  void didUpdateWidget(covariant EstimateChip old) {
    super.didUpdateWidget(old);
    if (old.model != widget.model || old.content != widget.content) {
      _debounce?.cancel();
      _debounce = Timer(_debounceDuration, _fetch);
    }
  }

  @override
  void dispose() {
    _debounce?.cancel();
    super.dispose();
  }

  Future<void> _fetch() async {
    final content = widget.content.trim();
    if (content.length < _minTriggerLength) {
      if (mounted && _estimate != null) {
        setState(() => _estimate = null);
      }
      return;
    }
    final client = ref.read(billingEstimateClientProvider);
    if (client == null) return;

    if (mounted) setState(() => _loading = true);
    try {
      final est = await client.estimate(
        model: widget.model,
        messages: [
          {'role': 'user', 'content': content},
        ],
      );
      if (mounted) {
        setState(() {
          _estimate = est;
          _loading = false;
        });
      }
    } catch (_) {
      // 静默 — 不阻塞用户输入
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final est = _estimate;
    if (est == null) {
      return _loading
          ? const SizedBox(
              width: 12,
              height: 12,
              child: CircularProgressIndicator(strokeWidth: 1.5),
            )
          : const SizedBox.shrink();
    }
    final label = est.displayLabel();
    if (label.isEmpty) return const SizedBox.shrink();

    final theme = Theme.of(context);
    final isByok = est.byokActive;
    final bg = isByok
        ? Colors.green.withValues(alpha: 0.12)
        : theme.colorScheme.surfaceContainerHighest;
    final fg = isByok ? Colors.green.shade700 : theme.colorScheme.onSurface;

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(10),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(
            isByok ? Icons.vpn_key : Icons.toll_outlined,
            size: 11,
            color: fg,
          ),
          const SizedBox(width: 4),
          Text(
            label,
            style: theme.textTheme.labelSmall?.copyWith(
              color: fg,
              fontFeatures: const [FontFeature.tabularFigures()],
            ),
          ),
        ],
      ),
    );
  }
}
