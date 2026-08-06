/// Ingest task SSE 进度页 —— /wiki/p/:pid/ingest/tasks/:tid
///
/// 接 brain `/v1/wiki/projects/{pid}/ingest/tasks/{tid}/events` 端点，
/// 实时显示 status / stage / progress / 已落地的 page 链接。
///
/// 当前 brain stub 端点（B0.5）只发一帧占位；workers/wiki-llm 上线后
/// 实际事件流（status/progress/page/done/error）会被本页面正确解析
/// （IngestStreamController 已支持完整 schema）。
library;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme.dart';
import 'ingest_stream_state.dart';

class IngestStreamPage extends ConsumerWidget {
  const IngestStreamPage({
    super.key,
    required this.projectId,
    required this.taskId,
  });

  final String projectId;
  final String taskId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final args = IngestStreamArgs(projectId: projectId, taskId: taskId);
    final async = ref.watch(ingestStreamControllerProvider(args));

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _Header(
          taskId: taskId,
          onBack: () =>
              context.go('/wiki/p/$projectId/sources'),
        ),
        Divider(height: 1, color: BiuTokens.borderSubtle),
        Expanded(
          child: async.when(
            loading: () => const Center(child: CircularProgressIndicator()),
            error: (e, _) => _ErrorView(message: e.toString()),
            data: (s) => _Body(state: s),
          ),
        ),
      ],
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.taskId, required this.onBack});
  final String taskId;
  final VoidCallback onBack;

  @override
  Widget build(BuildContext context) {
    final shortId = taskId.length > 8 ? taskId.substring(0, 8) : taskId;
    return Container(
      height: 48,
      padding: const EdgeInsets.symmetric(horizontal: 8),
      child: Row(
        children: [
          IconButton(
            tooltip: '返回源文件',
            onPressed: onBack,
            icon: const Icon(Icons.arrow_back, size: 18),
          ),
          const SizedBox(width: 4),
          Icon(Icons.bolt, size: 16, color: BiuTokens.purple),
          const SizedBox(width: 6),
          Text(
            'Ingest 进度',
            style: TextStyle(
              color: BiuTokens.text,
              fontSize: 14,
              fontWeight: FontWeight.w600,
            ),
          ),
          const SizedBox(width: 8),
          Text(
            shortId,
            style: TextStyle(
              color: BiuTokens.textMuted,
              fontSize: 11,
              fontFamily: 'monospace',
            ),
          ),
        ],
      ),
    );
  }
}

class _Body extends StatelessWidget {
  const _Body({required this.state});
  final IngestStreamState state;

  @override
  Widget build(BuildContext context) {
    return SingleChildScrollView(
      padding: const EdgeInsets.fromLTRB(24, 16, 24, 24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _StatusBlock(state: state),
          const SizedBox(height: 16),
          if (state.percent != null || state.stage != null)
            _ProgressBlock(state: state),
          const SizedBox(height: 16),
          _ResultPagesBlock(state: state),
          if (state.error != null && state.error!.isNotEmpty) ...[
            const SizedBox(height: 16),
            _ErrorBlock(error: state.error!),
          ],
          const SizedBox(height: 24),
          _EventLogBlock(state: state),
        ],
      ),
    );
  }
}

class _StatusBlock extends StatelessWidget {
  const _StatusBlock({required this.state});
  final IngestStreamState state;

