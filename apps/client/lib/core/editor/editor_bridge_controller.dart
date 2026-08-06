/// Transport-agnostic bridge controller used by both the Flutter Web
/// (iframe + postMessage) and native (inappwebview + addJavaScriptHandler)
/// implementations of the embedded Milkdown editor.
///
/// The controller owns the state machine; the platform widget owns the
/// transport. Wiring is symmetric:
///   * widget calls [onIncomingMessage] for every message from the editor
///   * controller calls [_send] (provided at attach time) when it needs
///     to push something into the editor
///
/// The controller is the single source of truth for the host-side
/// `revision` counter — every `setDoc` we emit increments it, and we
/// drop any inbound `docChanged` whose revision is older than what we
/// last sent (out-of-order safety).
library;

import 'dart:async';

import 'package:flutter/foundation.dart';

import 'editor_bridge_protocol.dart';

typedef EditorSend = Future<void> Function(BridgeMessage message);

typedef WikilinkResolver = Future<List<WikilinkSuggestion>> Function(
  String prefix,
);

class EditorBridgeController extends ChangeNotifier {
  EditorBridgeController({
    required this.initialMarkdown,
    required this.theme,
    this.readOnly = false,
    this.locale = 'zh-Hans',
    this.features = const BridgeFeatures(),
  });

  final String initialMarkdown;
  BridgeTheme theme;
  final bool readOnly;
  final String locale;
  final BridgeFeatures features;

  /// Called for every authoritative `docChanged` from the editor (i.e.
  /// after revision/echo filtering). Wire this to AutoSaveController.
  void Function(String markdown)? onMarkdownChanged;

  /// Called when the user taps a `[[wikilink]]` inside the editor.
  void Function(String slug)? onWikilinkTap;

  /// Called when the user taps an external link.
  void Function(String url)? onExternalLinkTap;

  /// Called whenever the editor selection changes (S3 P1-6 selection-edit).
  /// `empty` = collapsed caret → host should hide the Ask/Edit overlay.
  /// `coords` is viewport-relative (PM coordsAtPos); host adds the WebView's
  /// screen origin to anchor the follow overlay.
  void Function(EditorSelection selection)? onSelectionChange;

  /// Called by the controller to fetch wikilink completion candidates.
  /// Wire to apiRepository.
  WikilinkResolver? resolveWikilinks;

  EditorSend? _send;
  final Completer<void> _readyCompleter = Completer<void>();
  Future<void> get ready => _readyCompleter.future;

  int _hostRevision = 0;
  String? _lastEditorMarkdown;
  bool _initSent = false;

  /// Called by the platform widget once its transport is connected.
  /// The controller will hold off on sending anything until the editor
  /// announces `ready`.
  void attach(EditorSend send) {
    _send = send;
  }

  void detach() {
    _send = null;
  }

  /// Dispatch an incoming wire message from the editor. The platform
  /// widget is responsible for parsing and calling this.
  Future<void> onIncomingMessage(BridgeMessage msg) async {
    if (msg.v != kBridgeProtocolVersion) {
      debugPrint(
        '[editor-bridge] protocol version mismatch: '
        'host v=$kBridgeProtocolVersion, editor v=${msg.v}',
      );
      return;
    }
    switch (msg.type) {
      case 'ready':
        await _onReady();
      case 'docChanged':
        _onDocChanged(msg);
      case 'wikilinkQuery':
        await _onWikilinkQuery(msg);
      case 'navigate':
        _onNavigate(msg);
      case 'log':
        _onLog(msg);
      case 'selectionChanged':
        _onSelectionChanged(msg);
      default:
        debugPrint('[editor-bridge] unknown message type: ${msg.type}');
    }
  }

  Future<void> _onReady() async {
    if (_initSent) return;
    _initSent = true;
    await _send?.call(
      initMessage(
        markdown: initialMarkdown,
        theme: theme,
        readOnly: readOnly,
        locale: locale,
        features: features,
      ),
    );
    if (!_readyCompleter.isCompleted) _readyCompleter.complete();
  }

  void _onDocChanged(BridgeMessage msg) {
    final revision = msg.payload['revision'];
    final markdown = msg.payload['markdown'];
    if (revision is! num || markdown is! String) return;
    // Out-of-order guard: drop anything older than the last host write.
    if (revision.toInt() < _hostRevision) return;
    _lastEditorMarkdown = markdown;
    onMarkdownChanged?.call(markdown);
  }

