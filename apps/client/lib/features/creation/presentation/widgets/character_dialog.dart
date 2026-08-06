// CharacterDialog — 数字人角色选择 (含系统内置 + 自己创建).
//
// 通过 [pickCharacter] 函数调用; 返回选中的 CharacterEntry 或 null (取消).
// 列表头有「+ 新建」按钮, 点击展开内联表单创建私有角色 (name + voice 默认).
//
// 数据: aigcCharactersProvider (autoDispose; sheet 关闭后释放).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../../app/theme.dart';
import '../../application/aigc_providers.dart';
import '../../data/error_translator.dart';
import '../../domain/character.dart';

Future<CharacterEntry?> pickCharacter(BuildContext context) {
  return showModalBottomSheet<CharacterEntry?>(
    context: context,
    isScrollControlled: true,
    backgroundColor: BiuTokens.surface,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(BiuTokens.radiusLg)),
    ),
    builder: (_) => DraggableScrollableSheet(
      initialChildSize: 0.7,
      maxChildSize: 0.95,
      minChildSize: 0.4,
      expand: false,
      builder: (_, ctrl) => _CharacterSheet(scrollController: ctrl),
    ),
  );
}

class _CharacterSheet extends ConsumerStatefulWidget {
  const _CharacterSheet({required this.scrollController});
  final ScrollController scrollController;

  @override
  ConsumerState<_CharacterSheet> createState() => _CharacterSheetState();
}

class _CharacterSheetState extends ConsumerState<_CharacterSheet> {
  bool _creating = false;
  final _nameCtrl = TextEditingController();
  String _voiceDefault = '';
  bool _busy = false;

  @override
  void dispose() {
    _nameCtrl.dispose();
    super.dispose();
  }

