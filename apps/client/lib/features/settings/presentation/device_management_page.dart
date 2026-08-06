// 设备管理页（Runtime v3 R6.1 / D5）：批准新设备配对码 + 列出/吊销已配对设备。
//
// 远程设备控制的安全面板——家里 Mac 跑 `biu pair` 显示配对码，用户在此(已登录
// 设备)输入批准、绑定到自己账号；可随时吊销某台设备的 device token。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/layout/phone_nav.dart';
import '../../../data/agent_plane/devices_client.dart';
import '../../../data/api/_http_helpers.dart' show ApiError;
import '../../../services/auth_service.dart';

/// DevicesClient provider —— 复用 hubCredentialsProvider 的 endpoint + token。
/// 单 origin: brain 的 device 接口由 site nginx 按路径反代, 不换端口。
final devicesClientProvider = Provider<DevicesClient?>((ref) {
  final creds = ref.watch(hubCredentialsProvider);
  if (creds == null) return null;
  String strip(String s) => s.endsWith('/') ? s.substring(0, s.length - 1) : s;
  return DevicesClient(
    baseUrl: strip(creds.endpoint.toString()),
    tokenProvider: () async => creds.bearerToken,
  );
});

class DeviceManagementPage extends ConsumerStatefulWidget {
  const DeviceManagementPage({super.key});

  @override
  ConsumerState<DeviceManagementPage> createState() =>
      _DeviceManagementPageState();
}

class _DeviceManagementPageState extends ConsumerState<DeviceManagementPage> {
  Future<List<PairedDevice>>? _future;

  void _reload() {
    final client = ref.read(devicesClientProvider);
    setState(() {
      _future = client?.listDevices() ?? Future.value(const []);
    });
  }

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _reload());
  }

  Future<void> _approveDialog() async {
    final client = ref.read(devicesClientProvider);
    if (client == null) return;
    final ctrl = TextEditingController();
    final code = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('批准新设备'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('在目标设备运行 `biu pair`，把显示的 8 位配对码输入这里。'),
            const SizedBox(height: 12),
            TextField(
              controller: ctrl,
              keyboardType: TextInputType.number,
              maxLength: 8,
              decoration: const InputDecoration(
                labelText: '配对码', hintText: '12345678', counterText: '',
              ),
            ),
          ],
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('取消')),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, ctrl.text.trim()),
            child: const Text('批准'),
          ),
        ],
      ),
    );
    if (code == null || code.isEmpty || !mounted) return;
    try {
      final machine = await client.approvePairing(code);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('✅ 已批准设备「$machine」。该设备稍后会自动拿到 token。')),
      );
      _reload();
    } on ApiError catch (e) {
      if (!mounted) return;
      final msg = e.status == 404 ? '配对码无效或已过期' : '批准失败（${e.status}）';
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(msg)));
    }
  }

  Future<void> _setPolicy(PairedDevice d, String policy) async {
    final client = ref.read(devicesClientProvider);
    if (client == null || policy == d.toolPolicy) return;
    try {
      await client.setDevicePolicy(d.deviceId, policy);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('已把「${d.name}」的工具权限设为 $policy')),
      );
      _reload();
    } on ApiError catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('修改权限失败（${e.status}）')));
    }
  }

  Future<void> _revoke(PairedDevice d) async {
    final client = ref.read(devicesClientProvider);
    if (client == null) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('吊销「${d.name}」？'),
        content: const Text('吊销后该设备的 device token 立即失效，需重新 `biu pair` 配对。'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('取消')),
          FilledButton(
            style: FilledButton.styleFrom(
                backgroundColor: Theme.of(ctx).colorScheme.error),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('吊销'),
          ),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    try {
      await client.revokeDevice(d.deviceId);
      _reload();
    } on ApiError catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('吊销失败（${e.status}）')));
    }
  }

  @override
  Widget build(BuildContext context) {
    final cs = Theme.of(context).colorScheme;
    return Scaffold(
      appBar: AppBar(
        // 子页头左位 ← (手机形态; 桌面必须为 null — AppBar 对非空 leading
        // 恒占 56px, shrink 也会让标题右移, §3.3)。
        leading: phoneBackLeading(context),
        title: const Text('我的设备'),
        actions: [
          IconButton(
            tooltip: '刷新',
            onPressed: _reload,
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _approveDialog,
        icon: const Icon(Icons.add_link),
        label: const Text('批准新设备'),
      ),
      body: FutureBuilder<List<PairedDevice>>(
        future: _future,
        builder: (context, snap) {
          if (snap.connectionState == ConnectionState.waiting) {
            return const Center(child: CircularProgressIndicator());
          }
          if (snap.hasError) {
            return Center(child: Text('加载失败：${snap.error}'));
          }
          final devices = snap.data ?? const <PairedDevice>[];
          if (devices.isEmpty) {
            return Center(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Text(
                  '还没有配对的设备。\n在另一台机器运行 `biu pair`，再点右下角「批准新设备」输入配对码。',
                  textAlign: TextAlign.center,
                  style: TextStyle(color: cs.onSurfaceVariant),
                ),
              ),
            );
          }
          return ListView.separated(
            padding: const EdgeInsets.symmetric(vertical: 8),
            itemCount: devices.length,
            separatorBuilder: (_, _) => const Divider(height: 1),
            itemBuilder: (_, i) {
              final d = devices[i];
              final sub = StringBuffer('biu_dev_${d.prefix}…');
              // R6.4：在线态文案。在线 → 在线；离线 → 离线 + 最后活跃。
              if (!d.revoked) {
                if (d.online) {
                  sub.write(' · 在线');
                } else if (d.lastSeen != null) {
                  sub.write(' · 离线 · 最后 ${_fmt(d.lastSeen!)}');
                } else {
                  sub.write(' · 从未上线');
                }
              }
              return ListTile(
                leading: _DeviceLeading(
                  revoked: d.revoked,
                  online: d.online,
                  cs: cs,
                ),
                title: Text(d.name +
                    (d.revoked ? '（已吊销）' : '')),
                subtitle: Text(sub.toString()),
                trailing: d.revoked
                    ? null
                    : Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          _PolicyDropdown(
                            value: d.toolPolicy,
                            onChanged: (p) => _setPolicy(d, p),
                          ),
                          const SizedBox(width: 8),
                          TextButton(
                            onPressed: () => _revoke(d),
                            child: Text('吊销',
                                style: TextStyle(color: cs.error)),
                          ),
                        ],
                      ),
              );
            },
          );
        },
      ),
    );
  }

  static String _fmt(DateTime t) {
    final l = t.toLocal();
    return '${l.year}-${l.month.toString().padLeft(2, '0')}-${l.day.toString().padLeft(2, '0')} '
        '${l.hour.toString().padLeft(2, '0')}:${l.minute.toString().padLeft(2, '0')}';
  }
}

