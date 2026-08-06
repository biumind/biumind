// Pure-function helpers for the frontmatter editor + panel.
//
// All field coercion / list mutation / dirty-detection lives here so
// the widgets stay shells. Keeping these stand-alone keeps unit tests
// MaterialApp-free and lets future code (export/import, MCP tools)
// reuse the same shape rules without going through Flutter widgets.

/// Frontmatter keys we surface as first-class form fields. Order
/// matters: the editor renders them in this order so users see a
/// consistent layout regardless of how the underlying YAML was sorted.
const List<String> kKnownFrontmatterKeys = <String>[
  'title',
  'type',
  'created',
  'updated',
  'description',
  'origin',
  'tags',
  'sources',
  'related',
];

/// Built-in page type vocabulary surfaced in the type dropdown. Custom
/// strings are accepted (server doesn't validate); the editor offers
/// "自定义…" as an escape hatch.
///
/// `query` matches what deep-research worker writes (see
/// services/brain/internal/wiki/research/research.go).
const List<String> kKnownPageTypes = <String>[
  'entity',
  'concept',
  'source',
  'query',
  'comparison',
  'synthesis',
  'overview',
];

/// Coerce any frontmatter value to a string. Lists / maps degrade to
/// empty so a TextField can bind without crashing on legacy data.
String stringFieldValue(Object? value) {
  if (value is String) return value;
  if (value is num || value is bool) return value.toString();
  return '';
}

/// Coerce any frontmatter value to a list of trimmed non-empty strings.
/// Handles the three shapes our backends emit: list of scalars, single
/// scalar (treat as 1-element), or null/other (empty).
List<String> listFieldValue(Object? value) {
  if (value is List) {
    return value
        .where((v) => v != null)
        .map((v) => v.toString().trim())
        .where((s) => s.isNotEmpty)
        .toList(growable: false);
  }
  if (value is String && value.trim().isNotEmpty) {
    return <String>[value.trim()];
  }
  return const <String>[];
}

/// Set [key] to [value], or remove it when the value is empty
/// (empty string, empty list). Returns a NEW map; the input is
/// not mutated. Empty-value drop keeps the persisted JSON clean —
/// `description: ""` style noise never lands.
Map<String, dynamic> setField(
  Map<String, dynamic> fm,
  String key,
  Object? value,
) {
  final next = Map<String, dynamic>.of(fm);
  if (_isEmptyValue(value)) {
    next.remove(key);
  } else {
    next[key] = value;
  }
  return next;
}

bool _isEmptyValue(Object? value) {
  if (value == null) return true;
  if (value is String) return value.trim().isEmpty;
  if (value is List) return value.isEmpty;
  return false;
}

/// Append [item] to the list at [key]. Trims, dedupes case-insensitively
/// (first-seen casing wins), no-ops on blank input.
Map<String, dynamic> addToListField(
  Map<String, dynamic> fm,
  String key,
  String item,
) {
  final trimmed = item.trim();
  if (trimmed.isEmpty) return fm;
  final current = listFieldValue(fm[key]);
  for (final existing in current) {
    if (existing.toLowerCase() == trimmed.toLowerCase()) return fm;
  }
  return setField(fm, key, [...current, trimmed]);
}

/// Remove the entry at [index] from the list at [key].
Map<String, dynamic> removeFromListField(
  Map<String, dynamic> fm,
  String key,
  int index,
) {
  final current = listFieldValue(fm[key]);
  if (index < 0 || index >= current.length) return fm;
  return setField(fm, key, [...current]..removeAt(index));
}

/// Rename a key while preserving its value + insertion order. No-op on
/// collision (caller signals validation error). Used by the "extras"
/// section where users edit the key string itself.
Map<String, dynamic> renameKey(
  Map<String, dynamic> fm,
  String oldKey,
  String newKey,
) {
  if (oldKey == newKey) return fm;
  if (!fm.containsKey(oldKey)) return fm;
  if (newKey.trim().isEmpty) return fm;
  if (fm.containsKey(newKey)) return fm;
  final next = <String, dynamic>{};
  fm.forEach((k, v) {
    if (k == oldKey) {
      next[newKey] = v;
    } else {
      next[k] = v;
    }
  });
  return next;
}

/// True when [a] and [b] differ in any (key, value) pair. Order-
/// sensitive on lists (a tag reorder IS a real edit users care about).
bool isDirty(Map<String, dynamic> a, Map<String, dynamic> b) {
  if (a.length != b.length) return true;
  for (final key in a.keys) {
    if (!b.containsKey(key)) return true;
    if (!_valuesEqual(a[key], b[key])) return true;
  }
  return false;
}

bool _valuesEqual(Object? x, Object? y) {
  if (x == y) return true;
  if (x is List && y is List) {
    if (x.length != y.length) return false;
    for (var i = 0; i < x.length; i++) {
      if (!_valuesEqual(x[i], y[i])) return false;
    }
    return true;
  }
  if (x is Map && y is Map) {
    if (x.length != y.length) return false;
    for (final key in x.keys) {
      if (!y.containsKey(key)) return false;
      if (!_valuesEqual(x[key], y[key])) return false;
    }
    return true;
  }
  return false;
}

/// Strip a `[[Page]]` / `[[Page|alias]]` wrapper around a raw `related`
/// entry. Used by the read-only panel to show clean labels.
String unwrapWikilinkLabel(String raw) {
  final s = raw.trim();
  if (!s.startsWith('[[') || !s.endsWith(']]')) return s;
  final inner = s.substring(2, s.length - 2);
  final pipe = inner.indexOf('|');
  return (pipe < 0 ? inner : inner.substring(pipe + 1)).trim();
}

/// Return the slug (link target) inside a `[[…]]` wrapper, or the raw
/// string when no wrapper is present. Used to wire the related-chip
/// onTap to the wikilink resolver.
String unwrapWikilinkSlug(String raw) {
  final s = raw.trim();
  if (!s.startsWith('[[') || !s.endsWith(']]')) return s;
  final inner = s.substring(2, s.length - 2);
  final pipe = inner.indexOf('|');
  return (pipe < 0 ? inner : inner.substring(0, pipe)).trim();
}
