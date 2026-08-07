// BlockRenderer —— Chat 重构 R5。
//
// 把 [Block] sealed class dispatch 到对应 widget。R5 升级：
//   - TextBlock：assistant / user 统一走 ChatMarkdownView（富文本 + 代码 +
//     mermaid + math + svg + table），system 走纯 Text
//   - ToolUseBlock：折叠卡，默认折叠；点 header 展开看参数
//   - ToolResultBlock：折叠卡，默认折叠；展开看 monospace 输出
//   - ImageBlock：点开看大图

import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../../../l10n/app_localizations.dart';
import '../../domain/chat_models.dart';
import '../../domain/reasoning_parser.dart';
import '../../markdown/pipeline.dart';

class BlockRenderer extends StatelessWidget {
  const BlockRenderer({
    super.key,
    required this.block,
    required this.role,
  });

  final Block block;
  final MessageRole role;

  @override
  Widget build(BuildContext context) {
    return switch (block) {
      TextBlock(:final text, :final state) => _TextBlockView(
          text: text,
          streaming: state == BlockState.streaming,
          role: role,
        ),
      ToolUseBlock(:final toolName, :final input, :final state) =>
        _ToolUseBlockView(
          toolName: toolName,
          input: input,
          streaming: state == BlockState.streaming,
        ),
      ToolResultBlock(:final content, :final isError) =>
        _ToolResultBlockView(content: content, isError: isError),
      ImageBlock(:final id, :final mimeType, :final data) =>
        _ImageBlockView(cacheKey: id, mimeType: mimeType, base64Data: data),
    };
  }
}

class _TextBlockView extends StatelessWidget {
  const _TextBlockView({
    required this.text,
    required this.streaming,
    required this.role,
  });
  final String text;
  final bool streaming;
  final MessageRole role;

  @override
  Widget build(BuildContext context) {
    if (text.isEmpty && streaming) {
      return const Padding(
        padding: EdgeInsets.symmetric(vertical: 4),
        child: SizedBox(
          height: 16, width: 16,
          child: CircularProgressIndicator(strokeWidth: 2),
        ),
      );
    }
    // user / assistant / tool_result 统一走富文本管线 (markdown 渲染一致);
    // system 仍是纯文本。assistant 还会先扫一遍 `<think>` 标签拆段。
    Widget body;
    if (role == MessageRole.system) {
      body = Text(
        text,
        style: Theme.of(context).textTheme.bodyMedium,
      );
    } else if (role == MessageRole.assistant && hasReasoningTag(text)) {
      // 推理模型 (deepseek-r1 / glm-thinking / qwen-r1 / gpt-oss 等) 用
      // `<think>...</think>` 包裹推理过程。拆段渲染:reasoning 段进折叠面板,
      // text 段走普通 markdown。
      final segs = parseReasoning(text, isStreaming: streaming);
      body = Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          for (final s in segs)
            if (s.isReasoning)
              _ReasoningView(text: s.text, closed: s.closed)
            else
              ChatMarkdownView(text: s.text),
        ],
      );
    } else {
      body = ChatMarkdownView(text: text);
    }
    // P1-16: assistant 流式 text block 末尾追加紫色闪动方块 + 速度指示。
    if (streaming && role == MessageRole.assistant) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          body,
          Padding(
            padding: const EdgeInsets.only(top: 2),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                const _StreamingCursor(),
                const SizedBox(width: 8),
                _StreamRateMeter(text: text),
              ],
            ),
          ),
        ],
      );
    }
    return body;
  }
}

/// 推理段折叠面板 —— 头部显示「思考中…」(streaming, closed=false)
/// 或「推理过程」(closed=true);头部 InkWell 点击展开/收起内容。
/// 流式中默认展开让用户看到推理在跑;closed 后默认折叠让最终回答更突出。
///
/// 设计参考 lobehub MessageContent.Thinking,但不引入 shimmer 三方包 ——
/// 用 AnimationController 跑 0.6 → 1.0 alpha,够轻够薄。
class _ReasoningView extends StatefulWidget {
  const _ReasoningView({required this.text, required this.closed});
  final String text;
  /// false = 流式中尚未闭合,头部显示「思考中…」+ 默认展开。
  final bool closed;

  @override
  State<_ReasoningView> createState() => _ReasoningViewState();
}

