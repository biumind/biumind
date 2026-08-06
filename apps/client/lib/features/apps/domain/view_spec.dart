// View spec — Dart-side mirror of biuapp.ViewSpec from
// packages/go-sdk/biu/biuapp/manifest.go.
//
// Manifests come over the wire as opaque JSON maps; this module
// turns them into typed Dart classes so renderer code (host /
// layouts/) doesn't pepper the codebase with `j['layout'] as String?`
// dispatch and key typos.
//
// We don't try to be exhaustive — only the fields the v1.5
// renderer reads. Unknown fields survive in the original map so
// future client versions can roundtrip them without dropping data.

class ViewSpec {
  final String id;
  final String route;
  final String title;
  final ViewLayout layout;
  final ViewDataSource? dataSource;
  final List<String> refreshOn;
  final ViewItemTemplate? itemTemplate;
  final String detailView;
  final List<ViewActionRef> toolbar;
  final String schemaRef;
  final FormSubmit? submit;
  final String url;
  final String agentId;
  final List<ViewCard> cards;
  final ViewGrid? grid;
  final ViewAgentChat? agentChat;
  final ViewPagination? pagination;

  const ViewSpec({
    required this.id,
    required this.route,
    required this.title,
    required this.layout,
    this.dataSource,
    this.refreshOn = const [],
    this.itemTemplate,
    this.detailView = '',
    this.toolbar = const [],
    this.schemaRef = '',
    this.submit,
    this.url = '',
    this.agentId = '',
    this.cards = const [],
    this.grid,
    this.agentChat,
    this.pagination,
  });

  factory ViewSpec.fromJson(Map<String, dynamic> j) {
    return ViewSpec(
      id:           j['id'] as String? ?? '',
      route:        j['route'] as String? ?? '',
      title:        j['title'] as String? ?? '',
      layout:       _parseLayout(j['layout'] as String? ?? ''),
      dataSource:   _opt(j['data_source'], ViewDataSource.fromJson),
      refreshOn:    _stringList(j['refresh_on']),
      itemTemplate: _opt(j['item_template'], ViewItemTemplate.fromJson),
      detailView:   j['detail_view'] as String? ?? '',
      toolbar:      _list(j['toolbar'], ViewActionRef.fromJson),
      schemaRef:    j['schema_ref'] as String? ?? '',
      submit:       _opt(j['submit'], FormSubmit.fromJson),
      url:          j['url'] as String? ?? '',
      agentId:      j['agent_id'] as String? ?? '',
      cards:        _list(j['cards'], ViewCard.fromJson),
      grid:         _opt(j['grid'], ViewGrid.fromJson),
      agentChat:    _opt(j['agent_chat'], ViewAgentChat.fromJson),
      pagination:   _opt(j['pagination'], ViewPagination.fromJson),
    );
  }
}

enum ViewLayout {
  list,
  listDetail,
  form,
  webView,
  grid,        // v2.0
  dashboard,   // v2.0
  agentChat,   // v2.0
  custom,      // v2.0
  unknown,     // forward-compat: render a "this view requires a newer client" placeholder
}

ViewLayout _parseLayout(String s) {
  switch (s) {
    case 'list':         return ViewLayout.list;
    case 'list_detail':  return ViewLayout.listDetail;
    case 'form':         return ViewLayout.form;
    case 'webview':      return ViewLayout.webView;
    case 'grid':         return ViewLayout.grid;
    case 'dashboard':    return ViewLayout.dashboard;
    case 'agent_chat':   return ViewLayout.agentChat;
    case 'custom':       return ViewLayout.custom;
  }
  return ViewLayout.unknown;
}

class ViewDataSource {
  final String action;
  final Map<String, dynamic> input;

  const ViewDataSource({required this.action, this.input = const {}});

  factory ViewDataSource.fromJson(Map<String, dynamic> j) => ViewDataSource(
        action: j['action'] as String? ?? '',
        input: (j['input'] as Map<String, dynamic>?) ?? const {},
      );
}

class ViewItemTemplate {
  final String kind;
  final String title;
  final String subtitle;
  final String body;
  final String image;
  final List<ViewActionRef> actions;
  final String widgetType;
  final Map<String, dynamic> props;

  const ViewItemTemplate({
    required this.kind,
    this.title = '',
    this.subtitle = '',
    this.body = '',
    this.image = '',
    this.actions = const [],
    this.widgetType = '',
    this.props = const {},
  });

  factory ViewItemTemplate.fromJson(Map<String, dynamic> j) =>
      ViewItemTemplate(
        kind:       j['kind'] as String? ?? 'text',
        title:      j['title'] as String? ?? '',
        subtitle:   j['subtitle'] as String? ?? '',
        body:       j['body'] as String? ?? '',
        image:      j['image'] as String? ?? '',
        actions:    _list(j['actions'], ViewActionRef.fromJson),
        widgetType: j['widget_type'] as String? ?? '',
        props:      (j['props'] as Map<String, dynamic>?) ?? const {},
      );
}

class ViewActionRef {
  final String label;
  final String icon;
  final String action;
  final Map<String, dynamic> input;
  final String route;
  final String confirm;
  final String riskWarning;
  final ViewActionEffect? onSuccess;

  const ViewActionRef({
    required this.label,
    this.icon = '',
    this.action = '',
    this.input = const {},
    this.route = '',
    this.confirm = '',
    this.riskWarning = '',
    this.onSuccess,
  });

