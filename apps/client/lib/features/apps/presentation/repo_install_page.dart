// RepoInstallPage —— GitHub Repo App 安装确认页（Repo Apps M1.14）。
//
// 表单模式抄 add_webview_dialog.dart：Form + GlobalKey、validator、
// _submitting 提交态、_serverErr 内联错误条。做成子页面而非弹窗 —
// 分析结果 + 不定长 env 表单放不进 480px 固定弹窗。
//
// 三态：
//   1. 分析中   —— repoAnalyzeProvider loading
//   2. 分析失败 —— 展示服务端返回的"不支持"原因（error.message）+ 重试
//   3. 确认表单 —— 分析摘要 + 将执行的 install/start 命令 + env 表单
//
// 机密字段（D9）：标 🔒"仅存本机"，不进 install config、不上服务端；
// 安装成功后暂存 repoAppPendingEnvProvider（纯内存），伪独立窗口首次
// `biu repo-app ensure` 时下发给 CLI 写实例 .env。system 字段由 CLI
// 注入，表单不展示。
//
// 平台门控：仅 macOS / Linux（PlatformCaps.hasRepoAppRunner）；其余平台
// 显示降级说明。

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../app/theme.dart';
import '../../../core/layout/phone_nav.dart';
import '../../../core/platform/platform_caps.dart';
import '../../../data/api/apps_client.dart';
import '../../../data/apps_providers.dart';
import '../../../shared/page_scaffold.dart';
import 'apps_error.dart';

class RepoInstallPage extends ConsumerStatefulWidget {
  const RepoInstallPage({super.key, required this.repoUrl});

  /// 来自路由 query `?url=`；空串 → 先展示 URL 输入态。
  final String repoUrl;

  @override
  ConsumerState<RepoInstallPage> createState() => _RepoInstallPageState();
}

class _RepoInstallPageState extends ConsumerState<RepoInstallPage> {
  final _formKey = GlobalKey<FormState>();
  final _urlCtrl = TextEditingController();

  /// 已进入分析流程的 URL；空 = 还在 URL 输入态。
  late String _url = widget.repoUrl.trim();

  String _refType = 'release';
  bool _submitting = false;
  String? _serverErr;

  /// env 表单的当前值（含默认值初始化）。secret 值也只活在这里 +
  /// 安装成功后的 repoAppPendingEnvProvider（内存）。
  final Map<String, String> _envValues = {};

  @override
  void dispose() {
    _urlCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final caps = ref.watch(platformCapsProvider);
    return PageScaffold(
      title: '安装 GitHub 应用',
      leading: const PhoneBackButton(),
      maxWidth: 720,
      child: !caps.hasRepoAppRunner
          ? const _UnsupportedPlatform()
          : _url.isEmpty
              ? _buildUrlInput()
              : _buildAnalysis(),
    );
  }

  // ── 态 0：URL 输入 ────────────────────────────────────────────

