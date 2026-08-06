// Native (non-web) opener — sqlite via dart:ffi.
//
// Imported transparently by db.dart on every platform that has
// dart.library.io but not dart.library.html.

import 'dart:io';

import 'package:drift/drift.dart';
import 'package:drift/native.dart';
import 'package:path/path.dart' as p;
import 'package:path_provider/path_provider.dart';

QueryExecutor openDatabase() {
  return LazyDatabase(() async {
    final dir = await getApplicationSupportDirectory();
    final file = File(p.join(dir.path, 'biumind.sqlite'));
    return NativeDatabase(file);
  });
}

QueryExecutor memoryExecutor() => NativeDatabase.memory();
