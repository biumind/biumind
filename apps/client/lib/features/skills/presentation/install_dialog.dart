// InstallSkillDialog — three-tab modal mirroring server's
// /v1/skills oneof: URL fetch, .biuskill upload, or hand-written
// inline. Each tab maps to one SkillClient.installXXX method.
//
// Zip upload uses the file_selector package (cross-platform: macOS /
// Windows / Linux / iOS / Android / Web). Picked file is read into
// memory, base64-encoded, and uploaded as JSON — same path the
// server consumes from `biu skill install ./pkg.biuskill`.
//
// On success Navigator.pop(c, Skill) returns the created row so the
// caller can select it and refresh the list.

import 'dart:convert';
import 'dart:typed_data';

import 'package:file_selector/file_selector.dart';
import 'package:flutter/material.dart';

import '../../../app/theme.dart';
import '../../../data/api/skill_client.dart';
import '../../../l10n/app_localizations.dart';

class InstallSkillDialog extends StatefulWidget {
  const InstallSkillDialog({
    super.key,
    required this.client,
    this.initialTab = 'url',
  });
  final SkillClient client;
  /// Which tab to open on first frame: 'url' | 'zip' | 'inline'.
  /// Defaults to 'url' since marketplace install is the most common
  /// entry path. The "技能商店" button on SkillsPage opens the dialog
  /// with this set so users land directly on the URL tab.
  final String initialTab;

  @override
  State<InstallSkillDialog> createState() => _InstallSkillDialogState();
}

enum _Mode { url, zip, inline }

_Mode _modeFromString(String s) {
  switch (s) {
    case 'zip':
      return _Mode.zip;
    case 'inline':
      return _Mode.inline;
    default:
      return _Mode.url;
  }
}

class _InstallSkillDialogState extends State<InstallSkillDialog> {
  late _Mode _mode = _modeFromString(widget.initialTab);
  bool _busy = false;
  String? _error;

  // url
  final _urlCtrl = TextEditingController();
  // zip
  XFile? _zipFile;
  // inline
  final _idCtrl = TextEditingController();
  final _nameCtrl = TextEditingController();
  final _descCtrl = TextEditingController();
  final _bodyCtrl = TextEditingController();

  @override
  void dispose() {
    _urlCtrl.dispose();
    _idCtrl.dispose();
    _nameCtrl.dispose();
    _descCtrl.dispose();
    _bodyCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext c) {
    final t = AppLocalizations.of(c)!;
    return AlertDialog(
      title: Text(t.skillsAdd),
      content: SizedBox(
        width: 520,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          mainAxisSize: MainAxisSize.min,
          children: [
            SegmentedButton<_Mode>(
              segments: [
                ButtonSegment(value: _Mode.url, label: Text(t.skillsInstallURL)),
                ButtonSegment(value: _Mode.zip, label: Text(t.skillsInstallZip)),
                ButtonSegment(value: _Mode.inline, label: Text(t.skillsInstallInline)),
              ],
              selected: {_mode},
              onSelectionChanged: (s) => setState(() => _mode = s.first),
            ),
            const SizedBox(height: 16),
            _modeBody(t),
            if (_error != null) ...[
              const SizedBox(height: 12),
              Container(
                padding: const EdgeInsets.all(8),
                color: Theme.of(c).colorScheme.errorContainer,
                child: Text(_error!, style: const TextStyle(fontSize: 12)),
              ),
            ],
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: _busy ? null : () => Navigator.pop(c),
          child: Text(t.skillsCancel),
        ),
        FilledButton(
          onPressed: _busy ? null : () => _submit(t),
          child: _busy
              ? const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2))
              : Text(t.skillsInstall),
        ),
      ],
    );
  }

  Widget _modeBody(AppLocalizations t) {
    switch (_mode) {
      case _Mode.url:
        return TextField(
          controller: _urlCtrl,
          decoration: InputDecoration(
            labelText: 'https://…/SKILL.md',
            helperText: t.skillsInstallURLHint,
            border: const OutlineInputBorder(),
          ),
          autofocus: true,
        );
      case _Mode.zip:
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            OutlinedButton.icon(
              icon: const Icon(Icons.folder_open_outlined),
              label: Text(_zipFile?.name ?? t.skillsInstallZipPick),
              onPressed: () async {
                const typeGroup = XTypeGroup(
                  label: '.biuskill / .zip',
                  extensions: ['biuskill', 'zip'],
                );
                final picked = await openFile(acceptedTypeGroups: [typeGroup]);
                if (picked != null) setState(() => _zipFile = picked);
              },
            ),
            const SizedBox(height: 4),
            Text(t.skillsInstallZipHint,
                style: TextStyle(fontSize: 11, color: BiuTokens.textMuted)),
          ],
        );
      case _Mode.inline:
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            TextField(
              controller: _idCtrl,
              decoration: InputDecoration(
                labelText: t.skillsInlineIdentifier,
                helperText: 'kebab-case slug',
                border: const OutlineInputBorder(),
              ),
              autofocus: true,
            ),
            const SizedBox(height: 8),
            TextField(
              controller: _nameCtrl,
              decoration: InputDecoration(
                labelText: t.skillsInlineName,
                border: const OutlineInputBorder(),
              ),
            ),
            const SizedBox(height: 8),
            TextField(
              controller: _descCtrl,
              decoration: InputDecoration(
                labelText: t.skillsInlineDescription,
                border: const OutlineInputBorder(),
              ),
            ),
            const SizedBox(height: 8),
            TextField(
              controller: _bodyCtrl,
              maxLines: 8,
              decoration: InputDecoration(
                labelText: t.skillsInlineBody,
                helperText: r'$ARGS will be substituted at invocation',
                border: const OutlineInputBorder(),
              ),
            ),
          ],
        );
    }
  }

  Future<void> _submit(AppLocalizations t) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final s = await switch (_mode) {
        _Mode.url => _submitURL(t),
        _Mode.zip => _submitZip(t),
        _Mode.inline => _submitInline(t),
      };
      if (!mounted) return;
      Navigator.pop(context, s);
    } on SkillApiError catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.isTooLarge ? t.skillsErrTooLarge : (e.body.isEmpty ? '$e' : e.body);
        _busy = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = '$e';
        _busy = false;
      });
    }
  }

  Future<Skill> _submitURL(AppLocalizations t) async {
    final url = _urlCtrl.text.trim();
    if (url.isEmpty) throw t.skillsErrURLRequired;
    return widget.client.installFromUrl(url);
  }

  Future<Skill> _submitZip(AppLocalizations t) async {
    final f = _zipFile;
    if (f == null) throw t.skillsErrZipRequired;
    final Uint8List bytes = await f.readAsBytes();
    return widget.client.installFromZip(base64Encode(bytes));
  }

  Future<Skill> _submitInline(AppLocalizations t) async {
    final id = _idCtrl.text.trim();
    final name = _nameCtrl.text.trim();
    final desc = _descCtrl.text.trim();
    final body = _bodyCtrl.text;
    if (id.isEmpty || name.isEmpty || desc.isEmpty || body.trim().isEmpty) {
      throw t.skillsErrInlineRequired;
    }
    return widget.client.installInline(
      identifier: id, name: name, description: desc, body: body,
    );
  }
}
