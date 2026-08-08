// Simple non-provider settings panes:
//
//   AppearancePane    — theme picker
//   DefaultModelPane  — default chat model dropdown
//   CredentialsPane   — Identity URL + sign in / out
//   AboutPane         — app version / release notes link
//
// All wrapped in a centered scrollview with a section title.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/ui/biu_text_field.dart';
import '../../../data/api/identity_client.dart';
import '../../../l10n/app_localizations.dart';
import '../../../services/default_endpoints.dart';
import '../../../services/settings_repo.dart';
import '../../../features/update/application/update_check_controller.dart';
import '../application/settings_controller.dart';

class _PaneShell extends StatelessWidget {
  const _PaneShell({
    required this.title,
    required this.subtitle,
    required this.child,
  });
  final String title;
  final String subtitle;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 720),
        child: ListView(
          padding: const EdgeInsets.symmetric(
              horizontal: BiuTokens.space5, vertical: BiuTokens.space6),
          children: [
            Text(title, style: Theme.of(context).textTheme.headlineLarge),
            const SizedBox(height: BiuTokens.space1),
            Text(subtitle, style: Theme.of(context).textTheme.bodySmall),
            const SizedBox(height: BiuTokens.space5),
            child,
          ],
        ),
      ),
    );
  }
}

// ─── Appearance ────────────────────────────────────────

class AppearancePane extends ConsumerWidget {
  const AppearancePane({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final t = AppLocalizations.of(context)!;
    final theme = ref.watch(settingsControllerProvider).valueOrNull?.theme ??
        ThemePreference.system;
    Future<void> set(ThemePreference p) async {
      await ref.read(settingsControllerProvider.notifier).updateTheme(p);
    }
    return _PaneShell(
      title: t.settingsNavAppearance,
      subtitle: t.settingsAppearanceSectionSubtitle,
      child: Column(
        children: [
          _ChoiceRow(
            title: t.themeSystem,
            selected: theme == ThemePreference.system,
            onTap: () => set(ThemePreference.system),
          ),
          _ChoiceRow(
            title: t.themeLight,
            selected: theme == ThemePreference.light,
            onTap: () => set(ThemePreference.light),
          ),
          _ChoiceRow(
            title: t.themeDark,
            selected: theme == ThemePreference.dark,
            onTap: () => set(ThemePreference.dark),
          ),
        ],
      ),
    );
  }
}

// 注:原 DefaultModelPane(智能体 > 服务模型)已删——它写/读的
// settingsController.defaultChatModel 是自产自销的僵尸字段,不影响任何新会话
// 用什么模型(真正的默认模型在 chatPreferences.defaultModel,见 ChatSettingsPane)。
// 后端 settings_repo.default_chat_model 列保留不动,避免触 schema。

// ─── Credentials (Identity URL + login) ───────────────

class CredentialsPane extends ConsumerStatefulWidget {
  const CredentialsPane({super.key});
  @override
  ConsumerState<CredentialsPane> createState() => _CredentialsPaneState();
}

class _CredentialsPaneState extends ConsumerState<CredentialsPane> {
  final _identityUrl = TextEditingController();
  final _email = TextEditingController();
  final _password = TextEditingController();
  bool _busy = false;
  String? _msg;
  bool _ok = false;

  @override
  void initState() {
    super.initState();
    final s = ref.read(settingsControllerProvider).valueOrNull;
    _identityUrl.text = s?.identityUrl ?? defaultIdentityUrl();
    _email.text = s?.userEmail ?? '';
  }

  @override
  void dispose() {
    _identityUrl.dispose();
    _email.dispose();
    _password.dispose();
    super.dispose();
  }

