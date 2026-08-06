// A2UI subset — server-driven UI nodes apps push when their view's
// layout=custom (Design §7.3, §21.2).
//
// The shape:
//
//   { "kind": "<kind>", "props": {...}, "children": [...] }
//
// Containers carry children; leaves don't. Both can carry props.
// Anything outside the whitelist below renders as nothing — forward-
// compatible: a v3.0 widget kind shipped in a manifest doesn't crash
// a v2.0 client.
//
// We deliberately keep this layer parser-only. Rendering lives in
// host/a2ui_renderer.dart (Flutter Widget tree); validation lives in
// validation.go on the server side (the client also re-asserts depth
// + count caps as a defense-in-depth).

import 'dart:convert';

/// A2UIKind enumerates every shape the renderer knows. v2.0 ships
/// the design-doc-approved set; later versions add more (chart
/// variants, custom_widget plugin slots) by extending this enum.
enum A2UIKind {
  // Containers — recurse over children.
  column,
  row,
  stack,
  grid,
  // Leaves — render directly from props.
  text,
  image,
  card,
  button,
  input,
  chart,
  markdown,
  progress,
  divider,
  spacer,
  // Forward-compat fallback.
  unknown,
}

A2UIKind _parseKind(String s) {
  switch (s) {
    case 'column':   return A2UIKind.column;
    case 'row':      return A2UIKind.row;
    case 'stack':    return A2UIKind.stack;
    case 'grid':     return A2UIKind.grid;
    case 'text':     return A2UIKind.text;
    case 'image':    return A2UIKind.image;
    case 'card':     return A2UIKind.card;
    case 'button':   return A2UIKind.button;
    case 'input':    return A2UIKind.input;
    case 'chart':    return A2UIKind.chart;
    case 'markdown': return A2UIKind.markdown;
    case 'progress': return A2UIKind.progress;
    case 'divider':  return A2UIKind.divider;
    case 'spacer':   return A2UIKind.spacer;
  }
  return A2UIKind.unknown;
}

bool _isContainer(A2UIKind k) {
  switch (k) {
    case A2UIKind.column:
    case A2UIKind.row:
    case A2UIKind.stack:
    case A2UIKind.grid:
      return true;
    default:
      return false;
  }
}

class A2UINode {
  final A2UIKind kind;
  final String rawKind; // original string (helpful for unknown reporting)
  final Map<String, dynamic> props;
  final List<A2UINode> children;

  const A2UINode({
    required this.kind,
    required this.rawKind,
    this.props = const {},
    this.children = const [],
  });

  bool get isContainer => _isContainer(kind);
  bool get isUnknown => kind == A2UIKind.unknown;

  factory A2UINode.fromJson(Map<String, dynamic> j) {
    final raw = (j['kind'] ?? j['node']) as String? ?? '';
    final k = _parseKind(raw);
    final props = (j['props'] as Map<String, dynamic>?) ?? <String, dynamic>{};
    // Hoist common top-level keys (text/title/src/value/...) into
    // props so renderers don't have to dual-read both shapes. Lets
    // manifests author either:
    //   { "kind": "text", "text": "hi" }
    // or:
    //   { "kind": "text", "props": { "text": "hi" } }
    final merged = <String, dynamic>{...props};
    for (final extra in const [
      'text', 'title', 'subtitle', 'body', 'src', 'image', 'label',
      'placeholder', 'value', 'min', 'max', 'orientation', 'spacing',
      'padding', 'on_click', 'on_submit', 'columns', 'field',
      'icon', 'data', 'kindHint',
    ]) {
      if (j.containsKey(extra) && !merged.containsKey(extra)) {
        merged[extra] = j[extra];
      }
    }
    final childrenRaw = (j['children'] as List?) ?? const [];
    final children = childrenRaw
        .whereType<Map<String, dynamic>>()
        .map(A2UINode.fromJson)
        .toList(growable: false);
    return A2UINode(
      kind:     k,
      rawKind:  raw,
      props:    merged,
      children: children,
    );
  }

  /// Convenience: parse a top-level JSON blob (the typical form a
  /// custom-layout view returns from data_source.action).
  static A2UINode? parse(Object? raw) {
    if (raw is Map<String, dynamic>) return A2UINode.fromJson(raw);
    if (raw is String && raw.isNotEmpty) {
      final decoded = jsonDecode(raw);
      if (decoded is Map<String, dynamic>) return A2UINode.fromJson(decoded);
    }
    return null;
  }
}

