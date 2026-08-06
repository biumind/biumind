// HeroViewV2 —— ThreadsShellPage 在没选 thread 时显示的欢迎页。
// 设计文档 docs/BiuMind-Chat-UI-Benchmark-Optimization.md P1-15。
//
// 三段：
//   1. 时段问候 + 副标题
//   2. 6 张起点 prompt 卡片（点击 → 创建新 thread + 把 prompt 塞 composer）
//   3. 最近 5 个会话快跳（点击 → 选中那个 thread）

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme/effects.dart';
import '../../../../app/theme/extensions.dart' show BiuColors;
import '../../../../app/theme/palettes.dart';
import '../../../../app/theme/tokens.dart' show ShadowTokens;
import '../../../../core/ui/biu_card.dart';
import '../../../../core/ui/biu_chip.dart';
import '../../../../core/ui/biu_gradient_text.dart';
import '../../../../core/ui/biu_section_label.dart';
import '../../../../data/api/skill_client.dart' show Skill;
import '../../../../data/skill_providers.dart';
import '../../../../features/creation/application/credits_controller.dart';
import '../../../../features/settings/application/settings_controller.dart';
import '../../../../l10n/app_localizations.dart';
import '../../application/chat_controller.dart';
import '../../application/chat_preferences.dart';
import '../../application/draft_history_controller.dart';
import '../../domain/chat_models.dart';
import '../../domain/greeting.dart';
import 'changelog_banner.dart';
import 'shortcut_hint.dart';

class HeroViewV2 extends ConsumerWidget {
  const HeroViewV2({
    super.key,
    required this.userName,
    required this.onNewWithPrompt,
    required this.onPickRecent,
    required this.onNew,
    required this.recentThreads,
  });

  /// 时段问候里附加的称呼；null = 不带名字。
  final String? userName;
  /// 点击起点卡 → 父级负责：创建 thread + select + 把 prompt 注入 composer
  final void Function(String prompt) onNewWithPrompt;
  /// 点击最近会话快跳卡 → 父级 select。
  final void Function(String threadId) onPickRecent;
  /// 空白处的"新建对话"主按钮 → 父级走 NewThreadDialog。
  final VoidCallback onNew;
  /// 渲染列表的最近会话；最多 5 条，由父级裁剪。
  final List<Thread> recentThreads;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final hour = DateTime.now().hour;
    final greeting = greetingForHour(hour, userName: userName);

