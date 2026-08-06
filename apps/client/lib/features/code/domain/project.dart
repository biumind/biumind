// CodeProject — 编码模块的项目(代码仓库)领域模型。M1 多项目。
//
// 落 Drift(零云同步,Drift 即 SoT)。domain 不依赖 drift —— LocalCodeProject ↔
// CodeProject 的转换在 code_projects_dao.dart 里做。

import 'package:flutter/foundation.dart';

@immutable
class CodeProject {
  const CodeProject({
    required this.id,
    required this.name,
    required this.path,
    this.branch,
    required this.lastOpenedAt,
    this.hiddenFromRail = false,
    this.avatarColor,
    this.sortIndex = 0,
  });

  /// 唯一 id(创建时生成的 uuid / 毫秒戳)。
  final String id;

  /// 展示名(默认取 path 末段)。
  final String name;

  /// 仓库绝对路径。
  final String path;

  /// 当前分支(展示用;非 git 目录或未解析时 null)。
  final String? branch;

  /// 最后打开时间。WelcomePage "最近" 排序用。
  final DateTime lastOpenedAt;

  /// 从左 Rail 隐藏(不删项目,只隐藏)。
  final bool hiddenFromRail;

  /// 头像底色(null = 由 name 哈希生成)。
  final String? avatarColor;

  /// 手动排序位次(拖拽排序;小在前)。
  final int sortIndex;

  CodeProject copyWith({
    String? name,
    String? path,
    String? branch,
    DateTime? lastOpenedAt,
    bool? hiddenFromRail,
    String? avatarColor,
    int? sortIndex,
  }) {
    return CodeProject(
      id: id,
      name: name ?? this.name,
      path: path ?? this.path,
      branch: branch ?? this.branch,
      lastOpenedAt: lastOpenedAt ?? this.lastOpenedAt,
      hiddenFromRail: hiddenFromRail ?? this.hiddenFromRail,
      avatarColor: avatarColor ?? this.avatarColor,
      sortIndex: sortIndex ?? this.sortIndex,
    );
  }

  @override
  bool operator ==(Object other) =>
      other is CodeProject &&
      other.id == id &&
      other.name == name &&
      other.path == path &&
      other.branch == branch &&
      other.lastOpenedAt == lastOpenedAt &&
      other.hiddenFromRail == hiddenFromRail &&
      other.avatarColor == avatarColor &&
      other.sortIndex == sortIndex;

  @override
  int get hashCode => Object.hash(
      id, name, path, branch, lastOpenedAt, hiddenFromRail, avatarColor, sortIndex);
}