// ─── Validation ───────────────────────────────────────────────────
//
// Defense-in-depth: server-side validator catches manifest typos at
// install time, but custom_render payloads are produced at runtime
// by App actions, so we re-validate on the receive side. Failure
// modes are render-time degradations, not crashes.

class A2UIIssue {
  final String code;
  final String message;
  final String path; // dotted ".children[2].text"
  const A2UIIssue({required this.code, required this.message, required this.path});

  @override
  String toString() => '[$code] $path: $message';
}

class A2UIValidationConfig {
  final int maxDepth;
  final int maxNodes;
  final Set<String> allowedActions;
  final Set<String> imageSchemes;

  const A2UIValidationConfig({
    this.maxDepth = 8,
    this.maxNodes = 200,
    this.allowedActions = const {},
    this.imageSchemes = const {'https', 'cas'},
  });
}

class A2UIValidationResult {
  final List<A2UIIssue> issues;
  final int nodeCount;
  final int maxDepth;
  const A2UIValidationResult({
    required this.issues,
    required this.nodeCount,
    required this.maxDepth,
  });

  bool get ok => issues.isEmpty;
  bool get isFatal {
    for (final i in issues) {
      if (i.code == 'depth_exceeded' || i.code == 'node_count_exceeded') {
        return true;
      }
    }
    return false;
  }
}

/// Validate walks the tree once, capping depth + total node count
/// and checking that `on_click.action` references resolve to the
/// caller's allowed actions list.
A2UIValidationResult validateA2UI(A2UINode root, A2UIValidationConfig cfg) {
  final issues = <A2UIIssue>[];
  var nodeCount = 0;
  var observedDepth = 0;
  void walk(A2UINode n, int depth, String path) {
    nodeCount++;
    observedDepth = depth > observedDepth ? depth : observedDepth;
    if (depth > cfg.maxDepth) {
      issues.add(A2UIIssue(
        code: 'depth_exceeded',
        message: 'tree depth $depth exceeds limit ${cfg.maxDepth}',
        path: path,
      ));
      return;
    }
    if (nodeCount > cfg.maxNodes) {
      issues.add(A2UIIssue(
        code: 'node_count_exceeded',
        message: 'total nodes exceeded limit ${cfg.maxNodes}',
        path: path,
      ));
      return;
    }
    if (n.isUnknown && n.rawKind.isNotEmpty) {
      issues.add(A2UIIssue(
        code: 'unknown_kind',
        message: 'unknown node kind "${n.rawKind}" — rendered as empty',
        path: path,
      ));
    }
    // on_click / on_submit must reference allowed actions when set.
    for (final key in const ['on_click', 'on_submit']) {
      final raw = n.props[key];
      if (raw is Map<String, dynamic>) {
        final action = raw['action'] as String? ?? '';
        if (action.isEmpty) {
          continue; // route-only action is checked elsewhere
        }
        if (cfg.allowedActions.isNotEmpty &&
            !cfg.allowedActions.contains(action)) {
          issues.add(A2UIIssue(
            code: 'unknown_action',
            message: 'action "$action" not declared in manifest.actions[]',
            path: '$path.$key',
          ));
        }
      }
    }
    // image src protocol whitelist.
    if (n.kind == A2UIKind.image) {
      final src = (n.props['src'] ?? n.props['image']) as String? ?? '';
      if (src.isNotEmpty) {
        final scheme = _schemeOf(src);
        if (scheme.isNotEmpty && !cfg.imageSchemes.contains(scheme)) {
          issues.add(A2UIIssue(
            code: 'unsafe_image_scheme',
            message: 'image scheme "$scheme" not in whitelist ${cfg.imageSchemes}',
            path: '$path.src',
          ));
        }
      }
    }
    for (var i = 0; i < n.children.length; i++) {
      walk(n.children[i], depth + 1, '$path.children[$i]');
    }
  }
  walk(root, 0, r'$');
  return A2UIValidationResult(
    issues: issues,
    nodeCount: nodeCount,
    maxDepth: observedDepth,
  );
}

String _schemeOf(String url) {
  final idx = url.indexOf(':');
  if (idx <= 0) return '';
  return url.substring(0, idx).toLowerCase();
}
