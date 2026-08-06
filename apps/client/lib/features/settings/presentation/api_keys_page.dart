// BYOK — API Keys 设置页 (lobehub 风格两栏).
//
// 左: provider 卡片列表 (品牌 SVG logo + 名称 + 启用绿点), Accordion 分
// 云端凭据 / 本地凭据. 右: 选中 provider 的内联详情 (key/endpoint/模型/测试),
// 取代 dialog. 桌面宽屏 (≥900) 两栏; 窄屏点卡片 push 详情页.
//
// 云端凭据 (server BYOK): key 加密上传 identity, 跨设备可用.
// 本地凭据 (client-side BYOK P5): key 仅存本机 keychain, 本机直连上游.
//
// 设计: docs/BiuMind-BYOK-Unification-Design.md §8 + lobehub provider 设置参考.

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../app/theme.dart';
import '../../../core/llm/provider_catalog.dart';
import '../../../core/llm/provider_icon.dart';
import '../../../data/api/direct_llm_probe.dart';
import '../application/api_keys_providers.dart';
import '../data/api_keys_client.dart';

const _kWideBreakpoint = 900.0;

class ApiKeysPage extends ConsumerWidget {
  const ApiKeysPage({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // select(null-bool): 仅 configured 翻转重建; token 轮换不闪.
    ref.watch(apiKeysClientProvider.select((c) => c == null));
    final client = ref.read(apiKeysClientProvider);
    final list = ref.watch(apiKeysListProvider);
    if (client == null) return const _UnconfiguredHint();

    return LayoutBuilder(
      builder: (ctx, c) {
        final wide = c.maxWidth >= _kWideBreakpoint;
        return list.when(
          data: (entries) {
            if (wide) {
              final selected = ref.watch(selectedApiKeyProvider) ??
                  'server:${supportedByokProviders.first}';
              return Row(
                children: [
                  SizedBox(
                    width: 300,
                    child: Material(
                      color: BiuTokens.surface,
                      child: _ProviderCardList(entries: entries, wide: true),
                    ),
                  ),
                  const VerticalDivider(width: 1),
                  Expanded(
                    child: _DetailPane(
                        entries: entries, selected: selected),
                  ),
                ],
              );
            }
            return _ProviderCardList(entries: entries, wide: false);
          },
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (e, _) => _ErrorBanner(message: '$e'),
        );
      },
    );
  }
}

class _UnconfiguredHint extends StatelessWidget {
  const _UnconfiguredHint();
  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space6),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.settings_outlined, size: 48, color: BiuTokens.textMuted),
            const SizedBox(height: BiuTokens.space3),
            Text('请先在「凭证管理」配置 BiuMind 服务地址 + 登录',
                style: TextStyle(fontSize: 14, color: BiuTokens.textMuted)),
          ],
        ),
      ),
    );
  }
}

class _ErrorBanner extends StatelessWidget {
  const _ErrorBanner({required this.message});
  final String message;
  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.all(BiuTokens.space4),
        child: Text('加载失败: $message',
            style: TextStyle(color: BiuTokens.error, fontSize: 12)),
      );
}

// ─── 左栏: provider 卡片列表 ──────────────────────────

class _ProviderCardList extends ConsumerWidget {
  const _ProviderCardList({required this.entries, required this.wide});
  final List<ApiKeyEntry> entries;
  final bool wide;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final serverByProvider = {
      for (final e in entries.where((e) => !e.isClientSide)) e.provider: e,
    };
    final clientEntries = entries.where((e) => e.isClientSide).toList();
    // 单列表 (不再分「云端凭据 / 本机直连」两 section): server slot (云端出口,
    // 含未配置的 provider 占位保发现性) + client 已配置条目 (本机出口) + 添加入口.
    // 出口由卡尾「云端 / 桌面」badge 区分. 后端双出口 (relay 云端 + daemon 本机) 保留.
    return ListView(
      padding: const EdgeInsets.symmetric(vertical: BiuTokens.space3),
      children: [
        _Section(
            title: 'BYOK 凭据',
            hint: '云端 = relay 跨设备; 桌面 = 本机直连 (内网 proxy)'),
        for (final p in supportedByokProviders)
          _ProviderCard(
            selectKey: 'server:$p',
            slug: p,
            entry: serverByProvider[p],
            isClient: false,
            wide: wide,
          ),
        for (final e in clientEntries)
          _ProviderCard(
            selectKey: 'client:${e.id}',
            slug: e.provider,
            entry: e,
            isClient: true,
            wide: wide,
          ),
        ListTile(
          dense: true,
          leading: Icon(Icons.add, size: 20, color: BiuTokens.textMuted),
          title: Text('添加本机直连凭据',
              style: TextStyle(fontSize: 13, color: BiuTokens.textMuted)),
          onTap: () => _select(context, ref, 'client:new', wide, entries),
        ),
      ],
    );
  }

  void _select(BuildContext context, WidgetRef ref, String key,
      bool wide, List<ApiKeyEntry> entries) {
    if (wide) {
      ref.read(selectedApiKeyProvider.notifier).state = key;
    } else {
      Navigator.push(
        context,
        MaterialPageRoute(
          builder: (_) => _DetailPage(entries: entries, selected: key),
        ),
      );
    }
  }
}

