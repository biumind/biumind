/// mirror_download — 原生端落盘实现（conditional import 的默认分支）。
///
/// 写应用文档目录，返回完整路径供结果页展示 / 复制。
library;

import 'dart:io';

import 'package:path/path.dart' as p;
import 'package:path_provider/path_provider.dart';

/// 把 zip 字节写到应用文档目录，返回完整输出路径。
Future<String> saveMirrorZip({
  required String filename,
  required List<int> zipBytes,
}) async {
  final dir = await getApplicationDocumentsDirectory();
  final outPath = p.join(dir.path, filename);
  await File(outPath).writeAsBytes(zipBytes);
  return outPath;
}
