// /wiki 工作区主页 —— 列出所有 wiki 项目（卡片网格）+ 新建项目入口。
//
// 借鉴 knowcode projects_page 的视觉，但当前 biumind brain 后端只有
// 「项目」一级（knowcode 的 "workspace + project" 在 biumind 里目前没
// 分层），所以省去工作区编号行；后续如果接入工作区维度再补。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../app/theme.dart';
import '../../../../core/layout/phone_nav.dart';
import '../../../../core/ui/biu_text_field.dart';
import '../../application/wiki_controller.dart';
import '../../../../data/wiki_repository.dart' show RepoProject;
import 'welcome_screen.dart';

class ProjectsPage extends ConsumerWidget {
  const ProjectsPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final stateAsync = ref.watch(wikiControllerProvider);

    return stateAsync.when(
      loading: () => const Center(child: CircularProgressIndicator()),
      error: (e, _) => Center(child: SelectableText(e.toString())),
      data: (s) {
        if (s.noCredentials) {
          return const _NoCredsView();
        }
        return _Body(projects: s.projects);
      },
    );
  }
}

class _Body extends ConsumerStatefulWidget {
  const _Body({required this.projects});
  final List<RepoProject> projects;

  @override
  ConsumerState<_Body> createState() => _BodyState();
}

class _BodyState extends ConsumerState<_Body> {
  /// 新建项目；可选 [template] 携带模板信息（dialog 副标 + 自动项目名
  /// 建议 + 透传 template_id 让 brain seed schema/purpose 页）。`general`
  /// 等同空白（不 seed）。
  Future<void> _newProject({ProjectTemplate? template}) async {
    final ctrl = TextEditingController(
      text: template == null || template.id == 'general'
          ? ''
          : '${template.title} - ',
    );
    final name = await showDialog<String>(
      context: context,
      builder: (c) => AlertDialog(
        title: Text(template == null ? '新建项目' : '新建：${template.title}'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (template != null && template.id != 'general') ...[
              Text(
                template.description,
                style: TextStyle(color: BiuTokens.textMuted, fontSize: 12),
              ),
              const SizedBox(height: 12),
            ],
            BiuTextField(
              controller: ctrl,
              autofocus: true,
              labelText: '项目名',
              onSubmitted: (_) => Navigator.pop(c, ctrl.text.trim()),
            ),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(c, ctrl.text.trim()),
            child: const Text('创建'),
          ),
        ],
      ),
    );
    if (name == null || name.isEmpty) return;
    final p = await ref
        .read(wikiControllerProvider.notifier)
        .createProject(name, templateId: template?.id);
    if (!mounted) return;
    enterSubPage(context, '/wiki/p/${p.id}');
  }

  @override
  Widget build(BuildContext context) {
    if (widget.projects.isEmpty) {
      return Column(
        children: [
          _Header(onNewWorkspace: () => _newProject()),
          Expanded(
            child: WelcomeScreen(
              onCreate: (tpl) => _newProject(template: tpl),
            ),
          ),
        ],
      );
    }
    return SingleChildScrollView(
      padding: const EdgeInsets.fromLTRB(40, 32, 40, 40),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _Header(onNewWorkspace: () => _newProject()),
          const SizedBox(height: 32),
          _ProjectGrid(
            projects: widget.projects,
            onCreate: () => _newProject(),
          ),
        ],
      ),
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.onNewWorkspace});
  final VoidCallback onNewWorkspace;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Icon(Icons.add, size: 18, color: BiuTokens.textMuted),
        const SizedBox(width: 6),
        Text(
          '新建工作区',
          style: TextStyle(
            fontSize: 13,
            color: BiuTokens.textSecondary,
            fontWeight: FontWeight.w500,
          ),
        ),
        const Spacer(),
        OutlinedButton.icon(
          onPressed: onNewWorkspace,
          icon: const Icon(Icons.add, size: 16),
          label: const Text('新建工作区'),
          style: OutlinedButton.styleFrom(
            foregroundColor: BiuTokens.textSecondary,
            side: BorderSide(color: BiuTokens.borderSubtle),
            padding: EdgeInsets.symmetric(horizontal: 14, vertical: 10),
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
            ),
            textStyle:
                TextStyle(fontSize: 13, fontWeight: FontWeight.w500),
          ),
        ),
      ],
    );
  }
}

