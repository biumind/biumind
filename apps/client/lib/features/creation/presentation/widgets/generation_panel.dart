// GenerationPanel — 创作模块核心 composer.
//
// 布局 (zhiying-portal 对齐, 自上而下):
//   1. TabStrip: 视频/图片/数字人/爆款解析 + 跳转 chat
//   2. 模型选择 chip 行 (按 type 拉 aigcModelsProvider)
//   3. Prompt 输入框 (多行)
//   4. 参数 chip 行: 比例 / 分辨率 / 时长 / 数量 (按 selected model 显示)
//   5. 上传行: 首帧 / 尾帧 / 参考图 (按 model.feature 显示)
//   6. Switch 行: AI 优化 / 公开
//   7. 提交按钮 (圆形主色) + 报价 + 状态文字
//
// 数据绑定:
//   - watch generationFormControllerProvider 读 state
//   - read .notifier 改字段
//   - submit 走 tasksControllerProvider.notifier.submit (走乐观更新链路)

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../../../l10n/app_localizations.dart';
import '../../application/aigc_providers.dart';
import '../../application/credits_controller.dart';
import '../../application/generation_form_controller.dart';
import '../../application/tasks_controller.dart';
import '../../data/error_translator.dart';
import '../../domain/ai_model.dart';
import 'image_url_field.dart';
import 'param_chip.dart';
import 'tab_strip.dart';

class GenerationPanel extends ConsumerWidget {
  const GenerationPanel({super.key, this.dense = false});

  /// dense=true: 紧凑模式 (Hero 下方 / sheet 内). 默认 false 为 studio 主面板.
  final bool dense;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final form = ref.watch(generationFormControllerProvider);
    final t = AppLocalizations.of(context)!;

    return Container(
      decoration: BoxDecoration(
        color: BiuTokens.surface,
        borderRadius: BorderRadius.circular(BiuTokens.radiusLg),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      padding: EdgeInsets.all(dense ? 12 : 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          TabStrip(
            current: form.type,
            onSelect: (tt) => ref
                .read(generationFormControllerProvider.notifier)
                .selectType(tt),
          ),
          const SizedBox(height: 12),
          // 数字人暂无生成链路 (model-relay 无 adaptor, worker 直连已删) →
          // "即将上线"卡片, 从源头杜绝提交; 服务端亦对该类型返 501 兜底。
          if (form.type == GenerationType.digitalHuman)
            _ComingSoonCard(type: form.type)
          // 爆款解析: 贴短视频直链 → 转写 + 拆解。复用模型选择 + 提交行。
          else if (form.type == GenerationType.hotparse) ...[
            _ModelSelector(form: form),
            const SizedBox(height: 12),
            _HotparseUrlField(form: form),
            const SizedBox(height: 10),
            _SwitchRow(form: form),
            const SizedBox(height: 14),
            _SubmitRow(form: form, hint: '开始解析'),
          ]
          else ...[
            _ModelSelector(form: form),
            const SizedBox(height: 12),
            _PromptField(form: form),
            const SizedBox(height: 10),
            _ParamRow(form: form),
            const SizedBox(height: 8),
            _UploadRow(form: form),
            const SizedBox(height: 8),
            _SwitchRow(form: form),
            const SizedBox(height: 14),
            _SubmitRow(form: form, hint: t.creationSubmit),
          ],
        ],
      ),
    );
  }
}

// ─── 模型选择 ─────────────────────────────────

class _ModelSelector extends ConsumerWidget {
  const _ModelSelector({required this.form});
  final GenerationFormState form;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncModels = ref.watch(aigcModelsProvider(form.type.wire));
    return asyncModels.when(
      loading: () => const SizedBox(
        height: 32,
        child: Center(
          child: SizedBox(
            width: 14,
            height: 14,
            child: CircularProgressIndicator(strokeWidth: 1.5),
          ),
        ),
      ),
      error: (e, _) => Text(
        '$e',
        style: TextStyle(fontSize: 12, color: BiuTokens.error),
      ),
      data: (rawList) {
        final models = rawList
            .whereType<AiModel>()
            .toList(); // provider 已 fromJson, 但 raw List<dynamic>
        if (models.isEmpty) {
          return Text(
            '暂无可用模型',
            style: TextStyle(fontSize: 12, color: BiuTokens.textMuted),
          );
        }
        final options = models
            .map((m) => ParamChipOption<String>(
                  value: m.code,
                  label: m.displayName,
                  secondary: '${m.priceCredits} 积分',
                ))
            .toList();
        return Align(
          alignment: Alignment.centerLeft,
          child: ParamChip<String>(
            icon: Icons.auto_awesome_outlined,
            label: '选择模型',
            value: form.modelCode,
            options: options,
            sheetTitle: '选择模型',
            onChanged: (code) {
              final m = models.firstWhere((x) => x.code == code);
              ref.read(generationFormControllerProvider.notifier).selectModel(m);
            },
          ),
        );
      },
    );
  }
}

