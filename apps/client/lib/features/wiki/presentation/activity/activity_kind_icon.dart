/// Per-kind icon + accent color for Activity Feed cards.
///
/// 集中管理 — StatusBar pill / 卡头 / collapsed row 共用一套视觉。
library;

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';
import 'activity_state.dart';

({IconData icon, Color color}) activityKindVisual(ActivityKind kind) {
  switch (kind) {
    case ActivityKind.ingest:
      return (icon: Icons.bolt, color: StarredColors.lintGold);
    case ActivityKind.research:
      return (icon: Icons.travel_explore_outlined, color: BiuTokens.purple);
    case ActivityKind.lint:
      return (icon: Icons.fact_check_outlined, color: BiuTokens.success);
    case ActivityKind.dedup:
      return (icon: Icons.compare_arrows, color: BiuTokens.purple);
    case ActivityKind.sweep:
      return (
        icon: Icons.cleaning_services_outlined,
        color: BiuTokens.textSecondary,
      );
    case ActivityKind.unknown:
      return (icon: Icons.help_outline, color: BiuTokens.textMuted);
  }
}
