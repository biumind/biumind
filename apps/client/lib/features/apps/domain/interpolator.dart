// Template interpolation engine for view manifest strings.
//
// Manifests carry templated strings that the runtime client expands
// against (item, data, route, config, user) contexts. Examples from
// rss App's manifest:
//
//   ${item.title}
//   ${item.unread} 未读 · ${item.last_at | relative_time}
//   /apps/rss/feed/${item.id}
//   ${item.body | truncate(120)}
//
// Syntax (deliberately small):
//   ${path}                       — dotted accessor; missing → ''
//   ${path | filter}              — single filter
//   ${path | filter(arg, arg)}    — filter with positional args
//   ${path | filter(arg) | next}  — pipeline (filters chain L→R)
//   $${ ... }                     — escape; renders literally as ${...}
//
// Six built-in filters (matches Design §6.3):
//   relative_time   ('5 分钟前')
//   truncate(N)     (clamp to N chars + ellipsis)
//   escape_html     (XSS guard — used in card body)
//   date(fmt)       (yyyy-MM-dd / HH:mm — minimal subset)
//   domain          ("example.com" from a URL)
//   default(value)  (substitute when path resolves empty)
//
// Anything we don't recognize falls through as the raw string —
// surfacing the unsupported filter in the UI rather than crashing.

import 'dart:convert';

/// Context the interpolator pulls names from. Keys are top-level
/// roots ("item", "data", "route", "config", "user", "i18n", "field").
typedef InterpContext = Map<String, dynamic>;

/// Render returns the interpolated string. Missing path → empty.
class Interpolator {
  final InterpContext context;
  final DateTime Function() now;

  Interpolator(this.context, {DateTime Function()? now}) : now = now ?? DateTime.now;

  /// Renders [template], replacing every ${...} fragment.
  String render(String template) {
    if (template.isEmpty) return template;
    final out = StringBuffer();
    int i = 0;
    while (i < template.length) {
      // Escape: $${ ... } renders as ${...}
      if (i + 2 < template.length &&
          template[i] == r'$' &&
          template[i + 1] == r'$' &&
          template[i + 2] == '{') {
        final end = template.indexOf('}', i + 3);
        if (end < 0) {
          out.write(template.substring(i));
          break;
        }
        out.write(r'${');
        out.write(template.substring(i + 3, end));
        out.write('}');
        i = end + 1;
        continue;
      }
      if (i + 1 < template.length && template[i] == r'$' && template[i + 1] == '{') {
        final end = template.indexOf('}', i + 2);
        if (end < 0) {
          // Unterminated — leak the rest of the template literally.
          out.write(template.substring(i));
          break;
        }
        final expr = template.substring(i + 2, end);
        out.write(_renderExpr(expr));
        i = end + 1;
        continue;
      }
      out.write(template[i]);
      i++;
    }
    return out.toString();
  }

  String _renderExpr(String expr) {
    // expr = "path[ | filter[(args)] ]*"
    final parts = expr.split('|').map((s) => s.trim()).toList();
    if (parts.isEmpty) return '';
    var value = _resolvePath(parts.first);
    for (final filter in parts.skip(1)) {
      value = _applyFilter(filter, value);
    }
    return _stringify(value);
  }

  /// Looks up "a.b.c" in the context. Each step descends a Map; on
  /// missing key returns null. List indexing via [N] is intentionally
  /// not supported — keeps the engine small + matches Design §6.3.
  dynamic _resolvePath(String path) {
    if (path.isEmpty) return '';
    final segments = path.split('.');
    dynamic current = context;
    for (final seg in segments) {
      if (current is Map) {
        current = current[seg];
      } else {
        return null;
      }
    }
    return current;
  }

  dynamic _applyFilter(String spec, dynamic value) {
    // spec = "name" or "name(arg1, arg2, ...)"
    final parenIdx = spec.indexOf('(');
    String name;
    List<String> args;
    if (parenIdx < 0) {
      name = spec;
      args = const [];
    } else {
      name = spec.substring(0, parenIdx).trim();
      final tail = spec.substring(parenIdx + 1);
      final closeIdx = tail.lastIndexOf(')');
      if (closeIdx < 0) return value; // malformed; pass through
      args = _splitArgs(tail.substring(0, closeIdx));
    }
    switch (name) {
      case 'relative_time': return _relativeTime(value);
      case 'truncate':      return _truncate(value, args);
      case 'escape_html':   return _escapeHtml(value);
      case 'date':          return _formatDate(value, args);
      case 'domain':        return _domain(value);
      case 'default':       return _default(value, args);
    }
    return value;
  }

