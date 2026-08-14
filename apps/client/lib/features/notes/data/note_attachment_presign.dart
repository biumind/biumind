// Note attachment presign — 笔记附件的临时下载 URL 换取。
//
// 正文从插入、编辑、落库到同步全程只存 `biu-file://<uuid>` 规范 URI；
// presigned URL 只是 WebView DOM 层的渲染耗材：编辑器（Milkdown image
// block/inline 的 proxyDomURL）在写 <img src> 前经 bridge `presignGet`
// 消息向 host 换 URL，host 侧走这里的 presignGetFileUrl。URL 15 分钟
// 过期（brain presignGetTTL），编辑器侧缓存 14 分钟，过期 403 时经
// onImageLoadError 强制重换。
//
// 凭证在调用当下读取（note_editor_view 的闭包），token 轮换无影响。
library;

import 'dart:convert';

import 'package:http/http.dart' as http;

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
