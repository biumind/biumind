/// 页面级实时反馈条幅 —— ingest_activity_panel + page_regeneration_banner 合并版。
///
/// 监听 wikiSyncEventsProvider，按 entity 分流：
///
///   - entity=ingest_task && payload.affected_pages 含 pageId
///       → IngestActivityBanner: 紫色 "这页正在被 ingest 重写"
///         显示 phase / percent / 进度。终态 (done/failed) 自动消失。
///
///   - entity=page && op in {created/updated} && entity_id == pageId
///     && actor_type=worker (worker 写的，区别于用户编辑)
///       → PageRegenerationBanner: 绿色 "这页刚被 LLM 更新"
///         自动 5 秒后淡出。
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../application/sync_provider.dart' show wikiSyncEventsProvider;

class PageRealtimeBanners extends ConsumerStatefulWidget {
  const PageRealtimeBanners({
    super.key,
    required this.projectId,
    required this.pageId,
  });
  final String projectId;
  final String pageId;

  @override
  ConsumerState<PageRealtimeBanners> createState() =>
      _PageRealtimeBannersState();
}

class _PageRealtimeBannersState extends ConsumerState<PageRealtimeBanners> {
  // ingest 状态
  bool _ingesting = false;
  String? _ingestPhase;
  double? _ingestPercent;
  String? _ingestTaskId;

  // regeneration 状态（5s 自动消失）
  bool _regenerated = false;
  Timer? _regenerationTimer;

  @override
  void dispose() {
    _regenerationTimer?.cancel();
    super.dispose();
  }

  void _onEvent(Map<Object?, Object?> event) {
    final entity = event['entity'];
    final op = event['op'];
    final payload = event['payload'];
    final p = payload is Map ? payload.cast<String, Object?>() : const <String, Object?>{};

    if (entity == 'ingest_task') {
      // 判定该 ingest 是否影响当前 page：
      //   1) payload.page_id == pageId（worker 在写单页时常用）
      //   2) payload.affected_pages 数组含 pageId
      //   3) result_pages 数组含 pageId
      final hit = _ingestHits(p, widget.pageId);
      if (!hit) return;

      switch (op) {
        case 'started':
        case 'progress':
        case 'phase':
        case 'page_planned':
        case 'page':
          if (!mounted) return;
          setState(() {
            _ingesting = true;
            _ingestTaskId = (p['task_id'] ?? p['id'])?.toString();
            final stage = p['phase'] ?? p['stage'];
            if (stage is String) _ingestPhase = stage;
            final pct = p['percent'];
            if (pct is num) _ingestPercent = pct.toDouble() / 100.0;
          });
        case 'done':
        case 'partial':
          if (!mounted) return;
          setState(() {
            _ingesting = false;
            _ingestPercent = null;
            _ingestPhase = null;
          });
          _markRegenerated();
        case 'failed':
        case 'cancelled':
          if (!mounted) return;
          setState(() {
            _ingesting = false;
            _ingestPercent = null;
            _ingestPhase = null;
          });
      }
      return;
    }

    // worker / system 写的 page.updated → 显示 regeneration banner
    if (entity == 'page' && (op == 'updated' || op == 'created')) {
      final eid = event['entity_id'];
      if (eid != widget.pageId) return;
      final actor = event['actor_type'];
      if (actor != 'worker' && actor != 'system') return;
      _markRegenerated();
    }
  }

  bool _ingestHits(Map<String, Object?> p, String pageId) {
    if (p['page_id'] == pageId) return true;
    final affected = p['affected_pages'];
    if (affected is List && affected.any((e) => e?.toString() == pageId)) {
      return true;
    }
    final result = p['result_pages'];
    if (result is List && result.any((e) => e?.toString() == pageId)) {
      return true;
    }
    return false;
  }

  void _markRegenerated() {
    if (!mounted) return;
    setState(() => _regenerated = true);
    _regenerationTimer?.cancel();
    _regenerationTimer = Timer(const Duration(seconds: 5), () {
      if (!mounted) return;
      setState(() => _regenerated = false);
    });
  }

  @override
  Widget build(BuildContext context) {
    ref.listen<AsyncValue<Map<Object?, Object?>>>(
      wikiSyncEventsProvider(widget.projectId),
      (_, next) => next.whenData(_onEvent),
    );

    if (_ingesting) {
      return _IngestActivityBanner(
        phase: _ingestPhase,
        percent: _ingestPercent,
        taskId: _ingestTaskId,
      );
    }
    if (_regenerated) {
      return _RegenerationBanner(
        onDismiss: () {
          _regenerationTimer?.cancel();
          if (mounted) setState(() => _regenerated = false);
        },
      );
    }
    return const SizedBox.shrink();
  }
}

class _IngestActivityBanner extends StatelessWidget {
  const _IngestActivityBanner({
    this.phase,
    this.percent,
    this.taskId,
  });
  final String? phase;
  final double? percent;
  final String? taskId;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      decoration: BoxDecoration(
        color: BiuTokens.purpleLight,
        border: Border(bottom: BorderSide(color: BiuTokens.purpleSoft)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              const SizedBox(
                width: 12,
                height: 12,
                child: CircularProgressIndicator(strokeWidth: 1.5),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  '这一页正在被 LLM 重写${phase != null && phase!.isNotEmpty ? ' · $phase' : ''}',
                  style: TextStyle(
                    color: BiuTokens.purple,
                    fontSize: 12,
                    fontWeight: FontWeight.w500,
                  ),
                ),
              ),
              if (percent != null)
                Text(
                  '${(percent! * 100).toStringAsFixed(0)}%',
                  style: TextStyle(
                    color: BiuTokens.purple,
                    fontSize: 11,
                  ),
                ),
            ],
          ),
          if (percent != null) ...[
            const SizedBox(height: 4),
            ClipRRect(
              borderRadius: BorderRadius.circular(2),
              child: LinearProgressIndicator(
                value: percent,
                minHeight: 3,
                backgroundColor: BiuTokens.surface,
                color: BiuTokens.purple,
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _RegenerationBanner extends StatelessWidget {
  const _RegenerationBanner({required this.onDismiss});
  final VoidCallback onDismiss;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      decoration: BoxDecoration(
        color: BiuTokens.success.withValues(alpha: 0.08),
        border: Border(
          bottom:
              BorderSide(color: BiuTokens.success.withValues(alpha: 0.3)),
        ),
      ),
      child: Row(
        children: [
          Icon(Icons.auto_awesome, size: 14, color: BiuTokens.success),
          const SizedBox(width: 6),
          Expanded(
            child: Text(
              '这一页刚被 LLM 更新 · 内容已是最新',
              style: TextStyle(
                color: BiuTokens.success,
                fontSize: 12,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
          IconButton(
            tooltip: '关闭',
            onPressed: onDismiss,
            icon: const Icon(Icons.close, size: 14),
            padding: EdgeInsets.zero,
            constraints: const BoxConstraints(minWidth: 24, minHeight: 24),
          ),
        ],
      ),
    );
  }
}
