// M11.5 — RSS Settings page. Reachable from the ⋯ menu in the app bar.
//
// Theme uses the GLOBAL app theme (settingsControllerProvider) — RSS
// doesn't own a separate theme stack; in dark mode the reader already
// renders OLED true-black (RssReaderColors). RSS-specific prefs (refresh
// frequency, AI digest budget) persist to rss.user_preferences via
// user_prefs_get / user_prefs_update.

import 'dart:convert';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../../app/theme.dart';
import '../../../../settings/application/settings_controller.dart';
import '../../../../../services/settings_repo.dart' show ThemePreference;
import '../providers.dart';

class RssSettingsPage extends ConsumerStatefulWidget {
  const RssSettingsPage({super.key});

  @override
  ConsumerState<RssSettingsPage> createState() => _RssSettingsPageState();
}

class _RssSettingsPageState extends ConsumerState<RssSettingsPage> {
  Map<String, dynamic> _cfg = {};
  bool _loading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final actions = ref.read(rssActionsProvider);
    if (actions == null) {
      setState(() {
        _loading = false;
        _error = '未登录';
      });
      return;
    }
    try {
      final cfg = await actions.userPrefsGet();
      if (!mounted) return;
      setState(() {
        _cfg = cfg;
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
    final actions = ref.read(rssActionsProvider);
    if (actions == null) return;
    try {
      await actions.userPrefsUpdate(_cfg);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('保存失败: $e')));
    }
  }

  void _set(String key, Object? value) {
    setState(() => _cfg[key] = value);
    _save();
  }

  int get _refreshMin => (_cfg['refresh_min'] as num?)?.toInt() ?? 30;
  bool get _aiDigest => _cfg['ai_digest'] as bool? ?? true;

  @override
  Widget build(BuildContext context) {
    final settings = ref.watch(settingsControllerProvider).valueOrNull;
    final theme = settings?.theme ?? ThemePreference.system;
    return Scaffold(
      appBar: AppBar(title: const Text('RSS 设置')),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? Center(child: Text('加载失败: $_error'))
              : ListView(
                  padding: const EdgeInsets.symmetric(vertical: BiuTokens.space3),
                  children: [
                    _section('外观'),
                    ListTile(
                      title: const Text('主题'),
                      subtitle: const Text('深色模式下阅读器自动启用 OLED 真黑'),
                      trailing: DropdownButton<ThemePreference>(
                        value: theme,
                        underline: const SizedBox.shrink(),
                        onChanged: (t) {
                          if (t != null) {
                            ref
                                .read(settingsControllerProvider.notifier)
                                .updateTheme(t);
                          }
                        },
                        items: const [
                          DropdownMenuItem(
                              value: ThemePreference.system, child: Text('跟随系统')),
                          DropdownMenuItem(
                              value: ThemePreference.light, child: Text('浅色')),
                          DropdownMenuItem(
                              value: ThemePreference.dark, child: Text('深色 / 真黑')),
                        ],
                      ),
                    ),
                    const Divider(height: 1),
                    _section('订阅'),
                    ListTile(
                      title: const Text('默认刷新频率'),
                      subtitle: Text('每 $_refreshMin 分钟'),
                      trailing: DropdownButton<int>(
                        value: _refreshMin,
                        underline: const SizedBox.shrink(),
                        onChanged: (v) => v != null ? _set('refresh_min', v) : null,
                        items: const [
                          DropdownMenuItem(value: 15, child: Text('15 分钟')),
                          DropdownMenuItem(value: 30, child: Text('30 分钟')),
                          DropdownMenuItem(value: 60, child: Text('1 小时')),
                          DropdownMenuItem(value: 180, child: Text('3 小时')),
                        ],
                      ),
                    ),
                    SwitchListTile(
                      title: const Text('AI 摘要'),
                      subtitle: const Text('为新文章生成 AI 摘要 (消耗积分)'),
                      value: _aiDigest,
                      onChanged: (v) => _set('ai_digest', v),
                    ),
                    const Divider(height: 1),
                    _section('多源中继'),
                    _RelayField(
                      title: '公众号中继',
                      subtitle: '第三方 RSS 中继地址 (werss / 自部署 wewe-rss)，'
                          '用于将公众号转成 RSS。留空则停用「公众号」订阅。',
                      hint: 'https://werss.app/feed/<账号>',
                      value: (_cfg['wechat_relay'] as String?) ?? '',
                      onSaved: (v) => _set('wechat_relay', v),
                    ),
                    _RelayField(
                      title: 'Nitter 实例',
                      subtitle: '将 X (Twitter) 用户转成 RSS 的 Nitter 实例地址。'
                          '公开实例稳定性有限，建议自部署。留空则停用「X 用户」订阅。',
                      hint: 'https://nitter.net',
                      value: (_cfg['nitter_instance'] as String?) ?? '',
                      onSaved: (v) => _set('nitter_instance', v),
                    ),
                    const Divider(height: 1),
                    _section('数据'),
                    ListTile(
                      leading: const Icon(Icons.download_outlined),
                      title: const Text('导出 OPML'),
                      subtitle: const Text('复制全部订阅为 OPML (可导入其他阅读器)'),
                      onTap: _exportOpml,
                    ),
                    ListTile(
                      leading: const Icon(Icons.archive_outlined),
                      title: const Text('导出全部数据'),
                      subtitle: const Text('打包订阅/文章/收藏/规则/设置为 zip — 你的数据，随时带走'),
                      onTap: _exportArchive,
                    ),
                  ],
                ),
    );
  }

