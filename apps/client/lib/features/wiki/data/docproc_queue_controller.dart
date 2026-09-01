/// 应用级本机解析/导入队列（设计文档 §3.5 / P2 W1）：
/// 字节 workload 上限（桌面 200MB / 移动 80MB）+ 并发上限（桌面 3 /
/// 移动 1）+ 逐条状态机（queued/parsing/uploading/done/failed/cancelled）。
///
/// 从 import_dialog 的串行 for 循环升级而来：dialog 只 enqueue，处理在
/// 后台继续，进度经 DocprocTaskMirror 进 activity 管道。
///
/// 「并发」的含义是**流水线重叠**（A 在解析时 B 可以上传/建源）；
/// docproc parse 本身经 [_serializeParse] 锁串行 —— bundle 是单 JS
/// 上下文，不伪造并行解析。
///
/// 背压：parsing/uploading 的总字节超上限时，queued item 留在队列等待
/// （enqueue 本身不阻塞 UI）。单 item 超上限时允许独占窗口启动，避免死锁。
library;

import 'dart:async';
import 'dart:convert' show utf8;

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/docproc/docproc_bridge_controller.dart';
import '../../../core/platform/platform_caps.dart';
import '../../../data/api/wiki_client.dart';
import '../../../data/wiki_providers.dart'
    show sourcesListProvider, wikiRepositoryProvider;
import '../../code/data/files_client.dart' show filesClientProvider;
import '../application/docproc_preferences.dart';
import 'docproc_task_mirror.dart';

/// 队列条目的处理位置偏好（dialog 的「处理位置」选择）。
enum DocprocItemStatus { queued, parsing, uploading, done, failed, cancelled }

class DocprocQueueItem {
  const DocprocQueueItem({
    required this.id,
    required this.projectId,
    required this.filename,
    required this.bytes,
    required this.mime,
    this.externalId,
    this.location = DocprocProcessLocation.auto,
    this.status = DocprocItemStatus.queued,
    this.error,
    this.mirrorTaskId,
    this.phase,
    this.percent,
  });

  final String id;
  final String projectId;
  final String filename;
  final Uint8List bytes;
  final String mime;
  final String? externalId;

  /// 「文档处理位置」设置（docprocPreferencesProvider）在 **enqueue 时刻**
  /// 的快照 —— 设置变化不追溯已在队列里的 item（§3.4 / W3）。
  final DocprocProcessLocation location;

  final DocprocItemStatus status;
  final String? error;

  /// 镜像任务 id（processor=client）：activity 卡片 cancel/retry 靠它
  /// 反查本队 item；非本队（重启后的 stale 任务）查不到 → 隐藏按钮。
  final String? mirrorTaskId;

  /// 本机解析进度（bundle 上报）。
  final String? phase;
  final int? percent;

  int get byteSize => bytes.length;

  bool get isActive =>
      status == DocprocItemStatus.parsing ||
      status == DocprocItemStatus.uploading;

  DocprocQueueItem copyWith({
    DocprocProcessLocation? location,
    DocprocItemStatus? status,
    Object? error = _unset,
    Object? mirrorTaskId = _unset,
    Object? phase = _unset,
    Object? percent = _unset,
  }) =>
      DocprocQueueItem(
        id: id,
        projectId: projectId,
        filename: filename,
        bytes: bytes,
        mime: mime,
        externalId: externalId,
        location: location ?? this.location,
        status: status ?? this.status,
        error: identical(error, _unset) ? this.error : error as String?,
        mirrorTaskId: identical(mirrorTaskId, _unset)
            ? this.mirrorTaskId
            : mirrorTaskId as String?,
        phase: identical(phase, _unset) ? this.phase : phase as String?,
        percent: identical(percent, _unset) ? this.percent : percent as int?,
      );

  static const Object _unset = Object();
}

/// 云端 multipart 上传（FilesClient.uploadBytes 的注入点，测试可 fake）。
typedef CloudUploadFn = Future<({String fileId, String? mimeType, int sizeBytes})>
    Function({
  required String projectId,
  required String filename,
  required List<int> bytes,
  required String contentType,
  String? externalId,
});

