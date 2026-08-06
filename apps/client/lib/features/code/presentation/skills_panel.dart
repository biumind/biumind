// 项目级 Skills 面板(PERI-3)—— 右栏「Skills」。
//
// 列 ~/.biumind/skills(与「技能管理」同一存储)里的 skill,为当前项目按 agent(Claude/
// Codex)安装/卸载 —— 安装即把 skill symlink 进 <project>/.claude/skills 或 .codex/skills,
// 让外部 Claude Code / Codex 也能发现复用。走 daemon bridge(skills.* RPC,PERI-3)。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../application/projects_controller.dart';
import '../data/code_bridge_provider.dart';
import '../domain/skill_models.dart';

class SkillsPanel extends ConsumerStatefulWidget {
  const SkillsPanel({super.key});

  @override
  ConsumerState<SkillsPanel> createState() => _SkillsPanelState();
}

class _SkillsPanelState extends ConsumerState<SkillsPanel> {
  static const _agents = ['claude', 'codex'];

  List<HubSkill> _hub = const [];
  List<SkillInstallation> _installed = const [];
  bool _loading = true;
  String? _error;
  String? _busyKey; // "<name>:<agent>" 正在装/卸

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  String? get _root => ref.read(activeCodeProjectProvider)?.path;

  bool _isInstalled(String name, String agent) =>
      _installed.any((i) => i.name == name && i.agent == agent);

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
      final hub = await bridge.skillsListHub();
      final ins = await bridge.skillsInstallations(root);
      if (!mounted) return;
      setState(() {
        _hub = hub;
        _installed = ins;
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

  Future<void> _toggle(String name, String agent, bool install) async {
    final bridge = ref.read(codeBridgeClientProvider);
    final root = _root;
    if (bridge == null || root == null) return;
    final key = '$name:$agent';
    setState(() => _busyKey = key);
    try {
      if (install) {
        await bridge.skillsInstall(root, name, agent);
      } else {
        await bridge.skillsUninstall(root, name, agent);
      }
      await _load();
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('${install ? '安装' : '卸载'}失败: $e')));
      }
    } finally {
      if (mounted) setState(() => _busyKey = null);
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
            Icon(Icons.extension_rounded, size: 14, color: BiuTokens.textSecondary),
            const SizedBox(width: 6),
            const Expanded(
              child: Text('Skills',
                  style: TextStyle(fontSize: 12, fontWeight: FontWeight.w600)),
            ),
            InkWell(
              onTap: _loading ? null : _load,
              child: Icon(Icons.refresh_rounded, size: 15, color: BiuTokens.textMuted),
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
      return _centerHint(_error!);
    }
    if (_hub.isEmpty) {
      return _centerHint('~/.biumind/skills 下暂无 skill。\n在「技能管理」里添加后回来安装到项目。');
    }
    return ListView.separated(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 12),
      itemCount: _hub.length,
      separatorBuilder: (_, _) => const SizedBox(height: 10),
      itemBuilder: (_, i) => _skillCard(_hub[i]),
    );
  }

  Widget _skillCard(HubSkill s) {
    return Container(
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        border: Border.all(color: BiuTokens.borderSubtle),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(s.name,
              style: const TextStyle(fontSize: 12.5, fontWeight: FontWeight.w600)),
          if (s.description.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 2),
              child: Text(s.description,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(fontSize: 11, color: BiuTokens.textMuted)),
            ),
          const SizedBox(height: 8),
          Row(
            children: [
              for (final agent in _agents) ...[
                _agentToggle(s.name, agent),
                const SizedBox(width: 8),
              ],
            ],
          ),
        ],
      ),
    );
  }

  Widget _agentToggle(String name, String agent) {
    final installed = _isInstalled(name, agent);
    final busy = _busyKey == '$name:$agent';
    return InkWell(
      onTap: busy ? null : () => _toggle(name, agent, !installed),
      borderRadius: BorderRadius.circular(6),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 5),
        decoration: BoxDecoration(
          color: installed ? BiuTokens.purple.withValues(alpha: 0.12) : null,
          border: Border.all(
              color: installed ? BiuTokens.purple : BiuTokens.borderSubtle),
          borderRadius: BorderRadius.circular(6),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (busy)
              const SizedBox(
                  width: 12, height: 12, child: CircularProgressIndicator(strokeWidth: 2))
            else
              Icon(installed ? Icons.check_rounded : Icons.add_rounded,
                  size: 13,
                  color: installed ? BiuTokens.purple : BiuTokens.textMuted),
            const SizedBox(width: 4),
            Text(agent,
                style: TextStyle(
                    fontSize: 11,
                    color: installed ? BiuTokens.purple : BiuTokens.textSecondary)),
          ],
        ),
      ),
    );
  }

  Widget _centerHint(String msg) => Center(
        child: Padding(
          padding: const EdgeInsets.all(20),
          child: Text(msg,
              textAlign: TextAlign.center,
              style: TextStyle(fontSize: 12, color: BiuTokens.textMuted, height: 1.5)),
        ),
      );
}
