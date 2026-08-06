// SkillTile — single-row card for the technique-management list.
//
// Visual contract (from the design review):
//   ┌─────────────────────────────────────────────────────────────┐
//   │  ╭──╮                                                       │
//   │  │🛠 │  skill-creator    [内置]            已启用      ⋯    │
//   │  ╰──╯  Guides the user through creating a new skill.        │
//   └─────────────────────────────────────────────────────────────┘
//
// 80 px row · 48 px avatar · single-line description · status text +
// kebab menu on the right. The whole row is tappable; the menu is
// reachable independently so users don't accidentally trigger detail
// navigation when they meant to enable/disable.

import 'package:flutter/material.dart';

import '../../../app/theme.dart';
import '../../../core/ui/biu_hoverable.dart';
import '../../../data/api/skill_client.dart';
import '../../../l10n/app_localizations.dart';

enum _SkillMenuAction {
  detail,
  toggleEnable,
  toggleDisable,
  pinHome,
  approve,
  reject,
  delete,
}

class SkillTile extends StatelessWidget {
  const SkillTile({
    super.key,
    required this.skill,
    required this.onTap,
    required this.onMenuAction,
  });

  final Skill skill;
  final VoidCallback onTap;
  final void Function(SkillTileAction action) onMenuAction;

  @override
  Widget build(BuildContext context) {
    final t = AppLocalizations.of(context)!;
    final theme = Theme.of(context);
    final c = theme.extension<BiuColors>();
    // hover 时给 surface2 半透明覆盖,跟 prototype `.skill-row:hover` 一致。
    final hoverBg = (c?.surface2 ?? theme.colorScheme.surfaceContainer)
        .withValues(alpha: 0.55);
    // hover bg 不做动画 — AnimatedContainer 160ms 在快速划过列表时多 tile
    // 同时淡出会留残影,普通 Container 即时切换更顺滑。selected 状态本组件
    // 不参与(列表行无选中态),所以全部用 Container 即可。
    return BiuHoverable(
      onTap: onTap,
      builder: (ctx, hovered, _) => Container(
        color: hovered ? hoverBg : Colors.transparent,
        padding: const EdgeInsets.symmetric(
          horizontal: BiuTokens.space4,
          vertical: BiuTokens.space3,
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.center,
          children: [
            _SkillAvatar(skill: skill),
            const SizedBox(width: BiuTokens.space3),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Row(
                    children: [
                      Flexible(
                        child: Text(
                          skill.name.isEmpty ? skill.identifier : skill.name,
                          style: TextStyle(
                            fontSize: 15,
                            fontWeight: FontWeight.w600,
                            color: BiuTokens.text,
                          ),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                      const SizedBox(width: BiuTokens.space2),
                      _SourceBadge(source: skill.source, l10n: t),
                    ],
                  ),
                  const SizedBox(height: 4),
                  Text(
                    skill.description.isEmpty ? skill.identifier : skill.description,
                    style: TextStyle(
                      fontSize: 13,
                      color: BiuTokens.textSecondary,
                      height: 1.4,
                    ),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
              ),
            ),
            const SizedBox(width: BiuTokens.space3),
            _StatusLabel(status: skill.status, l10n: t),
            const SizedBox(width: BiuTokens.space2),
            _SkillTileMenu(
              skill: skill,
              onAction: onMenuAction,
            ),
          ],
        ),
      ),
    );
  }
}

/// Public re-export so SkillsPage can switch on menu actions without
/// reaching into the private enum.
class SkillTileAction {
  final _SkillMenuAction _v;
  const SkillTileAction._(this._v);

  static const detail = SkillTileAction._(_SkillMenuAction.detail);
  static const toggleEnable = SkillTileAction._(_SkillMenuAction.toggleEnable);
  static const toggleDisable = SkillTileAction._(_SkillMenuAction.toggleDisable);
  static const pinHome = SkillTileAction._(_SkillMenuAction.pinHome);
  static const approve = SkillTileAction._(_SkillMenuAction.approve);
  static const reject = SkillTileAction._(_SkillMenuAction.reject);
  static const delete = SkillTileAction._(_SkillMenuAction.delete);