/// 队列运行期依赖（每次调度时现取 —— 凭证/engine 生命周期独立于队列，
/// lazy resolve 避免 provider 重建丢队列状态）。
class DocprocQueueDeps {
  const DocprocQueueDeps({
    required this.caps,
    required this.engine,
    required this.wikiClient,
    required this.uploadToCloud,
    this.location = DocprocProcessLocation.auto,
  });

  final PlatformCaps caps;

  /// null = 平台不支持本机解析（全部走云端）。
  final DocprocEngine? engine;

  /// null = 未配置后端凭证（所有 item 直接 failed）。
  final WikiClient? wikiClient;

  /// null = FilesClient 不可用（本机解析失败的云端回退也不可用 → failed）。
  final CloudUploadFn? uploadToCloud;

  /// 当前「文档处理位置」设置（enqueue 时快照到 item 上，见
  /// [DocprocQueueItem.location]）。
  final DocprocProcessLocation location;
}

enum _LocalOutcome { success, fallback, cancelled }

class DocprocQueue extends ChangeNotifier {
  DocprocQueue({
    required DocprocQueueDeps Function() resolveDeps,
    this.onItemSettled,
  }) : _resolveDeps = resolveDeps;

  final DocprocQueueDeps Function() _resolveDeps;

  /// item 到达终态后的回调（provider 接线 sourcesListProvider invalidate）。
  final void Function(String projectId)? onItemSettled;

  final List<DocprocQueueItem> _items = [];
  final Map<String, DocprocTaskMirror> _mirrors = {};
  final Set<String> _cancelRequested = {};

  /// docproc parse 串行锁（bundle 单 JS 上下文）。
  Future<void> _parseTail = Future.value();

  List<DocprocQueueItem> get items => List.unmodifiable(_items);

  DocprocQueueItem? itemById(String id) {
    for (final i in _items) {
      if (i.id == id) return i;
    }
    return null;
  }

  /// activity 卡片反查：mirror 任务 id → 本队 item。
  DocprocQueueItem? itemByMirrorTask(String taskId) {
    for (final i in _items) {
      if (i.mirrorTaskId == taskId) return i;
    }
    return null;
  }

  void enqueue(List<DocprocQueueItem> batch) {
    if (batch.isEmpty) return;
    // 设置快照在 enqueue 时刻：之后改「文档处理位置」不影响这批 item。
    final location = _resolveDeps().location;
    _items.addAll(batch.map((i) => i.copyWith(location: location)));
    notifyListeners();
    _pump();
  }

  /// 取消在途 item：queued 的直接移除（镜像任务还没建，无需 PATCH）；
  /// parsing 的经引擎 cancel（parse future 以 cancelled 失败，_parseLocally
  /// 负责 mirror PATCH cancelled）；uploading 的 multipart 不可中断，no-op。
  void cancel(String id) {
    final item = itemById(id);
    if (item == null) return;
    switch (item.status) {
      case DocprocItemStatus.queued:
        _items.remove(item);
        notifyListeners();
      case DocprocItemStatus.parsing:
        if (_cancelRequested.add(id)) {
          _resolveDeps().engine?.cancel(id);
        }
      case DocprocItemStatus.uploading:
      case DocprocItemStatus.done:
      case DocprocItemStatus.failed:
      case DocprocItemStatus.cancelled:
        break;
    }
  }

  /// failed / cancelled 重新入队（bytes 保留；镜像任务在重跑时重建）。
  void retry(String id) {
    final item = itemById(id);
    if (item == null) return;
    if (item.status != DocprocItemStatus.failed &&
        item.status != DocprocItemStatus.cancelled) {
      return;
    }
    _cancelRequested.remove(id);
    _mirrors.remove(id);
    _replace(item.copyWith(
      status: DocprocItemStatus.queued,
      error: null,
      mirrorTaskId: null,
      phase: null,
      percent: null,
    ));
    _pump();
  }

  // ─── 调度 ───────────────────────────────────────────────