// ─── Prompt ─────────────────────────────────

class _PromptField extends ConsumerStatefulWidget {
  const _PromptField({required this.form});
  final GenerationFormState form;

  @override
  ConsumerState<_PromptField> createState() => _PromptFieldState();
}

class _PromptFieldState extends ConsumerState<_PromptField> {
  late final TextEditingController _ctrl;

  @override
  void initState() {
    super.initState();
    _ctrl = TextEditingController(text: widget.form.prompt);
  }

  @override
  void didUpdateWidget(covariant _PromptField old) {
    super.didUpdateWidget(old);
    if (widget.form.prompt != _ctrl.text) {
      _ctrl.value = TextEditingValue(
        text: widget.form.prompt,
        selection: TextSelection.collapsed(offset: widget.form.prompt.length),
      );
    }
  }

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = AppLocalizations.of(context)!;
    return TextField(
      controller: _ctrl,
      minLines: 3,
      maxLines: 6,
      maxLength: 2000,
      buildCounter: (_, {required currentLength, required isFocused, maxLength}) =>
          null,
      decoration: InputDecoration(
        hintText: t.creationPromptHint,
      ),
      onChanged: (v) =>
          ref.read(generationFormControllerProvider.notifier).setPrompt(v),
    );
  }
}

// ─── 爆款解析: 视频链接输入 ───────────────────

class _HotparseUrlField extends ConsumerStatefulWidget {
  const _HotparseUrlField({required this.form});
  final GenerationFormState form;

  @override
  ConsumerState<_HotparseUrlField> createState() => _HotparseUrlFieldState();
}

class _HotparseUrlFieldState extends ConsumerState<_HotparseUrlField> {
  late final TextEditingController _ctrl;

  @override
  void initState() {
    super.initState();
    _ctrl = TextEditingController(text: widget.form.hotparseSourceUrl ?? '');
  }

  @override
  void didUpdateWidget(covariant _HotparseUrlField old) {
    super.didUpdateWidget(old);
    final v = widget.form.hotparseSourceUrl ?? '';
    if (v != _ctrl.text) {
      _ctrl.value = TextEditingValue(
        text: v,
        selection: TextSelection.collapsed(offset: v.length),
      );
    }
  }

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final notifier = ref.read(generationFormControllerProvider.notifier);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        TextField(
          controller: _ctrl,
          keyboardType: TextInputType.url,
          decoration: const InputDecoration(
            prefixIcon: Icon(Icons.link, size: 18),
            hintText: '粘贴 B站 / 抖音 链接,或视频直链 (mp4/m3u8)',
          ),
          onChanged: notifier.setHotparseSourceUrl,
        ),
        Padding(
          padding: const EdgeInsets.only(top: 6, left: 4),
          child: Text(
            '支持 B站 / 抖音 分享链与公网视频直链;小红书解析即将上线。',
            style: TextStyle(fontSize: 11, color: BiuTokens.textMuted, height: 1.4),
          ),
        ),
      ],
    );
  }
}

// ─── 参数 chip 行 ─────────────────────────────

class _ParamRow extends ConsumerWidget {
  const _ParamRow({required this.form});
  final GenerationFormState form;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncModels = ref.watch(aigcModelsProvider(form.type.wire));
    final notifier = ref.read(generationFormControllerProvider.notifier);

    final models = asyncModels.maybeWhen(
      data: (l) => l.whereType<AiModel>().toList(),
      orElse: () => const <AiModel>[],
    );
    final model = models.firstWhere(
      (m) => m.code == form.modelCode,
      orElse: () =>
          const AiModel(code: '', type: '', displayName: '', providerCode: '', priceCredits: 0),
    );
    if (model.code.isEmpty) return const SizedBox.shrink();

    final chips = <Widget>[];

    // 比例
    if (model.aspectRatios.isNotEmpty) {
      chips.add(ParamChip<String>(
        icon: Icons.aspect_ratio,
        label: '比例',
        value: form.aspectRatio,
        sheetTitle: '画面比例',
        options: model.aspectRatios
            .map((o) => ParamChipOption<String>(
                  value: o.key,
                  label: o.label,
                  secondary: o.value.isEmpty ? null : o.value,
                ))
            .toList(),
        onChanged: notifier.setAspectRatio,
      ));
    }

    // 分辨率
    if (model.resolutions.isNotEmpty) {
      chips.add(ParamChip<String>(
        icon: Icons.high_quality_outlined,
        label: '分辨率',
        value: form.resolution,
        sheetTitle: '分辨率',
        options: model.resolutions
            .map((o) => ParamChipOption<String>(value: o.key, label: o.label))
            .toList(),
        onChanged: notifier.setResolution,
      ));
    }

