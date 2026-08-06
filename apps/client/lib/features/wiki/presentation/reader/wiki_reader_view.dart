// WikiReaderView — read-only rendering of a Wiki page.
//
// Concatenates blocks → markdown via blocksToMarkdown, then hands off
// to GptMarkdown which already renders headings / paragraphs / lists /
// code / tables / images / inline & block math (KaTeX).
//
// Wikilinks are pre-rewritten to `wiki://<encoded-target>` markdown
// links; we intercept those in onLinkTap and route via the wiki
// controller (selectPageByTitle). External links open in the system
// browser via url_launcher.
//
// Code fences with `lang=mermaid` get the existing MermaidPreview
// widget (renders via mermaid.ink) instead of a plain code box.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:gpt_markdown/gpt_markdown.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../../app/theme.dart';
import '../../../../data/wiki_repository.dart';
import '../../application/wiki_controller.dart';
import '../mermaid/mermaid_preview.dart';
import 'block_to_markdown.dart';

class WikiReaderView extends ConsumerWidget {
  const WikiReaderView({
    super.key,
    required this.blocks,
    this.padding = const EdgeInsets.fromLTRB(16, 0, 16, 24),
  });

  final List<RepoBlock> blocks;
  final EdgeInsets padding;

  Future<void> _onLinkTap(BuildContext context, WidgetRef ref,
      String url, String title) async {
    final wikiTarget = wikiTargetFromUrl(url);
    if (wikiTarget != null) {
      // Resolve wiki:// to a same-project page by case-insensitive title.
      final state = ref.read(wikiControllerProvider).valueOrNull;
      final pages = state?.pages ?? const [];
      for (final p in pages) {
        if (p.title.toLowerCase() == wikiTarget.toLowerCase()) {
          await ref
              .read(wikiControllerProvider.notifier)
              .selectPageById(p.id);
          return;
        }
      }
      // Unresolved — surface a hint so the user knows the link is dead
      // rather than a silent no-op.
      if (context.mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('找不到页面：$wikiTarget')),
        );
      }
      return;
    }
    // Non-wiki URL → external browser.
    final uri = Uri.tryParse(url);
    if (uri != null && await canLaunchUrl(uri)) {
      await launchUrl(uri, mode: LaunchMode.externalApplication);
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (blocks.isEmpty) {
      return Padding(
        padding: padding,
        child: Text(
          '此页面还没有内容。点击右上角「编辑」开始写。',
          style: TextStyle(color: BiuTokens.textMuted, fontSize: 13),
        ),
      );
    }
    final md = blocksToMarkdown(blocks);
    return SingleChildScrollView(
      padding: padding,
      child: GptMarkdown(
        md,
        style: TextStyle(
          color: BiuTokens.text,
          fontSize: 14.5,
          height: 1.7,
        ),
        onLinkTap: (url, title) => _onLinkTap(context, ref, url, title),
        codeBuilder: (ctx, name, code, closed) {
          if (name.toLowerCase() == 'mermaid' && code.trim().isNotEmpty) {
            return Padding(
              padding: const EdgeInsets.symmetric(vertical: BiuTokens.space2),
              child: MermaidPreview(source: code),
            );
          }
          return _ReaderCodeBlock(
            language: name,
            code: code,
            closed: closed,
          );
        },
      ),
    );
  }
}

class _ReaderCodeBlock extends StatelessWidget {
  const _ReaderCodeBlock({
    required this.language,
    required this.code,
    required this.closed,
  });
  final String language;
  final String code;
  final bool closed;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      margin: const EdgeInsets.symmetric(vertical: BiuTokens.space2),
      decoration: BoxDecoration(
        color: Theme.of(context).extension<BiuColors>()!.surface2,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (language.isNotEmpty)
            Container(
              padding: const EdgeInsets.symmetric(
                horizontal: BiuTokens.space3,
                vertical: BiuTokens.space1,
              ),
              decoration: BoxDecoration(
                border: Border(
                  bottom: BorderSide(color: BiuTokens.borderSubtle),
                ),
              ),
              child: Text(
                language,
                style: TextStyle(
                  fontSize: 11,
                  color: BiuTokens.textMuted,
                  fontFamily: 'monospace',
                ),
              ),
            ),
          Padding(
            padding: const EdgeInsets.all(BiuTokens.space3),
            child: SelectableText(
              code,
              style: TextStyle(
                fontSize: 13,
                height: 1.5,
                fontFamily: 'monospace',
                color: BiuTokens.text,
              ),
            ),
          ),
        ],
      ),
    );
  }
}
