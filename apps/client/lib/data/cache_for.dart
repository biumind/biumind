// cacheFor — autoDispose provider 的短期 keepAlive 工具。
//
// 给 FutureProvider.autoDispose / StreamProvider.autoDispose 一个短缓存窗口:
// 窗口内即便没有 watcher 也不 dispose, 避免页面来回切换时重拉 (loading 闪)。
// 到期自动释放。用户主动 ref.invalidate / ref.refresh 仍照常强制刷新。
//
// 用法:
//   final fooProvider = FutureProvider.autoDispose<X>((ref) async {
//     ref.cacheFor(const Duration(minutes: 2));
//     return ...;
//   });
//
// 适合数据低频变动 + 用户可能短时间反复进入的列表 (BYOK keys / 套餐 / 余额)。
// 不适合需要每次进入都拿最新的数据 (任务进度等) —— 那些保留纯 autoDispose。

import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

extension CacheForRef on Ref {
  /// 在 [duration] 内保持本 provider alive (即便无 watcher)。到期释放。
  void cacheFor(Duration duration) {
    final link = keepAlive();
    final timer = Timer(duration, link.close);
    // provider 先 dispose 时(容器销毁 / invalidate)取消挂起计时器 ——
    // 不取消的话 widget test teardown 会报 "A Timer is still pending"。
    onDispose(timer.cancel);
  }
}
