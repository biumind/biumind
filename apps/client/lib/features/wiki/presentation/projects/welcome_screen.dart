/// /wiki 没项目时的欢迎引导页 + 5 套起手模板选择。
///
/// 简化设计（vs knowcode 拆 welcome_screen + template_picker
/// 两文件 + 后端 listProjectTemplates 端点）：
///   - 模板硬编码在客户端（5 套：research / reading / personal /
///     business / general，对齐 llm_wiki）
///   - 选模板 + 输入项目名一气呵成（无独立 dialog）
///
/// 模板 id 透传到 brain createProject（POST /v1/wiki/projects body 带
/// template_id）；brain 按 id seed schema/purpose 页（general 不 seed）。
/// 模板内容（schema.md/purpose.md）权威在 brain 端 templates 包，client
/// 仅持展示元数据。
library;

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';

/// 项目模板元数据。
class ProjectTemplate {
  const ProjectTemplate({
    required this.id,
    required this.title,
    required this.description,
    required this.icon,
    required this.color,
  });
  final String id;
  final String title;
  final String description;
  final IconData icon;
  final Color color;
}

const List<ProjectTemplate> kProjectTemplates = [
  ProjectTemplate(
    id: 'research',
    title: '研究',
    description: '深度研究：假设追踪 + 方法论 + 综述 + 知识图谱',
    icon: Icons.travel_explore_outlined,
    color: NamedPalette.purple,
  ),
  ProjectTemplate(
    id: 'reading',
    title: '阅读',
    description: '读书笔记：人物 / 主题 / 情节线 / 章节记录',
    icon: Icons.menu_book_outlined,
    color: NamedPalette.green,
  ),
  ProjectTemplate(
    id: 'personal',
    title: '个人成长',
    description: '目标 / 习惯 / 反思 / 日记，链接成自我知识网',
    icon: Icons.spa_outlined,
    color: NamedPalette.amber,
  ),
  ProjectTemplate(
    id: 'business',
    title: '商务团队',
    description: '团队知识库：会议纪要 / 决策 / 项目 / 干系人',
    icon: Icons.groups_outlined,
    color: NamedPalette.blue,
  ),
  ProjectTemplate(
    id: 'general',
    title: '通用',
    description: '从空白开始，自由组织内容',
    icon: Icons.note_add_outlined,
    color: WikiPageTypeColors.other, // slate-400
  ),
];

class WelcomeScreen extends StatelessWidget {
  const WelcomeScreen({super.key, required this.onCreate});

  /// 用户选了某个模板后触发；id 取自 [kProjectTemplates]。
  final void Function(ProjectTemplate template) onCreate;

  @override
  Widget build(BuildContext context) {
    final brand = Theme.of(context).colorScheme.primary;
    return SingleChildScrollView(
      padding: const EdgeInsets.fromLTRB(40, 56, 40, 40),
      child: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 720),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.center,
            children: [
              Container(
                width: 56,
                height: 56,
                decoration: BoxDecoration(
                  color: brand.withValues(alpha: 0.12),
                  borderRadius: BorderRadius.circular(14),
                ),
                alignment: Alignment.center,
                child: Icon(
                  Icons.auto_awesome,
                  size: 28,
                  color: brand,
                ),
              ),
              const SizedBox(height: 16),
              Text(
                '欢迎使用 BiuMind 知识库',
                style: TextStyle(
                  color: BiuTokens.text,
                  fontSize: 22,
                  fontWeight: FontWeight.w700,
                  letterSpacing: -0.4,
                ),
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 8),
              Text(
                '一个项目就是一个独立的 Wiki — 挑一个起手模板，'
                '或从空白开始，然后把笔记 / 文档 / 网页粘进来。',
                style: TextStyle(
                  color: BiuTokens.textMuted,
                  fontSize: 13,
                  height: 1.55,
                ),
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 32),
              _TemplateGrid(onTap: onCreate),
            ],
          ),
        ),
      ),
    );
  }
}

class _TemplateGrid extends StatelessWidget {
  const _TemplateGrid({required this.onTap});
  final void Function(ProjectTemplate) onTap;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, c) {
        const minW = 200.0;
        const gap = 14.0;
        final cols = ((c.maxWidth + gap) / (minW + gap)).floor().clamp(2, 3);
        final cardW = (c.maxWidth - gap * (cols - 1)) / cols;
        return Wrap(
          spacing: gap,
          runSpacing: gap,
          children: [
            for (final tpl in kProjectTemplates)
              SizedBox(
                width: cardW,
                child: _TemplateCard(template: tpl, onTap: () => onTap(tpl)),
              ),
          ],
        );
      },
    );
  }
}

class _TemplateCard extends StatefulWidget {
  const _TemplateCard({required this.template, required this.onTap});
  final ProjectTemplate template;
  final VoidCallback onTap;

  @override
  State<_TemplateCard> createState() => _TemplateCardState();
}

class _TemplateCardState extends State<_TemplateCard> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final t = widget.template;
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      child: GestureDetector(
        onTap: widget.onTap,
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 120),
          padding: const EdgeInsets.all(16),
          decoration: BoxDecoration(
            color: BiuTokens.surface,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(
              color: _hover ? t.color : BiuTokens.borderSubtle,
              width: _hover ? 1.5 : 1,
            ),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Container(
                width: 36,
                height: 36,
                decoration: BoxDecoration(
                  color: t.color.withValues(alpha: 0.12),
                  borderRadius: BorderRadius.circular(8),
                ),
                alignment: Alignment.center,
                child: Icon(t.icon, size: 18, color: t.color),
              ),
              const SizedBox(height: 12),
              Text(
                t.title,
                style: TextStyle(
                  color: BiuTokens.text,
                  fontSize: 14,
                  fontWeight: FontWeight.w700,
                ),
              ),
              const SizedBox(height: 4),
              Text(
                t.description,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(
                  color: BiuTokens.textMuted,
                  fontSize: 11,
                  height: 1.5,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