  void _pump() {
    final caps = _resolveDeps().caps;
    while (true) {
      var activeCount = 0;
      var activeBytes = 0;
      for (final i in _items) {
        if (i.isActive) {
          activeCount++;
          activeBytes += i.byteSize;
        }
      }
      if (activeCount >= caps.docprocQueueConcurrency) return;
      DocprocQueueItem? next;
      for (final i in _items) {
        if (i.status != DocprocItemStatus.queued) continue;
        // 背压字节窗口；窗口全空时允许独占启动（防单个大 item 死锁）。
        if (activeCount == 0 ||
            activeBytes + i.byteSize <= caps.docprocQueueMaxBytes) {
          next = i;
          break;
        }
      }
      if (next == null) return;
      // _process 在首个 await 前同步把状态切出 queued，循环内 activeCount
      // 立即生效，不会重复拉起。
      unawaited(_process(next));
    }
  }

  Future<void> _process(DocprocQueueItem item) async {
    final deps = _resolveDeps();
    final wiki = deps.wikiClient;
    if (wiki == null) {
      _finish(item, DocprocItemStatus.failed, '未配置后端凭证');
      return;
    }

    if (_shouldParseLocally(item, deps)) {
      item = _replace(item.copyWith(status: DocprocItemStatus.parsing));
      final outcome = await _parseLocally(item, deps, wiki);
      switch (outcome) {
        case _LocalOutcome.success:
          _finish(item, DocprocItemStatus.done, null);
          return;
        case _LocalOutcome.cancelled:
          _finish(item, DocprocItemStatus.cancelled, null);
          return;
        case _LocalOutcome.fallback:
          // mirror failed 已 PATCH；继续云端路径（§8 失败自动转云端兜底）。
          final current = itemById(item.id);
          if (current == null) return; // 处理中被移除（理论上不会发生）
          item = current;
      }
    }

    item = _replace(item.copyWith(status: DocprocItemStatus.uploading));
    try {
      await _uploadToCloud(item, deps, wiki);
      _finish(item, DocprocItemStatus.done, null);
    } on Exception catch (e) {
      _finish(item, DocprocItemStatus.failed, e.toString());
    }
  }

  /// 本机解析 + ingest_tasks 镜像（W2 时序）：
  ///   a. 占位 source（parseStatus=processing）
  ///   b. 镜像任务 processor=client（best-effort）
  ///   c. docproc parse（串行锁）+ progress 节流 PATCH
  ///   d. 成功：同 relPath upsert（rawText + contentHash + parseMeta +
  ///      parseStatus=done）→ PATCH done
  ///   e. 失败：PATCH failed → fallback 云端；取消：PATCH cancelled
  Future<_LocalOutcome> _parseLocally(
    DocprocQueueItem item,
    DocprocQueueDeps deps,
    WikiClient wiki,
  ) async {
    final engine = deps.engine;
    if (engine == null) return _LocalOutcome.fallback;
    final mirror = DocprocTaskMirror(client: wiki, projectId: item.projectId);
    _mirrors[item.id] = mirror;
    engine.onProgress = _routeProgress;
    try {
      final placeholder = await wiki.createSource(
        item.projectId,
        relPath: item.filename,
        filename: item.filename,
        mime: item.mime,
        byteSize: item.byteSize,
        externalId: item.externalId,
        parseStatus: 'processing',
      );
      await mirror.start(sourceId: placeholder.id, title: item.filename);
      _replace(item.copyWith(mirrorTaskId: mirror.taskId));

      final result = await _serializeParse(
        () => engine.parse(
          fileName: item.filename,
          bytes: item.bytes,
          mimeHint: item.mime,
          requestId: item.id,
        ),
      );

      final contentHash = sha256.convert(utf8.encode(result.text));
      await wiki.createSource(
        item.projectId,
        relPath: item.filename,
        filename: item.filename,
        mime: item.mime,
        byteSize: item.byteSize,
        externalId: item.externalId,
        rawText: result.text,
        contentHash: contentHash.toString(),
        parseStatus: 'done',
        parseMeta: {
          'parser': 'docproc-web',
          'version': result.parserVersion,
          'format': result.format,
          'page_count': ?result.pageCount,
        },
      );
      await mirror.done();
      return _LocalOutcome.success;
    } on Exception catch (e) {
      if (_cancelRequested.remove(item.id)) {
        await mirror.cancelled();
        return _LocalOutcome.cancelled;
      }
      await mirror.failed(e);
      return _LocalOutcome.fallback;
    }
  }