    // Hero 改为全铺开布局,跟 prototype `.main { padding: gap-section 28px 60px }`
     // 一致:主区域 100% 宽,左右各 28px 边距(舒适型 32px),不再用 720 maxWidth
     // 把中间收死。窄屏 (<1100px) 时收到 24px 跟 sidebar 收缩态对齐。
     // 上限 1280 防止超宽屏 (3K/4K) 行长过宽阅读疲劳。
    return SingleChildScrollView(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(28, 32, 28, 60),
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 1280),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const ChangelogBannerV2(),
              const HeroShortcutHintV2(),
              // prototype `.hero { display: flex; gap: 14px } .hero-stats {
              //   margin-left: auto }` — 头像 + greeting + stats 三块同一 Row,
              //   **stats 贴右**。greeting 用 Expanded 占满头像与 stats 之间的
              //   剩余空间(等价 CSS `flex: 1`),把 stats Wrap 推到最右,跟
              //   prototype `margin-left: auto` 行为一致。
              //   早期用 Flexible(loose) 让 greeting 收缩到内容宽,stats 紧跟其后
              //   没贴右(用户截图反馈)。
              Row(
                crossAxisAlignment: CrossAxisAlignment.center,
                children: [
                  _BrandLogo(),
                  const SizedBox(width: 14),
                  Expanded(
                    child: Consumer(
                      // greeting 文字用当前色板的 brand gradient 做
                      // background-clip:text 等价(ShaderMask),跟 prototype
                      // `.hero h1 { background: hero-grad; -webkit-background-clip: text }`
                      // 完全一致。色板切换实时跟随。
                      builder: (_, ref, _) {
                        final palette = ref
                                .watch(settingsControllerProvider)
                                .valueOrNull
                                ?.palette ??
                            PaletteId.inkblueOrange;
                        // prototype `.hero h1 { font-hero: 30px;
                        //   font-weight: 700; letter-spacing: -0.022em }`。
                        return BiuGradientText(
                          greeting,
                          palette: palette,
                          style: const TextStyle(
                            fontSize: 30,
                            fontWeight: FontWeight.w700,
                            letterSpacing: -0.66,
                            height: 1.15,
                          ),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        );
                      },
                    ),
                  ),
                  const SizedBox(width: 12),
                  // hero-stats:flex-wrap + ml:auto。Wrap 让窄屏自动堆叠,
                  // 宽屏单行右贴 — 对应 prototype `flex-wrap: wrap`。
                  Wrap(
                    spacing: 8,
                    runSpacing: 6,
                    alignment: WrapAlignment.end,
                    crossAxisAlignment: WrapCrossAlignment.center,
                    children: [
                      Consumer(
                        builder: (_, ref, _) {
                          final stats = ref.watch(threadStatsProvider);
                          final v = stats.valueOrNull;
                          if (v == null || v.threadCount == 0) {
                            return const SizedBox.shrink();
                          }
                          // prototype `.stat { surf-2 + text-2 + padding 6×12;
                          //   font-sm 600; 无 border }`
                          return BiuChip(
                            disableBorder: true,
                            padding: const EdgeInsets.symmetric(
                                horizontal: 12, vertical: 6),
                            foregroundColor:
                                theme.extension<BiuColors>()?.text2,
                            label: Text(
                              l.chatV2HeroStatsThreads(v.messageCount, v.threadCount),
                              style: theme.textTheme.labelSmall?.copyWith(
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                          );
                        },
                      ),
                      const _RecentStatsChip(),
                      Consumer(
                        builder: (_, ref, _) {
                          final streak =
                              ref.watch(dailyStreakProvider).valueOrNull;
                          if (streak == null || streak < 2) {
                            return const SizedBox.shrink();
                          }
                          final c = theme.extension<BiuColors>()!;
                          // prototype `.stat.flame` — sem-warn 12% bg + 文字
                          return Tooltip(
                            message: l.chatV2HeroStreakTooltip(streak),
                            child: BiuChip(
                              padding: const EdgeInsets.symmetric(
                                  horizontal: 12, vertical: 6),
                              backgroundColor:
                                  c.accent.withValues(alpha: 0.12),
                              foregroundColor: c.accentHover,
                              label: Row(
                                mainAxisSize: MainAxisSize.min,
                                children: [
                                  const Text('🔥',
                                      style: TextStyle(fontSize: 12)),
                                  const SizedBox(width: 4),
                                  Text(
                                    l.chatV2HeroStreakChip(streak),
                                    style: theme.textTheme.labelSmall
                                        ?.copyWith(
                                      color: c.accentHover,
                                      fontWeight: FontWeight.w600,
                                    ),
                                  ),
                                ],
                              ),
                            ),
                          );
                        },
                      ),
                    ],
                  ),
                ],
              ),
              const SizedBox(height: 6),
              // prototype `.subhead { margin: 6px 0 22px; font-base; text-3 }`
              // — 一段话内联 link `<a>+ 新建空白对话 →</a>`,brand 色 inline,
              // 不带 button bg / padding。RichText + WidgetSpan 让 link 跟正文
              // 自然换行,跟 prototype CSS `<a>` 行内表现一致。
              _HeroSubhead(onNew: onNew),
              const SizedBox(height: 22),
              _StarterGrid(onTap: (p) {
                // 让 composer 知道有引用进来；onNewWithPrompt 让父级建 thread
                // + select；composer 的 listen 会 consume 这条 inject。
                ref
                    .read(composerInjectProvider.notifier)
                    .inject(p.prompt);
                onNewWithPrompt(p.prompt);
              }),
              const _SkillShelf(),
              const _RecentModelsShelf(),
              if (recentThreads.isNotEmpty) ...[
                BiuSectionLabel(l.chatV2HeroRecentLabel),
                _RecentList(threads: recentThreads, onPick: onPickRecent),
              ],
              // prototype `.section-h "本月数据" + .kv 3 列`,放最末。
              BiuSectionLabel(l.chatV2HeroKvLabel),
              const _HeroKvGrid(),
            ],
          ),
        ),
      ),
    );
  }
}

