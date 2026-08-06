// 通用占位页 — P4a 阶段 5 个 creation 子页都用. P4b-5 各自 split 成真页面.

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';

class CreationPlaceholder extends StatelessWidget {
  const CreationPlaceholder({
    super.key,
    required this.icon,
    required this.title,
    this.subtitle,
  });

  final IconData icon;
  final String title;
  final String? subtitle;

  @override
  Widget build(BuildContext context) {
    return Container(
      color: BiuTokens.bg,
      alignment: Alignment.center,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 56, color: BiuTokens.textMuted),
          const SizedBox(height: 12),
          Text(title,
              style: TextStyle(
                fontSize: 20,
                fontWeight: FontWeight.w600,
                color: BiuTokens.text,
              )),
          if (subtitle != null) ...[
            const SizedBox(height: 6),
            Text(subtitle!,
                style: TextStyle(fontSize: 13, color: BiuTokens.textMuted)),
          ],
          const SizedBox(height: 4),
          Text('TODO',
              style: TextStyle(
                  fontSize: 11,
                  letterSpacing: 1.5,
                  color: BiuTokens.textDisabled,
                  fontWeight: FontWeight.w600)),
        ],
      ),
    );
  }
}