  Future<void> _go({required bool register}) async {
    setState(() {
      _busy = true;
      _msg = null;
    });
    final ctl = ref.read(settingsControllerProvider.notifier);
    try {
      String? successMsg;
      if (register) {
        // Register no longer issues tokens — the LoginPage handles the
        // 6-digit verification flow. From the Settings pane just nudge
        // the user to do it there (避免在两个入口实现两遍 verify UI).
        final r = await ctl.register(
          identityUrl: _identityUrl.text,
          email: _email.text,
          password: _password.text,
        );
        successMsg = r.emailSent
            ? '验证码已发送至 ${r.email}, 请到登录页完成邮箱验证'
            : '账户已创建. 当前为开发模式 (SMTP 未配置), 请联系管理员从服务日志获取验证码';
      } else {
        final r = await ctl.login(
          identityUrl: _identityUrl.text,
          email: _email.text,
          password: _password.text,
        );
        if (!mounted) return;
        final t = AppLocalizations.of(context)!;
        successMsg = t.signInOk(r.email);
      }
      if (!mounted) return;
      setState(() {
        _ok = true;
        _msg = successMsg;
        _password.clear();
      });
    } on IdentityApiError catch (e) {
      setState(() {
        _ok = false;
        _msg = e.friendlyMessage.isNotEmpty
            ? e.friendlyMessage
            : 'http ${e.status}';
      });
    } catch (e) {
      setState(() {
        _ok = false;
        _msg = '$e';
      });
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = AppLocalizations.of(context)!;
    final s = ref.watch(settingsControllerProvider).valueOrNull;
    return _PaneShell(
      title: t.settingsNavCredentials,
      subtitle: t.settingsAccountSectionSubtitle,
      child: Column(
        children: [
          _Field(
              controller: _identityUrl,
              label: t.settingsServiceUrl,
              hint: defaultIdentityUrl()),
          _Field(
              controller: _email,
              label: t.signInEmail,
              hint: 'you@example.com',
              keyboardType: TextInputType.emailAddress),
          _Field(
              controller: _password,
              label: t.signInPassword,
              obscure: true,
              onSubmit: () => _busy ? null : _go(register: false)),
          const SizedBox(height: BiuTokens.space2),
          Wrap(
            spacing: BiuTokens.space2,
            runSpacing: BiuTokens.space2,
            children: [
              FilledButton(
                onPressed: _busy ? null : () => _go(register: false),
                child: Text(_busy ? '...' : t.signInSubmit),
              ),
              OutlinedButton(
                onPressed: _busy ? null : () => _go(register: true),
                child: Text(t.signInRegister),
              ),
              if (s?.userEmail != null && s!.userEmail!.isNotEmpty)
                TextButton(
                  onPressed: () async {
                    await ref
                        .read(settingsControllerProvider.notifier)
                        .signOut();
                    setState(() => _msg = null);
                  },
                  child: Text(t.signInSignOut(s.userEmail!)),
                ),
            ],
          ),
          if (_msg != null) ...[
            const SizedBox(height: BiuTokens.space3),
            Container(
              padding: const EdgeInsets.symmetric(
                  horizontal: BiuTokens.space3, vertical: BiuTokens.space2),
              decoration: BoxDecoration(
                color: _ok
                    ? BiuTokens.green.withValues(alpha: 0.08)
                    : BiuTokens.error.withValues(alpha: 0.08),
                borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
              ),
              child: Text(_msg!,
                  style: TextStyle(
                      fontSize: 13,
                      color: _ok ? BiuTokens.green : BiuTokens.error)),
            ),
          ],
          if (s?.tokenExpiresAt != null && s!.tokenExpiresAt!.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: BiuTokens.space2),
              child: Text(
                t.signInTokenExpires(s.tokenExpiresAt!),
                style: TextStyle(
                    color: BiuTokens.textMuted, fontSize: 12),
              ),
            ),
        ],
      ),
    );
  }
}

// ─── About ─────────────────────────────────────────────

class AboutPane extends ConsumerWidget {
  const AboutPane({super.key});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final t = AppLocalizations.of(context)!;
    final settings = ref.watch(settingsControllerProvider).valueOrNull;
    final asyncUpdate = ref.watch(updateAvailableProvider);
    final update = asyncUpdate.valueOrNull;

    // 检查更新 状态: 有更新 (banner 已在顶部提示, 这里只显状态) > 检查中 > 已最新。
    final String checkStatus;
    if (update != null) {
      checkStatus = update.isNightly
          ? '${t.settingsCheckUpdateAvailable} #${update.nightlyRun}'
          : t.settingsCheckUpdateAvailable;
    } else if (asyncUpdate.isLoading) {
      checkStatus = t.settingsCheckUpdateChecking;
    } else {
      checkStatus = t.settingsCheckUpdateLatest;
    }

