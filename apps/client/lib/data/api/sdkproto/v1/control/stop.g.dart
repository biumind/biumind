// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'stop.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

StopTask _$StopTaskFromJson(Map json) => StopTask(
  subtype: json['subtype'] as String? ?? 'stop_task',
  taskId: json['task_id'] as String,
);

Map<String, dynamic> _$StopTaskToJson(StopTask instance) => <String, dynamic>{
  'subtype': instance.subtype,
  'task_id': instance.taskId,
};
