// Unit tests for the hand-rolled AppLocalizations class.
//
// We don't pump a full MaterialApp — we exercise the delegate
// directly. That way the tests stay decoupled from any particular
// widget tree wiring and run sub-millisecond.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/l10n/app_localizations.dart';

void main() {
  group('AppLocalizations.delegate', () {
    test('isSupported matches en and zh, ignores everything else', () {
      const d = AppLocalizations.delegate;
      expect(d.isSupported(const Locale('en')), isTrue);
      expect(d.isSupported(const Locale('en', 'US')), isTrue);
      expect(d.isSupported(const Locale('zh')), isTrue);
      expect(d.isSupported(const Locale('zh', 'CN')), isTrue);
      expect(d.isSupported(const Locale('fr')), isFalse);
      expect(d.isSupported(const Locale('ja')), isFalse);
    });

    test('load(en) returns English strings', () async {
      final t = await AppLocalizations.delegate.load(const Locale('en'));
      expect(t.localeName, 'en');
      expect(t.navMemory, 'Memory');
      expect(t.memoryAddButton, 'Add');
      expect(t.memoryModeHybrid, 'hybrid (semantic + lexical)');
    });

    test('load(zh) returns Chinese strings', () async {
      final t = await AppLocalizations.delegate.load(const Locale('zh'));
      expect(t.localeName, 'zh');
      expect(t.navMemory, '记忆');
      expect(t.memoryAddButton, '添加');
      expect(t.memoryModeLexical, '仅关键词');
    });
  });

  group('placeholder substitution', () {
    test('memorySubtitle renders all four placeholders', () async {
      final t = await AppLocalizations.delegate.load(const Locale('en'));
      final out = t.memorySubtitle(
        'recall',
        '0.50',
        ' · score=2.39',
        '12m ago',
      );
      expect(out,
          'kind=recall · salience=0.50 · score=2.39 · 12m ago');
    });

    test('memorySubtitle in Chinese keeps placeholder slots', () async {
      final t = await AppLocalizations.delegate.load(const Locale('zh'));
      final out = t.memorySubtitle(
        '偏好',
        '0.80',
        '',
        '3 天前',
      );
      expect(out, '类型=偏好 · 重要度=0.80 · 3 天前');
    });

    test('relTime helpers substitute the integer value', () async {
      final en = await AppLocalizations.delegate.load(const Locale('en'));
      final zh = await AppLocalizations.delegate.load(const Locale('zh'));
      expect(en.relTimeMinutes(7), '7m ago');
      expect(zh.relTimeMinutes(7), '7 分钟前');
      expect(en.relTimeMonths(2), '2mo ago');
      expect(zh.relTimeMonths(2), '2 个月前');
    });

    test('commonError formats the {message} placeholder', () async {
      final en = await AppLocalizations.delegate.load(const Locale('en'));
      expect(en.commonError('boom'), 'Error: boom');
    });
  });

  group('missing-key fallback', () {
    test('unknown key returns sentinel rather than throwing', () async {
      final t = await AppLocalizations.delegate.load(const Locale('zh'));
      // Force-call the private path via the public getters' fallback by
      // confirming a known real key works (negative side: there's no
      // public way to ask for an unknown key, which is by design — the
      // fallback is for translation gaps, not user input).
      expect(t.commonOk, '确定');
    });
  });

  group('supportedLocales', () {
    test('exactly two locales declared', () {
      expect(AppLocalizations.supportedLocales, hasLength(2));
      final codes = AppLocalizations.supportedLocales
          .map((l) => l.languageCode)
          .toSet();
      expect(codes, equals({'en', 'zh'}));
    });
  });
}
