// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'permissions.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

PermissionRuleValue _$PermissionRuleValueFromJson(Map json) =>
    PermissionRuleValue(
      toolName: json['toolName'] as String,
      ruleContent: json['ruleContent'] as String?,
    );

Map<String, dynamic> _$PermissionRuleValueToJson(
  PermissionRuleValue instance,
) => <String, dynamic>{
  'toolName': instance.toolName,
  if (instance.ruleContent case final value?) 'ruleContent': value,
};

AddRules _$AddRulesFromJson(Map json) => AddRules(
  type: json['type'] as String? ?? 'addRules',
  rules: (json['rules'] as List<dynamic>)
      .map(
        (e) =>
            PermissionRuleValue.fromJson(Map<String, dynamic>.from(e as Map)),
      )
      .toList(),
  behavior: json['behavior'] as String,
  destination: json['destination'] as String,
);

Map<String, dynamic> _$AddRulesToJson(AddRules instance) => <String, dynamic>{
  'type': instance.type,
  'rules': instance.rules,
  'behavior': instance.behavior,
  'destination': instance.destination,
};

ReplaceRules _$ReplaceRulesFromJson(Map json) => ReplaceRules(
  type: json['type'] as String? ?? 'replaceRules',
  rules: (json['rules'] as List<dynamic>)
      .map(
        (e) =>
            PermissionRuleValue.fromJson(Map<String, dynamic>.from(e as Map)),
      )
      .toList(),
  behavior: json['behavior'] as String,
  destination: json['destination'] as String,
);

Map<String, dynamic> _$ReplaceRulesToJson(ReplaceRules instance) =>
    <String, dynamic>{
      'type': instance.type,
      'rules': instance.rules,
      'behavior': instance.behavior,
      'destination': instance.destination,
    };

RemoveRules _$RemoveRulesFromJson(Map json) => RemoveRules(
  type: json['type'] as String? ?? 'removeRules',
  rules: (json['rules'] as List<dynamic>)
      .map(
        (e) =>
            PermissionRuleValue.fromJson(Map<String, dynamic>.from(e as Map)),
      )
      .toList(),
  behavior: json['behavior'] as String,
  destination: json['destination'] as String,
);

Map<String, dynamic> _$RemoveRulesToJson(RemoveRules instance) =>
    <String, dynamic>{
      'type': instance.type,
      'rules': instance.rules,
      'behavior': instance.behavior,
      'destination': instance.destination,
    };

SetModeUpdate _$SetModeUpdateFromJson(Map json) => SetModeUpdate(
  type: json['type'] as String? ?? 'setMode',
  mode: json['mode'] as String,
  destination: json['destination'] as String,
);

Map<String, dynamic> _$SetModeUpdateToJson(SetModeUpdate instance) =>
    <String, dynamic>{
      'type': instance.type,
      'mode': instance.mode,
      'destination': instance.destination,
    };

AddDirectories _$AddDirectoriesFromJson(Map json) => AddDirectories(
  type: json['type'] as String? ?? 'addDirectories',
  directories: (json['directories'] as List<dynamic>)
      .map((e) => e as String)
      .toList(),
  destination: json['destination'] as String,
);

Map<String, dynamic> _$AddDirectoriesToJson(AddDirectories instance) =>
    <String, dynamic>{
      'type': instance.type,
      'directories': instance.directories,
      'destination': instance.destination,
    };

RemoveDirectories _$RemoveDirectoriesFromJson(Map json) => RemoveDirectories(
  type: json['type'] as String? ?? 'removeDirectories',
  directories: (json['directories'] as List<dynamic>)
      .map((e) => e as String)
      .toList(),
  destination: json['destination'] as String,
);

Map<String, dynamic> _$RemoveDirectoriesToJson(RemoveDirectories instance) =>
    <String, dynamic>{
      'type': instance.type,
      'directories': instance.directories,
      'destination': instance.destination,
    };

PermissionResult _$PermissionResultFromJson(Map json) => PermissionResult(
  behavior: json['behavior'] as String,
  updatedInput: json['updatedInput'],
  updatedPermissions: json['updatedPermissions'] as List<dynamic>?,
  message: json['message'] as String?,
  interrupt: json['interrupt'] as bool?,
);

Map<String, dynamic> _$PermissionResultToJson(PermissionResult instance) =>
    <String, dynamic>{
      'behavior': instance.behavior,
      if (instance.updatedInput case final value?) 'updatedInput': value,
      if (instance.updatedPermissions case final value?)
        'updatedPermissions': value,
      if (instance.message case final value?) 'message': value,
      if (instance.interrupt case final value?) 'interrupt': value,
    };
