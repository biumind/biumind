// 状态信号 (hook) 面板(PERI-1)—— 右栏「状态信号」。
//
// 展示 hook 安装态(node / 脚本 / claude+codex 注入)+ 各 agent 就绪态(版本门槛),
// 提供一键安装/卸载。hook 让 Claude/Codex 主动上报生命周期,驱动可靠的
// running↔input_required(「需要注意」分组),取代纯 JSONL 轮询反推。
// 走 daemon bridge(hooks.status / readiness / install / uninstall,PERI-1c 已就绪)。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../data/code_bridge_provider.dart';
import '../domain/hook_status.dart';

class HooksPanel extends ConsumerStatefulWidget {
  const HooksPanel({super.key});

  @override
  ConsumerState<HooksPanel> createState() => _HooksPanelState();
}

class _HooksPanelState extends ConsumerState<HooksPanel> {
  HookInstallStatus? _status;
  List<HookAgentReadiness> _readiness = const [];
  bool _loading = true;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  Future<void> _load() async {
    final bridge = ref.read(codeBridgeClientProvider);
    if (bridge == null) {
      setState(() {
        _loading = false;
        _error = 'daemon 未就绪';
      });
      return;
    }
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final st = await bridge.hooksStatus();
      final rd = await bridge.hooksReadiness();
      if (!mounted) return;
      setState(() {
        _status = st;
        _readiness = rd;
        _loading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = '$e';
      });
    }
  }

  Future<void> _install() async {
    final bridge = ref.read(codeBridgeClientProvider);
    if (bridge == null || _busy) return;
    setState(() => _busy = true);
    try {
      await bridge.hooksInstall();
      await _load();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('安装失败: $e')));
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _uninstall() async {
    final bridge = ref.read(codeBridgeClientProvider);
    if (bridge == null || _busy) return;
    setState(() => _busy = true);
    try {
      await bridge.hooksUninstall();
      await _load();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('卸载失败: $e')));
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _header(),
        const Divider(height: 1),
        Expanded(child: _body()),
      ],
    );
  }

  Widget _header() {
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 10, 8, 10),
      child: Row(
        children: [
          Icon(Icons.sensors_rounded, size: 14, color: BiuTokens.textSecondary),
          const SizedBox(width: 6),
          const Expanded(
            child: Text('状态信号 (hook)',
                style: TextStyle(fontSize: 12, fontWeight: FontWeight.w600)),
          ),
          InkWell(
            onTap: _loading ? null : _load,
            child: Icon(Icons.refresh_rounded,
                size: 15, color: BiuTokens.textMuted),
          ),
        ],
      ),
    );
  }

  Widget _body() {
    if (_loading) {
      return const Center(
        child: SizedBox(
            width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2)),
      );
    }
    if (_error != null) {
      return _centerHint(_error!, icon: Icons.error_outline_rounded);
    }
    final st = _status;
    return ListView(
      padding: const EdgeInsets.fromLTRB(12, 10, 12, 12),
      children: [
        Text(
          'hook 让 Claude/Codex 主动上报生命周期(等待审批 / 一轮结束),'
          '驱动可靠的「需要注意」分组,取代纯轮询反推。',
          style: TextStyle(
              fontSize: 11, color: BiuTokens.textMuted, height: 1.5),
        ),
        const SizedBox(height: 14),
        _kv('Node.js',
            st?.hasNode == true ? st!.nodePath : '未找到(hook 脚本需 Node.js)',
            ok: st?.hasNode == true),
        const SizedBox(height: 14),
        Text('agent 就绪态',
            style: TextStyle(
                fontSize: 11,
                fontWeight: FontWeight.w600,
                color: BiuTokens.textSecondary)),
        const SizedBox(height: 6),
        for (final r in _readiness) _readinessRow(r),
        const SizedBox(height: 18),
        Row(
          children: [
            Expanded(
              child: FilledButton.tonalIcon(
                onPressed: _busy ? null : _install,
                icon: const Icon(Icons.download_rounded, size: 15),
                label: const Text('安装 / 重装'),
              ),
            ),
            const SizedBox(width: 8),
            OutlinedButton(
              onPressed: _busy ? null : _uninstall,
              child: const Text('卸载'),
            ),
          ],
        ),
        if (_busy)
          const Padding(
            padding: EdgeInsets.only(top: 12),
            child: Center(
              child: SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2)),
            ),
          ),
      ],
    );
  }

  Widget _readinessRow(HookAgentReadiness r) {
    final color = r.usable ? BiuTokens.green : Colors.orange;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(r.usable ? Icons.check_circle_rounded : Icons.info_outline_rounded,
              size: 14, color: color),
          const SizedBox(width: 6),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(r.agent,
                    style: const TextStyle(
                        fontSize: 12, fontWeight: FontWeight.w600)),
                Text(r.reasonLabel,
                    style:
                        TextStyle(fontSize: 11, color: BiuTokens.textMuted)),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _kv(String k, String v, {required bool ok}) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Icon(ok ? Icons.check_circle_rounded : Icons.cancel_rounded,
                size: 14, color: ok ? BiuTokens.green : Colors.orange),
            const SizedBox(width: 6),
            Text(k,
                style: const TextStyle(
                    fontSize: 12, fontWeight: FontWeight.w600)),
          ],
        ),
        Padding(
          padding: const EdgeInsets.only(left: 20, top: 2),
          child: Text(v,
              style: TextStyle(
                  fontSize: 11,
                  color: BiuTokens.textMuted,
                  fontFamily: 'monospace')),
        ),
      ],
    );
  }

  Widget _centerHint(String msg, {required IconData icon}) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(20),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 28, color: BiuTokens.textMuted),
            const SizedBox(height: 8),
            Text(msg,
                textAlign: TextAlign.center,
                style: TextStyle(fontSize: 12, color: BiuTokens.textMuted)),
          ],
        ),
      ),
    );
  }
}
