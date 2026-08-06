// InstallDialog — permission confirmation modal.
//
// Renders the app's manifest.permissions[] as a checklist, with the
// description of each permission expanded inline. The user can opt
// out of any permission by unchecking; the install path then
// receives the granted subset (which the server enforces ⊆ manifest).
//
// Decision §21#3 — forced installs (org admin policy) skip the
// modal entirely; this widget handles only user-initiated installs.

import 'package:flutter/material.dart';

import '../../../app/theme.dart';
import '../../../l10n/app_localizations.dart';

/// Result returned by InstallDialog.show. null = user cancelled.
class InstallChoice {
  final List<String> grantedPermissions;
  const InstallChoice({required this.grantedPermissions});
}

/// Static helper to await a result from anywhere in the tree.
class InstallDialog {
  static Future<InstallChoice?> show(
    BuildContext context, {
    required String appName,
    required String version,
    required List<String> permissions,
    String? iconUrl,
    Map<String, String>? iconHeaders,
    String iconRaw = '',
  }) {
    return showDialog<InstallChoice>(
      context: context,
      builder: (_) => _Dialog(
        appName: appName,
        version: version,
        permissions: permissions,
        iconUrl: iconUrl,
        iconHeaders: iconHeaders,
        iconRaw: iconRaw,
      ),
    );
  }
}

class _Dialog extends StatefulWidget {
  const _Dialog({
    required this.appName,
    required this.version,
    required this.permissions,
    this.iconUrl,
    this.iconHeaders,
    this.iconRaw = '',
  });

  final String appName;
  final String version;
  final List<String> permissions;
  /// 由 resolveAppIcon 解析后的 cas:/http URL — 非空时 dialog 顶部
  /// 渲 24px 真图标。null = 走 emoji / 首字母 fallback。
  final String? iconUrl;
  final Map<String, String>? iconHeaders;
  /// 原始 manifest.icon 字段, 用于 emoji fallback 显示。
  final String iconRaw;

  @override
  State<_Dialog> createState() => _DialogState();
}

class _DialogState extends State<_Dialog> {
  late Set<String> _granted;

  @override
  void initState() {
    super.initState();
    // All permissions checked by default; user can uncheck to opt
    // out (the server treats granted as the authoritative set, so
    // unchecking results in the App lacking that permission at
    // runtime — desired behaviour for genuinely-optional perms).
    _granted = {...widget.permissions};
  }

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    final theme = Theme.of(context);
    return AlertDialog(
      title: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          _buildIcon(theme),
          const SizedBox(width: BiuTokens.space2),
          Expanded(
            child: Text(l10n.appsInstallTitle(widget.appName, widget.version)),
          ),
        ],
      ),
      content: SizedBox(
        width: 480,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(l10n.appsPermissionRequestIntro,
                style: theme.textTheme.bodyMedium),
            const SizedBox(height: BiuTokens.space3),
            if (widget.permissions.isEmpty)
              Text(l10n.appsNoPermissionRequested,
                  style: theme.textTheme.bodySmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant)),
            ...widget.permissions.map(_buildRow),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: Text(l10n.appsCancel),
        ),
        FilledButton(
          onPressed: () => Navigator.of(context).pop(
            InstallChoice(grantedPermissions: _granted.toList()..sort()),
          ),
          child: Text(l10n.appsInstall),
        ),
      ],
    );
  }

  Widget _buildIcon(ThemeData theme) {
    final scheme = theme.colorScheme;
    final letter = widget.appName.isEmpty
        ? '?'
        : widget.appName.characters.first.toUpperCase();
    Widget letterFallback() => Text(
          letter,
          style: TextStyle(
            fontSize: 14,
            fontWeight: FontWeight.w700,
            color: scheme.onSurfaceVariant,
          ),
        );
    Widget child;
    if (widget.iconUrl != null) {
      child = Image.network(
        widget.iconUrl!,
        width: 24,
        height: 24,
        fit: BoxFit.cover,
        headers: widget.iconHeaders,
        errorBuilder: (_, _, _) => letterFallback(),
      );
    } else if (widget.iconRaw.isNotEmpty &&
        !widget.iconRaw.startsWith('http') &&
        !widget.iconRaw.startsWith('cas:')) {
      child = Text(widget.iconRaw, style: const TextStyle(fontSize: 18));
    } else {
      child = letterFallback();
    }
    return Container(
      width: 32,
      height: 32,
      alignment: Alignment.center,
      decoration: BoxDecoration(
        color: scheme.surfaceContainerHigh,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: ClipRRect(
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        child: child,
      ),
    );
  }

  Widget _buildRow(String permission) {
    final l10n = AppLocalizations.of(context)!;
    final risky = _isRisky(permission);
    final scheme = Theme.of(context).colorScheme;
    return CheckboxListTile(
      contentPadding: EdgeInsets.zero,
      controlAffinity: ListTileControlAffinity.leading,
      dense: true,
      value: _granted.contains(permission),
      onChanged: (v) => setState(() {
        if (v == true) {
          _granted.add(permission);
        } else {
          _granted.remove(permission);
        }
      }),
      title: Row(
        children: [
          if (risky)
            Padding(
              padding: const EdgeInsets.only(right: 4),
              child: Icon(Icons.warning_amber_rounded,
                  size: 16, color: scheme.error),
            ),
          Expanded(
            child: Text(
              permission,
              style: TextStyle(
                fontFamily: 'monospace',
                fontWeight: FontWeight.w500,
                color: risky ? scheme.error : null,
              ),
            ),
          ),
        ],
      ),
      subtitle: Text(_describePermission(l10n, permission)),
    );
  }

  static bool _isRisky(String p) {
    if (p.startsWith('net.outbound')) return true;
    if (p == 'sandbox.exec') return true;
    if (p.startsWith('secrets.read')) return true;
    return false;
  }

  static String _describePermission(AppLocalizations l, String p) {
    // Strip the optional ":<param>" tail before dispatching.
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
