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
  });

  final bool wikilink;
  final bool mermaid;

  Map<String, dynamic> toJson() => {
        'wikilink': wikilink,
        'mermaid': mermaid,
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
}) {
  return BridgeMessage(
    type: 'init',
    payload: {
      'markdown': markdown,
      'theme': theme.wire,
      'readOnly': readOnly,
      'locale': locale,
      'features': features.toJson(),
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

BridgeMessage setOptionsMessage({BridgeTheme? theme, bool? readOnly}) {
  return BridgeMessage(
    type: 'setOptions',
    payload: {
      if (theme != null) 'theme': theme.wire,
      'readOnly': ?readOnly,
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

class WikilinkSuggestion {
  const WikilinkSuggestion({required this.slug, required this.title});

  final String slug;
  final String title;

  Map<String, dynamic> toJson() => {'slug': slug, 'title': title};
}
