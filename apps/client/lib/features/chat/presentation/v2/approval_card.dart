// ApprovalCardV2 —— daemon 通过 brain 反向问"这个工具能不能跑"时
// 在消息流底部浮起的 inline confirm。
//
// 数据流:
//   daemon engine PermissionAsk
//   → BiuSessionConnection 收 SDKControlRequest{can_use_tool}
//   → thread.autoApprove != auto 时 emit PermissionRequested event
//   → ChatController 投到 pendingApprovalsProvider
//   → 本 widget watch 该 provider 渲染卡片
//   → 用户点 "允许" / "拒绝" / "始终允许"
//   → req.respond(allow) 沿 .in 发 PermissionResult 回 daemon
//   → resolve(threadId, requestId) 把卡片摘掉
//
// 渲染位置:MessageListV2 上方(在 SelectionActionBar 之上、composer 之下),
// 让用户视线焦点不离开消息流。

import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../l10n/app_localizations.dart';
import '../../application/chat_controller.dart';
import '../../data/biu_session_connection.dart';
import '../../domain/chat_models.dart';

class ApprovalCardV2 extends ConsumerWidget {
  const ApprovalCardV2({super.key, required this.threadId});

  final String threadId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final pending = ref.watch(pendingApprovalsProvider).forThread(threadId);
    if (pending.isEmpty) return const SizedBox.shrink();
    // 同时只渲染最早一条;daemon engine 串行触发,通常队列长度=1。
    final req = pending.first;
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 4),
      child: Material(
        color: theme.colorScheme.surface,
        elevation: 2,
        borderRadius: BorderRadius.circular(10),
        child: Container(
          padding: const EdgeInsets.fromLTRB(14, 12, 14, 12),
          decoration: BoxDecoration(
            border: Border.all(
              color: theme.colorScheme.primary.withValues(alpha: 0.3),
              width: 1.2,
            ),
            borderRadius: BorderRadius.circular(10),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Row(
                children: [
                  Icon(Icons.shield_outlined,
                      size: 16, color: theme.colorScheme.primary),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      l.chatV2ApprovalTitle(req.toolName),
                      style: theme.textTheme.labelLarge?.copyWith(
                        fontWeight: FontWeight.w600,
                      ),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ],
              ),
              if (req.reason != null && req.reason!.isNotEmpty) ...[
                const SizedBox(height: 4),
                Text(
                  req.reason!,
                  style: theme.textTheme.bodySmall?.copyWith(
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ),
              ],
              if (req.input.isNotEmpty) ...[
                const SizedBox(height: 8),
                _InputPreview(input: req.input),
              ],
              const SizedBox(height: 10),
              Row(
                children: [
                  // 拒绝 — 普通灰按钮。daemon 收到 deny 把这次 tool_call 跳过;
                  // 模型继续 turn(可能换路径或道歉)。
                  TextButton.icon(
                    onPressed: () => _resolve(ref, req, allow: false),
                    icon: const Icon(Icons.close, size: 14),
                    label: Text(l.chatV2ApprovalDeny),
                    style: TextButton.styleFrom(
                      foregroundColor: theme.colorScheme.onSurfaceVariant,
                      visualDensity: VisualDensity.compact,
                    ),
                  ),
                  const SizedBox(width: 8),
                  // 始终允许 — 把 thread.autoApprove 升到 'auto',下次同 thread
                  // 不再问。tooltip 提醒影响范围(本会话)。
                  TextButton.icon(
                    onPressed: () => _resolveAlwaysForThread(ref, req),
                    icon: const Icon(Icons.fast_forward, size: 14),
                    label: Text(l.chatV2ApprovalAlways),
                    style: TextButton.styleFrom(
                      visualDensity: VisualDensity.compact,
                    ),
                  ),
                  const Spacer(),
                  // 允许 — 主操作按钮。primary filled。
                  FilledButton.icon(
                    onPressed: () => _resolve(ref, req, allow: true),
                    icon: const Icon(Icons.check, size: 14),
                    label: Text(l.chatV2ApprovalAllow),
                    style: FilledButton.styleFrom(
                      visualDensity: VisualDensity.compact,
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  void _resolve(WidgetRef ref, PermissionRequested req,
      {required bool allow}) {
    req.respond(allow: allow);
    ref.read(pendingApprovalsProvider.notifier).resolve(
          threadId,
          req.requestId,
        );
  }

  /// "始终允许" — 应答当前请求 + 把 thread.autoApprove 升到 auto,
  /// 后续 daemon 触发的 tool call 直接 BiuSessionConnection 自己应答。
  Future<void> _resolveAlwaysForThread(
      WidgetRef ref, PermissionRequested req) async {
    _resolve(ref, req, allow: true);
    final repo = ref.read(chatControllerDepsProvider).repo;
    await repo.setThreadAutoApprove(threadId, AutoApproveMode.auto);
  }
}

/// 工具入参预览 — JSON 美化 + 折叠 4 行。点击展开看完整。
class _InputPreview extends StatefulWidget {
  const _InputPreview({required this.input});
  final Map<String, dynamic> input;

  @override
  State<_InputPreview> createState() => _InputPreviewState();
}

class _InputPreviewState extends State<_InputPreview> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final pretty = const JsonEncoder.withIndent('  ').convert(widget.input);
    final lines = pretty.split('\n');
    final shouldFold = lines.length > 4 && !_expanded;
    final shown = shouldFold ? lines.take(4).join('\n') : pretty;
    return InkWell(
      onTap: lines.length > 4
          ? () => setState(() => _expanded = !_expanded)
          : null,
      borderRadius: BorderRadius.circular(6),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
        decoration: BoxDecoration(
          color: theme.colorScheme.surfaceContainerLow,
          borderRadius: BorderRadius.circular(6),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SelectableText(
              shown,
              style: theme.textTheme.bodySmall?.copyWith(
                fontFamily: 'monospace',
                color: theme.colorScheme.onSurfaceVariant,
              ),
            ),
            if (shouldFold)
              Padding(
                padding: const EdgeInsets.only(top: 2),
                child: Text(
                  AppLocalizations.of(context)!.chatV2ApprovalShowMore,
                  style: theme.textTheme.labelSmall?.copyWith(
                    color: theme.colorScheme.primary,
                  ),
                ),
              ),
          ],
        ),
      ),
    );
  }
}