class _Section extends StatelessWidget {
  const _Section({required this.title, required this.hint});
  final String title;
  final String hint;
  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.fromLTRB(
            BiuTokens.space4, BiuTokens.space3, BiuTokens.space4, BiuTokens.space2),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(title,
                style: TextStyle(
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                    color: BiuTokens.textMuted)),
            const SizedBox(height: 2),
            Text(hint,
                style: TextStyle(fontSize: 11, color: BiuTokens.textMuted)),
          ],
        ),
      );
}

class _ProviderCard extends ConsumerWidget {
  const _ProviderCard({
    required this.selectKey,
    required this.slug,
    required this.entry,
    required this.isClient,
    required this.wide,
  });
  final String selectKey;
  final String slug;
  final ApiKeyEntry? entry;
  final bool isClient;
  final bool wide;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final selected = wide ? (ref.watch(selectedApiKeyProvider) == selectKey) : false;
    final label = byokProviderLabels[slug] ?? slug;
    final configured = entry != null && entry!.status != ApiKeyStatus.revoked;
    final valid = configured && entry!.status == ApiKeyStatus.valid;

    return InkWell(
      onTap: () {
        if (wide) {
          ref.read(selectedApiKeyProvider.notifier).state = selectKey;
        } else {
          Navigator.push(
            context,
            MaterialPageRoute(
              builder: (_) => _DetailPage(
                  entries: ref.read(apiKeysListProvider).valueOrNull ?? const [],
                  selected: selectKey),
            ),
          );
        }
      },
      child: Container(
        color: selected ? BiuTokens.surface : Colors.transparent,
        padding: const EdgeInsets.symmetric(
            horizontal: BiuTokens.space4, vertical: BiuTokens.space3),
        child: Row(
          children: [
            providerIcon(slug, size: 24),
            const SizedBox(width: BiuTokens.space3),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(children: [
                    Text(label,
                        style: TextStyle(
                            fontSize: 13,
                            fontWeight: FontWeight.w600,
                            color: BiuTokens.text)),
                    const SizedBox(width: BiuTokens.space2),
                    if (isClient) _LocalBadge() else _CloudBadge(),
                  ]),
                  const SizedBox(height: 2),
                  Text(_subtitle(),
                      style:
                          TextStyle(fontSize: 11, color: BiuTokens.textMuted)),
                ],
              ),
            ),
            if (valid)
              const Icon(Icons.check_circle, size: 14, color: Colors.green)
            else if (configured)
              Icon(Icons.error_outline, size: 14, color: BiuTokens.error),
          ],
        ),
      ),
    );
  }

  String _subtitle() {
    if (entry == null || entry!.status == ApiKeyStatus.revoked) {
      return isClient ? '未配置' : '未配置 — 录入后跳过平台扣费';
    }
    final last4 = entry!.last4.isEmpty ? '????' : entry!.last4;
    final base = entry!.baseUrl.isEmpty ? '' : ' · ${entry!.baseUrl}';
    return '...$last4$base';
  }
}

class _LocalBadge extends StatelessWidget {
  @override
  Widget build(BuildContext context) => Container(
        padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
        decoration: BoxDecoration(
          color: BiuTokens.borderSubtle,
          borderRadius: BorderRadius.circular(3),
        ),
        child: Text('桌面',
            style: TextStyle(fontSize: 9, color: BiuTokens.textMuted)),
      );
}

