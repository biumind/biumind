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

/// 渲染时给 `biu-file://<uuid>` 图片换 presigned GET URL（15 分钟 TTL，
/// 编辑器侧缓存 + 过期重换）。返回空串表示换取失败。
typedef PresignGetResolver = Future<String> Function(String fileId);

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

  /// Called by the controller when the editor renders a `biu-file://`
  /// image and needs a presigned URL for the `<img>` tag.
  PresignGetResolver? resolvePresignGet;

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
      case 'presignGet':
        await _onPresignGet(msg);
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
        epoch: _hostRevision,
      ),
    );
    if (!_readyCompleter.isCompleted) _readyCompleter.complete();
  }

  void _onDocChanged(BridgeMessage msg) {
    final revision = msg.payload['revision'];
    final markdown = msg.payload['markdown'];
    if (revision is! num || markdown is! String) return;
    // 纪元校验（跨笔记防串内容）：编辑器复用同一个 webview，切换笔记
    // 瞬间上一篇的 docChanged 可能还在防抖队列/在途 postMessage 里。
    // setDoc 即新纪元（_hostRevision 递增随 setDoc 下发），迟到的旧
    // 纪元变更一律丢弃 —— 否则会存进新笔记。无 epoch 字段 = 旧版编辑
    // 器 bundle，退回 revision 守卫。
    final epoch = msg.payload['epoch'];
    if (epoch is num) {
      if (epoch.toInt() != _hostRevision) return;
    } else if (revision.toInt() < _hostRevision) {
      // Out-of-order guard: drop anything older than the last host write.
      return;
    }
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

  /// 编辑器渲染 biu-file:// 图片前来换临时 URL。resolver 未接线或换取
  /// 失败时回空串 —— 编辑器侧按失败处理（图片裂开，正文不受影响）。
  Future<void> _onPresignGet(BridgeMessage msg) async {
    final id = msg.id;
    final fileId = msg.payload['fileId'];
    final resolver = resolvePresignGet;
    if (id == null || fileId is! String) return;
    var url = '';
    if (resolver != null) {
      try {
        url = await resolver(fileId);
      } catch (_) {
        // 含 Error（note 侧未连接 hub 时抛 StateError）——一律回空串。
        url = '';
      }
    }
    await _send?.call(presignGetReplyMessage(id: id, url: url));
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
    // 未 attach 时不推进纪元：消息本来也发不出去，推进了只会让编辑器
    // （纪元还停留在旧值）后续 docChanged 全被 epoch 校验误杀。
    final send = _send;
    if (send == null) return;
    _hostRevision += 1;
    _lastEditorMarkdown = markdown;
    await send(
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
