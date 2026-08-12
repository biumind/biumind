// note_merge —— 段级三方合并（3-way merge），供 NoteOutboxFlusher 在 409
// 时把 base（上次服务端确认态）/ local（本地草稿）/ remote（服务端 current）
// 三份 markdown 合并。
//
// 设计目标（D4 个人空间，对标 Joplin/Obsidian）：两设备同改一篇笔记时，
// 不重叠段落自动合并、静默重发；同段两方都改才判冲突，交 UI 逐段裁决。
// 服务端零改动 —— 409 响应已带 remote 全文，base 由 v35 schema 的
// baseContentMd 列在 pull / flush 成功时回填。
//
// 算法：经典 diff3。对 base↔local、base↔remote 各求 LCS 匹配，导出两列
// 「变更区间」（O 轴上 deleted 段 + 对应插入段）；按 O 轴扫描两列变更：
//   - 仅一方触发的 O 区间 → 取该方插入（另一方此区间 == base，未改）。
//   - 两方 O 区间重叠（含同点纯插入）→ 冲突段，交 UI 裁决。
//   - 两方都未改的 O 段 → 原 base。
// 关键：两设备改相邻的不同段（base[B] 仅 local 改、base[C] 仅 remote 改）
// 的 O 区间不重叠 → 各自取改方 → 干净合并 [B', C']。同步点法会把这种判
// 成大冲突，故不用。
//
// 段级（非字符级）保守可预测，避免半词乱拼；段内细合并列 v2。LCS 自实现
// （O(n·m) DP，笔记段数级别足够），不依赖 diff_match_patch 的私有 line-mode API。

import 'dart:math' as math;

/// 一个冲突段的三份原文（O 轴上两方变更重叠的区域）。
/// UI 据此展示「保留本地 / 保留服务端 / 两者都保留」。
class MergeRegion {
  final String base;
  final String local;
  final String remote;
  const MergeRegion({
    required this.base,
    required this.local,
    required this.remote,
  });

  @override
  String toString() =>
      'MergeRegion(base=${base.length}c, local=${local.length}c, remote=${remote.length}c)';
}

/// 合并文档的一个有序片段。UI 按 [MergeResult.segments] 顺序渲染：
/// 已自动合并的稳定/单方改动段（只读预览）+ 冲突段（用户逐段裁决）。
sealed class MergeSegment {
  const MergeSegment();
}

/// 已合并定的段（base 未改 / 仅一方改 / 两方同改）。text 为最终正文。
class ResolvedMergeSegment extends MergeSegment {
  final String text;
  const ResolvedMergeSegment(this.text);
  @override
  String toString() => 'Resolved(${text.length}c)';
}

/// 冲突段 —— 两方在 base 同一区域都改了且不同。region 含 base/local/remote
/// 原文，UI 让用户选保留哪方（或两者都留）。
class ConflictMergeSegment extends MergeSegment {
  final MergeRegion region;
  const ConflictMergeSegment(this.region);
  @override
  String toString() => 'Conflict(region)';
}

/// merge3 的结果。merged 非 null = 无冲突，已合成全文；merged = null =
/// 有冲突段未决，UI 按 [segments] 渲染、裁决后调 repository.updateNote 落库。
class MergeResult {
  /// 合并后的完整 markdown；hasConflict 时为 null（需 UI 裁决后重建）。
  final String? merged;

  /// 有序片段列表（含 ResolvedMergeSegment 与 ConflictMergeSegment）。
  final List<MergeSegment> segments;

  /// 自动合并（非冲突）的变更区数 —— 含「一方改」「两方同改」。
  final int autoMerged;

  const MergeResult({
    required this.merged,
    required this.segments,
    required this.autoMerged,
  });

  bool get hasConflict => segments.any((s) => s is ConflictMergeSegment);

  @override
  String toString() =>
      'MergeResult(hasConflict=$hasConflict, autoMerged=$autoMerged, '
      'segments=${segments.length}, merged=${merged == null ? "null" : "${merged!.length}c"})';
}

