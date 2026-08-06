// 桌面 loopback 直连的 live 连接接线(M1 补齐 / M2 地基)。
//
// 把 BiuDaemonManager(spawn 的 biu serve,bridgeUrl=BIU_BRIDGE_URL)与
// CodeBridgeClient 串起来:daemon 起好且 bridgeUrl 就绪 → 建并连一个
// CodeBridgeClient(loopback 无 auth)。未登录/非桌面/daemon 未起 → null。
//
// bridge 是 loopback 工具,daemon 由 BiuDaemonManager 以 `biu serve`(无
// --auth-token)spawn,故 CodeBridgeClient 直接连 loopback 的 /v1/code/ws
// 端点(端口由 BIU_BRIDGE_URL 提供),无需 bearer。

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../data/agent_plane/biu_daemon_manager.dart';
import 'code_bridge_client.dart';

/// 当前 daemon 的 bridge base URL(loopback http,端口来自 BIU_BRIDGE_URL);未就绪为 null。
/// 只随 bridgeUrl 变化重建(select),避免 daemon 其它状态变动触发重连。
final codeBridgeUrlProvider = StreamProvider<String?>((ref) async* {
  final mgr = ref.watch(biuDaemonManagerProvider);
  if (mgr == null) {
    yield null;
    return;
  }
  yield mgr.state.bridgeUrl;
  yield* mgr.stream.map((s) => s.bridgeUrl);
});

/// live CodeBridgeClient —— bridgeUrl 就绪时建并连;变更时旧的 dispose。
/// 桌面 loopback 编码能力(git/fs/pty)走它。null = daemon 未就绪。
final codeBridgeClientProvider = Provider<CodeBridgeClient?>((ref) {
  final url = ref.watch(
    codeBridgeUrlProvider.select((async) => async.valueOrNull),
  );
  if (url == null || url.isEmpty) return null;
  final client = CodeBridgeClient(bridgeUrl: url);
  // connect 是幂等的;失败不抛(内部 best-effort)。
  client.connect();
  ref.onDispose(client.close);
  return client;
});
