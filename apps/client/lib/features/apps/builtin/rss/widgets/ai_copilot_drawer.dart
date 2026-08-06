// M9.4 RSS AI Co-Pilot — 右抽屉同步 Q&A.
//
// 设计:
//   - EndDrawer 由 RssAppPage 的 Scaffold 提供; 这里只画内容
//   - 顶部 view_kind 切换 chip (today/inbox/radar) + 下方输入框 + 提交按钮
//   - 历史显示在中间 (ListView reversed): 最新一对 (Q + A) 在底部
//   - A 段渲染: gpt_markdown + 自定义 [N] 引用按钮链接到 reader
//   - 点 [N] chip 调 url_launcher 打开 entry.url (跳 reader 留 v2 polish)
//
// 状态机:
//   idle / asking / answered / error
//   asking 时输入框 disabled, 显示 spinner

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:gpt_markdown/gpt_markdown.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../../../app/theme.dart';
import '../models.dart';
import '../providers.dart';

class AICopilotDrawer extends ConsumerStatefulWidget {
  const AICopilotDrawer({super.key});

  @override
  ConsumerState<AICopilotDrawer> createState() => _AICopilotDrawerState();
}

class _Turn {
  _Turn(this.question);
  final String question;
  CopilotAnswer? answer; // set when LLM returns
  String? error;         // set on failure
}

class _AICopilotDrawerState extends ConsumerState<AICopilotDrawer> {
  final _ctrl = TextEditingController();
  final _scroll = ScrollController();
  final List<_Turn> _turns = [];
  String _viewKind = 'today';
  bool _busy = false;

  @override
  void dispose() {
    _ctrl.dispose();
    _scroll.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final q = _ctrl.text.trim();
    if (q.isEmpty || _busy) return;
    final actions = ref.read(rssActionsProvider);
    if (actions == null) return;

    final turn = _Turn(q);
    setState(() {
      _turns.add(turn);
      _busy = true;
      _ctrl.clear();
    });
    _scrollToBottom();

    try {
      final ans = await actions.copilotAsk(
        question: q,
        viewKind: _viewKind,
      );
      if (!mounted) return;
      setState(() => turn.answer = ans);
    } catch (e) {
      if (!mounted) return;
      setState(() => turn.error = '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
      _scrollToBottom();
    }
  }

  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scroll.hasClients) {
        _scroll.animateTo(
          _scroll.position.maxScrollExtent,
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
        );
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Column(
        children: [
          _Header(
            viewKind: _viewKind,
            onChange: _busy ? null : (v) => setState(() => _viewKind = v),
          ),
          Expanded(
            child: _turns.isEmpty
                ? const _EmptyHint()
                : ListView.builder(
                    controller: _scroll,
                    padding: const EdgeInsets.all(BiuTokens.space4),
                    itemCount: _turns.length,
                    itemBuilder: (_, i) => _TurnView(turn: _turns[i]),
                  ),
          ),
          _Composer(
            controller: _ctrl,
            busy: _busy,
            onSubmit: _submit,
          ),
        ],
      ),
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.viewKind, required this.onChange});
  final String viewKind;
  final ValueChanged<String>? onChange;

  static const _options = [
    ('today', 'Today'),
    ('inbox', '收件箱'),
    ('radar', '雷达'),
  ];

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.fromLTRB(
          BiuTokens.space4, BiuTokens.space4, BiuTokens.space4, BiuTokens.space2),
      decoration: BoxDecoration(
        border: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.auto_awesome, size: 16, color: BiuTokens.purple),
              const SizedBox(width: 6),
              const Text('RSS Co-Pilot',
                  style: TextStyle(fontSize: 14, fontWeight: FontWeight.w700)),
              const Spacer(),
              Text('⌘J 切换',
                  style:
                      TextStyle(fontSize: 10, color: BiuTokens.textMuted)),
            ],
          ),
          const SizedBox(height: BiuTokens.space2),
          Wrap(
            spacing: 6,
            children: _options
                .map((o) => ChoiceChip(
                      label: Text(o.$2,
                          style: const TextStyle(fontSize: 11)),
                      selected: viewKind == o.$1,
                      onSelected: onChange == null
                          ? null
                          : (sel) {
                              if (sel) onChange!(o.$1);
                            },
                      visualDensity: VisualDensity.compact,
                    ))
                .toList(),
          ),
        ],
      ),
    );
  }
}

class _EmptyHint extends StatelessWidget {
  const _EmptyHint();
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.all(BiuTokens.space5),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Icon(Icons.lightbulb_outline, size: 28, color: BiuTokens.textMuted),
          const SizedBox(height: BiuTokens.space2),
          Text(
            '问问 AI 当前视图里的任何事',
            style: TextStyle(
                fontSize: 13, color: BiuTokens.textSecondary),
          ),
          const SizedBox(height: BiuTokens.space2),
          Text(
            '· 今天 AI 监管的新闻\n'
            '· 总结一下苹果发布会\n'
            '· 这周关注的话题是什么',
            style: TextStyle(
                fontSize: 11, color: BiuTokens.textMuted, height: 1.6),
          ),
        ],
      ),
    );
  }
}