/// 副标题段落 + inline brand-color 链接 "+ 新建空白对话 →"。
/// prototype `<p class="subhead">… <a>+ 新建空白对话 →</a></p>` 等价实现。
class _HeroSubhead extends StatefulWidget {
  const _HeroSubhead({required this.onNew});
  final VoidCallback onNew;

  @override
  State<_HeroSubhead> createState() => _HeroSubheadState();
}

class _HeroSubheadState extends State<_HeroSubhead> {
  late final TapGestureRecognizer _tap;

  @override
  void initState() {
    super.initState();
    _tap = TapGestureRecognizer()..onTap = widget.onNew;
  }

  @override
  void dispose() {
    _tap.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final cs = theme.colorScheme;
    final base = theme.textTheme.bodyMedium?.copyWith(
          color: theme.extension<BiuColors>()?.text3 ??
              theme.colorScheme.onSurfaceVariant,
          height: 1.5,
        ) ??
        TextStyle(color: cs.onSurfaceVariant);
    return RichText(
      text: TextSpan(
        style: base,
        children: [
          TextSpan(text: l.chatV2HeroSubtitle),
          TextSpan(
            text: '+ ${l.chatV2HeroNewBlank} →',
            style: base.copyWith(
              color: cs.primary,
              fontWeight: FontWeight.w600,
            ),
            recognizer: _tap,
            mouseCursor: SystemMouseCursors.click,
          ),
        ],
      ),
    );
  }
}

/// "本月数据" 三宫格 KV — 本月对话 / 余下积分 / 连击天数。
/// prototype `.kv { grid 3 cols; gap 12 } .item { surf-1 + hairline + radius-md
///   + pad-card } .label { 11px upper letter-spacing 0.06em } .v { 18px 700 tabular }`
class _HeroKvGrid extends ConsumerWidget {
  const _HeroKvGrid();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l = AppLocalizations.of(context)!;
    final monthMessages =
        ref.watch(recentStatsProvider(30)).valueOrNull?.messages;
    final credits =
        ref.watch(creditsBalanceProvider).valueOrNull?.totalCredits;
    final streak = ref.watch(dailyStreakProvider).valueOrNull;
    return LayoutBuilder(
      builder: (ctx, c) {
        const gap = 12.0;
        final width = (c.maxWidth - gap * 2) / 3;
        return Wrap(
          spacing: gap,
          runSpacing: gap,
          children: [
            SizedBox(
              width: width,
              child: _HeroKvItem(
                label: l.chatV2HeroKvMonthMessages,
                value: monthMessages == null ? '—' : _formatNumber(monthMessages),
              ),
            ),
            SizedBox(
              width: width,
              child: _HeroKvItem(
                label: l.chatV2HeroKvCredits,
                value: credits == null ? '—' : _formatNumber(credits),
              ),
            ),
            SizedBox(
              width: width,
              child: _HeroKvItem(
                label: l.chatV2HeroKvStreak,
                value: streak == null
                    ? '—'
                    : (streak >= 2 ? '$streak 🔥' : '$streak'),
              ),
            ),
          ],
        );
      },
    );
  }

  /// 千分位分隔(99982 → 99,982),跟 prototype 显示一致。
  static String _formatNumber(int n) {
    final s = n.toString();
    final buf = StringBuffer();
    for (var i = 0; i < s.length; i++) {
      if (i > 0 && (s.length - i) % 3 == 0) buf.write(',');
      buf.write(s[i]);
    }
    return buf.toString();
  }
}