  Future<void> _create() async {
    final client = ref.read(aigcClientProvider);
    if (client == null) return;
    final name = _nameCtrl.text.trim();
    if (name.isEmpty) return;
    setState(() => _busy = true);
    try {
      final c = await client.createCharacter(
        name: name,
        voiceDefault: _voiceDefault,
      );
      ref.invalidate(aigcCharactersProvider);
      if (!mounted) return;
      Navigator.of(context).pop(c);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(translateError(e)), backgroundColor: BiuTokens.error),
      );
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _delete(CharacterEntry c) async {
    final client = ref.read(aigcClientProvider);
    if (client == null || c.isSystem) return;
    try {
      await client.deleteCharacter(c.id);
      ref.invalidate(aigcCharactersProvider);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(translateError(e)), backgroundColor: BiuTokens.error),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final asyncList = ref.watch(aigcCharactersProvider);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 16),
      child: Column(
        children: [
          Container(
            width: 36,
            height: 4,
            decoration: BoxDecoration(
              color: BiuTokens.border,
              borderRadius: BorderRadius.circular(2),
            ),
          ),
          const SizedBox(height: 12),
          Row(
            children: [
              Text(
                '选择数字人',
                style: TextStyle(
                  fontSize: 16,
                  fontWeight: FontWeight.w600,
                  color: BiuTokens.text,
                ),
              ),
              const Spacer(),
              if (!_creating)
                TextButton.icon(
                  onPressed: () => setState(() => _creating = true),
                  icon: const Icon(Icons.add, size: 16),
                  label: const Text('新建'),
                ),
            ],
          ),
          if (_creating) _buildCreateForm(),
          const SizedBox(height: 8),
          Expanded(
            child: asyncList.when(
              loading: () => const Center(child: CircularProgressIndicator()),
              error: (e, _) => Text('$e', style: TextStyle(color: BiuTokens.error)),
              data: (raw) {
                final chars = raw.whereType<CharacterEntry>().toList();
                if (chars.isEmpty) {
                  return Center(
                    child: Text('暂无角色', style: TextStyle(color: BiuTokens.textMuted)),
                  );
                }
                return ListView.separated(
                  controller: widget.scrollController,
                  itemCount: chars.length,
                  separatorBuilder: (_, i) => Divider(
                    height: 1,
                    color: BiuTokens.borderSubtle,
                  ),
                  itemBuilder: (_, i) => _CharacterTile(
                    character: chars[i],
                    onTap: () => Navigator.of(context).pop(chars[i]),
                    onDelete: chars[i].isSystem ? null : () => _delete(chars[i]),
                  ),
                );
              },
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildCreateForm() {
    return Container(
      margin: const EdgeInsets.only(top: 8),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: BiuTokens.surfaceMuted,
        borderRadius: BorderRadius.circular(BiuTokens.radiusMd),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          TextField(
            controller: _nameCtrl,
            autofocus: true,
            decoration: const InputDecoration(
              hintText: '角色名 (如「主播 A」)',
              isDense: true,
            ),
            maxLength: 64,
            buildCounter: (_, {required currentLength, required isFocused, maxLength}) =>
                null,
          ),
          const SizedBox(height: 8),
          _VoiceQuickPicker(
            current: _voiceDefault,
            onPick: (v) => setState(() => _voiceDefault = v),
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              const Spacer(),
              TextButton(
                onPressed: _busy ? null : () => setState(() => _creating = false),
                child: const Text('取消'),
              ),
              const SizedBox(width: 8),
              FilledButton(
                onPressed: _busy ? null : _create,
                child: _busy
                    ? const SizedBox(
                        width: 14,
                        height: 14,
                        child: CircularProgressIndicator(
                          strokeWidth: 1.5,
                          color: Colors.white,
                        ),
                      )
                    : const Text('创建'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _CharacterTile extends StatelessWidget {
  const _CharacterTile({
    required this.character,
    required this.onTap,
    this.onDelete,
  });
  final CharacterEntry character;
  final VoidCallback onTap;
  final VoidCallback? onDelete;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      onTap: onTap,
      leading: CircleAvatar(
        backgroundColor: BiuTokens.purpleSoft,
        child: Text(
          character.name.isNotEmpty ? character.name[0] : '?',
          style: TextStyle(
            color: BiuTokens.purple,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
      title: Text(
        character.name,
        style: TextStyle(
          fontSize: 14,
          fontWeight: FontWeight.w600,
          color: BiuTokens.text,
        ),
      ),
      subtitle: Row(
        children: [
          if (character.isSystem)
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
              decoration: BoxDecoration(
                color: BiuTokens.purpleSoft,
                borderRadius: BorderRadius.circular(BiuTokens.radiusFull),
              ),
              child: Text(
                '内置',
                style: TextStyle(
                  fontSize: 9,
                  fontWeight: FontWeight.w600,
                  color: BiuTokens.purple,
                ),
              ),
            ),
          if (character.voiceDefault.isNotEmpty) ...[
            const SizedBox(width: 6),
            Text(
              character.voiceDefault,
              style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
            ),
          ],
        ],
      ),
      trailing: onDelete == null
          ? null
          : IconButton(
              onPressed: onDelete,
              icon: Icon(Icons.delete_outline, size: 18, color: BiuTokens.textMuted),
              tooltip: '删除',
            ),
    );
  }
}

/// _VoiceQuickPicker — 创建表单内嵌的简易音色选择 (单行 chip + 「更多…」打开 voice sheet).
class _VoiceQuickPicker extends ConsumerWidget {
  const _VoiceQuickPicker({required this.current, required this.onPick});
  final String current;
  final ValueChanged<String> onPick;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncList = ref.watch(aigcVoicesProvider(null));
    return asyncList.when(
      loading: () => const SizedBox(
        height: 24,
        child: Center(
          child: SizedBox(
            width: 12,
            height: 12,
            child: CircularProgressIndicator(strokeWidth: 1.5),
          ),
        ),
      ),
      error: (e, _) => const SizedBox.shrink(),
      data: (raw) {
        final voices = raw.whereType<VoiceEntry>().take(4).toList();
        return Wrap(
          spacing: 4,
          runSpacing: 4,
          children: [
            for (final v in voices)
              ChoiceChip(
                label: Text(v.name, style: const TextStyle(fontSize: 11)),
                selected: v.id == current,
                onSelected: (s) => onPick(s ? v.id : ''),
                visualDensity: VisualDensity.compact,
              ),
            ActionChip(
              label: const Text('更多…', style: TextStyle(fontSize: 11)),
              avatar: const Icon(Icons.more_horiz, size: 12),
              onPressed: () async {
                final v = await pickVoice(context);
                if (v != null) onPick(v.id);
              },
              visualDensity: VisualDensity.compact,
            ),
          ],
        );
      },
    );
  }
}

// ─── Voice picker sheet (公开 API, character_dialog 内嵌也用) ────────

Future<VoiceEntry?> pickVoice(BuildContext context, {String? provider}) {
  return showModalBottomSheet<VoiceEntry?>(
    context: context,
    isScrollControlled: true,
    backgroundColor: BiuTokens.surface,
    shape: const RoundedRectangleBorder(
      borderRadius: BorderRadius.vertical(top: Radius.circular(BiuTokens.radiusLg)),
    ),
    builder: (_) => DraggableScrollableSheet(
      initialChildSize: 0.6,
      maxChildSize: 0.95,
      minChildSize: 0.4,
      expand: false,
      builder: (_, ctrl) => _VoiceSheet(scrollController: ctrl, provider: provider),
    ),
  );
}

class _VoiceSheet extends ConsumerWidget {
  const _VoiceSheet({required this.scrollController, this.provider});
  final ScrollController scrollController;
  final String? provider;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncList = ref.watch(aigcVoicesProvider(provider));
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 16),
      child: Column(
        children: [
          Container(
            width: 36,
            height: 4,
            decoration: BoxDecoration(
              color: BiuTokens.border,
              borderRadius: BorderRadius.circular(2),
            ),
          ),
          const SizedBox(height: 12),
          Align(
            alignment: Alignment.centerLeft,
            child: Text(
              '选择音色',
              style: TextStyle(
                fontSize: 16,
                fontWeight: FontWeight.w600,
                color: BiuTokens.text,
              ),
            ),
          ),
          const SizedBox(height: 12),
          Expanded(
            child: asyncList.when(
              loading: () => const Center(child: CircularProgressIndicator()),
              error: (e, _) => Text('$e', style: TextStyle(color: BiuTokens.error)),
              data: (raw) {
                final voices = raw.whereType<VoiceEntry>().toList();
                if (voices.isEmpty) {
                  return Center(
                    child: Text('暂无音色', style: TextStyle(color: BiuTokens.textMuted)),
                  );
                }
                return ListView.builder(
                  controller: scrollController,
                  itemCount: voices.length,
                  itemBuilder: (_, i) {
                    final v = voices[i];
                    return ListTile(
                      onTap: () => Navigator.of(context).pop(v),
                      leading: Icon(
                        v.gender == 'female'
                            ? Icons.face_3_outlined
                            : (v.gender == 'male' ? Icons.face_outlined : Icons.audio_file),
                        color: BiuTokens.purple,
                      ),
                      title: Text(
                        v.name,
                        style: TextStyle(fontWeight: FontWeight.w600, color: BiuTokens.text),
                      ),
                      subtitle: Text(
                        '${v.provider} · ${v.language} · ${v.style}',
                        style: TextStyle(fontSize: 11, color: BiuTokens.textMuted),
                      ),
                      trailing: v.sampleUrl.isEmpty
                          ? null
                          : IconButton(
                              icon: const Icon(Icons.play_circle_outline, size: 20),
                              onPressed: () {/* v2: 播试听 */},
                            ),
                    );
                  },
                );
              },
            ),
          ),
        ],
      ),
    );
  }
}
