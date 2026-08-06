/// Activity Feed in-memory model + reducer.
///
/// Folds events from the project sync stream (every kind that lands
/// in ``events.entity_type ∈ {ingest_task, research_task, lint_run,
/// dedup_run, sweep_run}``) into a per-task ``ActivityTask``.
/// The drawer renders these directly; per-page UI like
/// ``IngestActivityPanel`` keeps using the richer ``IngestTaskState``
/// reducer in ``features/sync/ingest_task_state.dart``.
///
/// See ``docs/activity-feed-protocol.md`` §5 for the design.
///
/// Forward-compatibility contract (mirror server):
///   - Unknown ``entity_type`` is routed to [ActivityKind.unknown] so
///     reducer never throws on a future kind.
///   - Unknown ``op`` advances ``lastEventId`` but otherwise no-ops —
///     same idempotency guarantee as ``IngestTaskReducer``.
///   - ``rawPhase`` always carries the original string; UI falls back
///     to it whenever per-kind enums don't recognize the value.
library;

import 'package:flutter/foundation.dart';

/// High-level task taxonomy. The drawer branches on this for icon /
/// color; the reducer for routing entity-type-specific summary fields.
///
/// [unknown] is the catch-all when the server starts emitting a new
/// ``entity_type`` an older client doesn't know. We still record the
/// raw string in [ActivityTask.rawKind] so debug output is meaningful.
enum ActivityKind { ingest, research, lint, dedup, sweep, unknown }

/// Map a server-side ``entity_type`` to the client kind enum.
ActivityKind parseActivityKind(String rawEntityType) {
  switch (rawEntityType) {
    case 'ingest_task':
      return ActivityKind.ingest;
    case 'research_task':
      return ActivityKind.research;
    case 'lint_run':
      return ActivityKind.lint;
    case 'dedup_run':
      return ActivityKind.dedup;
    case 'sweep_run':
      return ActivityKind.sweep;
    default:
      return ActivityKind.unknown;
  }
}

/// Lifecycle status. The drawer collapses anything in
/// [ActivityStatus.running] into the "Running" section and everything
/// terminal into "Recent". [unknown] only exists so a future server-
/// side status string doesn't crash older clients.
enum ActivityStatus { running, done, failed, cancelled, unknown }

/// Translate the server's per-row ``status`` string (used by the REST
/// ``/activity`` endpoint, NOT the WS event ``op``).
ActivityStatus parseActivityStatus(String raw) {
  switch (raw) {
    case 'running':
      return ActivityStatus.running;
    case 'done':
      return ActivityStatus.done;
    case 'failed':
      return ActivityStatus.failed;
    case 'cancelled':
      return ActivityStatus.cancelled;
    default:
      return ActivityStatus.unknown;
  }
}

/// Map a WS event ``op`` to the resulting status. Mirrors the logic
/// in ``server/knowcode/api/activity.py::_list_synchronous``: only
/// terminal ops set a terminal status; ``progress`` / ``phase`` /
/// ``page_planned`` are still "running".
ActivityStatus _statusFromOp(String op) {
  switch (op) {
    case 'done':
      return ActivityStatus.done;
    case 'failed':
      return ActivityStatus.failed;
    case 'cancelled':
      return ActivityStatus.cancelled;
    default:
      // started / phase / page_planned / progress / unknown → running.
      return ActivityStatus.running;
  }
}

/// Threshold below which a successfully-completed task collapses into
/// a single line in the drawer. Failures are never collapsed —
/// operators must see the error reason. The 2 s figure matches
/// ``docs/activity-feed-protocol.md`` §5.5.
const Duration kActivityCollapseThreshold = Duration(seconds: 2);

/// One row in the Activity Feed.
///
/// Replaced (not mutated) on each event so widgets watching individual
/// tasks shortcut on equality. ``summary`` is intentionally a free-form
/// map — different kinds carry different fields, and the drawer cards
/// pick out what's relevant per kind.
@immutable
class ActivityTask {
  const ActivityTask({
    required this.id,
    required this.kind,
    required this.rawKind,
    required this.status,
    required this.label,
    required this.startedAt,
    required this.lastUpdatedAt,
    this.rawPhase,
    this.summary = const <String, Object?>{},
    this.lastEventId = 0,
    this.cancelable = false,
    this.cancelRequested = false,
  });

  /// ``entity_id`` from the events table (UUID string).
  final String id;
  final ActivityKind kind;

  /// Raw server-side ``entity_type``. Useful when [kind] is
  /// [ActivityKind.unknown] so log / debug output still shows what the
  /// server actually sent.
  final String rawKind;

  final ActivityStatus status;

  /// Last ``payload.phase`` string seen (only for kinds that emit phase
  /// events; null otherwise). Drawer cards display this verbatim when
  /// they don't have a per-kind enum mapping.
  final String? rawPhase;

  /// Human-readable card title, e.g. ``Ingest design.pdf``,
  /// ``Lint structural``, ``Research: transformers``.
  final String label;