class _TurnView extends StatelessWidget {
  const _TurnView({required this.turn});
  final _Turn turn;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // Question bubble — right aligned
        Align(
          alignment: Alignment.centerRight,
          child: Container(
            margin: const EdgeInsets.only(bottom: 6),
            padding: const EdgeInsets.symmetric(
                horizontal: BiuTokens.space3, vertical: BiuTokens.space2),
            constraints:
                BoxConstraints(maxWidth: MediaQuery.of(context).size.width * 0.7),
            decoration: BoxDecoration(
              color: BiuTokens.purple.withValues(alpha: 0.10),
              borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
            ),
            child: Text(turn.question,
                style: const TextStyle(fontSize: 13, height: 1.4)),
          ),
        ),
        // Answer / error / loading
        if (turn.error != null)
          Container(
            margin: const EdgeInsets.only(bottom: BiuTokens.space3),
            padding: const EdgeInsets.all(BiuTokens.space3),
            decoration: BoxDecoration(
              color: Colors.redAccent.withValues(alpha: 0.06),
              border: Border.all(color: Colors.redAccent.withValues(alpha: 0.3)),
              borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
            ),
            child: Text(turn.error!,
                style: const TextStyle(fontSize: 12, color: Colors.redAccent)),
          )
        else if (turn.answer == null)
          const Padding(
            padding: EdgeInsets.symmetric(vertical: BiuTokens.space3),
            child: SizedBox(
              width: 16,
              height: 16,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
          )
        else
          _AnswerView(answer: turn.answer!),
      ],
    );
  }
}

class _AnswerView extends StatelessWidget {
  const _AnswerView({required this.answer});
  final CopilotAnswer answer;

  @override
  Widget build(BuildContext context) {
    return Container(
      margin: const EdgeInsets.only(bottom: BiuTokens.space4),
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        border: Border.all(color: BiuTokens.borderSubtle),
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          GptMarkdown(answer.answer,
              style: const TextStyle(fontSize: 13, height: 1.55)),
          if (answer.citations.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space3),
            Divider(height: 1, color: BiuTokens.borderSubtle),
            const SizedBox(height: BiuTokens.space2),
            Text('引用',
                style: TextStyle(
                    fontSize: 10,
                    color: BiuTokens.textMuted,
                    fontWeight: FontWeight.w500)),
            const SizedBox(height: 4),
            Wrap(
              spacing: 6,
              runSpacing: 4,
              children: answer.citations
                  .map((c) => _CitationChip(c: c))
                  .toList(),
            ),
          ],
        ],
      ),
    );
  }
}

class _CitationChip extends StatelessWidget {
  const _CitationChip({required this.c});
  final CopilotCitation c;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
      onTap: c.url.isEmpty
          ? null
          : () => launchUrl(Uri.parse(c.url),
              mode: LaunchMode.externalApplication),
      child: Container(
        padding:
            const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
        decoration: BoxDecoration(
          color: BiuTokens.purple.withValues(alpha: 0.08),
          border: Border.all(color: BiuTokens.purple.withValues(alpha: 0.3)),
          borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text('[${c.n}]',
                style: TextStyle(
                    fontSize: 10,
                    fontWeight: FontWeight.w700,
                    color: BiuTokens.purple)),
            const SizedBox(width: 4),
            ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 200),
              child: Text(
                c.title,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                    fontSize: 10, color: BiuTokens.textSecondary),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _Composer extends StatelessWidget {
  const _Composer({
    required this.controller,
    required this.busy,
    required this.onSubmit,
  });

  final TextEditingController controller;
  final bool busy;
  final VoidCallback onSubmit;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        border: Border(top: BorderSide(color: BiuTokens.borderSubtle)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Expanded(
            child: TextField(
              controller: controller,
              enabled: !busy,
              minLines: 1,
              maxLines: 4,
              decoration: const InputDecoration(
                hintText: '问点什么...',
                isDense: true,
                border: OutlineInputBorder(),
              ),
              style: const TextStyle(fontSize: 13),
              onSubmitted: (_) => onSubmit(),
            ),
          ),
          const SizedBox(width: BiuTokens.space2),
          IconButton(
            onPressed: busy ? null : onSubmit,
            icon: busy
                ? const SizedBox(
                    width: 16,
                    height: 16,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : Icon(Icons.send, size: 18, color: BiuTokens.purple),
            tooltip: '发送 (Enter)',
          ),
        ],
      ),
    );
  }
}
