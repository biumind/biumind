import 'package:biumind/core/collab/collab_presence.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('colorForUser is deterministic', () {
    final a = colorForUser('u-1');
    final b = colorForUser('u-1');
    final c = colorForUser('u-2');
    expect(a, b);
    expect(a == c, isFalse);
  });

  test('stub watch / debug emit', () async {
    final svc = StubCollabPresence();
    final stream = svc.watch('wiki://page/p1');
    final got = <List<RemoteCursor>>[];
    final sub = stream.listen(got.add);
    svc.debugEmit('wiki://page/p1', [
      RemoteCursor(userId: 'u', displayName: 'A', color: Colors.red),
    ]);
    await Future<void>.delayed(const Duration(milliseconds: 10));
    expect(got.length, 1);
    expect(got.first.first.displayName, 'A');
    await sub.cancel();
  });
}
