// M13.5 Tier2 — Entry.fromJson parses transcript_segments for synced playback.

import 'package:biumind/features/apps/builtin/rss/models.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('parses transcript_segments into sorted TranscriptSegment list', () {
    final e = Entry.fromJson({
      'id': 'e1',
      'feed_id': 'f1',
      'title': 'Episode 1',
      'enclosure_url': 'https://cdn/ep1.mp3',
      'transcribed': true,
      'transcript_segments': [
        {'id': 0, 'start': 0, 'end': 1.5, 'text': '你好'},
        {'id': 1, 'start': 1.5, 'end': 3.2, 'text': '世界'},
      ],
    });
    expect(e.transcriptSegments, hasLength(2));
    expect(e.transcriptSegments[0].start, 0);
    expect(e.transcriptSegments[1].start, 1.5);
    expect(e.transcriptSegments[1].end, 3.2);
    expect(e.transcriptSegments[1].text, '世界');
  });

  test('no segments → empty list (ordinary article)', () {
    final e = Entry.fromJson({'id': 'a', 'feed_id': 'f', 'title': 'x'});
    expect(e.transcriptSegments, isEmpty);
    expect(e.enclosureUrl, '');
  });
}
