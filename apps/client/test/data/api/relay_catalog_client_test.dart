// RelayCatalogModel DTO 解析测试 — 含 is_default_chat 平台默认标记。

import 'package:flutter_test/flutter_test.dart';
import 'package:biumind/data/api/relay_catalog_client.dart';

void main() {
  test('is_default_chat 缺席时 false（admin 未设默认 → 字段省略）', () {
    final m = RelayCatalogModel.fromJson({
      'code': 'kimi-k3',
      'display_name': 'Kimi K3',
      'family': 'kimi',
      'mode': 'chat',
    });
    expect(m.isDefaultChat, isFalse);
  });

  test('is_default_chat=true 透传（picker 显示"默认"标）', () {
    final m = RelayCatalogModel.fromJson({
      'code': 'claude-sonnet-4-6',
      'display_name': 'Claude Sonnet 4.6',
      'family': 'claude',
      'mode': 'chat',
      'is_default_chat': true,
    });
    expect(m.isDefaultChat, isTrue);
  });

  test('显式 false 也归一为 false', () {
    final m = RelayCatalogModel.fromJson({
      'code': 'x',
      'display_name': 'X',
      'mode': 'chat',
      'is_default_chat': false,
    });
    expect(m.isDefaultChat, isFalse);
  });
}
