// Modal dialog for adding a new feed. Three source types (M13):
//   • RSS / 网站  — paste a feed or site URL; server auto-discovers.
//   • 公众号       — type the account name; we build a third-party relay
//                    (werss / wewe-rss) feed URL from the configured relay
//                    base. Depends on a third-party relay (v3 risk R7).
//   • X 用户       — type the @handle; we build <Nitter 实例>/<handle>/rss
//                    from the configured Nitter instance.
//
// Relay base / Nitter instance live in RSS Settings (rss.user_preferences);
// the 公众号 / X tabs are disabled until they're configured, with a shortcut
// hint pointing at Settings. URL construction mirrors the Go helpers
// WeChatFeedURL / NitterFeedURL so client and server agree.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../../app/theme.dart';
import '../providers.dart';

Future<bool> showAddFeedSheet(BuildContext context, WidgetRef ref) async {
  final result = await showDialog<bool>(
    context: context,
    builder: (ctx) => const _AddFeedDialog(),
  );
  return result ?? false;
}

enum _SourceType { rss, wechat, x }

class _AddFeedDialog extends ConsumerStatefulWidget {
  const _AddFeedDialog();
  @override
  ConsumerState<_AddFeedDialog> createState() => _AddFeedDialogState();
}

class _AddFeedDialogState extends ConsumerState<_AddFeedDialog> {
  final _urlCtrl = TextEditingController();
  final _titleCtrl = TextEditingController();
  final _nameCtrl = TextEditingController(); // 公众号名 / X handle
  _SourceType _type = _SourceType.rss;
  bool _busy = false;
  bool _loadingPrefs = true;
  String? _error;

  // Relay config pulled from rss.user_preferences.
  String _wechatRelay = '';
  String _nitterInstance = '';

  @override
  void initState() {
    super.initState();
    _loadPrefs();
  }

