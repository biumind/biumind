/// mirror_download — Web 端实现（dart.library.html 分支）。
///
/// 无法写文件系统，改为 Blob + 隐藏 anchor click 触发浏览器下载；
/// 返回文件名供结果页展示（下载目录由浏览器决定）。
library;

import 'dart:js_interop';
import 'dart:typed_data';

import 'package:web/web.dart' as web;

/// 把 zip 字节经浏览器下载交给用户，返回下载文件名。
Future<String> saveMirrorZip({
  required String filename,
  required List<int> zipBytes,
}) async {
  final blob = web.Blob(
    [Uint8List.fromList(zipBytes).toJS].toJS,
    web.BlobPropertyBag(type: 'application/zip'),
  );
  final url = web.URL.createObjectURL(blob);
  final anchor = web.document.createElement('a') as web.HTMLAnchorElement
    ..href = url
    ..download = filename
    ..style.display = 'none';
  web.document.body?.append(anchor);
  anchor.click();
  anchor.remove();
  web.URL.revokeObjectURL(url);
  return filename;
}