class _HeroKvItem extends StatelessWidget {
  const _HeroKvItem({required this.label, required this.value});
  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = theme.extension<BiuColors>();
    final cs = theme.colorScheme;
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: c?.surface1 ?? cs.surfaceContainerLow,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(
          color: c?.borderHairline ?? cs.outlineVariant,
          width: 1,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            label,
            style: TextStyle(
              fontSize: 11,
              fontWeight: FontWeight.w500,
              color: c?.textMuted ?? cs.onSurfaceVariant,
              letterSpacing: 0.66, // 0.06em × 11
            ),
          ),
          const SizedBox(height: 2),
          Text(
            value,
            style: TextStyle(
              fontSize: 18,
              fontWeight: FontWeight.w700,
              color: c?.text1 ?? cs.onSurface,
              fontFeatures: const [FontFeature.tabularFigures()],
            ),
          ),
        ],
      ),
    );
  }
}

class _BrandLogo extends ConsumerWidget {
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final brightness = Theme.of(context).brightness;
    // 从 settings 拿当前色板,Hero radial+linear 双层叠 — 跟 prototype 一致
    final palette = ref.watch(settingsControllerProvider).valueOrNull?.palette
        ?? PaletteId.inkblueOrange;
    final spec = paletteSpecOf(palette);
    // prototype `.hero .avatar { width 44; radius 14; box-shadow: shadow-md;
    //   font-weight: 800; font-size: avatar*0.42 = 18.5 }`。Flutter avatar
    //   原 40px / radius 10 / weight 700 偏小,这里向 prototype 默认密度对齐。
    const size = 44.0;
    final radius = BorderRadius.circular(14);
    final shadows = ShadowTokens.forBrightness(brightness).md;
    final base = heroDecoration(spec, brightness, borderRadius: radius);
    return Container(
      width: size,
      height: size,
      decoration: base.copyWith(boxShadow: shadows),
      // foregroundDecoration 在主 gradient 之上叠 radial overlay (顶角 brand 80% 漫射)
      foregroundDecoration: BoxDecoration(
        gradient: heroRadialOverlay(spec, brightness),
        borderRadius: radius,
      ),
      alignment: Alignment.center,
      child: const Text(
        'B',
        style: TextStyle(
          color: Colors.white,
          fontWeight: FontWeight.w800,
          fontSize: 18.5, // prototype avatar*0.42 = 18.5
          letterSpacing: -0.4,
          shadows: bannerTextShadow,
        ),
      ),
    );
  }
}

class _StarterGrid extends StatelessWidget {
  const _StarterGrid({required this.onTap});
  final ValueChanged<StarterPrompt> onTap;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (ctx, c) {
        // prototype `.qa-grid { display: grid; grid-template-columns: repeat(3, 1fr);
        //   gap: var(--gap-grid) }` + `@media (max-width: 1280px) { 2 cols }`
        //   + `@media (max-width: 700px) { 1 col }`。
        //
        // 旧实现: LayoutBuilder + Wrap + SizedBox(width: (maxW - gap*(cols-1))/cols)
        // 自己手算列宽,问题:
        //   1. 浮点除不尽(1280-28)/3 = 417.333...,渲染像素累积让 cards 整体
        //      左偏 ~5px → 跟上下其他元素左对齐失败(用户截图反馈)。
        //   2. Wrap 默认 cross.start,同行卡片**底部不等高** — prototype CSS
        //      Grid 同 row cells 自动等高,Wrap 没有这行为。
        // 修:换 GridView.count,Flutter 内部走整数像素均分(SliverGridDelegate
        // WithFixedCrossAxisCount)+ childAspectRatio 强制同行等高,跟 prototype
        // CSS Grid 行为 1:1。
        final cols = c.maxWidth >= 880 ? 3 : (c.maxWidth >= 480 ? 2 : 1);
        const gap = 14.0; // prototype --gap-grid (default density)
        // childAspectRatio = cardWidth / cardHeight。3 列时 ~417/150 ≈ 2.78,
        // 2 列时 ~620/170 ≈ 3.65,1 列时 ~1252/120 ≈ 10。这里取折中值让两行
        // body(2 行文字)能完整放下不溢出。
        final aspectRatio = cols == 3 ? 2.6 : (cols == 2 ? 3.4 : 8.5);
        return GridView.count(
          crossAxisCount: cols,
          crossAxisSpacing: gap,
          mainAxisSpacing: gap,
          childAspectRatio: aspectRatio,
          shrinkWrap: true,
          physics: const NeverScrollableScrollPhysics(),
          padding: EdgeInsets.zero,
          children: [
            for (final p in kStarterPrompts)
              _StarterCard(prompt: p, onTap: () => onTap(p)),
          ],
        );
      },
    );
  }
}

