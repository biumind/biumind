// CollabPresence — co-editing cursor & active-viewer tracking.
//
// Different from device-level Presence (online/offline); this tracks "who is
// looking at this exact resource RIGHT NOW" with cursor / selection state.
//
// MVP: data types + abstract service; backed by Realtime topic
// `<resource_kind>:presence:<id>` (e.g. `wiki:presence:<page_id>`) once the
// server-side Presence subscription emits these.

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class CursorState {
  final String blockId;
  final int offset;
  final int? selectionStart;
  final int? selectionEnd;
  const CursorState({
    required this.blockId,
    required this.offset,
    this.selectionStart,
    this.selectionEnd,
  });
}

class RemoteCursor {
  final String userId;
  final String displayName;
  final Color color;
  final String? avatarUrl;
  final CursorState? state;
  final DateTime? lastSeen;
  const RemoteCursor({
    required this.userId,
    required this.displayName,
    required this.color,
    this.avatarUrl,
    this.state,
    this.lastSeen,
  });
}

abstract interface class CollabPresence {
  /// Watch remote cursors on a resource (e.g. wiki://page/<id>).
  Stream<List<RemoteCursor>> watch(String resourceUri);

  /// Push local cursor update; throttled by caller (recommend 200ms).
  Future<void> updateCursor(String resourceUri, CursorState state);

  /// Notify server that we left the resource view.
  Future<void> leave(String resourceUri);
}

/// Stub implementation used in tests / dev.
class StubCollabPresence implements CollabPresence {
  final Map<String, StreamController<List<RemoteCursor>>> _ctrls = {};

  @override
  Stream<List<RemoteCursor>> watch(String resourceUri) {
    return _ctrls.putIfAbsent(
      resourceUri,
      () => StreamController.broadcast(),
    ).stream;
  }

  @override
  Future<void> updateCursor(String uri, CursorState state) async {
    // Stub: no-op. Real impl publishes to NATS via Realtime.
  }

  @override
  Future<void> leave(String uri) async {
    final c = _ctrls.remove(uri);
    await c?.close();
  }

  /// Test helper: inject a remote cursor list for a resource.
  void debugEmit(String uri, List<RemoteCursor> cursors) {
    _ctrls[uri]?.add(cursors);
  }
}

final collabPresenceProvider = Provider<CollabPresence>((ref) {
  return StubCollabPresence();
});

/// Deterministic color per user id (until server provides accent).
Color colorForUser(String userId) {
  final hash = userId.codeUnits.fold<int>(0, (a, b) => (a + b * 31) & 0xFFFFFFFF);
  final hue = (hash % 360).toDouble();
  return HSVColor.fromAHSV(1.0, hue, 0.55, 0.85).toColor();
}
