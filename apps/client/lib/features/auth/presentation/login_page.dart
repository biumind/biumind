// LoginPage — full-screen sign-in shown when no credentials are
// configured. Two modes managed internally:
//   * mode=signIn  → email + password (login or kick off register)
//   * mode=verify  → 6-digit code entry post-register / on email_not_verified
//
// Switching modes is in-component state (no router push) — simpler than
// introducing a /verify-email route since we never want users navigating
// to verify with a bookmark; it's always a continuation of the sign-in
// flow with email already known.

import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/ui/biu_text_field.dart';
import '../../../data/api/identity_client.dart';
import '../../../l10n/app_localizations.dart';
import '../../../services/default_endpoints.dart';
import '../../../services/settings_repo.dart';
import '../../../shared/brand/biu_mark.dart';
import '../../splash/presentation/splash_page.dart' show biuMarkHeroTag;
import '../../settings/application/settings_controller.dart';

// signIn:        email + password (login or kick off register)
// verify:        post-register / email_not_verified — 6-digit code entry
// forgotEmail:   "forgot password" lookup — user types email, server sends reset code
// resetCode:     reset flow — user types 6-digit code + new password
enum _AuthMode { signIn, verify, forgotEmail, resetCode }

class LoginPage extends ConsumerStatefulWidget {
  const LoginPage({super.key});

  @override
  ConsumerState<LoginPage> createState() => _LoginPageState();
}

class _LoginPageState extends ConsumerState<LoginPage> {
  late final TextEditingController _identityUrl;
  late final TextEditingController _email;
  late final TextEditingController _password;
  late final TextEditingController _code;
  late final TextEditingController _newPassword;
  bool _busy = false;
  bool _showAdvanced = false;
  String? _message;
  String? _info; // 成功/中性提示 (e.g. "code sent")
  _AuthMode _mode = _AuthMode.signIn;
  // 重发倒计时 — 60s 内 resend 按钮禁用; 0 表示可点击.
  int _resendCooldown = 0;
  Timer? _cooldownTimer;

  @override
  void initState() {
    super.initState();
    final s = ref.read(settingsControllerProvider).valueOrNull ??
        const AppSettings();
    _identityUrl = TextEditingController(
      text: s.identityUrl ?? defaultIdentityUrl(),
    );
    _email = TextEditingController(text: s.userEmail ?? '');
    _password = TextEditingController();
    _code = TextEditingController();
    _newPassword = TextEditingController();
  }

  @override
  void dispose() {
    _cooldownTimer?.cancel();
    _identityUrl.dispose();
    _email.dispose();
    _password.dispose();
    _code.dispose();
    _newPassword.dispose();
    super.dispose();
  }

  void _startCooldown([int seconds = 60]) {
    _cooldownTimer?.cancel();
    setState(() => _resendCooldown = seconds);
    _cooldownTimer = Timer.periodic(const Duration(seconds: 1), (t) {
      if (!mounted) {
        t.cancel();
        return;
      }
      setState(() {
        _resendCooldown -= 1;
        if (_resendCooldown <= 0) {
          _resendCooldown = 0;
          t.cancel();
        }
      });
    });
  }

