// SDKPostTurnSummary (type=system + subtype=post_turn_summary)

import 'package:json_annotation/json_annotation.dart';
import 'sdk_message.dart';

part 'post_turn.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class SDKPostTurnSummary extends SDKMessage {
  @override
  final String type;
  final String subtype;
  @JsonKey(name: 'summarizes_uuid')
  final String summarizesUuid;
  @JsonKey(name: 'status_category')
  final String statusCategory;
  @JsonKey(name: 'status_detail')
  final String statusDetail;
  @JsonKey(name: 'is_noteworthy')
  final bool isNoteworthy;
  final String title;
  final String description;
  @JsonKey(name: 'recent_action')
  final String recentAction;
  @JsonKey(name: 'needs_action')
  final bool needsAction;
  @JsonKey(name: 'artifact_urls')
  final List<String> artifactUrls;
  @override
  final String uuid;
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;

  SDKPostTurnSummary({
    this.type = 'system',
    this.subtype = 'post_turn_summary',
    required this.summarizesUuid,
    required this.statusCategory,
    required this.statusDetail,
    required this.isNoteworthy,
    required this.title,
    required this.description,
    required this.recentAction,
    required this.needsAction,
    required this.artifactUrls,
    required this.uuid,
    required this.sessionId,
  });

  factory SDKPostTurnSummary.fromJson(Map<String, dynamic> json) =>
      _$SDKPostTurnSummaryFromJson(json);
  Map<String, dynamic> toJson() => _$SDKPostTurnSummaryToJson(this);
}
