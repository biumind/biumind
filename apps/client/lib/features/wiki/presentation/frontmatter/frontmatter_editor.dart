// FrontmatterEditor — structured form for page metadata.
//
// Surfaces the well-known fields (title / type / created / description
// / origin / tags / sources / related) as proper widgets, then lists
// every other key in an "extras" expansion where the user can rename
// keys, edit values, or add brand-new ones.
//
// Stateless from the parent's perspective: receives the current
// frontmatter map + an `onChanged(newMap)` callback. Every edit
// produces a fresh map.

import 'package:flutter/material.dart';

import '../../../../app/theme.dart';
import 'frontmatter_helpers.dart';

class FrontmatterEditor extends StatefulWidget {
  const FrontmatterEditor({
    super.key,
    required this.frontmatter,
    required this.onChanged,
  });
  final Map<String, dynamic> frontmatter;
  final ValueChanged<Map<String, dynamic>> onChanged;

  @override
  State<FrontmatterEditor> createState() => _FrontmatterEditorState();
}

class _FrontmatterEditorState extends State<FrontmatterEditor> {
  // ── Controllers for first-class fields ────────────────────────
  final _title = TextEditingController();
  final _desc = TextEditingController();
  final _created = TextEditingController();
  final _origin = TextEditingController();
  final _customType = TextEditingController();

  static const _customTypeSentinel = '__custom__';
  String? _typeValue; // null = unset; '__custom__' = freeform

  /// Last hydrated map signature — guards against re-hydrating on our
  /// own onChanged echo (which would clobber the user's caret mid-typing).
  String _lastHydrated = '';

  @override
  void initState() {
    super.initState();
    _hydrate();
  }

  @override
  void didUpdateWidget(covariant FrontmatterEditor old) {
    super.didUpdateWidget(old);
    final sig = _signature(widget.frontmatter);
    if (sig != _lastHydrated && !_isLocalEcho(widget.frontmatter)) {
      _hydrate();
    }
  }

  @override
  void dispose() {
    _title.dispose();
    _desc.dispose();
    _created.dispose();
    _origin.dispose();
    _customType.dispose();
    super.dispose();
  }

  bool _isLocalEcho(Map<String, dynamic> next) =>
      stringFieldValue(next['title']) == _title.text &&
      stringFieldValue(next['description']) == _desc.text &&
      stringFieldValue(next['created']) == _created.text &&
      stringFieldValue(next['origin']) == _origin.text;

  String _signature(Map<String, dynamic> fm) {
    final keys = fm.keys.toList()..sort();
    return keys.map((k) => '$k=${fm[k]}').join('|');
  }

  void _hydrate() {
    final fm = widget.frontmatter;
    _title.text = stringFieldValue(fm['title']);
    _desc.text = stringFieldValue(fm['description']);
    _created.text = stringFieldValue(fm['created']);
    _origin.text = stringFieldValue(fm['origin']);
    final t = stringFieldValue(fm['type']);
    if (t.isEmpty) {
      _typeValue = null;
      _customType.text = '';
    } else if (kKnownPageTypes.contains(t)) {
      _typeValue = t;
      _customType.text = '';
    } else {
      _typeValue = _customTypeSentinel;
      _customType.text = t;
    }
    _lastHydrated = _signature(fm);
  }

  void _emit(Map<String, dynamic> next) {
    _lastHydrated = _signature(next);
    widget.onChanged(next);
  }

  void _onType(String? v) {
    setState(() => _typeValue = v);
    if (v == null) {
      _emit(setField(widget.frontmatter, 'type', null));
    } else if (v == _customTypeSentinel) {
      _emit(setField(widget.frontmatter, 'type', null));
    } else {
      _customType.text = '';
      _emit(setField(widget.frontmatter, 'type', v));
    }
  }

