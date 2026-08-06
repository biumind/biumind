import 'package:biumind/core/osintegration/os_integration.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('registrations stored', () async {
    final os = OsIntegration(NoopOsAdapter());
    await os.registerQuickAction(QuickAction(id: 'new_chat', label: 'New chat', handler: () {}));
    await os.registerShareReceiver(ShareReceiver(
      id: 'wiki.clip', acceptMimeTypes: ['text/plain'], handler: (_) {},
    ));
    await os.registerProtocolHandler(const ProtocolHandler(scheme: 'biumind', handler: _noopUri));
    await os.registerGlobalShortcut(GlobalShortcut(
      id: 'cmd_palette', accelerator: 'cmd+k', handler: () {},
    ));

    expect(os.quickActions.map((a) => a.id), ['new_chat']);
    expect(os.shareReceivers.length, 1);
    expect(os.protocolHandlers.first.scheme, 'biumind');
    expect(os.globalShortcuts.first.accelerator, 'cmd+k');
  });
}

void _noopUri(Uri u) {}