    // 时长 (视频)
    final dur = model.duration;
    if (dur != null) {
      chips.add(ParamChip<int>(
        icon: Icons.timer_outlined,
        label: '时长',
        value: form.durationSeconds,
        sheetTitle: '视频时长',
        options: dur.steps
            .map((s) => ParamChipOption<int>(value: s, label: '$s 秒'))
            .toList(),
        onChanged: notifier.setDurationSeconds,
      ));
    }

    // 数量
    chips.add(ParamChip<int>(
      icon: Icons.numbers,
      label: '数量',
      value: form.numOutputs,
      sheetTitle: '生成数量',
      options: const [
        ParamChipOption<int>(value: 1, label: '1 张'),
        ParamChipOption<int>(value: 2, label: '2 张'),
        ParamChipOption<int>(value: 4, label: '4 张'),
      ],
      onChanged: notifier.setNumOutputs,
    ));

    return Wrap(
      spacing: 6,
      runSpacing: 6,
      children: chips,
    );
  }
}

// ─── 上传行 ─────────────────────────────────

class _UploadRow extends ConsumerWidget {
  const _UploadRow({required this.form});
  final GenerationFormState form;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncModels = ref.watch(aigcModelsProvider(form.type.wire));
    final notifier = ref.read(generationFormControllerProvider.notifier);

    final models = asyncModels.maybeWhen(
      data: (l) => l.whereType<AiModel>().toList(),
      orElse: () => const <AiModel>[],
    );
    final model = models.firstWhere(
      (m) => m.code == form.modelCode,
      orElse: () =>
          const AiModel(code: '', type: '', displayName: '', providerCode: '', priceCredits: 0),
    );
    if (model.code.isEmpty) return const SizedBox.shrink();

    final t = AppLocalizations.of(context)!;
    final widgets = <Widget>[];

    if (model.supportsFirstFrame) {
      widgets.add(ImageUrlField(
        label: t.creationFirstFrame,
        value: form.firstFrameUrl,
        icon: Icons.start,
        onChanged: notifier.setFirstFrame,
      ));
    }
    if (model.supportsLastFrame) {
      widgets.add(ImageUrlField(
        label: t.creationLastFrame,
        value: form.lastFrameUrl,
        icon: Icons.last_page,
        onChanged: notifier.setLastFrame,
      ));
    }
    if (model.supportsReferenceImage) {
      widgets.add(ImageUrlListField(
        label: t.creationReferenceImage,
        values: form.referenceImageUrls,
        max: model.referenceImageCount > 0 ? model.referenceImageCount : 5,
        onAdd: (u) => notifier.addReferenceImage(
          u,
          max: model.referenceImageCount > 0 ? model.referenceImageCount : 5,
        ),
        onRemove: notifier.removeReferenceImage,
      ));
    }
    if (widgets.isEmpty) return const SizedBox.shrink();
    return Wrap(spacing: 6, runSpacing: 6, children: widgets);
  }
}

// ─── 即将上线门禁卡 ────────────────────────
//
// 数字人暂无可用生成链路 (见 build() 处注释)。选中该 tab 时渲染本卡片取代
// composer, 不提供任何提交入口。功能上线时:接 model-relay digital_human
// adaptor + 还原角色/音色选择行 (历史实现见本文件 git log: _CharacterRow /
// _PickerChip)。
// (爆款解析已落地,见 _HotparseUrlField + hotparse_result_view.dart)

class _ComingSoonCard extends StatelessWidget {
  const _ComingSoonCard({required this.type});
  final GenerationType type;

  @override
  Widget build(BuildContext context) {
    final isDigitalHuman = type == GenerationType.digitalHuman;
    final title = isDigitalHuman ? '数字人合成' : '爆款解析';
    final desc = isDigitalHuman
        ? '上传形象 + 选择音色, 一键生成口播数字人视频。功能打磨中, 即将上线。'
        : '粘贴热门短视频链接, 自动拆解文案 / 分镜 / 脚本。功能打磨中, 即将上线。';
    final icon = isDigitalHuman
        ? Icons.person_outline
        : Icons.local_fire_department_outlined;
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 28),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
        border: Border.all(color: BiuTokens.borderSubtle),
      ),
      child: Column(
        children: [
          Icon(icon, size: 32, color: BiuTokens.textMuted),
          const SizedBox(height: 12),
          Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                title,
                style: TextStyle(
                  fontSize: 15,
                  fontWeight: FontWeight.w600,
                  color: BiuTokens.text,
                ),
              ),
              const SizedBox(width: 8),
              Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
                decoration: BoxDecoration(
                  color: BiuTokens.purpleSoft,
                  borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
                ),
                child: Text(
                  '即将上线',
                  style: TextStyle(
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                    color: BiuTokens.purple,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            desc,
            textAlign: TextAlign.center,
            style: TextStyle(
              fontSize: 12,
              height: 1.5,
              color: BiuTokens.textSecondary,
            ),
          ),
        ],
      ),
    );
  }
}

