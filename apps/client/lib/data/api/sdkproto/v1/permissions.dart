// PermissionUpdate 6 union variant + PermissionResult。

import 'package:json_annotation/json_annotation.dart';

part 'permissions.g.dart';

class PermissionBehavior {
  static const allow = 'allow';
  static const deny = 'deny';
  static const ask = 'ask';
}

class PermissionMode {
  static const defaultMode = 'default';
  static const acceptEdits = 'acceptEdits';
  static const bypassPermissions = 'bypassPermissions';
  static const plan = 'plan';
  static const dontAsk = 'dontAsk';
}

class PermissionDestination {
  static const userSettings = 'userSettings';
  static const projectSettings = 'projectSettings';
  static const localSettings = 'localSettings';
  static const session = 'session';
  static const cliArg = 'cliArg';
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class PermissionRuleValue {
  final String toolName;
  final String? ruleContent;
  PermissionRuleValue({required this.toolName, this.ruleContent});
  factory PermissionRuleValue.fromJson(Map<String, dynamic> json) =>
      _$PermissionRuleValueFromJson(json);
  Map<String, dynamic> toJson() => _$PermissionRuleValueToJson(this);
}

abstract class PermissionUpdate {
  String get type;
  Map<String, dynamic> toJson();

  static PermissionUpdate fromJson(Map<String, dynamic> json) {
    final t = json['type'] as String? ?? '';
    switch (t) {
      case 'addRules':
        return AddRules.fromJson(json);
      case 'replaceRules':
        return ReplaceRules.fromJson(json);
      case 'removeRules':
        return RemoveRules.fromJson(json);
      case 'setMode':
        return SetModeUpdate.fromJson(json);
      case 'addDirectories':
        return AddDirectories.fromJson(json);
      case 'removeDirectories':
        return RemoveDirectories.fromJson(json);
      default:
        throw ArgumentError('unknown PermissionUpdate type: $t');
    }
  }
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class AddRules implements PermissionUpdate {
  @override
  final String type;
  final List<PermissionRuleValue> rules;
  final String behavior;
  final String destination;

  AddRules({
    this.type = 'addRules',
    required this.rules,
    required this.behavior,
    required this.destination,
  });

  factory AddRules.fromJson(Map<String, dynamic> json) =>
      _$AddRulesFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$AddRulesToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class ReplaceRules implements PermissionUpdate {
  @override
  final String type;
  final List<PermissionRuleValue> rules;
  final String behavior;
  final String destination;

  ReplaceRules({
    this.type = 'replaceRules',
    required this.rules,
    required this.behavior,
    required this.destination,
  });

  factory ReplaceRules.fromJson(Map<String, dynamic> json) =>
      _$ReplaceRulesFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$ReplaceRulesToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class RemoveRules implements PermissionUpdate {
  @override
  final String type;
  final List<PermissionRuleValue> rules;
  final String behavior;
  final String destination;

  RemoveRules({
    this.type = 'removeRules',
    required this.rules,
    required this.behavior,
    required this.destination,
  });

  factory RemoveRules.fromJson(Map<String, dynamic> json) =>
      _$RemoveRulesFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$RemoveRulesToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class SetModeUpdate implements PermissionUpdate {
  @override
  final String type;
  final String mode;
  final String destination;

  SetModeUpdate({
    this.type = 'setMode',
    required this.mode,
    required this.destination,
  });

  factory SetModeUpdate.fromJson(Map<String, dynamic> json) =>
      _$SetModeUpdateFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$SetModeUpdateToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class AddDirectories implements PermissionUpdate {
  @override
  final String type;
  final List<String> directories;
  final String destination;

  AddDirectories({
    this.type = 'addDirectories',
    required this.directories,
    required this.destination,
  });

  factory AddDirectories.fromJson(Map<String, dynamic> json) =>
      _$AddDirectoriesFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$AddDirectoriesToJson(this);
}

@JsonSerializable(includeIfNull: false, anyMap: true)
class RemoveDirectories implements PermissionUpdate {
  @override
  final String type;
  final List<String> directories;
  final String destination;

  RemoveDirectories({
    this.type = 'removeDirectories',
    required this.directories,
    required this.destination,
  });

  factory RemoveDirectories.fromJson(Map<String, dynamic> json) =>
      _$RemoveDirectoriesFromJson(json);
  @override
  Map<String, dynamic> toJson() => _$RemoveDirectoriesToJson(this);
}

/// PermissionResult —— allow / deny 两态合并到单 class。
@JsonSerializable(includeIfNull: false, anyMap: true)
class PermissionResult {
  final String behavior;
  final dynamic updatedInput;
  final List<dynamic>? updatedPermissions;
  final String? message;
  final bool? interrupt;

  PermissionResult({
    required this.behavior,
    this.updatedInput,
    this.updatedPermissions,
    this.message,
    this.interrupt,
  });

  factory PermissionResult.fromJson(Map<String, dynamic> json) =>
      _$PermissionResultFromJson(json);
  Map<String, dynamic> toJson() => _$PermissionResultToJson(this);

  bool get isAllow => behavior == PermissionBehavior.allow;
  bool get isDeny => behavior == PermissionBehavior.deny;
}
