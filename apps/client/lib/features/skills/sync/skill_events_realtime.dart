// SkillEventsRealtime — listens on `org:<orgId>:skill_events` and
// keeps the skills list reactive across devices/users.
//
// Server side: services/runtime/internal/api/skills_propose_handlers.go
// publishes one frame per propose / approve / reject / share-org with
// kind = biumind.runtime.skill.{proposed,approved,rejected,shared} and
// payload = {skill_id, identifier, org_id, [reason|update_of]}.
//
// Mirrors features/code/sync/code_tasks_realtime.dart in shape — JWT
// is parsed locally for the org claim, RealtimeHub does the SSE work,
// frames trigger a Riverpod invalidate so the SkillsPage's
// skillsListProvider refetches.
//
// Toast surfacing: a broadcast Stream<SkillEventNotice> lets the page
// pop a SnackBar when an admin remotely approves the user's draft.

import 'dart:async';
import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:logging/logging.dart';

import '../../../data/sse/realtime_hub.dart';
import '../../../data/skill_providers.dart';
import '../../../services/auth_service.dart';

final _log = Logger('biumind.skills.realtime');

/// One observed remote skill state-change. Surfaced for toast / snack
/// UI; the underlying SkillsList refresh happens inside the listener.
class SkillEventNotice {
  /// e.g. biumind.runtime.skill.approved
  final String kind;
  final String skillId;
  final String identifier;
  final String? reason;
  final String? updateOf;

  const SkillEventNotice({
    required this.kind,
    required this.skillId,
    required this.identifier,
    this.reason,
    this.updateOf,
  });

  /// Last segment of `biumind.runtime.skill.<verb>`, for UI rendering.
  String get verb {
    final i = kind.lastIndexOf('.');
    return i < 0 ? kind : kind.substring(i + 1);
  }
}

/// Long-lived listener. Riverpod-managed singleton: `start()` is
/// idempotent and called from SkillsPage initState; `stop()` runs on
/// dispose or when credentials change.
class SkillEventsListener {
  SkillEventsListener(this._ref);
  final Ref _ref;

  RealtimeHub? _hub;
  StreamSubscription<RealtimeFrame>? _sub;
  String? _topic;
  final StreamController<SkillEventNotice> _ctrl =
      StreamController<SkillEventNotice>.broadcast();

  /// Public — page subscribes for toast UI.
  Stream<SkillEventNotice> get notices => _ctrl.stream;

  void start() {
    if (_topic != null) return; // already running
    final creds = _ref.read(hubCredentialsProvider);
    if (creds == null) {
      _log.fine('no creds; skipping skill events listener');
      return;
    }
    final orgId = _decodeOrgId(creds.bearerToken);
    if (orgId == null) {
      _log.warning('JWT missing org_id; skill events listener disabled');
      return;
    }
    _topic = 'org:$orgId:skill_events';

    _hub = RealtimeHub(RealtimeHubConfig(
      // Same path-replace pattern as code_tasks_realtime — Realtime
      // is reverse-proxied behind the model-relay host in deploy/test; native
      // setups that need a separate :7008 should adjust their nginx.
      endpoint: creds.endpoint.replace(path: '/v1/realtime/stream'),
      auth: () async {
        final c = _ref.read(hubCredentialsProvider);
        return c?.bearerToken ?? '';
      },
    ));

    _sub = _hub!.subscribe(_topic!).listen(
      _onFrame,
      onError: (Object e) {
        _log.warning('skill events stream error: $e');
      },
    );
    _log.info('skill events listener subscribed to $_topic');
  }

  Future<void> stop() async {
    await _sub?.cancel();
    _sub = null;
    await _hub?.dispose();
    _hub = null;
    _topic = null;
  }

  void _onFrame(RealtimeFrame frame) {
    final kind = frame.kind;
    if (!kind.startsWith('biumind.runtime.skill.')) {
      // Ignore unrelated frames published on the same topic — server
      // reserves the topic name but a forward-compat message kind we
      // don't recognise yet shouldn't trigger spurious refreshes.
      return;
    }
    // Refresh the list — cheap and avoids per-row merge logic. The
    // server source-of-truth wins on every visible state change.
    _ref.invalidate(skillsListProvider);

    final p = frame.payload;
    final notice = SkillEventNotice(
      kind: kind,
      skillId: p['skill_id']?.toString() ?? '',
      identifier: p['identifier']?.toString() ?? '',
      reason: p['reason']?.toString(),
      updateOf: p['update_of']?.toString(),
    );
    if (!_ctrl.isClosed) _ctrl.add(notice);
  }

  /// Lightweight base64url JWT payload decode → org_id claim. Same
  /// shape as code_tasks_realtime._decodeJwtSub; keeping a local copy
  /// avoids cross-feature coupling for one shared helper that's only
  /// used in two files.
  String? _decodeOrgId(String jwt) {
    try {
      final parts = jwt.split('.');
      if (parts.length != 3) return null;
      var payload = parts[1];
      while (payload.length % 4 != 0) {
        payload += '=';
      }
      final json = utf8.decode(base64Url.decode(payload));
      final m = jsonDecode(json) as Map<String, dynamic>;
      final v = m['org_id'];
      if (v is String && v.isNotEmpty) return v;
      return null;
    } catch (_) {
      return null;
    }
  }
}

/// Singleton listener — created once per session, restarted when
/// credentials change. SkillsPage reads this provider in initState
/// to ensure a model-relay subscription exists while the page is open.
final skillEventsListenerProvider = Provider<SkillEventsListener>((ref) {
  final l = SkillEventsListener(ref);
  // Restart on credential rotation so token-refresh edge cases pick
  // up the new bearer without a manual sign-out/in cycle.
  ref.listen<HubCredentials?>(hubCredentialsProvider, (prev, next) {
    if (prev?.bearerToken != next?.bearerToken) {
      l.stop().then((_) => l.start());
    }
  });
  ref.onDispose(() => l.stop());
  return l;
});

/// Page-side stream — toast surface for "your draft was approved
/// elsewhere". Watch this with `ref.listen` to get one-shot
/// notifications without the rebuild churn of a full StreamProvider.
final skillEventNoticesProvider = StreamProvider<SkillEventNotice>((ref) {
  final listener = ref.watch(skillEventsListenerProvider);
  return listener.notices;
});