  /// Kind-specific opaque dict. Stripped of framing fields
  /// (``schema_version``, ``trace_id``, ``task_id``) on entry so
  /// the drawer can iterate without ignoring noise.
  final Map<String, Object?> summary;

  final DateTime startedAt;
  final DateTime lastUpdatedAt;

  /// Largest ``event_id`` already applied. Same idempotency role as
  /// in ``IngestTaskState``.
  final int lastEventId;

  /// True when the kind supports cancellation (v1: ingest only) AND
  /// the task is in flight AND no cancel has been requested yet.
  final bool cancelable;
  final bool cancelRequested;

  /// Wall-clock duration since [startedAt] using the latest event's
  /// timestamp. Used for the collapse decision and for "ran in 412ms"
  /// labels in the recent section.
  Duration get duration => lastUpdatedAt.difference(startedAt);

  /// True when the task is terminal AND its duration is under the
  /// collapse threshold. Failures are excluded — they always render
  /// as full cards so the error is visible.
  bool get isCollapsedRecent {
    if (status != ActivityStatus.done && status != ActivityStatus.cancelled) {
      return false;
    }
    return duration < kActivityCollapseThreshold;
  }

  ActivityTask copyWith({
    ActivityKind? kind,
    String? rawKind,
    ActivityStatus? status,
    Object? rawPhase = _unset,
    String? label,
    Map<String, Object?>? summary,
    DateTime? startedAt,
    DateTime? lastUpdatedAt,
    int? lastEventId,
    bool? cancelable,
    bool? cancelRequested,
  }) =>
      ActivityTask(
        id: id,
        kind: kind ?? this.kind,
        rawKind: rawKind ?? this.rawKind,
        status: status ?? this.status,
        rawPhase: identical(rawPhase, _unset)
            ? this.rawPhase
            : rawPhase as String?,
        label: label ?? this.label,
        summary: summary ?? this.summary,
        startedAt: startedAt ?? this.startedAt,
        lastUpdatedAt: lastUpdatedAt ?? this.lastUpdatedAt,
        lastEventId: lastEventId ?? this.lastEventId,
        cancelable: cancelable ?? this.cancelable,
        cancelRequested: cancelRequested ?? this.cancelRequested,
      );

  static const Object _unset = Object();
}

/// Pure reducer over ``Map<id, ActivityTask>``. Decoupled from
/// Flutter / Riverpod for unit testability — the notifier wraps it.
///
/// Two entry points:
///   - [apply] for one decoded WS event (``catchup`` / ``live``).
///   - [applyBackfill] for the REST cold-start response.
///
/// Both go through [_mergeStarted] / per-op transitions so the result
/// is identical regardless of source.
class ActivityFeedReducer {
  ActivityFeedReducer({Map<String, ActivityTask>? initial})
      : _state = <String, ActivityTask>{...?initial};

  final Map<String, ActivityTask> _state;

  Map<String, ActivityTask> get state => Map.unmodifiable(_state);

  ActivityTask? operator [](String id) => _state[id];

  /// Apply one ``live``-frame event dict (or one element of a
  /// ``catchup`` batch). Returns the updated [ActivityTask], or null
  /// when the event was ignored (missing fields, stale event_id, or
  /// an unknown entity_type we don't represent).
  ///
  /// Idempotent: events whose ``event_id`` is at or below the task's
  /// current ``lastEventId`` are dropped so a reconnect-driven catchup
  /// doesn't double-apply.
  ActivityTask? apply(Map<Object?, Object?> event) {
    final entity = event['entity'];
    final op = event['op'];
    final entityId = event['entity_id'];
    final eventId = event['event_id'];
    if (entity is! String || op is! String || entityId is! String ||
        eventId is! int) {
      return null;
    }

    final kind = parseActivityKind(entity);
    final payload = event['payload'];
    final p = payload is Map ? payload : const <Object?, Object?>{};

    final prev = _state[entityId];
    if (prev != null && eventId <= prev.lastEventId) {
      // Duplicate (catchup-after-reconnect rebroadcast). Already applied.
      return prev;
    }

    final summary = _summaryFromPayload(p);
    final now = DateTime.now();

    ActivityTask next;
    if (op == 'started' || prev == null) {
      // First-seen task: synthesize the row from this event regardless
      // of which op landed first. Operators occasionally point a fresh
      // client at a project mid-run; we'd rather show partial state
      // than nothing.
      next = ActivityTask(
        id: entityId,
        kind: kind,
        rawKind: entity,
        status: _statusFromOp(op),
        label: prev?.label ?? _labelFor(kind, p),
        rawPhase: _phaseFromPayload(p) ?? prev?.rawPhase,
        summary: summary,
        startedAt: prev?.startedAt ?? now,
        lastUpdatedAt: now,
        lastEventId: eventId,
        cancelable: kind == ActivityKind.ingest &&
            _statusFromOp(op) == ActivityStatus.running,
        cancelRequested: prev?.cancelRequested ?? false,
      );
    } else {
      // Subsequent op: merge into existing row. Status transitions
      // by op; summary fields accumulate.
      final mergedSummary = <String, Object?>{...prev.summary, ...summary};
      next = prev.copyWith(
        status: _statusFromOp(op),
        rawPhase: _phaseFromPayload(p) ?? prev.rawPhase,
        summary: mergedSummary,
        lastUpdatedAt: now,
        lastEventId: eventId,
        // Once a task hits a terminal state, cancelable goes false.
        cancelable: prev.cancelable && _statusFromOp(op) == ActivityStatus.running,
      );
    }
    _state[entityId] = next;
    return next;
  }

