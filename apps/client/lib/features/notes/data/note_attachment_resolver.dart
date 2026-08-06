// NoteAttachmentResolver — biu-file:// ↔ 可匿名 GET 的 presigned URL 双向重写。
//
// 背景：WebView 里的 Milkdown 加载不了 `biu-file://` 自定义 scheme，img
// 标签也带不了 Authorization 头。所以：
//   * 进编辑器前（setDoc / initialMarkdown）：把正文里的
//     `biu-file://<uuid>` 换成 brain `POST /v1/files/{id}/presign-get`
//     给的临时 URL，编辑器直接 <img src> 就能渲染。
//   * 出编辑器（onMarkdownChanged 落库前）：按映射把临时 URL 换回
//     `biu-file://<uuid>`，保证本地库 / 同步出去的永远是规范 URI。
//
// 临时 URL 里不一定含 uuid（MinIO object_key 是内部布局），映射必须
// 正反双向显式维护，不能靠正则从 URL 反推。
//
// URL 15 分钟过期（brain presignGetTTL）：打开笔记 / 远端覆盖时刷新；
// 编辑过程中过期导致图片裂开本期可接受，重开笔记即恢复。
//
// 实例按笔记编辑器视图生命周期持有一份（note_editor_view initState
// 创建；桌面三栏切笔记复用同一视图，映射随会话累积、不跨视图共享）。
// 换取 presign 时凭证在调用当下读取，token 轮换不影响已建立的映射。
library;

import 'dart:convert';

import 'package:http/http.dart' as http;

/// 正文里引用附件的规范形式：图片 `![name](biu-file://<uuid>)`，
/// 非图片附件是普通链接 `[name](biu-file://<uuid>)`。匹配只看
/// `biu-file://<uuid>` 本身，不区分图片 / 链接语境。
final RegExp biuFilePattern = RegExp(r'biu-file://([0-9a-fA-F-]{36})');

/// 换取单个 fileId 的可匿名 GET 临时 URL。失败抛异常。
typedef PresignGetFetcher = Future<String> Function(String fileId);

/// 调 brain `POST /v1/files/{id}/presign-get` 拿临时下载 URL。
/// 走 model-relay（endpoint 与 FilesClient 同源），Bearer 鉴权。
Future<String> presignGetFileUrl(
  Uri endpoint,
  String bearerToken,
  String fileId,
) async {
  final url = endpoint.replace(path: '/v1/files/$fileId/presign-get');
  final resp = await http.post(
    url,
    headers: {'Authorization': 'Bearer $bearerToken'},
  );
  if (resp.statusCode < 200 || resp.statusCode >= 300) {
    throw Exception('presign-get $fileId: ${resp.statusCode}');
  }
  final j = jsonDecode(resp.body) as Map<String, dynamic>;
  final signed = j['url']?.toString() ?? '';
  if (signed.isEmpty) {
    throw Exception('presign-get $fileId: empty url');
  }
  return signed;
}

class NoteAttachmentResolver {
  NoteAttachmentResolver({required PresignGetFetcher presignGet})
      : _presignGet = presignGet;

  final PresignGetFetcher _presignGet;

  /// fileId → 当前临时 URL。
  final Map<String, String> _urlByFileId = {};

  /// 临时 URL → fileId（URL 未必含 uuid，必须显式反查）。
  final Map<String, String> _fileIdByUrl = {};

  /// 提取正文里引用的全部附件 fileId（去重，保持出现顺序）。
  List<String> extractFileIds(String markdown) {
    final seen = <String>{};
    final out = <String>[];
    for (final m in biuFilePattern.allMatches(markdown)) {
      final id = m.group(1)!;
      if (seen.add(id)) out.add(id);
    }
    return out;
  }

  /// 登记一条 fileId ↔ 临时 URL 映射；同一 fileId 重新登记时旧 URL
  /// 的反向映射作废（旧 URL 已过期，不应再换回）。
  void registerMapping(String fileId, String url) {
    final old = _urlByFileId[fileId];
    if (old != null) _fileIdByUrl.remove(old);
    _urlByFileId[fileId] = url;
    _fileIdByUrl[url] = fileId;
  }

  /// 换取并登记单个 fileId 的临时 URL（新上传的图片插入编辑器前调用）。
  Future<String> resolveOne(String fileId) async {
    final url = await _presignGet(fileId);
    registerMapping(fileId, url);
    return url;
  }

  /// 进编辑器：把 `biu-file://<uuid>` 全部换成临时可渲染 URL。
  /// 并行换取（串行 await 会让 N 个附件的首屏付 N 个 RTT）；单个
  /// 文件换取失败时保留原 biu-file:// 形式（编辑器里裂开但正文
  /// 不丢，落库仍是规范 URI）。
  Future<String> resolveForEditor(String markdown) async {
    final ids = extractFileIds(markdown);
    if (ids.isEmpty) return markdown;
    // 并行发出全部 presign 请求，全部落定后按 id 顺序逐个替换 —
    // 与串行版结果等价（各 id 的替换互不影响）。
    final urls = await Future.wait([
      for (final id in ids) _tryResolveOne(id),
    ]);
    var out = markdown;
    for (var i = 0; i < ids.length; i++) {
      final url = urls[i];
      if (url != null) out = out.replaceAll('biu-file://${ids[i]}', url);
    }
    return out;
  }

  /// resolveOne 的失败兜底版：换取失败返回 null（正文保留原 URI）。
  Future<String?> _tryResolveOne(String fileId) async {
    try {
      return await resolveOne(fileId);
    } on Exception {
      return null;
    }
  }

  /// 出编辑器（落库 / 同步前）：把已知临时 URL 换回
  /// `biu-file://<uuid>`。未登记过的 URL 原样保留。
  String toCanonical(String markdown) {
    if (_fileIdByUrl.isEmpty) return markdown;
    var out = markdown;
    _fileIdByUrl.forEach((url, fileId) {
      out = out.replaceAll(url, 'biu-file://$fileId');
    });
    return out;
  }
}