/// 设备图标 + 在线态小圆点（R6.4）。吊销优先显示警示图标；否则设备图标右下角
/// 叠一个在线绿点 / 离线灰点。
class _DeviceLeading extends StatelessWidget {
  const _DeviceLeading({
    required this.revoked,
    required this.online,
    required this.cs,
  });
  final bool revoked;
  final bool online;
  final ColorScheme cs;

  @override
  Widget build(BuildContext context) {
    if (revoked) {
      return Icon(Icons.gpp_bad_outlined, color: cs.error);
    }
    return Stack(
      clipBehavior: Clip.none,
      children: [
        Icon(Icons.devices, color: cs.primary),
        Positioned(
          right: -2,
          bottom: -2,
          child: Container(
            width: 10,
            height: 10,
            decoration: BoxDecoration(
              color: online ? Colors.green : cs.outline,
              shape: BoxShape.circle,
              border: Border.all(color: cs.surface, width: 1.5),
            ),
          ),
        ),
      ],
    );
  }
}

/// 工具权限 preset 下拉（R6.3 / D7）。中文标签映射 readonly/workspace-write/full。
class _PolicyDropdown extends StatelessWidget {
  const _PolicyDropdown({required this.value, required this.onChanged});
  final String value;
  final ValueChanged<String> onChanged;

  static const _labels = {
    'readonly': '只读',
    'workspace-write': '读写限定目录',
    'full': '全权',
  };

  @override
  Widget build(BuildContext context) {
    return DropdownButton<String>(
      value: kToolPolicies.contains(value) ? value : 'workspace-write',
      isDense: true,
      underline: const SizedBox.shrink(),
      items: [
        for (final p in kToolPolicies)
          DropdownMenuItem(value: p, child: Text(_labels[p] ?? p)),
      ],
      onChanged: (p) {
        if (p != null) onChanged(p);
      },
    );
  }
}
