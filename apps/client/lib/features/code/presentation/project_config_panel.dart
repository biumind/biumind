// 项目配置面板(PERI-2)—— 右栏「项目配置」。
//
// 编辑 .biu/config.toml 的 [agent] 段:默认 agent / 默认权限档 / prompt 前缀。
// prompt 前缀在任务启动时由 daemon 拼到每个 prompt 最前(服务端施加,见 projcfg)。
// 走 daemon bridge(config.read / config.write,PERI-2 已就绪),作用于当前活动项目。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../application/projects_controller.dart';
import '../data/code_bridge_provider.dart';
import '../domain/project_config.dart';

class ProjectConfigPanel extends ConsumerStatefulWidget {
  const ProjectConfigPanel({super.key});

  @override
  ConsumerState<ProjectConfigPanel> createState() =>
      _ProjectConfigPanelState();
}

class _ProjectConfigPanelState extends ConsumerState<ProjectConfigPanel> {
  static const _agents = ['biu', 'claude', 'codex'];
  static const _perms = ['ask', 'auto_edit', 'full_access'];

  ProjectConfig _cfg = const ProjectConfig();
  final _prefixCtl = TextEditingController();
  bool _loading = true;
  bool _saving = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  @override
  void dispose() {
    _prefixCtl.dispose();
    super.dispose();
  }

  String? get _root => ref.read(activeCodeProjectProvider)?.path;

  Future<void> _load() async {
    final bridge = ref.read(codeBridgeClientProvider);
    final root = _root;
    if (bridge == null || root == null) {
      setState(() {
        _loading = false;
        _error = bridge == null ? 'daemon 未就绪' : '未选择项目';
      });
      return;
    }
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final cfg = await bridge.configRead(root);
      if (!mounted) return;
      setState(() {
        _cfg = cfg;
        _prefixCtl.text = cfg.promptPrefix;
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

  Future<void> _save() async {
    final bridge = ref.read(codeBridgeClientProvider);
    final root = _root;
    if (bridge == null || root == null || _saving) return;
    setState(() => _saving = true);
    final next = _cfg.copyWith(promptPrefix: _prefixCtl.text);
    try {
      await bridge.configWrite(root, next);
      if (!mounted) return;
      setState(() => _cfg = next);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('已保存'), duration: Duration(seconds: 1)),
      );
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('保存失败: $e')));
      }
    } finally {
      if (mounted) setState(() => _saving = false);
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

  Widget _header() => Padding(
        padding: const EdgeInsets.fromLTRB(12, 10, 8, 10),
        child: Row(
          children: [
            Icon(Icons.tune_rounded, size: 14, color: BiuTokens.textSecondary),
            const SizedBox(width: 6),
            const Expanded(
              child: Text('项目配置',
                  style:
                      TextStyle(fontSize: 12, fontWeight: FontWeight.w600)),
            ),
            InkWell(
              onTap: _loading ? null : _load,
              child: Icon(Icons.refresh_rounded,
                  size: 15, color: BiuTokens.textMuted),
            ),
          ],
        ),
      );

  Widget _body() {
    if (_loading) {
      return const Center(
        child: SizedBox(
            width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2)),
      );
    }
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(20),
          child: Text(_error!,
              textAlign: TextAlign.center,
              style: TextStyle(fontSize: 12, color: BiuTokens.textMuted)),
        ),
      );
    }
    return ListView(
      padding: const EdgeInsets.fromLTRB(12, 12, 12, 12),
      children: [
        Text('新任务默认值与每个任务的 prompt 前缀,保存到本项目 .biu/config.toml。',
            style: TextStyle(
                fontSize: 11, color: BiuTokens.textMuted, height: 1.5)),
        const SizedBox(height: 16),
        _label('默认 agent'),
        _dropdown(_agents, _cfg.agentDefault,
            (v) => setState(() => _cfg = _cfg.copyWith(agentDefault: v))),
        const SizedBox(height: 14),
        _label('默认权限档'),
        _dropdown(_perms, _cfg.defaultPermissionMode,
            (v) => setState(
                () => _cfg = _cfg.copyWith(defaultPermissionMode: v))),
        const SizedBox(height: 14),
        _label('prompt 前缀'),
        Text('拼到本项目每个任务 prompt 最前(如「遵循 STYLE.md」)。',
            style: TextStyle(fontSize: 10.5, color: BiuTokens.textMuted)),
        const SizedBox(height: 6),
        TextField(
          controller: _prefixCtl,
          maxLines: 5,
          minLines: 3,
          style: const TextStyle(fontSize: 12, fontFamily: 'monospace'),
          decoration: const InputDecoration(
            isDense: true,
            border: OutlineInputBorder(),
            hintText: '(空 = 不加前缀)',
          ),
        ),
        const SizedBox(height: 16),
        FilledButton.icon(
          onPressed: _saving ? null : _save,
          icon: const Icon(Icons.save_rounded, size: 15),
          label: const Text('保存'),
        ),
      ],
    );
  }

  Widget _label(String s) => Padding(
        padding: const EdgeInsets.only(bottom: 6),
        child: Text(s,
            style: TextStyle(
                fontSize: 11,
                fontWeight: FontWeight.w600,
                color: BiuTokens.textSecondary)),
      );

  Widget _dropdown(
          List<String> opts, String value, ValueChanged<String> onChanged) =>
      DropdownButtonFormField<String>(
        initialValue: opts.contains(value) ? value : opts.first,
        isDense: true,
        decoration: const InputDecoration(
            isDense: true, border: OutlineInputBorder()),
        items: [
          for (final o in opts)
            DropdownMenuItem(
                value: o, child: Text(o, style: const TextStyle(fontSize: 12))),
        ],
        onChanged: (v) {
          if (v != null) onChanged(v);
        },
      );
}
