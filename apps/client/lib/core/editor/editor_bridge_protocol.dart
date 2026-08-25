// Bridge protocol v1 — Dart side. Mirrors
// apps/client/editor-web/src/bridge/protocol.ts. Both definitions MUST
// evolve in lockstep; protocol changes that drop or rename fields require
// bumping the version constant.

const int kBridgeProtocolVersion = 1;

enum BridgeTheme { light, dark }

extension BridgeThemeWire on BridgeTheme {
  String get wire => name;
  static BridgeTheme parse(String value) {
    return value == 'dark' ? BridgeTheme.dark : BridgeTheme.light;
  }
}

class BridgeFeatures {
  const BridgeFeatures({
    this.wikilink = true,
    this.mermaid = true,
    this.contextMenu = 'custom',
    this.aiActions = false,
    this.imageUpload = false,
    this.platform,
  });

  final bool wikilink;
  final bool mermaid;

  /// 右键菜单载体：custom = bundle 内自绘 HTML 菜单（默认，桌面/Web）；
  /// native = 平台系统菜单（iOS/Android 移动端，长按 callout 是强平台习惯）。
  final String contextMenu;

  /// host 已接 AI 动作（选区询问/编辑 overlay）；false 时菜单不渲染 AI 组。
  final bool aiActions;

  /// host 已接图片上传链路（选图 → presign 直传，notes 专属能力）；
  /// false 时图片菜单不渲染「替换图片…」。
  final bool imageUpload;

  /// 平台标记（M1 移动端）：'ios' | 'android' | 'macos' | 'web'。
  /// bundle 据此在 `<html data-platform>` 标注，分流 CSS（iOS callout
  /// 抑制）、入场动画与移动端裁剪；null = 非移动端（行为不变）。
  final String? platform;

  Map<String, dynamic> toJson() => {
        'wikilink': wikilink,
        'mermaid': mermaid,
        'contextMenu': contextMenu,
        'aiActions': aiActions,
        'imageUpload': imageUpload,
        'platform': ?platform,
      };
}

class BridgeMessage {
  const BridgeMessage({
    required this.type,
    required this.payload,
    this.id,
    this.v = kBridgeProtocolVersion,
  });

  factory BridgeMessage.fromJson(Map<String, dynamic> json) {
    final type = json['type'];
    final v = json['v'];
    final payload = json['payload'];
    if (type is! String || v is! num || payload is! Map) {
      throw const FormatException('bridge message: missing required fields');
    }
    final id = json['id'];
    return BridgeMessage(
      type: type,
      v: v.toInt(),
      id: id is String ? id : null,
      payload: Map<String, dynamic>.from(payload),
    );
  }

  final String type;
  final int v;
  final String? id;
  final Map<String, dynamic> payload;

  Map<String, dynamic> toJson() {
    final out = <String, dynamic>{'type': type, 'v': v, 'payload': payload};
    if (id != null) out['id'] = id;
    return out;
  }
}

// Outbound (Host → Editor) constructors.

BridgeMessage initMessage({
  required String markdown,
  required BridgeTheme theme,
  required bool readOnly,
  required String locale,
  BridgeFeatures features = const BridgeFeatures(),
  int epoch = 0,
}) {
  return BridgeMessage(
    type: 'init',
    payload: {
      'markdown': markdown,
      'theme': theme.wire,
      'readOnly': readOnly,
      'locale': locale,
      'features': features.toJson(),
      // 文档纪元初值：编辑器（重）初始化后与 host 的 _hostRevision 对齐，
      // 否则后续 docChanged 会被 epoch 校验误杀。
      'epoch': epoch,
    },
  );
}

BridgeMessage setDocMessage({
  required String markdown,
  required int revision,
  bool preserveSelection = true,
}) {
  return BridgeMessage(
    type: 'setDoc',
    payload: {
      'markdown': markdown,
      'revision': revision,
      'preserveSelection': preserveSelection,
    },
  );
}

BridgeMessage setOptionsMessage({BridgeTheme? theme, bool? readOnly, String? locale}) {
  return BridgeMessage(
    type: 'setOptions',
    payload: {
      if (theme != null) 'theme': theme.wire,
      'readOnly': ?readOnly,
      // 运行时切换 UI 语言：菜单等现构建的文案即刻生效（crepe 文案维持 init）。
      'locale': ?locale,
    },
  );
}

BridgeMessage commandMessage(String name, {Map<String, dynamic>? args}) {
  return BridgeMessage(
    type: 'command',
    payload: {'name': name, 'args': ?args},
  );
}

BridgeMessage wikilinkQueryReplyMessage({
  required String id,
  required List<WikilinkSuggestion> items,
}) {
  return BridgeMessage(
    type: 'wikilinkQuery.reply',
    id: id,
    payload: {'items': items.map((e) => e.toJson()).toList()},
  );
}

/// presignGet.reply — 编辑器渲染 `biu-file://<uuid>` 图片前向 host 换
/// 临时 URL 的应答。`url` 为空串 = 换取失败（图片裂开，编辑器不崩）。
BridgeMessage presignGetReplyMessage({
  required String id,
  required String url,
}) {
  return BridgeMessage(
    type: 'presignGet.reply',
    id: id,
    payload: {'url': url},
  );
}

/// clipboardRead.reply — 编辑器读系统剪贴板的应答（自绘右键菜单「粘贴」）。
/// `text` 为 null = 剪贴板为空 / 读取失败，编辑器侧把粘贴项置灰。
BridgeMessage clipboardReadReplyMessage({
  required String id,
  required String? text,
}) {
  return BridgeMessage(
    type: 'clipboardRead.reply',
    id: id,
    payload: {'text': text},
  );
}

/// imageUpload.reply — 图片菜单「替换图片…」的应答。host 走既有上传链路
/// （选图 → presign 直传）；`uri` = `biu-file://<uuid>` 规范 URI，
/// null = 用户取消 / 上传失败（编辑器不改动图片节点）。
BridgeMessage imageUploadReplyMessage({
  required String id,
  required String? uri,
}) {
  return BridgeMessage(
    type: 'imageUpload.reply',
    id: id,
    payload: {'uri': uri},
  );
}

/// aiAction — 右键菜单 AI 动作（协议 P1 预留；菜单组 P2 才渲染）。
class EditorAiAction {
  const EditorAiAction({
    required this.action,
    required this.from,
    required this.to,
    required this.text,
  });

  /// 'ask' | 'edit'
  final String action;
  final int from;
  final int to;
  final String text;

  factory EditorAiAction.fromJson(Map<String, dynamic> j) => EditorAiAction(
        action: (j['action'] as String?) ?? 'ask',
        from: (j['from'] as num?)?.toInt() ?? 0,
        to: (j['to'] as num?)?.toInt() ?? 0,
        text: (j['text'] as String?) ?? '',
      );
}

class WikilinkSuggestion {
  const WikilinkSuggestion({required this.slug, required this.title});

  final String slug;
  final String title;

  Map<String, dynamic> toJson() => {'slug': slug, 'title': title};
}