  /// Apply a list of decoded WS events at once. Single notification at
  /// the end so widgets don't churn through the catchup backlog.
  ///
  /// Returns true when at least one event modified state.
  bool applyBatch(Iterable<Object?> events) {
    var changed = false;
    for (final e in events) {
      if (e is! Map) continue;
      final result = apply(e.cast<Object?, Object?>());
      if (result != null) changed = true;
    }
    return changed;
  }

  /// Seed the reducer from a REST ``/v1/projects/{p}/activity`` response.
  ///
  /// Each DTO is already in ActivityTask shape on the wire. We replace
  /// any existing entry with the backfill version because the REST
  /// endpoint is authoritative for cold-start state — it already
  /// projected events.id ordering server-side.
  ///
  /// ``lastEventId`` is left at 0 for backfill rows because the REST
  /// endpoint doesn't expose the underlying event_id. Subsequent live
  /// events with higher event_id will overlay correctly.
  void applyBackfill(Iterable<Map<String, Object?>> rows) {
    for (final row in rows) {
      final id = row['id'];
      if (id is! String) continue;
      final entity = (row['raw_kind'] as String?) ?? '';
      final kind = parseActivityKind(entity);
      final status = parseActivityStatus(
        (row['status'] as String?) ?? 'unknown',
      );
      final label = (row['label'] as String?) ?? entity;
      final phase = row['phase'] as String?;
      final summary = (row['summary'] is Map)
          ? Map<String, Object?>.from(row['summary']! as Map)
          : <String, Object?>{};
      final startedAt = _parseDateTime(row['started_at']) ?? DateTime.now();
      final updatedAt = _parseDateTime(row['last_updated_at']) ?? startedAt;
      final cancelable = (row['cancelable'] as bool?) ?? false;
      _state[id] = ActivityTask(
        id: id,
        kind: kind,
        rawKind: entity,
        status: status,
        rawPhase: phase,
        label: label,
        summary: summary,
        startedAt: startedAt,
        lastUpdatedAt: updatedAt,
        lastEventId: 0,
        cancelable: cancelable,
      );
    }
  }
}

// ── helpers ─────────────────────────────────────────────────────────


/// Strip the framing fields from an event payload, leaving only the
/// kind-specific data the drawer cares about.
Map<String, Object?> _summaryFromPayload(Map<Object?, Object?> raw) {
  final out = <String, Object?>{};
  raw.forEach((k, v) {
    if (k is! String) return;
    if (k == 'schema_version' || k == 'trace_id' || k == 'task_id') return;
    out[k] = v;
  });
  return out;
}

String? _phaseFromPayload(Map<Object?, Object?> p) {
  final phase = p['phase'];
  return phase is String ? phase : null;
}

/// Server-side label generation, mirrored. Keeps the offline path
/// (live events) consistent with the cold-start REST response.
///
/// We only construct the label on the *first* event for a task —
/// subsequent transitions reuse the original label so a
/// progress event doesn't overwrite "Ingest design.pdf" with
/// nothing useful.
String _labelFor(ActivityKind kind, Map<Object?, Object?> p) {
  switch (kind) {
    case ActivityKind.ingest:
      final filename = p['source_filename'];
      return filename is String ? 'Ingest $filename' : 'Ingest';
    case ActivityKind.research:
      final topic = p['topic'];
      if (topic is String && topic.isNotEmpty) {
        return 'Research: ${_truncate(topic, 60)}';
      }
      return 'Research';
    case ActivityKind.lint:
      final k = p['kind'];
      return k is String && k.isNotEmpty ? 'Lint $k' : 'Lint';
    case ActivityKind.dedup:
      final k = p['kind'];
      if (k == 'merge') {
        final canonical = p['canonical_slug'];
        return canonical is String && canonical.isNotEmpty
            ? 'Dedup merge → $canonical'
            : 'Dedup merge';
      }
      return k is String && k.isNotEmpty ? 'Dedup $k' : 'Dedup';
    case ActivityKind.sweep:
      return 'Sweep reviews';
    case ActivityKind.unknown:
      return 'Task';
  }
}

String _truncate(String text, int n) {
  if (text.length <= n) return text;
  return '${text.substring(0, n - 1)}…';
}

DateTime? _parseDateTime(Object? raw) {
  if (raw is String) return DateTime.tryParse(raw);
  if (raw is DateTime) return raw;
  return null;
}
