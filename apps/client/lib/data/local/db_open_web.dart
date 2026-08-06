// Web opener — drift WASM with persistent OPFS / IndexedDB storage.
//
// `WasmDatabase.open()` automatically picks the best storage the
// browser supports:
//
//   * OPFS (Origin Private File System) — dedicated worker, fully
//     persistent. Chrome 109+ / Safari 17+ / Firefox 111+.
//   * IndexedDB — fallback when OPFS is unavailable.
//   * In-memory — fallback when both are unavailable (older browsers,
//     extension contexts).
//
// Artifacts shipped under apps/client/web/:
//
//   sqlite3.wasm           — copied from drift's bundled artifact
//                            (extension/devtools/build/sqlite3.wasm)
//   drift_worker.dart.js   — compiled from drift's web/drift_worker.dart
//                            via `dart compile js -O2 …` in build.sh
//
// Both URIs are relative to the deployed page; the static file server
// serves them alongside main.dart.js.

import 'package:drift/drift.dart';
import 'package:drift/wasm.dart';

const _databaseName = 'biumind';

QueryExecutor openDatabase() {
  return LazyDatabase(() async {
    final result = await WasmDatabase.open(
      databaseName: _databaseName,
      sqlite3Uri: Uri.parse('sqlite3.wasm'),
      driftWorkerUri: Uri.parse('drift_worker.dart.js'),
    );
    return result.resolvedExecutor;
  });
}

QueryExecutor memoryExecutor() {
  return LazyDatabase(() async {
    final result = await WasmDatabase.open(
      databaseName: '$_databaseName-memory',
      sqlite3Uri: Uri.parse('sqlite3.wasm'),
      driftWorkerUri: Uri.parse('drift_worker.dart.js'),
    );
    return result.resolvedExecutor;
  });
}