  Widget _section(String label) => Padding(
        padding: const EdgeInsets.fromLTRB(
            BiuTokens.space4, BiuTokens.space4, BiuTokens.space4, BiuTokens.space2),
        child: Text(label,
            style: TextStyle(
                fontSize: 12,
                fontWeight: FontWeight.w700,
                color: BiuTokens.textMuted,
                letterSpacing: 0.5)),
      );

  Future<void> _exportOpml() async {
    final actions = ref.read(rssActionsProvider);
    if (actions == null) return;
    try {
      final xml = await actions.opmlExport();
      await Clipboard.setData(ClipboardData(text: xml));
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('OPML 已复制到剪贴板')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('导出失败: $e')));
    }
  }

  // M14.3 — full data takeout: fetch the base64 zip, let the user pick a
  // save location (file_selector, already a dep), write the bytes.
  Future<void> _exportArchive() async {
    final actions = ref.read(rssActionsProvider);
    if (actions == null) return;
    try {
      final res = await actions.exportArchive();
      final b64 = res['archive_b64'] as String?;
      if (b64 == null || b64.isEmpty) {
        throw Exception('空归档');
      }
      final bytes = base64Decode(b64);
      final filename = (res['filename'] as String?) ?? 'biumind-rss-export.zip';
      final loc = await getSaveLocation(
        suggestedName: filename,
        acceptedTypeGroups: const [
          XTypeGroup(label: 'ZIP', extensions: ['zip']),
        ],
      );
      if (loc == null) return; // user cancelled
      final file = XFile.fromData(
        Uint8List.fromList(bytes),
        name: filename,
        mimeType: 'application/zip',
      );
      await file.saveTo(loc.path);
      if (!mounted) return;
      final c = (res['counts'] as Map?)?.cast<String, dynamic>() ?? {};
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content: Text('已导出 ${c['feeds'] ?? 0} 订阅 · ${c['entries'] ?? 0} 文章')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('导出失败: $e')));
    }
  }
}

/// M13.3/13.4 — a labelled relay-URL text field that persists on submit or
/// focus loss (not on every keystroke). Used for the 公众号 / Nitter relay
/// addresses stored in rss.user_preferences.
class _RelayField extends StatefulWidget {
  const _RelayField({
    required this.title,
    required this.subtitle,
    required this.hint,
    required this.value,
    required this.onSaved,
  });

  final String title;
  final String subtitle;
  final String hint;
  final String value;
  final ValueChanged<String> onSaved;

  @override
  State<_RelayField> createState() => _RelayFieldState();
}

class _RelayFieldState extends State<_RelayField> {
  late final TextEditingController _ctrl =
      TextEditingController(text: widget.value);
  late final FocusNode _focus = FocusNode();

  @override
  void initState() {
    super.initState();
    _focus.addListener(() {
      if (!_focus.hasFocus) _save();
    });
  }

  @override
  void dispose() {
    _focus.dispose();
    _ctrl.dispose();
    super.dispose();
  }

  void _save() {
    final v = _ctrl.text.trim();
    if (v != widget.value) widget.onSaved(v);
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(
          BiuTokens.space4, BiuTokens.space2, BiuTokens.space4, BiuTokens.space2),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(widget.title,
              style:
                  const TextStyle(fontSize: 15, fontWeight: FontWeight.w500)),
          const SizedBox(height: 2),
          Text(widget.subtitle,
              style: TextStyle(fontSize: 12, color: BiuTokens.textMuted)),
          const SizedBox(height: BiuTokens.space2),
          TextField(
            controller: _ctrl,
            focusNode: _focus,
            decoration: InputDecoration(
              hintText: widget.hint,
              isDense: true,
              border: const OutlineInputBorder(),
            ),
            onSubmitted: (_) => _save(),
          ),
        ],
      ),
    );
  }
}