// ─── Switch 行 ─────────────────────────────

class _SwitchRow extends ConsumerWidget {
  const _SwitchRow({required this.form});
  final GenerationFormState form;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final t = AppLocalizations.of(context)!;
    final notifier = ref.read(generationFormControllerProvider.notifier);
    return Row(
      children: [
        _MiniSwitch(
          label: t.creationAiOptimize,
          value: form.aiOptimize,
          onChanged: (_) => notifier.toggleAiOptimize(),
        ),
        const SizedBox(width: 12),
        _MiniSwitch(
          label: t.creationSharePublic,
          value: form.isPublic,
          onChanged: (_) => notifier.toggleIsPublic(),
        ),
      ],
    );
  }
}

class _MiniSwitch extends StatelessWidget {
  const _MiniSwitch({
    required this.label,
    required this.value,
    required this.onChanged,
  });
  final String label;
  final bool value;
  final ValueChanged<bool> onChanged;

  @override
  Widget build(BuildContext context) {
    return InkWell(
      borderRadius: BorderRadius.circular(BiuTokens.radiusSm),
      onTap: () => onChanged(!value),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 4),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Transform.scale(
              scale: 0.75,
              child: Switch(value: value, onChanged: onChanged),
            ),
            Text(
              label,
              style: TextStyle(
                fontSize: 12,
                color: value ? BiuTokens.text : BiuTokens.textSecondary,
                fontWeight: value ? FontWeight.w600 : FontWeight.w500,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

// ─── 提交按钮 ─────────────────────────────

class _SubmitRow extends ConsumerStatefulWidget {
  const _SubmitRow({required this.form, required this.hint});
  final GenerationFormState form;
  final String hint;

  @override
  ConsumerState<_SubmitRow> createState() => _SubmitRowState();
}

class _SubmitRowState extends ConsumerState<_SubmitRow> {
  bool _busy = false;

  @override
  Widget build(BuildContext context) {
    final form = widget.form;
    final asyncModels = ref.watch(aigcModelsProvider(form.type.wire));
    final price = asyncModels.maybeWhen(
      data: (l) {
        for (final raw in l) {
          if (raw is AiModel && raw.code == form.modelCode) {
            return raw.priceCredits;
          }
        }
        return 0;
      },
      orElse: () => 0,
    );

    final disabled = !form.canSubmit || _busy;

    return Row(
      children: [
        if (price > 0)
          Padding(
            padding: const EdgeInsets.only(right: 12),
            child: Text(
              AppLocalizations.of(context)!.creationCreditCost(price),
              style: TextStyle(fontSize: 12, color: BiuTokens.textSecondary),
            ),
          ),
        const Spacer(),
        FilledButton.icon(
          icon: _busy
              ? const SizedBox(
                  width: 14,
                  height: 14,
                  child: CircularProgressIndicator(
                    strokeWidth: 1.5,
                    color: Colors.white,
                  ),
                )
              : const Icon(Icons.send_rounded, size: 16),
          label: Text(widget.hint),
          style: FilledButton.styleFrom(
            backgroundColor: disabled ? BiuTokens.textMuted : BiuTokens.purple,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
            ),
            padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 12),
          ),
          onPressed: disabled ? null : () => _submit(context),
        ),
      ],
    );
  }

  Future<void> _submit(BuildContext context) async {
    final form = widget.form;
    final tasks = ref.read(tasksControllerProvider.notifier);
    setState(() => _busy = true);
    try {
      await tasks.submit(
        type: form.type.wire,
        modelCode: form.modelCode!,
        prompt: form.prompt,
        params: form.buildParams(),
        negativePrompt: form.negativePrompt.isEmpty ? null : form.negativePrompt,
        isPublic: form.isPublic,
      );
      if (!context.mounted) return;
      // submit 成功 = identity 已经 Consume 扣费. 立即刷新 sidebar 余额,
      // 避免 5min 缓存继续显示扣费前的数字.
      ref.invalidate(creditsBalanceProvider);
      ref.read(generationFormControllerProvider.notifier).resetAfterSubmit();
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(AppLocalizations.of(context)!.creationCardQueued),
          duration: const Duration(seconds: 2),
        ),
      );
    } catch (e) {
      if (!context.mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(translateError(e)),
          backgroundColor: BiuTokens.error,
        ),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }
}