  @override
  Widget build(BuildContext context) {
    final fm = widget.frontmatter;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _row(
          TextField(
            controller: _title,
            decoration: const InputDecoration(
              labelText: '标题 (title)',
              border: OutlineInputBorder(),
              isDense: true,
            ),
            onChanged: (v) => _emit(setField(fm, 'title', v)),
          ),
        ),
        _row(_typeRow()),
        _row(
          TextField(
            controller: _desc,
            maxLines: 2,
            decoration: const InputDecoration(
              labelText: '描述 (description)',
              border: OutlineInputBorder(),
              isDense: true,
            ),
            onChanged: (v) => _emit(setField(fm, 'description', v)),
          ),
        ),
        _row(
          TextField(
            controller: _created,
            decoration: const InputDecoration(
              labelText: '创建日期 (YYYY-MM-DD)',
              border: OutlineInputBorder(),
              isDense: true,
            ),
            onChanged: (v) => _emit(setField(fm, 'created', v)),
          ),
        ),
        _row(
          TextField(
            controller: _origin,
            decoration: const InputDecoration(
              labelText: '来源 (origin)',
              hintText: 'manual / web-clip / deep-research / …',
              border: OutlineInputBorder(),
              isDense: true,
            ),
            onChanged: (v) => _emit(setField(fm, 'origin', v)),
          ),
        ),
        const SizedBox(height: BiuTokens.space2),
        _ChipListField(
          label: '标签 (tags)',
          icon: Icons.label_outline,
          items: listFieldValue(fm['tags']),
          hint: '回车添加标签',
          onAdd: (v) => _emit(addToListField(fm, 'tags', v)),
          onRemove: (i) => _emit(removeFromListField(fm, 'tags', i)),
        ),
        _ChipListField(
          label: '来源文件 (sources)',
          icon: Icons.description_outlined,
          items: listFieldValue(fm['sources']),
          hint: '回车添加 source 文件名',
          onAdd: (v) => _emit(addToListField(fm, 'sources', v)),
          onRemove: (i) => _emit(removeFromListField(fm, 'sources', i)),
        ),
        _ChipListField(
          label: '相关页 (related)',
          icon: Icons.link,
          items: listFieldValue(fm['related']),
          hint: '回车添加 [[wikilink]]',
          onAdd: (v) => _emit(addToListField(fm, 'related', v)),
          onRemove: (i) => _emit(removeFromListField(fm, 'related', i)),
        ),
        const SizedBox(height: BiuTokens.space2),
        _ExtrasSection(frontmatter: fm, onChanged: _emit),
      ],
    );
  }

  Widget _typeRow() {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Expanded(
          child: DropdownButtonFormField<String?>(
            initialValue: _typeValue,
            isDense: true,
            decoration: const InputDecoration(
              labelText: '类型 (type)',
              border: OutlineInputBorder(),
              isDense: true,
            ),
            items: [
              const DropdownMenuItem<String?>(
                  value: null, child: Text('— 未设置 —')),
              for (final t in kKnownPageTypes)
                DropdownMenuItem<String?>(value: t, child: Text(t)),
              const DropdownMenuItem<String?>(
                  value: _customTypeSentinel, child: Text('自定义…')),
            ],
            onChanged: _onType,
          ),
        ),
        if (_typeValue == _customTypeSentinel) ...[
          const SizedBox(width: BiuTokens.space2),
          Expanded(
            child: TextField(
              controller: _customType,
              decoration: const InputDecoration(
                labelText: '自定义类型',
                border: OutlineInputBorder(),
                isDense: true,
              ),
              onChanged: (v) => _emit(setField(widget.frontmatter, 'type', v)),
            ),
          ),
        ],
      ],
    );
  }

  Widget _row(Widget child) => Padding(
        padding: const EdgeInsets.only(bottom: BiuTokens.space2),
        child: child,
      );
}

// ── Chip-list field ──────────────────────────────────────────────

class _ChipListField extends StatefulWidget {
  const _ChipListField({
    required this.label,
    required this.icon,
    required this.items,
    required this.hint,
    required this.onAdd,
    required this.onRemove,
  });

  final String label;
  final IconData icon;
  final List<String> items;
  final String hint;
  final ValueChanged<String> onAdd;
  final ValueChanged<int> onRemove;

  @override
  State<_ChipListField> createState() => _ChipListFieldState();
}

class _ChipListFieldState extends State<_ChipListField> {
  final _ctrl = TextEditingController();