/// 段级三方合并。三份任意 markdown，返回 [MergeResult]。
MergeResult merge3(String baseText, String localText, String remoteText) {
  final base = _splitParas(baseText);
  final local = _splitParas(localText);
  final remote = _splitParas(remoteText);

  // 三份完全一致 —— 无需合并。
  if (_listEq(base, local) && _listEq(base, remote)) {
    return MergeResult(
        merged: _joinParas(base),
        segments: [ResolvedMergeSegment(_joinParas(base))],
        autoMerged: 0);
  }

  // 两列变更区间（按 base 索引轴，oStart 升序）。每个 _Change：base 删了
  // [oStart,oEnd) 这些段，对应插入方加了 [xStart,xEnd) 这些段。纯插入
  // oStart==oEnd；纯删除 xStart==xEnd。
  final changesLocal = _changes(base, local);
  final changesRemote = _changes(base, remote);

  final segments = <MergeSegment>[];
  var autoMerged = 0;
  var oPos = 0; // base 轴扫描位置
  var iL = 0; // changesLocal 游标
  var iR = 0; // changesRemote 游标

  void pushResolved(String text) {
    if (text.isNotEmpty) segments.add(ResolvedMergeSegment(text));
  }

  while (iL < changesLocal.length || iR < changesRemote.length) {
    final cl = iL < changesLocal.length ? changesLocal[iL] : null;
    final cr = iR < changesRemote.length ? changesRemote[iR] : null;
    final overlaps = cl != null && cr != null && _overlaps(cl, cr);

    if (overlaps) {
      // 冲突区：两方 O 区间重叠（或同点纯插入）。贪心吞掉所有与当前区重叠
      // 的后续变更，合成一个最大冲突区。
      final oStart = math.min(cl.oStart, cr.oStart);
      var oEnd = math.max(cl.oEnd, cr.oEnd);
      var moreL = iL + 1, moreR = iR + 1;
      var grown = true;
      while (grown) {
        grown = false;
        while (moreL < changesLocal.length &&
            changesLocal[moreL].oStart < oEnd) {
          oEnd = math.max(oEnd, changesLocal[moreL].oEnd);
          moreL++;
          grown = true;
        }
        while (moreR < changesRemote.length &&
            changesRemote[moreR].oStart < oEnd) {
          oEnd = math.max(oEnd, changesRemote[moreR].oEnd);
          moreR++;
          grown = true;
        }
      }
      pushResolved(_joinParas(base.sublist(oPos, oStart))); // 区前稳定段
      final localSlice = _otherSlice(base, local, changesLocal, oStart, oEnd);
      final remoteSlice =
          _otherSlice(base, remote, changesRemote, oStart, oEnd);
      if (_listEq(localSlice, remoteSlice)) {
        // 两方在此区做了相同改动 —— 非冲突，自动取一份。
        pushResolved(_joinParas(localSlice));
        autoMerged++;
      } else {
        segments.add(ConflictMergeSegment(MergeRegion(
          base: _joinParas(base.sublist(oStart, oEnd)),
          local: _joinParas(localSlice),
          remote: _joinParas(remoteSlice),
        )));
      }
      oPos = oEnd;
      iL = moreL;
      iR = moreR;
    } else {
      // 单方变更（另一方此 O 区未改）。取较早者处理。
      final useLocal = cr == null || (cl != null && cl.oStart <= cr.oStart);
      final c = (useLocal ? cl : cr)!;
      pushResolved(_joinParas(base.sublist(oPos, c.oStart))); // 稳定段
      pushResolved(_joinParas(useLocal
          ? local.sublist(c.xStart, c.xEnd)
          : remote.sublist(c.xStart, c.xEnd)));
      if (c.oStart != c.oEnd || c.xStart != c.xEnd) autoMerged++;
      oPos = c.oEnd;
      if (useLocal) {
        iL++;
      } else {
        iR++;
      }
    }
  }
  pushResolved(_joinParas(base.sublist(oPos))); // 尾部稳定段

  final hasConflict = segments.any((s) => s is ConflictMergeSegment);
  final merged = hasConflict
      ? null
      : _joinParas([
          for (final s in segments)
            if (s is ResolvedMergeSegment) ...s.text.split('\n\n'),
        ]);

  return MergeResult(
    merged: merged,
    segments: segments,
    autoMerged: autoMerged,
  );
}

// ─── 内部：段切分 / 合并 ───────────────────────────────────────

/// 按空行切段（行内 \n 保留，如 list/codeblock 整块算一段）。空白段丢弃。
List<String> _splitParas(String s) {
  if (s.isEmpty) return const [];
  return s
      .split(RegExp(r'\n[ \t]*\n'))
      .map((p) => p.trim())
      .where((p) => p.isNotEmpty)
      .toList();
}

/// 段列表用 \n\n 拼回（标准 markdown 段分隔）。空列表 → 空串。
String _joinParas(List<String> paras) => paras.join('\n\n');

bool _listEq(List<String> a, List<String> b) {
  if (identical(a, b)) return true;
  if (a.length != b.length) return false;
  for (var i = 0; i < a.length; i++) {
    if (a[i] != b[i]) return false;
  }
  return true;
}

