// Boards tab — responsive grid of newsnow-style colored cards.
// Cards are 340–440px wide; each lazy-fetches its own top-30 snapshot.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../../app/theme.dart';
import '../providers.dart';
import 'board_card.dart';

class BoardsTab extends ConsumerWidget {
  const BoardsTab({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final boardsAsync = ref.watch(boardsProvider);
    return Container(
      color: BiuTokens.bg,
      child: boardsAsync.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(
          child: Padding(
            padding: const EdgeInsets.all(BiuTokens.space5),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(Icons.warning_amber_rounded,
                    size: 36, color: BiuTokens.error),
                const SizedBox(height: BiuTokens.space2),
                SelectableText('加载榜单失败：$e',
                    style: const TextStyle(fontSize: 13)),
                const SizedBox(height: BiuTokens.space3),
                OutlinedButton(
                  onPressed: () => ref.refreshBoards(),
                  child: const Text('重试'),
                ),
              ],
            ),
          ),
        ),
        data: (boards) {
          if (boards.isEmpty) {
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(BiuTokens.space5),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(Icons.trending_up,
                        size: 36, color: BiuTokens.textMuted),
                    const SizedBox(height: BiuTokens.space2),
                    Text('暂无榜单',
                        style: TextStyle(
                            fontSize: 14,
                            color: BiuTokens.textSecondary,
                            fontWeight: FontWeight.w500)),
                    const SizedBox(height: BiuTokens.space1),
                    Text('请等待后端首次拉取，或检查 newsnow 抓取任务',
                        style: TextStyle(
                            fontSize: 12, color: BiuTokens.textMuted)),
                  ],
                ),
              ),
            );
          }
          return RefreshIndicator(
            onRefresh: () async => ref.refreshBoards(),
            child: LayoutBuilder(builder: (ctx, constraints) {
              final width = constraints.maxWidth;
              // Min card width ~340, max ~440. Below 720 → 1 column.
              const targetWidth = 380.0;
              const spacing = BiuTokens.space4;
              const padding = BiuTokens.space4;
              final inner = width - padding * 2;
              int cols;
              if (width < 720) {
                cols = 1;
              } else {
                cols = ((inner + spacing) / (targetWidth + spacing))
                    .floor()
                    .clamp(1, 8);
              }
              final cardWidth = cols == 1
                  ? inner
                  : (inner - spacing * (cols - 1)) / cols;
              const cardHeight = 520.0;
              return SingleChildScrollView(
                physics: const AlwaysScrollableScrollPhysics(),
                padding: const EdgeInsets.all(padding),
                child: Wrap(
                  spacing: spacing,
                  runSpacing: spacing,
                  children: [
                    for (final b in boards)
                      SizedBox(
                        width: cardWidth,
                        height: cardHeight,
                        child: BoardCard(board: b),
                      ),
                  ],
                ),
              );
            }),
          );
        },
      ),
    );
  }
}
