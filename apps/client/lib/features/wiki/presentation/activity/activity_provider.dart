/// Activity Feed providers —— B2.7 + B2.9 升级版。
///
/// 数据流：
///
///   ┌────────────────────────────┐
///   │  REST GET /activity (cold) │  ← build() 启动时一次
///   └─────────────┬──────────────┘
///                 │ applyBackfill
///                 ▼
///   ┌────────────────────────────┐
///   │  ActivityFeedReducer       │
///   └─────────────┬──────────────┘
///                 ▲ apply
///                 │
///   ┌────────────────────────────┐
///   │  WS /sync (live events)    │  ← B2.9 sync_provider
///   └────────────────────────────┘
///
/// 失去 WS 时仍可手动 refresh()（重新 cold-start）。brain 端 events_outbox
/// listener 接通后 WS 自动有 catchup/live 帧；接通前 WS 只发 ready+ping，
/// drawer 仍能用 polling/refresh 路径。
library;

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../data/wiki_providers.dart' show wikiRepositoryProvider;
import '../../application/sync_provider.dart' show wikiSyncEventsProvider;
import 'activity_state.dart';

class ActivityFeedNotifier
    extends FamilyAsyncNotifier<List<ActivityTask>, String> {
  final ActivityFeedReducer _reducer = ActivityFeedReducer();
  ProviderSubscription<AsyncValue<Map<Object?, Object?>>>? _sub;

  @override
  Future<List<ActivityTask>> build(String projectId) async {
    ref.onDispose(() {
      _sub?.close();
      _sub = null;
    });
    if (projectId.isEmpty) return const [];

    // 接 WS 实时 events 流 —— 任何 event 到达就过一次 reducer + 重发 list。
    _sub?.close();
    _sub = ref.listen<AsyncValue<Map<Object?, Object?>>>(
      wikiSyncEventsProvider(projectId),
      (_, next) {
        next.whenData((event) {
          final task = _reducer.apply(event);
          if (task != null) state = AsyncData(_currentSorted());
        });
      },
    );

    return _fetch(projectId);
  }

  Future<List<ActivityTask>> _fetch(String projectId) async {
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) return const [];
    try {
      final raw = await repo.client.listActivity(projectId);
      _reducer.applyBackfill(raw);
      return _currentSorted();
    } on Exception {
      return _currentSorted();
    }
  }

  /// running 在前；同段内按 lastUpdatedAt desc。
  List<ActivityTask> _currentSorted() {
    return _reducer.state.values.toList()
      ..sort((a, b) {
        final aRun = a.status == ActivityStatus.running ? 0 : 1;
        final bRun = b.status == ActivityStatus.running ? 0 : 1;
        if (aRun != bRun) return aRun.compareTo(bRun);
        return b.lastUpdatedAt.compareTo(a.lastUpdatedAt);
      });
  }

  Future<void> refresh() async {
    final pid = arg;
    state = const AsyncLoading();
    try {
      state = AsyncData(await _fetch(pid));
    } on Exception catch (e, st) {
      state = AsyncError(e, st);
    }
  }
}

final activityFeedProvider = AsyncNotifierProvider.family<
    ActivityFeedNotifier, List<ActivityTask>, String>(
  ActivityFeedNotifier.new,
);

/// 仅运行中任务。Drawer 顶部 section 用。
final activityFeedRunningProvider =
    Provider.family<List<ActivityTask>, String>((ref, projectId) {
  final async = ref.watch(activityFeedProvider(projectId));
  final list = async.valueOrNull ?? const <ActivityTask>[];
  return list.where((t) => t.status == ActivityStatus.running).toList();
});

/// 终态任务（recent）。Drawer 底部 section 用。
final activityFeedRecentProvider =
    Provider.family<List<ActivityTask>, String>((ref, projectId) {
  final async = ref.watch(activityFeedProvider(projectId));
  final list = async.valueOrNull ?? const <ActivityTask>[];
  return list.where((t) => t.status != ActivityStatus.running).toList();
});

/// 当前 running 计数 — StatusBar pill / NavRail 角标用。
final activityFeedCountProvider = Provider.family<int, String>(
  (ref, projectId) =>
      ref.watch(activityFeedRunningProvider(projectId)).length,
);