// _EmptyHint 已被 WelcomeScreen 替代（B7 补完）— 删除。

class _ProjectGrid extends StatelessWidget {
  const _ProjectGrid({required this.projects, required this.onCreate});
  final List<RepoProject> projects;
  final VoidCallback onCreate;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(builder: (context, c) {
      const minCardWidth = 240.0;
      const gap = 16.0;
      final maxWidth = c.maxWidth;
      final columns =
          ((maxWidth + gap) / (minCardWidth + gap)).floor().clamp(2, 4);
      final cardWidth = (maxWidth - gap * (columns - 1)) / columns;
      return Wrap(
        spacing: gap,
        runSpacing: gap,
        children: [
          for (final p in projects)
            SizedBox(
              width: cardWidth,
              child: _ProjectCard(project: p),
            ),
          SizedBox(
            width: cardWidth,
            child: _CreateCard(onTap: onCreate),
          ),
        ],
      );
    });
  }
}

class _ProjectCard extends StatefulWidget {
  const _ProjectCard({required this.project});
  final RepoProject project;

  @override
  State<_ProjectCard> createState() => _ProjectCardState();
}

class _ProjectCardState extends State<_ProjectCard> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final p = widget.project;
    final theme = Theme.of(context);
    final brand = theme.colorScheme.primary;
    final brandSoft = theme.colorScheme.primaryContainer;
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      child: GestureDetector(
        onTap: () => enterSubPage(context, '/wiki/p/${p.id}'),
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 120),
          height: 132,
          padding: const EdgeInsets.all(BiuTokens.space4),
          decoration: BoxDecoration(
            color: BiuTokens.surface,
            borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
            border: Border.all(
              color: _hover ? brand : BiuTokens.borderSubtle,
              width: _hover ? 1.5 : 1,
            ),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Container(
                    width: 36,
                    height: 36,
                    decoration: BoxDecoration(
                      color: brandSoft,
                      borderRadius:
                          BorderRadius.circular(BiuTokens.radiusSm),
                    ),
                    alignment: Alignment.center,
                    child: Icon(
                      Icons.bookmark_outline,
                      size: 18,
                      color: brand,
                    ),
                  ),
                  const Spacer(),
                  Icon(Icons.arrow_forward,
                      size: 16, color: BiuTokens.textMuted),
                ],
              ),
              const Spacer(),
              Text(
                p.name.isEmpty ? '(未命名)' : p.name,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                  fontSize: 16,
                  fontWeight: FontWeight.w700,
                  color: BiuTokens.text,
                ),
              ),
              const SizedBox(height: 4),
              Text(
                p.pendingCreate ? '同步中…' : '打开项目',
                style: TextStyle(
                  fontSize: 12,
                  color: BiuTokens.textMuted,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _CreateCard extends StatefulWidget {
  const _CreateCard({required this.onTap});
  final VoidCallback onTap;

  @override
  State<_CreateCard> createState() => _CreateCardState();
}

class _CreateCardState extends State<_CreateCard> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final brand = Theme.of(context).colorScheme.primary;
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      child: GestureDetector(
        onTap: widget.onTap,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 120),
          height: 132,
          decoration: BoxDecoration(
            color: BiuTokens.bg,
            borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
            border: Border.all(
              color: _hover ? brand : BiuTokens.borderSubtle,
              width: 1,
              style: BorderStyle.solid,
            ),
          ),
          alignment: Alignment.center,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(Icons.add,
                  size: 24,
                  color: _hover ? brand : BiuTokens.textMuted),
              const SizedBox(height: 6),
              Text(
                '新建项目',
                style: TextStyle(
                  fontSize: 13,
                  fontWeight: FontWeight.w500,
                  color: _hover ? brand : BiuTokens.textSecondary,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _NoCredsView extends StatelessWidget {
  const _NoCredsView();
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.cloud_off, size: 48),
            const SizedBox(height: 16),
            const Text('未配置后端凭证'),
            const SizedBox(height: 8),
            Text(
              '请先在「全局设置」中登录或填写 model-relay URL + Token。',
              textAlign: TextAlign.center,
              style: TextStyle(color: BiuTokens.textSecondary),
            ),
            const SizedBox(height: 16),
            FilledButton.icon(
              onPressed: () => context.go('/settings'),
              icon: const Icon(Icons.settings),
              label: const Text('打开设置'),
            ),
          ],
        ),
      ),
    );
  }
}