  Future<void> _doLogin() async {
    setState(() {
      _busy = true;
      _message = null;
      _info = null;
    });
    final ctl = ref.read(settingsControllerProvider.notifier);
    try {
      await ctl.login(
        identityUrl: _identityUrl.text,
        email: _email.text,
        password: _password.text,
      );
      // Router redirect handles the navigation to /chat.
    } on IdentityApiError catch (e) {
      if (!mounted) return;
      // 邮箱未验证 — 切到 verify 模式; 顺手触发一次 resend 以发送新 code.
      if (e.code == 'email_not_verified') {
        await _switchToVerify(autoResend: true);
        return;
      }
      setState(() => _message = _mapAuthError(e, AppLocalizations.of(context)!));
    } catch (e) {
      setState(() => _message = '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _doRegister() async {
    setState(() {
      _busy = true;
      _message = null;
      _info = null;
    });
    final ctl = ref.read(settingsControllerProvider.notifier);
    try {
      final result = await ctl.register(
        identityUrl: _identityUrl.text,
        email: _email.text,
        password: _password.text,
      );
      if (!mounted) return;
      final t = AppLocalizations.of(context)!;
      setState(() {
        _mode = _AuthMode.verify;
        _info = result.emailSent
            ? t.signInCodeSent(_email.text)
            : t.signInCodeDevMode;
      });
      _startCooldown();
    } on IdentityApiError catch (e) {
      if (!mounted) return;
      setState(() => _message = _mapAuthError(e, AppLocalizations.of(context)!));
    } catch (e) {
      setState(() => _message = '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _switchToVerify({bool autoResend = false}) async {
    final t = AppLocalizations.of(context)!;
    setState(() {
      _mode = _AuthMode.verify;
      _message = null;
      _info = t.signInEnterCodeSentTo(_email.text);
      _busy = false;
    });
    if (autoResend) {
      await _doResend(silent: true);
    }
  }

  Future<void> _doVerify() async {
    final t = AppLocalizations.of(context)!;
    final code = _code.text.trim();
    if (code.length != 6) {
      setState(() => _message = t.signInEnterCode);
      return;
    }
    setState(() {
      _busy = true;
      _message = null;
      _info = null;
    });
    final ctl = ref.read(settingsControllerProvider.notifier);
    try {
      await ctl.verifyEmail(
        identityUrl: _identityUrl.text,
        email: _email.text,
        code: code,
      );
      // 成功 → settings 已写入 token, 路由 redirect 自动跳 /chat.
    } on IdentityApiError catch (e) {
      if (!mounted) return;
      setState(() => _message = _mapVerifyError(e, AppLocalizations.of(context)!));
      // 错码不清空输入, 让用户改一位再提交; locked / expired 时清空提示重发.
      if (e.code == 'code_locked' ||
          e.code == 'code_expired' ||
          e.code == 'code_already_used') {
        _code.clear();
      }
    } catch (e) {
      setState(() => _message = '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _doResend({bool silent = false}) async {
    if (_resendCooldown > 0) return;
    setState(() {
      _busy = true;
      _message = null;
      if (!silent) _info = null;
    });
    final ctl = ref.read(settingsControllerProvider.notifier);
    try {
      final sent = await ctl.resendVerification(
        identityUrl: _identityUrl.text,
        email: _email.text,
      );
      if (!mounted) return;
      _startCooldown();
      final t = AppLocalizations.of(context)!;
      setState(() {
        _info = sent
            ? t.signInCodeResent(_email.text)
            : t.signInCodeRegenerated;
      });
    } on IdentityApiError catch (e) {
      if (!mounted) return;
      setState(() => _message = _mapVerifyError(e, AppLocalizations.of(context)!));
    } catch (e) {
      setState(() => _message = '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _backToSignIn() {
    setState(() {
      _mode = _AuthMode.signIn;
      _message = null;
      _info = null;
      _code.clear();
      _newPassword.clear();
    });
    _cooldownTimer?.cancel();
    _resendCooldown = 0;
  }

  void _enterForgotPassword() {
    final t = AppLocalizations.of(context)!;
    setState(() {
      _mode = _AuthMode.forgotEmail;
      _message = null;
      _info = t.signInForgotHint;
      _password.clear();
    });
  }

  Future<void> _doForgot() async {
    final t = AppLocalizations.of(context)!;
    final email = _email.text.trim();
    if (email.isEmpty || !email.contains('@')) {
      setState(() => _message = t.signInErrEmailRequired);
      return;
    }
    setState(() {
      _busy = true;
      _message = null;
      _info = null;
    });
    final ctl = ref.read(settingsControllerProvider.notifier);
    try {
      final sent = await ctl.forgotPassword(
        identityUrl: _identityUrl.text,
        email: email,
      );
      if (!mounted) return;
      setState(() {
        _mode = _AuthMode.resetCode;
        _info = sent
            ? t.signInResetCodeSent(email)
            : t.signInResetCodeDevMode;
      });
      _startCooldown();
    } on IdentityApiError catch (e) {
      if (!mounted) return;
      setState(() => _message = _mapVerifyError(e, AppLocalizations.of(context)!));
    } catch (e) {
      setState(() => _message = '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _doReset() async {
    final t = AppLocalizations.of(context)!;
    final code = _code.text.trim();
    final newPass = _newPassword.text;
    if (code.length != 6) {
      setState(() => _message = t.signInEnterCode);
      return;
    }
    if (newPass.length < 8) {
      setState(() => _message = t.signInErrNewPasswordShort);
      return;
    }
    setState(() {
      _busy = true;
      _message = null;
      _info = null;
    });
    final ctl = ref.read(settingsControllerProvider.notifier);
    try {
      await ctl.resetPassword(
        identityUrl: _identityUrl.text,
        email: _email.text,
        code: code,
        newPassword: newPass,
      );
      if (!mounted) return;
      // 成功 — 切回登录页, 让用户用新密码登录.
      setState(() {
        _mode = _AuthMode.signIn;
        _info = t.signInPasswordReset;
        _password.clear();
        _code.clear();
        _newPassword.clear();
      });
      _cooldownTimer?.cancel();
      _resendCooldown = 0;
    } on IdentityApiError catch (e) {
      if (!mounted) return;
      setState(() => _message = _mapVerifyError(e, AppLocalizations.of(context)!));
      if (e.code == 'code_locked' ||
          e.code == 'code_expired' ||
          e.code == 'code_already_used') {
        _code.clear();
      }
    } catch (e) {
      setState(() => _message = '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _resendReset() async {
    if (_resendCooldown > 0) return;
    setState(() {
      _busy = true;
      _message = null;
    });
    final ctl = ref.read(settingsControllerProvider.notifier);
    try {
      final sent = await ctl.forgotPassword(
        identityUrl: _identityUrl.text,
        email: _email.text,
      );
      if (!mounted) return;
      _startCooldown();
      final t = AppLocalizations.of(context)!;
      setState(() {
        _info = sent
            ? t.signInCodeResent(_email.text)
            : t.signInCodeRegenerated;
      });
    } on IdentityApiError catch (e) {
      if (!mounted) return;
      setState(() => _message = _mapVerifyError(e, AppLocalizations.of(context)!));
    } catch (e) {
      setState(() => _message = '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = AppLocalizations.of(context)!;
    return Scaffold(
      backgroundColor: BiuTokens.bg,
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 400),
          child: SingleChildScrollView(
            padding: const EdgeInsets.all(BiuTokens.space5),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                const Center(
                  child: Hero(
                    tag: biuMarkHeroTag,
                    child: BiuMark(size: 72),
                  ),
                ),
                const SizedBox(height: BiuTokens.space4),
                Text(
                  'BiuMind',
                  textAlign: TextAlign.center,
                  style: Theme.of(context).textTheme.displayMedium,
                ),
                const SizedBox(height: BiuTokens.space2),
                Text(
                  _subtitleFor(_mode, t),
                  textAlign: TextAlign.center,
                  style: Theme.of(context).textTheme.bodySmall,
                ),
                const SizedBox(height: BiuTokens.space6),

                if (_mode == _AuthMode.signIn) ..._signInForm(t)
                else if (_mode == _AuthMode.verify) ..._verifyForm(t)
                else if (_mode == _AuthMode.forgotEmail) ..._forgotEmailForm(t)
                else ..._resetCodeForm(t),

                if (_message != null) ...[
                  const SizedBox(height: BiuTokens.space3),
                  _banner(_message!, color: BiuTokens.error, soft: BiuTokens.errorSoft, icon: Icons.error_outline),
                ],
                if (_info != null) ...[
                  const SizedBox(height: BiuTokens.space3),
                  _banner(_info!, color: BiuTokens.purple, soft: BiuTokens.purpleSoft, icon: Icons.info_outline),
                ],

                const SizedBox(height: BiuTokens.space5),

                if (_mode == _AuthMode.signIn) ..._signInActions(t)
                else if (_mode == _AuthMode.verify) ..._verifyActions(t)
                else if (_mode == _AuthMode.forgotEmail) ..._forgotEmailActions(t)
                else ..._resetCodeActions(t),
              ],
            ),
          ),
        ),
      ),
    );
  }

  List<Widget> _signInForm(AppLocalizations t) => [
        _label(t.signInEmail),
        BiuTextField(
          controller: _email,
          keyboardType: TextInputType.emailAddress,
          autofillHints: const [AutofillHints.email, AutofillHints.username],
          hintText: 'you@example.com',
        ),
        const SizedBox(height: BiuTokens.space3),
        _label(t.signInPassword),
        BiuTextField(
          controller: _password,
          obscureText: true,
          autofillHints: const [AutofillHints.password],
          onSubmitted: (_) => _busy ? null : _doLogin(),
        ),
        const SizedBox(height: BiuTokens.space2),
        MouseRegion(
          cursor: SystemMouseCursors.click,
          child: GestureDetector(
            onTap: () => setState(() => _showAdvanced = !_showAdvanced),
            behavior: HitTestBehavior.opaque,
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 6),
              child: Row(
                children: [
                  Icon(
                    _showAdvanced ? Icons.expand_less : Icons.expand_more,
                    size: 16,
                    color: BiuTokens.textMuted,
                  ),
                  const SizedBox(width: 4),
                  Text(
                    t.signInAdvanced,
                    style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
                  ),
                ],
              ),
            ),
          ),
        ),
        if (_showAdvanced) ...[
          _label(t.signInIdentityUrl),
          BiuTextField(
            controller: _identityUrl,
            keyboardType: TextInputType.url,
            hintText: defaultIdentityUrl(),
          ),
        ],
      ];

  List<Widget> _verifyForm(AppLocalizations t) => [
        _label(t.signInEmail),
        BiuTextField(
          controller: _email,
          enabled: false,
        ),
        const SizedBox(height: BiuTokens.space3),
        _label(t.signInVerifyCode),
        BiuTextField(
          controller: _code,
          keyboardType: TextInputType.number,
          maxLength: 6,
          autofocus: true,
          inputFormatters: [FilteringTextInputFormatter.digitsOnly],
          textAlign: TextAlign.center,
          style: const TextStyle(
            fontSize: 22,
            letterSpacing: 6,
            fontFeatures: [FontFeature.tabularFigures()],
          ),
          // counterText 隐藏 + 自定义 hint dot — 走 decoration override
          decoration: const InputDecoration(
            hintText: '······',
            counterText: '',
          ),
          onSubmitted: (_) => _busy ? null : _doVerify(),
        ),
      ];

  List<Widget> _signInActions(AppLocalizations t) => [
        SizedBox(
          height: 40,
          child: FilledButton(
            onPressed: _busy ? null : _doLogin,
            child: Text(_busy ? '...' : t.signInSubmit),
          ),
        ),
        const SizedBox(height: BiuTokens.space1),
        Align(
          alignment: Alignment.centerRight,
          child: TextButton(
            onPressed: _busy ? null : _enterForgotPassword,
            style: TextButton.styleFrom(
              foregroundColor: BiuTokens.textSecondary,
              textStyle: const TextStyle(fontSize: 12),
              padding: EdgeInsets.zero,
              minimumSize: const Size(0, 28),
            ),
            child: Text(t.signInForgotPassword),
          ),
        ),
        Row(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Text(t.signInNoAccount, style: Theme.of(context).textTheme.bodySmall),
            TextButton(
              onPressed: _busy ? null : _doRegister,
              style: TextButton.styleFrom(
                foregroundColor: BiuTokens.purple,
                textStyle: const TextStyle(
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                ),
              ),
              child: Text(t.signInRegister),
            ),
          ],
        ),
      ];

  List<Widget> _forgotEmailForm(AppLocalizations t) => [
        _label(t.signInEmail),
        BiuTextField(
          controller: _email,
          keyboardType: TextInputType.emailAddress,
          autofillHints: const [AutofillHints.email],
          autofocus: true,
          hintText: 'you@example.com',
          onSubmitted: (_) => _busy ? null : _doForgot(),
        ),
      ];

  List<Widget> _forgotEmailActions(AppLocalizations t) => [
        SizedBox(
          height: 40,
          child: FilledButton(
            onPressed: _busy ? null : _doForgot,
            child: Text(_busy ? '...' : t.signInSendResetCode),
          ),
        ),
        const SizedBox(height: BiuTokens.space2),
        Align(
          alignment: Alignment.center,
          child: TextButton(
            onPressed: _busy ? null : _backToSignIn,
            style: TextButton.styleFrom(
              foregroundColor: BiuTokens.textSecondary,
              textStyle: const TextStyle(fontSize: 13),
            ),
            child: Text(t.signInBackToSignIn),
          ),
        ),
      ];

  List<Widget> _resetCodeForm(AppLocalizations t) => [
        _label(t.signInEmail),
        BiuTextField(
          controller: _email,
          enabled: false,
        ),
        const SizedBox(height: BiuTokens.space3),
        _label(t.signInVerifyCode),
        BiuTextField(
          controller: _code,
          keyboardType: TextInputType.number,
          maxLength: 6,
          autofocus: true,
          inputFormatters: [FilteringTextInputFormatter.digitsOnly],
          textAlign: TextAlign.center,
          style: const TextStyle(
            fontSize: 22,
            letterSpacing: 6,
            fontFeatures: [FontFeature.tabularFigures()],
          ),
          decoration: const InputDecoration(
            hintText: '······',
            counterText: '',
          ),
        ),
        const SizedBox(height: BiuTokens.space3),
        _label(t.signInNewPasswordLabel),
        BiuTextField(
          controller: _newPassword,
          obscureText: true,
          autofillHints: const [AutofillHints.newPassword],
          onSubmitted: (_) => _busy ? null : _doReset(),
        ),
      ];

  List<Widget> _resetCodeActions(AppLocalizations t) => [
        SizedBox(
          height: 40,
          child: FilledButton(
            onPressed: _busy ? null : _doReset,
            child: Text(_busy ? '...' : t.signInResetPassword),
          ),
        ),
        const SizedBox(height: BiuTokens.space2),
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            TextButton(
              onPressed: _busy ? null : _backToSignIn,
              style: TextButton.styleFrom(
                foregroundColor: BiuTokens.textSecondary,
                textStyle: const TextStyle(fontSize: 13),
              ),
              child: Text(t.signInBackToSignIn),
            ),
            TextButton(
              onPressed: (_busy || _resendCooldown > 0) ? null : _resendReset,
              style: TextButton.styleFrom(
                foregroundColor: BiuTokens.purple,
                textStyle: const TextStyle(
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                ),
              ),
              child: Text(
                _resendCooldown > 0
                    ? t.signInResendCooldown(_resendCooldown)
                    : t.signInResendCode,
              ),
            ),
          ],
        ),
      ];

  String _subtitleFor(_AuthMode mode, AppLocalizations t) {
    switch (mode) {
      case _AuthMode.signIn:
        return t.signInSubtitle;
      case _AuthMode.verify:
        return t.signInSubtitleVerify;
      case _AuthMode.forgotEmail:
        return t.signInSubtitleForgot;
      case _AuthMode.resetCode:
        return t.signInSubtitleReset;
    }
  }

  List<Widget> _verifyActions(AppLocalizations t) => [
        SizedBox(
          height: 40,
          child: FilledButton(
            onPressed: _busy ? null : _doVerify,
            child: Text(_busy ? '...' : t.signInVerifySubmit),
          ),
        ),
        const SizedBox(height: BiuTokens.space2),
        Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          children: [
            TextButton(
              onPressed: _busy ? null : _backToSignIn,
              style: TextButton.styleFrom(
                foregroundColor: BiuTokens.textSecondary,
                textStyle: const TextStyle(fontSize: 13),
              ),
              child: Text(t.signInBackToSignIn),
            ),
            TextButton(
              onPressed: (_busy || _resendCooldown > 0) ? null : _doResend,
              style: TextButton.styleFrom(
                foregroundColor: BiuTokens.purple,
                textStyle: const TextStyle(
                  fontSize: 13,
                  fontWeight: FontWeight.w600,
                ),
              ),
              child: Text(
                _resendCooldown > 0
                    ? t.signInResendCooldown(_resendCooldown)
                    : t.signInResendCode,
              ),
            ),
          ],
        ),
      ];

  Widget _banner(String text, {required Color color, required Color soft, required IconData icon}) {
    return Container(
      padding: const EdgeInsets.symmetric(
        horizontal: BiuTokens.space3,
        vertical: BiuTokens.space2,
      ),
      decoration: BoxDecoration(
        color: soft,
        borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      ),
      child: Row(
        children: [
          Icon(icon, size: 14, color: color),
          const SizedBox(width: BiuTokens.space2),
          Expanded(
            child: Text(text, style: TextStyle(fontSize: 13, color: color)),
          ),
        ],
      ),
    );
  }

  Widget _label(String text) {
    return Padding(
      padding: const EdgeInsets.only(bottom: BiuTokens.space1),
      child: Text(
        text,
        style: TextStyle(
          fontSize: 13,
          fontWeight: FontWeight.w500,
          color: BiuTokens.textSecondary,
        ),
      ),
    );
  }
}

/// Map Identity error codes to a single localized line. Falls through
/// to the server's message (or HTTP status) for codes we don't model
/// yet, so new error types still surface usefully.
String _mapAuthError(IdentityApiError e, AppLocalizations t) {
  switch (e.code) {
    case 'invalid_credentials':
      return t.signInErrInvalidCredentials;
    case 'email_taken':
    case 'user_exists':
    case 'already_exists':
      return t.signInErrEmailTaken;
    case 'invalid_email':
      return t.signInErrInvalidEmail;
    case 'password_too_short':
    case 'weak_password':
      return t.signInErrPasswordTooShort;
    case 'identity_unreachable':
    case '':
      if (e.status == 0) return t.signInErrNetwork;
      break;
  }
  final msg = e.friendlyMessage;
  return msg.isEmpty ? t.signInErrUnknown : msg;
}

String _mapVerifyError(IdentityApiError e, AppLocalizations t) {
  switch (e.code) {
    case 'invalid_code':
      return t.signInErrInvalidCode;
    case 'code_expired':
      return t.signInErrCodeExpired;
    case 'code_locked':
      return t.signInErrCodeLocked;
    case 'code_already_used':
      return t.signInErrCodeUsed;
    case 'no_pending_code':
      return t.signInErrNoPendingCode;
    case 'rate_limited':
      return t.signInErrRateLimited;
    case '':
      if (e.status == 0) return t.signInErrNetwork;
      break;
  }
  return e.friendlyMessage;
}
