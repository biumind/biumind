// view_spec.dart unit tests — pin manifest JSON parsing.
//
// Regressions here surface as views silently rendering nothing —
// catch them at the parsing seam.

import 'package:biumind/features/apps/domain/view_spec.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('parses minimal list view', () {
    final v = ViewSpec.fromJson({
      'id': 'home',
      'route': '/apps/rss',
      'title': 'RSS',
      'layout': 'list',
    });
    expect(v.id, 'home');
    expect(v.layout, ViewLayout.list);
    expect(v.dataSource, isNull);
  });

  test('parses list_detail with item template + actions', () {
    final v = ViewSpec.fromJson({
      'id': 'home',
      'route': '/apps/rss',
      'layout': 'list_detail',
      'data_source': {'action': 'list_subscriptions'},
      'item_template': {
        'kind': 'card',
        'title': r'${item.title}',
        'subtitle': r'${item.unread} 未读',
        'actions': [
          {'label': '打开', 'route': r'/apps/rss/feed/${item.id}'},
          {'label': '取消订阅', 'action': 'unsubscribe', 'confirm': '确认？'},
        ],
      },
      'refresh_on': ['app:install:<self>:item_arrived'],
    });
    expect(v.layout, ViewLayout.listDetail);
    expect(v.dataSource?.action, 'list_subscriptions');
    expect(v.itemTemplate?.kind, 'card');
    expect(v.itemTemplate?.actions.length, 2);
    expect(v.itemTemplate?.actions[1].action, 'unsubscribe');
    expect(v.itemTemplate?.actions[1].confirm, '确认？');
    expect(v.refreshOn, ['app:install:<self>:item_arrived']);
  });

  test('parses form layout with submit + on_success', () {
    final v = ViewSpec.fromJson({
      'id': 'add',
      'route': '/apps/rss/add',
      'layout': 'form',
      'schema_ref': 'actions.subscribe.input_schema',
      'submit': {
        'action': 'subscribe',
        'on_success': {'toast': '订阅成功', 'navigate': '/apps/rss'},
      },
    });
    expect(v.layout, ViewLayout.form);
    expect(v.schemaRef, 'actions.subscribe.input_schema');
    expect(v.submit?.action, 'subscribe');
    expect(v.submit?.onSuccess?.toast, '订阅成功');
    expect(v.submit?.onSuccess?.navigate, '/apps/rss');
  });

  test('parses webview layout', () {
    final v = ViewSpec.fromJson({
      'id': 'kimi',
      'route': '/apps/kimi',
      'layout': 'webview',
      'url': 'https://kimi.moonshot.cn/',
    });
    expect(v.layout, ViewLayout.webView);
    expect(v.url, 'https://kimi.moonshot.cn/');
  });

  test('falls back to ViewLayout.unknown for unrecognised values', () {
    // forward-compat: a view authored against a future client should
    // not crash an older client; AppViewHost shows a "newer client
    // required" placeholder for these.
    final v = ViewSpec.fromJson({
      'id': 'experimental',
      'route': '/apps/x',
      'layout': 'hologram_5d',
    });
    expect(v.layout, ViewLayout.unknown);
  });

  test('absent / mistyped fields produce safe defaults, not exceptions', () {
    final v = ViewSpec.fromJson({});
    expect(v.id, '');
    expect(v.route, '');
    expect(v.layout, ViewLayout.unknown);
    expect(v.refreshOn, isEmpty);
    expect(v.toolbar, isEmpty);
  });

  // ─── M16 — grid / dashboard / agent_chat parsing ───────────────

  test('parses grid layout with custom columns + tile config', () {
    final v = ViewSpec.fromJson({
      'id': 'shelf',
      'route': '/apps/shelf/home',
      'layout': 'grid',
      'data_source': {'action': 'list'},
      'item_template': {
        'kind': 'card',
        'title': r'${item.title}',
        'image': r'${item.cover}',
      },
      'grid': {
        'columns': [1, 3, 4],
        'spacing': 16,
        'aspect_ratio': 1.5,
      },
      'detail_view': 'detail',
    });
    expect(v.layout, ViewLayout.grid);
    expect(v.grid, isNotNull);
    expect(v.grid!.columns, [1, 3, 4]);
    expect(v.grid!.spacing, 16);
    expect(v.grid!.aspectRatio, 1.5);
    expect(v.detailView, 'detail');
  });

  test('grid columns fall back to defaults when missing', () {
    final v = ViewSpec.fromJson({
      'id': 'g',
      'route': '/apps/g/home',
      'layout': 'grid',
    });
    // No grid block → grid is null; renderer falls back to ViewGrid().
    expect(v.layout, ViewLayout.grid);
    expect(v.grid, isNull);
  });

  test('grid columnsForWidth picks correctly per breakpoint', () {
    const grid = ViewGrid(columns: [1, 2, 4]);
    expect(grid.columnsForWidth(360), 1);   // narrow
    expect(grid.columnsForWidth(900), 2);   // medium
    expect(grid.columnsForWidth(1600), 4);  // wide
  });

  test('grid columnsForWidth clamps to last entry', () {
    const grid = ViewGrid(columns: [3]); // single value → constant 3
    expect(grid.columnsForWidth(360), 3);
    expect(grid.columnsForWidth(1600), 3);
  });

  test('grid clamps invalid column counts at parse', () {
    final g = ViewGrid.fromJson({'columns': [0, 8, 2]});
    expect(g.columns, [1, 6, 2]); // 0 → 1, 8 → 6
  });

  test('parses dashboard layout with cards', () {
    final v = ViewSpec.fromJson({
      'id': 'overview',
      'route': '/apps/ops/overview',
      'layout': 'dashboard',
      'cards': [
        {
          'id': 'today',
          'title': '今日完成',
          'kind': 'number',
          'span': 4,
          'field': 'data.count',
          'format': 'comma',
          'data_source': {'action': 'stats', 'input': {'range': 'today'}},
        },
        {
          'id': 'recent',
          'kind': 'list',
          'span': 8,
          'data_source': {'action': 'recent'},
        },
      ],
    });
    expect(v.layout, ViewLayout.dashboard);
    expect(v.cards.length, 2);
    expect(v.cards[0].title, '今日完成');
    expect(v.cards[0].kind, 'number');
    expect(v.cards[0].span, 4);
    expect(v.cards[0].field, 'data.count');
    expect(v.cards[0].format, 'comma');
    expect(v.cards[0].dataSource?.action, 'stats');
    expect(v.cards[1].span, 8);
  });

  test('dashboard card span clamps to [1, 12]', () {
    final c1 = ViewCard.fromJson({'id': 'a', 'span': 0});
    final c2 = ViewCard.fromJson({'id': 'b', 'span': 99});
    expect(c1.span, 1);
    expect(c2.span, 12);
  });

  test('dashboard card span defaults to 4 when missing', () {
    final c = ViewCard.fromJson({'id': 'a'});
    expect(c.span, 4);
  });

  test('parses agent_chat layout with config', () {
    final v = ViewSpec.fromJson({
      'id': 'translate',
      'route': '/apps/translate/chat',
      'layout': 'agent_chat',
      'agent_id': '00000000-0000-0000-0000-000000000001',
      'agent_chat': {
        'initial_prompt': r'Translate ${route.id} to English',
        'tool_filter': ['translate.', 'memory.'],
        'system_prompt_override': 'You are a precise translator.',
        'title': 'Translation chat',
      },
    });
    expect(v.layout, ViewLayout.agentChat);
    expect(v.agentId, '00000000-0000-0000-0000-000000000001');
    expect(v.agentChat?.initialPrompt, r'Translate ${route.id} to English');
    expect(v.agentChat?.toolFilter, ['translate.', 'memory.']);
    expect(v.agentChat?.systemPromptOverride, 'You are a precise translator.');
    expect(v.agentChat?.title, 'Translation chat');
  });

  test('agent_chat without config block parses minimally', () {
    final v = ViewSpec.fromJson({
      'id': 'c',
      'route': '/apps/c/chat',
      'layout': 'agent_chat',
      'agent_id': 'xyz',
    });
    expect(v.layout, ViewLayout.agentChat);
    expect(v.agentChat, isNull);
  });
}