  Future<void> _loadPrefs() async {
    final actions = ref.read(rssActionsProvider);
    if (actions == null) {
      setState(() => _loadingPrefs = false);
      return;
    }
    try {
      final cfg = await actions.userPrefsGet();
      if (!mounted) return;
      setState(() {
        _wechatRelay = (cfg['wechat_relay'] as String?)?.trim() ?? '';
        _nitterInstance = (cfg['nitter_instance'] as String?)?.trim() ?? '';
        _loadingPrefs = false;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() => _loadingPrefs = false);
    }
  }

  @override
  void dispose() {
    _urlCtrl.dispose();
    _titleCtrl.dispose();
    _nameCtrl.dispose();
    super.dispose();
  }

  // Mirror of Go WeChatFeedURL: <relay trimmed of trailing />/<encoded name>.
  String _buildWechatUrl() =>
      '${_wechatRelay.replaceAll(RegExp(r'/+$'), '')}/'
      '${Uri.encodeComponent(_nameCtrl.text.trim())}';

  // Mirror of Go NitterFeedURL: <instance>/<handle without @>/rss.
  String _buildNitterUrl() {
    final handle = _nameCtrl.text.trim().replaceFirst(RegExp(r'^@'), '');
    return '${_nitterInstance.replaceAll(RegExp(r'/+$'), '')}/$handle/rss';
  }

  Future<void> _submit() async {
    final actions = ref.read(rssActionsProvider);
    if (actions == null) {
      setState(() => _error = '尚未登录');
      return;
    }

    String url;
    String kind;
    switch (_type) {
      case _SourceType.rss:
        url = _urlCtrl.text.trim();
        kind = 'rss';
        if (url.isEmpty) {
          setState(() => _error = '请输入 RSS 或站点 URL');
          return;
        }
      case _SourceType.wechat:
        if (_nameCtrl.text.trim().isEmpty) {
          setState(() => _error = '请输入公众号名');
          return;
        }
        url = _buildWechatUrl();
        kind = 'wechat';
      case _SourceType.x:
        if (_nameCtrl.text.trim().isEmpty) {
          setState(() => _error = '请输入 X 用户名（@handle）');
          return;
        }
        url = _buildNitterUrl();
        kind = 'x';
    }

    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await actions.feedsAdd(url, title: _titleCtrl.text.trim(), kind: kind);
      if (!mounted) return;
      ref.refreshFeeds();
      Navigator.of(context).pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _busy = false;
        _error = '$e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('添加订阅'),
      content: SizedBox(
        width: 460,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            SegmentedButton<_SourceType>(
              style: SegmentedButton.styleFrom(
                visualDensity: VisualDensity.compact,
                textStyle: const TextStyle(fontSize: 13),
              ),
              segments: const [
                ButtonSegment(
                    value: _SourceType.rss,
                    label: Text('RSS / 网站'),
                    icon: Icon(Icons.rss_feed, size: 15)),
                ButtonSegment(
                    value: _SourceType.wechat,
                    label: Text('公众号'),
                    icon: Icon(Icons.chat_outlined, size: 15)),
                ButtonSegment(
                    value: _SourceType.x,
                    label: Text('X 用户'),
                    icon: Icon(Icons.alternate_email, size: 15)),
              ],
              selected: {_type},
              showSelectedIcon: false,
              onSelectionChanged: _busy
                  ? null
                  : (s) => setState(() {
                        _type = s.first;
                        _error = null;
                      }),
            ),
            const SizedBox(height: BiuTokens.space4),
            if (_loadingPrefs)
              const Padding(
                padding: EdgeInsets.symmetric(vertical: BiuTokens.space3),
                child: Center(child: CircularProgressIndicator()),
              )
            else
              ..._buildBody(),
            if (_error != null) ...[
              const SizedBox(height: BiuTokens.space3),
              Text(_error!,
                  style: const TextStyle(color: BiuTokens.error, fontSize: 13)),
            ],
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: _busy ? null : () => Navigator.of(context).pop(false),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: (_busy || !_canSubmit()) ? null : _submit,
          child: _busy
              ? const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('订阅'),
        ),
      ],
    );
  }

  bool _canSubmit() {
    switch (_type) {
      case _SourceType.rss:
        return true;
      case _SourceType.wechat:
        return _wechatRelay.isNotEmpty;
      case _SourceType.x:
        return _nitterInstance.isNotEmpty;
    }
  }

  List<Widget> _buildBody() {
    switch (_type) {
      case _SourceType.rss:
        return [
          TextField(
            controller: _urlCtrl,
            autofocus: true,
            enabled: !_busy,
            decoration: const InputDecoration(
              labelText: 'Feed URL',
              hintText: 'https://example.com/feed.xml',
            ),
            onSubmitted: (_) => _submit(),
          ),
          const SizedBox(height: BiuTokens.space3),
          TextField(
            controller: _titleCtrl,
            enabled: !_busy,
            decoration: const InputDecoration(
              labelText: '标题（可选）',
              hintText: '留空则使用 feed 自带的标题',
            ),
          ),
        ];
      case _SourceType.wechat:
        return [
          if (_wechatRelay.isEmpty)
            _relayMissingNote('请先在「设置 → 公众号中继」配置中继地址')
          else
            TextField(
              controller: _nameCtrl,
              autofocus: true,
              enabled: !_busy,
              decoration: const InputDecoration(
                labelText: '公众号名',
                hintText: '例如：卓克科技参考',
              ),
              onSubmitted: (_) => _submit(),
            ),
          const SizedBox(height: BiuTokens.space2),
          _thirdPartyNote('公众号订阅依赖第三方 RSS 中继（$_wechatRelay），'
              '中继不可用时该订阅会显示抓取错误。'),
        ];
      case _SourceType.x:
        return [
          if (_nitterInstance.isEmpty)
            _relayMissingNote('请先在「设置 → Nitter 实例」配置实例地址')
          else
            TextField(
              controller: _nameCtrl,
              autofocus: true,
              enabled: !_busy,
              decoration: const InputDecoration(
                labelText: 'X 用户名',
                hintText: '@elonmusk',
              ),
              onSubmitted: (_) => _submit(),
            ),
          const SizedBox(height: BiuTokens.space2),
          _thirdPartyNote('X 用户订阅经 Nitter 实例（$_nitterInstance）拉取，'
              '公开实例稳定性有限，建议自部署。'),
        ];
    }
  }

  Widget _relayMissingNote(String text) => Container(
        padding: const EdgeInsets.all(BiuTokens.space3),
        decoration: BoxDecoration(
          color: BiuTokens.purple.withValues(alpha: 0.08),
          borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
        ),
        child: Row(
          children: [
            Icon(Icons.settings_outlined, size: 16, color: BiuTokens.purple),
            const SizedBox(width: BiuTokens.space2),
            Expanded(
              child: Text(text,
                  style: TextStyle(
                      fontSize: 13, color: BiuTokens.textSecondary)),
            ),
          ],
        ),
      );

  Widget _thirdPartyNote(String text) => Text(
        text,
        style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
      );
}
