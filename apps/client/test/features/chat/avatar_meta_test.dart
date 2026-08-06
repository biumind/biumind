// avatar_meta_test — provider/model → AvatarMeta 分发表 + fallback。
//
// Widget 渲染由 MessageAvatar 处理 (单测意义不大 — 圆 + 字两件事)。
// 这里集中测纯函数:
//   * resolveAssistantAvatar 命中已知 provider / heuristic 前缀 / 落 fallback
//   * resolveUserAvatar 三档 (name / email / 都空)

import 'package:flutter/material.dart';
import 'package:biumind/l10n/app_localizations.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/domain/avatar_meta.dart';

void main() {
  group('resolveAssistantAvatar', () {
    test('Anthropic catalog 模型 → C / 橙', () {
      final m = resolveAssistantAvatar('claude-opus-4-7');
      expect(m.label, 'C');
      expect(m.background, const Color(0xFFD97757));
    });

    test('OpenAI catalog 模型 → G / 绿', () {
      final m = resolveAssistantAvatar('gpt-4o');
      expect(m.label, 'G');
      expect(m.background, const Color(0xFF10A37F));
    });

    test('Google catalog 模型 → G / 蓝', () {
      final m = resolveAssistantAvatar('gemini-2.0-flash');
      expect(m.label, 'G');
      expect(m.background, const Color(0xFF4285F4));
    });

    test('claude- 前缀 heuristic → Anthropic 橙 (catalog 没收录的型号)', () {
      final m = resolveAssistantAvatar('claude-future-9');
      expect(m.background, const Color(0xFFD97757));
    });

    test('o1 系列 → OpenAI 绿', () {
      final m = resolveAssistantAvatar('o1-pro');
      expect(m.label, 'G');
    });

    test('完全未知 model → fallback 用首字符大写', () {
      final m = resolveAssistantAvatar('mystery-model-x');
      expect(m.label, 'M');
    });

    test('null / empty → AI 字样 fallback', () {
      expect(resolveAssistantAvatar(null).label, 'A');
      expect(resolveAssistantAvatar('').label, 'A');
    });

    test('CJK 首字符 fallback', () {
      // 自定义 provider 起了中文别名 — 仍能拿到一个能看的字符。
      final m = resolveAssistantAvatar('文心一言-3.5');
      expect(m.label, '文');
    });
  });

  group('resolveUserAvatar', () {
    test('name 优先于 email', () {
      final m = resolveUserAvatar(name: 'Alice', email: 'b@x.com');
      expect(m.label, 'A');
    });

    test('只有 email 用 email 首字符', () {
      final m = resolveUserAvatar(email: 'didi@example.com');
      expect(m.label, 'D');
    });

    test('email 大小写归一', () {
      final m = resolveUserAvatar(email: 'mary@example.com');
      expect(m.label, 'M');
    });

    test('全空 → 通用 emoji 占位', () {
      final m = resolveUserAvatar();
      expect(m.label, '👤');
    });

    test('whitespace name 等同空 → fallback', () {
      final m = resolveUserAvatar(name: '   ');
      expect(m.label, '👤');
    });

    test('CJK 用户名 → 取首字 (不变换大小写)', () {
      final m = resolveUserAvatar(name: '张三');
      expect(m.label, '张');
    });
  });
}
