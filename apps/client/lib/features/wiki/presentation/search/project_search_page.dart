/// /wiki/p/:pid/search —— 项目内搜索页。
///
/// 数据：调 brain `POST /v1/search`（已支持 project_id + scope='wiki' 过滤），
/// 返回 BM25 / 向量 / 图谱关系扩展三路 hit，按 score desc 合并展示。
///
/// UX：300ms debounce 输入 → 自动触发 search；清空输入 → 列表清空；点击
/// 命中行跳到 /wiki/p/:pid/pages/:pageId。
library;

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme.dart';
import '../../../../core/layout/phone_nav.dart';
import '../../../../data/api/wiki_client.dart'
    show WikiSearchHit, WikiSearchResult;
import '../../../../data/wiki_providers.dart' show wikiRepositoryProvider;

class ProjectSearchPage extends ConsumerStatefulWidget {
  const ProjectSearchPage({super.key, required this.projectId});

  final String projectId;

  @override
  ConsumerState<ProjectSearchPage> createState() => _ProjectSearchPageState();
}

class _ProjectSearchPageState extends ConsumerState<ProjectSearchPage> {
  final TextEditingController _ctrl = TextEditingController();
  final FocusNode _focus = FocusNode();
  Timer? _debounce;
  String _query = '';
  WikiSearchResult? _result;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      _focus.requestFocus();
    });
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _ctrl.dispose();
    _focus.dispose();
    super.dispose();
  }

  void _onChanged(String v) {
    _debounce?.cancel();
    if (v.trim().isEmpty) {
      setState(() {
        _result = null;
        _query = '';
        _error = null;
      });
      return;
    }
    _debounce = Timer(const Duration(milliseconds: 300), () => _runSearch(v));
  }

  Future<void> _runSearch(String q) async {
    if (!mounted) return;
    final query = q.trim();
    if (query.isEmpty) return;
    setState(() {
      _busy = true;
      _query = query;
      _error = null;
    });
    final repo = ref.read(wikiRepositoryProvider);
    if (repo == null) {
      setState(() {
        _busy = false;
        _error = '未配置后端凭证';
      });
      return;
    }
    try {
      final res =
          await repo.client.searchInProject(widget.projectId, query: query);
      if (!mounted || _query != query) return; // 过期请求
      setState(() {
        _result = res;
        _busy = false;
      });
    } on Exception catch (e) {
      if (!mounted) return;
      setState(() {
        _error = '搜索失败：$e';
        _busy = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      children: [
        _SearchBar(
          controller: _ctrl,
          focusNode: _focus,
          busy: _busy,
          onChanged: _onChanged,
          onSubmit: () => _runSearch(_ctrl.text),
        ),
        Divider(height: 1, color: BiuTokens.borderSubtle),
        Expanded(child: _Body(
          query: _query,
          result: _result,
          busy: _busy,
          error: _error,
          projectId: widget.projectId,
        )),
      ],
    );
  }
}

class _SearchBar extends StatelessWidget {
  const _SearchBar({
    required this.controller,
    required this.focusNode,
    required this.busy,
    required this.onChanged,
    required this.onSubmit,
  });

  final TextEditingController controller;
  final FocusNode focusNode;
  final bool busy;
  final ValueChanged<String> onChanged;
  final VoidCallback onSubmit;

  @override
  Widget build(BuildContext context) {
    return Container(
      height: 48,
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Row(
        children: [
          // 子页头左位 ← (手机形态; 桌面 shrink 不占位, §3.3)。
          const PhoneBackButton(),
          Icon(Icons.search, size: 18, color: BiuTokens.textSecondary),
          const SizedBox(width: 10),
          Expanded(
            child: TextField(
              controller: controller,
              focusNode: focusNode,
              onChanged: onChanged,
              onSubmitted: (_) => onSubmit(),
              decoration: InputDecoration(
                hintText: '搜索本项目页面 / 块 / 关系图谱…',
                hintStyle:
                    TextStyle(color: BiuTokens.textMuted, fontSize: 13),
                border: InputBorder.none,
                isDense: true,
                contentPadding: const EdgeInsets.symmetric(vertical: 8),
              ),
              style: TextStyle(color: BiuTokens.text, fontSize: 13),
            ),
          ),
          if (busy)
            const SizedBox(
              width: 14,
              height: 14,
              child: CircularProgressIndicator(strokeWidth: 1.5),
            )
          else if (controller.text.isNotEmpty)
            IconButton(
              tooltip: '清空',
              onPressed: () {
                controller.clear();
                onChanged('');
              },
              icon: const Icon(Icons.close, size: 16),
              padding: EdgeInsets.zero,
              constraints: const BoxConstraints(minWidth: 28, minHeight: 28),
            ),
        ],
      ),
    );
  }
}

class _Body extends StatelessWidget {
  const _Body({
    required this.query,
    required this.result,
    required this.busy,
    required this.error,
    required this.projectId,
  });

  final String query;
  final WikiSearchResult? result;
  final bool busy;
  final String? error;
  final String projectId;

  @override
  Widget build(BuildContext context) {
    if (error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: SelectableText(
            error!,
            style: TextStyle(color: BiuTokens.error, fontSize: 12),
          ),
        ),
      );
    }
    if (query.isEmpty) {
      return _PlaceholderTip();
    }
    if (busy && result == null) {
      return const Center(child: CircularProgressIndicator());
    }
    final hits = result?.hits ?? const <WikiSearchHit>[];
    if (hits.isEmpty) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Text(
            '没有匹配 "$query" 的内容',
            style: TextStyle(color: BiuTokens.textMuted, fontSize: 13),
          ),
        ),
      );
    }
    return ListView.separated(
      padding: const EdgeInsets.symmetric(vertical: 8),
      itemCount: hits.length,
      separatorBuilder: (_, _) => Divider(
        height: 1,
        color: BiuTokens.borderSubtle.withValues(alpha: 0.6),
      ),
      itemBuilder: (_, i) =>
          _HitRow(hit: hits[i], projectId: projectId, query: query),
    );
  }
}