  factory ViewActionRef.fromJson(Map<String, dynamic> j) => ViewActionRef(
        label:       j['label'] as String? ?? '',
        icon:        j['icon'] as String? ?? '',
        action:      j['action'] as String? ?? '',
        input:       (j['input'] as Map<String, dynamic>?) ?? const {},
        route:       j['route'] as String? ?? '',
        confirm:     j['confirm'] as String? ?? '',
        riskWarning: j['risk_warning'] as String? ?? '',
        onSuccess:   _opt(j['on_success'], ViewActionEffect.fromJson),
      );
}

class ViewActionEffect {
  final String toast;
  final bool refresh;
  final String navigate;

  const ViewActionEffect({this.toast = '', this.refresh = false, this.navigate = ''});

  factory ViewActionEffect.fromJson(Map<String, dynamic> j) => ViewActionEffect(
        toast:    j['toast'] as String? ?? '',
        refresh:  j['refresh'] as bool? ?? false,
        navigate: j['navigate'] as String? ?? '',
      );
}

class FormSubmit {
  final String action;
  final ViewActionEffect? onSuccess;

  const FormSubmit({required this.action, this.onSuccess});

  factory FormSubmit.fromJson(Map<String, dynamic> j) => FormSubmit(
        action:    j['action'] as String? ?? '',
        onSuccess: _opt(j['on_success'], ViewActionEffect.fromJson),
      );
}

class ViewCard {
  final String id;
  final String title;
  final String kind;     // text | number | list | chart
  final ViewDataSource? dataSource;
  final int span;        // 1..12 — defaults to 4
  final String field;    // dotted path into card response; "" → whole payload
  final String format;   // "comma" | "percent" | ""

  const ViewCard({
    required this.id,
    this.title = '',
    this.kind = 'text',
    this.dataSource,
    this.span = 4,
    this.field = '',
    this.format = '',
  });

  factory ViewCard.fromJson(Map<String, dynamic> j) {
    var s = (j['span'] as num?)?.toInt() ?? 4;
    if (s < 1) s = 1;
    if (s > 12) s = 12;
    return ViewCard(
      id:         j['id'] as String? ?? '',
      title:      j['title'] as String? ?? '',
      kind:       j['kind'] as String? ?? 'text',
      dataSource: _opt(j['data_source'], ViewDataSource.fromJson),
      span:       s,
      field:      j['field'] as String? ?? '',
      format:     j['format'] as String? ?? '',
    );
  }
}

class ViewGrid {
  /// Columns at narrow / medium / wide breakpoints. The renderer picks
  /// `columns[breakpointIndex]` (clamped to last entry when fewer than
  /// 3 are supplied). Empty list → defaults [1, 2, 3].
  final List<int> columns;
  final int spacing;
  final double aspectRatio;

  const ViewGrid({
    this.columns = const [1, 2, 3],
    this.spacing = 12,
    this.aspectRatio = 1.0,
  });

  factory ViewGrid.fromJson(Map<String, dynamic> j) {
    final cols = (j['columns'] as List?)
            ?.whereType<num>()
            .map((n) {
              var i = n.toInt();
              if (i < 1) i = 1;
              if (i > 6) i = 6;
              return i;
            })
            .toList(growable: false) ??
        const [1, 2, 3];
    return ViewGrid(
      columns:     cols.isEmpty ? const [1, 2, 3] : cols,
      spacing:     (j['spacing'] as num?)?.toInt() ?? 12,
      aspectRatio: (j['aspect_ratio'] as num?)?.toDouble() ?? 1.0,
    );
  }

  /// Picks the column count for a given screen width.
  /// Breakpoints follow Material guidelines: ≤600 narrow, 600-1200 medium, >1200 wide.
  int columnsForWidth(double width) {
    final c = columns;
    if (c.isEmpty) return 1;
    int idx;
    if (width <= 600) {
      idx = 0;
    } else if (width <= 1200) {
      idx = 1;
    } else {
      idx = 2;
    }
    if (idx >= c.length) idx = c.length - 1;
    return c[idx];
  }
}

class ViewAgentChat {
  final String initialPrompt;
  final List<String> toolFilter;
  final String systemPromptOverride;
  final String title;

  const ViewAgentChat({
    this.initialPrompt = '',
    this.toolFilter = const [],
    this.systemPromptOverride = '',
    this.title = '',
  });

  factory ViewAgentChat.fromJson(Map<String, dynamic> j) => ViewAgentChat(
        initialPrompt:        j['initial_prompt'] as String? ?? '',
        toolFilter:           _stringList(j['tool_filter']),
        systemPromptOverride: j['system_prompt_override'] as String? ?? '',
        title:                j['title'] as String? ?? '',
      );
}

class ViewPagination {
  final String pageParam;
  final String totalField;
  final int pageSize;

  const ViewPagination({
    this.pageParam = 'page',
    this.totalField = '',
    this.pageSize = 20,
  });

  factory ViewPagination.fromJson(Map<String, dynamic> j) => ViewPagination(
        pageParam:  j['page_param'] as String? ?? 'page',
        totalField: j['total_field'] as String? ?? '',
        pageSize:   j['page_size'] as int? ?? 20,
      );
}

// ─── Helpers ────────────────────────────────────────────────

T? _opt<T>(Object? raw, T Function(Map<String, dynamic>) fromJson) {
  if (raw is Map<String, dynamic>) return fromJson(raw);
  return null;
}

List<T> _list<T>(Object? raw, T Function(Map<String, dynamic>) fromJson) {
  if (raw is! List) return const [];
  return raw
      .whereType<Map<String, dynamic>>()
      .map(fromJson)
      .toList(growable: false);
}

List<String> _stringList(Object? raw) {
  if (raw is! List) return const [];
  return raw.whereType<String>().toList(growable: false);
}
