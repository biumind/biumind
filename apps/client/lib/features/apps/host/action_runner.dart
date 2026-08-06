// ActionRunner — turns a ViewActionRef into a real side effect.
//
// Rules (Design §6.4):
//   - confirm — show OS dialog with Cancel/OK; abort on Cancel.
//   - risk_warning — secondary confirm, even after the user said yes
//                    in the regular confirm. Used by destructive ops.
//   - action — POST /v1/apps/{name}/invoke; show error toast on fail.
//   - route  — push MaterialPageRoute (sibling AppViewHost).
//   - on_success.toast    — SnackBar after success.
//   - on_success.refresh  — invalidate the view's data provider.
//   - on_success.navigate — Navigator.push to that route.
//
// ActionRef must have either `action` or `route` (validated server-
// side); having neither is a no-op rather than crashing the UI.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../data/api/_http_helpers.dart';
import '../../../data/apps_providers.dart';
import '../domain/view_spec.dart';

class ActionRunner {
  ActionRunner({
    required this.ref,
    required this.appIdentifier,
    required this.installId,
    required this.onRouteNavigate,
    this.onRefresh,
  });

  final WidgetRef ref;
  final String appIdentifier;
  final String installId;

  /// Called when an action's `route` field fires (or on_success.navigate).
  /// Caller decides how to navigate (we don't import GoRouter here to
  /// stay agnostic).
  final void Function(BuildContext context, String route) onRouteNavigate;

  /// Called when on_success.refresh is set on a successful action.
  /// Typically invalidates the view's data provider.
  final void Function()? onRefresh;

  Future<void> run(BuildContext context, ViewActionRef a) async {
    if (a.confirm.isNotEmpty) {
      final ok = await _confirm(context, a.confirm);
      if (ok != true) return;
      if (!context.mounted) return;
    }
    if (a.riskWarning.isNotEmpty) {
      final ok = await _confirm(context, a.riskWarning, destructive: true);
      if (ok != true) return;
      if (!context.mounted) return;
    }

    if (a.action.isNotEmpty) {
      await _invoke(context, a);
      return;
    }
    if (a.route.isNotEmpty) {
      onRouteNavigate(context, a.route);
      return;
    }
    // No action / no route — degrade silently. Validator catches this
    // server-side; defending the UI from manifest typos is cheap.
  }

  Future<bool?> _confirm(BuildContext context, String message, {bool destructive = false}) {
    return showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        content: Text(message),
        actions: [
          TextButton(
              onPressed: () => Navigator.of(context).pop(false),
              child: const Text('取消')),
          destructive
              ? FilledButton.tonal(
                  onPressed: () => Navigator.of(context).pop(true),
                  child: const Text('确认'))
              : FilledButton(
                  onPressed: () => Navigator.of(context).pop(true),
                  child: const Text('确认')),
        ],
      ),
    );
  }

  Future<void> _invoke(BuildContext context, ViewActionRef a) async {
    final client = ref.read(appsClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) {
      if (context.mounted) _showSnack(context, 'BiuMind model-relay 未配置');
      return;
    }
    try {
      await client.invoke(
        identifier: appIdentifier,
        action: a.action,
        input: a.input,
        token: token,
      );
      if (!context.mounted) return;
      if (a.onSuccess != null) {
        if (a.onSuccess!.toast.isNotEmpty) {
          _showSnack(context, a.onSuccess!.toast);
        }
        if (a.onSuccess!.refresh) onRefresh?.call();
        if (a.onSuccess!.navigate.isNotEmpty) {
          onRouteNavigate(context, a.onSuccess!.navigate);
        }
      }
    } on ApiError catch (e) {
      if (context.mounted) _showSnack(context, e.body.isNotEmpty ? e.body : 'failed: ${e.status}');
    } catch (e) {
      if (context.mounted) _showSnack(context, e.toString());
    }
  }

  void _showSnack(BuildContext context, String text) {
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(text)));
  }
}