  Future<void> _onWikilinkQuery(BridgeMessage msg) async {
    final id = msg.id;
    final prefix = msg.payload['prefix'];
    final resolver = resolveWikilinks;
    if (id == null || prefix is! String || resolver == null) return;
    List<WikilinkSuggestion> items;
    try {
      items = await resolver(prefix);
    } on Exception catch (_) {
      items = const <WikilinkSuggestion>[];
    }
    await _send?.call(wikilinkQueryReplyMessage(id: id, items: items));
  }

  void _onNavigate(BridgeMessage msg) {
    final kind = msg.payload['kind'];
    if (kind == 'wikilink') {
      final slug = msg.payload['slug'];
      if (slug is String) onWikilinkTap?.call(slug);
    } else if (kind == 'external') {
      final url = msg.payload['url'];
      if (url is String) onExternalLinkTap?.call(url);
    }
  }

  void _onLog(BridgeMessage msg) {
    final level = msg.payload['level'];
    final text = msg.payload['msg'];
    debugPrint('[editor-bridge:$level] $text');
  }

  void _onSelectionChanged(BridgeMessage msg) {
    final sel = EditorSelection.fromJson(msg.payload);
    onSelectionChange?.call(sel);
  }

  /// Push a server-authoritative markdown into the editor (used after
  /// 409 conflict resolution to overwrite the local buffer with the
  /// freshly-fetched version).
  Future<void> setDoc(String markdown, {bool preserveSelection = true}) async {
    if (_lastEditorMarkdown == markdown) return;
    _hostRevision += 1;
    _lastEditorMarkdown = markdown;
    await _send?.call(
      setDocMessage(
        markdown: markdown,
        revision: _hostRevision,
        preserveSelection: preserveSelection,
      ),
    );
  }

  Future<void> setOptions({BridgeTheme? newTheme, bool? readOnly}) async {
    if (newTheme != null) theme = newTheme;
    await _send?.call(setOptionsMessage(theme: newTheme, readOnly: readOnly));
  }

  Future<void> command(String name, {Map<String, dynamic>? args}) async {
    await _send?.call(commandMessage(name, args: args));
  }

  /// S3 P1-6: replace the captured [from, to] span with `markdown` (parsed
  /// back into ProseMirror nodes by the editor). `expectedText` is the
  /// TOCTOU guard — the editor rejects the replace if the doc changed since
  /// capture. Fires the normal markdownUpdated → autosave path.
  Future<void> replaceSelection({
    required String markdown,
    required int from,
    required int to,
    required String expectedText,
  }) async {
    await command('replaceSelection', args: {
      'markdown': markdown,
      'from': from,
      'to': to,
      'expectedText': expectedText,
    });
  }
}

/// A snapshot of the editor selection pushed via `selectionChanged`.
class EditorSelection {
  const EditorSelection({
    required this.from,
    required this.to,
    required this.text,
    required this.empty,
    required this.coords,
  });

  final int from;
  final int to;
  final String text;
  final bool empty;
  final EditorSelectionCoords coords;

  factory EditorSelection.fromJson(Map<String, dynamic> j) {
    final c = (j['coords'] as Map?) ?? const <String, dynamic>{};
    return EditorSelection(
      from: (j['from'] as num?)?.toInt() ?? 0,
      to: (j['to'] as num?)?.toInt() ?? 0,
      text: (j['text'] as String?) ?? '',
      empty: (j['empty'] as bool?) ?? true,
      coords: EditorSelectionCoords.fromJson(c.cast<String, dynamic>()),
    );
  }
}

/// Viewport-relative rect of the selection head (PM `view.coordsAtPos(head)`).
/// The host adds the WebView's screen origin to get absolute overlay coords.
class EditorSelectionCoords {
  const EditorSelectionCoords({this.left = 0, this.top = 0, this.right = 0, this.bottom = 0});

  final double left;
  final double top;
  final double right;
  final double bottom;

  factory EditorSelectionCoords.fromJson(Map<String, dynamic> j) =>
      EditorSelectionCoords(
        left: (j['left'] as num?)?.toDouble() ?? 0,
        top: (j['top'] as num?)?.toDouble() ?? 0,
        right: (j['right'] as num?)?.toDouble() ?? 0,
        bottom: (j['bottom'] as num?)?.toDouble() ?? 0,
      );
}
