// Maps a newsnow source id (eg. 'wallstreetcn-hot', 'zhihu') to one of
// the bundled asset PNGs at assets/rss_icons/. Sub-board IDs strip the
// trailing segment ('cls-hot' → 'cls'). Some specific multi-segment
// IDs map to a different basename ('36kr-quick' → '36kr',
// 'github-trending-today' → 'github').
//
// Icons are MIT-licensed from ourongxing/newsnow.

import 'package:flutter/material.dart';

const _aliases = <String, String>{
  '36kr-quick': '36kr',
  '36kr-renqi': '36kr',
  'kr-quick': '36kr',
  'kr-renqi': '36kr',
  'github-trending-today': 'github',
  'qqvideo-tv-hotsearch': 'qqvideo',
  'iqiyi-hot-ranklist': 'iqiyi',
  'pcbeta-windows11': 'pcbeta',
  'sputniknewscn': 'sputniknewscn',
};

String _basenameFor(String sourceId) {
  if (_aliases.containsKey(sourceId)) return _aliases[sourceId]!;
  // Strip the last hyphen-separated segment for sub-board IDs.
  final dash = sourceId.indexOf('-');
  if (dash > 0) return sourceId.substring(0, dash);
  return sourceId;
}

/// Returns the asset path for the source's logo. Caller wraps in
/// Image.asset with errorBuilder so missing icons fall back gracefully.
String boardIconAsset(String sourceId) =>
    'assets/rss_icons/${_basenameFor(sourceId)}.png';

/// 24×24 logo widget. Falls back to a single-letter avatar when the
/// asset isn't bundled (e.g. a newly-seeded board we forgot to ship
/// the icon for).
class BoardLogo extends StatelessWidget {
  const BoardLogo({
    super.key,
    required this.sourceId,
    required this.fallbackLetter,
    required this.fallbackBg,
    required this.fallbackFg,
    this.size = 24,
  });

  final String sourceId;
  final String fallbackLetter;
  final Color fallbackBg;
  final Color fallbackFg;
  final double size;

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(size / 2),
      child: Image.asset(
        boardIconAsset(sourceId),
        width: size,
        height: size,
        fit: BoxFit.cover,
        errorBuilder: (_, _, _) => _letterFallback(),
      ),
    );
  }

  Widget _letterFallback() {
    return Container(
      width: size,
      height: size,
      alignment: Alignment.center,
      decoration: BoxDecoration(color: fallbackBg, shape: BoxShape.circle),
      child: Text(
        fallbackLetter.isEmpty ? '?' : fallbackLetter.characters.first,
        style: TextStyle(
          color: fallbackFg,
          fontWeight: FontWeight.w700,
          fontSize: size * 0.55,
        ),
      ),
    );
  }
}
