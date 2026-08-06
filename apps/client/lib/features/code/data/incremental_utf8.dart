// 增量 UTF-8 解码器(M2)。PTY 字节流在任意字节处被分块,多字节字符(中文/emoji)
// 可能跨块切断。本类暂存尾部不完整的多字节序列,拼齐后再 decode,避免乱码 U+FFFD。
//
// leftover / valid_up_to 逻辑放在消费端:PTY 字节无损传到 Flutter,在喂
// xterm.write(String) 前在此重组(Channel<String> 强制合法 UTF-8)。

import 'dart:convert';
import 'dart:typed_data';

class IncrementalUtf8Decoder {
  Uint8List _leftover = Uint8List(0);

  /// 解码一块字节:返回完整部分的字符串,尾部不完整序列留到下次。
  String decode(Uint8List data) {
    final combined = Uint8List(_leftover.length + data.length)
      ..setAll(0, _leftover)
      ..setAll(_leftover.length, data);
    final validLen = validPrefixLen(combined);
    _leftover = Uint8List.fromList(
        Uint8List.sublistView(combined, validLen));
    if (validLen == 0) return '';
    return utf8.decode(
      Uint8List.sublistView(combined, 0, validLen),
      allowMalformed: true,
    );
  }

  /// 返回 b 中构成完整 UTF-8 的前缀长度;尾部不完整的多字节序列被排除。
  /// 暴露为静态以便单测。
  static int validPrefixLen(Uint8List b) {
    if (b.isEmpty) return 0;
    // 回看至多 3 个 continuation 字节(10xxxxxx)定位 lead 字节。
    var i = b.length;
    var back = 0;
    while (i > 0 && back < 3 && (b[i - 1] & 0xC0) == 0x80) {
      i--;
      back++;
    }
    if (i == 0) return b.length; // 全 continuation(异常),整段交给 decode
    final lead = b[i - 1];
    final int seqLen;
    if (lead & 0x80 == 0) {
      seqLen = 1; // ASCII
    } else if (lead & 0xE0 == 0xC0) {
      seqLen = 2;
    } else if (lead & 0xF0 == 0xE0) {
      seqLen = 3;
    } else if (lead & 0xF8 == 0xF0) {
      seqLen = 4;
    } else {
      seqLen = 1; // 非法 lead,当单字节(交给 allowMalformed)
    }
    final have = b.length - (i - 1); // lead + 已到的 continuation 数
    if (have < seqLen) return i - 1; // 不完整:切在 lead 前,留作 leftover
    return b.length; // 完整
  }
}
