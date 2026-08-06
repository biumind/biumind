// OsIntegration — unified registration of platform-native hooks.
//
// Per Client Architecture invariant C10: features declare their integrations
// (quick actions, share receivers, protocol handlers) once; this layer maps
// them to whichever platform we're on.
//
// MVP: registry data structures + interface; concrete platform adapters
// (lib/platform/os_integration_*) land in P3.6 with native integration.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class QuickAction {
  final String id;
  final String label;        // i18n key
  final IconData? icon;
  final void Function() handler;
  const QuickAction({required this.id, required this.label, this.icon, required this.handler});
}

class ShareReceiver {
  final String id;
  final List<String> acceptMimeTypes; // e.g. text/plain, image/*, application/pdf
  final void Function(SharePayload payload) handler;
  const ShareReceiver({
    required this.id,
    required this.acceptMimeTypes,
    required this.handler,
  });
}

class SharePayload {
  final String? text;
  final Uri? url;
  final List<Uri> files;
  final Map<String, Object?> metadata;
  const SharePayload({this.text, this.url, this.files = const [], this.metadata = const {}});
}

class ProtocolHandler {
  final String scheme; // "biumind"
  final void Function(Uri uri) handler;
  const ProtocolHandler({required this.scheme, required this.handler});
}

class GlobalShortcut {
  final String id;
  final String accelerator; // platform-portable string e.g. "ctrl+shift+space"
  final void Function() handler;
  const GlobalShortcut({required this.id, required this.accelerator, required this.handler});
}

abstract interface class OsIntegrationAdapter {
  Future<void> registerQuickAction(QuickAction a);
  Future<void> registerShareReceiver(ShareReceiver r);
  Future<void> registerProtocolHandler(ProtocolHandler h);
  Future<void> registerGlobalShortcut(GlobalShortcut s);
  Future<void> dispose();
}

class OsIntegration {
  OsIntegration(this._adapter);
  final OsIntegrationAdapter _adapter;

  final List<QuickAction> _quickActions = [];
  final List<ShareReceiver> _shareReceivers = [];
  final List<ProtocolHandler> _protocols = [];
  final List<GlobalShortcut> _shortcuts = [];

  Future<void> registerQuickAction(QuickAction a) async {
    _quickActions.add(a);
    await _adapter.registerQuickAction(a);
  }

  Future<void> registerShareReceiver(ShareReceiver r) async {
    _shareReceivers.add(r);
    await _adapter.registerShareReceiver(r);
  }

  Future<void> registerProtocolHandler(ProtocolHandler h) async {
    _protocols.add(h);
    await _adapter.registerProtocolHandler(h);
  }

  Future<void> registerGlobalShortcut(GlobalShortcut s) async {
    _shortcuts.add(s);
    await _adapter.registerGlobalShortcut(s);
  }

  /// All registered quick actions (introspection / debug).
  List<QuickAction> get quickActions => List.unmodifiable(_quickActions);
  List<ShareReceiver> get shareReceivers => List.unmodifiable(_shareReceivers);
  List<ProtocolHandler> get protocolHandlers => List.unmodifiable(_protocols);
  List<GlobalShortcut> get globalShortcuts => List.unmodifiable(_shortcuts);
}

/// No-op adapter — the default until platform-specific adapters land.
/// All registrations succeed silently; nothing reaches the OS.
class NoopOsAdapter implements OsIntegrationAdapter {
  @override Future<void> registerQuickAction(QuickAction a) async {}
  @override Future<void> registerShareReceiver(ShareReceiver r) async {}
  @override Future<void> registerProtocolHandler(ProtocolHandler h) async {}
  @override Future<void> registerGlobalShortcut(GlobalShortcut s) async {}
  @override Future<void> dispose() async {}
}

final osIntegrationProvider = Provider<OsIntegration>((ref) {
  return OsIntegration(NoopOsAdapter());
});