class _StarterCard extends StatelessWidget {
  const _StarterCard({required this.prompt, required this.onTap});
  final StarterPrompt prompt;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    // prototype `.qa` 是垂直布局(icon top → title → body),不是横向 Row。
    //   .qa .ico-wrap { margin-bottom: 12px }  // icon 在上,title 在下
    //   .qa h3        { margin: 0 0 4px }
    //   .qa p         { line-height: var(--line-body) }
    // BiuCard lift:3 + padding:16 跟 prototype `.qa { padding: var(--pad-card) }` 对齐
    // (default density --pad-card = 16px)。
    return BiuCard(
      onTap: onTap,
      lift: 3,
      padding: const EdgeInsets.all(16),
      borderRadius: BorderRadius.circular(14),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Container(
            width: 36,
            height: 36,
            decoration: BoxDecoration(
              color: theme.colorScheme.primaryContainer,
              // prototype `.qa .ico-wrap { border-radius: var(--radius-md)=10 }`
              borderRadius: BorderRadius.circular(10),
            ),
            alignment: Alignment.center,
            child: Icon(prompt.icon,
                size: 18, color: theme.colorScheme.primary),
          ),
          const SizedBox(height: 12),
          Text(
            prompt.title,
            // prototype `.qa h3 { font-card-title: 14.5px; weight 600 }`
            style: theme.textTheme.bodyMedium?.copyWith(
              fontSize: 14.5,
              fontWeight: FontWeight.w600,
              color: theme.extension<BiuColors>()?.text1,
            ),
          ),
          const SizedBox(height: 4),
          Text(
            prompt.prompt,
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
            style: theme.textTheme.bodySmall?.copyWith(
              color: theme.colorScheme.onSurfaceVariant,
              height: 1.55, // prototype --line-body
            ),
          ),
        ],
      ),
    );
  }
}

class _RecentList extends StatelessWidget {
  const _RecentList({required this.threads, required this.onPick});
  final List<Thread> threads;
  final ValueChanged<String> onPick;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    // prototype `.recent-row { border-bottom: 1px solid border-hairline;
    //   padding: 10px 8px; gap: 12px; color: text-2 } :hover { color: text-1 }`
    //   — 密集列表用 hairline 细分隔线区分,**默认文字 text-2**(比主文区淡
    //   一档,克制不抢眼),hover 时升到 text-1。早期实现用 bodyMedium 默认
    //   onSurface (text-1),hover 没切色,跟 prototype 不一致。
    final c = theme.extension<BiuColors>();
    final cs = theme.colorScheme;
    final hairline = c?.borderHairline ?? cs.outlineVariant;
    final text1 = c?.text1 ?? cs.onSurface;
    final text2 = c?.text2 ?? cs.onSurfaceVariant;
    final textMuted = c?.textMuted ?? cs.onSurfaceVariant;
    return Column(
      children: [
        for (final t in threads)
          _RecentRow(
            title: t.title.isEmpty ? l.chatV2NewThreadFallback : t.title,
            time: relativeTime(t.updatedAt),
            text1: text1,
            text2: text2,
            textMuted: textMuted,
            hoverBg: c?.surface1 ?? cs.surfaceContainerLow,
            hairline: hairline,
            onTap: () => onPick(t.id),
          ),
      ],
    );
  }
}

