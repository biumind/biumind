// ComposerAttachments —— per-thread 附件状态机单测。

import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/features/chat/application/attachments_provider.dart';

Attachment _img(String id, {int size = 16}) => Attachment(
      id: id,
      name: '$id.png',
      mime: 'image/png',
      bytes: Uint8List(size),
    );

void main() {
  group('ComposerAttachmentsNotifier', () {
    test('starts empty', () {
      final n = ComposerAttachmentsNotifier();
      expect(n.state, isEmpty);
    });

    test('add appends to state', () {
      final n = ComposerAttachmentsNotifier();
      n.add(_img('a'));
      n.add(_img('b'));
      expect(n.state.map((a) => a.id), ['a', 'b']);
    });

    test('remove drops by id; others stay', () {
      final n = ComposerAttachmentsNotifier();
      n.add(_img('a'));
      n.add(_img('b'));
      n.add(_img('c'));
      n.remove('b');
      expect(n.state.map((a) => a.id), ['a', 'c']);
    });

    test('remove unknown id is a no-op', () {
      final n = ComposerAttachmentsNotifier();
      n.add(_img('a'));
      n.remove('z');
      expect(n.state.map((a) => a.id), ['a']);
    });

    test('clear empties state', () {
      final n = ComposerAttachmentsNotifier();
      n.add(_img('a'));
      n.add(_img('b'));
      n.clear();
      expect(n.state, isEmpty);
    });

    test('clear on empty state is a no-op (same identity)', () {
      final n = ComposerAttachmentsNotifier();
      final s0 = n.state;
      n.clear();
      expect(identical(n.state, s0), true);
    });

    test('Attachment.isImage / sizeBytes correct', () {
      final a = Attachment(
        id: 'x',
        name: 'x.png',
        mime: 'image/png',
        bytes: Uint8List(1024),
      );
      expect(a.isImage, true);
      expect(a.sizeBytes, 1024);
      expect(a.status, AttachmentStatus.ready);
    });

    test('non-image mime → isImage false', () {
      final a = Attachment(
        id: 'p',
        name: 'doc.pdf',
        mime: 'application/pdf',
        bytes: Uint8List(0),
      );
      expect(a.isImage, false);
    });
  });
}
