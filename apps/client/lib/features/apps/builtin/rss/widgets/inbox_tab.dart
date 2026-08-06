// Inbox tab — 3-pane (feeds | entries | reader) layout that
// collapses on narrow viewports.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../../../app/theme.dart';
import '../models.dart';
import '../providers.dart';
import 'entries_pane.dart';
import 'feeds_pane.dart';
import 'reader_pane.dart';

class InboxTab extends ConsumerWidget {
  const InboxTab({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return LayoutBuilder(
      builder: (ctx, constraints) {
        final isNarrow = constraints.maxWidth < 800;
        if (isNarrow) {
          return const _NarrowInbox();
        }
        return Container(
          color: BiuTokens.bg,
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: const [
              FeedsPane(),
              EntriesPane(),
              Expanded(child: ReaderPane()),
            ],
          ),
        );
      },
    );
  }
}

/// Narrow layout: feeds drawer (toggle), entries list, reader sheet.
class _NarrowInbox extends ConsumerStatefulWidget {
  const _NarrowInbox();
  @override
  ConsumerState<_NarrowInbox> createState() => _NarrowInboxState();
}

class _NarrowInboxState extends ConsumerState<_NarrowInbox> {
  bool _feedsOpen = false;

  @override
  Widget build(BuildContext context) {
    final selection = ref.watch(rssSelectionProvider);
    return Stack(
      children: [
        Container(
          color: BiuTokens.bg,
          child: Column(
            children: [
              Container(
                padding: const EdgeInsets.symmetric(
                    horizontal: BiuTokens.space3, vertical: BiuTokens.space2),
                decoration: BoxDecoration(
                  border: Border(
                      bottom: BorderSide(color: BiuTokens.borderSubtle)),
                ),
                child: Row(
                  children: [
                    IconButton(
                      icon: const Icon(Icons.menu, size: 20),
                      onPressed: () => setState(() => _feedsOpen = true),
                    ),
                    Expanded(
                      child: Text(
                        _feedTitle(ref, selection),
                        style: const TextStyle(
                            fontSize: 14, fontWeight: FontWeight.w600),
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                  ],
                ),
              ),
              const Expanded(child: EntriesPane()),
            ],
          ),
        ),
        if (selection.selectedEntryId != null)
          Positioned.fill(
            child: GestureDetector(
              onTap: () =>
                  ref.read(rssSelectionProvider.notifier).selectEntry(null),
              child: Container(color: Colors.black.withValues(alpha: 0.4)),
            ),
          ),
        if (selection.selectedEntryId != null)
          Positioned(
            left: 0,
            right: 0,
            bottom: 0,
            top: 60,
            child: Material(
              color: BiuTokens.bg,
              borderRadius: const BorderRadius.vertical(
                  top: Radius.circular(BiuTokens.radiusLg)),
              child: const ReaderPane(),
            ),
          ),
        if (_feedsOpen)
          Positioned.fill(
            child: GestureDetector(
              onTap: () => setState(() => _feedsOpen = false),
              child: Container(color: Colors.black.withValues(alpha: 0.4)),
            ),
          ),
        if (_feedsOpen)
          Positioned(
            left: 0,
            top: 0,
            bottom: 0,
            child: SizedBox(
              width: 260,
              child: Material(
                color: BiuTokens.surface,
                child: const FeedsPane(),
              ),
            ),
          ),
      ],
    );
  }

  String _feedTitle(WidgetRef ref, RssSelection selection) {
    if (selection.selectedFeedId == 'all') return '全部';
    final feeds = ref.watch(feedsProvider).valueOrNull ?? const <Feed>[];
    for (final f in feeds) {
      if (f.id == selection.selectedFeedId) return f.title;
    }
    return '全部';
  }
}

/// Internal: 用于 launch entry url 时的便捷 helper（preserved for the
/// narrow layout reader interactions even though most launches go via
/// the reader pane itself).
Future<void> launchEntryUrl(Entry entry) async {
  final uri = safeParseUri(entry.url);
  if (uri == null) return;
  await launchUrl(uri, mode: LaunchMode.externalApplication);
}
