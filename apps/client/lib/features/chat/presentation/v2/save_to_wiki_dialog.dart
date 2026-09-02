/// 消息级「存为 Wiki 页」—— 把一条 assistant 消息沉淀进知识库。
///
/// 对齐 llm_wiki chat-save-to-wiki.ts 的思路（Apache-2.0 重写，不拷贝代码）：
/// 清洗 think 块与 save-worthy 注释标记 → 取首个可见行做标题 →
/// 选项目建页 + PUT body。标题碰撞由 wiki 页面树天然容忍（biumind 页面
/// 按 id 寻址，不像 reference 按文件名，故无需 slug+时间戳防碰撞）。
library;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme.dart';
import '../../../../data/api/wiki_client.dart';
import '../../../../data/wiki_providers.dart';
import '../../../../data/wiki_repository.dart';

/// 清洗 assistant 消息为可落库的 markdown：剥 think/thinking 块
/// （含未闭合的流式残段）与 save-worthy / sources 注释标记。
String cleanAssistantContentForWikiSave(String content) {
  return content
      .replaceAll(
          RegExp(r'<!--\s*(?:save-worthy|sources):[^\n]*?-->'), '')
      .replaceAll(
          RegExp(r'<think(?:ing)?>\s*[\s\S]*?</think(?:ing)?>\s*',
              caseSensitive: false),
          '')
      .replaceAll(
          RegExp(r'<think(?:ing)?>\s*[\s\S]*$', caseSensitive: false), '')
      .trimLeft()
      .trimRight();
}

/// 从清洗后的内容取标题：首个可见行（剥 # 前缀），截 60 字符。
String titleFromCleanAssistantContent(String clean) {
  for (final line in clean.split('\n')) {
    final t = line.replaceAll(RegExp(r'^#+\s*'), '').trim();
    if (t.isNotEmpty) {
      return t.length > 60 ? t.substring(0, 60) : t;
    }
  }
  return '未命名保存';
}

/// 弹「存为 Wiki 页」对话框。返回 true = 已保存（调用方无需处理，
/// 成功反馈与跳转在对话框内部完成）。
Future<void> showSaveToWikiDialog(
  BuildContext context,
  WidgetRef ref, {
  required String content,
}) async {
  final repo = ref.read(wikiRepositoryProvider);
  if (repo == null) {
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('知识库未就绪，请稍后再试')),
    );
    return;
  }
  final clean = cleanAssistantContentForWikiSave(content);
  if (clean.isEmpty) {
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('消息没有可保存的正文')),
    );
    return;
  }
  await showDialog<void>(
    context: context,
    builder: (ctx) => _SaveToWikiDialog(repo: repo, clean: clean),
  );
}

class _SaveToWikiDialog extends StatefulWidget {
  const _SaveToWikiDialog({required this.repo, required this.clean});

  final WikiRepository repo;
  final String clean;

  @override
  State<_SaveToWikiDialog> createState() => _SaveToWikiDialogState();
}

class _SaveToWikiDialogState extends State<_SaveToWikiDialog> {
  late final TextEditingController _titleCtrl =
      TextEditingController(text: titleFromCleanAssistantContent(widget.clean));
  late final Future<List<WikiProject>> _projectsFut =
      widget.repo.client.listProjects();
  String? _selectedProjectId;
  bool _saving = false;
  String? _error;

  @override
  void dispose() {
    _titleCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('存为 Wiki 页'),
      content: SizedBox(
        width: 420,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            TextField(
              controller: _titleCtrl,
              autofocus: true,
              decoration: const InputDecoration(
                labelText: '页面标题',
                hintText: '取消息首个可见行，可改',
              ),
            ),
            const SizedBox(height: 12),
            FutureBuilder<List<WikiProject>>(
              future: _projectsFut,
              builder: (context, snap) {
                if (snap.hasError) {
                  return Text('项目列表加载失败：${snap.error}',
                      style: TextStyle(color: BiuTokens.error, fontSize: 12));
                }
                if (!snap.hasData) {
                  return const Padding(
                    padding: EdgeInsets.symmetric(vertical: 12),
                    child: Center(child: CircularProgressIndicator()),
                  );
                }
                final projects = snap.data!;
                if (projects.isEmpty) {
                  return Text('还没有知识库项目，请先到 Wiki 模块创建',
                      style:
                          TextStyle(color: BiuTokens.textMuted, fontSize: 12));
                }
                _selectedProjectId ??= projects.first.id;
                return DropdownButtonFormField<String>(
                  initialValue: _selectedProjectId,
                  decoration: const InputDecoration(labelText: '存入项目'),
                  items: [
                    for (final p in projects)
                      DropdownMenuItem(value: p.id, child: Text(p.name)),
                  ],
                  onChanged: (v) => setState(() => _selectedProjectId = v),
                );
              },
            ),
            if (_error != null) ...[
              const SizedBox(height: 8),
              Text(_error!,
                  style: TextStyle(color: BiuTokens.error, fontSize: 12)),
            ],
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: _saving ? null : () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: _saving ? null : _save,
          child: Text(_saving ? '保存中…' : '保存'),
        ),
      ],
    );
  }

  Future<void> _save() async {
    final title = _titleCtrl.text.trim();
    final pid = _selectedProjectId;
    if (title.isEmpty || pid == null) return;
    setState(() {
      _saving = true;
      _error = null;
    });
    try {
      final page = await widget.repo.client.createPage(pid, title: title);
      // 新建页 version=1；updatePageBody 顺带把页面对账进本地 Drift 缓存。
      await widget.repo
          .updatePageBody(pid, page.id, widget.clean, page.version);
      if (!mounted) return;
      Navigator.pop(context);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text('已存为 Wiki 页：$title'),
          action: SnackBarAction(
            label: '打开',
            onPressed: () => context.go('/wiki/p/$pid/pages/${page.id}'),
          ),
        ),
      );
    } on Exception catch (e) {
      if (!mounted) return;
      setState(() {
        _saving = false;
        _error = '保存失败：$e';
      });
    }
  }
}
