// note_merge 单测 —— 段级三方合并（diff3）。
//
// 覆盖：
//   * 三份相同 → 无合并
//   * 单方改 / 单方增段 / 单方删段 → 自动合并
//   * 两方相同改动 → 自动合并（非冲突）
//   * 两方改相邻的不同段 → 自动合并（diff3 正确性关键 case，同步点法会误判）
//   * 两方改同段不同 → 冲突
//   * 一方删段 + 另一方改该段 → 冲突

import 'package:flutter_test/flutter_test.dart';

import 'package:biumind/data/note_merge.dart';

void main() {
  group('merge3 无冲突', () {
    test('三份相同 → merged == base，无段', () {
      final r = merge3('A\n\nB\n\nC', 'A\n\nB\n\nC', 'A\n\nB\n\nC');
      expect(r.hasConflict, isFalse);
      expect(r.merged, 'A\n\nB\n\nC');
    });

    test('单方改（local 改 P2，remote 同 base）→ merged == local', () {
      final r = merge3('P1\n\nP2\n\nP3', 'P1\n\nP2L\n\nP3', 'P1\n\nP2\n\nP3');
      expect(r.hasConflict, isFalse);
      expect(r.merged, 'P1\n\nP2L\n\nP3');
      expect(r.autoMerged, greaterThan(0));
    });

    test('单方增段（local 加一段）→ merged 含新段', () {
      final r = merge3('A\n\nB', 'A\n\nX\n\nB', 'A\n\nB');
      expect(r.hasConflict, isFalse);
      expect(r.merged, 'A\n\nX\n\nB');
    });

    test('单方删段（local 删中段）→ merged 不含该段', () {
      final r = merge3('A\n\nB\n\nC', 'A\n\nC', 'A\n\nB\n\nC');
      expect(r.hasConflict, isFalse);
      expect(r.merged, 'A\n\nC');
    });

    test('两方相同改动 → 自动合并（非冲突）', () {
      final r = merge3('A\n\nB\n\nC', 'A\n\nZ\n\nC', 'A\n\nZ\n\nC');
      expect(r.hasConflict, isFalse);
      expect(r.merged, 'A\n\nZ\n\nC');
    });

    test('两方改相邻的不同段 → 自动合并双方（diff3 关键 case）', () {
      // local 改 P2，remote 改 P3 —— 同步点法会把 [P2,P3] 当一个冲突区；
      // 正确 diff3 应识别两段变更 O 区间不重叠，各自取改方。
      final r = merge3(
          'P1\n\nP2\n\nP3', 'P1\n\nP2L\n\nP3', 'P1\n\nP2\n\nP3R');
      expect(r.hasConflict, isFalse);
      expect(r.merged, 'P1\n\nP2L\n\nP3R');
    });

    test('两方各增不同段（不同位）→ 都保留', () {
      final r = merge3('A\n\nC', 'A\n\nB\n\nC', 'A\n\nC\n\nD');
      expect(r.hasConflict, isFalse);
      // local 在 A,C 间插 B；remote 在 C 后插 D。
      expect(r.merged, 'A\n\nB\n\nC\n\nD');
    });
  });

  group('merge3 冲突', () {
    test('两方改同段不同 → 1 个冲突段，merged=null', () {
      final r = merge3('A\n\nB\n\nC', 'A\n\nBX\n\nC', 'A\n\nBY\n\nC');
      expect(r.hasConflict, isTrue);
      expect(r.merged, isNull);
      final conflict = r.segments.whereType<ConflictMergeSegment>().single;
      expect(conflict.region.base, 'B');
      expect(conflict.region.local, 'BX');
      expect(conflict.region.remote, 'BY');
    });

    test('一方删段 + 另一方改该段 → 冲突', () {
      final r = merge3('A\n\nB\n\nC', 'A\n\nC', 'A\n\nB2\n\nC');
      expect(r.hasConflict, isTrue);
      expect(r.merged, isNull);
    });

    test('冲突段前后的稳定段在 segments 里', () {
      final r = merge3('A\n\nB\n\nC\n\nD', 'A\n\nBX\n\nC\n\nD', 'A\n\nBY\n\nC\n\nD');
      // segments: Resolved(A), Conflict(B), Resolved(C\n\nD)（相邻稳定段合成）
      final resolvedTexts = r.segments
          .whereType<ResolvedMergeSegment>()
          .map((s) => s.text)
          .toList();
      expect(resolvedTexts, ['A', 'C\n\nD']);
      expect(r.segments.whereType<ConflictMergeSegment>().length, 1);
    });
  });
}