class _CloudBadge extends StatelessWidget {
  @override
  Widget build(BuildContext context) => Container(
        padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
        decoration: BoxDecoration(
          color: BiuTokens.borderSubtle,
          borderRadius: BorderRadius.circular(3),
        ),
        child: Text('云端',
            style: TextStyle(fontSize: 9, color: BiuTokens.textMuted)),
      );
}

// ─── 右栏: 详情面板 ───────────────────────────────────

/// 解析 selected key → 渲染对应详情.
class _DetailPane extends StatelessWidget {
  const _DetailPane({required this.entries, required this.selected});
  final List<ApiKeyEntry> entries;
  final String selected;

  @override
  Widget build(BuildContext context) {
    // ValueKey(selected): 切选中条目时强制重建 _ProviderDetail State, 否则
    // _ProviderDetailState 复用 → initState 不重跑 → _provider/输入框显旧条目
    // 数据 (预存 bug, 且会致 Bug1 保存后跳新条目却不刷新).
    if (selected == 'client:new') {
      return _ProviderDetail(
          key: const ValueKey('client:new'), isClient: true, isNew: true);
    }
    if (selected.startsWith('server:')) {
      final slug = selected.substring('server:'.length);
      final match = entries.where((e) => !e.isClientSide && e.provider == slug);
      return _ProviderDetail(
          key: ValueKey('server:$slug'),
          slug: slug,
          entry: match.isEmpty ? null : match.first,
          isClient: false);
    }
    if (selected.startsWith('client:')) {
      final id = selected.substring('client:'.length);
      final match = entries.where((e) => e.isClientSide && e.id == id);
      if (match.isEmpty) return const Center(child: Text('该凭据已删除'));
      return _ProviderDetail(
          key: ValueKey('client:$id'), entry: match.first, isClient: true);
    }
    return const Center(child: Text('选择左侧 provider 配置'));
  }
}

/// 窄屏 push 的详情页 (Scaffold 包 _DetailPane).
class _DetailPage extends StatelessWidget {
  const _DetailPage({required this.entries, required this.selected});
  final List<ApiKeyEntry> entries;
  final String selected;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('配置凭据')),
      body: _DetailPane(entries: entries, selected: selected),
    );
  }
}

// ─── 内联详情编辑 (取代 dialog) ───────────────────────

class _ProviderDetail extends ConsumerStatefulWidget {
  const _ProviderDetail({
    super.key,
    this.slug,
    this.entry,
    required this.isClient,
    this.isNew = false,
  });
  final String? slug;
  final ApiKeyEntry? entry;
  final bool isClient;
  final bool isNew;

  @override
  ConsumerState<_ProviderDetail> createState() => _ProviderDetailState();
}

class _ProviderDetailState extends ConsumerState<_ProviderDetail> {
  final _keyCtrl = TextEditingController();
  final _labelCtrl = TextEditingController();
  final _baseurlCtrl = TextEditingController();
  final _modelglobsCtrl = TextEditingController();
  String _provider = 'custom';
  String _protocol = 'openai_compat';
  bool _saving = false;
  String? _error;
  String? _testResult;
  bool _obscure = true;

  bool get _isCustom => _provider == 'custom';

  /// legacy client-side standard 条目 (Bug 2 收口前建的 anthropic/openai/... 本机
  /// 直连, 模型组恒空 = 死凭据). 编辑态显失效提示 + 仅删, 不让改。
  bool get _isLegacyClientStandard => widget.isClient && _provider != 'custom';

  @override
  void initState() {
    super.initState();
    final e = widget.entry;
    _provider = widget.slug ?? e?.provider ?? 'custom';
    // client-side 本机直连收口 custom-only: server-side 已覆盖 standard 云端出口,
    // client-side 走 standard 是死凭据 (Bug 2). 新建强制 custom; legacy standard
    // 条目保留原 slug → _isLegacyClientStandard 触发失效提示。
    if (widget.isClient && widget.isNew) {
      _provider = 'custom';
    }
    _labelCtrl.text = e?.label ?? '';
    _baseurlCtrl.text = e?.baseUrl ?? '';
    _modelglobsCtrl.text = e?.modelGlobs.join(',') ?? '';
    // standard provider 协议固定按 slug 派生 (anthropic/google/openai_compat);
    // custom 用用户存值. 忽略 legacy standard 条目存的错 protocol.
    _protocol = _provider == 'custom'
        ? ((e?.protocol.isNotEmpty ?? false) ? e!.protocol : 'openai_compat')
        : protocolForProviderSlug(_provider);
  }