class _ReasoningViewState extends State<_ReasoningView>
    with SingleTickerProviderStateMixin {
  late bool _expanded;
  AnimationController? _shimmer;

  @override
  void initState() {
    super.initState();
    _expanded = !widget.closed; // 流式默认展开;闭合后默认折叠
    if (!widget.closed) _startShimmer();
  }

  @override
  void didUpdateWidget(covariant _ReasoningView old) {
    super.didUpdateWidget(old);
    if (old.closed != widget.closed) {
      if (widget.closed) {
        _shimmer?.dispose();
        _shimmer = null;
        // closed → 自动收起,回到聚焦最终回答。仅当之前默认展开状态时
        // 才主动收;用户已手动调过就尊重用户选择。
        if (_expanded && !old.closed) _expanded = false;
      } else {
        _startShimmer();
      }
    }
  }

  void _startShimmer() {
    _shimmer = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 1200),
    )..repeat(reverse: true);
  }

  @override
  void dispose() {
    _shimmer?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l10n = AppLocalizations.of(context)!;
    final tone = theme.colorScheme.surfaceContainerHighest;
    final headerLabel = widget.closed
        ? l10n.chatV2ReasoningClosed
        : l10n.chatV2ReasoningStreaming;
    final headerIcon = widget.closed ? Icons.psychology_outlined : Icons.auto_awesome;
    Widget headerText = Text(
      headerLabel,
      style: theme.textTheme.labelMedium?.copyWith(
        color: theme.colorScheme.onSurfaceVariant,
        fontWeight: FontWeight.w600,
      ),
    );
    if (!widget.closed && _shimmer != null) {
      headerText = AnimatedBuilder(
        animation: _shimmer!,
        builder: (_, child) {
          final t = _shimmer!.value;
          return Opacity(opacity: 0.55 + 0.45 * t, child: child);
        },
        child: headerText,
      );
    }
    return Container(
      margin: const EdgeInsets.symmetric(vertical: 4),
      decoration: BoxDecoration(
        color: tone,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: theme.colorScheme.outlineVariant),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          InkWell(
            onTap: () => setState(() => _expanded = !_expanded),
            borderRadius: BorderRadius.circular(6),
            child: Padding(
              padding:
                  const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
              child: Row(
                children: [
                  Icon(headerIcon, size: 14, color: theme.colorScheme.primary),
                  const SizedBox(width: 6),
                  Expanded(child: headerText),
                  Icon(
                    _expanded ? Icons.expand_less : Icons.expand_more,
                    size: 16,
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ],
              ),
            ),
          ),
          if (_expanded)
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 0, 12, 8),
              // 推理过程统一灰色 + 略小字号,跟最终回答区分;不走 markdown
              // 避免推理里 `**` `#` 之类碎片标记被误渲染。
              child: Text(
                widget.text,
                style: theme.textTheme.bodySmall?.copyWith(
                  color: theme.colorScheme.onSurfaceVariant,
                  height: 1.55,
                ),
              ),
            ),
        ],
      ),
    );
  }
}

/// 流式速度指示 —— 显示 ≈N t/s。
/// 算法：观察 [text] 长度变化，估算 chars/sec → 除以 4 当 tokens/sec。
/// EMA(α=0.3) 平滑去抖；< 1s 数据点不显示（避免 0/0 与首字幻觉）。
class _StreamRateMeter extends StatefulWidget {
  const _StreamRateMeter({required this.text});
  final String text;

  @override
  State<_StreamRateMeter> createState() => _StreamRateMeterState();
}

class _StreamRateMeterState extends State<_StreamRateMeter> {
  DateTime? _firstSeenAt;
  int _firstSeenLen = 0;
  DateTime? _lastSampleAt;
  int _lastSampleLen = 0;
  double _ema = 0.0;
  bool _haveSample = false;

  static const _alpha = 0.3;

  @override
  void initState() {
    super.initState();
    _firstSeenAt = DateTime.now();
    _firstSeenLen = widget.text.length;
    _lastSampleAt = _firstSeenAt;
    _lastSampleLen = _firstSeenLen;
  }

  @override
  void didUpdateWidget(covariant _StreamRateMeter old) {
    super.didUpdateWidget(old);
    if (widget.text == old.text) return;
    final now = DateTime.now();
    final last = _lastSampleAt;
    if (last == null) return;
    final dtMs = now.difference(last).inMilliseconds;
    if (dtMs < 50) return; // 太密的更新不采样
    final dLen = widget.text.length - _lastSampleLen;
    if (dLen <= 0) return;
    final instCharsPerSec = dLen * 1000.0 / dtMs;
    if (!_haveSample) {
      _ema = instCharsPerSec;
      _haveSample = true;
    } else {
      _ema = _alpha * instCharsPerSec + (1 - _alpha) * _ema;
    }
    _lastSampleAt = now;
    _lastSampleLen = widget.text.length;
  }

  @override
  Widget build(BuildContext context) {
    if (!_haveSample) return const SizedBox.shrink();
    final firstAt = _firstSeenAt;
    if (firstAt == null) return const SizedBox.shrink();
    final elapsed = DateTime.now().difference(firstAt);
    if (elapsed.inMilliseconds < 800) return const SizedBox.shrink();
    final tps = (_ema / 4).round(); // 1 token ≈ 4 chars
    if (tps <= 0) return const SizedBox.shrink();
    final theme = Theme.of(context);
    return Text(
      '≈$tps t/s',
      style: theme.textTheme.labelSmall?.copyWith(
        color: theme.colorScheme.onSurfaceVariant,
        fontFeatures: const [FontFeature.tabularFigures()],
      ),
    );
  }
}