  @override
  void dispose() {
    _ctrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: BiuTokens.space2),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(widget.icon, size: 12, color: BiuTokens.textMuted),
              const SizedBox(width: 4),
              Text(
                widget.label,
                style: TextStyle(
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                    color: BiuTokens.textMuted),
              ),
            ],
          ),
          const SizedBox(height: 4),
          Wrap(
            spacing: 4,
            runSpacing: 4,
            children: [
              for (var i = 0; i < widget.items.length; i++)
                InputChip(
                  label: Text(widget.items[i]),
                  labelStyle: const TextStyle(fontSize: 11),
                  visualDensity: VisualDensity.compact,
                  onDeleted: () => widget.onRemove(i),
                ),
              SizedBox(
                width: 200,
                child: TextField(
                  controller: _ctrl,
                  decoration: InputDecoration(
                    hintText: widget.hint,
                    border: const OutlineInputBorder(),
                    isDense: true,
                    contentPadding: const EdgeInsets.symmetric(
                        horizontal: 8, vertical: 6),
                  ),
                  style: const TextStyle(fontSize: 11),
                  onSubmitted: (v) {
                    widget.onAdd(v);
                    _ctrl.clear();
                  },
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

// ── Extras section ───────────────────────────────────────────────

class _ExtrasSection extends StatefulWidget {
  const _ExtrasSection({required this.frontmatter, required this.onChanged});
  final Map<String, dynamic> frontmatter;
  final ValueChanged<Map<String, dynamic>> onChanged;

  @override
  State<_ExtrasSection> createState() => _ExtrasSectionState();
}

class _ExtrasSectionState extends State<_ExtrasSection> {
  final _newKey = TextEditingController();
  final _newValue = TextEditingController();

  @override
  void dispose() {
    _newKey.dispose();
    _newValue.dispose();
    super.dispose();
  }

  List<MapEntry<String, dynamic>> _extras() => widget.frontmatter.entries
      .where((e) => !kKnownFrontmatterKeys.contains(e.key))
      .toList(growable: false);

  void _addExtra() {
    final k = _newKey.text.trim();
    final v = _newValue.text.trim();
    if (k.isEmpty || v.isEmpty) return;
    if (widget.frontmatter.containsKey(k)) return;
    widget.onChanged(setField(widget.frontmatter, k, v));
    _newKey.clear();
    _newValue.clear();
  }

  @override
  Widget build(BuildContext context) {
    final extras = _extras();
    return ExpansionTile(
      tilePadding: EdgeInsets.zero,
      childrenPadding: EdgeInsets.zero,
      dense: true,
      title: Text(
        '其他字段${extras.isEmpty ? "" : " · ${extras.length}"}',
        style: TextStyle(
            fontSize: 11,
            fontWeight: FontWeight.w600,
            color: BiuTokens.textMuted),
      ),
      children: [
        for (final e in extras)
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 2),
            child: Row(
              children: [
                SizedBox(
                  width: 110,
                  child: Text(
                    e.key,
                    style: const TextStyle(
                      fontSize: 11,
                      fontFamily: 'JetBrains Mono, ui-monospace, monospace',
                    ),
                  ),
                ),
                Expanded(
                  child: TextField(
                    controller: TextEditingController(
                        text: e.value is List
                            ? (e.value as List).join(', ')
                            : (e.value?.toString() ?? '')),
                    decoration: const InputDecoration(
                      isDense: true,
                      border: OutlineInputBorder(),
                      contentPadding: EdgeInsets.symmetric(
                          horizontal: 6, vertical: 6),
                    ),
                    style: const TextStyle(fontSize: 11),
                    onSubmitted: (v) =>
                        widget.onChanged(setField(widget.frontmatter, e.key, v)),
                  ),
                ),
                IconButton(
                  icon: const Icon(Icons.close, size: 14),
                  onPressed: () =>
                      widget.onChanged(setField(widget.frontmatter, e.key, null)),
                ),
              ],
            ),
          ),
        const SizedBox(height: 4),
        Row(
          children: [
            SizedBox(
              width: 110,
              child: TextField(
                controller: _newKey,
                decoration: const InputDecoration(
                  hintText: 'key',
                  border: OutlineInputBorder(),
                  isDense: true,
                  contentPadding:
                      EdgeInsets.symmetric(horizontal: 6, vertical: 6),
                ),
                style: const TextStyle(fontSize: 11),
              ),
            ),
            const SizedBox(width: 4),
            Expanded(
              child: TextField(
                controller: _newValue,
                decoration: const InputDecoration(
                  hintText: 'value',
                  border: OutlineInputBorder(),
                  isDense: true,
                  contentPadding:
                      EdgeInsets.symmetric(horizontal: 6, vertical: 6),
                ),
                style: const TextStyle(fontSize: 11),
                onSubmitted: (_) => _addExtra(),
              ),
            ),
            IconButton(
              icon: const Icon(Icons.add, size: 14),
              onPressed: _addExtra,
            ),
          ],
        ),
      ],
    );
  }
}