  @override
  void dispose() {
    _keyCtrl.dispose();
    _labelCtrl.dispose();
    _baseurlCtrl.dispose();
    _modelglobsCtrl.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    final apiKey = _keyCtrl.text.trim();
    final client = ref.read(apiKeysClientProvider);
    if (client == null) return;
    final baseUrl = _baseurlCtrl.text.trim();
    final globs = _modelglobsCtrl.text
        .split(',')
        .map((s) => s.trim())
        .where((s) => s.isNotEmpty)
        .toList();

    // 校验
    if (!widget.isClient && apiKey.isEmpty && widget.entry == null) {
      setState(() => _error = 'API Key 不能为空');
      return;
    }
    if (widget.isClient && baseUrl.isEmpty) {
      setState(() => _error = '本机直连凭据必须填上游地址 (Base URL)');
      return;
    }
    if (_isCustom && globs.isEmpty) {
      setState(() => _error = '自定义 provider 必须填所用模型 (如 glm-* 或 glm-4.5)');
      return;
    }

    setState(() {
      _saving = true;
      _error = null;
    });
    final messenger = ScaffoldMessenger.of(context);
    try {
      ApiKeyEntry saved;
      if (widget.isClient) {
        // apiKey 空 = 编辑不改 key (identity 保留原加密值); 非空 = 新建/覆盖.
        // key 加密存 identity, 不写本地 keychain.
        saved = await client.upsert(
          provider: _provider,
          apiKey: apiKey,
          label: _labelCtrl.text.trim(),
          baseUrl: baseUrl,
          protocol: _protocol,
          modelGlobs: _isCustom ? globs : null,
          isClientSide: true,
        );
      } else {
        saved = await client.upsert(
          provider: _provider,
          apiKey: apiKey,
          label: _labelCtrl.text.trim(),
          baseUrl: _isCustom ? baseUrl : null,
          protocol: _isCustom ? _protocol : null,
          modelGlobs: _isCustom ? globs : null,
        );
      }
      if (!mounted) return;
      // Bug1: 等 list 刷新含新条目后切到该条目编辑态 (新建场景), 并清 key 输入
      // 防重复 upsert; Bug5: SnackBar 成功反馈. ValueKey(selected) 保证切后
      // _ProviderDetail 重建读新条目数据.
      ref.invalidate(apiKeysListProvider);
      if (widget.entry == null) {
        await ref.read(apiKeysListProvider.future);
        if (!mounted) return;
        final newKey = widget.isClient
            ? 'client:${saved.id}'
            : 'server:${saved.provider}';
        ref.read(selectedApiKeyProvider.notifier).state = newKey;
      }
      _keyCtrl.clear();
      setState(() {
        _saving = false;
        _error = null;
      });
      messenger.showSnackBar(const SnackBar(content: Text('已保存')));
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _saving = false;
        _error = '$e';
      });
    }
  }

  Future<void> _test() async {
    final client = ref.read(apiKeysClientProvider);
    if (client == null) return;
    final messenger = ScaffoldMessenger.of(context);
    setState(() => _testResult = null);
    try {
      if (widget.isClient) {
        final id = widget.entry?.id;
        // key: 新建 (无 id) 用 _keyCtrl 用户刚输; 已存则从 identity 取明文 (测完即弃).
        String key;
        if (id == null) {
          key = _keyCtrl.text.trim();
        } else {
          try {
            final cred = await client.fetchCredentials(id);
            if (!mounted) return;
            key = (cred['api_key'] as String?) ?? '';
          } catch (e) {
            if (!mounted) return;
            setState(() => _testResult = '取凭据失败: $e');
            return;
          }
        }
        if (key.isEmpty) {
          setState(() => _testResult = '未找到该凭据的密钥');
          return;
        }
        final base = _baseurlCtrl.text.trim();
        var model = _firstConcrete(_modelglobsCtrl.text);
        if (model.isEmpty) {
          // custom 全通配 globs (如 glm-*) 无法直接发探测 —— 拉上游 /models 按 glob
          // 取首个具体模型再测. 多数 OpenAI 兼容上游 (new-api/vLLM/DeepSeek)
          // 支持 /models; 不支持或无匹配则提示用户改填具体名.
          setState(() => _testResult = '解析通配模型中 (拉上游 /models)…');
          try {
            final ids = await listUpstreamModels(
              providerId: _protocol, apiKey: key, baseUrl: base);
            if (!mounted) return;
            final globs = _modelglobsCtrl.text
                .split(',')
                .map((s) => s.trim())
                .where((s) => s.isNotEmpty)
                .toList();
            model = ids.cast<String?>().firstWhere(
                  (m) => m != null && globs.any((g) => _globMatch(g, m)),
                  orElse: () => null,
                ) ??
                '';
          } catch (e) {
            if (!mounted) return;
            setState(() => _testResult =
                '拉取上游 /models 失败: $e; 请改填具体模型名 (如 glm-4.5)');
            return;
          }
          if (model.isEmpty) {
            setState(() => _testResult =
                '上游 /models 未列出匹配「${_modelglobsCtrl.text}」的模型; 请改填具体模型名 (如 glm-4.5)');
            return;
          }
        }
        final r = await generateProbe(
          providerId: _protocol,
          apiKey: key,
          model: model,
          baseUrl: base,
        );
        if (!mounted) return;
        setState(() => _testResult = r.ok
            ? '连通正常 (${r.latencyMs}ms) · 模型 $model'
            : '连不上: ${r.errMsg ?? "未知"}');
      } else {
        final r = await client.test(_provider);
        if (!mounted) return;
        setState(() => _testResult = '结果: ${r.result}');
        ref.invalidate(apiKeysListProvider);
      }
    } catch (e) {
      if (!mounted) return;
      setState(() => _testResult = '测试失败: $e');
    }
    messenger.showSnackBar(SnackBar(content: Text(_testResult ?? '')));
  }

  Future<void> _delete() async {
    final client = ref.read(apiKeysClientProvider);
    if (client == null) return;
    final ok = await showDialog<bool>(
      context: context,
      builder: (dctx) => AlertDialog(
        title: Text(widget.isClient ? '删除本机直连凭据?' : '撤销 API Key?'),
        content: const Text('此操作不可恢复, 需要重新录入.'),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(dctx, false),
              child: const Text('取消')),
          TextButton(
            style: TextButton.styleFrom(foregroundColor: BiuTokens.error),
            onPressed: () => Navigator.pop(dctx, true),
            child: Text(widget.isClient ? '删除' : '撤销'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    if (!mounted) return;
    try {
      await client.remove(_provider,
          isClientSide: widget.isClient, id: widget.entry?.id);
      if (!mounted) return;
      ref.invalidate(apiKeysListProvider);
      ref.read(selectedApiKeyProvider.notifier).state = null;
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = '$e');
    }
  }

  @override
  Widget build(BuildContext context) {
    final label = byokProviderLabels[_provider] ?? _provider;
    final hasExisting = widget.entry != null;
    return SingleChildScrollView(
      padding: const EdgeInsets.all(BiuTokens.space5),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(children: [
            providerIcon(_provider, size: 28),
            const SizedBox(width: BiuTokens.space3),
            Text(label,
                style: const TextStyle(
                    fontSize: 18, fontWeight: FontWeight.w700)),
            const SizedBox(width: BiuTokens.space2),
            if (widget.isClient) _LocalBadge() else _CloudBadge(),
          ]),
          const SizedBox(height: BiuTokens.space4),
          // client-side 收口 custom-only (Bug 2): 不再给 provider 下拉. legacy
          // client-side standard 条目 = 死凭据, 显失效提示引导删重建。
          if (_isLegacyClientStandard)
            Container(
              width: double.infinity,
              padding: const EdgeInsets.all(BiuTokens.space3),
              decoration: BoxDecoration(
                color: BiuTokens.error.withValues(alpha: 0.08),
                borderRadius: BorderRadius.circular(6),
                border: Border.all(color: BiuTokens.error.withValues(alpha: 0.4)),
              ),
              child: Text(
                '本机直连仅支持 custom provider. 此「$label」是历史遗留的死凭据'
                '(无可用模型), 请删除后以 custom 重建。',
                style: TextStyle(fontSize: 12, color: BiuTokens.error),
              ),
            ),
          TextField(
            controller: _baseurlCtrl,
            decoration: InputDecoration(
              labelText: widget.isClient
                  ? '上游地址 (Base URL, 必填)'
                  : 'Base URL (仅 custom 必填)',
              hintText: widget.isClient
                  ? 'https://内网代理 或 https://api.anthropic.com'
                  : 'https://new-api.example.com',
            ),
            inputFormatters: [FilteringTextInputFormatter.deny(RegExp(r'\s'))],
          ),
          if (_isCustom) ...[
            const SizedBox(height: BiuTokens.space3),
            DropdownButtonFormField<String>(
              initialValue: _protocol,
              decoration: const InputDecoration(labelText: '协议'),
              items: const [
                DropdownMenuItem(
                    value: 'openai_compat',
                    child: Text('OpenAI 兼容 (new-api / vLLM / DeepSeek 等)')),
                DropdownMenuItem(value: 'anthropic', child: Text('Anthropic')),
                DropdownMenuItem(value: 'google', child: Text('Google')),
              ],
              onChanged: (v) => setState(() => _protocol = v ?? 'openai_compat'),
            ),
            const SizedBox(height: BiuTokens.space3),
            TextField(
              controller: _modelglobsCtrl,
              decoration: InputDecoration(
                labelText: _isCustom ? '所用模型 (必填, 逗号分隔)' : '所用模型 (可选)',
                hintText: 'glm-*  或  glm-4.5,glm-4\n(仅前缀 glm-* 或全 *, 不支持中间通配)',
              ),
            ),
          ],
          const SizedBox(height: BiuTokens.space3),
          TextField(
            controller: _keyCtrl,
            obscureText: _obscure,
            decoration: InputDecoration(
              labelText: widget.isClient
                  ? (hasExisting ? 'API Key (留空保留原密钥)' : 'API Key')
                  : (hasExisting ? '新 API Key (覆盖, 留空不变)' : 'API Key'),
              hintText: 'sk-...',
              prefixText: hasExisting && !widget.isClient
                  ? '当前: ...${widget.entry!.last4}   '
                  : null,
              suffixIcon: IconButton(
                icon: Icon(_obscure
                    ? Icons.visibility_off_outlined
                    : Icons.visibility_outlined),
                onPressed: () => setState(() => _obscure = !_obscure),
              ),
            ),
            inputFormatters: [FilteringTextInputFormatter.deny(RegExp(r'\s'))],
          ),
          const SizedBox(height: BiuTokens.space3),
          TextField(
            controller: _labelCtrl,
            decoration: const InputDecoration(labelText: '备注 (可选)'),
            maxLength: 40,
          ),
          if (_error != null) ...[
            const SizedBox(height: BiuTokens.space2),
            Text(_error!,
                style: TextStyle(color: BiuTokens.error, fontSize: 12)),
          ],
          if (_testResult != null) ...[
            const SizedBox(height: BiuTokens.space2),
            Text(_testResult!,
                style: TextStyle(fontSize: 12, color: BiuTokens.textMuted)),
          ],
          const SizedBox(height: BiuTokens.space4),
          Row(children: [
            OutlinedButton(
              onPressed: (_saving || _isLegacyClientStandard) ? null : _test,
              child: const Text('测试'),
            ),
            const SizedBox(width: BiuTokens.space2),
            FilledButton(
              onPressed: (_saving || _isLegacyClientStandard) ? null : _save,
              child: _saving
                  ? const SizedBox(
                      width: 16,
                      height: 16,
                      child: CircularProgressIndicator(strokeWidth: 2))
                  : const Text('保存'),
            ),
            if (hasExisting || widget.isNew) ...[
              const SizedBox(width: BiuTokens.space2),
              TextButton(
                style: TextButton.styleFrom(foregroundColor: BiuTokens.error),
                onPressed: _delete,
                child: Text(widget.isClient ? '删除' : '撤销'),
              ),
            ],
          ]),
        ],
      ),
    );
  }
}

String _firstConcrete(String globsCsv) {
  for (final g in globsCsv.split(',').map((s) => s.trim())) {
    if (g.isNotEmpty && !g.contains('*')) return g;
  }
  return '';
}

/// 通配 glob 匹配 (与 client_side_resolver._globMatch 同语义).
/// '*' 全匹配; 'foo-*' 前缀匹配; 否则精确相等. **不支持中间通配** (如 'glm-*-mini'
/// 会被当精确串, 永不命中 —— UI hintText 已明示). 测试路径解析通配 globs 到具体模型时用.
bool _globMatch(String g, String model) {
  if (g == '*') return true;
  if (g.endsWith('*')) return model.startsWith(g.substring(0, g.length - 1));
  return g == model;
}