/// 流式光标 —— 文本块结尾的闪动方块。
/// 单 AnimationController, 性能开销极小; pixel-fixed 8×16 紫色圆角矩形。
class _StreamingCursor extends StatefulWidget {
  const _StreamingCursor();
  @override
  State<_StreamingCursor> createState() => _StreamingCursorState();
}

class _StreamingCursorState extends State<_StreamingCursor>
    with SingleTickerProviderStateMixin {
  late final AnimationController _ac;

  @override
  void initState() {
    super.initState();
    _ac = AnimationController(
      vsync: this,
      duration: const Duration(milliseconds: 900),
    )..repeat();
  }

  @override
  void dispose() {
    _ac.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final color = Theme.of(context).colorScheme.primary;
    return AnimatedBuilder(
      animation: _ac,
      builder: (_, _) {
        final v = _ac.value;
        final op = v < 0.5 ? 1.0 - v * 1.2 : 0.4 + (v - 0.5) * 1.2;
        return Opacity(
          opacity: op.clamp(0.4, 1.0),
          child: Container(
            width: 8,
            height: 16,
            decoration: BoxDecoration(
              color: color,
              borderRadius: BorderRadius.circular(2),
            ),
          ),
        );
      },
    );
  }
}

/// 折叠卡的统一外观。header 点一下切展开/收起；右侧可选 trailing actions
/// （复制 / 重试 等小图标）。
class _CollapsibleCard extends StatefulWidget {
  const _CollapsibleCard({
    required this.headerIcon,
    required this.headerLabel,
    required this.body,
    this.tone,
    this.copyText,
  });
  final IconData headerIcon;
  final String headerLabel;
  final Widget body;
  final Color? tone;
  /// 非空时 header 右侧显示一个复制图标，点击复制这段文本到剪贴板。
  final String? copyText;

  @override
  State<_CollapsibleCard> createState() => _CollapsibleCardState();
}

class _CollapsibleCardState extends State<_CollapsibleCard> {
  bool _open = false;

  Future<void> _copy(BuildContext context) async {
    final t = widget.copyText;
    if (t == null || t.isEmpty) return;
    await Clipboard.setData(ClipboardData(text: t));
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
        content: Text('已复制'),
        duration: Duration(seconds: 1),
      ));
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final tone = widget.tone ?? theme.colorScheme.surfaceContainerHighest;
    return Container(
      margin: const EdgeInsets.symmetric(vertical: 4),
      decoration: BoxDecoration(
        color: tone,
        borderRadius: BorderRadius.circular(6),
        border: Border.all(color: theme.colorScheme.outlineVariant),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          InkWell(
            onTap: () => setState(() => _open = !_open),
            borderRadius: BorderRadius.circular(6),
            child: Padding(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
              child: Row(
                children: [
                  Icon(widget.headerIcon, size: 14, color: theme.colorScheme.primary),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      widget.headerLabel,
                      style: theme.textTheme.labelMedium?.copyWith(
                        fontWeight: FontWeight.w600,
                      ),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                  if (widget.copyText != null && widget.copyText!.isNotEmpty)
                    IconButton(
                      icon: const Icon(Icons.copy_outlined, size: 14),
                      tooltip: '复制',
                      visualDensity: VisualDensity.compact,
                      padding: EdgeInsets.zero,
                      constraints:
                          const BoxConstraints(minWidth: 24, minHeight: 24),
                      onPressed: () => _copy(context),
                    ),
                  const SizedBox(width: 4),
                  Icon(
                    _open ? Icons.expand_less : Icons.expand_more,
                    size: 16,
                    color: theme.colorScheme.onSurfaceVariant,
                  ),
                ],
              ),
            ),
          ),
          if (_open)
            Padding(
              padding: const EdgeInsets.fromLTRB(8, 0, 8, 8),
              child: widget.body,
            ),
        ],
      ),
    );
  }
}

class _ToolUseBlockView extends StatelessWidget {
  const _ToolUseBlockView({
    required this.toolName,
    required this.input,
    required this.streaming,
  });
  final String toolName;
  final Map<String, dynamic>? input;
  final bool streaming;

  @override
  Widget build(BuildContext context) {
    final pretty = input == null || input!.isEmpty
        ? '{}'
        : const JsonEncoder.withIndent('  ').convert(input);
    final argCount = input?.length ?? 0;
    final summary = argCount == 0 ? '' : '  ($argCount args)';
    return _CollapsibleCard(
      headerIcon: streaming ? Icons.sync : Icons.build_outlined,
      headerLabel: streaming ? '$toolName … 调用中' : '$toolName$summary',
      copyText: pretty,
      body: Text(
        pretty,
        style: Theme.of(context).textTheme.bodySmall?.copyWith(
              fontFamily: 'monospace',
            ),
      ),
    );
  }
}

