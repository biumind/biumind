import 'package:json_annotation/json_annotation.dart';

part 'stop.g.dart';

@JsonSerializable(includeIfNull: false, anyMap: true)
class StopTask {
  final String subtype;
  @JsonKey(name: 'task_id')
  final String taskId;

  StopTask({this.subtype = 'stop_task', required this.taskId});

  factory StopTask.fromJson(Map<String, dynamic> json) =>
      _$StopTaskFromJson(json);
  Map<String, dynamic> toJson() => _$StopTaskToJson(this);
}