  bool get isDetail => _v == _SkillMenuAction.detail;
  bool get isToggleEnable => _v == _SkillMenuAction.toggleEnable;
  bool get isToggleDisable => _v == _SkillMenuAction.toggleDisable;
  bool get isPinHome => _v == _SkillMenuAction.pinHome;
  bool get isApprove => _v == _SkillMenuAction.approve;
  bool get isReject => _v == _SkillMenuAction.reject;
  bool get isDelete => _v == _SkillMenuAction.delete;
}

// ─── Avatar ─────────────────────────────────────────────────

class _SkillAvatar extends StatelessWidget {
  const _SkillAvatar({required this.skill});
  final Skill skill;

  @override
  Widget build(BuildContext context) {
    const size = 48.0;
    final icon = skill.manifest.icon;

    Widget child;
    if (icon.isEmpty) {
      child = _LetterAvatar(seed: skill.identifier);
    } else if (icon.startsWith('http://') || icon.startsWith('https://')) {
      child = ClipRRect(
        borderRadius: BorderRadius.circular(12),
        child: Image.network(
          icon,
          width: size,
          height: size,
          fit: BoxFit.cover,
          errorBuilder: (_, _, _) => _LetterAvatar(seed: skill.identifier),
        ),
      );
    } else {
      // Treat as emoji (or any text glyph). Auto-coloured background.
      child = _EmojiAvatar(seed: skill.identifier, glyph: icon);
    }
    return SizedBox(width: size, height: size, child: child);
  }
}

class _LetterAvatar extends StatelessWidget {
  const _LetterAvatar({required this.seed});
  final String seed;

  @override
  Widget build(BuildContext context) {
    final color = _seedColor(seed);
    final letter = seed.isEmpty ? '?' : seed[0].toUpperCase();
    return Container(
      decoration: BoxDecoration(
        color: color,
        borderRadius: BorderRadius.circular(12),
      ),
      alignment: Alignment.center,
      child: Text(
        letter,
        style: const TextStyle(
          fontSize: 20,
          fontWeight: FontWeight.w600,
          color: Colors.white,
        ),
      ),
    );
  }
}

class _EmojiAvatar extends StatelessWidget {
  const _EmojiAvatar({required this.seed, required this.glyph});
  final String seed;
  final String glyph;

  @override
  Widget build(BuildContext context) {
    final color = _seedColor(seed).withValues(alpha: 0.16);
    return Container(
      decoration: BoxDecoration(
        color: color,
        borderRadius: BorderRadius.circular(12),
      ),
      alignment: Alignment.center,
      child: Text(
        glyph,
        style: const TextStyle(fontSize: 24),
      ),
    );
  }
}

/// Deterministic color from the identifier — 走 CategoryPalette 8 色循环。
/// (skill 之间需要视觉区分,跟用户主题色板无关 → 用 CategoryColors namespace)
Color _seedColor(String seed) => CategoryPalette.colorFor(seed);

// ─── Source badge ───────────────────────────────────────────

class _SourceBadge extends StatelessWidget {
  const _SourceBadge({required this.source, required this.l10n});
  final String source;
  final AppLocalizations l10n;

  @override
  Widget build(BuildContext context) {
    final (label, bg, fg) = _sourceStyle(source, l10n);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: bg,
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        label,
        style: TextStyle(fontSize: 11, fontWeight: FontWeight.w500, color: fg),
      ),
    );
  }
}

(String, Color, Color) _sourceStyle(String source, AppLocalizations l10n) =>
    switch (source) {
      'bundled' => (l10n.skillsFilterBundled, SkillSourceBadge.bundledBg, SkillSourceBadge.bundledFg),
      'org' => (l10n.skillsFilterOrg, SkillSourceBadge.orgBg, SkillSourceBadge.orgFg),
      'marketplace' => (l10n.skillsFilterMarketplace, SkillSourceBadge.marketplaceBg, SkillSourceBadge.marketplaceFg),
      'imported' => ('已导入', SkillSourceBadge.importedBg, SkillSourceBadge.importedFg),
      _ => (l10n.skillsFilterMy, SkillSourceBadge.userBg, SkillSourceBadge.userFg),
    };