// ─── 内部：LCS + 变更区间 ──────────────────────────────────────

class _Change {
  final int oStart, oEnd, xStart, xEnd;
  const _Change(this.oStart, this.oEnd, this.xStart, this.xEnd);
  @override
  String toString() => '_Change(o[$oStart,$oEnd) x[$xStart,$xEnd))';
}

/// base ↔ other 的 LCS 匹配对（按 o 升序）。
List<({int o, int x})> _lcsMatches(List<String> o, List<String> x) {
  final n = o.length, m = x.length;
  if (n == 0 || m == 0) return const [];
  final dp = List.generate(n + 1, (_) => List<int>.filled(m + 1, 0));
  for (var i = n - 1; i >= 0; i--) {
    for (var j = m - 1; j >= 0; j--) {
      dp[i][j] = o[i] == x[j]
          ? dp[i + 1][j + 1] + 1
          : math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  final matches = <({int o, int x})>[];
  var i = 0;
  var j = 0;
  while (i < n && j < m) {
    if (o[i] == x[j]) {
      matches.add((o: i, x: j));
      i++;
      j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      i++;
    } else {
      j++;
    }
  }
  return matches;
}

/// 从 LCS 匹配导出变更区间：匹配对之间的「空隙」= base 删一段 + other 插一段。
List<_Change> _changes(List<String> base, List<String> other) {
  final matches = _lcsMatches(base, other);
  final changes = <_Change>[];
  var oi = 0;
  var xi = 0;
  for (final m in matches) {
    if (oi < m.o || xi < m.x) {
      changes.add(_Change(oi, m.o, xi, m.x));
    }
    oi = m.o + 1;
    xi = m.x + 1;
  }
  if (oi < base.length || xi < other.length) {
    changes.add(_Change(oi, base.length, xi, other.length));
  }
  return changes;
}

/// 两个变更在 O 轴上是否冲突：
///   - 区间重叠（oStart < 对方 oEnd 且反之）。
///   - 或同点纯插入（两方都在 oStart==oEnd==p 插入，区间空但同位）。
bool _overlaps(_Change a, _Change b) {
  final lo = a.oStart < b.oStart ? a : b;
  final hi = a.oStart < b.oStart ? b : a;
  if (lo.oEnd > hi.oStart) return true; // 区间相交
  if (lo.oEnd == hi.oStart && lo.oStart == lo.oEnd && hi.oStart == hi.oEnd) {
    return true; // 两方同点纯插入（理论上 hi.oStart==lo.oEnd==同一点）
  }
  return false;
}

/// 取 other 序列中与 base 区间 [a,b) 对齐的内容切片。
///
/// 用于冲突区裁出 local/remote 文本：遍历 other 的变更 + 稳定匹配，收集
/// 所有落在 [a,b) 内的 other 段：
///   - 稳定匹配段（base[k]==other[k']）：k in [a,b) → 计 other 对应段。
///   - 变更插入段 other[xStart,xEnd)：当插入点 oStart 落在 [a,b) 内
///     （a<=oStart<b；点查询 a==b 时 oStart==a）→ 计插入段。
///   - 变更删除段（base[oStart,oEnd) 被 other 删）：不计 other 段（other
///     此处无内容）。
/// 返回 other 子列表（可能为空 = other 在此区无内容，如纯删除）。
List<String> _otherSlice(
    List<String> base, List<String> other, List<_Change> changes, int a, int b) {
  var oi = 0;
  var xi = 0;
  int? start;
  var end = 0;

  for (final c in changes) {
    // 稳定区 [oi, c.oStart)：每段 base[k] 对应 other[xi + (k-oi)]。
    for (var k = oi; k < c.oStart; k++) {
      if (k >= a && k < b) {
        start ??= xi + (k - oi);
        end = xi + (k - oi) + 1;
      }
    }
    // 变更插入段：插入点 c.oStart 落在 [a,b) 内才计入（含点查询 a==b）。
    final insertionInZone =
        (c.oStart >= a && c.oStart < b) || (a == b && c.oStart == a);
    if (insertionInZone && c.xEnd > c.xStart) {
      start ??= c.xStart;
      end = c.xEnd;
    }
    oi = c.oEnd;
    xi = c.xEnd;
  }
  // 尾稳定区 [oi, base.length)。
  for (var k = oi; k < base.length; k++) {
    if (k >= a && k < b) {
      start ??= xi + (k - oi);
      end = xi + (k - oi) + 1;
    }
  }
  if (start == null) return const [];
  return other.sublist(start, end);
}