  Future<void> _uploadToCloud(
    DocprocQueueItem item,
    DocprocQueueDeps deps,
    WikiClient wiki,
  ) async {
    final uploadFn = deps.uploadToCloud;
    if (uploadFn == null) {
      throw StateError('云端上传通道不可用（FilesClient 未配置）');
    }
    final upload = await uploadFn(
      projectId: item.projectId,
      filename: item.filename,
      bytes: item.bytes,
      contentType: item.mime,
      externalId: item.externalId,
    );
    await wiki.createSource(
      item.projectId,
      relPath: item.filename,
      fileId: upload.fileId,
      filename: item.filename,
      mime: upload.mimeType ?? item.mime,
      byteSize: upload.sizeBytes,
      externalId: item.externalId,
    );
  }

  // ─── 内部 ───────────────────────────────────────────────

  /// §3.4 策略矩阵（W3 起三态设置驱动，矩阵实现收敛在
  /// docproc_preferences.dart 的 [docprocShouldParseLocally]）：
  /// 引擎可用 + item 入队时的设置快照 + 平台/大小判定。
  bool _shouldParseLocally(DocprocQueueItem item, DocprocQueueDeps deps) {
    if (deps.engine == null) return false;
    return docprocShouldParseLocally(
      location: item.location,
      caps: deps.caps,
      byteSize: item.byteSize,
    );
  }

  /// bundle 进度按 requestId(=item.id) 路由：item 进度 + 镜像 PATCH。
  void _routeProgress(String id, String phase, int percent) {
    final item = itemById(id);
    if (item == null || item.status != DocprocItemStatus.parsing) return;
    _replace(item.copyWith(phase: phase, percent: percent));
    unawaited(_mirrors[id]?.progress(phase, percent));
  }

  Future<T> _serializeParse<T>(Future<T> Function() fn) {
    final run = _parseTail.then((_) => fn());
    _parseTail = run.then((_) {}, onError: (_) {});
    return run;
  }

  DocprocQueueItem _replace(DocprocQueueItem updated) {
    final i = _items.indexWhere((e) => e.id == updated.id);
    if (i < 0) return updated;
    _items[i] = updated;
    notifyListeners();
    return updated;
  }

  void _finish(DocprocQueueItem item, DocprocItemStatus status, String? error) {
    _mirrors.remove(item.id);
    _cancelRequested.remove(item.id);
    final current = itemById(item.id);
    if (current == null) return;
    _replace(current.copyWith(status: status, error: error));
    onItemSettled?.call(item.projectId);
    _pump();
  }
}

// ─── Riverpod 接线 ─────────────────────────────────────────

/// 应用级 docproc 引擎（隐藏 webview 挂在 _AppShell，attach/detach 由
/// DocprocEngineView 负责）。
final docprocEngineControllerProvider = Provider<DocprocBridgeController>(
  (ref) {
    return DocprocBridgeController(caps: ref.watch(platformCapsProvider));
  },
);

/// 应用级解析/导入队列。依赖 lazy resolve（见 [DocprocQueueDeps]），
/// 队列本身不随凭证/engine provider 重建。
final docprocQueueProvider = ChangeNotifierProvider<DocprocQueue>((ref) {
  return DocprocQueue(
    resolveDeps: () {
      final caps = ref.read(platformCapsProvider);
      final repo = ref.read(wikiRepositoryProvider);
      final files = ref.read(filesClientProvider);
      return DocprocQueueDeps(
        caps: caps,
        engine: caps.hasLocalDocproc
            ? ref.read(docprocEngineControllerProvider)
            : null,
        location: ref.read(docprocPreferencesProvider).location,
        wikiClient: repo?.client,
        uploadToCloud: files == null
            ? null
            : ({
                required projectId,
                required filename,
                required bytes,
                required contentType,
                externalId,
              }) async {
                final res = await files.uploadBytes(
                  bytes: bytes,
                  filename: filename,
                  contentType: contentType,
                  source: 'wiki-source',
                  metadata: {
                    'project_id': projectId,
                    'rel_path': filename,
                    'external_id': ?externalId,
                  },
                );
                return (
                  fileId: res.fileId,
                  mimeType: res.mimeType,
                  sizeBytes: res.sizeBytes,
                );
              },
      );
    },
    onItemSettled: (projectId) =>
        ref.invalidate(sourcesListProvider(projectId)),
  );
});