// ─── Status label ───────────────────────────────────────────

class _StatusLabel extends StatelessWidget {
  const _StatusLabel({required this.status, required this.l10n});
  final String status;
  final AppLocalizations l10n;

  @override
  Widget build(BuildContext context) {
    final (label, color) = _statusStyle(status, l10n);
    return Text(
      label,
      style: TextStyle(
        fontSize: 13,
        fontWeight: FontWeight.w500,
        color: color,
      ),
    );
  }
}

(String, Color) _statusStyle(String status, AppLocalizations l10n) =>
    switch (status) {
      'active' => ('已启用', NamedPaletteStrong.emerald),
      'staged' || 'staged_org' => ('待审核', NamedPaletteStrong.amber),
      'disabled' => ('已停用', BiuTokens.textMuted),
      'suspended' => ('已暂停', NamedPaletteStrong.red),
      _ => (status, BiuTokens.textMuted),
    };

// ─── Kebab menu ─────────────────────────────────────────────

class _SkillTileMenu extends StatelessWidget {
  const _SkillTileMenu({required this.skill, required this.onAction});
  final Skill skill;
  final void Function(SkillTileAction) onAction;

  @override
  Widget build(BuildContext context) {
    return PopupMenuButton<SkillTileAction>(
      icon: const Icon(Icons.more_horiz, size: 20),
      tooltip: '更多',
      onSelected: onAction,
      itemBuilder: (_) => _itemsFor(skill),
    );
  }

  List<PopupMenuEntry<SkillTileAction>> _itemsFor(Skill s) {
    final items = <PopupMenuEntry<SkillTileAction>>[
      const PopupMenuItem(
        value: SkillTileAction.detail,
        child: ListTile(
          leading: Icon(Icons.info_outline, size: 18),
          title: Text('查看详情'),
          contentPadding: EdgeInsets.zero,
          dense: true,
        ),
      ),
    ];
    final isStaged = s.status == 'staged' || s.status == 'staged_org';
    final isBundled = s.source == 'bundled';

    if (isStaged) {
      items.addAll([
        PopupMenuItem(
          value: SkillTileAction.approve,
          child: ListTile(
            leading: Icon(Icons.check, size: 18, color: NamedPaletteStrong.emerald),
            title: const Text('批准'),
            contentPadding: EdgeInsets.zero,
            dense: true,
          ),
        ),
        PopupMenuItem(
          value: SkillTileAction.reject,
          child: ListTile(
            leading: Icon(Icons.close, size: 18, color: NamedPaletteStrong.red),
            title: const Text('驳回'),
            contentPadding: EdgeInsets.zero,
            dense: true,
          ),
        ),
      ]);
    } else if (s.status == 'active') {
      items.add(const PopupMenuItem(
        value: SkillTileAction.pinHome,
        child: ListTile(
          leading: Icon(Icons.push_pin_outlined, size: 18),
          title: Text('置顶到默认助手'),
          contentPadding: EdgeInsets.zero,
          dense: true,
        ),
      ));
      items.add(const PopupMenuItem(
        value: SkillTileAction.toggleDisable,
        child: ListTile(
          leading: Icon(Icons.pause_circle_outline, size: 18),
          title: Text('停用'),
          contentPadding: EdgeInsets.zero,
          dense: true,
        ),
      ));
    } else if (s.status == 'disabled') {
      items.add(const PopupMenuItem(
        value: SkillTileAction.toggleEnable,
        child: ListTile(
          leading: Icon(Icons.play_circle_outline, size: 18),
          title: Text('启用'),
          contentPadding: EdgeInsets.zero,
          dense: true,
        ),
      ));
    }

    if (!isBundled) {
      items.add(const PopupMenuDivider());
      items.add(PopupMenuItem(
        value: SkillTileAction.delete,
        child: ListTile(
          leading: Icon(Icons.delete_outline, size: 18, color: NamedPaletteStrong.red),
          title: Text('删除', style: TextStyle(color: NamedPaletteStrong.red)),
          contentPadding: EdgeInsets.zero,
          dense: true,
        ),
      ));
    }
    return items;
  }
}