    return _PaneShell(
      title: t.settingsNavAbout,
      subtitle: t.aboutSubtitle,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _kv('BiuMind', t.aboutVersion),
          _kv(t.aboutBuild, '0.1.0+1'),
          const SizedBox(height: BiuTokens.space4),
          // ── 更新 ──────────────────────────────────────────
          _kv(t.settingsCheckUpdate, checkStatus),
          const SizedBox(height: BiuTokens.space3),
          SwitchListTile(
            contentPadding: EdgeInsets.zero,
            dense: true,
            title: Text(t.settingsFetchNightly,
                style:
                    TextStyle(fontSize: 13, color: BiuTokens.text)),
            subtitle: Text(t.settingsFetchNightlySubtitle,
                style: TextStyle(
                    fontSize: 11, color: BiuTokens.textSecondary)),
            value: settings?.fetchNightly ?? false,
            // settings 未加载完时禁用, 避免写默认值。
            onChanged: settings == null
                ? null
                : (v) => ref
                    .read(settingsControllerProvider.notifier)
                    .updateFetchNightly(v),
          ),
          const SizedBox(height: BiuTokens.space4),
          Text(t.aboutTagline,
              style: TextStyle(
                  color: BiuTokens.textMuted, fontSize: 12)),
        ],
      ),
    );
  }

  Widget _kv(String k, String v) => Padding(
        padding: const EdgeInsets.only(bottom: BiuTokens.space2),
        child: Row(
          children: [
            SizedBox(
              width: 120,
              child: Text(k,
                  style: TextStyle(
                      fontSize: 13, color: BiuTokens.textSecondary)),
            ),
            Text(v,
                style: TextStyle(fontSize: 13, color: BiuTokens.text)),
          ],
        ),
      );
}

// ─── Reusable bits ─────────────────────────────────────

class _ChoiceRow extends StatelessWidget {
  const _ChoiceRow({
    required this.title,
    required this.selected,
    required this.onTap,
  });
  final String title;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      onTap: onTap,
      behavior: HitTestBehavior.opaque,
      child: Container(
        margin: const EdgeInsets.only(bottom: BiuTokens.space2),
        padding: const EdgeInsets.symmetric(
            horizontal: BiuTokens.space3, vertical: BiuTokens.space3),
        decoration: BoxDecoration(
          color: selected ? BiuTokens.purpleLight : BiuTokens.surface,
          borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
          border: Border.all(
              color: selected ? BiuTokens.purple : BiuTokens.border,
              width: selected ? 1.5 : 1),
        ),
        child: Row(
          children: [
            Container(
              width: 14,
              height: 14,
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                border: Border.all(
                  color: selected ? BiuTokens.purple : BiuTokens.textMuted,
                  width: selected ? 4 : 1.5,
                ),
                color: Colors.white,
              ),
            ),
            const SizedBox(width: BiuTokens.space3),
            Text(title,
                style: TextStyle(fontSize: 14, color: BiuTokens.text)),
          ],
        ),
      ),
    );
  }
}

class _Field extends StatelessWidget {
  const _Field({
    required this.controller,
    required this.label,
    this.hint,
    this.obscure = false,
    this.keyboardType,
    this.onSubmit,
  });
  final TextEditingController controller;
  final String label;
  final String? hint;
  final bool obscure;
  final TextInputType? keyboardType;
  final VoidCallback? onSubmit;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: BiuTokens.space3),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label,
              style: TextStyle(
                  fontSize: 13,
                  color: BiuTokens.textSecondary,
                  fontWeight: FontWeight.w500)),
          const SizedBox(height: BiuTokens.space1),
          BiuTextField(
            controller: controller,
            obscureText: obscure,
            keyboardType: keyboardType,
            onSubmitted: onSubmit == null ? null : (_) => onSubmit!(),
            style: const TextStyle(fontSize: 14),
            hintText: hint,
          ),
        ],
      ),
    );
  }
}
