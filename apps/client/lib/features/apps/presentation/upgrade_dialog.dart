// UpgradeDialog — permission-diff modal for App upgrades (M15).
//
// Shows three sections (only the ones with content):
//   + 新增权限    red, requires individual checkbox accept
//   - 移除权限    grey, info-only
//   = 已授予      collapsed by default; expandable for context
//
// Returns:
//   null   user cancelled / closed
//   list   accepted_new_permissions (every "added" perm must be
//          checked; "升级" button stays disabled until they all are)
//
// "暂不升级" cancels; pinning the version isn't a Modal action — it
// lives on the install detail page. The Modal stays focused on the
// "should I take this version" decision.

import 'package:flutter/material.dart';

import '../../../app/theme.dart';
import '../../../data/api/apps_client.dart';
import '../../../l10n/app_localizations.dart';

class UpgradeDialog {
  /// Returns the list of accepted new permissions, or null on cancel.
  /// `appName` is the display name shown in the title; the rest
  /// comes from the server's UpgradeStatus.
  static Future<List<String>?> show(
    BuildContext context, {
    required String appName,
    required UpgradeStatus status,
  }) {
    return showDialog<List<String>>(
      context: context,
      builder: (_) => _Body(appName: appName, status: status),
    );
  }
}

class _Body extends StatefulWidget {
  const _Body({required this.appName, required this.status});
  final String appName;
  final UpgradeStatus status;

  @override
  State<_Body> createState() => _BodyState();
}

class _BodyState extends State<_Body> {
  late Set<String> _accepted;
  bool _showUnchanged = false;

  @override
  void initState() {
    super.initState();
    _accepted = {};
  }

  bool get _allAdditionsAccepted {
    for (final p in widget.status.permsDiff.added) {
      if (!_accepted.contains(p)) return false;
    }
    return true;
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final theme = Theme.of(context);
    final scheme = theme.colorScheme;
    final diff = widget.status.permsDiff;

    return AlertDialog(
      title: Text(l10n.upgradeTitle(
        widget.appName,
        widget.status.currentVersion,
        widget.status.targetVersion,
      )),
      content: SizedBox(
        width: 520,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              if (!diff.isBreaking)
                Text(l10n.upgradeNoNewPerms,
                    style: theme.textTheme.bodyMedium),
              if (diff.isBreaking) ...[
                Text(l10n.upgradeNeedsApproval,
                    style: theme.textTheme.bodyMedium),
                const SizedBox(height: BiuTokens.space3),
                _SectionHeader(
                  icon: Icons.add_circle_outline,
                  color: scheme.error,
                  label: l10n.upgradeSectionAdded,
                  count: diff.added.length,
                ),
                ...diff.added.map((p) => CheckboxListTile(
                      contentPadding: EdgeInsets.zero,
                      controlAffinity: ListTileControlAffinity.leading,
                      dense: true,
                      value: _accepted.contains(p),
                      title: Text(p,
                          style: TextStyle(
                              fontFamily: 'monospace',
                              color: scheme.error,
                              fontWeight: FontWeight.w500)),
                      subtitle: Text(_describePermission(l10n, p)),
                      onChanged: (v) => setState(() {
                        if (v == true) {
                          _accepted.add(p);
                        } else {
                          _accepted.remove(p);
                        }
                      }),
                    )),
              ],
              if (diff.removed.isNotEmpty) ...[
                const SizedBox(height: BiuTokens.space3),
                _SectionHeader(
                  icon: Icons.remove_circle_outline,
                  color: scheme.onSurfaceVariant,
                  label: l10n.upgradeSectionRemoved,
                  count: diff.removed.length,
                ),
                ...diff.removed.map((p) => Padding(
                      padding: const EdgeInsets.symmetric(vertical: 2),
                      child: Text('· $p',
                          style: theme.textTheme.bodySmall?.copyWith(
                              fontFamily: 'monospace',
                              color: scheme.onSurfaceVariant,
                              decoration: TextDecoration.lineThrough)),
                    )),
              ],
              if (diff.unchanged.isNotEmpty) ...[
                const SizedBox(height: BiuTokens.space3),
                InkWell(
                  onTap: () => setState(() => _showUnchanged = !_showUnchanged),
                  child: _SectionHeader(
                    icon: _showUnchanged
                        ? Icons.expand_less
                        : Icons.expand_more,
                    color: scheme.onSurfaceVariant,
                    label: l10n.upgradeSectionUnchanged,
                    count: diff.unchanged.length,
                  ),
                ),
                if (_showUnchanged)
                  ...diff.unchanged.map((p) => Padding(
                        padding: const EdgeInsets.symmetric(vertical: 2),
                        child: Text('· $p',
                            style: theme.textTheme.bodySmall?.copyWith(
                                fontFamily: 'monospace',
                                color: scheme.onSurfaceVariant)),
                      )),
              ],
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: Text(l10n.upgradeCancel),
        ),
        FilledButton(
          onPressed: _allAdditionsAccepted
              ? () => Navigator.of(context).pop(_accepted.toList()..sort())
              : null,
          child: Text(l10n.upgradeApply),
        ),
      ],
    );
  }

  static String _describePermission(AppLocalizations l, String p) {
    final scope = p.split(':').first;
    switch (scope) {
      case 'net.outbound':       return l.permNetOutbound;
      case 'hub.invoke':         return l.permHubInvoke;
      case 'wiki.read':          return l.permWikiRead;
      case 'wiki.write':         return l.permWikiWrite;
      case 'graph.read':         return l.permGraphRead;
      case 'graph.write':        return l.permGraphWrite;
      case 'memory.read':        return l.permMemoryRead;
      case 'memory.write':       return l.permMemoryWrite;
      case 'files.read':         return l.permFilesRead;
      case 'files.write':        return l.permFilesWrite;
      case 'cron.register':      return l.permCronRegister;
      case 'webhook.register':   return l.permWebhookRegister;
      case 'notify.send':        return l.permNotifySend;
      case 'sandbox.exec':       return l.permSandboxExec;
      case 'oauth':              return l.permOauth;
      case 'secrets.read':       return l.permSecretsRead;
    }
    return p;
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({
    required this.icon,
    required this.color,
    required this.label,
    required this.count,
  });

  final IconData icon;
  final Color color;
  final String label;
  final int count;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        children: [
          Icon(icon, size: 16, color: color),
          const SizedBox(width: 6),
          Text(
            '$label ($count)',
            style: Theme.of(context).textTheme.titleSmall?.copyWith(
              fontWeight: FontWeight.w600,
              color: color,
            ),
          ),
        ],
      ),
    );
  }
}
