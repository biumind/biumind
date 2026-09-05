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
import 'dart:convert' show base64Decode;

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart' show Clipboard, ClipboardData;

import '../platform/rich_clipboard.dart';
import 'editor_bridge_protocol.dart';

typedef EditorSend = Future<void> Function(BridgeMessage message);

typedef WikilinkResolver = Future<List<WikilinkSuggestion>> Function(
  String prefix,
);

/// 渲染时给 `biu-file://<uuid>` 图片换 presigned GET URL（15 分钟 TTL，
/// 编辑器侧缓存 + 过期重换）。返回空串表示换取失败。
typedef PresignGetResolver = Future<String> Function(String fileId);

/// 粘贴/拖入图片上传（编辑器 onUpload 链路）：File 已读成 bytes，
/// host 走 presign 直传后返回 `biu-file://<uuid>` 规范 URI；
/// 失败返回 null（编辑器侧 onUpload 抛错，图片节点不插入）。
typedef ImageFileUploadResolver = Future<String?> Function(
  String name,
  String mime,
  Uint8List bytes,
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

  /// 编辑器 UI 语言（默认 zh-Hans；宿主页可传参，setLocale 运行时切换）。
  String locale;
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

  /// 自绘右键菜单「剪切/复制」落到系统剪贴板。缺省实现：payload 带 html
  /// 且平台支持时写双格式（macOS NSPasteboard），否则写 Flutter Clipboard
  /// 纯文本；宿主页需要拦截（如埋点）时可覆盖。
  Future<void> Function(String text, String? html)? onClipboardWrite;

  /// 双格式剪贴板写入（macOS method channel）。返回 false = 平台不支持，
  /// 回退纯文本。测试可注入 mock；默认走 lib/core/platform/rich_clipboard。
  Future<bool> Function(String text, String html) richClipboardWriter =
      writeRichClipboard;

  /// 图片剪贴板写入（macOS method channel，单图复制）。返回 false = 平台
  /// 不支持 / 通道失败，回退双格式/纯文本。测试可注入 mock。
  Future<bool> Function(String text, String? html, Uint8List pngBytes)
      richClipboardImageWriter = writeImageClipboard;

  /// 右键菜单 AI 动作（协议 P1 预留；菜单组 P2 渲染后才有真实流量）。
  void Function(EditorAiAction action)? onAiAction;

  /// 图片菜单「替换图片…」：host 走既有上传链路（选图 → presign 直传），
  /// 返回 `biu-file://<uuid>` 规范 URI；取消/失败返回 null（编辑器不动节点）。
  /// 仅 notes 宿主页接线（wiki 无附件链路，features.imageUpload 保持 false）。
  Future<String?> Function()? resolveImageUpload;

  /// 粘贴/拖入图片上传（编辑器 onUpload 链路）：File 已读成 bytes 发来，
  /// host 走与「插入图片」同一条 presign 直传链路。仅 notes 宿主页接线。
  ImageFileUploadResolver? resolveImageFileUpload;

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
      case 'clipboardWrite':
        await _onClipboardWrite(msg);
      case 'clipboardRead':
        await _onClipboardRead(msg);
      case 'aiAction':
        _onAiAction(msg);
      case 'imageUpload':
        await _onImageUpload(msg);
      case 'imageFileUpload':
        await _onImageFileUpload(msg);
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

  /// 自绘右键菜单「剪切/复制」：execCommand 在 WKWebView 常失败，编辑器
  /// 把文本经 bridge 送过来，这里落系统剪贴板。payload 带 html（P2 双格式）
  /// 时优先写 text+html（macOS NSPasteboard），不支持/失败回退纯文本。
  /// payload 带 imageBase64（单图复制的 PNG 本体）时优先写图片格式，
  /// 平台无能力时逐级回退 html → 纯文本。
  Future<void> _onClipboardWrite(BridgeMessage msg) async {
    final text = msg.payload['text'];
    if (text is! String) return;
    final html = msg.payload['html'];
    final htmlStr = html is String && html.isNotEmpty ? html : null;
    final handler = onClipboardWrite;
    if (handler != null) {
      await handler(text, htmlStr);
      return;
    }
    final imageBase64 = msg.payload['imageBase64'];
    final imageMime = msg.payload['imageMime'];
    if (imageBase64 is String &&
        imageBase64.isNotEmpty &&
        imageMime == 'image/png') {
      try {
        if (await richClipboardImageWriter(
            text, htmlStr, base64Decode(imageBase64))) {
          return;
        }
      } catch (_) {
        // base64 解码失败 / 通道异常 —— 继续回退链路
      }
    }
    if (htmlStr != null) {
      if (await richClipboardWriter(text, htmlStr)) return;
    }
    await Clipboard.setData(ClipboardData(text: text));
  }

  /// 右键菜单「粘贴」前的剪贴板读取。空/读不到回 null —— 编辑器侧置灰粘贴项。
  Future<void> _onClipboardRead(BridgeMessage msg) async {
    final id = msg.id;
    if (id == null) return;
    String? text;
    try {
      final data = await Clipboard.getData(Clipboard.kTextPlain);
      final value = data?.text;
      if (value != null && value.isNotEmpty) text = value;
    } catch (_) {
      text = null;
    }
    await _send?.call(clipboardReadReplyMessage(id: id, text: text));
  }

  /// 协议 P1 预留：菜单组 P2 才渲染，先只记录日志；宿主页接 selection-edit
  /// overlay 时注入 onAiAction。
  void _onAiAction(BridgeMessage msg) {
    final action = EditorAiAction.fromJson(msg.payload);
    final handler = onAiAction;
    if (handler != null) {
      handler(action);
      return;
    }
    debugPrint(
      '[editor-bridge] aiAction ${action.action} '
      '[${action.from}, ${action.to}) 未接线（P2）',
    );
  }

  /// 图片菜单「替换图片…」（P2）：host 走既有上传链路换规范 URI。
  /// resolver 未接线/取消/失败回 null —— 编辑器侧不改动图片节点。
  Future<void> _onImageUpload(BridgeMessage msg) async {
    final id = msg.id;
    if (id == null) return;
    String? uri;
    final resolver = resolveImageUpload;
    if (resolver != null) {
      try {
        uri = await resolver();
      } catch (_) {
        uri = null;
      }
    }
    await _send?.call(imageUploadReplyMessage(id: id, uri: uri));
  }

  /// 粘贴/拖入图片上传（onUpload 链路）：payload 带 base64 文件内容，
  /// host 走 presign 直传。resolver 未接线/失败回 null —— 编辑器侧
  /// onUpload 抛错，图片节点不插入（绝不回落 blob URL）。
  Future<void> _onImageFileUpload(BridgeMessage msg) async {
    final id = msg.id;
    if (id == null) return;
    String? uri;
    final resolver = resolveImageFileUpload;
    final name = msg.payload['name'];
    final mime = msg.payload['mime'];
    final dataBase64 = msg.payload['dataBase64'];
    if (resolver != null &&
        name is String &&
        mime is String &&
        dataBase64 is String) {
      try {
        final bytes = base64Decode(dataBase64);
        uri = await resolver(name, mime, bytes);
      } catch (_) {
        uri = null;
      }
    }
    await _send?.call(imageFileUploadReplyMessage(id: id, uri: uri));
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

  /// 运行时切换编辑器 UI 语言（自绘右键菜单等现构建文案即刻生效；
  /// crepe 自身文案维持 init 时的语言，见设计文档 §7）。
  Future<void> setLocale(String newLocale) async {
    if (newLocale == locale) return;
    locale = newLocale;
    await _send?.call(setOptionsMessage(locale: newLocale));
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
    this.before = '',
    this.after = '',
    required this.empty,
    required this.coords,
  });

  final int from;
  final int to;
  final String text;

  /// 选区前/后各约 1200 字符纯文本上下文（编辑器 doc.textBetween 窗口），
  /// 原样传给 selection-edit 接口做 BEFORE/AFTER 段。旧版 bundle 不带，
  /// 缺省空串（服务端按无上下文处理）。
  final String before;
  final String after;
  final bool empty;
  final EditorSelectionCoords coords;

  factory EditorSelection.fromJson(Map<String, dynamic> j) {
    final c = (j['coords'] as Map?) ?? const <String, dynamic>{};
    return EditorSelection(
      from: (j['from'] as num?)?.toInt() ?? 0,
      to: (j['to'] as num?)?.toInt() ?? 0,
      text: (j['text'] as String?) ?? '',
      before: (j['before'] as String?) ?? '',
      after: (j['after'] as String?) ?? '',
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