class _ToolResultBlockView extends StatelessWidget {
  const _ToolResultBlockView({required this.content, required this.isError});
  final String content;
  final bool isError;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final preview = _firstLine(content);
    final lineCount = '\n'.allMatches(content).length + 1;
    final lineHint = lineCount > 1 ? '  ($lineCount lines)' : '';
    return _CollapsibleCard(
      headerIcon: isError ? Icons.error_outline : Icons.check_circle_outline,
      headerLabel: isError
          ? 'tool error · $preview$lineHint'
          : 'tool result · $preview$lineHint',
      tone: isError ? theme.colorScheme.errorContainer : null,
      copyText: content,
      body: Text(
        content,
        style: theme.textTheme.bodySmall?.copyWith(
              fontFamily: 'monospace',
            ),
      ),
    );
  }

  static String _firstLine(String s) {
    final i = s.indexOf('\n');
    final line = i < 0 ? s : s.substring(0, i);
    return line.length > 80 ? '${line.substring(0, 80)}…' : line;
  }
}

/// 解码后字节的进程内 LRU 缓存（key = block id）。
///
/// 为什么必须缓存：`_ImageBlockView.build` 若每次都 `base64Decode`，会产出**新的**
/// `Uint8List` 实例；`Image.memory` 的 `MemoryImage` 缓存键按 bytes **引用相等**比较，
/// 新实例 → ImageCache 永远 miss → 每次重建都异步重解码 → 解码间隙空白帧。ListView
/// 默认 cacheExtent ~250px、图片约 260px 高，滚动一过屏 item 即被回收重建，于是滚动
/// 回看时图片频闪。返回同一 `Uint8List` 实例 → provider 稳定 → ImageCache 命中 → 不再
/// 重解码。上限 64 张防跨会话无限增长（图片字节本身另有 Flutter ImageCache 管理，这里
/// 只缓存解码后的字节引用，开销可控）。
// 注：Dart 的 map 字面量即 LinkedHashMap（按插入序），`.keys.first` 取最旧项可靠。
final _imageBytesCache = <String, Uint8List?>{};
const _imageBytesCacheMax = 64;

Uint8List? _decodeImageCached(String key, String base64Data) {
  final hit = _imageBytesCache.remove(key); // remove+重插 = LRU 触底刷新
  if (hit != null || _imageBytesCache.containsKey(key)) {
    _imageBytesCache[key] = hit; // 含解码失败的 null（避免反复重试解码）
    return hit;
  }
  Uint8List? bytes;
  try {
    bytes = base64Decode(base64Data);
  } catch (_) {
    bytes = null;
  }
  _imageBytesCache[key] = bytes;
  if (_imageBytesCache.length > _imageBytesCacheMax) {
    _imageBytesCache.remove(_imageBytesCache.keys.first);
  }
  return bytes;
}

class _ImageBlockView extends StatelessWidget {
  const _ImageBlockView({
    required this.cacheKey,
    required this.mimeType,
    required this.base64Data,
  });
  final String cacheKey;
  final String mimeType;
  final String base64Data;

  @override
  Widget build(BuildContext context) {
    final bytes = _decodeImageCached(cacheKey, base64Data);
    if (bytes == null) {
      return Text(
        '[image:$mimeType decode failed]',
        style: Theme.of(context).textTheme.bodySmall,
      );
    }
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: GestureDetector(
        onTap: () => _openFullScreen(context, bytes),
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxHeight: 260, maxWidth: 360),
          child: ClipRRect(
            borderRadius: BorderRadius.circular(6),
            // gaplessPlayback：万一仍发生重载，保留上一帧而非闪空白。
            child: Image.memory(bytes, fit: BoxFit.contain, gaplessPlayback: true),
          ),
        ),
      ),
    );
  }

  void _openFullScreen(BuildContext context, Uint8List bytes) {
    Navigator.of(context).push(
      PageRouteBuilder(
        opaque: false,
        barrierDismissible: true,
        barrierColor: Colors.black87,
        pageBuilder: (ctx, a, b) => _FullScreenImage(bytes: bytes),
      ),
    );
  }
}

class _FullScreenImage extends StatelessWidget {
  const _FullScreenImage({required this.bytes});
  final Uint8List bytes;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.transparent,
      body: GestureDetector(
        onTap: () => Navigator.of(context).pop(),
        child: Center(
          child: InteractiveViewer(
            minScale: 0.5,
            maxScale: 6,
            child: Image.memory(bytes),
          ),
        ),
      ),
    );
  }
}

