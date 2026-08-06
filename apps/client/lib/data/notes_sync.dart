// NotesSyncPoller — 笔记域增量同步（N1 轮询版）。
//
// 设计 §4 D4 的协议是「GET /v1/notes/changes?since=N 增量 + WS 推送」，
// 但服务端 N0 未做 /v1/notes/sync WS，故 N1 先轮询：周期性（默认 15s）
// + kick() 手动触发（flusher 冲刷成功后也会踢一脚）。
//
// 游标持久化到现有 SseCursors 表（scope='notes.changes'，lastEventId
// 存事件 id 的十进制字符串）—— 重启 app 后从上次位置续拉，不漏事件。
// 删除事件（tombstone）由 repository.applyChanges 应用到 Drift。
//
// 多设备一致性：单用户多设备同 scope 事件流，轮询天然幂等（事件 payload
// 是整行快照，重复应用无害）；cursor 失效/resyncRequired 是 WS 时代的
// 概念，轮询下拉不回溯源所以不涉及 —— since=0 即全量重放。

import 'dart:async';

import 'package:drift/drift.dart' show InsertMode;
import 'package:logging/logging.dart';

import 'local/db.dart';
import 'notes_repository.dart';

class NotesSyncPoller {
  NotesSyncPoller({
    required this.db,
    required this.repository,
    this.interval = const Duration(seconds: 15),
    Logger? log,
  }) : _log = log ?? Logger('NotesSyncPoller');

  final AppDb db;
  final NotesRepository repository;
  final Duration interval;
  final Logger _log;

  /// SseCursors 表里笔记域 changes 游标的 scope。
  static const cursorScope = 'notes.changes';

  /// 单页拉取上限（服务端上限 1000，默认 200）。
  static const _pageSize = 500;

  /// 单次 pullOnce 的最大翻页数 —— 防止异常服务端把我们卡死在循环里。
  static const _maxPages = 10;

  Timer? _timer;
  bool _pulling = false;

  void start() {
    _timer ??= Timer.periodic(interval, (_) => pullOnce());
    // 启动即拉一轮，不必等第一个 interval。
    unawaited(pullOnce());
  }

  void stop() {
    _timer?.cancel();
    _timer = null;
  }

  void dispose() => stop();

  /// 立即拉一轮（仓库 flush 成功后、UI 下拉刷新时调用）。
  /// 与进行中的 pull 合并。
  Future<void> kick() => pullOnce();

  /// 从游标位置拉 changes 并应用到 Drift，然后把游标推进到 latest。
  /// 网络失败吞掉（local-first：本地缓存照常可用，下个周期重试）。
  Future<void> pullOnce() async {
    if (_pulling) return;
    _pulling = true;
    try {
      var since = await _readCursor();
      for (var page = 0; page < _maxPages; page++) {
        final res = await repository.client.changes(since, limit: _pageSize);
        await repository.applyChanges(res.events);
        await _writeCursor(res.latest);
        // 满页说明可能还有，继续翻；不满页（含空页）即追平。
        if (res.events.length < _pageSize) break;
        since = res.latest;
      }
    } catch (e) {
      _log.fine('notes changes pull failed: $e');
    } finally {
      _pulling = false;
    }
  }

  Future<int> _readCursor() async {
    final row = await (db.select(db.sseCursors)
          ..where((t) => t.scope.equals(cursorScope)))
        .getSingleOrNull();
    return int.tryParse(row?.lastEventId ?? '') ?? 0;
  }

  Future<void> _writeCursor(int latest) async {
    await db.into(db.sseCursors).insert(
          SseCursorsCompanion.insert(
            scope: cursorScope,
            lastEventId: '$latest',
            updatedAt: DateTime.now().toUtc(),
          ),
          mode: InsertMode.insertOrReplace,
        );
  }
}