  @override
  Widget build(BuildContext context) {
    final (label, color, icon) = switch (state.status) {
      'connecting' => ('连接中', BiuTokens.textMuted, Icons.cable),
      'pending' => ('排队中', BiuTokens.textMuted, Icons.hourglass_empty),
      'running' => ('解析中', BiuTokens.purple, Icons.autorenew),
      'partial' => ('部分完成', BiuTokens.purple, Icons.donut_small),
      'done' => ('已完成', BiuTokens.success, Icons.check_circle_outline),
      'failed' => ('失败', BiuTokens.error, Icons.error_outline),
      'cancelled' => ('已取消', BiuTokens.textMuted, Icons.cancel_outlined),
      'disconnected' =>
        ('连接断开', BiuTokens.error, Icons.cloud_off_outlined),
      _ => (state.status, BiuTokens.textSecondary, Icons.help_outline),
    };
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.06),
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        border: Border.all(color: color.withValues(alpha: 0.3)),
      ),
      child: Row(
        children: [
          Icon(icon, color: color, size: 22),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  label,
                  style: TextStyle(
                    color: color,
                    fontSize: 16,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                if (state.connected)
                  Padding(
                    padding: const EdgeInsets.only(top: 2),
                    child: Text(
                      'SSE 已连接 · 实时同步',
                      style: TextStyle(
                        color: BiuTokens.textMuted,
                        fontSize: 11,
                      ),
                    ),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _ProgressBlock extends StatelessWidget {
  const _ProgressBlock({required this.state});
  final IngestStreamState state;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Text(
              '阶段',
              style: TextStyle(
                color: BiuTokens.textSecondary,
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(width: 8),
            Text(
              state.stage ?? '—',
              style: TextStyle(
                color: BiuTokens.text,
                fontSize: 13,
              ),
            ),
            const Spacer(),
            if (state.percent != null)
              Text(
                '${(state.percent! * 100).toStringAsFixed(0)}%',
                style: TextStyle(
                  color: BiuTokens.textMuted,
                  fontSize: 11,
                ),
              ),
          ],
        ),
        const SizedBox(height: 6),
        ClipRRect(
          borderRadius: BorderRadius.circular(2),
          child: LinearProgressIndicator(
            value: state.percent,
            minHeight: 4,
            backgroundColor: BiuTokens.surfaceMuted,
            color: BiuTokens.purple,
          ),
        ),
      ],
    );
  }
}

class _ResultPagesBlock extends StatelessWidget {
  const _ResultPagesBlock({required this.state});
  final IngestStreamState state;

  @override
  Widget build(BuildContext context) {
    if (state.resultPages.isEmpty) {
      return Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          border: Border.all(color: BiuTokens.borderSubtle),
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        ),
        child: Row(
          children: [
            Icon(Icons.inbox_outlined,
                color: BiuTokens.textMuted, size: 16),
            const SizedBox(width: 8),
            Text(
              '尚无生成的页面',
              style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
            ),
          ],
        ),
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          '生成的页面 (${state.resultPages.length})',
          style: TextStyle(
            color: BiuTokens.textSecondary,
            fontSize: 12,
            fontWeight: FontWeight.w600,
          ),
        ),
        const SizedBox(height: 6),
        ...state.resultPages.map(
          (pid) => _PageRow(projectId: state.projectId, pageId: pid),
        ),
      ],
    );
  }
}

class _PageRow extends StatelessWidget {
  const _PageRow({required this.projectId, required this.pageId});
  final String projectId;
  final String pageId;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: () =>
          context.go('/wiki/p/$projectId/pages/$pageId'),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 6),
        child: Row(
          children: [
            Icon(Icons.description_outlined,
                size: 14, color: BiuTokens.textSecondary),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                pageId,
                style: TextStyle(
                  color: BiuTokens.text,
                  fontSize: 12,
                  fontFamily: 'monospace',
                ),
              ),
            ),
            Icon(Icons.arrow_forward,
                size: 14, color: BiuTokens.textMuted),
          ],
        ),
      ),
    );
  }
}

class _ErrorBlock extends StatelessWidget {
  const _ErrorBlock({required this.error});
  final String error;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: BiuTokens.errorSoft,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(Icons.warning_amber_outlined,
              color: BiuTokens.error, size: 16),
          const SizedBox(width: 8),
          Expanded(
            child: SelectableText(
              error,
              style: TextStyle(color: BiuTokens.error, fontSize: 12),
            ),
          ),
        ],
      ),
    );
  }
}

class _EventLogBlock extends StatelessWidget {
  const _EventLogBlock({required this.state});
  final IngestStreamState state;

  @override
  Widget build(BuildContext context) {
    if (state.events.isEmpty) return const SizedBox.shrink();
    return ExpansionTile(
      tilePadding: EdgeInsets.zero,
      title: Text(
        '事件日志 (${state.events.length})',
        style: TextStyle(
          color: BiuTokens.textSecondary,
          fontSize: 12,
          fontWeight: FontWeight.w600,
        ),
      ),
      children: [
        Container(
          padding: const EdgeInsets.all(8),
          decoration: BoxDecoration(
            color: BiuTokens.surfaceMuted,
            borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              for (final f in state.events.reversed.take(20))
                Padding(
                  padding: const EdgeInsets.symmetric(vertical: 2),
                  child: Text(
                    '[#${f.eventId} ${f.op}] ${f.payload}',
                    style: TextStyle(
                      color: BiuTokens.textSecondary,
                      fontSize: 11,
                      fontFamily: 'monospace',
                    ),
                  ),
                ),
            ],
          ),
        ),
      ],
    );
  }
}

class _ErrorView extends StatelessWidget {
  const _ErrorView({required this.message});
  final String message;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: SelectableText(
          message,
          style: TextStyle(color: BiuTokens.error, fontSize: 12),
        ),
      ),
    );
  }
}