/// Recent 单行 — hover 时字色从 text-2 升到 text-1,bg 浅染 surface-1。
/// 跟 prototype `.recent-row { color: text-2 } :hover { color: text-1 }` 一致。
class _RecentRow extends StatefulWidget {
  const _RecentRow({
    required this.title,
    required this.time,
    required this.text1,
    required this.text2,
    required this.textMuted,
    required this.hoverBg,
    required this.hairline,
    required this.onTap,
  });
  final String title;
  final String time;
  final Color text1;
  final Color text2;
  final Color textMuted;
  final Color hoverBg;
  final Color hairline;
  final VoidCallback onTap;

  @override
  State<_RecentRow> createState() => _RecentRowState();
}

class _RecentRowState extends State<_RecentRow> {
  bool _hover = false;

  @override
  Widget build(BuildContext context) {
    final fg = _hover ? widget.text1 : widget.text2;
    return MouseRegion(
      cursor: SystemMouseCursors.click,
      onEnter: (_) => setState(() => _hover = true),
      onExit: (_) => setState(() => _hover = false),
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: widget.onTap,
        // hover bg 即时切换避免残影 — 跟 _NavRow / thread tile 同款修复
        child: Container(
          decoration: BoxDecoration(
            color: _hover ? widget.hoverBg : null,
            borderRadius: BorderRadius.circular(6),
            border: Border(
              bottom: BorderSide(color: widget.hairline),
            ),
          ),
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 10),
          child: Row(
            children: [
              Icon(
                Icons.chat_bubble_outline,
                size: 14,
                color: widget.textMuted,
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Text(
                  widget.title,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(fontSize: 13.5, color: fg),
                ),
              ),
              const SizedBox(width: 8),
              Text(
                widget.time,
                style: TextStyle(
                  fontSize: 11,
                  color: widget.textMuted,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// 技能货架 —— Hero 上"起点 prompt"下方的横向 chip 行。
/// 取已安装 skills 前 6 个，点击 → composer 注入 `@<identifier> ` 让用户
/// 直接接着输入提示词；空列表时整段不渲染。
class _SkillShelf extends ConsumerWidget {
  const _SkillShelf();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l = AppLocalizations.of(context)!;
    final skillsAsync = ref.watch(skillsListProvider);
    final skills = skillsAsync.valueOrNull ?? const <Skill>[];
    if (skills.isEmpty) return const SizedBox.shrink();
    final shown = skills.take(6).toList(growable: false);
    return Padding(
      padding: const EdgeInsets.only(top: 24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          BiuSectionLabel(l.chatV2HeroSkillsLabel),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final s in shown)
                _SkillChip(
                  skill: s,
                  onTap: () => ref
                      .read(composerInjectProvider.notifier)
                      .inject('@${s.identifier} '),
                ),
            ],
          ),
        ],
      ),
    );
  }
}

class _SkillChip extends StatelessWidget {
  const _SkillChip({required this.skill, required this.onTap});
  final Skill skill;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    // 技能 chip 用 brand soft 高亮态 — prototype `.chip.brand`,
    // brand-soft bg + brand 文字 + 无边框,跟模型 chip(filled active)区分。
    // @ 符号比 skill name 弱(prototype `.chip .at { color: text-muted }`)。
    final theme = Theme.of(context);
    return BiuChip(
      onTap: onTap,
      brand: true,
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      label: Text.rich(
        TextSpan(
          children: [
            TextSpan(
              text: '@',
              style: TextStyle(
                color: theme.colorScheme.onSurfaceVariant.withValues(alpha: 0.6),
                fontFamily: 'monospace',
              ),
            ),
            TextSpan(
              text: skill.identifier,
              style: const TextStyle(
                fontFamily: 'monospace',
                fontWeight: FontWeight.w500,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// 周报 chip —— 点击在 7d ↔ 30d 之间切换。
/// 状态本地（不持久化跨会话）；用户每次进 Hero 都从默认 7d 开始。
class _RecentStatsChip extends ConsumerStatefulWidget {
  const _RecentStatsChip();
  @override
  ConsumerState<_RecentStatsChip> createState() => _RecentStatsChipState();
}

class _RecentStatsChipState extends ConsumerState<_RecentStatsChip> {
  int _days = 7;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l = AppLocalizations.of(context)!;
    final stats = ref.watch(recentStatsProvider(_days));
    final v = stats.valueOrNull;
    if (v == null || v.messages == 0) return const SizedBox.shrink();
    final label = v.days == 7
        ? l.chatV2HeroStatsThisWeek(v.messages, v.activeThreads)
        : l.chatV2HeroStatsRecentDays(v.days, v.messages, v.activeThreads);
    // prototype `.stat.brand` — brand-soft 浅色背景 + brand 文字 + 无边框,
    // 视觉比当前的 0.4 alpha 实色填充弱很多,跟 prototype 完全对齐。
    return Tooltip(
      message: l.chatV2HeroStatsSwitchTooltip(v.days, v.messages, v.activeThreads),
      child: BiuChip(
        onTap: () => setState(() => _days = _days == 7 ? 30 : 7),
        brand: true,
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
        label: Text(
          label,
          style: theme.textTheme.labelSmall?.copyWith(
            fontWeight: FontWeight.w600,
          ),
        ),
        trailing: const Icon(Icons.swap_horiz, size: 11),
      ),
    );
  }
}

/// Hero "最近用过的模型"行 —— 点击 chip 设为全局默认模型。
/// 数据来源：ChatRepo.recentModels (DISTINCT model GROUP BY MAX createdAt)。
class _RecentModelsShelf extends ConsumerWidget {
  const _RecentModelsShelf();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final l = AppLocalizations.of(context)!;
    final async = ref.watch(recentModelsProvider);
    final list = async.valueOrNull ?? const [];
    if (list.isEmpty) return const SizedBox.shrink();
    final defaultModel = ref.watch(
      chatPreferencesProvider.select((p) => p.defaultModel),
    );
    return Padding(
      padding: const EdgeInsets.only(top: 24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          BiuSectionLabel(l.chatV2HeroRecentModelsLabel),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final m in list)
                _RecentModelChip(
                  code: m.code,
                  active: m.code == defaultModel,
                  onTap: () async {
                    final messenger = ScaffoldMessenger.of(context);
                    await ref
                        .read(chatPreferencesProvider.notifier)
                        .setDefaultModel(m.code);
                    messenger.showSnackBar(SnackBar(
                      content: Text(
                        l.chatV2HeroSetDefaultModel(m.code),
                      ),
                      duration: const Duration(seconds: 2),
                    ));
                  },
                ),
            ],
          ),
        ],
      ),
    );
  }
}

class _RecentModelChip extends StatelessWidget {
  const _RecentModelChip({
    required this.code,
    required this.active,
    required this.onTap,
  });
  final String code;
  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    // 当前激活的模型用 BiuChip(active:true) — brand 实色 + 白字 filled,
    // 其余模型用默认 surface-2 chip,跟 prototype `.chip.active` / `.chip` 一致。
    return BiuChip(
      onTap: onTap,
      active: active,
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      leading: Icon(
        active ? Icons.check : Icons.psychology_outlined,
        size: 12,
      ),
      label: Text(
        code,
        style: const TextStyle(
          fontFamily: 'monospace',
          fontWeight: FontWeight.w500,
        ),
      ),
    );
  }
}