  Widget _buildUrlInput() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        TextField(
          controller: _urlCtrl,
          autofocus: true,
          decoration: const InputDecoration(
            labelText: 'GitHub 仓库地址',
            hintText: 'https://github.com/owner/repo',
            helperText: '将在本机克隆、安装依赖并运行该仓库的服务',
          ),
          keyboardType: TextInputType.url,
          onSubmitted: (_) => _startAnalyze(),
        ),
        if (_serverErr != null) ...[
          const SizedBox(height: BiuTokens.space2),
          Text(
            _serverErr!,
            style: TextStyle(
                color: Theme.of(context).colorScheme.error, fontSize: 12),
          ),
        ],
        const SizedBox(height: BiuTokens.space3),
        FilledButton.icon(
          onPressed: _startAnalyze,
          icon: const Icon(Icons.analytics_outlined, size: 18),
          label: const Text('分析仓库'),
        ),
      ],
    );
  }

  void _startAnalyze() {
    final v = _urlCtrl.text.trim();
    if (v.isEmpty) return;
    final u = Uri.tryParse(v);
    if (u == null ||
        (u.scheme != 'http' && u.scheme != 'https') ||
        u.host.isEmpty) {
      setState(() => _serverErr = 'URL 格式无效，请输入完整的 https 地址');
      return;
    }
    setState(() {
      _serverErr = null;
      _url = v;
    });
  }

  // ── 态 1/2/3：分析中 / 失败 / 确认表单 ─────────────────────────

  Widget _buildAnalysis() {
    final analysisAsync = ref.watch(repoAnalyzeProvider(_url));
    return analysisAsync.when(
      loading: () => const Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            CircularProgressIndicator(),
            SizedBox(height: BiuTokens.space3),
            Text('正在分析仓库…'),
          ],
        ),
      ),
      error: (e, _) => _AnalyzeError(
        url: _url,
        message: humanizeAppsError(context, e),
        onRetry: () => ref.invalidate(repoAnalyzeProvider(_url)),
        onBack: () => setState(() {
          _url = '';
          _urlCtrl.text = widget.repoUrl;
        }),
      ),
      data: (analysis) {
        if (analysis == null) {
          return const Center(child: Text('尚未配置服务器凭据，请先在设置中登录'));
        }
        return _buildConfirmForm(analysis);
      },
    );
  }

  Widget _buildConfirmForm(RepoAnalysis analysis) {
    // 默认值只在首次进入表单时灌入（_envValues 为空），重试 / 重建不
    // 覆盖用户已改的内容。
    if (_envValues.isEmpty) {
      for (final f in analysis.envSchema) {
        if (!f.system && f.defaultValue.isNotEmpty) {
          _envValues[f.name] = f.defaultValue;
        }
      }
    }
    final theme = Theme.of(context);
    final draft = analysis.manifestDraft;
    final stack = analysis.stack;
    final meta = analysis.repoMeta;
    final envFields =
        analysis.envSchema.where((f) => !f.system).toList(growable: false);

    return Form(
      key: _formKey,
      child: ListView(
        children: [
          // ── 分析摘要 ──
          Text(
            draft.title.isEmpty ? draft.identifier : draft.title,
            style: theme.textTheme.titleLarge
                ?.copyWith(fontWeight: FontWeight.w700),
          ),
          const SizedBox(height: 4),
          Text(
            [
              if (draft.version.isNotEmpty) 'v${draft.version}',
              if (meta.stars > 0) '★ ${meta.stars}',
              if (meta.license.isNotEmpty) meta.license,
              if (meta.latestRef.isNotEmpty) '最新 ${meta.latestRef}',
            ].join(' · '),
            style: theme.textTheme.bodySmall
                ?.copyWith(color: theme.colorScheme.onSurfaceVariant),
          ),
          if (draft.description.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space2),
            Text(draft.description),
          ],
          if (analysis.warnings.isNotEmpty) ...[
            const SizedBox(height: BiuTokens.space3),
            _WarningsCard(warnings: analysis.warnings),
          ],
          const SizedBox(height: BiuTokens.space4),

          // ── 将执行的命令 ──
          Text('将在本机执行',
              style: theme.textTheme.titleSmall
                  ?.copyWith(fontWeight: FontWeight.w600)),
          const SizedBox(height: BiuTokens.space2),
          _CommandsCard(stack: stack),
          const SizedBox(height: BiuTokens.space4),

          // ── env 表单 ──
          if (envFields.isNotEmpty) ...[
            Text('配置',
                style: theme.textTheme.titleSmall
                    ?.copyWith(fontWeight: FontWeight.w600)),
            const SizedBox(height: BiuTokens.space2),
            for (final f in envFields) ...[
              TextFormField(
                initialValue: _envValues[f.name] ?? '',
                obscureText: f.secret,
                decoration: InputDecoration(
                  labelText: f.label.isEmpty ? f.name : f.label,
                  helperText: f.secret ? '🔒 仅存本机，不上传服务器' : null,
                ),
                validator: (v) {
                  if (f.optional) return null;
                  if ((v ?? '').trim().isEmpty) return '请填写 ${f.name}';
                  return null;
                },
                onChanged: (v) => _envValues[f.name] = v,
              ),
              const SizedBox(height: BiuTokens.space3),
            ],
          ],

          // ── 版本来源 ──
          Text('版本来源',
              style: theme.textTheme.titleSmall
                  ?.copyWith(fontWeight: FontWeight.w600)),
          const SizedBox(height: BiuTokens.space2),
          SegmentedButton<String>(
            segments: [
              ButtonSegment(
                value: 'release',
                label: Text(meta.latestRef.isEmpty
                    ? '最新 Release'
                    : '最新 Release (${meta.latestRef})'),
              ),
              ButtonSegment(
                value: 'branch',
                label: Text(meta.defaultBranch.isEmpty
                    ? '默认分支'
                    : '默认分支 (${meta.defaultBranch})'),
              ),
            ],
            selected: {_refType},
            onSelectionChanged: (s) => setState(() => _refType = s.first),
          ),

          if (_serverErr != null) ...[
            const SizedBox(height: BiuTokens.space3),
            Text(
              _serverErr!,
              style: TextStyle(
                  color: theme.colorScheme.error, fontSize: 12),
            ),
          ],
          const SizedBox(height: BiuTokens.space4),
          Row(
            children: [
              FilledButton(
                onPressed: _submitting ? null : () => _submit(analysis),
                child: _submitting
                    ? const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('安装'),
              ),
              const SizedBox(width: BiuTokens.space3),
              TextButton(
                onPressed: _submitting
                    ? null
                    : () => setState(() {
                          _url = '';
                          _urlCtrl.text = widget.repoUrl;
                        }),
                child: const Text('换一个仓库'),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Future<void> _submit(RepoAnalysis analysis) async {
    if (!(_formKey.currentState?.validate() ?? false)) return;
    final client = ref.read(appsClientProvider);
    final token = ref.read(appsBearerProvider);
    if (client == null || token == null) {
      setState(() => _serverErr = '尚未配置服务器凭据');
      return;
    }
    setState(() {
      _submitting = true;
      _serverErr = null;
    });
    try {
      // D9 红线：机密不进 install config（不上服务端）；非机密配置随
      // install 落库。secret 值安装成功后走 repoAppPendingEnvProvider
      // 内存接力，由窗口页首次 ensure 时下发给本机 CLI。
      final config = <String, dynamic>{};
      final secrets = <String, String>{};
      for (final f in analysis.envSchema) {
        if (f.system) continue;
        final v = (_envValues[f.name] ?? '').trim();
        if (v.isEmpty) continue;
        if (f.secret) {
          secrets[f.name] = v;
        } else {
          config[f.name] = v;
        }
      }
      final install = await client.installRepo(
        repoUrl: _url,
        refType: _refType,
        config: config,
        token: token,
      );
      if (!mounted) return;
      if (secrets.isNotEmpty) {
        final cur = ref.read(repoAppPendingEnvProvider);
        ref.read(repoAppPendingEnvProvider.notifier).state = {
          ...cur,
          install.id: secrets,
        };
      }
      // 照模板：catalog 徽章 + user scope 列表。
      ref.invalidate(appsCatalogProvider);
      ref.invalidateInstallScope('user');
      if (!mounted) return;
      context.pushReplacement('/apps/detail/${install.identifier}');
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _serverErr = humanizeAppsError(context, e);
        _submitting = false;
      });
    }
  }
}

class _UnsupportedPlatform extends StatelessWidget {
  const _UnsupportedPlatform();

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space6),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.desktop_mac_outlined,
                size: 32, color: theme.colorScheme.onSurfaceVariant),
            const SizedBox(height: BiuTokens.space3),
            Text('当前平台暂不支持安装 GitHub 应用',
                style: theme.textTheme.titleMedium),
            const SizedBox(height: BiuTokens.space2),
            Text(
              'GitHub 应用需要在本机克隆并运行仓库，目前仅 macOS / Linux 客户端可用。',
              textAlign: TextAlign.center,
              style: theme.textTheme.bodySmall
                  ?.copyWith(color: theme.colorScheme.onSurfaceVariant),
            ),
          ],
        ),
      ),
    );
  }
}