class _PlaceholderTip extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.travel_explore_outlined,
              size: 48, color: BiuTokens.textMuted),
          const SizedBox(height: 12),
          Text(
            '输入关键词开始搜索',
            style: TextStyle(color: BiuTokens.text, fontSize: 14),
          ),
          const SizedBox(height: 4),
          Text(
            'BM25 关键词 · 向量语义 · 图谱关系扩展',
            style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
          ),
        ],
      ),
    );
  }
}

class _HitRow extends StatelessWidget {
  const _HitRow({
    required this.hit,
    required this.projectId,
    required this.query,
  });

  final WikiSearchHit hit;
  final String projectId;
  final String query;

  @override
  Widget build(BuildContext context) {
    // 搜索类型用 brand / accent / text — 跟随当前色板,wiki=keyword=brand
     // 是因为"keyword 搜索"是 BiuMind 的核心能力,vector="语义"用 accent,
     // graph 中性色。三类可视化差异保留。
    final cs = Theme.of(context).colorScheme;
    final (icon, kindLabel, kindColor) = switch (hit.kind) {
      'wiki' => (
        Icons.description_outlined,
        '关键词',
        cs.primary,
      ),
      'vector' => (
        Icons.psychology_outlined,
        '语义',
        cs.secondary,
      ),
      'graph' => (
        Icons.hub_outlined,
        '图谱',
        BiuTokens.text,
      ),
      _ => (
        Icons.help_outline,
        hit.kind,
        BiuTokens.textMuted,
      ),
    };

    return InkWell(
      onTap: () => context.go('/wiki/p/$projectId/pages/${hit.pageId}'),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Icon(icon, size: 16, color: BiuTokens.textSecondary),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          hit.title.isEmpty ? '(未命名)' : hit.title,
                          style: TextStyle(
                            color: BiuTokens.text,
                            fontSize: 13,
                            fontWeight: FontWeight.w600,
                          ),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                      const SizedBox(width: 8),
                      _Badge(label: kindLabel, color: kindColor),
                      const SizedBox(width: 6),
                      Text(
                        hit.score.toStringAsFixed(2),
                        style: TextStyle(
                          color: BiuTokens.textMuted,
                          fontSize: 11,
                          fontFeatures: const <FontFeature>[
                            FontFeature.tabularFigures(),
                          ],
                        ),
                      ),
                    ],
                  ),
                  if (hit.snippet.isNotEmpty) ...[
                    const SizedBox(height: 4),
                    Text(
                      hit.snippet,
                      style: TextStyle(
                        color: BiuTokens.textSecondary,
                        fontSize: 12,
                        height: 1.4,
                      ),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ],
                  if (hit.viaSeedPageId != null) ...[
                    const SizedBox(height: 4),
                    Text(
                      '通过相关页发现',
                      style: TextStyle(
                        color: BiuTokens.textMuted,
                        fontSize: 11,
                      ),
                    ),
                  ],
                ],
              ),
            ),
            Icon(Icons.chevron_right, size: 16, color: BiuTokens.textMuted),
          ],
        ),
      ),
    );
  }
}

class _Badge extends StatelessWidget {
  const _Badge({required this.label, required this.color});
  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.1),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 10,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}
