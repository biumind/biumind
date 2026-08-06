// Git 历史面板(CORE-5)—— 右栏「历史」。
//
// 主从视图:gitLog 列提交;点提交 → gitShowDiff 看该提交统一 diff(+ commit 元信息)。
// 走 daemon bridge(git.log / git.showDiff,M4-A 已就绪),作用于当前活动项目。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../application/projects_controller.dart';
import '../data/code_bridge_provider.dart';
import '../domain/git_models.dart' show GitCommit;

class GitHistoryPanel extends ConsumerStatefulWidget {
  const GitHistoryPanel({super.key});

  @override
  ConsumerState<GitHistoryPanel> createState() => _GitHistoryPanelState();
}

class _GitHistoryPanelState extends ConsumerState<GitHistoryPanel> {
  List<GitCommit> _commits = const [];
  bool _loading = true;
  String? _error;
  GitCommit? _selected;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  Future<void> _load() async {
    final bridge = ref.read(codeBridgeClientProvider);
    final root = ref.read(activeCodeProjectProvider)?.path;
    if (bridge == null || root == null) {
      setState(() {
        _loading = false;
        _error = bridge == null ? '本地 daemon 未就绪' : '未打开项目';
      });
      return;
    }
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final commits = await bridge.gitLog(root, limit: 100);
      if (!mounted) return;
      setState(() {
        _commits = commits;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Container(
          height: 32,
          padding: const EdgeInsets.only(left: 12, right: 4),
          decoration: BoxDecoration(
            border: Border(bottom: BorderSide(color: BiuTokens.borderSubtle)),
          ),
          child: Row(
            children: [
              if (_selected != null)
                InkWell(
                  onTap: () => setState(() => _selected = null),
                  child: Icon(Icons.arrow_back_rounded,
                      size: 15, color: BiuTokens.textSecondary),
                ),
              if (_selected != null) const SizedBox(width: 8),
              Expanded(
                child: Text(
                  _selected == null ? '提交历史' : _selected!.shortHash,
                  style: TextStyle(
                      fontSize: 11.5,
                      fontWeight: FontWeight.w700,
                      letterSpacing: 0.3,
                      color: BiuTokens.textSecondary),
                ),
              ),
              if (_selected == null)
                InkWell(
                  onTap: _load,
                  child: Padding(
                    padding: const EdgeInsets.all(5),
                    child: Icon(Icons.refresh_rounded,
                        size: 15, color: BiuTokens.textSecondary),
                  ),
                ),
            ],
          ),
        ),
        Expanded(child: _body()),
      ],
    );
  }

  Widget _body() {
    if (_loading) {
      return const Center(child: CircularProgressIndicator(strokeWidth: 2));
    }
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Text(_error!,
              textAlign: TextAlign.center,
              style: TextStyle(fontSize: 12.5, color: BiuTokens.textMuted)),
        ),
      );
    }
    if (_selected != null) {
      return _CommitDetailView(commit: _selected!);
    }
    if (_commits.isEmpty) {
      return Center(
        child: Text('无提交记录',
            style: TextStyle(fontSize: 12.5, color: BiuTokens.textMuted)),
      );
    }
    return ListView.builder(
      itemCount: _commits.length,
      itemBuilder: (ctx, i) {
        final c = _commits[i];
        return InkWell(
          onTap: () => setState(() => _selected = c),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(c.message,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(fontSize: 12.5, height: 1.35)),
                const SizedBox(height: 3),
                Row(
                  children: [
                    Text(c.shortHash,
                        style: TextStyle(
                            fontSize: 10.5,
                            fontFamily: 'SF Mono',
                            color: BiuTokens.purple)),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text('${c.author} · ${c.date}',
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(
                              fontSize: 10.5, color: BiuTokens.textMuted)),
                    ),
                  ],
                ),
              ],
            ),
          ),
        );
      },
    );
  }
}

class _CommitDetailView extends ConsumerStatefulWidget {
  const _CommitDetailView({required this.commit});
  final GitCommit commit;

  @override
  ConsumerState<_CommitDetailView> createState() => _CommitDetailViewState();
}

class _CommitDetailViewState extends ConsumerState<_CommitDetailView> {
  String? _diff;
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  Future<void> _load() async {
    final bridge = ref.read(codeBridgeClientProvider);
    final root = ref.read(activeCodeProjectProvider)?.path;
    if (bridge == null || root == null) {
      setState(() {
        _loading = false;
        _error = 'daemon / 项目不可用';
      });
      return;
    }
    try {
      final diff = await bridge.gitShowDiff(root, widget.commit.hash);
      if (!mounted) return;
      setState(() {
        _diff = diff;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final c = widget.commit;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        // 提交元信息。
        Padding(
          padding: const EdgeInsets.fromLTRB(12, 10, 12, 8),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(c.message,
                  style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600)),
              const SizedBox(height: 4),
              Text('${c.author} · ${c.date}',
                  style: TextStyle(fontSize: 11, color: BiuTokens.textMuted)),
            ],
          ),
        ),
        Divider(height: 1, color: BiuTokens.borderSubtle),
        Expanded(child: _diffBody()),
      ],
    );
  }

  Widget _diffBody() {
    if (_loading) {
      return const Center(child: CircularProgressIndicator(strokeWidth: 2));
    }
    if (_error != null) {
      return Center(
        child: Text(_error!,
            style: TextStyle(fontSize: 12, color: BiuTokens.textMuted)),
      );
    }
    final diff = _diff ?? '';
    if (diff.trim().isEmpty) {
      return Center(
        child: Text('无文件变更',
            style: TextStyle(fontSize: 12, color: BiuTokens.textMuted)),
      );
    }
    return _DiffText(diff: diff);
  }
}

/// 统一 diff 文本渲染:按行着色(+ 绿 / - 红 / @@ 紫 / 其余默认)。
class _DiffText extends StatelessWidget {
  const _DiffText({required this.diff});
  final String diff;

  @override
  Widget build(BuildContext context) {
    final lines = diff.split('\n');
    return SelectionArea(
      child: Scrollbar(
        child: SingleChildScrollView(
          primary: true,
          child: SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 12),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  for (final line in lines) _line(line),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _line(String line) {
    Color color = BiuTokens.textSecondary;
    if (line.startsWith('+') && !line.startsWith('+++')) {
      color = BiuTokens.green;
    } else if (line.startsWith('-') && !line.startsWith('---')) {
      color = BiuTokens.error;
    } else if (line.startsWith('@@')) {
      color = BiuTokens.purple;
    } else if (line.startsWith('diff ') ||
        line.startsWith('+++') ||
        line.startsWith('---') ||
        line.startsWith('index ')) {
      color = BiuTokens.textMuted;
    }
    return Text(
      line.isEmpty ? ' ' : line,
      style: TextStyle(
        fontSize: 11.5,
        height: 1.4,
        fontFamily: 'JetBrains Mono, ui-monospace, monospace',
        color: color,
      ),
    );
  }
}