class _AnalyzeError extends StatelessWidget {
  const _AnalyzeError({
    required this.url,
    required this.message,
    required this.onRetry,
    required this.onBack,
  });

  final String url;
  final String message;
  final VoidCallback onRetry;
  final VoidCallback onBack;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(BiuTokens.space6),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.error_outline,
                size: 32, color: theme.colorScheme.error),
            const SizedBox(height: BiuTokens.space3),
            Text('分析失败', style: theme.textTheme.titleMedium),
            const SizedBox(height: BiuTokens.space2),
            SelectableText(url,
                style: theme.textTheme.bodySmall,
                textAlign: TextAlign.center),
            const SizedBox(height: BiuTokens.space2),
            // 服务端返回的"不支持的项目类型"等原因原样展示。
            Text(
              message,
              textAlign: TextAlign.center,
              style: theme.textTheme.bodySmall
                  ?.copyWith(color: theme.colorScheme.error),
            ),
            const SizedBox(height: BiuTokens.space3),
            Wrap(
              spacing: BiuTokens.space2,
              children: [
                FilledButton.icon(
                  onPressed: onRetry,
                  icon: const Icon(Icons.refresh, size: 18),
                  label: const Text('重试'),
                ),
                TextButton(onPressed: onBack, child: const Text('换一个仓库')),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class _WarningsCard extends StatelessWidget {
  const _WarningsCard({required this.warnings});
  final List<String> warnings;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.all(BiuTokens.space2),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHigh,
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          for (final w in warnings)
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Icon(Icons.warning_amber_rounded,
                    size: 14, color: theme.colorScheme.onSurfaceVariant),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(w, style: theme.textTheme.bodySmall),
                ),
              ],
            ),
        ],
      ),
    );
  }
}

class _CommandsCard extends StatelessWidget {
  const _CommandsCard({required this.stack});
  final RepoStack stack;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    Widget row(String label, String value) {
      if (value.isEmpty) return const SizedBox.shrink();
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 2),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 72,
              child: Text(label,
                  style: theme.textTheme.bodySmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant)),
            ),
            Expanded(
              child: SelectableText(
                value,
                style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
              ),
            ),
          ],
        ),
      );
    }

    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(BiuTokens.space3),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHigh,
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (stack.kind.isNotEmpty) row('技术栈', stack.kind),
          row('安装依赖', stack.installCmd),
          row('启动', stack.startCmd),
          if (stack.port > 0) row('端口', '${stack.port}'),
          row('健康检查', stack.healthPath),
          for (final r in stack.runtimeReqs)
            row(
              '运行时',
              '${r.name}${r.version.isEmpty ? '' : ' ${r.version}'}'
                  '${r.autoInstall ? '（缺失时将自动安装）' : ''}',
            ),
        ],
      ),
    );
  }
}