  // ─── Filter implementations ──────────────────────────────

  String _relativeTime(dynamic v) {
    final t = _parseTime(v);
    if (t == null) return _stringify(v);
    final delta = now().difference(t);
    if (delta.inSeconds < 60) return '刚刚';
    if (delta.inMinutes < 60) return '${delta.inMinutes} 分钟前';
    if (delta.inHours < 24) return '${delta.inHours} 小时前';
    if (delta.inDays < 30) return '${delta.inDays} 天前';
    return '${(delta.inDays / 30).floor()} 个月前';
  }

  String _truncate(dynamic v, List<String> args) {
    final s = _stringify(v);
    if (args.isEmpty) return s;
    final n = int.tryParse(args.first) ?? s.length;
    if (s.length <= n) return s;
    return '${s.substring(0, n)}…';
  }

  String _escapeHtml(dynamic v) {
    return _stringify(v)
        .replaceAll('&', '&amp;')
        .replaceAll('<', '&lt;')
        .replaceAll('>', '&gt;')
        .replaceAll('"', '&quot;')
        .replaceAll("'", '&#39;');
  }

  String _formatDate(dynamic v, List<String> args) {
    final t = _parseTime(v);
    if (t == null) return _stringify(v);
    final fmt = args.isNotEmpty ? args.first : 'yyyy-MM-dd';
    return _applyDateFormat(t.toLocal(), fmt);
  }

  String _applyDateFormat(DateTime t, String fmt) {
    String two(int n) => n.toString().padLeft(2, '0');
    return fmt
        .replaceAll('yyyy', t.year.toString().padLeft(4, '0'))
        .replaceAll('MM',   two(t.month))
        .replaceAll('dd',   two(t.day))
        .replaceAll('HH',   two(t.hour))
        .replaceAll('mm',   two(t.minute))
        .replaceAll('ss',   two(t.second));
  }

  String _domain(dynamic v) {
    final s = _stringify(v);
    if (s.isEmpty) return '';
    try {
      return Uri.parse(s).host;
    } catch (_) {
      return s;
    }
  }

  dynamic _default(dynamic v, List<String> args) {
    if (v == null || (v is String && v.isEmpty)) {
      return args.isEmpty ? '' : args.first;
    }
    return v;
  }

  // ─── Helpers ─────────────────────────────────────────────

  /// Splits "a, 'b, c', d" into ['a', 'b, c', 'd']. Tiny parser —
  /// only single quotes survive comma-aware splitting because that's
  /// all the manifest filters need (most args are integers / format
  /// strings, no nested parens).
  List<String> _splitArgs(String raw) {
    if (raw.isEmpty) return const [];
    final out = <String>[];
    final buf = StringBuffer();
    var inQuote = false;
    for (var i = 0; i < raw.length; i++) {
      final c = raw[i];
      if (c == "'" || c == '"') {
        inQuote = !inQuote;
        continue;
      }
      if (c == ',' && !inQuote) {
        out.add(buf.toString().trim());
        buf.clear();
        continue;
      }
      buf.write(c);
    }
    final tail = buf.toString().trim();
    if (tail.isNotEmpty) out.add(tail);
    return out;
  }

  String _stringify(dynamic v) {
    if (v == null) return '';
    if (v is String) return v;
    if (v is num || v is bool) return v.toString();
    // Maps / lists are non-trivial to render; let the caller see the
    // JSON shape rather than Dart's `[Instance of '...]'`.
    try {
      return const JsonEncoder().convert(v);
    } catch (_) {
      return v.toString();
    }
  }

  DateTime? _parseTime(dynamic v) {
    if (v is DateTime) return v;
    if (v is String && v.isNotEmpty) return DateTime.tryParse(v);
    if (v is int) {
      // milliseconds since epoch heuristic: < 10^12 = seconds.
      return v < 100000000000
          ? DateTime.fromMillisecondsSinceEpoch(v * 1000, isUtc: true)
          : DateTime.fromMillisecondsSinceEpoch(v, isUtc: true);
    }
    return null;
  }
}
