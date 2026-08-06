// ReferralShareSheet — W6-13 邀请分享 sheet.
//
// 平台原生分享待 share_plus 集成 (后续真集成); 这里先提供:
//   - 复制邀请码
//   - 复制带 UTM 的邀请链接
//   - 二维码 placeholder (真二维码需要 qr_flutter)

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

class ReferralShareSheet extends StatelessWidget {
  final String inviteCode;
  final String inviterUserID;
  final String? inviteBaseURL; // e.g. https://biumind.com/signup
  const ReferralShareSheet({
    super.key,
    required this.inviteCode,
    required this.inviterUserID,
    this.inviteBaseURL,
  });

  String get _shareLink {
    final base = inviteBaseURL ?? 'https://biumind.com/signup';
    final sep = base.contains('?') ? '&' : '?';
    return '$base${sep}ref=$inviteCode&inviter=$inviterUserID';
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.all(20),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('分享邀请', style: theme.textTheme.titleLarge),
            const SizedBox(height: 16),
            // 邀请码
            _InfoBox(
              label: '邀请码',
              value: inviteCode,
              hint: '请朋友输入此码',
              onCopy: () => _copy(context, inviteCode, '邀请码已复制'),
            ),
            const SizedBox(height: 12),
            _InfoBox(
              label: '邀请链接',
              value: _shareLink,
              hint: '朋友点击直接进入注册',
              onCopy: () => _copy(context, _shareLink, '邀请链接已复制'),
            ),
            const SizedBox(height: 16),
            // 二维码占位区
            Container(
              width: double.infinity,
              padding: const EdgeInsets.symmetric(vertical: 32),
              decoration: BoxDecoration(
                color: theme.dividerColor.withValues(alpha: 0.1),
                borderRadius: BorderRadius.circular(12),
              ),
              alignment: Alignment.center,
              child: Column(
                children: [
                  Icon(Icons.qr_code_2, size: 64, color: theme.hintColor),
                  const SizedBox(height: 8),
                  Text(
                    '海报 / 二维码即将上线',
                    style: theme.textTheme.bodySmall?.copyWith(color: theme.hintColor),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  void _copy(BuildContext context, String text, String tip) {
    Clipboard.setData(ClipboardData(text: text));
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(tip)));
  }
}

class _InfoBox extends StatelessWidget {
  final String label;
  final String value;
  final String hint;
  final VoidCallback onCopy;
  const _InfoBox({
    required this.label,
    required this.value,
    required this.hint,
    required this.onCopy,
  });

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
      decoration: BoxDecoration(
        color: theme.colorScheme.primaryContainer.withValues(alpha: 0.3),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label, style: theme.textTheme.bodySmall?.copyWith(color: theme.hintColor)),
          const SizedBox(height: 4),
          Row(
            children: [
              Expanded(
                child: SelectableText(
                  value,
                  style: theme.textTheme.titleMedium?.copyWith(fontWeight: FontWeight.w700),
                  maxLines: 1,
                ),
              ),
              IconButton(
                icon: const Icon(Icons.copy, size: 18),
                onPressed: onCopy,
                tooltip: '复制',
              ),
            ],
          ),
          Text(hint, style: theme.textTheme.bodySmall?.copyWith(color: theme.hintColor)),
        ],
      ),
    );
  }
}
