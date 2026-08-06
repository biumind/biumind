import 'package:json_annotation/json_annotation.dart';
import 'events.dart';

part 'notification.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class NotificationHook implements HookInput {
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;
  @override
  @JsonKey(name: 'transcript_path')
  final String transcriptPath;
  @override
  final String cwd;
  @override
  @JsonKey(name: 'hook_event_name')
  final String hookEventName;
  final String message;
  final String? title;
  @JsonKey(name: 'notification_type')
  final String notificationType;

  NotificationHook({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.notification,
    required this.message,
    this.title,
    required this.notificationType,
  });

  factory NotificationHook.fromJson(Map<String, dynamic> json) =>
      _$NotificationHookFromJson(json);
  Map<String, dynamic> toJson() => _$NotificationHookToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class UserPromptSubmit implements HookInput {
  @override
  @JsonKey(name: 'session_id')
  final String sessionId;
  @override
  @JsonKey(name: 'transcript_path')
  final String transcriptPath;
  @override
  final String cwd;
  @override
  @JsonKey(name: 'hook_event_name')
  final String hookEventName;
  final String prompt;

  UserPromptSubmit({
    required this.sessionId,
    required this.transcriptPath,
    required this.cwd,
    this.hookEventName = HookEvent.userPromptSubmit,
    required this.prompt,
  });

  factory UserPromptSubmit.fromJson(Map<String, dynamic> json) =>
      _$UserPromptSubmitFromJson(json);
  Map<String, dynamic> toJson() => _$UserPromptSubmitToJson(this);
}
