// MermaidPreview — read-only rendered diagram block.
//
// Wraps `Image.network(mermaid_url)` with sane fallback / loading /
// error states. Long-press surfaces the raw source for copy + an
// "open in browser" link, so users can paste into mermaid.live to
// debug syntax errors when the renderer rejects.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../../app/theme.dart';
import 'mermaid_url.dart';

class MermaidPreview extends StatelessWidget {
  const MermaidPreview({
    super.key,
    required this.source,
    this.maxHeight = 360,
  });

  /// Mermaid diagram source.
  final String source;

  /// Vertical cap. Diagrams taller than this scroll inside the preview
  /// rather than expanding the page indefinitely.
  final double maxHeight;

  @override
  Widget build(BuildContext context) {
    final url = mermaidImageUrl(source);
    return Container(
      constraints: BoxConstraints(maxHeight: maxHeight),
      decoration: BoxDecoration(
        color: Colors.white,
        border: Border.all(color: BiuTokens.borderSubtle),
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: GestureDetector(
        onLongPress: () => _showSourceSheet(context),
        child: InteractiveViewer(
          child: Image.network(
            url,
            fit: BoxFit.contain,
            loadingBuilder: (_, child, prog) {
              if (prog == null) return child;
              return const Padding(
                padding: EdgeInsets.all(BiuTokens.space4),
                child: Center(
                  child: SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  ),
                ),
              );
            },
            errorBuilder: (_, _, _) => _ErrorFallback(
              source: source,
              onOpenLive: () => _openInLive(source),
            ),
          ),
        ),
      ),
    );
  }

  Future<void> _showSourceSheet(BuildContext context) async {
    await showModalBottomSheet<void>(
      context: context,
      builder: (_) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.all(BiuTokens.space3),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(
                'Mermaid 源码',
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  color: BiuTokens.textMuted,
                ),
              ),
              const SizedBox(height: BiuTokens.space2),
              SelectableText(
                source,
                style: const TextStyle(
                  fontFamily: 'JetBrains Mono, ui-monospace, monospace',
                  fontSize: 11,
                  height: 1.4,
                ),
              ),
              const SizedBox(height: BiuTokens.space3),
              Row(
                children: [
                  Expanded(
                    child: OutlinedButton.icon(
                      icon: const Icon(Icons.copy, size: 14),
                      label: const Text('复制源码'),
                      onPressed: () async {
                        await Clipboard.setData(ClipboardData(text: source));
                        if (!context.mounted) return;
                        Navigator.of(context).pop();
                        ScaffoldMessenger.of(context).showSnackBar(
                          const SnackBar(content: Text('已复制')),
                        );
                      },
                    ),
                  ),
                  const SizedBox(width: BiuTokens.space2),
                  Expanded(
                    child: FilledButton.icon(
                      icon: const Icon(Icons.open_in_new, size: 14),
                      label: const Text('在 mermaid.live 打开'),
                      onPressed: () {
                        Navigator.of(context).pop();
                        _openInLive(source);
                      },
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

  static Future<void> _openInLive(String source) async {
    // mermaid.live edit URL 格式: /edit#pako:<base64url(deflate(json))>
    // 直接复用 mermaidLiveEditUrl helper, 跟 mermaid.ink 渲染共享同一个
    // pako payload, 字段对齐 (mermaid 是 stringified JSON)。
    final uri = Uri.parse(mermaidLiveEditUrl(source));
    await launchUrl(uri, mode: LaunchMode.externalApplication);
  }
}

class _ErrorFallback extends StatelessWidget {
  const _ErrorFallback({required this.source, required this.onOpenLive});
  final String source;
  final VoidCallback onOpenLive;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(BiuTokens.space3),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          const Row(
            children: [
              Icon(Icons.error_outline, size: 14, color: BiuTokens.error),
              SizedBox(width: 4),
              Text(
                'Mermaid 渲染失败',
                style: TextStyle(
                  fontSize: 11,
                  fontWeight: FontWeight.w600,
                  color: BiuTokens.error,
                ),
              ),
            ],
          ),
          const SizedBox(height: 4),
          Text(
            '检查语法 — 点击下方在线编辑器调试',
            style: TextStyle(fontSize: 10, color: BiuTokens.textMuted),
          ),
          const SizedBox(height: BiuTokens.space2),
          Container(
            padding: const EdgeInsets.all(BiuTokens.space2),
            decoration: BoxDecoration(
              color: BiuTokens.surfaceMuted,
              borderRadius: BorderRadius.circular(4),
            ),
            child: Text(
              source.length > 200
                  ? '${source.substring(0, 200)}…'
                  : source,
              style: TextStyle(
                fontFamily: 'JetBrains Mono, ui-monospace, monospace',
                fontSize: 10,
                color: BiuTokens.textMuted,
              ),
              maxLines: 4,
              overflow: TextOverflow.ellipsis,
            ),
          ),
          const SizedBox(height: BiuTokens.space2),
          TextButton.icon(
            icon: const Icon(Icons.open_in_new, size: 12),
            label: const Text('在 mermaid.live 打开'),
            onPressed: onOpenLive,
          ),
        ],
      ),
    );
  }
}
