// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'db.dart';

// ignore_for_file: type=lint
class $WikiProjectsTable extends WikiProjects
    with TableInfo<$WikiProjectsTable, LocalWikiProject> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $WikiProjectsTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<String> id = GeneratedColumn<String>(
    'id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _nameMeta = const VerificationMeta('name');
  @override
  late final GeneratedColumn<String> name = GeneratedColumn<String>(
    'name',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _updatedAtMeta = const VerificationMeta(
    'updatedAt',
  );
  @override
  late final GeneratedColumn<DateTime> updatedAt = GeneratedColumn<DateTime>(
    'updated_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  @override
  List<GeneratedColumn> get $columns => [id, name, updatedAt];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'wiki_projects';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalWikiProject> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    } else if (isInserting) {
      context.missing(_idMeta);
    }
    if (data.containsKey('name')) {
      context.handle(
        _nameMeta,
        name.isAcceptableOrUnknown(data['name']!, _nameMeta),
      );
    } else if (isInserting) {
      context.missing(_nameMeta);
    }
    if (data.containsKey('updated_at')) {
      context.handle(
        _updatedAtMeta,
        updatedAt.isAcceptableOrUnknown(data['updated_at']!, _updatedAtMeta),
      );
    } else if (isInserting) {
      context.missing(_updatedAtMeta);
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalWikiProject map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalWikiProject(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}id'],
      )!,
      name: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}name'],
      )!,
      updatedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}updated_at'],
      )!,
    );
  }

  @override
  $WikiProjectsTable createAlias(String alias) {
    return $WikiProjectsTable(attachedDatabase, alias);
  }
}

class LocalWikiProject extends DataClass
    implements Insertable<LocalWikiProject> {
  final String id;
  final String name;
  final DateTime updatedAt;
  const LocalWikiProject({
    required this.id,
    required this.name,
    required this.updatedAt,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<String>(id);
    map['name'] = Variable<String>(name);
    map['updated_at'] = Variable<DateTime>(updatedAt);
    return map;
  }

  WikiProjectsCompanion toCompanion(bool nullToAbsent) {
    return WikiProjectsCompanion(
      id: Value(id),
      name: Value(name),
      updatedAt: Value(updatedAt),
    );
  }

  factory LocalWikiProject.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalWikiProject(
      id: serializer.fromJson<String>(json['id']),
      name: serializer.fromJson<String>(json['name']),
      updatedAt: serializer.fromJson<DateTime>(json['updatedAt']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<String>(id),
      'name': serializer.toJson<String>(name),
      'updatedAt': serializer.toJson<DateTime>(updatedAt),
    };
  }

  LocalWikiProject copyWith({String? id, String? name, DateTime? updatedAt}) =>
      LocalWikiProject(
        id: id ?? this.id,
        name: name ?? this.name,
        updatedAt: updatedAt ?? this.updatedAt,
      );
  LocalWikiProject copyWithCompanion(WikiProjectsCompanion data) {
    return LocalWikiProject(
      id: data.id.present ? data.id.value : this.id,
      name: data.name.present ? data.name.value : this.name,
      updatedAt: data.updatedAt.present ? data.updatedAt.value : this.updatedAt,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalWikiProject(')
          ..write('id: $id, ')
          ..write('name: $name, ')
          ..write('updatedAt: $updatedAt')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(id, name, updatedAt);
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalWikiProject &&
          other.id == this.id &&
          other.name == this.name &&
          other.updatedAt == this.updatedAt);
}

class WikiProjectsCompanion extends UpdateCompanion<LocalWikiProject> {
  final Value<String> id;
  final Value<String> name;
  final Value<DateTime> updatedAt;
  final Value<int> rowid;
  const WikiProjectsCompanion({
    this.id = const Value.absent(),
    this.name = const Value.absent(),
    this.updatedAt = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  WikiProjectsCompanion.insert({
    required String id,
    required String name,
    required DateTime updatedAt,
    this.rowid = const Value.absent(),
  }) : id = Value(id),
       name = Value(name),
       updatedAt = Value(updatedAt);
  static Insertable<LocalWikiProject> custom({
    Expression<String>? id,
    Expression<String>? name,
    Expression<DateTime>? updatedAt,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (name != null) 'name': name,
      if (updatedAt != null) 'updated_at': updatedAt,
      if (rowid != null) 'rowid': rowid,
    });
  }

  WikiProjectsCompanion copyWith({
    Value<String>? id,
    Value<String>? name,
    Value<DateTime>? updatedAt,
    Value<int>? rowid,
  }) {
    return WikiProjectsCompanion(
      id: id ?? this.id,
      name: name ?? this.name,
      updatedAt: updatedAt ?? this.updatedAt,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<String>(id.value);
    }
    if (name.present) {
      map['name'] = Variable<String>(name.value);
    }
    if (updatedAt.present) {
      map['updated_at'] = Variable<DateTime>(updatedAt.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('WikiProjectsCompanion(')
          ..write('id: $id, ')
          ..write('name: $name, ')
          ..write('updatedAt: $updatedAt, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $WikiPagesTable extends WikiPages
    with TableInfo<$WikiPagesTable, LocalWikiPage> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $WikiPagesTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<String> id = GeneratedColumn<String>(
    'id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _projectIdMeta = const VerificationMeta(
    'projectId',
  );
  @override
  late final GeneratedColumn<String> projectId = GeneratedColumn<String>(
    'project_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _titleMeta = const VerificationMeta('title');
  @override
  late final GeneratedColumn<String> title = GeneratedColumn<String>(
    'title',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant(''),
  );
  static const VerificationMeta _versionMeta = const VerificationMeta(
    'version',
  );
  @override
  late final GeneratedColumn<int> version = GeneratedColumn<int>(
    'version',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultValue: const Constant(1),
  );
  static const VerificationMeta _parentIdMeta = const VerificationMeta(
    'parentId',
  );
  @override
  late final GeneratedColumn<String> parentId = GeneratedColumn<String>(
    'parent_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _updatedAtMeta = const VerificationMeta(
    'updatedAt',
  );
  @override
  late final GeneratedColumn<DateTime> updatedAt = GeneratedColumn<DateTime>(
    'updated_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  @override
  List<GeneratedColumn> get $columns => [
    id,
    projectId,
    title,
    version,
    parentId,
    updatedAt,
  ];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'wiki_pages';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalWikiPage> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    } else if (isInserting) {
      context.missing(_idMeta);
    }
    if (data.containsKey('project_id')) {
      context.handle(
        _projectIdMeta,
        projectId.isAcceptableOrUnknown(data['project_id']!, _projectIdMeta),
      );
    } else if (isInserting) {
      context.missing(_projectIdMeta);
    }
    if (data.containsKey('title')) {
      context.handle(
        _titleMeta,
        title.isAcceptableOrUnknown(data['title']!, _titleMeta),
      );
    }
    if (data.containsKey('version')) {
      context.handle(
        _versionMeta,
        version.isAcceptableOrUnknown(data['version']!, _versionMeta),
      );
    }
    if (data.containsKey('parent_id')) {
      context.handle(
        _parentIdMeta,
        parentId.isAcceptableOrUnknown(data['parent_id']!, _parentIdMeta),
      );
    }
    if (data.containsKey('updated_at')) {
      context.handle(
        _updatedAtMeta,
        updatedAt.isAcceptableOrUnknown(data['updated_at']!, _updatedAtMeta),
      );
    } else if (isInserting) {
      context.missing(_updatedAtMeta);
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalWikiPage map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalWikiPage(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}id'],
      )!,
      projectId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}project_id'],
      )!,
      title: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}title'],
      )!,
      version: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}version'],
      )!,
      parentId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}parent_id'],
      ),
      updatedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}updated_at'],
      )!,
    );
  }

  @override
  $WikiPagesTable createAlias(String alias) {
    return $WikiPagesTable(attachedDatabase, alias);
  }
}

class LocalWikiPage extends DataClass implements Insertable<LocalWikiPage> {
  final String id;
  final String projectId;
  final String title;
  final int version;
  final String? parentId;
  final DateTime updatedAt;
  const LocalWikiPage({
    required this.id,
    required this.projectId,
    required this.title,
    required this.version,
    this.parentId,
    required this.updatedAt,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<String>(id);
    map['project_id'] = Variable<String>(projectId);
    map['title'] = Variable<String>(title);
    map['version'] = Variable<int>(version);
    if (!nullToAbsent || parentId != null) {
      map['parent_id'] = Variable<String>(parentId);
    }
    map['updated_at'] = Variable<DateTime>(updatedAt);
    return map;
  }

  WikiPagesCompanion toCompanion(bool nullToAbsent) {
    return WikiPagesCompanion(
      id: Value(id),
      projectId: Value(projectId),
      title: Value(title),
      version: Value(version),
      parentId: parentId == null && nullToAbsent
          ? const Value.absent()
          : Value(parentId),
      updatedAt: Value(updatedAt),
    );
  }

  factory LocalWikiPage.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalWikiPage(
      id: serializer.fromJson<String>(json['id']),
      projectId: serializer.fromJson<String>(json['projectId']),
      title: serializer.fromJson<String>(json['title']),
      version: serializer.fromJson<int>(json['version']),
      parentId: serializer.fromJson<String?>(json['parentId']),
      updatedAt: serializer.fromJson<DateTime>(json['updatedAt']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<String>(id),
      'projectId': serializer.toJson<String>(projectId),
      'title': serializer.toJson<String>(title),
      'version': serializer.toJson<int>(version),
      'parentId': serializer.toJson<String?>(parentId),
      'updatedAt': serializer.toJson<DateTime>(updatedAt),
    };
  }

  LocalWikiPage copyWith({
    String? id,
    String? projectId,
    String? title,
    int? version,
    Value<String?> parentId = const Value.absent(),
    DateTime? updatedAt,
  }) => LocalWikiPage(
    id: id ?? this.id,
    projectId: projectId ?? this.projectId,
    title: title ?? this.title,
    version: version ?? this.version,
    parentId: parentId.present ? parentId.value : this.parentId,
    updatedAt: updatedAt ?? this.updatedAt,
  );
  LocalWikiPage copyWithCompanion(WikiPagesCompanion data) {
    return LocalWikiPage(
      id: data.id.present ? data.id.value : this.id,
      projectId: data.projectId.present ? data.projectId.value : this.projectId,
      title: data.title.present ? data.title.value : this.title,
      version: data.version.present ? data.version.value : this.version,
      parentId: data.parentId.present ? data.parentId.value : this.parentId,
      updatedAt: data.updatedAt.present ? data.updatedAt.value : this.updatedAt,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalWikiPage(')
          ..write('id: $id, ')
          ..write('projectId: $projectId, ')
          ..write('title: $title, ')
          ..write('version: $version, ')
          ..write('parentId: $parentId, ')
          ..write('updatedAt: $updatedAt')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode =>
      Object.hash(id, projectId, title, version, parentId, updatedAt);
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalWikiPage &&
          other.id == this.id &&
          other.projectId == this.projectId &&
          other.title == this.title &&
          other.version == this.version &&
          other.parentId == this.parentId &&
          other.updatedAt == this.updatedAt);
}

class WikiPagesCompanion extends UpdateCompanion<LocalWikiPage> {
  final Value<String> id;
  final Value<String> projectId;
  final Value<String> title;
  final Value<int> version;
  final Value<String?> parentId;
  final Value<DateTime> updatedAt;
  final Value<int> rowid;
  const WikiPagesCompanion({
    this.id = const Value.absent(),
    this.projectId = const Value.absent(),
    this.title = const Value.absent(),
    this.version = const Value.absent(),
    this.parentId = const Value.absent(),
    this.updatedAt = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  WikiPagesCompanion.insert({
    required String id,
    required String projectId,
    this.title = const Value.absent(),
    this.version = const Value.absent(),
    this.parentId = const Value.absent(),
    required DateTime updatedAt,
    this.rowid = const Value.absent(),
  }) : id = Value(id),
       projectId = Value(projectId),
       updatedAt = Value(updatedAt);
  static Insertable<LocalWikiPage> custom({
    Expression<String>? id,
    Expression<String>? projectId,
    Expression<String>? title,
    Expression<int>? version,
    Expression<String>? parentId,
    Expression<DateTime>? updatedAt,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (projectId != null) 'project_id': projectId,
      if (title != null) 'title': title,
      if (version != null) 'version': version,
      if (parentId != null) 'parent_id': parentId,
      if (updatedAt != null) 'updated_at': updatedAt,
      if (rowid != null) 'rowid': rowid,
    });
  }

  WikiPagesCompanion copyWith({
    Value<String>? id,
    Value<String>? projectId,
    Value<String>? title,
    Value<int>? version,
    Value<String?>? parentId,
    Value<DateTime>? updatedAt,
    Value<int>? rowid,
  }) {
    return WikiPagesCompanion(
      id: id ?? this.id,
      projectId: projectId ?? this.projectId,
      title: title ?? this.title,
      version: version ?? this.version,
      parentId: parentId ?? this.parentId,
      updatedAt: updatedAt ?? this.updatedAt,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<String>(id.value);
    }
    if (projectId.present) {
      map['project_id'] = Variable<String>(projectId.value);
    }
    if (title.present) {
      map['title'] = Variable<String>(title.value);
    }
    if (version.present) {
      map['version'] = Variable<int>(version.value);
    }
    if (parentId.present) {
      map['parent_id'] = Variable<String>(parentId.value);
    }
    if (updatedAt.present) {
      map['updated_at'] = Variable<DateTime>(updatedAt.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('WikiPagesCompanion(')
          ..write('id: $id, ')
          ..write('projectId: $projectId, ')
          ..write('title: $title, ')
          ..write('version: $version, ')
          ..write('parentId: $parentId, ')
          ..write('updatedAt: $updatedAt, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $WikiBlocksTable extends WikiBlocks
    with TableInfo<$WikiBlocksTable, LocalWikiBlock> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $WikiBlocksTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<String> id = GeneratedColumn<String>(
    'id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _pageIdMeta = const VerificationMeta('pageId');
  @override
  late final GeneratedColumn<String> pageId = GeneratedColumn<String>(
    'page_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _positionMeta = const VerificationMeta(
    'position',
  );
  @override
  late final GeneratedColumn<double> position = GeneratedColumn<double>(
    'position',
    aliasedName,
    false,
    type: DriftSqlType.double,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _typeMeta = const VerificationMeta('type');
  @override
  late final GeneratedColumn<String> type = GeneratedColumn<String>(
    'type',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _contentJsonMeta = const VerificationMeta(
    'contentJson',
  );
  @override
  late final GeneratedColumn<String> contentJson = GeneratedColumn<String>(
    'content_json',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _versionMeta = const VerificationMeta(
    'version',
  );
  @override
  late final GeneratedColumn<int> version = GeneratedColumn<int>(
    'version',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultValue: const Constant(1),
  );
  static const VerificationMeta _deletedMeta = const VerificationMeta(
    'deleted',
  );
  @override
  late final GeneratedColumn<bool> deleted = GeneratedColumn<bool>(
    'deleted',
    aliasedName,
    false,
    type: DriftSqlType.bool,
    requiredDuringInsert: false,
    defaultConstraints: GeneratedColumn.constraintIsAlways(
      'CHECK ("deleted" IN (0, 1))',
    ),
    defaultValue: const Constant(false),
  );
  static const VerificationMeta _updatedAtMeta = const VerificationMeta(
    'updatedAt',
  );
  @override
  late final GeneratedColumn<DateTime> updatedAt = GeneratedColumn<DateTime>(
    'updated_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  @override
  List<GeneratedColumn> get $columns => [
    id,
    pageId,
    position,
    type,
    contentJson,
    version,
    deleted,
    updatedAt,
  ];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'wiki_blocks';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalWikiBlock> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    } else if (isInserting) {
      context.missing(_idMeta);
    }
    if (data.containsKey('page_id')) {
      context.handle(
        _pageIdMeta,
        pageId.isAcceptableOrUnknown(data['page_id']!, _pageIdMeta),
      );
    } else if (isInserting) {
      context.missing(_pageIdMeta);
    }
    if (data.containsKey('position')) {
      context.handle(
        _positionMeta,
        position.isAcceptableOrUnknown(data['position']!, _positionMeta),
      );
    } else if (isInserting) {
      context.missing(_positionMeta);
    }
    if (data.containsKey('type')) {
      context.handle(
        _typeMeta,
        type.isAcceptableOrUnknown(data['type']!, _typeMeta),
      );
    } else if (isInserting) {
      context.missing(_typeMeta);
    }
    if (data.containsKey('content_json')) {
      context.handle(
        _contentJsonMeta,
        contentJson.isAcceptableOrUnknown(
          data['content_json']!,
          _contentJsonMeta,
        ),
      );
    } else if (isInserting) {
      context.missing(_contentJsonMeta);
    }
    if (data.containsKey('version')) {
      context.handle(
        _versionMeta,
        version.isAcceptableOrUnknown(data['version']!, _versionMeta),
      );
    }
    if (data.containsKey('deleted')) {
      context.handle(
        _deletedMeta,
        deleted.isAcceptableOrUnknown(data['deleted']!, _deletedMeta),
      );
    }
    if (data.containsKey('updated_at')) {
      context.handle(
        _updatedAtMeta,
        updatedAt.isAcceptableOrUnknown(data['updated_at']!, _updatedAtMeta),
      );
    } else if (isInserting) {
      context.missing(_updatedAtMeta);
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalWikiBlock map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalWikiBlock(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}id'],
      )!,
      pageId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}page_id'],
      )!,
      position: attachedDatabase.typeMapping.read(
        DriftSqlType.double,
        data['${effectivePrefix}position'],
      )!,
      type: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}type'],
      )!,
      contentJson: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}content_json'],
      )!,
      version: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}version'],
      )!,
      deleted: attachedDatabase.typeMapping.read(
        DriftSqlType.bool,
        data['${effectivePrefix}deleted'],
      )!,
      updatedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}updated_at'],
      )!,
    );
  }

  @override
  $WikiBlocksTable createAlias(String alias) {
    return $WikiBlocksTable(attachedDatabase, alias);
  }
}

class LocalWikiBlock extends DataClass implements Insertable<LocalWikiBlock> {
  final String id;
  final String pageId;
  final double position;
  final String type;
  final String contentJson;
  final int version;
  final bool deleted;
  final DateTime updatedAt;
  const LocalWikiBlock({
    required this.id,
    required this.pageId,
    required this.position,
    required this.type,
    required this.contentJson,
    required this.version,
    required this.deleted,
    required this.updatedAt,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<String>(id);
    map['page_id'] = Variable<String>(pageId);
    map['position'] = Variable<double>(position);
    map['type'] = Variable<String>(type);
    map['content_json'] = Variable<String>(contentJson);
    map['version'] = Variable<int>(version);
    map['deleted'] = Variable<bool>(deleted);
    map['updated_at'] = Variable<DateTime>(updatedAt);
    return map;
  }

  WikiBlocksCompanion toCompanion(bool nullToAbsent) {
    return WikiBlocksCompanion(
      id: Value(id),
      pageId: Value(pageId),
      position: Value(position),
      type: Value(type),
      contentJson: Value(contentJson),
      version: Value(version),
      deleted: Value(deleted),
      updatedAt: Value(updatedAt),
    );
  }

  factory LocalWikiBlock.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalWikiBlock(
      id: serializer.fromJson<String>(json['id']),
      pageId: serializer.fromJson<String>(json['pageId']),
      position: serializer.fromJson<double>(json['position']),
      type: serializer.fromJson<String>(json['type']),
      contentJson: serializer.fromJson<String>(json['contentJson']),
      version: serializer.fromJson<int>(json['version']),
      deleted: serializer.fromJson<bool>(json['deleted']),
      updatedAt: serializer.fromJson<DateTime>(json['updatedAt']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<String>(id),
      'pageId': serializer.toJson<String>(pageId),
      'position': serializer.toJson<double>(position),
      'type': serializer.toJson<String>(type),
      'contentJson': serializer.toJson<String>(contentJson),
      'version': serializer.toJson<int>(version),
      'deleted': serializer.toJson<bool>(deleted),
      'updatedAt': serializer.toJson<DateTime>(updatedAt),
    };
  }

  LocalWikiBlock copyWith({
    String? id,
    String? pageId,
    double? position,
    String? type,
    String? contentJson,
    int? version,
    bool? deleted,
    DateTime? updatedAt,
  }) => LocalWikiBlock(
    id: id ?? this.id,
    pageId: pageId ?? this.pageId,
    position: position ?? this.position,
    type: type ?? this.type,
    contentJson: contentJson ?? this.contentJson,
    version: version ?? this.version,
    deleted: deleted ?? this.deleted,
    updatedAt: updatedAt ?? this.updatedAt,
  );
  LocalWikiBlock copyWithCompanion(WikiBlocksCompanion data) {
    return LocalWikiBlock(
      id: data.id.present ? data.id.value : this.id,
      pageId: data.pageId.present ? data.pageId.value : this.pageId,
      position: data.position.present ? data.position.value : this.position,
      type: data.type.present ? data.type.value : this.type,
      contentJson: data.contentJson.present
          ? data.contentJson.value
          : this.contentJson,
      version: data.version.present ? data.version.value : this.version,
      deleted: data.deleted.present ? data.deleted.value : this.deleted,
      updatedAt: data.updatedAt.present ? data.updatedAt.value : this.updatedAt,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalWikiBlock(')
          ..write('id: $id, ')
          ..write('pageId: $pageId, ')
          ..write('position: $position, ')
          ..write('type: $type, ')
          ..write('contentJson: $contentJson, ')
          ..write('version: $version, ')
          ..write('deleted: $deleted, ')
          ..write('updatedAt: $updatedAt')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(
    id,
    pageId,
    position,
    type,
    contentJson,
    version,
    deleted,
    updatedAt,
  );
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalWikiBlock &&
          other.id == this.id &&
          other.pageId == this.pageId &&
          other.position == this.position &&
          other.type == this.type &&
          other.contentJson == this.contentJson &&
          other.version == this.version &&
          other.deleted == this.deleted &&
          other.updatedAt == this.updatedAt);
}

class WikiBlocksCompanion extends UpdateCompanion<LocalWikiBlock> {
  final Value<String> id;
  final Value<String> pageId;
  final Value<double> position;
  final Value<String> type;
  final Value<String> contentJson;
  final Value<int> version;
  final Value<bool> deleted;
  final Value<DateTime> updatedAt;
  final Value<int> rowid;
  const WikiBlocksCompanion({
    this.id = const Value.absent(),
    this.pageId = const Value.absent(),
    this.position = const Value.absent(),
    this.type = const Value.absent(),
    this.contentJson = const Value.absent(),
    this.version = const Value.absent(),
    this.deleted = const Value.absent(),
    this.updatedAt = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  WikiBlocksCompanion.insert({
    required String id,
    required String pageId,
    required double position,
    required String type,
    required String contentJson,
    this.version = const Value.absent(),
    this.deleted = const Value.absent(),
    required DateTime updatedAt,
    this.rowid = const Value.absent(),
  }) : id = Value(id),
       pageId = Value(pageId),
       position = Value(position),
       type = Value(type),
       contentJson = Value(contentJson),
       updatedAt = Value(updatedAt);
  static Insertable<LocalWikiBlock> custom({
    Expression<String>? id,
    Expression<String>? pageId,
    Expression<double>? position,
    Expression<String>? type,
    Expression<String>? contentJson,
    Expression<int>? version,
    Expression<bool>? deleted,
    Expression<DateTime>? updatedAt,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (pageId != null) 'page_id': pageId,
      if (position != null) 'position': position,
      if (type != null) 'type': type,
      if (contentJson != null) 'content_json': contentJson,
      if (version != null) 'version': version,
      if (deleted != null) 'deleted': deleted,
      if (updatedAt != null) 'updated_at': updatedAt,
      if (rowid != null) 'rowid': rowid,
    });
  }

  WikiBlocksCompanion copyWith({
    Value<String>? id,
    Value<String>? pageId,
    Value<double>? position,
    Value<String>? type,
    Value<String>? contentJson,
    Value<int>? version,
    Value<bool>? deleted,
    Value<DateTime>? updatedAt,
    Value<int>? rowid,
  }) {
    return WikiBlocksCompanion(
      id: id ?? this.id,
      pageId: pageId ?? this.pageId,
      position: position ?? this.position,
      type: type ?? this.type,
      contentJson: contentJson ?? this.contentJson,
      version: version ?? this.version,
      deleted: deleted ?? this.deleted,
      updatedAt: updatedAt ?? this.updatedAt,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<String>(id.value);
    }
    if (pageId.present) {
      map['page_id'] = Variable<String>(pageId.value);
    }
    if (position.present) {
      map['position'] = Variable<double>(position.value);
    }
    if (type.present) {
      map['type'] = Variable<String>(type.value);
    }
    if (contentJson.present) {
      map['content_json'] = Variable<String>(contentJson.value);
    }
    if (version.present) {
      map['version'] = Variable<int>(version.value);
    }
    if (deleted.present) {
      map['deleted'] = Variable<bool>(deleted.value);
    }
    if (updatedAt.present) {
      map['updated_at'] = Variable<DateTime>(updatedAt.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('WikiBlocksCompanion(')
          ..write('id: $id, ')
          ..write('pageId: $pageId, ')
          ..write('position: $position, ')
          ..write('type: $type, ')
          ..write('contentJson: $contentJson, ')
          ..write('version: $version, ')
          ..write('deleted: $deleted, ')
          ..write('updatedAt: $updatedAt, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $WikiOutboxTable extends WikiOutbox
    with TableInfo<$WikiOutboxTable, OutboxEntry> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $WikiOutboxTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<int> id = GeneratedColumn<int>(
    'id',
    aliasedName,
    false,
    hasAutoIncrement: true,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultConstraints: GeneratedColumn.constraintIsAlways(
      'PRIMARY KEY AUTOINCREMENT',
    ),
  );
  static const VerificationMeta _opMeta = const VerificationMeta('op');
  @override
  late final GeneratedColumn<String> op = GeneratedColumn<String>(
    'op',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _entityIdMeta = const VerificationMeta(
    'entityId',
  );
  @override
  late final GeneratedColumn<String> entityId = GeneratedColumn<String>(
    'entity_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _projectIdMeta = const VerificationMeta(
    'projectId',
  );
  @override
  late final GeneratedColumn<String> projectId = GeneratedColumn<String>(
    'project_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _pageIdMeta = const VerificationMeta('pageId');
  @override
  late final GeneratedColumn<String> pageId = GeneratedColumn<String>(
    'page_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _payloadJsonMeta = const VerificationMeta(
    'payloadJson',
  );
  @override
  late final GeneratedColumn<String> payloadJson = GeneratedColumn<String>(
    'payload_json',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _baseVersionMeta = const VerificationMeta(
    'baseVersion',
  );
  @override
  late final GeneratedColumn<int> baseVersion = GeneratedColumn<int>(
    'base_version',
    aliasedName,
    true,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _attemptsMeta = const VerificationMeta(
    'attempts',
  );
  @override
  late final GeneratedColumn<int> attempts = GeneratedColumn<int>(
    'attempts',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultValue: const Constant(0),
  );
  static const VerificationMeta _lastErrorMeta = const VerificationMeta(
    'lastError',
  );
  @override
  late final GeneratedColumn<String> lastError = GeneratedColumn<String>(
    'last_error',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _createdAtMeta = const VerificationMeta(
    'createdAt',
  );
  @override
  late final GeneratedColumn<DateTime> createdAt = GeneratedColumn<DateTime>(
    'created_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _nextAttemptAtMeta = const VerificationMeta(
    'nextAttemptAt',
  );
  @override
  late final GeneratedColumn<DateTime> nextAttemptAt =
      GeneratedColumn<DateTime>(
        'next_attempt_at',
        aliasedName,
        false,
        type: DriftSqlType.dateTime,
        requiredDuringInsert: true,
      );
  @override
  List<GeneratedColumn> get $columns => [
    id,
    op,
    entityId,
    projectId,
    pageId,
    payloadJson,
    baseVersion,
    attempts,
    lastError,
    createdAt,
    nextAttemptAt,
  ];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'wiki_outbox';
  @override
  VerificationContext validateIntegrity(
    Insertable<OutboxEntry> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    }
    if (data.containsKey('op')) {
      context.handle(_opMeta, op.isAcceptableOrUnknown(data['op']!, _opMeta));
    } else if (isInserting) {
      context.missing(_opMeta);
    }
    if (data.containsKey('entity_id')) {
      context.handle(
        _entityIdMeta,
        entityId.isAcceptableOrUnknown(data['entity_id']!, _entityIdMeta),
      );
    } else if (isInserting) {
      context.missing(_entityIdMeta);
    }
    if (data.containsKey('project_id')) {
      context.handle(
        _projectIdMeta,
        projectId.isAcceptableOrUnknown(data['project_id']!, _projectIdMeta),
      );
    }
    if (data.containsKey('page_id')) {
      context.handle(
        _pageIdMeta,
        pageId.isAcceptableOrUnknown(data['page_id']!, _pageIdMeta),
      );
    }
    if (data.containsKey('payload_json')) {
      context.handle(
        _payloadJsonMeta,
        payloadJson.isAcceptableOrUnknown(
          data['payload_json']!,
          _payloadJsonMeta,
        ),
      );
    } else if (isInserting) {
      context.missing(_payloadJsonMeta);
    }
    if (data.containsKey('base_version')) {
      context.handle(
        _baseVersionMeta,
        baseVersion.isAcceptableOrUnknown(
          data['base_version']!,
          _baseVersionMeta,
        ),
      );
    }
    if (data.containsKey('attempts')) {
      context.handle(
        _attemptsMeta,
        attempts.isAcceptableOrUnknown(data['attempts']!, _attemptsMeta),
      );
    }
    if (data.containsKey('last_error')) {
      context.handle(
        _lastErrorMeta,
        lastError.isAcceptableOrUnknown(data['last_error']!, _lastErrorMeta),
      );
    }
    if (data.containsKey('created_at')) {
      context.handle(
        _createdAtMeta,
        createdAt.isAcceptableOrUnknown(data['created_at']!, _createdAtMeta),
      );
    } else if (isInserting) {
      context.missing(_createdAtMeta);
    }
    if (data.containsKey('next_attempt_at')) {
      context.handle(
        _nextAttemptAtMeta,
        nextAttemptAt.isAcceptableOrUnknown(
          data['next_attempt_at']!,
          _nextAttemptAtMeta,
        ),
      );
    } else if (isInserting) {
      context.missing(_nextAttemptAtMeta);
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  OutboxEntry map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return OutboxEntry(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}id'],
      )!,
      op: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}op'],
      )!,
      entityId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}entity_id'],
      )!,
      projectId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}project_id'],
      ),
      pageId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}page_id'],
      ),
      payloadJson: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}payload_json'],
      )!,
      baseVersion: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}base_version'],
      ),
      attempts: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}attempts'],
      )!,
      lastError: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}last_error'],
      ),
      createdAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}created_at'],
      )!,
      nextAttemptAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}next_attempt_at'],
      )!,
    );
  }

  @override
  $WikiOutboxTable createAlias(String alias) {
    return $WikiOutboxTable(attachedDatabase, alias);
  }
}

class OutboxEntry extends DataClass implements Insertable<OutboxEntry> {
  final int id;
  final String op;
  final String entityId;
  final String? projectId;
  final String? pageId;
  final String payloadJson;
  final int? baseVersion;
  final int attempts;
  final String? lastError;
  final DateTime createdAt;
  final DateTime nextAttemptAt;
  const OutboxEntry({
    required this.id,
    required this.op,
    required this.entityId,
    this.projectId,
    this.pageId,
    required this.payloadJson,
    this.baseVersion,
    required this.attempts,
    this.lastError,
    required this.createdAt,
    required this.nextAttemptAt,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<int>(id);
    map['op'] = Variable<String>(op);
    map['entity_id'] = Variable<String>(entityId);
    if (!nullToAbsent || projectId != null) {
      map['project_id'] = Variable<String>(projectId);
    }
    if (!nullToAbsent || pageId != null) {
      map['page_id'] = Variable<String>(pageId);
    }
    map['payload_json'] = Variable<String>(payloadJson);
    if (!nullToAbsent || baseVersion != null) {
      map['base_version'] = Variable<int>(baseVersion);
    }
    map['attempts'] = Variable<int>(attempts);
    if (!nullToAbsent || lastError != null) {
      map['last_error'] = Variable<String>(lastError);
    }
    map['created_at'] = Variable<DateTime>(createdAt);
    map['next_attempt_at'] = Variable<DateTime>(nextAttemptAt);
    return map;
  }

  WikiOutboxCompanion toCompanion(bool nullToAbsent) {
    return WikiOutboxCompanion(
      id: Value(id),
      op: Value(op),
      entityId: Value(entityId),
      projectId: projectId == null && nullToAbsent
          ? const Value.absent()
          : Value(projectId),
      pageId: pageId == null && nullToAbsent
          ? const Value.absent()
          : Value(pageId),
      payloadJson: Value(payloadJson),
      baseVersion: baseVersion == null && nullToAbsent
          ? const Value.absent()
          : Value(baseVersion),
      attempts: Value(attempts),
      lastError: lastError == null && nullToAbsent
          ? const Value.absent()
          : Value(lastError),
      createdAt: Value(createdAt),
      nextAttemptAt: Value(nextAttemptAt),
    );
  }

  factory OutboxEntry.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return OutboxEntry(
      id: serializer.fromJson<int>(json['id']),
      op: serializer.fromJson<String>(json['op']),
      entityId: serializer.fromJson<String>(json['entityId']),
      projectId: serializer.fromJson<String?>(json['projectId']),
      pageId: serializer.fromJson<String?>(json['pageId']),
      payloadJson: serializer.fromJson<String>(json['payloadJson']),
      baseVersion: serializer.fromJson<int?>(json['baseVersion']),
      attempts: serializer.fromJson<int>(json['attempts']),
      lastError: serializer.fromJson<String?>(json['lastError']),
      createdAt: serializer.fromJson<DateTime>(json['createdAt']),
      nextAttemptAt: serializer.fromJson<DateTime>(json['nextAttemptAt']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<int>(id),
      'op': serializer.toJson<String>(op),
      'entityId': serializer.toJson<String>(entityId),
      'projectId': serializer.toJson<String?>(projectId),
      'pageId': serializer.toJson<String?>(pageId),
      'payloadJson': serializer.toJson<String>(payloadJson),
      'baseVersion': serializer.toJson<int?>(baseVersion),
      'attempts': serializer.toJson<int>(attempts),
      'lastError': serializer.toJson<String?>(lastError),
      'createdAt': serializer.toJson<DateTime>(createdAt),
      'nextAttemptAt': serializer.toJson<DateTime>(nextAttemptAt),
    };
  }

  OutboxEntry copyWith({
    int? id,
    String? op,
    String? entityId,
    Value<String?> projectId = const Value.absent(),
    Value<String?> pageId = const Value.absent(),
    String? payloadJson,
    Value<int?> baseVersion = const Value.absent(),
    int? attempts,
    Value<String?> lastError = const Value.absent(),
    DateTime? createdAt,
    DateTime? nextAttemptAt,
  }) => OutboxEntry(
    id: id ?? this.id,
    op: op ?? this.op,
    entityId: entityId ?? this.entityId,
    projectId: projectId.present ? projectId.value : this.projectId,
    pageId: pageId.present ? pageId.value : this.pageId,
    payloadJson: payloadJson ?? this.payloadJson,
    baseVersion: baseVersion.present ? baseVersion.value : this.baseVersion,
    attempts: attempts ?? this.attempts,
    lastError: lastError.present ? lastError.value : this.lastError,
    createdAt: createdAt ?? this.createdAt,
    nextAttemptAt: nextAttemptAt ?? this.nextAttemptAt,
  );
  OutboxEntry copyWithCompanion(WikiOutboxCompanion data) {
    return OutboxEntry(
      id: data.id.present ? data.id.value : this.id,
      op: data.op.present ? data.op.value : this.op,
      entityId: data.entityId.present ? data.entityId.value : this.entityId,
      projectId: data.projectId.present ? data.projectId.value : this.projectId,
      pageId: data.pageId.present ? data.pageId.value : this.pageId,
      payloadJson: data.payloadJson.present
          ? data.payloadJson.value
          : this.payloadJson,
      baseVersion: data.baseVersion.present
          ? data.baseVersion.value
          : this.baseVersion,
      attempts: data.attempts.present ? data.attempts.value : this.attempts,
      lastError: data.lastError.present ? data.lastError.value : this.lastError,
      createdAt: data.createdAt.present ? data.createdAt.value : this.createdAt,
      nextAttemptAt: data.nextAttemptAt.present
          ? data.nextAttemptAt.value
          : this.nextAttemptAt,
    );
  }

  @override
  String toString() {
    return (StringBuffer('OutboxEntry(')
          ..write('id: $id, ')
          ..write('op: $op, ')
          ..write('entityId: $entityId, ')
          ..write('projectId: $projectId, ')
          ..write('pageId: $pageId, ')
          ..write('payloadJson: $payloadJson, ')
          ..write('baseVersion: $baseVersion, ')
          ..write('attempts: $attempts, ')
          ..write('lastError: $lastError, ')
          ..write('createdAt: $createdAt, ')
          ..write('nextAttemptAt: $nextAttemptAt')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(
    id,
    op,
    entityId,
    projectId,
    pageId,
    payloadJson,
    baseVersion,
    attempts,
    lastError,
    createdAt,
    nextAttemptAt,
  );
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is OutboxEntry &&
          other.id == this.id &&
          other.op == this.op &&
          other.entityId == this.entityId &&
          other.projectId == this.projectId &&
          other.pageId == this.pageId &&
          other.payloadJson == this.payloadJson &&
          other.baseVersion == this.baseVersion &&
          other.attempts == this.attempts &&
          other.lastError == this.lastError &&
          other.createdAt == this.createdAt &&
          other.nextAttemptAt == this.nextAttemptAt);
}

class WikiOutboxCompanion extends UpdateCompanion<OutboxEntry> {
  final Value<int> id;
  final Value<String> op;
  final Value<String> entityId;
  final Value<String?> projectId;
  final Value<String?> pageId;
  final Value<String> payloadJson;
  final Value<int?> baseVersion;
  final Value<int> attempts;
  final Value<String?> lastError;
  final Value<DateTime> createdAt;
  final Value<DateTime> nextAttemptAt;
  const WikiOutboxCompanion({
    this.id = const Value.absent(),
    this.op = const Value.absent(),
    this.entityId = const Value.absent(),
    this.projectId = const Value.absent(),
    this.pageId = const Value.absent(),
    this.payloadJson = const Value.absent(),
    this.baseVersion = const Value.absent(),
    this.attempts = const Value.absent(),
    this.lastError = const Value.absent(),
    this.createdAt = const Value.absent(),
    this.nextAttemptAt = const Value.absent(),
  });
  WikiOutboxCompanion.insert({
    this.id = const Value.absent(),
    required String op,
    required String entityId,
    this.projectId = const Value.absent(),
    this.pageId = const Value.absent(),
    required String payloadJson,
    this.baseVersion = const Value.absent(),
    this.attempts = const Value.absent(),
    this.lastError = const Value.absent(),
    required DateTime createdAt,
    required DateTime nextAttemptAt,
  }) : op = Value(op),
       entityId = Value(entityId),
       payloadJson = Value(payloadJson),
       createdAt = Value(createdAt),
       nextAttemptAt = Value(nextAttemptAt);
  static Insertable<OutboxEntry> custom({
    Expression<int>? id,
    Expression<String>? op,
    Expression<String>? entityId,
    Expression<String>? projectId,
    Expression<String>? pageId,
    Expression<String>? payloadJson,
    Expression<int>? baseVersion,
    Expression<int>? attempts,
    Expression<String>? lastError,
    Expression<DateTime>? createdAt,
    Expression<DateTime>? nextAttemptAt,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (op != null) 'op': op,
      if (entityId != null) 'entity_id': entityId,
      if (projectId != null) 'project_id': projectId,
      if (pageId != null) 'page_id': pageId,
      if (payloadJson != null) 'payload_json': payloadJson,
      if (baseVersion != null) 'base_version': baseVersion,
      if (attempts != null) 'attempts': attempts,
      if (lastError != null) 'last_error': lastError,
      if (createdAt != null) 'created_at': createdAt,
      if (nextAttemptAt != null) 'next_attempt_at': nextAttemptAt,
    });
  }

  WikiOutboxCompanion copyWith({
    Value<int>? id,
    Value<String>? op,
    Value<String>? entityId,
    Value<String?>? projectId,
    Value<String?>? pageId,
    Value<String>? payloadJson,
    Value<int?>? baseVersion,
    Value<int>? attempts,
    Value<String?>? lastError,
    Value<DateTime>? createdAt,
    Value<DateTime>? nextAttemptAt,
  }) {
    return WikiOutboxCompanion(
      id: id ?? this.id,
      op: op ?? this.op,
      entityId: entityId ?? this.entityId,
      projectId: projectId ?? this.projectId,
      pageId: pageId ?? this.pageId,
      payloadJson: payloadJson ?? this.payloadJson,
      baseVersion: baseVersion ?? this.baseVersion,
      attempts: attempts ?? this.attempts,
      lastError: lastError ?? this.lastError,
      createdAt: createdAt ?? this.createdAt,
      nextAttemptAt: nextAttemptAt ?? this.nextAttemptAt,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<int>(id.value);
    }
    if (op.present) {
      map['op'] = Variable<String>(op.value);
    }
    if (entityId.present) {
      map['entity_id'] = Variable<String>(entityId.value);
    }
    if (projectId.present) {
      map['project_id'] = Variable<String>(projectId.value);
    }
    if (pageId.present) {
      map['page_id'] = Variable<String>(pageId.value);
    }
    if (payloadJson.present) {
      map['payload_json'] = Variable<String>(payloadJson.value);
    }
    if (baseVersion.present) {
      map['base_version'] = Variable<int>(baseVersion.value);
    }
    if (attempts.present) {
      map['attempts'] = Variable<int>(attempts.value);
    }
    if (lastError.present) {
      map['last_error'] = Variable<String>(lastError.value);
    }
    if (createdAt.present) {
      map['created_at'] = Variable<DateTime>(createdAt.value);
    }
    if (nextAttemptAt.present) {
      map['next_attempt_at'] = Variable<DateTime>(nextAttemptAt.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('WikiOutboxCompanion(')
          ..write('id: $id, ')
          ..write('op: $op, ')
          ..write('entityId: $entityId, ')
          ..write('projectId: $projectId, ')
          ..write('pageId: $pageId, ')
          ..write('payloadJson: $payloadJson, ')
          ..write('baseVersion: $baseVersion, ')
          ..write('attempts: $attempts, ')
          ..write('lastError: $lastError, ')
          ..write('createdAt: $createdAt, ')
          ..write('nextAttemptAt: $nextAttemptAt')
          ..write(')'))
        .toString();
  }
}

class $NoteNotebooksTable extends NoteNotebooks
    with TableInfo<$NoteNotebooksTable, LocalNoteNotebook> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $NoteNotebooksTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<String> id = GeneratedColumn<String>(
    'id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _nameMeta = const VerificationMeta('name');
  @override
  late final GeneratedColumn<String> name = GeneratedColumn<String>(
    'name',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _positionMeta = const VerificationMeta(
    'position',
  );
  @override
  late final GeneratedColumn<double> position = GeneratedColumn<double>(
    'position',
    aliasedName,
    false,
    type: DriftSqlType.double,
    requiredDuringInsert: false,
    defaultValue: const Constant(0.0),
  );
  static const VerificationMeta _updatedAtMeta = const VerificationMeta(
    'updatedAt',
  );
  @override
  late final GeneratedColumn<DateTime> updatedAt = GeneratedColumn<DateTime>(
    'updated_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  @override
  List<GeneratedColumn> get $columns => [id, name, position, updatedAt];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'note_notebooks';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalNoteNotebook> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    } else if (isInserting) {
      context.missing(_idMeta);
    }
    if (data.containsKey('name')) {
      context.handle(
        _nameMeta,
        name.isAcceptableOrUnknown(data['name']!, _nameMeta),
      );
    } else if (isInserting) {
      context.missing(_nameMeta);
    }
    if (data.containsKey('position')) {
      context.handle(
        _positionMeta,
        position.isAcceptableOrUnknown(data['position']!, _positionMeta),
      );
    }
    if (data.containsKey('updated_at')) {
      context.handle(
        _updatedAtMeta,
        updatedAt.isAcceptableOrUnknown(data['updated_at']!, _updatedAtMeta),
      );
    } else if (isInserting) {
      context.missing(_updatedAtMeta);
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalNoteNotebook map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalNoteNotebook(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}id'],
      )!,
      name: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}name'],
      )!,
      position: attachedDatabase.typeMapping.read(
        DriftSqlType.double,
        data['${effectivePrefix}position'],
      )!,
      updatedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}updated_at'],
      )!,
    );
  }

  @override
  $NoteNotebooksTable createAlias(String alias) {
    return $NoteNotebooksTable(attachedDatabase, alias);
  }
}

class LocalNoteNotebook extends DataClass
    implements Insertable<LocalNoteNotebook> {
  final String id;
  final String name;
  final double position;
  final DateTime updatedAt;
  const LocalNoteNotebook({
    required this.id,
    required this.name,
    required this.position,
    required this.updatedAt,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<String>(id);
    map['name'] = Variable<String>(name);
    map['position'] = Variable<double>(position);
    map['updated_at'] = Variable<DateTime>(updatedAt);
    return map;
  }

  NoteNotebooksCompanion toCompanion(bool nullToAbsent) {
    return NoteNotebooksCompanion(
      id: Value(id),
      name: Value(name),
      position: Value(position),
      updatedAt: Value(updatedAt),
    );
  }

  factory LocalNoteNotebook.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalNoteNotebook(
      id: serializer.fromJson<String>(json['id']),
      name: serializer.fromJson<String>(json['name']),
      position: serializer.fromJson<double>(json['position']),
      updatedAt: serializer.fromJson<DateTime>(json['updatedAt']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<String>(id),
      'name': serializer.toJson<String>(name),
      'position': serializer.toJson<double>(position),
      'updatedAt': serializer.toJson<DateTime>(updatedAt),
    };
  }

  LocalNoteNotebook copyWith({
    String? id,
    String? name,
    double? position,
    DateTime? updatedAt,
  }) => LocalNoteNotebook(
    id: id ?? this.id,
    name: name ?? this.name,
    position: position ?? this.position,
    updatedAt: updatedAt ?? this.updatedAt,
  );
  LocalNoteNotebook copyWithCompanion(NoteNotebooksCompanion data) {
    return LocalNoteNotebook(
      id: data.id.present ? data.id.value : this.id,
      name: data.name.present ? data.name.value : this.name,
      position: data.position.present ? data.position.value : this.position,
      updatedAt: data.updatedAt.present ? data.updatedAt.value : this.updatedAt,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalNoteNotebook(')
          ..write('id: $id, ')
          ..write('name: $name, ')
          ..write('position: $position, ')
          ..write('updatedAt: $updatedAt')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(id, name, position, updatedAt);
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalNoteNotebook &&
          other.id == this.id &&
          other.name == this.name &&
          other.position == this.position &&
          other.updatedAt == this.updatedAt);
}

class NoteNotebooksCompanion extends UpdateCompanion<LocalNoteNotebook> {
  final Value<String> id;
  final Value<String> name;
  final Value<double> position;
  final Value<DateTime> updatedAt;
  final Value<int> rowid;
  const NoteNotebooksCompanion({
    this.id = const Value.absent(),
    this.name = const Value.absent(),
    this.position = const Value.absent(),
    this.updatedAt = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  NoteNotebooksCompanion.insert({
    required String id,
    required String name,
    this.position = const Value.absent(),
    required DateTime updatedAt,
    this.rowid = const Value.absent(),
  }) : id = Value(id),
       name = Value(name),
       updatedAt = Value(updatedAt);
  static Insertable<LocalNoteNotebook> custom({
    Expression<String>? id,
    Expression<String>? name,
    Expression<double>? position,
    Expression<DateTime>? updatedAt,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (name != null) 'name': name,
      if (position != null) 'position': position,
      if (updatedAt != null) 'updated_at': updatedAt,
      if (rowid != null) 'rowid': rowid,
    });
  }

  NoteNotebooksCompanion copyWith({
    Value<String>? id,
    Value<String>? name,
    Value<double>? position,
    Value<DateTime>? updatedAt,
    Value<int>? rowid,
  }) {
    return NoteNotebooksCompanion(
      id: id ?? this.id,
      name: name ?? this.name,
      position: position ?? this.position,
      updatedAt: updatedAt ?? this.updatedAt,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<String>(id.value);
    }
    if (name.present) {
      map['name'] = Variable<String>(name.value);
    }
    if (position.present) {
      map['position'] = Variable<double>(position.value);
    }
    if (updatedAt.present) {
      map['updated_at'] = Variable<DateTime>(updatedAt.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('NoteNotebooksCompanion(')
          ..write('id: $id, ')
          ..write('name: $name, ')
          ..write('position: $position, ')
          ..write('updatedAt: $updatedAt, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $NoteNotesTable extends NoteNotes
    with TableInfo<$NoteNotesTable, LocalNote> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $NoteNotesTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<String> id = GeneratedColumn<String>(
    'id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _notebookIdMeta = const VerificationMeta(
    'notebookId',
  );
  @override
  late final GeneratedColumn<String> notebookId = GeneratedColumn<String>(
    'notebook_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _titleMeta = const VerificationMeta('title');
  @override
  late final GeneratedColumn<String> title = GeneratedColumn<String>(
    'title',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant(''),
  );
  static const VerificationMeta _contentMdMeta = const VerificationMeta(
    'contentMd',
  );
  @override
  late final GeneratedColumn<String> contentMd = GeneratedColumn<String>(
    'content_md',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant(''),
  );
  static const VerificationMeta _isTodoMeta = const VerificationMeta('isTodo');
  @override
  late final GeneratedColumn<bool> isTodo = GeneratedColumn<bool>(
    'is_todo',
    aliasedName,
    false,
    type: DriftSqlType.bool,
    requiredDuringInsert: false,
    defaultConstraints: GeneratedColumn.constraintIsAlways(
      'CHECK ("is_todo" IN (0, 1))',
    ),
    defaultValue: const Constant(false),
  );
  static const VerificationMeta _todoCompletedAtMeta = const VerificationMeta(
    'todoCompletedAt',
  );
  @override
  late final GeneratedColumn<DateTime> todoCompletedAt =
      GeneratedColumn<DateTime>(
        'todo_completed_at',
        aliasedName,
        true,
        type: DriftSqlType.dateTime,
        requiredDuringInsert: false,
      );
  static const VerificationMeta _positionMeta = const VerificationMeta(
    'position',
  );
  @override
  late final GeneratedColumn<double> position = GeneratedColumn<double>(
    'position',
    aliasedName,
    false,
    type: DriftSqlType.double,
    requiredDuringInsert: false,
    defaultValue: const Constant(0.0),
  );
  static const VerificationMeta _versionMeta = const VerificationMeta(
    'version',
  );
  @override
  late final GeneratedColumn<int> version = GeneratedColumn<int>(
    'version',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultValue: const Constant(1),
  );
  static const VerificationMeta _trashedMeta = const VerificationMeta(
    'trashed',
  );
  @override
  late final GeneratedColumn<bool> trashed = GeneratedColumn<bool>(
    'trashed',
    aliasedName,
    false,
    type: DriftSqlType.bool,
    requiredDuringInsert: false,
    defaultConstraints: GeneratedColumn.constraintIsAlways(
      'CHECK ("trashed" IN (0, 1))',
    ),
    defaultValue: const Constant(false),
  );
  static const VerificationMeta _trashedAtMeta = const VerificationMeta(
    'trashedAt',
  );
  @override
  late final GeneratedColumn<DateTime> trashedAt = GeneratedColumn<DateTime>(
    'trashed_at',
    aliasedName,
    true,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _archivedAtMeta = const VerificationMeta(
    'archivedAt',
  );
  @override
  late final GeneratedColumn<DateTime> archivedAt = GeneratedColumn<DateTime>(
    'archived_at',
    aliasedName,
    true,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _promotedPageIdMeta = const VerificationMeta(
    'promotedPageId',
  );
  @override
  late final GeneratedColumn<String> promotedPageId = GeneratedColumn<String>(
    'promoted_page_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _updatedAtMeta = const VerificationMeta(
    'updatedAt',
  );
  @override
  late final GeneratedColumn<DateTime> updatedAt = GeneratedColumn<DateTime>(
    'updated_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  @override
  List<GeneratedColumn> get $columns => [
    id,
    notebookId,
    title,
    contentMd,
    isTodo,
    todoCompletedAt,
    position,
    version,
    trashed,
    trashedAt,
    archivedAt,
    promotedPageId,
    updatedAt,
  ];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'note_notes';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalNote> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    } else if (isInserting) {
      context.missing(_idMeta);
    }
    if (data.containsKey('notebook_id')) {
      context.handle(
        _notebookIdMeta,
        notebookId.isAcceptableOrUnknown(data['notebook_id']!, _notebookIdMeta),
      );
    }
    if (data.containsKey('title')) {
      context.handle(
        _titleMeta,
        title.isAcceptableOrUnknown(data['title']!, _titleMeta),
      );
    }
    if (data.containsKey('content_md')) {
      context.handle(
        _contentMdMeta,
        contentMd.isAcceptableOrUnknown(data['content_md']!, _contentMdMeta),
      );
    }
    if (data.containsKey('is_todo')) {
      context.handle(
        _isTodoMeta,
        isTodo.isAcceptableOrUnknown(data['is_todo']!, _isTodoMeta),
      );
    }
    if (data.containsKey('todo_completed_at')) {
      context.handle(
        _todoCompletedAtMeta,
        todoCompletedAt.isAcceptableOrUnknown(
          data['todo_completed_at']!,
          _todoCompletedAtMeta,
        ),
      );
    }
    if (data.containsKey('position')) {
      context.handle(
        _positionMeta,
        position.isAcceptableOrUnknown(data['position']!, _positionMeta),
      );
    }
    if (data.containsKey('version')) {
      context.handle(
        _versionMeta,
        version.isAcceptableOrUnknown(data['version']!, _versionMeta),
      );
    }
    if (data.containsKey('trashed')) {
      context.handle(
        _trashedMeta,
        trashed.isAcceptableOrUnknown(data['trashed']!, _trashedMeta),
      );
    }
    if (data.containsKey('trashed_at')) {
      context.handle(
        _trashedAtMeta,
        trashedAt.isAcceptableOrUnknown(data['trashed_at']!, _trashedAtMeta),
      );
    }
    if (data.containsKey('archived_at')) {
      context.handle(
        _archivedAtMeta,
        archivedAt.isAcceptableOrUnknown(data['archived_at']!, _archivedAtMeta),
      );
    }
    if (data.containsKey('promoted_page_id')) {
      context.handle(
        _promotedPageIdMeta,
        promotedPageId.isAcceptableOrUnknown(
          data['promoted_page_id']!,
          _promotedPageIdMeta,
        ),
      );
    }
    if (data.containsKey('updated_at')) {
      context.handle(
        _updatedAtMeta,
        updatedAt.isAcceptableOrUnknown(data['updated_at']!, _updatedAtMeta),
      );
    } else if (isInserting) {
      context.missing(_updatedAtMeta);
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalNote map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalNote(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}id'],
      )!,
      notebookId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}notebook_id'],
      ),
      title: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}title'],
      )!,
      contentMd: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}content_md'],
      )!,
      isTodo: attachedDatabase.typeMapping.read(
        DriftSqlType.bool,
        data['${effectivePrefix}is_todo'],
      )!,
      todoCompletedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}todo_completed_at'],
      ),
      position: attachedDatabase.typeMapping.read(
        DriftSqlType.double,
        data['${effectivePrefix}position'],
      )!,
      version: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}version'],
      )!,
      trashed: attachedDatabase.typeMapping.read(
        DriftSqlType.bool,
        data['${effectivePrefix}trashed'],
      )!,
      trashedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}trashed_at'],
      ),
      archivedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}archived_at'],
      ),
      promotedPageId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}promoted_page_id'],
      ),
      updatedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}updated_at'],
      )!,
    );
  }

  @override
  $NoteNotesTable createAlias(String alias) {
    return $NoteNotesTable(attachedDatabase, alias);
  }
}

class LocalNote extends DataClass implements Insertable<LocalNote> {
  final String id;
  final String? notebookId;
  final String title;
  final String contentMd;
  final bool isTodo;
  final DateTime? todoCompletedAt;
  final double position;
  final int version;
  final bool trashed;
  final DateTime? trashedAt;

  /// 归档时间（转入知识库后服务端置位）。null = 未归档。归档笔记不进
  /// 默认列表（对齐服务端 GET /v1/notes 默认排除归档）。
  final DateTime? archivedAt;

  /// 转入知识库后对应的 wiki page id，null = 未转入。编辑器用它显示
  /// 「已转入知识库」只读提示条。
  final String? promotedPageId;
  final DateTime updatedAt;
  const LocalNote({
    required this.id,
    this.notebookId,
    required this.title,
    required this.contentMd,
    required this.isTodo,
    this.todoCompletedAt,
    required this.position,
    required this.version,
    required this.trashed,
    this.trashedAt,
    this.archivedAt,
    this.promotedPageId,
    required this.updatedAt,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<String>(id);
    if (!nullToAbsent || notebookId != null) {
      map['notebook_id'] = Variable<String>(notebookId);
    }
    map['title'] = Variable<String>(title);
    map['content_md'] = Variable<String>(contentMd);
    map['is_todo'] = Variable<bool>(isTodo);
    if (!nullToAbsent || todoCompletedAt != null) {
      map['todo_completed_at'] = Variable<DateTime>(todoCompletedAt);
    }
    map['position'] = Variable<double>(position);
    map['version'] = Variable<int>(version);
    map['trashed'] = Variable<bool>(trashed);
    if (!nullToAbsent || trashedAt != null) {
      map['trashed_at'] = Variable<DateTime>(trashedAt);
    }
    if (!nullToAbsent || archivedAt != null) {
      map['archived_at'] = Variable<DateTime>(archivedAt);
    }
    if (!nullToAbsent || promotedPageId != null) {
      map['promoted_page_id'] = Variable<String>(promotedPageId);
    }
    map['updated_at'] = Variable<DateTime>(updatedAt);
    return map;
  }

  NoteNotesCompanion toCompanion(bool nullToAbsent) {
    return NoteNotesCompanion(
      id: Value(id),
      notebookId: notebookId == null && nullToAbsent
          ? const Value.absent()
          : Value(notebookId),
      title: Value(title),
      contentMd: Value(contentMd),
      isTodo: Value(isTodo),
      todoCompletedAt: todoCompletedAt == null && nullToAbsent
          ? const Value.absent()
          : Value(todoCompletedAt),
      position: Value(position),
      version: Value(version),
      trashed: Value(trashed),
      trashedAt: trashedAt == null && nullToAbsent
          ? const Value.absent()
          : Value(trashedAt),
      archivedAt: archivedAt == null && nullToAbsent
          ? const Value.absent()
          : Value(archivedAt),
      promotedPageId: promotedPageId == null && nullToAbsent
          ? const Value.absent()
          : Value(promotedPageId),
      updatedAt: Value(updatedAt),
    );
  }

  factory LocalNote.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalNote(
      id: serializer.fromJson<String>(json['id']),
      notebookId: serializer.fromJson<String?>(json['notebookId']),
      title: serializer.fromJson<String>(json['title']),
      contentMd: serializer.fromJson<String>(json['contentMd']),
      isTodo: serializer.fromJson<bool>(json['isTodo']),
      todoCompletedAt: serializer.fromJson<DateTime?>(json['todoCompletedAt']),
      position: serializer.fromJson<double>(json['position']),
      version: serializer.fromJson<int>(json['version']),
      trashed: serializer.fromJson<bool>(json['trashed']),
      trashedAt: serializer.fromJson<DateTime?>(json['trashedAt']),
      archivedAt: serializer.fromJson<DateTime?>(json['archivedAt']),
      promotedPageId: serializer.fromJson<String?>(json['promotedPageId']),
      updatedAt: serializer.fromJson<DateTime>(json['updatedAt']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<String>(id),
      'notebookId': serializer.toJson<String?>(notebookId),
      'title': serializer.toJson<String>(title),
      'contentMd': serializer.toJson<String>(contentMd),
      'isTodo': serializer.toJson<bool>(isTodo),
      'todoCompletedAt': serializer.toJson<DateTime?>(todoCompletedAt),
      'position': serializer.toJson<double>(position),
      'version': serializer.toJson<int>(version),
      'trashed': serializer.toJson<bool>(trashed),
      'trashedAt': serializer.toJson<DateTime?>(trashedAt),
      'archivedAt': serializer.toJson<DateTime?>(archivedAt),
      'promotedPageId': serializer.toJson<String?>(promotedPageId),
      'updatedAt': serializer.toJson<DateTime>(updatedAt),
    };
  }

  LocalNote copyWith({
    String? id,
    Value<String?> notebookId = const Value.absent(),
    String? title,
    String? contentMd,
    bool? isTodo,
    Value<DateTime?> todoCompletedAt = const Value.absent(),
    double? position,
    int? version,
    bool? trashed,
    Value<DateTime?> trashedAt = const Value.absent(),
    Value<DateTime?> archivedAt = const Value.absent(),
    Value<String?> promotedPageId = const Value.absent(),
    DateTime? updatedAt,
  }) => LocalNote(
    id: id ?? this.id,
    notebookId: notebookId.present ? notebookId.value : this.notebookId,
    title: title ?? this.title,
    contentMd: contentMd ?? this.contentMd,
    isTodo: isTodo ?? this.isTodo,
    todoCompletedAt: todoCompletedAt.present
        ? todoCompletedAt.value
        : this.todoCompletedAt,
    position: position ?? this.position,
    version: version ?? this.version,
    trashed: trashed ?? this.trashed,
    trashedAt: trashedAt.present ? trashedAt.value : this.trashedAt,
    archivedAt: archivedAt.present ? archivedAt.value : this.archivedAt,
    promotedPageId: promotedPageId.present
        ? promotedPageId.value
        : this.promotedPageId,
    updatedAt: updatedAt ?? this.updatedAt,
  );
  LocalNote copyWithCompanion(NoteNotesCompanion data) {
    return LocalNote(
      id: data.id.present ? data.id.value : this.id,
      notebookId: data.notebookId.present
          ? data.notebookId.value
          : this.notebookId,
      title: data.title.present ? data.title.value : this.title,
      contentMd: data.contentMd.present ? data.contentMd.value : this.contentMd,
      isTodo: data.isTodo.present ? data.isTodo.value : this.isTodo,
      todoCompletedAt: data.todoCompletedAt.present
          ? data.todoCompletedAt.value
          : this.todoCompletedAt,
      position: data.position.present ? data.position.value : this.position,
      version: data.version.present ? data.version.value : this.version,
      trashed: data.trashed.present ? data.trashed.value : this.trashed,
      trashedAt: data.trashedAt.present ? data.trashedAt.value : this.trashedAt,
      archivedAt: data.archivedAt.present
          ? data.archivedAt.value
          : this.archivedAt,
      promotedPageId: data.promotedPageId.present
          ? data.promotedPageId.value
          : this.promotedPageId,
      updatedAt: data.updatedAt.present ? data.updatedAt.value : this.updatedAt,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalNote(')
          ..write('id: $id, ')
          ..write('notebookId: $notebookId, ')
          ..write('title: $title, ')
          ..write('contentMd: $contentMd, ')
          ..write('isTodo: $isTodo, ')
          ..write('todoCompletedAt: $todoCompletedAt, ')
          ..write('position: $position, ')
          ..write('version: $version, ')
          ..write('trashed: $trashed, ')
          ..write('trashedAt: $trashedAt, ')
          ..write('archivedAt: $archivedAt, ')
          ..write('promotedPageId: $promotedPageId, ')
          ..write('updatedAt: $updatedAt')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(
    id,
    notebookId,
    title,
    contentMd,
    isTodo,
    todoCompletedAt,
    position,
    version,
    trashed,
    trashedAt,
    archivedAt,
    promotedPageId,
    updatedAt,
  );
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalNote &&
          other.id == this.id &&
          other.notebookId == this.notebookId &&
          other.title == this.title &&
          other.contentMd == this.contentMd &&
          other.isTodo == this.isTodo &&
          other.todoCompletedAt == this.todoCompletedAt &&
          other.position == this.position &&
          other.version == this.version &&
          other.trashed == this.trashed &&
          other.trashedAt == this.trashedAt &&
          other.archivedAt == this.archivedAt &&
          other.promotedPageId == this.promotedPageId &&
          other.updatedAt == this.updatedAt);
}

class NoteNotesCompanion extends UpdateCompanion<LocalNote> {
  final Value<String> id;
  final Value<String?> notebookId;
  final Value<String> title;
  final Value<String> contentMd;
  final Value<bool> isTodo;
  final Value<DateTime?> todoCompletedAt;
  final Value<double> position;
  final Value<int> version;
  final Value<bool> trashed;
  final Value<DateTime?> trashedAt;
  final Value<DateTime?> archivedAt;
  final Value<String?> promotedPageId;
  final Value<DateTime> updatedAt;
  final Value<int> rowid;
  const NoteNotesCompanion({
    this.id = const Value.absent(),
    this.notebookId = const Value.absent(),
    this.title = const Value.absent(),
    this.contentMd = const Value.absent(),
    this.isTodo = const Value.absent(),
    this.todoCompletedAt = const Value.absent(),
    this.position = const Value.absent(),
    this.version = const Value.absent(),
    this.trashed = const Value.absent(),
    this.trashedAt = const Value.absent(),
    this.archivedAt = const Value.absent(),
    this.promotedPageId = const Value.absent(),
    this.updatedAt = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  NoteNotesCompanion.insert({
    required String id,
    this.notebookId = const Value.absent(),
    this.title = const Value.absent(),
    this.contentMd = const Value.absent(),
    this.isTodo = const Value.absent(),
    this.todoCompletedAt = const Value.absent(),
    this.position = const Value.absent(),
    this.version = const Value.absent(),
    this.trashed = const Value.absent(),
    this.trashedAt = const Value.absent(),
    this.archivedAt = const Value.absent(),
    this.promotedPageId = const Value.absent(),
    required DateTime updatedAt,
    this.rowid = const Value.absent(),
  }) : id = Value(id),
       updatedAt = Value(updatedAt);
  static Insertable<LocalNote> custom({
    Expression<String>? id,
    Expression<String>? notebookId,
    Expression<String>? title,
    Expression<String>? contentMd,
    Expression<bool>? isTodo,
    Expression<DateTime>? todoCompletedAt,
    Expression<double>? position,
    Expression<int>? version,
    Expression<bool>? trashed,
    Expression<DateTime>? trashedAt,
    Expression<DateTime>? archivedAt,
    Expression<String>? promotedPageId,
    Expression<DateTime>? updatedAt,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (notebookId != null) 'notebook_id': notebookId,
      if (title != null) 'title': title,
      if (contentMd != null) 'content_md': contentMd,
      if (isTodo != null) 'is_todo': isTodo,
      if (todoCompletedAt != null) 'todo_completed_at': todoCompletedAt,
      if (position != null) 'position': position,
      if (version != null) 'version': version,
      if (trashed != null) 'trashed': trashed,
      if (trashedAt != null) 'trashed_at': trashedAt,
      if (archivedAt != null) 'archived_at': archivedAt,
      if (promotedPageId != null) 'promoted_page_id': promotedPageId,
      if (updatedAt != null) 'updated_at': updatedAt,
      if (rowid != null) 'rowid': rowid,
    });
  }

  NoteNotesCompanion copyWith({
    Value<String>? id,
    Value<String?>? notebookId,
    Value<String>? title,
    Value<String>? contentMd,
    Value<bool>? isTodo,
    Value<DateTime?>? todoCompletedAt,
    Value<double>? position,
    Value<int>? version,
    Value<bool>? trashed,
    Value<DateTime?>? trashedAt,
    Value<DateTime?>? archivedAt,
    Value<String?>? promotedPageId,
    Value<DateTime>? updatedAt,
    Value<int>? rowid,
  }) {
    return NoteNotesCompanion(
      id: id ?? this.id,
      notebookId: notebookId ?? this.notebookId,
      title: title ?? this.title,
      contentMd: contentMd ?? this.contentMd,
      isTodo: isTodo ?? this.isTodo,
      todoCompletedAt: todoCompletedAt ?? this.todoCompletedAt,
      position: position ?? this.position,
      version: version ?? this.version,
      trashed: trashed ?? this.trashed,
      trashedAt: trashedAt ?? this.trashedAt,
      archivedAt: archivedAt ?? this.archivedAt,
      promotedPageId: promotedPageId ?? this.promotedPageId,
      updatedAt: updatedAt ?? this.updatedAt,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<String>(id.value);
    }
    if (notebookId.present) {
      map['notebook_id'] = Variable<String>(notebookId.value);
    }
    if (title.present) {
      map['title'] = Variable<String>(title.value);
    }
    if (contentMd.present) {
      map['content_md'] = Variable<String>(contentMd.value);
    }
    if (isTodo.present) {
      map['is_todo'] = Variable<bool>(isTodo.value);
    }
    if (todoCompletedAt.present) {
      map['todo_completed_at'] = Variable<DateTime>(todoCompletedAt.value);
    }
    if (position.present) {
      map['position'] = Variable<double>(position.value);
    }
    if (version.present) {
      map['version'] = Variable<int>(version.value);
    }
    if (trashed.present) {
      map['trashed'] = Variable<bool>(trashed.value);
    }
    if (trashedAt.present) {
      map['trashed_at'] = Variable<DateTime>(trashedAt.value);
    }
    if (archivedAt.present) {
      map['archived_at'] = Variable<DateTime>(archivedAt.value);
    }
    if (promotedPageId.present) {
      map['promoted_page_id'] = Variable<String>(promotedPageId.value);
    }
    if (updatedAt.present) {
      map['updated_at'] = Variable<DateTime>(updatedAt.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('NoteNotesCompanion(')
          ..write('id: $id, ')
          ..write('notebookId: $notebookId, ')
          ..write('title: $title, ')
          ..write('contentMd: $contentMd, ')
          ..write('isTodo: $isTodo, ')
          ..write('todoCompletedAt: $todoCompletedAt, ')
          ..write('position: $position, ')
          ..write('version: $version, ')
          ..write('trashed: $trashed, ')
          ..write('trashedAt: $trashedAt, ')
          ..write('archivedAt: $archivedAt, ')
          ..write('promotedPageId: $promotedPageId, ')
          ..write('updatedAt: $updatedAt, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $NoteTagsTable extends NoteTags
    with TableInfo<$NoteTagsTable, LocalNoteTag> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $NoteTagsTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<String> id = GeneratedColumn<String>(
    'id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _nameMeta = const VerificationMeta('name');
  @override
  late final GeneratedColumn<String> name = GeneratedColumn<String>(
    'name',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  @override
  List<GeneratedColumn> get $columns => [id, name];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'note_tags';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalNoteTag> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    } else if (isInserting) {
      context.missing(_idMeta);
    }
    if (data.containsKey('name')) {
      context.handle(
        _nameMeta,
        name.isAcceptableOrUnknown(data['name']!, _nameMeta),
      );
    } else if (isInserting) {
      context.missing(_nameMeta);
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalNoteTag map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalNoteTag(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}id'],
      )!,
      name: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}name'],
      )!,
    );
  }

  @override
  $NoteTagsTable createAlias(String alias) {
    return $NoteTagsTable(attachedDatabase, alias);
  }
}

class LocalNoteTag extends DataClass implements Insertable<LocalNoteTag> {
  final String id;
  final String name;
  const LocalNoteTag({required this.id, required this.name});
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<String>(id);
    map['name'] = Variable<String>(name);
    return map;
  }

  NoteTagsCompanion toCompanion(bool nullToAbsent) {
    return NoteTagsCompanion(id: Value(id), name: Value(name));
  }

  factory LocalNoteTag.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalNoteTag(
      id: serializer.fromJson<String>(json['id']),
      name: serializer.fromJson<String>(json['name']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<String>(id),
      'name': serializer.toJson<String>(name),
    };
  }

  LocalNoteTag copyWith({String? id, String? name}) =>
      LocalNoteTag(id: id ?? this.id, name: name ?? this.name);
  LocalNoteTag copyWithCompanion(NoteTagsCompanion data) {
    return LocalNoteTag(
      id: data.id.present ? data.id.value : this.id,
      name: data.name.present ? data.name.value : this.name,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalNoteTag(')
          ..write('id: $id, ')
          ..write('name: $name')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(id, name);
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalNoteTag && other.id == this.id && other.name == this.name);
}

class NoteTagsCompanion extends UpdateCompanion<LocalNoteTag> {
  final Value<String> id;
  final Value<String> name;
  final Value<int> rowid;
  const NoteTagsCompanion({
    this.id = const Value.absent(),
    this.name = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  NoteTagsCompanion.insert({
    required String id,
    required String name,
    this.rowid = const Value.absent(),
  }) : id = Value(id),
       name = Value(name);
  static Insertable<LocalNoteTag> custom({
    Expression<String>? id,
    Expression<String>? name,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (name != null) 'name': name,
      if (rowid != null) 'rowid': rowid,
    });
  }

  NoteTagsCompanion copyWith({
    Value<String>? id,
    Value<String>? name,
    Value<int>? rowid,
  }) {
    return NoteTagsCompanion(
      id: id ?? this.id,
      name: name ?? this.name,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<String>(id.value);
    }
    if (name.present) {
      map['name'] = Variable<String>(name.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('NoteTagsCompanion(')
          ..write('id: $id, ')
          ..write('name: $name, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $NoteNoteTagsTable extends NoteNoteTags
    with TableInfo<$NoteNoteTagsTable, NoteNoteTag> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $NoteNoteTagsTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _noteIdMeta = const VerificationMeta('noteId');
  @override
  late final GeneratedColumn<String> noteId = GeneratedColumn<String>(
    'note_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _tagIdMeta = const VerificationMeta('tagId');
  @override
  late final GeneratedColumn<String> tagId = GeneratedColumn<String>(
    'tag_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  @override
  List<GeneratedColumn> get $columns => [noteId, tagId];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'note_note_tags';
  @override
  VerificationContext validateIntegrity(
    Insertable<NoteNoteTag> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('note_id')) {
      context.handle(
        _noteIdMeta,
        noteId.isAcceptableOrUnknown(data['note_id']!, _noteIdMeta),
      );
    } else if (isInserting) {
      context.missing(_noteIdMeta);
    }
    if (data.containsKey('tag_id')) {
      context.handle(
        _tagIdMeta,
        tagId.isAcceptableOrUnknown(data['tag_id']!, _tagIdMeta),
      );
    } else if (isInserting) {
      context.missing(_tagIdMeta);
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {noteId, tagId};
  @override
  NoteNoteTag map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return NoteNoteTag(
      noteId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}note_id'],
      )!,
      tagId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}tag_id'],
      )!,
    );
  }

  @override
  $NoteNoteTagsTable createAlias(String alias) {
    return $NoteNoteTagsTable(attachedDatabase, alias);
  }
}

class NoteNoteTag extends DataClass implements Insertable<NoteNoteTag> {
  final String noteId;
  final String tagId;
  const NoteNoteTag({required this.noteId, required this.tagId});
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['note_id'] = Variable<String>(noteId);
    map['tag_id'] = Variable<String>(tagId);
    return map;
  }

  NoteNoteTagsCompanion toCompanion(bool nullToAbsent) {
    return NoteNoteTagsCompanion(noteId: Value(noteId), tagId: Value(tagId));
  }

  factory NoteNoteTag.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return NoteNoteTag(
      noteId: serializer.fromJson<String>(json['noteId']),
      tagId: serializer.fromJson<String>(json['tagId']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'noteId': serializer.toJson<String>(noteId),
      'tagId': serializer.toJson<String>(tagId),
    };
  }

  NoteNoteTag copyWith({String? noteId, String? tagId}) =>
      NoteNoteTag(noteId: noteId ?? this.noteId, tagId: tagId ?? this.tagId);
  NoteNoteTag copyWithCompanion(NoteNoteTagsCompanion data) {
    return NoteNoteTag(
      noteId: data.noteId.present ? data.noteId.value : this.noteId,
      tagId: data.tagId.present ? data.tagId.value : this.tagId,
    );
  }

  @override
  String toString() {
    return (StringBuffer('NoteNoteTag(')
          ..write('noteId: $noteId, ')
          ..write('tagId: $tagId')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(noteId, tagId);
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is NoteNoteTag &&
          other.noteId == this.noteId &&
          other.tagId == this.tagId);
}

class NoteNoteTagsCompanion extends UpdateCompanion<NoteNoteTag> {
  final Value<String> noteId;
  final Value<String> tagId;
  final Value<int> rowid;
  const NoteNoteTagsCompanion({
    this.noteId = const Value.absent(),
    this.tagId = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  NoteNoteTagsCompanion.insert({
    required String noteId,
    required String tagId,
    this.rowid = const Value.absent(),
  }) : noteId = Value(noteId),
       tagId = Value(tagId);
  static Insertable<NoteNoteTag> custom({
    Expression<String>? noteId,
    Expression<String>? tagId,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (noteId != null) 'note_id': noteId,
      if (tagId != null) 'tag_id': tagId,
      if (rowid != null) 'rowid': rowid,
    });
  }

  NoteNoteTagsCompanion copyWith({
    Value<String>? noteId,
    Value<String>? tagId,
    Value<int>? rowid,
  }) {
    return NoteNoteTagsCompanion(
      noteId: noteId ?? this.noteId,
      tagId: tagId ?? this.tagId,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (noteId.present) {
      map['note_id'] = Variable<String>(noteId.value);
    }
    if (tagId.present) {
      map['tag_id'] = Variable<String>(tagId.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('NoteNoteTagsCompanion(')
          ..write('noteId: $noteId, ')
          ..write('tagId: $tagId, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $NoteOutboxTable extends NoteOutbox
    with TableInfo<$NoteOutboxTable, NoteOutboxEntry> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $NoteOutboxTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<int> id = GeneratedColumn<int>(
    'id',
    aliasedName,
    false,
    hasAutoIncrement: true,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultConstraints: GeneratedColumn.constraintIsAlways(
      'PRIMARY KEY AUTOINCREMENT',
    ),
  );
  static const VerificationMeta _opMeta = const VerificationMeta('op');
  @override
  late final GeneratedColumn<String> op = GeneratedColumn<String>(
    'op',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _entityIdMeta = const VerificationMeta(
    'entityId',
  );
  @override
  late final GeneratedColumn<String> entityId = GeneratedColumn<String>(
    'entity_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _notebookIdMeta = const VerificationMeta(
    'notebookId',
  );
  @override
  late final GeneratedColumn<String> notebookId = GeneratedColumn<String>(
    'notebook_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _payloadJsonMeta = const VerificationMeta(
    'payloadJson',
  );
  @override
  late final GeneratedColumn<String> payloadJson = GeneratedColumn<String>(
    'payload_json',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _baseVersionMeta = const VerificationMeta(
    'baseVersion',
  );
  @override
  late final GeneratedColumn<int> baseVersion = GeneratedColumn<int>(
    'base_version',
    aliasedName,
    true,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _attemptsMeta = const VerificationMeta(
    'attempts',
  );
  @override
  late final GeneratedColumn<int> attempts = GeneratedColumn<int>(
    'attempts',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultValue: const Constant(0),
  );
  static const VerificationMeta _lastErrorMeta = const VerificationMeta(
    'lastError',
  );
  @override
  late final GeneratedColumn<String> lastError = GeneratedColumn<String>(
    'last_error',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _createdAtMeta = const VerificationMeta(
    'createdAt',
  );
  @override
  late final GeneratedColumn<DateTime> createdAt = GeneratedColumn<DateTime>(
    'created_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _nextAttemptAtMeta = const VerificationMeta(
    'nextAttemptAt',
  );
  @override
  late final GeneratedColumn<DateTime> nextAttemptAt =
      GeneratedColumn<DateTime>(
        'next_attempt_at',
        aliasedName,
        false,
        type: DriftSqlType.dateTime,
        requiredDuringInsert: true,
      );
  @override
  List<GeneratedColumn> get $columns => [
    id,
    op,
    entityId,
    notebookId,
    payloadJson,
    baseVersion,
    attempts,
    lastError,
    createdAt,
    nextAttemptAt,
  ];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'note_outbox';
  @override
  VerificationContext validateIntegrity(
    Insertable<NoteOutboxEntry> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    }
    if (data.containsKey('op')) {
      context.handle(_opMeta, op.isAcceptableOrUnknown(data['op']!, _opMeta));
    } else if (isInserting) {
      context.missing(_opMeta);
    }
    if (data.containsKey('entity_id')) {
      context.handle(
        _entityIdMeta,
        entityId.isAcceptableOrUnknown(data['entity_id']!, _entityIdMeta),
      );
    } else if (isInserting) {
      context.missing(_entityIdMeta);
    }
    if (data.containsKey('notebook_id')) {
      context.handle(
        _notebookIdMeta,
        notebookId.isAcceptableOrUnknown(data['notebook_id']!, _notebookIdMeta),
      );
    }
    if (data.containsKey('payload_json')) {
      context.handle(
        _payloadJsonMeta,
        payloadJson.isAcceptableOrUnknown(
          data['payload_json']!,
          _payloadJsonMeta,
        ),
      );
    } else if (isInserting) {
      context.missing(_payloadJsonMeta);
    }
    if (data.containsKey('base_version')) {
      context.handle(
        _baseVersionMeta,
        baseVersion.isAcceptableOrUnknown(
          data['base_version']!,
          _baseVersionMeta,
        ),
      );
    }
    if (data.containsKey('attempts')) {
      context.handle(
        _attemptsMeta,
        attempts.isAcceptableOrUnknown(data['attempts']!, _attemptsMeta),
      );
    }
    if (data.containsKey('last_error')) {
      context.handle(
        _lastErrorMeta,
        lastError.isAcceptableOrUnknown(data['last_error']!, _lastErrorMeta),
      );
    }
    if (data.containsKey('created_at')) {
      context.handle(
        _createdAtMeta,
        createdAt.isAcceptableOrUnknown(data['created_at']!, _createdAtMeta),
      );
    } else if (isInserting) {
      context.missing(_createdAtMeta);
    }
    if (data.containsKey('next_attempt_at')) {
      context.handle(
        _nextAttemptAtMeta,
        nextAttemptAt.isAcceptableOrUnknown(
          data['next_attempt_at']!,
          _nextAttemptAtMeta,
        ),
      );
    } else if (isInserting) {
      context.missing(_nextAttemptAtMeta);
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  NoteOutboxEntry map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return NoteOutboxEntry(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}id'],
      )!,
      op: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}op'],
      )!,
      entityId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}entity_id'],
      )!,
      notebookId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}notebook_id'],
      ),
      payloadJson: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}payload_json'],
      )!,
      baseVersion: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}base_version'],
      ),
      attempts: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}attempts'],
      )!,
      lastError: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}last_error'],
      ),
      createdAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}created_at'],
      )!,
      nextAttemptAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}next_attempt_at'],
      )!,
    );
  }

  @override
  $NoteOutboxTable createAlias(String alias) {
    return $NoteOutboxTable(attachedDatabase, alias);
  }
}

class NoteOutboxEntry extends DataClass implements Insertable<NoteOutboxEntry> {
  final int id;
  final String op;
  final String entityId;
  final String? notebookId;
  final String payloadJson;
  final int? baseVersion;
  final int attempts;
  final String? lastError;
  final DateTime createdAt;
  final DateTime nextAttemptAt;
  const NoteOutboxEntry({
    required this.id,
    required this.op,
    required this.entityId,
    this.notebookId,
    required this.payloadJson,
    this.baseVersion,
    required this.attempts,
    this.lastError,
    required this.createdAt,
    required this.nextAttemptAt,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<int>(id);
    map['op'] = Variable<String>(op);
    map['entity_id'] = Variable<String>(entityId);
    if (!nullToAbsent || notebookId != null) {
      map['notebook_id'] = Variable<String>(notebookId);
    }
    map['payload_json'] = Variable<String>(payloadJson);
    if (!nullToAbsent || baseVersion != null) {
      map['base_version'] = Variable<int>(baseVersion);
    }
    map['attempts'] = Variable<int>(attempts);
    if (!nullToAbsent || lastError != null) {
      map['last_error'] = Variable<String>(lastError);
    }
    map['created_at'] = Variable<DateTime>(createdAt);
    map['next_attempt_at'] = Variable<DateTime>(nextAttemptAt);
    return map;
  }

  NoteOutboxCompanion toCompanion(bool nullToAbsent) {
    return NoteOutboxCompanion(
      id: Value(id),
      op: Value(op),
      entityId: Value(entityId),
      notebookId: notebookId == null && nullToAbsent
          ? const Value.absent()
          : Value(notebookId),
      payloadJson: Value(payloadJson),
      baseVersion: baseVersion == null && nullToAbsent
          ? const Value.absent()
          : Value(baseVersion),
      attempts: Value(attempts),
      lastError: lastError == null && nullToAbsent
          ? const Value.absent()
          : Value(lastError),
      createdAt: Value(createdAt),
      nextAttemptAt: Value(nextAttemptAt),
    );
  }

  factory NoteOutboxEntry.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return NoteOutboxEntry(
      id: serializer.fromJson<int>(json['id']),
      op: serializer.fromJson<String>(json['op']),
      entityId: serializer.fromJson<String>(json['entityId']),
      notebookId: serializer.fromJson<String?>(json['notebookId']),
      payloadJson: serializer.fromJson<String>(json['payloadJson']),
      baseVersion: serializer.fromJson<int?>(json['baseVersion']),
      attempts: serializer.fromJson<int>(json['attempts']),
      lastError: serializer.fromJson<String?>(json['lastError']),
      createdAt: serializer.fromJson<DateTime>(json['createdAt']),
      nextAttemptAt: serializer.fromJson<DateTime>(json['nextAttemptAt']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<int>(id),
      'op': serializer.toJson<String>(op),
      'entityId': serializer.toJson<String>(entityId),
      'notebookId': serializer.toJson<String?>(notebookId),
      'payloadJson': serializer.toJson<String>(payloadJson),
      'baseVersion': serializer.toJson<int?>(baseVersion),
      'attempts': serializer.toJson<int>(attempts),
      'lastError': serializer.toJson<String?>(lastError),
      'createdAt': serializer.toJson<DateTime>(createdAt),
      'nextAttemptAt': serializer.toJson<DateTime>(nextAttemptAt),
    };
  }

  NoteOutboxEntry copyWith({
    int? id,
    String? op,
    String? entityId,
    Value<String?> notebookId = const Value.absent(),
    String? payloadJson,
    Value<int?> baseVersion = const Value.absent(),
    int? attempts,
    Value<String?> lastError = const Value.absent(),
    DateTime? createdAt,
    DateTime? nextAttemptAt,
  }) => NoteOutboxEntry(
    id: id ?? this.id,
    op: op ?? this.op,
    entityId: entityId ?? this.entityId,
    notebookId: notebookId.present ? notebookId.value : this.notebookId,
    payloadJson: payloadJson ?? this.payloadJson,
    baseVersion: baseVersion.present ? baseVersion.value : this.baseVersion,
    attempts: attempts ?? this.attempts,
    lastError: lastError.present ? lastError.value : this.lastError,
    createdAt: createdAt ?? this.createdAt,
    nextAttemptAt: nextAttemptAt ?? this.nextAttemptAt,
  );
  NoteOutboxEntry copyWithCompanion(NoteOutboxCompanion data) {
    return NoteOutboxEntry(
      id: data.id.present ? data.id.value : this.id,
      op: data.op.present ? data.op.value : this.op,
      entityId: data.entityId.present ? data.entityId.value : this.entityId,
      notebookId: data.notebookId.present
          ? data.notebookId.value
          : this.notebookId,
      payloadJson: data.payloadJson.present
          ? data.payloadJson.value
          : this.payloadJson,
      baseVersion: data.baseVersion.present
          ? data.baseVersion.value
          : this.baseVersion,
      attempts: data.attempts.present ? data.attempts.value : this.attempts,
      lastError: data.lastError.present ? data.lastError.value : this.lastError,
      createdAt: data.createdAt.present ? data.createdAt.value : this.createdAt,
      nextAttemptAt: data.nextAttemptAt.present
          ? data.nextAttemptAt.value
          : this.nextAttemptAt,
    );
  }

  @override
  String toString() {
    return (StringBuffer('NoteOutboxEntry(')
          ..write('id: $id, ')
          ..write('op: $op, ')
          ..write('entityId: $entityId, ')
          ..write('notebookId: $notebookId, ')
          ..write('payloadJson: $payloadJson, ')
          ..write('baseVersion: $baseVersion, ')
          ..write('attempts: $attempts, ')
          ..write('lastError: $lastError, ')
          ..write('createdAt: $createdAt, ')
          ..write('nextAttemptAt: $nextAttemptAt')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(
    id,
    op,
    entityId,
    notebookId,
    payloadJson,
    baseVersion,
    attempts,
    lastError,
    createdAt,
    nextAttemptAt,
  );
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is NoteOutboxEntry &&
          other.id == this.id &&
          other.op == this.op &&
          other.entityId == this.entityId &&
          other.notebookId == this.notebookId &&
          other.payloadJson == this.payloadJson &&
          other.baseVersion == this.baseVersion &&
          other.attempts == this.attempts &&
          other.lastError == this.lastError &&
          other.createdAt == this.createdAt &&
          other.nextAttemptAt == this.nextAttemptAt);
}

class NoteOutboxCompanion extends UpdateCompanion<NoteOutboxEntry> {
  final Value<int> id;
  final Value<String> op;
  final Value<String> entityId;
  final Value<String?> notebookId;
  final Value<String> payloadJson;
  final Value<int?> baseVersion;
  final Value<int> attempts;
  final Value<String?> lastError;
  final Value<DateTime> createdAt;
  final Value<DateTime> nextAttemptAt;
  const NoteOutboxCompanion({
    this.id = const Value.absent(),
    this.op = const Value.absent(),
    this.entityId = const Value.absent(),
    this.notebookId = const Value.absent(),
    this.payloadJson = const Value.absent(),
    this.baseVersion = const Value.absent(),
    this.attempts = const Value.absent(),
    this.lastError = const Value.absent(),
    this.createdAt = const Value.absent(),
    this.nextAttemptAt = const Value.absent(),
  });
  NoteOutboxCompanion.insert({
    this.id = const Value.absent(),
    required String op,
    required String entityId,
    this.notebookId = const Value.absent(),
    required String payloadJson,
    this.baseVersion = const Value.absent(),
    this.attempts = const Value.absent(),
    this.lastError = const Value.absent(),
    required DateTime createdAt,
    required DateTime nextAttemptAt,
  }) : op = Value(op),
       entityId = Value(entityId),
       payloadJson = Value(payloadJson),
       createdAt = Value(createdAt),
       nextAttemptAt = Value(nextAttemptAt);
  static Insertable<NoteOutboxEntry> custom({
    Expression<int>? id,
    Expression<String>? op,
    Expression<String>? entityId,
    Expression<String>? notebookId,
    Expression<String>? payloadJson,
    Expression<int>? baseVersion,
    Expression<int>? attempts,
    Expression<String>? lastError,
    Expression<DateTime>? createdAt,
    Expression<DateTime>? nextAttemptAt,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (op != null) 'op': op,
      if (entityId != null) 'entity_id': entityId,
      if (notebookId != null) 'notebook_id': notebookId,
      if (payloadJson != null) 'payload_json': payloadJson,
      if (baseVersion != null) 'base_version': baseVersion,
      if (attempts != null) 'attempts': attempts,
      if (lastError != null) 'last_error': lastError,
      if (createdAt != null) 'created_at': createdAt,
      if (nextAttemptAt != null) 'next_attempt_at': nextAttemptAt,
    });
  }

  NoteOutboxCompanion copyWith({
    Value<int>? id,
    Value<String>? op,
    Value<String>? entityId,
    Value<String?>? notebookId,
    Value<String>? payloadJson,
    Value<int?>? baseVersion,
    Value<int>? attempts,
    Value<String?>? lastError,
    Value<DateTime>? createdAt,
    Value<DateTime>? nextAttemptAt,
  }) {
    return NoteOutboxCompanion(
      id: id ?? this.id,
      op: op ?? this.op,
      entityId: entityId ?? this.entityId,
      notebookId: notebookId ?? this.notebookId,
      payloadJson: payloadJson ?? this.payloadJson,
      baseVersion: baseVersion ?? this.baseVersion,
      attempts: attempts ?? this.attempts,
      lastError: lastError ?? this.lastError,
      createdAt: createdAt ?? this.createdAt,
      nextAttemptAt: nextAttemptAt ?? this.nextAttemptAt,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<int>(id.value);
    }
    if (op.present) {
      map['op'] = Variable<String>(op.value);
    }
    if (entityId.present) {
      map['entity_id'] = Variable<String>(entityId.value);
    }
    if (notebookId.present) {
      map['notebook_id'] = Variable<String>(notebookId.value);
    }
    if (payloadJson.present) {
      map['payload_json'] = Variable<String>(payloadJson.value);
    }
    if (baseVersion.present) {
      map['base_version'] = Variable<int>(baseVersion.value);
    }
    if (attempts.present) {
      map['attempts'] = Variable<int>(attempts.value);
    }
    if (lastError.present) {
      map['last_error'] = Variable<String>(lastError.value);
    }
    if (createdAt.present) {
      map['created_at'] = Variable<DateTime>(createdAt.value);
    }
    if (nextAttemptAt.present) {
      map['next_attempt_at'] = Variable<DateTime>(nextAttemptAt.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('NoteOutboxCompanion(')
          ..write('id: $id, ')
          ..write('op: $op, ')
          ..write('entityId: $entityId, ')
          ..write('notebookId: $notebookId, ')
          ..write('payloadJson: $payloadJson, ')
          ..write('baseVersion: $baseVersion, ')
          ..write('attempts: $attempts, ')
          ..write('lastError: $lastError, ')
          ..write('createdAt: $createdAt, ')
          ..write('nextAttemptAt: $nextAttemptAt')
          ..write(')'))
        .toString();
  }
}

class $CodeTasksTable extends CodeTasks
    with TableInfo<$CodeTasksTable, LocalCodeTask> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $CodeTasksTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<String> id = GeneratedColumn<String>(
    'id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _titleMeta = const VerificationMeta('title');
  @override
  late final GeneratedColumn<String> title = GeneratedColumn<String>(
    'title',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _promptMeta = const VerificationMeta('prompt');
  @override
  late final GeneratedColumn<String> prompt = GeneratedColumn<String>(
    'prompt',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _agentMeta = const VerificationMeta('agent');
  @override
  late final GeneratedColumn<String> agent = GeneratedColumn<String>(
    'agent',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _modeMeta = const VerificationMeta('mode');
  @override
  late final GeneratedColumn<String> mode = GeneratedColumn<String>(
    'mode',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _statusMeta = const VerificationMeta('status');
  @override
  late final GeneratedColumn<String> status = GeneratedColumn<String>(
    'status',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _eventsJsonMeta = const VerificationMeta(
    'eventsJson',
  );
  @override
  late final GeneratedColumn<String> eventsJson = GeneratedColumn<String>(
    'events_json',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant('[]'),
  );
  static const VerificationMeta _costUsdMeta = const VerificationMeta(
    'costUsd',
  );
  @override
  late final GeneratedColumn<double> costUsd = GeneratedColumn<double>(
    'cost_usd',
    aliasedName,
    false,
    type: DriftSqlType.double,
    requiredDuringInsert: false,
    defaultValue: const Constant(0.0),
  );
  static const VerificationMeta _inputTokensMeta = const VerificationMeta(
    'inputTokens',
  );
  @override
  late final GeneratedColumn<int> inputTokens = GeneratedColumn<int>(
    'input_tokens',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultValue: const Constant(0),
  );
  static const VerificationMeta _outputTokensMeta = const VerificationMeta(
    'outputTokens',
  );
  @override
  late final GeneratedColumn<int> outputTokens = GeneratedColumn<int>(
    'output_tokens',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultValue: const Constant(0),
  );
  static const VerificationMeta _createdAtMeta = const VerificationMeta(
    'createdAt',
  );
  @override
  late final GeneratedColumn<DateTime> createdAt = GeneratedColumn<DateTime>(
    'created_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _completedAtMeta = const VerificationMeta(
    'completedAt',
  );
  @override
  late final GeneratedColumn<DateTime> completedAt = GeneratedColumn<DateTime>(
    'completed_at',
    aliasedName,
    true,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _errorMessageMeta = const VerificationMeta(
    'errorMessage',
  );
  @override
  late final GeneratedColumn<String> errorMessage = GeneratedColumn<String>(
    'error_message',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _workspaceJsonMeta = const VerificationMeta(
    'workspaceJson',
  );
  @override
  late final GeneratedColumn<String> workspaceJson = GeneratedColumn<String>(
    'workspace_json',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _compareGroupIdMeta = const VerificationMeta(
    'compareGroupId',
  );
  @override
  late final GeneratedColumn<String> compareGroupId = GeneratedColumn<String>(
    'compare_group_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _originDeviceIdMeta = const VerificationMeta(
    'originDeviceId',
  );
  @override
  late final GeneratedColumn<String> originDeviceId = GeneratedColumn<String>(
    'origin_device_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _originDeviceLabelMeta = const VerificationMeta(
    'originDeviceLabel',
  );
  @override
  late final GeneratedColumn<String> originDeviceLabel =
      GeneratedColumn<String>(
        'origin_device_label',
        aliasedName,
        true,
        type: DriftSqlType.string,
        requiredDuringInsert: false,
      );
  static const VerificationMeta _projectIdMeta = const VerificationMeta(
    'projectId',
  );
  @override
  late final GeneratedColumn<String> projectId = GeneratedColumn<String>(
    'project_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _updatedAtMeta = const VerificationMeta(
    'updatedAt',
  );
  @override
  late final GeneratedColumn<DateTime> updatedAt = GeneratedColumn<DateTime>(
    'updated_at',
    aliasedName,
    true,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _modelMeta = const VerificationMeta('model');
  @override
  late final GeneratedColumn<String> model = GeneratedColumn<String>(
    'model',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _starredMeta = const VerificationMeta(
    'starred',
  );
  @override
  late final GeneratedColumn<bool> starred = GeneratedColumn<bool>(
    'starred',
    aliasedName,
    false,
    type: DriftSqlType.bool,
    requiredDuringInsert: false,
    defaultConstraints: GeneratedColumn.constraintIsAlways(
      'CHECK ("starred" IN (0, 1))',
    ),
    defaultValue: const Constant(false),
  );
  @override
  List<GeneratedColumn> get $columns => [
    id,
    title,
    prompt,
    agent,
    mode,
    status,
    eventsJson,
    costUsd,
    inputTokens,
    outputTokens,
    createdAt,
    completedAt,
    errorMessage,
    workspaceJson,
    compareGroupId,
    originDeviceId,
    originDeviceLabel,
    projectId,
    updatedAt,
    model,
    starred,
  ];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'code_tasks';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalCodeTask> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    } else if (isInserting) {
      context.missing(_idMeta);
    }
    if (data.containsKey('title')) {
      context.handle(
        _titleMeta,
        title.isAcceptableOrUnknown(data['title']!, _titleMeta),
      );
    } else if (isInserting) {
      context.missing(_titleMeta);
    }
    if (data.containsKey('prompt')) {
      context.handle(
        _promptMeta,
        prompt.isAcceptableOrUnknown(data['prompt']!, _promptMeta),
      );
    } else if (isInserting) {
      context.missing(_promptMeta);
    }
    if (data.containsKey('agent')) {
      context.handle(
        _agentMeta,
        agent.isAcceptableOrUnknown(data['agent']!, _agentMeta),
      );
    } else if (isInserting) {
      context.missing(_agentMeta);
    }
    if (data.containsKey('mode')) {
      context.handle(
        _modeMeta,
        mode.isAcceptableOrUnknown(data['mode']!, _modeMeta),
      );
    } else if (isInserting) {
      context.missing(_modeMeta);
    }
    if (data.containsKey('status')) {
      context.handle(
        _statusMeta,
        status.isAcceptableOrUnknown(data['status']!, _statusMeta),
      );
    } else if (isInserting) {
      context.missing(_statusMeta);
    }
    if (data.containsKey('events_json')) {
      context.handle(
        _eventsJsonMeta,
        eventsJson.isAcceptableOrUnknown(data['events_json']!, _eventsJsonMeta),
      );
    }
    if (data.containsKey('cost_usd')) {
      context.handle(
        _costUsdMeta,
        costUsd.isAcceptableOrUnknown(data['cost_usd']!, _costUsdMeta),
      );
    }
    if (data.containsKey('input_tokens')) {
      context.handle(
        _inputTokensMeta,
        inputTokens.isAcceptableOrUnknown(
          data['input_tokens']!,
          _inputTokensMeta,
        ),
      );
    }
    if (data.containsKey('output_tokens')) {
      context.handle(
        _outputTokensMeta,
        outputTokens.isAcceptableOrUnknown(
          data['output_tokens']!,
          _outputTokensMeta,
        ),
      );
    }
    if (data.containsKey('created_at')) {
      context.handle(
        _createdAtMeta,
        createdAt.isAcceptableOrUnknown(data['created_at']!, _createdAtMeta),
      );
    } else if (isInserting) {
      context.missing(_createdAtMeta);
    }
    if (data.containsKey('completed_at')) {
      context.handle(
        _completedAtMeta,
        completedAt.isAcceptableOrUnknown(
          data['completed_at']!,
          _completedAtMeta,
        ),
      );
    }
    if (data.containsKey('error_message')) {
      context.handle(
        _errorMessageMeta,
        errorMessage.isAcceptableOrUnknown(
          data['error_message']!,
          _errorMessageMeta,
        ),
      );
    }
    if (data.containsKey('workspace_json')) {
      context.handle(
        _workspaceJsonMeta,
        workspaceJson.isAcceptableOrUnknown(
          data['workspace_json']!,
          _workspaceJsonMeta,
        ),
      );
    }
    if (data.containsKey('compare_group_id')) {
      context.handle(
        _compareGroupIdMeta,
        compareGroupId.isAcceptableOrUnknown(
          data['compare_group_id']!,
          _compareGroupIdMeta,
        ),
      );
    }
    if (data.containsKey('origin_device_id')) {
      context.handle(
        _originDeviceIdMeta,
        originDeviceId.isAcceptableOrUnknown(
          data['origin_device_id']!,
          _originDeviceIdMeta,
        ),
      );
    }
    if (data.containsKey('origin_device_label')) {
      context.handle(
        _originDeviceLabelMeta,
        originDeviceLabel.isAcceptableOrUnknown(
          data['origin_device_label']!,
          _originDeviceLabelMeta,
        ),
      );
    }
    if (data.containsKey('project_id')) {
      context.handle(
        _projectIdMeta,
        projectId.isAcceptableOrUnknown(data['project_id']!, _projectIdMeta),
      );
    }
    if (data.containsKey('updated_at')) {
      context.handle(
        _updatedAtMeta,
        updatedAt.isAcceptableOrUnknown(data['updated_at']!, _updatedAtMeta),
      );
    }
    if (data.containsKey('model')) {
      context.handle(
        _modelMeta,
        model.isAcceptableOrUnknown(data['model']!, _modelMeta),
      );
    }
    if (data.containsKey('starred')) {
      context.handle(
        _starredMeta,
        starred.isAcceptableOrUnknown(data['starred']!, _starredMeta),
      );
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalCodeTask map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalCodeTask(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}id'],
      )!,
      title: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}title'],
      )!,
      prompt: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}prompt'],
      )!,
      agent: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}agent'],
      )!,
      mode: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}mode'],
      )!,
      status: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}status'],
      )!,
      eventsJson: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}events_json'],
      )!,
      costUsd: attachedDatabase.typeMapping.read(
        DriftSqlType.double,
        data['${effectivePrefix}cost_usd'],
      )!,
      inputTokens: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}input_tokens'],
      )!,
      outputTokens: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}output_tokens'],
      )!,
      createdAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}created_at'],
      )!,
      completedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}completed_at'],
      ),
      errorMessage: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}error_message'],
      ),
      workspaceJson: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}workspace_json'],
      ),
      compareGroupId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}compare_group_id'],
      ),
      originDeviceId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}origin_device_id'],
      ),
      originDeviceLabel: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}origin_device_label'],
      ),
      projectId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}project_id'],
      ),
      updatedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}updated_at'],
      ),
      model: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}model'],
      ),
      starred: attachedDatabase.typeMapping.read(
        DriftSqlType.bool,
        data['${effectivePrefix}starred'],
      )!,
    );
  }

  @override
  $CodeTasksTable createAlias(String alias) {
    return $CodeTasksTable(attachedDatabase, alias);
  }
}

class LocalCodeTask extends DataClass implements Insertable<LocalCodeTask> {
  final String id;
  final String title;
  final String prompt;
  final String agent;
  final String mode;
  final String status;

  /// AgentEvent[] JSON, 流式事件全列表. 每次 event 来 in-memory 拼好后 upsert.
  final String eventsJson;
  final double costUsd;
  final int inputTokens;
  final int outputTokens;
  final DateTime createdAt;
  final DateTime? completedAt;
  final String? errorMessage;

  /// WorkspaceRef.toJson() 序列化。null = 任务还在 allocate / passthrough 模式。
  final String? workspaceJson;

  /// 对比组关联. 同 prompt 派给多 agent 时这些 task 共享同一 id。
  final String? compareGroupId;

  /// 任务跑在哪台机器 (CSY4)。本机创建 = 本机 codeOriginDeviceId;
  /// 远端 pull / Realtime 拉来的任务 = 对方 device id。
  final String? originDeviceId;
  final String? originDeviceLabel;

  /// 所属项目 (M1 多项目)。指向 CodeProjects.id。nullable —— 老任务(单 workspace
  /// 时代)无项目归属;M1 迁移后新任务必带。
  final String? projectId;

  /// 最后更新时间 (M1)。TaskList 按它排序/显示
  /// "n 分钟前"。nullable —— 老行无,读时回退 createdAt。
  final DateTime? updatedAt;

  /// 用户为本任务选的模型 id(M4,schema v23)。null = agent 默认。仅本地持久化
  /// (codeSync 已废弃 D4/Code-I6,不进同步)。
  final String? model;

  /// 星标(CORE-2,schema v26)。仅本地。
  final bool starred;
  const LocalCodeTask({
    required this.id,
    required this.title,
    required this.prompt,
    required this.agent,
    required this.mode,
    required this.status,
    required this.eventsJson,
    required this.costUsd,
    required this.inputTokens,
    required this.outputTokens,
    required this.createdAt,
    this.completedAt,
    this.errorMessage,
    this.workspaceJson,
    this.compareGroupId,
    this.originDeviceId,
    this.originDeviceLabel,
    this.projectId,
    this.updatedAt,
    this.model,
    required this.starred,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<String>(id);
    map['title'] = Variable<String>(title);
    map['prompt'] = Variable<String>(prompt);
    map['agent'] = Variable<String>(agent);
    map['mode'] = Variable<String>(mode);
    map['status'] = Variable<String>(status);
    map['events_json'] = Variable<String>(eventsJson);
    map['cost_usd'] = Variable<double>(costUsd);
    map['input_tokens'] = Variable<int>(inputTokens);
    map['output_tokens'] = Variable<int>(outputTokens);
    map['created_at'] = Variable<DateTime>(createdAt);
    if (!nullToAbsent || completedAt != null) {
      map['completed_at'] = Variable<DateTime>(completedAt);
    }
    if (!nullToAbsent || errorMessage != null) {
      map['error_message'] = Variable<String>(errorMessage);
    }
    if (!nullToAbsent || workspaceJson != null) {
      map['workspace_json'] = Variable<String>(workspaceJson);
    }
    if (!nullToAbsent || compareGroupId != null) {
      map['compare_group_id'] = Variable<String>(compareGroupId);
    }
    if (!nullToAbsent || originDeviceId != null) {
      map['origin_device_id'] = Variable<String>(originDeviceId);
    }
    if (!nullToAbsent || originDeviceLabel != null) {
      map['origin_device_label'] = Variable<String>(originDeviceLabel);
    }
    if (!nullToAbsent || projectId != null) {
      map['project_id'] = Variable<String>(projectId);
    }
    if (!nullToAbsent || updatedAt != null) {
      map['updated_at'] = Variable<DateTime>(updatedAt);
    }
    if (!nullToAbsent || model != null) {
      map['model'] = Variable<String>(model);
    }
    map['starred'] = Variable<bool>(starred);
    return map;
  }

  CodeTasksCompanion toCompanion(bool nullToAbsent) {
    return CodeTasksCompanion(
      id: Value(id),
      title: Value(title),
      prompt: Value(prompt),
      agent: Value(agent),
      mode: Value(mode),
      status: Value(status),
      eventsJson: Value(eventsJson),
      costUsd: Value(costUsd),
      inputTokens: Value(inputTokens),
      outputTokens: Value(outputTokens),
      createdAt: Value(createdAt),
      completedAt: completedAt == null && nullToAbsent
          ? const Value.absent()
          : Value(completedAt),
      errorMessage: errorMessage == null && nullToAbsent
          ? const Value.absent()
          : Value(errorMessage),
      workspaceJson: workspaceJson == null && nullToAbsent
          ? const Value.absent()
          : Value(workspaceJson),
      compareGroupId: compareGroupId == null && nullToAbsent
          ? const Value.absent()
          : Value(compareGroupId),
      originDeviceId: originDeviceId == null && nullToAbsent
          ? const Value.absent()
          : Value(originDeviceId),
      originDeviceLabel: originDeviceLabel == null && nullToAbsent
          ? const Value.absent()
          : Value(originDeviceLabel),
      projectId: projectId == null && nullToAbsent
          ? const Value.absent()
          : Value(projectId),
      updatedAt: updatedAt == null && nullToAbsent
          ? const Value.absent()
          : Value(updatedAt),
      model: model == null && nullToAbsent
          ? const Value.absent()
          : Value(model),
      starred: Value(starred),
    );
  }

  factory LocalCodeTask.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalCodeTask(
      id: serializer.fromJson<String>(json['id']),
      title: serializer.fromJson<String>(json['title']),
      prompt: serializer.fromJson<String>(json['prompt']),
      agent: serializer.fromJson<String>(json['agent']),
      mode: serializer.fromJson<String>(json['mode']),
      status: serializer.fromJson<String>(json['status']),
      eventsJson: serializer.fromJson<String>(json['eventsJson']),
      costUsd: serializer.fromJson<double>(json['costUsd']),
      inputTokens: serializer.fromJson<int>(json['inputTokens']),
      outputTokens: serializer.fromJson<int>(json['outputTokens']),
      createdAt: serializer.fromJson<DateTime>(json['createdAt']),
      completedAt: serializer.fromJson<DateTime?>(json['completedAt']),
      errorMessage: serializer.fromJson<String?>(json['errorMessage']),
      workspaceJson: serializer.fromJson<String?>(json['workspaceJson']),
      compareGroupId: serializer.fromJson<String?>(json['compareGroupId']),
      originDeviceId: serializer.fromJson<String?>(json['originDeviceId']),
      originDeviceLabel: serializer.fromJson<String?>(
        json['originDeviceLabel'],
      ),
      projectId: serializer.fromJson<String?>(json['projectId']),
      updatedAt: serializer.fromJson<DateTime?>(json['updatedAt']),
      model: serializer.fromJson<String?>(json['model']),
      starred: serializer.fromJson<bool>(json['starred']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<String>(id),
      'title': serializer.toJson<String>(title),
      'prompt': serializer.toJson<String>(prompt),
      'agent': serializer.toJson<String>(agent),
      'mode': serializer.toJson<String>(mode),
      'status': serializer.toJson<String>(status),
      'eventsJson': serializer.toJson<String>(eventsJson),
      'costUsd': serializer.toJson<double>(costUsd),
      'inputTokens': serializer.toJson<int>(inputTokens),
      'outputTokens': serializer.toJson<int>(outputTokens),
      'createdAt': serializer.toJson<DateTime>(createdAt),
      'completedAt': serializer.toJson<DateTime?>(completedAt),
      'errorMessage': serializer.toJson<String?>(errorMessage),
      'workspaceJson': serializer.toJson<String?>(workspaceJson),
      'compareGroupId': serializer.toJson<String?>(compareGroupId),
      'originDeviceId': serializer.toJson<String?>(originDeviceId),
      'originDeviceLabel': serializer.toJson<String?>(originDeviceLabel),
      'projectId': serializer.toJson<String?>(projectId),
      'updatedAt': serializer.toJson<DateTime?>(updatedAt),
      'model': serializer.toJson<String?>(model),
      'starred': serializer.toJson<bool>(starred),
    };
  }

  LocalCodeTask copyWith({
    String? id,
    String? title,
    String? prompt,
    String? agent,
    String? mode,
    String? status,
    String? eventsJson,
    double? costUsd,
    int? inputTokens,
    int? outputTokens,
    DateTime? createdAt,
    Value<DateTime?> completedAt = const Value.absent(),
    Value<String?> errorMessage = const Value.absent(),
    Value<String?> workspaceJson = const Value.absent(),
    Value<String?> compareGroupId = const Value.absent(),
    Value<String?> originDeviceId = const Value.absent(),
    Value<String?> originDeviceLabel = const Value.absent(),
    Value<String?> projectId = const Value.absent(),
    Value<DateTime?> updatedAt = const Value.absent(),
    Value<String?> model = const Value.absent(),
    bool? starred,
  }) => LocalCodeTask(
    id: id ?? this.id,
    title: title ?? this.title,
    prompt: prompt ?? this.prompt,
    agent: agent ?? this.agent,
    mode: mode ?? this.mode,
    status: status ?? this.status,
    eventsJson: eventsJson ?? this.eventsJson,
    costUsd: costUsd ?? this.costUsd,
    inputTokens: inputTokens ?? this.inputTokens,
    outputTokens: outputTokens ?? this.outputTokens,
    createdAt: createdAt ?? this.createdAt,
    completedAt: completedAt.present ? completedAt.value : this.completedAt,
    errorMessage: errorMessage.present ? errorMessage.value : this.errorMessage,
    workspaceJson: workspaceJson.present
        ? workspaceJson.value
        : this.workspaceJson,
    compareGroupId: compareGroupId.present
        ? compareGroupId.value
        : this.compareGroupId,
    originDeviceId: originDeviceId.present
        ? originDeviceId.value
        : this.originDeviceId,
    originDeviceLabel: originDeviceLabel.present
        ? originDeviceLabel.value
        : this.originDeviceLabel,
    projectId: projectId.present ? projectId.value : this.projectId,
    updatedAt: updatedAt.present ? updatedAt.value : this.updatedAt,
    model: model.present ? model.value : this.model,
    starred: starred ?? this.starred,
  );
  LocalCodeTask copyWithCompanion(CodeTasksCompanion data) {
    return LocalCodeTask(
      id: data.id.present ? data.id.value : this.id,
      title: data.title.present ? data.title.value : this.title,
      prompt: data.prompt.present ? data.prompt.value : this.prompt,
      agent: data.agent.present ? data.agent.value : this.agent,
      mode: data.mode.present ? data.mode.value : this.mode,
      status: data.status.present ? data.status.value : this.status,
      eventsJson: data.eventsJson.present
          ? data.eventsJson.value
          : this.eventsJson,
      costUsd: data.costUsd.present ? data.costUsd.value : this.costUsd,
      inputTokens: data.inputTokens.present
          ? data.inputTokens.value
          : this.inputTokens,
      outputTokens: data.outputTokens.present
          ? data.outputTokens.value
          : this.outputTokens,
      createdAt: data.createdAt.present ? data.createdAt.value : this.createdAt,
      completedAt: data.completedAt.present
          ? data.completedAt.value
          : this.completedAt,
      errorMessage: data.errorMessage.present
          ? data.errorMessage.value
          : this.errorMessage,
      workspaceJson: data.workspaceJson.present
          ? data.workspaceJson.value
          : this.workspaceJson,
      compareGroupId: data.compareGroupId.present
          ? data.compareGroupId.value
          : this.compareGroupId,
      originDeviceId: data.originDeviceId.present
          ? data.originDeviceId.value
          : this.originDeviceId,
      originDeviceLabel: data.originDeviceLabel.present
          ? data.originDeviceLabel.value
          : this.originDeviceLabel,
      projectId: data.projectId.present ? data.projectId.value : this.projectId,
      updatedAt: data.updatedAt.present ? data.updatedAt.value : this.updatedAt,
      model: data.model.present ? data.model.value : this.model,
      starred: data.starred.present ? data.starred.value : this.starred,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalCodeTask(')
          ..write('id: $id, ')
          ..write('title: $title, ')
          ..write('prompt: $prompt, ')
          ..write('agent: $agent, ')
          ..write('mode: $mode, ')
          ..write('status: $status, ')
          ..write('eventsJson: $eventsJson, ')
          ..write('costUsd: $costUsd, ')
          ..write('inputTokens: $inputTokens, ')
          ..write('outputTokens: $outputTokens, ')
          ..write('createdAt: $createdAt, ')
          ..write('completedAt: $completedAt, ')
          ..write('errorMessage: $errorMessage, ')
          ..write('workspaceJson: $workspaceJson, ')
          ..write('compareGroupId: $compareGroupId, ')
          ..write('originDeviceId: $originDeviceId, ')
          ..write('originDeviceLabel: $originDeviceLabel, ')
          ..write('projectId: $projectId, ')
          ..write('updatedAt: $updatedAt, ')
          ..write('model: $model, ')
          ..write('starred: $starred')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hashAll([
    id,
    title,
    prompt,
    agent,
    mode,
    status,
    eventsJson,
    costUsd,
    inputTokens,
    outputTokens,
    createdAt,
    completedAt,
    errorMessage,
    workspaceJson,
    compareGroupId,
    originDeviceId,
    originDeviceLabel,
    projectId,
    updatedAt,
    model,
    starred,
  ]);
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalCodeTask &&
          other.id == this.id &&
          other.title == this.title &&
          other.prompt == this.prompt &&
          other.agent == this.agent &&
          other.mode == this.mode &&
          other.status == this.status &&
          other.eventsJson == this.eventsJson &&
          other.costUsd == this.costUsd &&
          other.inputTokens == this.inputTokens &&
          other.outputTokens == this.outputTokens &&
          other.createdAt == this.createdAt &&
          other.completedAt == this.completedAt &&
          other.errorMessage == this.errorMessage &&
          other.workspaceJson == this.workspaceJson &&
          other.compareGroupId == this.compareGroupId &&
          other.originDeviceId == this.originDeviceId &&
          other.originDeviceLabel == this.originDeviceLabel &&
          other.projectId == this.projectId &&
          other.updatedAt == this.updatedAt &&
          other.model == this.model &&
          other.starred == this.starred);
}

class CodeTasksCompanion extends UpdateCompanion<LocalCodeTask> {
  final Value<String> id;
  final Value<String> title;
  final Value<String> prompt;
  final Value<String> agent;
  final Value<String> mode;
  final Value<String> status;
  final Value<String> eventsJson;
  final Value<double> costUsd;
  final Value<int> inputTokens;
  final Value<int> outputTokens;
  final Value<DateTime> createdAt;
  final Value<DateTime?> completedAt;
  final Value<String?> errorMessage;
  final Value<String?> workspaceJson;
  final Value<String?> compareGroupId;
  final Value<String?> originDeviceId;
  final Value<String?> originDeviceLabel;
  final Value<String?> projectId;
  final Value<DateTime?> updatedAt;
  final Value<String?> model;
  final Value<bool> starred;
  final Value<int> rowid;
  const CodeTasksCompanion({
    this.id = const Value.absent(),
    this.title = const Value.absent(),
    this.prompt = const Value.absent(),
    this.agent = const Value.absent(),
    this.mode = const Value.absent(),
    this.status = const Value.absent(),
    this.eventsJson = const Value.absent(),
    this.costUsd = const Value.absent(),
    this.inputTokens = const Value.absent(),
    this.outputTokens = const Value.absent(),
    this.createdAt = const Value.absent(),
    this.completedAt = const Value.absent(),
    this.errorMessage = const Value.absent(),
    this.workspaceJson = const Value.absent(),
    this.compareGroupId = const Value.absent(),
    this.originDeviceId = const Value.absent(),
    this.originDeviceLabel = const Value.absent(),
    this.projectId = const Value.absent(),
    this.updatedAt = const Value.absent(),
    this.model = const Value.absent(),
    this.starred = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  CodeTasksCompanion.insert({
    required String id,
    required String title,
    required String prompt,
    required String agent,
    required String mode,
    required String status,
    this.eventsJson = const Value.absent(),
    this.costUsd = const Value.absent(),
    this.inputTokens = const Value.absent(),
    this.outputTokens = const Value.absent(),
    required DateTime createdAt,
    this.completedAt = const Value.absent(),
    this.errorMessage = const Value.absent(),
    this.workspaceJson = const Value.absent(),
    this.compareGroupId = const Value.absent(),
    this.originDeviceId = const Value.absent(),
    this.originDeviceLabel = const Value.absent(),
    this.projectId = const Value.absent(),
    this.updatedAt = const Value.absent(),
    this.model = const Value.absent(),
    this.starred = const Value.absent(),
    this.rowid = const Value.absent(),
  }) : id = Value(id),
       title = Value(title),
       prompt = Value(prompt),
       agent = Value(agent),
       mode = Value(mode),
       status = Value(status),
       createdAt = Value(createdAt);
  static Insertable<LocalCodeTask> custom({
    Expression<String>? id,
    Expression<String>? title,
    Expression<String>? prompt,
    Expression<String>? agent,
    Expression<String>? mode,
    Expression<String>? status,
    Expression<String>? eventsJson,
    Expression<double>? costUsd,
    Expression<int>? inputTokens,
    Expression<int>? outputTokens,
    Expression<DateTime>? createdAt,
    Expression<DateTime>? completedAt,
    Expression<String>? errorMessage,
    Expression<String>? workspaceJson,
    Expression<String>? compareGroupId,
    Expression<String>? originDeviceId,
    Expression<String>? originDeviceLabel,
    Expression<String>? projectId,
    Expression<DateTime>? updatedAt,
    Expression<String>? model,
    Expression<bool>? starred,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (title != null) 'title': title,
      if (prompt != null) 'prompt': prompt,
      if (agent != null) 'agent': agent,
      if (mode != null) 'mode': mode,
      if (status != null) 'status': status,
      if (eventsJson != null) 'events_json': eventsJson,
      if (costUsd != null) 'cost_usd': costUsd,
      if (inputTokens != null) 'input_tokens': inputTokens,
      if (outputTokens != null) 'output_tokens': outputTokens,
      if (createdAt != null) 'created_at': createdAt,
      if (completedAt != null) 'completed_at': completedAt,
      if (errorMessage != null) 'error_message': errorMessage,
      if (workspaceJson != null) 'workspace_json': workspaceJson,
      if (compareGroupId != null) 'compare_group_id': compareGroupId,
      if (originDeviceId != null) 'origin_device_id': originDeviceId,
      if (originDeviceLabel != null) 'origin_device_label': originDeviceLabel,
      if (projectId != null) 'project_id': projectId,
      if (updatedAt != null) 'updated_at': updatedAt,
      if (model != null) 'model': model,
      if (starred != null) 'starred': starred,
      if (rowid != null) 'rowid': rowid,
    });
  }

  CodeTasksCompanion copyWith({
    Value<String>? id,
    Value<String>? title,
    Value<String>? prompt,
    Value<String>? agent,
    Value<String>? mode,
    Value<String>? status,
    Value<String>? eventsJson,
    Value<double>? costUsd,
    Value<int>? inputTokens,
    Value<int>? outputTokens,
    Value<DateTime>? createdAt,
    Value<DateTime?>? completedAt,
    Value<String?>? errorMessage,
    Value<String?>? workspaceJson,
    Value<String?>? compareGroupId,
    Value<String?>? originDeviceId,
    Value<String?>? originDeviceLabel,
    Value<String?>? projectId,
    Value<DateTime?>? updatedAt,
    Value<String?>? model,
    Value<bool>? starred,
    Value<int>? rowid,
  }) {
    return CodeTasksCompanion(
      id: id ?? this.id,
      title: title ?? this.title,
      prompt: prompt ?? this.prompt,
      agent: agent ?? this.agent,
      mode: mode ?? this.mode,
      status: status ?? this.status,
      eventsJson: eventsJson ?? this.eventsJson,
      costUsd: costUsd ?? this.costUsd,
      inputTokens: inputTokens ?? this.inputTokens,
      outputTokens: outputTokens ?? this.outputTokens,
      createdAt: createdAt ?? this.createdAt,
      completedAt: completedAt ?? this.completedAt,
      errorMessage: errorMessage ?? this.errorMessage,
      workspaceJson: workspaceJson ?? this.workspaceJson,
      compareGroupId: compareGroupId ?? this.compareGroupId,
      originDeviceId: originDeviceId ?? this.originDeviceId,
      originDeviceLabel: originDeviceLabel ?? this.originDeviceLabel,
      projectId: projectId ?? this.projectId,
      updatedAt: updatedAt ?? this.updatedAt,
      model: model ?? this.model,
      starred: starred ?? this.starred,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<String>(id.value);
    }
    if (title.present) {
      map['title'] = Variable<String>(title.value);
    }
    if (prompt.present) {
      map['prompt'] = Variable<String>(prompt.value);
    }
    if (agent.present) {
      map['agent'] = Variable<String>(agent.value);
    }
    if (mode.present) {
      map['mode'] = Variable<String>(mode.value);
    }
    if (status.present) {
      map['status'] = Variable<String>(status.value);
    }
    if (eventsJson.present) {
      map['events_json'] = Variable<String>(eventsJson.value);
    }
    if (costUsd.present) {
      map['cost_usd'] = Variable<double>(costUsd.value);
    }
    if (inputTokens.present) {
      map['input_tokens'] = Variable<int>(inputTokens.value);
    }
    if (outputTokens.present) {
      map['output_tokens'] = Variable<int>(outputTokens.value);
    }
    if (createdAt.present) {
      map['created_at'] = Variable<DateTime>(createdAt.value);
    }
    if (completedAt.present) {
      map['completed_at'] = Variable<DateTime>(completedAt.value);
    }
    if (errorMessage.present) {
      map['error_message'] = Variable<String>(errorMessage.value);
    }
    if (workspaceJson.present) {
      map['workspace_json'] = Variable<String>(workspaceJson.value);
    }
    if (compareGroupId.present) {
      map['compare_group_id'] = Variable<String>(compareGroupId.value);
    }
    if (originDeviceId.present) {
      map['origin_device_id'] = Variable<String>(originDeviceId.value);
    }
    if (originDeviceLabel.present) {
      map['origin_device_label'] = Variable<String>(originDeviceLabel.value);
    }
    if (projectId.present) {
      map['project_id'] = Variable<String>(projectId.value);
    }
    if (updatedAt.present) {
      map['updated_at'] = Variable<DateTime>(updatedAt.value);
    }
    if (model.present) {
      map['model'] = Variable<String>(model.value);
    }
    if (starred.present) {
      map['starred'] = Variable<bool>(starred.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('CodeTasksCompanion(')
          ..write('id: $id, ')
          ..write('title: $title, ')
          ..write('prompt: $prompt, ')
          ..write('agent: $agent, ')
          ..write('mode: $mode, ')
          ..write('status: $status, ')
          ..write('eventsJson: $eventsJson, ')
          ..write('costUsd: $costUsd, ')
          ..write('inputTokens: $inputTokens, ')
          ..write('outputTokens: $outputTokens, ')
          ..write('createdAt: $createdAt, ')
          ..write('completedAt: $completedAt, ')
          ..write('errorMessage: $errorMessage, ')
          ..write('workspaceJson: $workspaceJson, ')
          ..write('compareGroupId: $compareGroupId, ')
          ..write('originDeviceId: $originDeviceId, ')
          ..write('originDeviceLabel: $originDeviceLabel, ')
          ..write('projectId: $projectId, ')
          ..write('updatedAt: $updatedAt, ')
          ..write('model: $model, ')
          ..write('starred: $starred, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $CodeProjectsTable extends CodeProjects
    with TableInfo<$CodeProjectsTable, LocalCodeProject> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $CodeProjectsTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<String> id = GeneratedColumn<String>(
    'id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _nameMeta = const VerificationMeta('name');
  @override
  late final GeneratedColumn<String> name = GeneratedColumn<String>(
    'name',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _pathMeta = const VerificationMeta('path');
  @override
  late final GeneratedColumn<String> path = GeneratedColumn<String>(
    'path',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _branchMeta = const VerificationMeta('branch');
  @override
  late final GeneratedColumn<String> branch = GeneratedColumn<String>(
    'branch',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _lastOpenedAtMeta = const VerificationMeta(
    'lastOpenedAt',
  );
  @override
  late final GeneratedColumn<int> lastOpenedAt = GeneratedColumn<int>(
    'last_opened_at',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultValue: const Constant(0),
  );
  static const VerificationMeta _hiddenFromRailMeta = const VerificationMeta(
    'hiddenFromRail',
  );
  @override
  late final GeneratedColumn<bool> hiddenFromRail = GeneratedColumn<bool>(
    'hidden_from_rail',
    aliasedName,
    false,
    type: DriftSqlType.bool,
    requiredDuringInsert: false,
    defaultConstraints: GeneratedColumn.constraintIsAlways(
      'CHECK ("hidden_from_rail" IN (0, 1))',
    ),
    defaultValue: const Constant(false),
  );
  static const VerificationMeta _avatarColorMeta = const VerificationMeta(
    'avatarColor',
  );
  @override
  late final GeneratedColumn<String> avatarColor = GeneratedColumn<String>(
    'avatar_color',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _sortIndexMeta = const VerificationMeta(
    'sortIndex',
  );
  @override
  late final GeneratedColumn<int> sortIndex = GeneratedColumn<int>(
    'sort_index',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultValue: const Constant(0),
  );
  @override
  List<GeneratedColumn> get $columns => [
    id,
    name,
    path,
    branch,
    lastOpenedAt,
    hiddenFromRail,
    avatarColor,
    sortIndex,
  ];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'code_projects';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalCodeProject> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    } else if (isInserting) {
      context.missing(_idMeta);
    }
    if (data.containsKey('name')) {
      context.handle(
        _nameMeta,
        name.isAcceptableOrUnknown(data['name']!, _nameMeta),
      );
    } else if (isInserting) {
      context.missing(_nameMeta);
    }
    if (data.containsKey('path')) {
      context.handle(
        _pathMeta,
        path.isAcceptableOrUnknown(data['path']!, _pathMeta),
      );
    } else if (isInserting) {
      context.missing(_pathMeta);
    }
    if (data.containsKey('branch')) {
      context.handle(
        _branchMeta,
        branch.isAcceptableOrUnknown(data['branch']!, _branchMeta),
      );
    }
    if (data.containsKey('last_opened_at')) {
      context.handle(
        _lastOpenedAtMeta,
        lastOpenedAt.isAcceptableOrUnknown(
          data['last_opened_at']!,
          _lastOpenedAtMeta,
        ),
      );
    }
    if (data.containsKey('hidden_from_rail')) {
      context.handle(
        _hiddenFromRailMeta,
        hiddenFromRail.isAcceptableOrUnknown(
          data['hidden_from_rail']!,
          _hiddenFromRailMeta,
        ),
      );
    }
    if (data.containsKey('avatar_color')) {
      context.handle(
        _avatarColorMeta,
        avatarColor.isAcceptableOrUnknown(
          data['avatar_color']!,
          _avatarColorMeta,
        ),
      );
    }
    if (data.containsKey('sort_index')) {
      context.handle(
        _sortIndexMeta,
        sortIndex.isAcceptableOrUnknown(data['sort_index']!, _sortIndexMeta),
      );
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalCodeProject map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalCodeProject(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}id'],
      )!,
      name: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}name'],
      )!,
      path: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}path'],
      )!,
      branch: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}branch'],
      ),
      lastOpenedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}last_opened_at'],
      )!,
      hiddenFromRail: attachedDatabase.typeMapping.read(
        DriftSqlType.bool,
        data['${effectivePrefix}hidden_from_rail'],
      )!,
      avatarColor: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}avatar_color'],
      ),
      sortIndex: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}sort_index'],
      )!,
    );
  }

  @override
  $CodeProjectsTable createAlias(String alias) {
    return $CodeProjectsTable(attachedDatabase, alias);
  }
}

class LocalCodeProject extends DataClass
    implements Insertable<LocalCodeProject> {
  final String id;
  final String name;

  /// 仓库绝对路径。
  final String path;

  /// 当前分支(展示用,nullable —— 非 git 目录或未解析)。
  final String? branch;

  /// 最后打开时间(ms epoch)。WelcomePage "最近" 排序用。
  final int lastOpenedAt;

  /// 从左 Rail 隐藏。不删项目、只隐藏。
  final bool hiddenFromRail;

  /// 头像底色(ProjectRail 头像;null = 由 name 哈希生成)。
  final String? avatarColor;

  /// 手动排序位次(M1 拖拽排序)。小在前;同值回退 lastOpenedAt。默认 0。
  final int sortIndex;
  const LocalCodeProject({
    required this.id,
    required this.name,
    required this.path,
    this.branch,
    required this.lastOpenedAt,
    required this.hiddenFromRail,
    this.avatarColor,
    required this.sortIndex,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<String>(id);
    map['name'] = Variable<String>(name);
    map['path'] = Variable<String>(path);
    if (!nullToAbsent || branch != null) {
      map['branch'] = Variable<String>(branch);
    }
    map['last_opened_at'] = Variable<int>(lastOpenedAt);
    map['hidden_from_rail'] = Variable<bool>(hiddenFromRail);
    if (!nullToAbsent || avatarColor != null) {
      map['avatar_color'] = Variable<String>(avatarColor);
    }
    map['sort_index'] = Variable<int>(sortIndex);
    return map;
  }

  CodeProjectsCompanion toCompanion(bool nullToAbsent) {
    return CodeProjectsCompanion(
      id: Value(id),
      name: Value(name),
      path: Value(path),
      branch: branch == null && nullToAbsent
          ? const Value.absent()
          : Value(branch),
      lastOpenedAt: Value(lastOpenedAt),
      hiddenFromRail: Value(hiddenFromRail),
      avatarColor: avatarColor == null && nullToAbsent
          ? const Value.absent()
          : Value(avatarColor),
      sortIndex: Value(sortIndex),
    );
  }

  factory LocalCodeProject.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalCodeProject(
      id: serializer.fromJson<String>(json['id']),
      name: serializer.fromJson<String>(json['name']),
      path: serializer.fromJson<String>(json['path']),
      branch: serializer.fromJson<String?>(json['branch']),
      lastOpenedAt: serializer.fromJson<int>(json['lastOpenedAt']),
      hiddenFromRail: serializer.fromJson<bool>(json['hiddenFromRail']),
      avatarColor: serializer.fromJson<String?>(json['avatarColor']),
      sortIndex: serializer.fromJson<int>(json['sortIndex']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<String>(id),
      'name': serializer.toJson<String>(name),
      'path': serializer.toJson<String>(path),
      'branch': serializer.toJson<String?>(branch),
      'lastOpenedAt': serializer.toJson<int>(lastOpenedAt),
      'hiddenFromRail': serializer.toJson<bool>(hiddenFromRail),
      'avatarColor': serializer.toJson<String?>(avatarColor),
      'sortIndex': serializer.toJson<int>(sortIndex),
    };
  }

  LocalCodeProject copyWith({
    String? id,
    String? name,
    String? path,
    Value<String?> branch = const Value.absent(),
    int? lastOpenedAt,
    bool? hiddenFromRail,
    Value<String?> avatarColor = const Value.absent(),
    int? sortIndex,
  }) => LocalCodeProject(
    id: id ?? this.id,
    name: name ?? this.name,
    path: path ?? this.path,
    branch: branch.present ? branch.value : this.branch,
    lastOpenedAt: lastOpenedAt ?? this.lastOpenedAt,
    hiddenFromRail: hiddenFromRail ?? this.hiddenFromRail,
    avatarColor: avatarColor.present ? avatarColor.value : this.avatarColor,
    sortIndex: sortIndex ?? this.sortIndex,
  );
  LocalCodeProject copyWithCompanion(CodeProjectsCompanion data) {
    return LocalCodeProject(
      id: data.id.present ? data.id.value : this.id,
      name: data.name.present ? data.name.value : this.name,
      path: data.path.present ? data.path.value : this.path,
      branch: data.branch.present ? data.branch.value : this.branch,
      lastOpenedAt: data.lastOpenedAt.present
          ? data.lastOpenedAt.value
          : this.lastOpenedAt,
      hiddenFromRail: data.hiddenFromRail.present
          ? data.hiddenFromRail.value
          : this.hiddenFromRail,
      avatarColor: data.avatarColor.present
          ? data.avatarColor.value
          : this.avatarColor,
      sortIndex: data.sortIndex.present ? data.sortIndex.value : this.sortIndex,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalCodeProject(')
          ..write('id: $id, ')
          ..write('name: $name, ')
          ..write('path: $path, ')
          ..write('branch: $branch, ')
          ..write('lastOpenedAt: $lastOpenedAt, ')
          ..write('hiddenFromRail: $hiddenFromRail, ')
          ..write('avatarColor: $avatarColor, ')
          ..write('sortIndex: $sortIndex')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(
    id,
    name,
    path,
    branch,
    lastOpenedAt,
    hiddenFromRail,
    avatarColor,
    sortIndex,
  );
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalCodeProject &&
          other.id == this.id &&
          other.name == this.name &&
          other.path == this.path &&
          other.branch == this.branch &&
          other.lastOpenedAt == this.lastOpenedAt &&
          other.hiddenFromRail == this.hiddenFromRail &&
          other.avatarColor == this.avatarColor &&
          other.sortIndex == this.sortIndex);
}

class CodeProjectsCompanion extends UpdateCompanion<LocalCodeProject> {
  final Value<String> id;
  final Value<String> name;
  final Value<String> path;
  final Value<String?> branch;
  final Value<int> lastOpenedAt;
  final Value<bool> hiddenFromRail;
  final Value<String?> avatarColor;
  final Value<int> sortIndex;
  final Value<int> rowid;
  const CodeProjectsCompanion({
    this.id = const Value.absent(),
    this.name = const Value.absent(),
    this.path = const Value.absent(),
    this.branch = const Value.absent(),
    this.lastOpenedAt = const Value.absent(),
    this.hiddenFromRail = const Value.absent(),
    this.avatarColor = const Value.absent(),
    this.sortIndex = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  CodeProjectsCompanion.insert({
    required String id,
    required String name,
    required String path,
    this.branch = const Value.absent(),
    this.lastOpenedAt = const Value.absent(),
    this.hiddenFromRail = const Value.absent(),
    this.avatarColor = const Value.absent(),
    this.sortIndex = const Value.absent(),
    this.rowid = const Value.absent(),
  }) : id = Value(id),
       name = Value(name),
       path = Value(path);
  static Insertable<LocalCodeProject> custom({
    Expression<String>? id,
    Expression<String>? name,
    Expression<String>? path,
    Expression<String>? branch,
    Expression<int>? lastOpenedAt,
    Expression<bool>? hiddenFromRail,
    Expression<String>? avatarColor,
    Expression<int>? sortIndex,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (name != null) 'name': name,
      if (path != null) 'path': path,
      if (branch != null) 'branch': branch,
      if (lastOpenedAt != null) 'last_opened_at': lastOpenedAt,
      if (hiddenFromRail != null) 'hidden_from_rail': hiddenFromRail,
      if (avatarColor != null) 'avatar_color': avatarColor,
      if (sortIndex != null) 'sort_index': sortIndex,
      if (rowid != null) 'rowid': rowid,
    });
  }

  CodeProjectsCompanion copyWith({
    Value<String>? id,
    Value<String>? name,
    Value<String>? path,
    Value<String?>? branch,
    Value<int>? lastOpenedAt,
    Value<bool>? hiddenFromRail,
    Value<String?>? avatarColor,
    Value<int>? sortIndex,
    Value<int>? rowid,
  }) {
    return CodeProjectsCompanion(
      id: id ?? this.id,
      name: name ?? this.name,
      path: path ?? this.path,
      branch: branch ?? this.branch,
      lastOpenedAt: lastOpenedAt ?? this.lastOpenedAt,
      hiddenFromRail: hiddenFromRail ?? this.hiddenFromRail,
      avatarColor: avatarColor ?? this.avatarColor,
      sortIndex: sortIndex ?? this.sortIndex,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<String>(id.value);
    }
    if (name.present) {
      map['name'] = Variable<String>(name.value);
    }
    if (path.present) {
      map['path'] = Variable<String>(path.value);
    }
    if (branch.present) {
      map['branch'] = Variable<String>(branch.value);
    }
    if (lastOpenedAt.present) {
      map['last_opened_at'] = Variable<int>(lastOpenedAt.value);
    }
    if (hiddenFromRail.present) {
      map['hidden_from_rail'] = Variable<bool>(hiddenFromRail.value);
    }
    if (avatarColor.present) {
      map['avatar_color'] = Variable<String>(avatarColor.value);
    }
    if (sortIndex.present) {
      map['sort_index'] = Variable<int>(sortIndex.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('CodeProjectsCompanion(')
          ..write('id: $id, ')
          ..write('name: $name, ')
          ..write('path: $path, ')
          ..write('branch: $branch, ')
          ..write('lastOpenedAt: $lastOpenedAt, ')
          ..write('hiddenFromRail: $hiddenFromRail, ')
          ..write('avatarColor: $avatarColor, ')
          ..write('sortIndex: $sortIndex, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $CodeTaskArtifactsTable extends CodeTaskArtifacts
    with TableInfo<$CodeTaskArtifactsTable, LocalCodeTaskArtifact> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $CodeTaskArtifactsTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<String> id = GeneratedColumn<String>(
    'id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _taskIdMeta = const VerificationMeta('taskId');
  @override
  late final GeneratedColumn<String> taskId = GeneratedColumn<String>(
    'task_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _kindMeta = const VerificationMeta('kind');
  @override
  late final GeneratedColumn<String> kind = GeneratedColumn<String>(
    'kind',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _relPathMeta = const VerificationMeta(
    'relPath',
  );
  @override
  late final GeneratedColumn<String> relPath = GeneratedColumn<String>(
    'rel_path',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _mimeTypeMeta = const VerificationMeta(
    'mimeType',
  );
  @override
  late final GeneratedColumn<String> mimeType = GeneratedColumn<String>(
    'mime_type',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _sizeBytesMeta = const VerificationMeta(
    'sizeBytes',
  );
  @override
  late final GeneratedColumn<int> sizeBytes = GeneratedColumn<int>(
    'size_bytes',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultValue: const Constant(0),
  );
  static const VerificationMeta _sha256Meta = const VerificationMeta('sha256');
  @override
  late final GeneratedColumn<String> sha256 = GeneratedColumn<String>(
    'sha256',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _opMeta = const VerificationMeta('op');
  @override
  late final GeneratedColumn<String> op = GeneratedColumn<String>(
    'op',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _previewSummaryMeta = const VerificationMeta(
    'previewSummary',
  );
  @override
  late final GeneratedColumn<String> previewSummary = GeneratedColumn<String>(
    'preview_summary',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _previewDataB64Meta = const VerificationMeta(
    'previewDataB64',
  );
  @override
  late final GeneratedColumn<String> previewDataB64 = GeneratedColumn<String>(
    'preview_data_b64',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _previewMimeTypeMeta = const VerificationMeta(
    'previewMimeType',
  );
  @override
  late final GeneratedColumn<String> previewMimeType = GeneratedColumn<String>(
    'preview_mime_type',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _createdAtMeta = const VerificationMeta(
    'createdAt',
  );
  @override
  late final GeneratedColumn<DateTime> createdAt = GeneratedColumn<DateTime>(
    'created_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  @override
  List<GeneratedColumn> get $columns => [
    id,
    taskId,
    kind,
    relPath,
    mimeType,
    sizeBytes,
    sha256,
    op,
    previewSummary,
    previewDataB64,
    previewMimeType,
    createdAt,
  ];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'code_task_artifacts';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalCodeTaskArtifact> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    } else if (isInserting) {
      context.missing(_idMeta);
    }
    if (data.containsKey('task_id')) {
      context.handle(
        _taskIdMeta,
        taskId.isAcceptableOrUnknown(data['task_id']!, _taskIdMeta),
      );
    } else if (isInserting) {
      context.missing(_taskIdMeta);
    }
    if (data.containsKey('kind')) {
      context.handle(
        _kindMeta,
        kind.isAcceptableOrUnknown(data['kind']!, _kindMeta),
      );
    } else if (isInserting) {
      context.missing(_kindMeta);
    }
    if (data.containsKey('rel_path')) {
      context.handle(
        _relPathMeta,
        relPath.isAcceptableOrUnknown(data['rel_path']!, _relPathMeta),
      );
    } else if (isInserting) {
      context.missing(_relPathMeta);
    }
    if (data.containsKey('mime_type')) {
      context.handle(
        _mimeTypeMeta,
        mimeType.isAcceptableOrUnknown(data['mime_type']!, _mimeTypeMeta),
      );
    }
    if (data.containsKey('size_bytes')) {
      context.handle(
        _sizeBytesMeta,
        sizeBytes.isAcceptableOrUnknown(data['size_bytes']!, _sizeBytesMeta),
      );
    }
    if (data.containsKey('sha256')) {
      context.handle(
        _sha256Meta,
        sha256.isAcceptableOrUnknown(data['sha256']!, _sha256Meta),
      );
    } else if (isInserting) {
      context.missing(_sha256Meta);
    }
    if (data.containsKey('op')) {
      context.handle(_opMeta, op.isAcceptableOrUnknown(data['op']!, _opMeta));
    } else if (isInserting) {
      context.missing(_opMeta);
    }
    if (data.containsKey('preview_summary')) {
      context.handle(
        _previewSummaryMeta,
        previewSummary.isAcceptableOrUnknown(
          data['preview_summary']!,
          _previewSummaryMeta,
        ),
      );
    }
    if (data.containsKey('preview_data_b64')) {
      context.handle(
        _previewDataB64Meta,
        previewDataB64.isAcceptableOrUnknown(
          data['preview_data_b64']!,
          _previewDataB64Meta,
        ),
      );
    }
    if (data.containsKey('preview_mime_type')) {
      context.handle(
        _previewMimeTypeMeta,
        previewMimeType.isAcceptableOrUnknown(
          data['preview_mime_type']!,
          _previewMimeTypeMeta,
        ),
      );
    }
    if (data.containsKey('created_at')) {
      context.handle(
        _createdAtMeta,
        createdAt.isAcceptableOrUnknown(data['created_at']!, _createdAtMeta),
      );
    } else if (isInserting) {
      context.missing(_createdAtMeta);
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalCodeTaskArtifact map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalCodeTaskArtifact(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}id'],
      )!,
      taskId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}task_id'],
      )!,
      kind: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}kind'],
      )!,
      relPath: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}rel_path'],
      )!,
      mimeType: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}mime_type'],
      ),
      sizeBytes: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}size_bytes'],
      )!,
      sha256: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}sha256'],
      )!,
      op: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}op'],
      )!,
      previewSummary: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}preview_summary'],
      ),
      previewDataB64: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}preview_data_b64'],
      ),
      previewMimeType: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}preview_mime_type'],
      ),
      createdAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}created_at'],
      )!,
    );
  }

  @override
  $CodeTaskArtifactsTable createAlias(String alias) {
    return $CodeTaskArtifactsTable(attachedDatabase, alias);
  }
}

class LocalCodeTaskArtifact extends DataClass
    implements Insertable<LocalCodeTaskArtifact> {
  final String id;
  final String taskId;
  final String kind;
  final String relPath;
  final String? mimeType;
  final int sizeBytes;
  final String sha256;
  final String op;

  /// L2 preview — 默认 null, 由 preview generator 填。
  final String? previewSummary;
  final String? previewDataB64;
  final String? previewMimeType;
  final DateTime createdAt;
  const LocalCodeTaskArtifact({
    required this.id,
    required this.taskId,
    required this.kind,
    required this.relPath,
    this.mimeType,
    required this.sizeBytes,
    required this.sha256,
    required this.op,
    this.previewSummary,
    this.previewDataB64,
    this.previewMimeType,
    required this.createdAt,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<String>(id);
    map['task_id'] = Variable<String>(taskId);
    map['kind'] = Variable<String>(kind);
    map['rel_path'] = Variable<String>(relPath);
    if (!nullToAbsent || mimeType != null) {
      map['mime_type'] = Variable<String>(mimeType);
    }
    map['size_bytes'] = Variable<int>(sizeBytes);
    map['sha256'] = Variable<String>(sha256);
    map['op'] = Variable<String>(op);
    if (!nullToAbsent || previewSummary != null) {
      map['preview_summary'] = Variable<String>(previewSummary);
    }
    if (!nullToAbsent || previewDataB64 != null) {
      map['preview_data_b64'] = Variable<String>(previewDataB64);
    }
    if (!nullToAbsent || previewMimeType != null) {
      map['preview_mime_type'] = Variable<String>(previewMimeType);
    }
    map['created_at'] = Variable<DateTime>(createdAt);
    return map;
  }

  CodeTaskArtifactsCompanion toCompanion(bool nullToAbsent) {
    return CodeTaskArtifactsCompanion(
      id: Value(id),
      taskId: Value(taskId),
      kind: Value(kind),
      relPath: Value(relPath),
      mimeType: mimeType == null && nullToAbsent
          ? const Value.absent()
          : Value(mimeType),
      sizeBytes: Value(sizeBytes),
      sha256: Value(sha256),
      op: Value(op),
      previewSummary: previewSummary == null && nullToAbsent
          ? const Value.absent()
          : Value(previewSummary),
      previewDataB64: previewDataB64 == null && nullToAbsent
          ? const Value.absent()
          : Value(previewDataB64),
      previewMimeType: previewMimeType == null && nullToAbsent
          ? const Value.absent()
          : Value(previewMimeType),
      createdAt: Value(createdAt),
    );
  }

  factory LocalCodeTaskArtifact.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalCodeTaskArtifact(
      id: serializer.fromJson<String>(json['id']),
      taskId: serializer.fromJson<String>(json['taskId']),
      kind: serializer.fromJson<String>(json['kind']),
      relPath: serializer.fromJson<String>(json['relPath']),
      mimeType: serializer.fromJson<String?>(json['mimeType']),
      sizeBytes: serializer.fromJson<int>(json['sizeBytes']),
      sha256: serializer.fromJson<String>(json['sha256']),
      op: serializer.fromJson<String>(json['op']),
      previewSummary: serializer.fromJson<String?>(json['previewSummary']),
      previewDataB64: serializer.fromJson<String?>(json['previewDataB64']),
      previewMimeType: serializer.fromJson<String?>(json['previewMimeType']),
      createdAt: serializer.fromJson<DateTime>(json['createdAt']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<String>(id),
      'taskId': serializer.toJson<String>(taskId),
      'kind': serializer.toJson<String>(kind),
      'relPath': serializer.toJson<String>(relPath),
      'mimeType': serializer.toJson<String?>(mimeType),
      'sizeBytes': serializer.toJson<int>(sizeBytes),
      'sha256': serializer.toJson<String>(sha256),
      'op': serializer.toJson<String>(op),
      'previewSummary': serializer.toJson<String?>(previewSummary),
      'previewDataB64': serializer.toJson<String?>(previewDataB64),
      'previewMimeType': serializer.toJson<String?>(previewMimeType),
      'createdAt': serializer.toJson<DateTime>(createdAt),
    };
  }

  LocalCodeTaskArtifact copyWith({
    String? id,
    String? taskId,
    String? kind,
    String? relPath,
    Value<String?> mimeType = const Value.absent(),
    int? sizeBytes,
    String? sha256,
    String? op,
    Value<String?> previewSummary = const Value.absent(),
    Value<String?> previewDataB64 = const Value.absent(),
    Value<String?> previewMimeType = const Value.absent(),
    DateTime? createdAt,
  }) => LocalCodeTaskArtifact(
    id: id ?? this.id,
    taskId: taskId ?? this.taskId,
    kind: kind ?? this.kind,
    relPath: relPath ?? this.relPath,
    mimeType: mimeType.present ? mimeType.value : this.mimeType,
    sizeBytes: sizeBytes ?? this.sizeBytes,
    sha256: sha256 ?? this.sha256,
    op: op ?? this.op,
    previewSummary: previewSummary.present
        ? previewSummary.value
        : this.previewSummary,
    previewDataB64: previewDataB64.present
        ? previewDataB64.value
        : this.previewDataB64,
    previewMimeType: previewMimeType.present
        ? previewMimeType.value
        : this.previewMimeType,
    createdAt: createdAt ?? this.createdAt,
  );
  LocalCodeTaskArtifact copyWithCompanion(CodeTaskArtifactsCompanion data) {
    return LocalCodeTaskArtifact(
      id: data.id.present ? data.id.value : this.id,
      taskId: data.taskId.present ? data.taskId.value : this.taskId,
      kind: data.kind.present ? data.kind.value : this.kind,
      relPath: data.relPath.present ? data.relPath.value : this.relPath,
      mimeType: data.mimeType.present ? data.mimeType.value : this.mimeType,
      sizeBytes: data.sizeBytes.present ? data.sizeBytes.value : this.sizeBytes,
      sha256: data.sha256.present ? data.sha256.value : this.sha256,
      op: data.op.present ? data.op.value : this.op,
      previewSummary: data.previewSummary.present
          ? data.previewSummary.value
          : this.previewSummary,
      previewDataB64: data.previewDataB64.present
          ? data.previewDataB64.value
          : this.previewDataB64,
      previewMimeType: data.previewMimeType.present
          ? data.previewMimeType.value
          : this.previewMimeType,
      createdAt: data.createdAt.present ? data.createdAt.value : this.createdAt,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalCodeTaskArtifact(')
          ..write('id: $id, ')
          ..write('taskId: $taskId, ')
          ..write('kind: $kind, ')
          ..write('relPath: $relPath, ')
          ..write('mimeType: $mimeType, ')
          ..write('sizeBytes: $sizeBytes, ')
          ..write('sha256: $sha256, ')
          ..write('op: $op, ')
          ..write('previewSummary: $previewSummary, ')
          ..write('previewDataB64: $previewDataB64, ')
          ..write('previewMimeType: $previewMimeType, ')
          ..write('createdAt: $createdAt')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(
    id,
    taskId,
    kind,
    relPath,
    mimeType,
    sizeBytes,
    sha256,
    op,
    previewSummary,
    previewDataB64,
    previewMimeType,
    createdAt,
  );
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalCodeTaskArtifact &&
          other.id == this.id &&
          other.taskId == this.taskId &&
          other.kind == this.kind &&
          other.relPath == this.relPath &&
          other.mimeType == this.mimeType &&
          other.sizeBytes == this.sizeBytes &&
          other.sha256 == this.sha256 &&
          other.op == this.op &&
          other.previewSummary == this.previewSummary &&
          other.previewDataB64 == this.previewDataB64 &&
          other.previewMimeType == this.previewMimeType &&
          other.createdAt == this.createdAt);
}

class CodeTaskArtifactsCompanion
    extends UpdateCompanion<LocalCodeTaskArtifact> {
  final Value<String> id;
  final Value<String> taskId;
  final Value<String> kind;
  final Value<String> relPath;
  final Value<String?> mimeType;
  final Value<int> sizeBytes;
  final Value<String> sha256;
  final Value<String> op;
  final Value<String?> previewSummary;
  final Value<String?> previewDataB64;
  final Value<String?> previewMimeType;
  final Value<DateTime> createdAt;
  final Value<int> rowid;
  const CodeTaskArtifactsCompanion({
    this.id = const Value.absent(),
    this.taskId = const Value.absent(),
    this.kind = const Value.absent(),
    this.relPath = const Value.absent(),
    this.mimeType = const Value.absent(),
    this.sizeBytes = const Value.absent(),
    this.sha256 = const Value.absent(),
    this.op = const Value.absent(),
    this.previewSummary = const Value.absent(),
    this.previewDataB64 = const Value.absent(),
    this.previewMimeType = const Value.absent(),
    this.createdAt = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  CodeTaskArtifactsCompanion.insert({
    required String id,
    required String taskId,
    required String kind,
    required String relPath,
    this.mimeType = const Value.absent(),
    this.sizeBytes = const Value.absent(),
    required String sha256,
    required String op,
    this.previewSummary = const Value.absent(),
    this.previewDataB64 = const Value.absent(),
    this.previewMimeType = const Value.absent(),
    required DateTime createdAt,
    this.rowid = const Value.absent(),
  }) : id = Value(id),
       taskId = Value(taskId),
       kind = Value(kind),
       relPath = Value(relPath),
       sha256 = Value(sha256),
       op = Value(op),
       createdAt = Value(createdAt);
  static Insertable<LocalCodeTaskArtifact> custom({
    Expression<String>? id,
    Expression<String>? taskId,
    Expression<String>? kind,
    Expression<String>? relPath,
    Expression<String>? mimeType,
    Expression<int>? sizeBytes,
    Expression<String>? sha256,
    Expression<String>? op,
    Expression<String>? previewSummary,
    Expression<String>? previewDataB64,
    Expression<String>? previewMimeType,
    Expression<DateTime>? createdAt,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (taskId != null) 'task_id': taskId,
      if (kind != null) 'kind': kind,
      if (relPath != null) 'rel_path': relPath,
      if (mimeType != null) 'mime_type': mimeType,
      if (sizeBytes != null) 'size_bytes': sizeBytes,
      if (sha256 != null) 'sha256': sha256,
      if (op != null) 'op': op,
      if (previewSummary != null) 'preview_summary': previewSummary,
      if (previewDataB64 != null) 'preview_data_b64': previewDataB64,
      if (previewMimeType != null) 'preview_mime_type': previewMimeType,
      if (createdAt != null) 'created_at': createdAt,
      if (rowid != null) 'rowid': rowid,
    });
  }

  CodeTaskArtifactsCompanion copyWith({
    Value<String>? id,
    Value<String>? taskId,
    Value<String>? kind,
    Value<String>? relPath,
    Value<String?>? mimeType,
    Value<int>? sizeBytes,
    Value<String>? sha256,
    Value<String>? op,
    Value<String?>? previewSummary,
    Value<String?>? previewDataB64,
    Value<String?>? previewMimeType,
    Value<DateTime>? createdAt,
    Value<int>? rowid,
  }) {
    return CodeTaskArtifactsCompanion(
      id: id ?? this.id,
      taskId: taskId ?? this.taskId,
      kind: kind ?? this.kind,
      relPath: relPath ?? this.relPath,
      mimeType: mimeType ?? this.mimeType,
      sizeBytes: sizeBytes ?? this.sizeBytes,
      sha256: sha256 ?? this.sha256,
      op: op ?? this.op,
      previewSummary: previewSummary ?? this.previewSummary,
      previewDataB64: previewDataB64 ?? this.previewDataB64,
      previewMimeType: previewMimeType ?? this.previewMimeType,
      createdAt: createdAt ?? this.createdAt,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<String>(id.value);
    }
    if (taskId.present) {
      map['task_id'] = Variable<String>(taskId.value);
    }
    if (kind.present) {
      map['kind'] = Variable<String>(kind.value);
    }
    if (relPath.present) {
      map['rel_path'] = Variable<String>(relPath.value);
    }
    if (mimeType.present) {
      map['mime_type'] = Variable<String>(mimeType.value);
    }
    if (sizeBytes.present) {
      map['size_bytes'] = Variable<int>(sizeBytes.value);
    }
    if (sha256.present) {
      map['sha256'] = Variable<String>(sha256.value);
    }
    if (op.present) {
      map['op'] = Variable<String>(op.value);
    }
    if (previewSummary.present) {
      map['preview_summary'] = Variable<String>(previewSummary.value);
    }
    if (previewDataB64.present) {
      map['preview_data_b64'] = Variable<String>(previewDataB64.value);
    }
    if (previewMimeType.present) {
      map['preview_mime_type'] = Variable<String>(previewMimeType.value);
    }
    if (createdAt.present) {
      map['created_at'] = Variable<DateTime>(createdAt.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('CodeTaskArtifactsCompanion(')
          ..write('id: $id, ')
          ..write('taskId: $taskId, ')
          ..write('kind: $kind, ')
          ..write('relPath: $relPath, ')
          ..write('mimeType: $mimeType, ')
          ..write('sizeBytes: $sizeBytes, ')
          ..write('sha256: $sha256, ')
          ..write('op: $op, ')
          ..write('previewSummary: $previewSummary, ')
          ..write('previewDataB64: $previewDataB64, ')
          ..write('previewMimeType: $previewMimeType, ')
          ..write('createdAt: $createdAt, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $ChatThreadsV2Table extends ChatThreadsV2
    with TableInfo<$ChatThreadsV2Table, LocalChatThreadV2> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $ChatThreadsV2Table(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<String> id = GeneratedColumn<String>(
    'id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _titleMeta = const VerificationMeta('title');
  @override
  late final GeneratedColumn<String> title = GeneratedColumn<String>(
    'title',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant(''),
  );
  static const VerificationMeta _modeMeta = const VerificationMeta('mode');
  @override
  late final GeneratedColumn<String> mode = GeneratedColumn<String>(
    'mode',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _environmentIdMeta = const VerificationMeta(
    'environmentId',
  );
  @override
  late final GeneratedColumn<String> environmentId = GeneratedColumn<String>(
    'environment_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _poolTagMeta = const VerificationMeta(
    'poolTag',
  );
  @override
  late final GeneratedColumn<String> poolTag = GeneratedColumn<String>(
    'pool_tag',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _modelMeta = const VerificationMeta('model');
  @override
  late final GeneratedColumn<String> model = GeneratedColumn<String>(
    'model',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _providerIdMeta = const VerificationMeta(
    'providerId',
  );
  @override
  late final GeneratedColumn<String> providerId = GeneratedColumn<String>(
    'provider_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _systemPromptMeta = const VerificationMeta(
    'systemPrompt',
  );
  @override
  late final GeneratedColumn<String> systemPrompt = GeneratedColumn<String>(
    'system_prompt',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _projectIdMeta = const VerificationMeta(
    'projectId',
  );
  @override
  late final GeneratedColumn<String> projectId = GeneratedColumn<String>(
    'project_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _workdirMeta = const VerificationMeta(
    'workdir',
  );
  @override
  late final GeneratedColumn<String> workdir = GeneratedColumn<String>(
    'workdir',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _autoApproveMeta = const VerificationMeta(
    'autoApprove',
  );
  @override
  late final GeneratedColumn<String> autoApprove = GeneratedColumn<String>(
    'auto_approve',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant('manual'),
  );
  static const VerificationMeta _runtimeEnvModeMeta = const VerificationMeta(
    'runtimeEnvMode',
  );
  @override
  late final GeneratedColumn<String> runtimeEnvMode = GeneratedColumn<String>(
    'runtime_env_mode',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant('none'),
  );
  static const VerificationMeta _backendMeta = const VerificationMeta(
    'backend',
  );
  @override
  late final GeneratedColumn<String> backend = GeneratedColumn<String>(
    'backend',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant('biumindkit'),
  );
  static const VerificationMeta _pinnedMeta = const VerificationMeta('pinned');
  @override
  late final GeneratedColumn<bool> pinned = GeneratedColumn<bool>(
    'pinned',
    aliasedName,
    false,
    type: DriftSqlType.bool,
    requiredDuringInsert: false,
    defaultConstraints: GeneratedColumn.constraintIsAlways(
      'CHECK ("pinned" IN (0, 1))',
    ),
    defaultValue: const Constant(false),
  );
  static const VerificationMeta _archivedMeta = const VerificationMeta(
    'archived',
  );
  @override
  late final GeneratedColumn<bool> archived = GeneratedColumn<bool>(
    'archived',
    aliasedName,
    false,
    type: DriftSqlType.bool,
    requiredDuringInsert: false,
    defaultConstraints: GeneratedColumn.constraintIsAlways(
      'CHECK ("archived" IN (0, 1))',
    ),
    defaultValue: const Constant(false),
  );
  static const VerificationMeta _createdAtMeta = const VerificationMeta(
    'createdAt',
  );
  @override
  late final GeneratedColumn<DateTime> createdAt = GeneratedColumn<DateTime>(
    'created_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _updatedAtMeta = const VerificationMeta(
    'updatedAt',
  );
  @override
  late final GeneratedColumn<DateTime> updatedAt = GeneratedColumn<DateTime>(
    'updated_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _remoteUpdatedAtUsMeta = const VerificationMeta(
    'remoteUpdatedAtUs',
  );
  @override
  late final GeneratedColumn<int> remoteUpdatedAtUs = GeneratedColumn<int>(
    'remote_updated_at_us',
    aliasedName,
    true,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _ownerKeyMeta = const VerificationMeta(
    'ownerKey',
  );
  @override
  late final GeneratedColumn<String> ownerKey = GeneratedColumn<String>(
    'owner_key',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant(''),
  );
  @override
  List<GeneratedColumn> get $columns => [
    id,
    title,
    mode,
    environmentId,
    poolTag,
    model,
    providerId,
    systemPrompt,
    projectId,
    workdir,
    autoApprove,
    runtimeEnvMode,
    backend,
    pinned,
    archived,
    createdAt,
    updatedAt,
    remoteUpdatedAtUs,
    ownerKey,
  ];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'chat_threads_v2';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalChatThreadV2> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    } else if (isInserting) {
      context.missing(_idMeta);
    }
    if (data.containsKey('title')) {
      context.handle(
        _titleMeta,
        title.isAcceptableOrUnknown(data['title']!, _titleMeta),
      );
    }
    if (data.containsKey('mode')) {
      context.handle(
        _modeMeta,
        mode.isAcceptableOrUnknown(data['mode']!, _modeMeta),
      );
    } else if (isInserting) {
      context.missing(_modeMeta);
    }
    if (data.containsKey('environment_id')) {
      context.handle(
        _environmentIdMeta,
        environmentId.isAcceptableOrUnknown(
          data['environment_id']!,
          _environmentIdMeta,
        ),
      );
    }
    if (data.containsKey('pool_tag')) {
      context.handle(
        _poolTagMeta,
        poolTag.isAcceptableOrUnknown(data['pool_tag']!, _poolTagMeta),
      );
    }
    if (data.containsKey('model')) {
      context.handle(
        _modelMeta,
        model.isAcceptableOrUnknown(data['model']!, _modelMeta),
      );
    }
    if (data.containsKey('provider_id')) {
      context.handle(
        _providerIdMeta,
        providerId.isAcceptableOrUnknown(data['provider_id']!, _providerIdMeta),
      );
    }
    if (data.containsKey('system_prompt')) {
      context.handle(
        _systemPromptMeta,
        systemPrompt.isAcceptableOrUnknown(
          data['system_prompt']!,
          _systemPromptMeta,
        ),
      );
    }
    if (data.containsKey('project_id')) {
      context.handle(
        _projectIdMeta,
        projectId.isAcceptableOrUnknown(data['project_id']!, _projectIdMeta),
      );
    }
    if (data.containsKey('workdir')) {
      context.handle(
        _workdirMeta,
        workdir.isAcceptableOrUnknown(data['workdir']!, _workdirMeta),
      );
    }
    if (data.containsKey('auto_approve')) {
      context.handle(
        _autoApproveMeta,
        autoApprove.isAcceptableOrUnknown(
          data['auto_approve']!,
          _autoApproveMeta,
        ),
      );
    }
    if (data.containsKey('runtime_env_mode')) {
      context.handle(
        _runtimeEnvModeMeta,
        runtimeEnvMode.isAcceptableOrUnknown(
          data['runtime_env_mode']!,
          _runtimeEnvModeMeta,
        ),
      );
    }
    if (data.containsKey('backend')) {
      context.handle(
        _backendMeta,
        backend.isAcceptableOrUnknown(data['backend']!, _backendMeta),
      );
    }
    if (data.containsKey('pinned')) {
      context.handle(
        _pinnedMeta,
        pinned.isAcceptableOrUnknown(data['pinned']!, _pinnedMeta),
      );
    }
    if (data.containsKey('archived')) {
      context.handle(
        _archivedMeta,
        archived.isAcceptableOrUnknown(data['archived']!, _archivedMeta),
      );
    }
    if (data.containsKey('created_at')) {
      context.handle(
        _createdAtMeta,
        createdAt.isAcceptableOrUnknown(data['created_at']!, _createdAtMeta),
      );
    } else if (isInserting) {
      context.missing(_createdAtMeta);
    }
    if (data.containsKey('updated_at')) {
      context.handle(
        _updatedAtMeta,
        updatedAt.isAcceptableOrUnknown(data['updated_at']!, _updatedAtMeta),
      );
    } else if (isInserting) {
      context.missing(_updatedAtMeta);
    }
    if (data.containsKey('remote_updated_at_us')) {
      context.handle(
        _remoteUpdatedAtUsMeta,
        remoteUpdatedAtUs.isAcceptableOrUnknown(
          data['remote_updated_at_us']!,
          _remoteUpdatedAtUsMeta,
        ),
      );
    }
    if (data.containsKey('owner_key')) {
      context.handle(
        _ownerKeyMeta,
        ownerKey.isAcceptableOrUnknown(data['owner_key']!, _ownerKeyMeta),
      );
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalChatThreadV2 map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalChatThreadV2(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}id'],
      )!,
      title: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}title'],
      )!,
      mode: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}mode'],
      )!,
      environmentId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}environment_id'],
      ),
      poolTag: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}pool_tag'],
      ),
      model: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}model'],
      ),
      providerId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}provider_id'],
      ),
      systemPrompt: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}system_prompt'],
      ),
      projectId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}project_id'],
      ),
      workdir: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}workdir'],
      ),
      autoApprove: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}auto_approve'],
      )!,
      runtimeEnvMode: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}runtime_env_mode'],
      )!,
      backend: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}backend'],
      )!,
      pinned: attachedDatabase.typeMapping.read(
        DriftSqlType.bool,
        data['${effectivePrefix}pinned'],
      )!,
      archived: attachedDatabase.typeMapping.read(
        DriftSqlType.bool,
        data['${effectivePrefix}archived'],
      )!,
      createdAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}created_at'],
      )!,
      updatedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}updated_at'],
      )!,
      remoteUpdatedAtUs: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}remote_updated_at_us'],
      ),
      ownerKey: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}owner_key'],
      )!,
    );
  }

  @override
  $ChatThreadsV2Table createAlias(String alias) {
    return $ChatThreadsV2Table(attachedDatabase, alias);
  }
}

class LocalChatThreadV2 extends DataClass
    implements Insertable<LocalChatThreadV2> {
  final String id;
  final String title;

  /// 'chat' | 'agent' | 'task'
  final String mode;

  /// agent / task mode 选的 worker; chat mode 必空
  final String? environmentId;

  /// task mode 路由用 pool tag; chat / agent 必空
  final String? poolTag;
  final String? model;

  /// 指定走哪个 chat.providers.provider_id slug(biumind_cloud / anthropic / ...)。
  /// null = 老语义,brain 自己挑 active provider。picker 选模型时一并设上,
  /// 保证同 model id 多 provider 时切换准确。
  final String? providerId;
  final String? systemPrompt;

  /// 关联到的 wiki project；null = 全局对话。Wiki 工作区内嵌 chat 面板
  /// 用这个过滤；按 (project_id, updated_at desc) 分组排序。
  final String? projectId;

  /// Agent / task 模式下 daemon 跑工具的工作目录。chat 模式必空。
  /// brain 投递 work payload 时透传，daemon worker.go chdir + 写到
  /// biumindkit Options.Cwd / PermissionUpdate.AddDirectories。
  final String? workdir;

  /// Agent 工具调用自治程度: 'auto' / 'whitelist' / 'manual' (default).
  /// client 拦截 SDKControlRequest{can_use_tool} 时按此字段决定立即应答
  /// or 弹 ApprovalCard。chat 模式无意义但字段共用,简单。
  final String autoApprove;

  /// 工具执行环境 (Runtime v3 轴 B): 'none' | 'local' | 'cloud'。决定工具在
  /// 哪落地执行,与 mode(轴 A:loop 在哪)正交。chat 恒 'none'；agent 默认
  /// 'local'(本机 daemon),可选 'cloud'(云沙箱,R5 落地);task 恒 'cloud'。
  /// createSession 透传给 brain → agent_sessions.runtime_env_mode。
  final String runtimeEnvMode;

  /// Agent loop backend (Runtime v3 R3/Q3): 'biumindkit'(默认) | 'claude-cli'
  /// | 'codex-cli'。biumindkit=内建 loop;claude-cli=外部 Claude Code CLI 当
  /// backend(CLI 自执行工具,用用户自己的 ~/.claude 订阅,不计 biumind 额度)。
  /// 仅 agent 模式有意义;chat/task 恒 biumindkit。createSession 透传给 brain。
  final String backend;
  final bool pinned;
  final bool archived;
  final DateTime createdAt;
  final DateTime updatedAt;

  /// 下行同步的精确比较基准: 服务端 thread.updated_at 的微秒整数
  /// (RFC3339Nano 解析后 microsecondsSinceEpoch 原样写入,~1.75e15 在
  /// web double 安全范围内,无需 int64)。updatedAt 列被 Drift 截断到秒,
  /// 无法区分同一秒内的多次服务端更新(user/assistant 同秒落库),故另存
  /// 此列。null = 本机产生、从未从服务端同步过的会话。
  final int? remoteUpdatedAtUs;

  /// P0 数据隔离（docs/BiuMind-Local-Data-Isolation-Design.md §2）：scope 列 =
  /// sha256(normalize(identityUrl)) + ":" + JWT sub，「环境 × 账号」复合键。
  /// 所有查询强制按此列过滤；'' 为非法值（查询永不匹配，写入必填当前 scope）。
  final String ownerKey;
  const LocalChatThreadV2({
    required this.id,
    required this.title,
    required this.mode,
    this.environmentId,
    this.poolTag,
    this.model,
    this.providerId,
    this.systemPrompt,
    this.projectId,
    this.workdir,
    required this.autoApprove,
    required this.runtimeEnvMode,
    required this.backend,
    required this.pinned,
    required this.archived,
    required this.createdAt,
    required this.updatedAt,
    this.remoteUpdatedAtUs,
    required this.ownerKey,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<String>(id);
    map['title'] = Variable<String>(title);
    map['mode'] = Variable<String>(mode);
    if (!nullToAbsent || environmentId != null) {
      map['environment_id'] = Variable<String>(environmentId);
    }
    if (!nullToAbsent || poolTag != null) {
      map['pool_tag'] = Variable<String>(poolTag);
    }
    if (!nullToAbsent || model != null) {
      map['model'] = Variable<String>(model);
    }
    if (!nullToAbsent || providerId != null) {
      map['provider_id'] = Variable<String>(providerId);
    }
    if (!nullToAbsent || systemPrompt != null) {
      map['system_prompt'] = Variable<String>(systemPrompt);
    }
    if (!nullToAbsent || projectId != null) {
      map['project_id'] = Variable<String>(projectId);
    }
    if (!nullToAbsent || workdir != null) {
      map['workdir'] = Variable<String>(workdir);
    }
    map['auto_approve'] = Variable<String>(autoApprove);
    map['runtime_env_mode'] = Variable<String>(runtimeEnvMode);
    map['backend'] = Variable<String>(backend);
    map['pinned'] = Variable<bool>(pinned);
    map['archived'] = Variable<bool>(archived);
    map['created_at'] = Variable<DateTime>(createdAt);
    map['updated_at'] = Variable<DateTime>(updatedAt);
    if (!nullToAbsent || remoteUpdatedAtUs != null) {
      map['remote_updated_at_us'] = Variable<int>(remoteUpdatedAtUs);
    }
    map['owner_key'] = Variable<String>(ownerKey);
    return map;
  }

  ChatThreadsV2Companion toCompanion(bool nullToAbsent) {
    return ChatThreadsV2Companion(
      id: Value(id),
      title: Value(title),
      mode: Value(mode),
      environmentId: environmentId == null && nullToAbsent
          ? const Value.absent()
          : Value(environmentId),
      poolTag: poolTag == null && nullToAbsent
          ? const Value.absent()
          : Value(poolTag),
      model: model == null && nullToAbsent
          ? const Value.absent()
          : Value(model),
      providerId: providerId == null && nullToAbsent
          ? const Value.absent()
          : Value(providerId),
      systemPrompt: systemPrompt == null && nullToAbsent
          ? const Value.absent()
          : Value(systemPrompt),
      projectId: projectId == null && nullToAbsent
          ? const Value.absent()
          : Value(projectId),
      workdir: workdir == null && nullToAbsent
          ? const Value.absent()
          : Value(workdir),
      autoApprove: Value(autoApprove),
      runtimeEnvMode: Value(runtimeEnvMode),
      backend: Value(backend),
      pinned: Value(pinned),
      archived: Value(archived),
      createdAt: Value(createdAt),
      updatedAt: Value(updatedAt),
      remoteUpdatedAtUs: remoteUpdatedAtUs == null && nullToAbsent
          ? const Value.absent()
          : Value(remoteUpdatedAtUs),
      ownerKey: Value(ownerKey),
    );
  }

  factory LocalChatThreadV2.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalChatThreadV2(
      id: serializer.fromJson<String>(json['id']),
      title: serializer.fromJson<String>(json['title']),
      mode: serializer.fromJson<String>(json['mode']),
      environmentId: serializer.fromJson<String?>(json['environmentId']),
      poolTag: serializer.fromJson<String?>(json['poolTag']),
      model: serializer.fromJson<String?>(json['model']),
      providerId: serializer.fromJson<String?>(json['providerId']),
      systemPrompt: serializer.fromJson<String?>(json['systemPrompt']),
      projectId: serializer.fromJson<String?>(json['projectId']),
      workdir: serializer.fromJson<String?>(json['workdir']),
      autoApprove: serializer.fromJson<String>(json['autoApprove']),
      runtimeEnvMode: serializer.fromJson<String>(json['runtimeEnvMode']),
      backend: serializer.fromJson<String>(json['backend']),
      pinned: serializer.fromJson<bool>(json['pinned']),
      archived: serializer.fromJson<bool>(json['archived']),
      createdAt: serializer.fromJson<DateTime>(json['createdAt']),
      updatedAt: serializer.fromJson<DateTime>(json['updatedAt']),
      remoteUpdatedAtUs: serializer.fromJson<int?>(json['remoteUpdatedAtUs']),
      ownerKey: serializer.fromJson<String>(json['ownerKey']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<String>(id),
      'title': serializer.toJson<String>(title),
      'mode': serializer.toJson<String>(mode),
      'environmentId': serializer.toJson<String?>(environmentId),
      'poolTag': serializer.toJson<String?>(poolTag),
      'model': serializer.toJson<String?>(model),
      'providerId': serializer.toJson<String?>(providerId),
      'systemPrompt': serializer.toJson<String?>(systemPrompt),
      'projectId': serializer.toJson<String?>(projectId),
      'workdir': serializer.toJson<String?>(workdir),
      'autoApprove': serializer.toJson<String>(autoApprove),
      'runtimeEnvMode': serializer.toJson<String>(runtimeEnvMode),
      'backend': serializer.toJson<String>(backend),
      'pinned': serializer.toJson<bool>(pinned),
      'archived': serializer.toJson<bool>(archived),
      'createdAt': serializer.toJson<DateTime>(createdAt),
      'updatedAt': serializer.toJson<DateTime>(updatedAt),
      'remoteUpdatedAtUs': serializer.toJson<int?>(remoteUpdatedAtUs),
      'ownerKey': serializer.toJson<String>(ownerKey),
    };
  }

  LocalChatThreadV2 copyWith({
    String? id,
    String? title,
    String? mode,
    Value<String?> environmentId = const Value.absent(),
    Value<String?> poolTag = const Value.absent(),
    Value<String?> model = const Value.absent(),
    Value<String?> providerId = const Value.absent(),
    Value<String?> systemPrompt = const Value.absent(),
    Value<String?> projectId = const Value.absent(),
    Value<String?> workdir = const Value.absent(),
    String? autoApprove,
    String? runtimeEnvMode,
    String? backend,
    bool? pinned,
    bool? archived,
    DateTime? createdAt,
    DateTime? updatedAt,
    Value<int?> remoteUpdatedAtUs = const Value.absent(),
    String? ownerKey,
  }) => LocalChatThreadV2(
    id: id ?? this.id,
    title: title ?? this.title,
    mode: mode ?? this.mode,
    environmentId: environmentId.present
        ? environmentId.value
        : this.environmentId,
    poolTag: poolTag.present ? poolTag.value : this.poolTag,
    model: model.present ? model.value : this.model,
    providerId: providerId.present ? providerId.value : this.providerId,
    systemPrompt: systemPrompt.present ? systemPrompt.value : this.systemPrompt,
    projectId: projectId.present ? projectId.value : this.projectId,
    workdir: workdir.present ? workdir.value : this.workdir,
    autoApprove: autoApprove ?? this.autoApprove,
    runtimeEnvMode: runtimeEnvMode ?? this.runtimeEnvMode,
    backend: backend ?? this.backend,
    pinned: pinned ?? this.pinned,
    archived: archived ?? this.archived,
    createdAt: createdAt ?? this.createdAt,
    updatedAt: updatedAt ?? this.updatedAt,
    remoteUpdatedAtUs: remoteUpdatedAtUs.present
        ? remoteUpdatedAtUs.value
        : this.remoteUpdatedAtUs,
    ownerKey: ownerKey ?? this.ownerKey,
  );
  LocalChatThreadV2 copyWithCompanion(ChatThreadsV2Companion data) {
    return LocalChatThreadV2(
      id: data.id.present ? data.id.value : this.id,
      title: data.title.present ? data.title.value : this.title,
      mode: data.mode.present ? data.mode.value : this.mode,
      environmentId: data.environmentId.present
          ? data.environmentId.value
          : this.environmentId,
      poolTag: data.poolTag.present ? data.poolTag.value : this.poolTag,
      model: data.model.present ? data.model.value : this.model,
      providerId: data.providerId.present
          ? data.providerId.value
          : this.providerId,
      systemPrompt: data.systemPrompt.present
          ? data.systemPrompt.value
          : this.systemPrompt,
      projectId: data.projectId.present ? data.projectId.value : this.projectId,
      workdir: data.workdir.present ? data.workdir.value : this.workdir,
      autoApprove: data.autoApprove.present
          ? data.autoApprove.value
          : this.autoApprove,
      runtimeEnvMode: data.runtimeEnvMode.present
          ? data.runtimeEnvMode.value
          : this.runtimeEnvMode,
      backend: data.backend.present ? data.backend.value : this.backend,
      pinned: data.pinned.present ? data.pinned.value : this.pinned,
      archived: data.archived.present ? data.archived.value : this.archived,
      createdAt: data.createdAt.present ? data.createdAt.value : this.createdAt,
      updatedAt: data.updatedAt.present ? data.updatedAt.value : this.updatedAt,
      remoteUpdatedAtUs: data.remoteUpdatedAtUs.present
          ? data.remoteUpdatedAtUs.value
          : this.remoteUpdatedAtUs,
      ownerKey: data.ownerKey.present ? data.ownerKey.value : this.ownerKey,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalChatThreadV2(')
          ..write('id: $id, ')
          ..write('title: $title, ')
          ..write('mode: $mode, ')
          ..write('environmentId: $environmentId, ')
          ..write('poolTag: $poolTag, ')
          ..write('model: $model, ')
          ..write('providerId: $providerId, ')
          ..write('systemPrompt: $systemPrompt, ')
          ..write('projectId: $projectId, ')
          ..write('workdir: $workdir, ')
          ..write('autoApprove: $autoApprove, ')
          ..write('runtimeEnvMode: $runtimeEnvMode, ')
          ..write('backend: $backend, ')
          ..write('pinned: $pinned, ')
          ..write('archived: $archived, ')
          ..write('createdAt: $createdAt, ')
          ..write('updatedAt: $updatedAt, ')
          ..write('remoteUpdatedAtUs: $remoteUpdatedAtUs, ')
          ..write('ownerKey: $ownerKey')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(
    id,
    title,
    mode,
    environmentId,
    poolTag,
    model,
    providerId,
    systemPrompt,
    projectId,
    workdir,
    autoApprove,
    runtimeEnvMode,
    backend,
    pinned,
    archived,
    createdAt,
    updatedAt,
    remoteUpdatedAtUs,
    ownerKey,
  );
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalChatThreadV2 &&
          other.id == this.id &&
          other.title == this.title &&
          other.mode == this.mode &&
          other.environmentId == this.environmentId &&
          other.poolTag == this.poolTag &&
          other.model == this.model &&
          other.providerId == this.providerId &&
          other.systemPrompt == this.systemPrompt &&
          other.projectId == this.projectId &&
          other.workdir == this.workdir &&
          other.autoApprove == this.autoApprove &&
          other.runtimeEnvMode == this.runtimeEnvMode &&
          other.backend == this.backend &&
          other.pinned == this.pinned &&
          other.archived == this.archived &&
          other.createdAt == this.createdAt &&
          other.updatedAt == this.updatedAt &&
          other.remoteUpdatedAtUs == this.remoteUpdatedAtUs &&
          other.ownerKey == this.ownerKey);
}

class ChatThreadsV2Companion extends UpdateCompanion<LocalChatThreadV2> {
  final Value<String> id;
  final Value<String> title;
  final Value<String> mode;
  final Value<String?> environmentId;
  final Value<String?> poolTag;
  final Value<String?> model;
  final Value<String?> providerId;
  final Value<String?> systemPrompt;
  final Value<String?> projectId;
  final Value<String?> workdir;
  final Value<String> autoApprove;
  final Value<String> runtimeEnvMode;
  final Value<String> backend;
  final Value<bool> pinned;
  final Value<bool> archived;
  final Value<DateTime> createdAt;
  final Value<DateTime> updatedAt;
  final Value<int?> remoteUpdatedAtUs;
  final Value<String> ownerKey;
  final Value<int> rowid;
  const ChatThreadsV2Companion({
    this.id = const Value.absent(),
    this.title = const Value.absent(),
    this.mode = const Value.absent(),
    this.environmentId = const Value.absent(),
    this.poolTag = const Value.absent(),
    this.model = const Value.absent(),
    this.providerId = const Value.absent(),
    this.systemPrompt = const Value.absent(),
    this.projectId = const Value.absent(),
    this.workdir = const Value.absent(),
    this.autoApprove = const Value.absent(),
    this.runtimeEnvMode = const Value.absent(),
    this.backend = const Value.absent(),
    this.pinned = const Value.absent(),
    this.archived = const Value.absent(),
    this.createdAt = const Value.absent(),
    this.updatedAt = const Value.absent(),
    this.remoteUpdatedAtUs = const Value.absent(),
    this.ownerKey = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  ChatThreadsV2Companion.insert({
    required String id,
    this.title = const Value.absent(),
    required String mode,
    this.environmentId = const Value.absent(),
    this.poolTag = const Value.absent(),
    this.model = const Value.absent(),
    this.providerId = const Value.absent(),
    this.systemPrompt = const Value.absent(),
    this.projectId = const Value.absent(),
    this.workdir = const Value.absent(),
    this.autoApprove = const Value.absent(),
    this.runtimeEnvMode = const Value.absent(),
    this.backend = const Value.absent(),
    this.pinned = const Value.absent(),
    this.archived = const Value.absent(),
    required DateTime createdAt,
    required DateTime updatedAt,
    this.remoteUpdatedAtUs = const Value.absent(),
    this.ownerKey = const Value.absent(),
    this.rowid = const Value.absent(),
  }) : id = Value(id),
       mode = Value(mode),
       createdAt = Value(createdAt),
       updatedAt = Value(updatedAt);
  static Insertable<LocalChatThreadV2> custom({
    Expression<String>? id,
    Expression<String>? title,
    Expression<String>? mode,
    Expression<String>? environmentId,
    Expression<String>? poolTag,
    Expression<String>? model,
    Expression<String>? providerId,
    Expression<String>? systemPrompt,
    Expression<String>? projectId,
    Expression<String>? workdir,
    Expression<String>? autoApprove,
    Expression<String>? runtimeEnvMode,
    Expression<String>? backend,
    Expression<bool>? pinned,
    Expression<bool>? archived,
    Expression<DateTime>? createdAt,
    Expression<DateTime>? updatedAt,
    Expression<int>? remoteUpdatedAtUs,
    Expression<String>? ownerKey,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (title != null) 'title': title,
      if (mode != null) 'mode': mode,
      if (environmentId != null) 'environment_id': environmentId,
      if (poolTag != null) 'pool_tag': poolTag,
      if (model != null) 'model': model,
      if (providerId != null) 'provider_id': providerId,
      if (systemPrompt != null) 'system_prompt': systemPrompt,
      if (projectId != null) 'project_id': projectId,
      if (workdir != null) 'workdir': workdir,
      if (autoApprove != null) 'auto_approve': autoApprove,
      if (runtimeEnvMode != null) 'runtime_env_mode': runtimeEnvMode,
      if (backend != null) 'backend': backend,
      if (pinned != null) 'pinned': pinned,
      if (archived != null) 'archived': archived,
      if (createdAt != null) 'created_at': createdAt,
      if (updatedAt != null) 'updated_at': updatedAt,
      if (remoteUpdatedAtUs != null) 'remote_updated_at_us': remoteUpdatedAtUs,
      if (ownerKey != null) 'owner_key': ownerKey,
      if (rowid != null) 'rowid': rowid,
    });
  }

  ChatThreadsV2Companion copyWith({
    Value<String>? id,
    Value<String>? title,
    Value<String>? mode,
    Value<String?>? environmentId,
    Value<String?>? poolTag,
    Value<String?>? model,
    Value<String?>? providerId,
    Value<String?>? systemPrompt,
    Value<String?>? projectId,
    Value<String?>? workdir,
    Value<String>? autoApprove,
    Value<String>? runtimeEnvMode,
    Value<String>? backend,
    Value<bool>? pinned,
    Value<bool>? archived,
    Value<DateTime>? createdAt,
    Value<DateTime>? updatedAt,
    Value<int?>? remoteUpdatedAtUs,
    Value<String>? ownerKey,
    Value<int>? rowid,
  }) {
    return ChatThreadsV2Companion(
      id: id ?? this.id,
      title: title ?? this.title,
      mode: mode ?? this.mode,
      environmentId: environmentId ?? this.environmentId,
      poolTag: poolTag ?? this.poolTag,
      model: model ?? this.model,
      providerId: providerId ?? this.providerId,
      systemPrompt: systemPrompt ?? this.systemPrompt,
      projectId: projectId ?? this.projectId,
      workdir: workdir ?? this.workdir,
      autoApprove: autoApprove ?? this.autoApprove,
      runtimeEnvMode: runtimeEnvMode ?? this.runtimeEnvMode,
      backend: backend ?? this.backend,
      pinned: pinned ?? this.pinned,
      archived: archived ?? this.archived,
      createdAt: createdAt ?? this.createdAt,
      updatedAt: updatedAt ?? this.updatedAt,
      remoteUpdatedAtUs: remoteUpdatedAtUs ?? this.remoteUpdatedAtUs,
      ownerKey: ownerKey ?? this.ownerKey,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<String>(id.value);
    }
    if (title.present) {
      map['title'] = Variable<String>(title.value);
    }
    if (mode.present) {
      map['mode'] = Variable<String>(mode.value);
    }
    if (environmentId.present) {
      map['environment_id'] = Variable<String>(environmentId.value);
    }
    if (poolTag.present) {
      map['pool_tag'] = Variable<String>(poolTag.value);
    }
    if (model.present) {
      map['model'] = Variable<String>(model.value);
    }
    if (providerId.present) {
      map['provider_id'] = Variable<String>(providerId.value);
    }
    if (systemPrompt.present) {
      map['system_prompt'] = Variable<String>(systemPrompt.value);
    }
    if (projectId.present) {
      map['project_id'] = Variable<String>(projectId.value);
    }
    if (workdir.present) {
      map['workdir'] = Variable<String>(workdir.value);
    }
    if (autoApprove.present) {
      map['auto_approve'] = Variable<String>(autoApprove.value);
    }
    if (runtimeEnvMode.present) {
      map['runtime_env_mode'] = Variable<String>(runtimeEnvMode.value);
    }
    if (backend.present) {
      map['backend'] = Variable<String>(backend.value);
    }
    if (pinned.present) {
      map['pinned'] = Variable<bool>(pinned.value);
    }
    if (archived.present) {
      map['archived'] = Variable<bool>(archived.value);
    }
    if (createdAt.present) {
      map['created_at'] = Variable<DateTime>(createdAt.value);
    }
    if (updatedAt.present) {
      map['updated_at'] = Variable<DateTime>(updatedAt.value);
    }
    if (remoteUpdatedAtUs.present) {
      map['remote_updated_at_us'] = Variable<int>(remoteUpdatedAtUs.value);
    }
    if (ownerKey.present) {
      map['owner_key'] = Variable<String>(ownerKey.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('ChatThreadsV2Companion(')
          ..write('id: $id, ')
          ..write('title: $title, ')
          ..write('mode: $mode, ')
          ..write('environmentId: $environmentId, ')
          ..write('poolTag: $poolTag, ')
          ..write('model: $model, ')
          ..write('providerId: $providerId, ')
          ..write('systemPrompt: $systemPrompt, ')
          ..write('projectId: $projectId, ')
          ..write('workdir: $workdir, ')
          ..write('autoApprove: $autoApprove, ')
          ..write('runtimeEnvMode: $runtimeEnvMode, ')
          ..write('backend: $backend, ')
          ..write('pinned: $pinned, ')
          ..write('archived: $archived, ')
          ..write('createdAt: $createdAt, ')
          ..write('updatedAt: $updatedAt, ')
          ..write('remoteUpdatedAtUs: $remoteUpdatedAtUs, ')
          ..write('ownerKey: $ownerKey, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $ChatMessagesV2Table extends ChatMessagesV2
    with TableInfo<$ChatMessagesV2Table, LocalChatMessageV2> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $ChatMessagesV2Table(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<String> id = GeneratedColumn<String>(
    'id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _threadIdMeta = const VerificationMeta(
    'threadId',
  );
  @override
  late final GeneratedColumn<String> threadId = GeneratedColumn<String>(
    'thread_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _roleMeta = const VerificationMeta('role');
  @override
  late final GeneratedColumn<String> role = GeneratedColumn<String>(
    'role',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _statusMeta = const VerificationMeta('status');
  @override
  late final GeneratedColumn<String> status = GeneratedColumn<String>(
    'status',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _sessionIdMeta = const VerificationMeta(
    'sessionId',
  );
  @override
  late final GeneratedColumn<String> sessionId = GeneratedColumn<String>(
    'session_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _stopReasonMeta = const VerificationMeta(
    'stopReason',
  );
  @override
  late final GeneratedColumn<String> stopReason = GeneratedColumn<String>(
    'stop_reason',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _modelMeta = const VerificationMeta('model');
  @override
  late final GeneratedColumn<String> model = GeneratedColumn<String>(
    'model',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _inputTokensMeta = const VerificationMeta(
    'inputTokens',
  );
  @override
  late final GeneratedColumn<int> inputTokens = GeneratedColumn<int>(
    'input_tokens',
    aliasedName,
    true,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _outputTokensMeta = const VerificationMeta(
    'outputTokens',
  );
  @override
  late final GeneratedColumn<int> outputTokens = GeneratedColumn<int>(
    'output_tokens',
    aliasedName,
    true,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _seqMeta = const VerificationMeta('seq');
  @override
  late final GeneratedColumn<int> seq = GeneratedColumn<int>(
    'seq',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _errorMessageMeta = const VerificationMeta(
    'errorMessage',
  );
  @override
  late final GeneratedColumn<String> errorMessage = GeneratedColumn<String>(
    'error_message',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _createdAtMeta = const VerificationMeta(
    'createdAt',
  );
  @override
  late final GeneratedColumn<DateTime> createdAt = GeneratedColumn<DateTime>(
    'created_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _completedAtMeta = const VerificationMeta(
    'completedAt',
  );
  @override
  late final GeneratedColumn<DateTime> completedAt = GeneratedColumn<DateTime>(
    'completed_at',
    aliasedName,
    true,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _ownerKeyMeta = const VerificationMeta(
    'ownerKey',
  );
  @override
  late final GeneratedColumn<String> ownerKey = GeneratedColumn<String>(
    'owner_key',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant(''),
  );
  @override
  List<GeneratedColumn> get $columns => [
    id,
    threadId,
    role,
    status,
    sessionId,
    stopReason,
    model,
    inputTokens,
    outputTokens,
    seq,
    errorMessage,
    createdAt,
    completedAt,
    ownerKey,
  ];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'chat_messages_v2';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalChatMessageV2> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    } else if (isInserting) {
      context.missing(_idMeta);
    }
    if (data.containsKey('thread_id')) {
      context.handle(
        _threadIdMeta,
        threadId.isAcceptableOrUnknown(data['thread_id']!, _threadIdMeta),
      );
    } else if (isInserting) {
      context.missing(_threadIdMeta);
    }
    if (data.containsKey('role')) {
      context.handle(
        _roleMeta,
        role.isAcceptableOrUnknown(data['role']!, _roleMeta),
      );
    } else if (isInserting) {
      context.missing(_roleMeta);
    }
    if (data.containsKey('status')) {
      context.handle(
        _statusMeta,
        status.isAcceptableOrUnknown(data['status']!, _statusMeta),
      );
    } else if (isInserting) {
      context.missing(_statusMeta);
    }
    if (data.containsKey('session_id')) {
      context.handle(
        _sessionIdMeta,
        sessionId.isAcceptableOrUnknown(data['session_id']!, _sessionIdMeta),
      );
    }
    if (data.containsKey('stop_reason')) {
      context.handle(
        _stopReasonMeta,
        stopReason.isAcceptableOrUnknown(data['stop_reason']!, _stopReasonMeta),
      );
    }
    if (data.containsKey('model')) {
      context.handle(
        _modelMeta,
        model.isAcceptableOrUnknown(data['model']!, _modelMeta),
      );
    }
    if (data.containsKey('input_tokens')) {
      context.handle(
        _inputTokensMeta,
        inputTokens.isAcceptableOrUnknown(
          data['input_tokens']!,
          _inputTokensMeta,
        ),
      );
    }
    if (data.containsKey('output_tokens')) {
      context.handle(
        _outputTokensMeta,
        outputTokens.isAcceptableOrUnknown(
          data['output_tokens']!,
          _outputTokensMeta,
        ),
      );
    }
    if (data.containsKey('seq')) {
      context.handle(
        _seqMeta,
        seq.isAcceptableOrUnknown(data['seq']!, _seqMeta),
      );
    } else if (isInserting) {
      context.missing(_seqMeta);
    }
    if (data.containsKey('error_message')) {
      context.handle(
        _errorMessageMeta,
        errorMessage.isAcceptableOrUnknown(
          data['error_message']!,
          _errorMessageMeta,
        ),
      );
    }
    if (data.containsKey('created_at')) {
      context.handle(
        _createdAtMeta,
        createdAt.isAcceptableOrUnknown(data['created_at']!, _createdAtMeta),
      );
    } else if (isInserting) {
      context.missing(_createdAtMeta);
    }
    if (data.containsKey('completed_at')) {
      context.handle(
        _completedAtMeta,
        completedAt.isAcceptableOrUnknown(
          data['completed_at']!,
          _completedAtMeta,
        ),
      );
    }
    if (data.containsKey('owner_key')) {
      context.handle(
        _ownerKeyMeta,
        ownerKey.isAcceptableOrUnknown(data['owner_key']!, _ownerKeyMeta),
      );
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalChatMessageV2 map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalChatMessageV2(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}id'],
      )!,
      threadId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}thread_id'],
      )!,
      role: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}role'],
      )!,
      status: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}status'],
      )!,
      sessionId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}session_id'],
      ),
      stopReason: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}stop_reason'],
      ),
      model: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}model'],
      ),
      inputTokens: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}input_tokens'],
      ),
      outputTokens: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}output_tokens'],
      ),
      seq: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}seq'],
      )!,
      errorMessage: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}error_message'],
      ),
      createdAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}created_at'],
      )!,
      completedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}completed_at'],
      ),
      ownerKey: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}owner_key'],
      )!,
    );
  }

  @override
  $ChatMessagesV2Table createAlias(String alias) {
    return $ChatMessagesV2Table(attachedDatabase, alias);
  }
}

class LocalChatMessageV2 extends DataClass
    implements Insertable<LocalChatMessageV2> {
  /// = SDK frame uuid 或客户端生成的 ULID（user message）
  final String id;
  final String threadId;

  /// 'user' | 'assistant' | 'tool_result' | 'system'
  final String role;

  /// 'pending' | 'streaming' | 'completed' | 'failed' | 'cancelled'
  final String status;

  /// brain session this message belongs to; user 消息 mid-session 转新 session 时也会更新
  final String? sessionId;

  /// end_turn | tool_use | max_tokens | stop_sequence | error
  final String? stopReason;
  final String? model;
  final int? inputTokens;
  final int? outputTokens;

  /// 同 thread 内顺序，用于排序 + cursor pagination
  final int seq;
  final String? errorMessage;
  final DateTime createdAt;
  final DateTime? completedAt;

  /// 见 ChatThreadsV2.ownerKey —— 环境 × 账号隔离键，查询必填过滤。
  final String ownerKey;
  const LocalChatMessageV2({
    required this.id,
    required this.threadId,
    required this.role,
    required this.status,
    this.sessionId,
    this.stopReason,
    this.model,
    this.inputTokens,
    this.outputTokens,
    required this.seq,
    this.errorMessage,
    required this.createdAt,
    this.completedAt,
    required this.ownerKey,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<String>(id);
    map['thread_id'] = Variable<String>(threadId);
    map['role'] = Variable<String>(role);
    map['status'] = Variable<String>(status);
    if (!nullToAbsent || sessionId != null) {
      map['session_id'] = Variable<String>(sessionId);
    }
    if (!nullToAbsent || stopReason != null) {
      map['stop_reason'] = Variable<String>(stopReason);
    }
    if (!nullToAbsent || model != null) {
      map['model'] = Variable<String>(model);
    }
    if (!nullToAbsent || inputTokens != null) {
      map['input_tokens'] = Variable<int>(inputTokens);
    }
    if (!nullToAbsent || outputTokens != null) {
      map['output_tokens'] = Variable<int>(outputTokens);
    }
    map['seq'] = Variable<int>(seq);
    if (!nullToAbsent || errorMessage != null) {
      map['error_message'] = Variable<String>(errorMessage);
    }
    map['created_at'] = Variable<DateTime>(createdAt);
    if (!nullToAbsent || completedAt != null) {
      map['completed_at'] = Variable<DateTime>(completedAt);
    }
    map['owner_key'] = Variable<String>(ownerKey);
    return map;
  }

  ChatMessagesV2Companion toCompanion(bool nullToAbsent) {
    return ChatMessagesV2Companion(
      id: Value(id),
      threadId: Value(threadId),
      role: Value(role),
      status: Value(status),
      sessionId: sessionId == null && nullToAbsent
          ? const Value.absent()
          : Value(sessionId),
      stopReason: stopReason == null && nullToAbsent
          ? const Value.absent()
          : Value(stopReason),
      model: model == null && nullToAbsent
          ? const Value.absent()
          : Value(model),
      inputTokens: inputTokens == null && nullToAbsent
          ? const Value.absent()
          : Value(inputTokens),
      outputTokens: outputTokens == null && nullToAbsent
          ? const Value.absent()
          : Value(outputTokens),
      seq: Value(seq),
      errorMessage: errorMessage == null && nullToAbsent
          ? const Value.absent()
          : Value(errorMessage),
      createdAt: Value(createdAt),
      completedAt: completedAt == null && nullToAbsent
          ? const Value.absent()
          : Value(completedAt),
      ownerKey: Value(ownerKey),
    );
  }

  factory LocalChatMessageV2.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalChatMessageV2(
      id: serializer.fromJson<String>(json['id']),
      threadId: serializer.fromJson<String>(json['threadId']),
      role: serializer.fromJson<String>(json['role']),
      status: serializer.fromJson<String>(json['status']),
      sessionId: serializer.fromJson<String?>(json['sessionId']),
      stopReason: serializer.fromJson<String?>(json['stopReason']),
      model: serializer.fromJson<String?>(json['model']),
      inputTokens: serializer.fromJson<int?>(json['inputTokens']),
      outputTokens: serializer.fromJson<int?>(json['outputTokens']),
      seq: serializer.fromJson<int>(json['seq']),
      errorMessage: serializer.fromJson<String?>(json['errorMessage']),
      createdAt: serializer.fromJson<DateTime>(json['createdAt']),
      completedAt: serializer.fromJson<DateTime?>(json['completedAt']),
      ownerKey: serializer.fromJson<String>(json['ownerKey']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<String>(id),
      'threadId': serializer.toJson<String>(threadId),
      'role': serializer.toJson<String>(role),
      'status': serializer.toJson<String>(status),
      'sessionId': serializer.toJson<String?>(sessionId),
      'stopReason': serializer.toJson<String?>(stopReason),
      'model': serializer.toJson<String?>(model),
      'inputTokens': serializer.toJson<int?>(inputTokens),
      'outputTokens': serializer.toJson<int?>(outputTokens),
      'seq': serializer.toJson<int>(seq),
      'errorMessage': serializer.toJson<String?>(errorMessage),
      'createdAt': serializer.toJson<DateTime>(createdAt),
      'completedAt': serializer.toJson<DateTime?>(completedAt),
      'ownerKey': serializer.toJson<String>(ownerKey),
    };
  }

  LocalChatMessageV2 copyWith({
    String? id,
    String? threadId,
    String? role,
    String? status,
    Value<String?> sessionId = const Value.absent(),
    Value<String?> stopReason = const Value.absent(),
    Value<String?> model = const Value.absent(),
    Value<int?> inputTokens = const Value.absent(),
    Value<int?> outputTokens = const Value.absent(),
    int? seq,
    Value<String?> errorMessage = const Value.absent(),
    DateTime? createdAt,
    Value<DateTime?> completedAt = const Value.absent(),
    String? ownerKey,
  }) => LocalChatMessageV2(
    id: id ?? this.id,
    threadId: threadId ?? this.threadId,
    role: role ?? this.role,
    status: status ?? this.status,
    sessionId: sessionId.present ? sessionId.value : this.sessionId,
    stopReason: stopReason.present ? stopReason.value : this.stopReason,
    model: model.present ? model.value : this.model,
    inputTokens: inputTokens.present ? inputTokens.value : this.inputTokens,
    outputTokens: outputTokens.present ? outputTokens.value : this.outputTokens,
    seq: seq ?? this.seq,
    errorMessage: errorMessage.present ? errorMessage.value : this.errorMessage,
    createdAt: createdAt ?? this.createdAt,
    completedAt: completedAt.present ? completedAt.value : this.completedAt,
    ownerKey: ownerKey ?? this.ownerKey,
  );
  LocalChatMessageV2 copyWithCompanion(ChatMessagesV2Companion data) {
    return LocalChatMessageV2(
      id: data.id.present ? data.id.value : this.id,
      threadId: data.threadId.present ? data.threadId.value : this.threadId,
      role: data.role.present ? data.role.value : this.role,
      status: data.status.present ? data.status.value : this.status,
      sessionId: data.sessionId.present ? data.sessionId.value : this.sessionId,
      stopReason: data.stopReason.present
          ? data.stopReason.value
          : this.stopReason,
      model: data.model.present ? data.model.value : this.model,
      inputTokens: data.inputTokens.present
          ? data.inputTokens.value
          : this.inputTokens,
      outputTokens: data.outputTokens.present
          ? data.outputTokens.value
          : this.outputTokens,
      seq: data.seq.present ? data.seq.value : this.seq,
      errorMessage: data.errorMessage.present
          ? data.errorMessage.value
          : this.errorMessage,
      createdAt: data.createdAt.present ? data.createdAt.value : this.createdAt,
      completedAt: data.completedAt.present
          ? data.completedAt.value
          : this.completedAt,
      ownerKey: data.ownerKey.present ? data.ownerKey.value : this.ownerKey,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalChatMessageV2(')
          ..write('id: $id, ')
          ..write('threadId: $threadId, ')
          ..write('role: $role, ')
          ..write('status: $status, ')
          ..write('sessionId: $sessionId, ')
          ..write('stopReason: $stopReason, ')
          ..write('model: $model, ')
          ..write('inputTokens: $inputTokens, ')
          ..write('outputTokens: $outputTokens, ')
          ..write('seq: $seq, ')
          ..write('errorMessage: $errorMessage, ')
          ..write('createdAt: $createdAt, ')
          ..write('completedAt: $completedAt, ')
          ..write('ownerKey: $ownerKey')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(
    id,
    threadId,
    role,
    status,
    sessionId,
    stopReason,
    model,
    inputTokens,
    outputTokens,
    seq,
    errorMessage,
    createdAt,
    completedAt,
    ownerKey,
  );
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalChatMessageV2 &&
          other.id == this.id &&
          other.threadId == this.threadId &&
          other.role == this.role &&
          other.status == this.status &&
          other.sessionId == this.sessionId &&
          other.stopReason == this.stopReason &&
          other.model == this.model &&
          other.inputTokens == this.inputTokens &&
          other.outputTokens == this.outputTokens &&
          other.seq == this.seq &&
          other.errorMessage == this.errorMessage &&
          other.createdAt == this.createdAt &&
          other.completedAt == this.completedAt &&
          other.ownerKey == this.ownerKey);
}

class ChatMessagesV2Companion extends UpdateCompanion<LocalChatMessageV2> {
  final Value<String> id;
  final Value<String> threadId;
  final Value<String> role;
  final Value<String> status;
  final Value<String?> sessionId;
  final Value<String?> stopReason;
  final Value<String?> model;
  final Value<int?> inputTokens;
  final Value<int?> outputTokens;
  final Value<int> seq;
  final Value<String?> errorMessage;
  final Value<DateTime> createdAt;
  final Value<DateTime?> completedAt;
  final Value<String> ownerKey;
  final Value<int> rowid;
  const ChatMessagesV2Companion({
    this.id = const Value.absent(),
    this.threadId = const Value.absent(),
    this.role = const Value.absent(),
    this.status = const Value.absent(),
    this.sessionId = const Value.absent(),
    this.stopReason = const Value.absent(),
    this.model = const Value.absent(),
    this.inputTokens = const Value.absent(),
    this.outputTokens = const Value.absent(),
    this.seq = const Value.absent(),
    this.errorMessage = const Value.absent(),
    this.createdAt = const Value.absent(),
    this.completedAt = const Value.absent(),
    this.ownerKey = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  ChatMessagesV2Companion.insert({
    required String id,
    required String threadId,
    required String role,
    required String status,
    this.sessionId = const Value.absent(),
    this.stopReason = const Value.absent(),
    this.model = const Value.absent(),
    this.inputTokens = const Value.absent(),
    this.outputTokens = const Value.absent(),
    required int seq,
    this.errorMessage = const Value.absent(),
    required DateTime createdAt,
    this.completedAt = const Value.absent(),
    this.ownerKey = const Value.absent(),
    this.rowid = const Value.absent(),
  }) : id = Value(id),
       threadId = Value(threadId),
       role = Value(role),
       status = Value(status),
       seq = Value(seq),
       createdAt = Value(createdAt);
  static Insertable<LocalChatMessageV2> custom({
    Expression<String>? id,
    Expression<String>? threadId,
    Expression<String>? role,
    Expression<String>? status,
    Expression<String>? sessionId,
    Expression<String>? stopReason,
    Expression<String>? model,
    Expression<int>? inputTokens,
    Expression<int>? outputTokens,
    Expression<int>? seq,
    Expression<String>? errorMessage,
    Expression<DateTime>? createdAt,
    Expression<DateTime>? completedAt,
    Expression<String>? ownerKey,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (threadId != null) 'thread_id': threadId,
      if (role != null) 'role': role,
      if (status != null) 'status': status,
      if (sessionId != null) 'session_id': sessionId,
      if (stopReason != null) 'stop_reason': stopReason,
      if (model != null) 'model': model,
      if (inputTokens != null) 'input_tokens': inputTokens,
      if (outputTokens != null) 'output_tokens': outputTokens,
      if (seq != null) 'seq': seq,
      if (errorMessage != null) 'error_message': errorMessage,
      if (createdAt != null) 'created_at': createdAt,
      if (completedAt != null) 'completed_at': completedAt,
      if (ownerKey != null) 'owner_key': ownerKey,
      if (rowid != null) 'rowid': rowid,
    });
  }

  ChatMessagesV2Companion copyWith({
    Value<String>? id,
    Value<String>? threadId,
    Value<String>? role,
    Value<String>? status,
    Value<String?>? sessionId,
    Value<String?>? stopReason,
    Value<String?>? model,
    Value<int?>? inputTokens,
    Value<int?>? outputTokens,
    Value<int>? seq,
    Value<String?>? errorMessage,
    Value<DateTime>? createdAt,
    Value<DateTime?>? completedAt,
    Value<String>? ownerKey,
    Value<int>? rowid,
  }) {
    return ChatMessagesV2Companion(
      id: id ?? this.id,
      threadId: threadId ?? this.threadId,
      role: role ?? this.role,
      status: status ?? this.status,
      sessionId: sessionId ?? this.sessionId,
      stopReason: stopReason ?? this.stopReason,
      model: model ?? this.model,
      inputTokens: inputTokens ?? this.inputTokens,
      outputTokens: outputTokens ?? this.outputTokens,
      seq: seq ?? this.seq,
      errorMessage: errorMessage ?? this.errorMessage,
      createdAt: createdAt ?? this.createdAt,
      completedAt: completedAt ?? this.completedAt,
      ownerKey: ownerKey ?? this.ownerKey,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<String>(id.value);
    }
    if (threadId.present) {
      map['thread_id'] = Variable<String>(threadId.value);
    }
    if (role.present) {
      map['role'] = Variable<String>(role.value);
    }
    if (status.present) {
      map['status'] = Variable<String>(status.value);
    }
    if (sessionId.present) {
      map['session_id'] = Variable<String>(sessionId.value);
    }
    if (stopReason.present) {
      map['stop_reason'] = Variable<String>(stopReason.value);
    }
    if (model.present) {
      map['model'] = Variable<String>(model.value);
    }
    if (inputTokens.present) {
      map['input_tokens'] = Variable<int>(inputTokens.value);
    }
    if (outputTokens.present) {
      map['output_tokens'] = Variable<int>(outputTokens.value);
    }
    if (seq.present) {
      map['seq'] = Variable<int>(seq.value);
    }
    if (errorMessage.present) {
      map['error_message'] = Variable<String>(errorMessage.value);
    }
    if (createdAt.present) {
      map['created_at'] = Variable<DateTime>(createdAt.value);
    }
    if (completedAt.present) {
      map['completed_at'] = Variable<DateTime>(completedAt.value);
    }
    if (ownerKey.present) {
      map['owner_key'] = Variable<String>(ownerKey.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('ChatMessagesV2Companion(')
          ..write('id: $id, ')
          ..write('threadId: $threadId, ')
          ..write('role: $role, ')
          ..write('status: $status, ')
          ..write('sessionId: $sessionId, ')
          ..write('stopReason: $stopReason, ')
          ..write('model: $model, ')
          ..write('inputTokens: $inputTokens, ')
          ..write('outputTokens: $outputTokens, ')
          ..write('seq: $seq, ')
          ..write('errorMessage: $errorMessage, ')
          ..write('createdAt: $createdAt, ')
          ..write('completedAt: $completedAt, ')
          ..write('ownerKey: $ownerKey, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $ChatContentBlocksTable extends ChatContentBlocks
    with TableInfo<$ChatContentBlocksTable, LocalChatContentBlock> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $ChatContentBlocksTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<String> id = GeneratedColumn<String>(
    'id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _messageIdMeta = const VerificationMeta(
    'messageId',
  );
  @override
  late final GeneratedColumn<String> messageId = GeneratedColumn<String>(
    'message_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _blockIndexMeta = const VerificationMeta(
    'blockIndex',
  );
  @override
  late final GeneratedColumn<int> blockIndex = GeneratedColumn<int>(
    'block_index',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _typeMeta = const VerificationMeta('type');
  @override
  late final GeneratedColumn<String> type = GeneratedColumn<String>(
    'type',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _textContentMeta = const VerificationMeta(
    'textContent',
  );
  @override
  late final GeneratedColumn<String> textContent = GeneratedColumn<String>(
    'text_content',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _toolUseIdMeta = const VerificationMeta(
    'toolUseId',
  );
  @override
  late final GeneratedColumn<String> toolUseId = GeneratedColumn<String>(
    'tool_use_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _toolUseNameMeta = const VerificationMeta(
    'toolUseName',
  );
  @override
  late final GeneratedColumn<String> toolUseName = GeneratedColumn<String>(
    'tool_use_name',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _toolUseInputJsonMeta = const VerificationMeta(
    'toolUseInputJson',
  );
  @override
  late final GeneratedColumn<String> toolUseInputJson = GeneratedColumn<String>(
    'tool_use_input_json',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _toolResultIdMeta = const VerificationMeta(
    'toolResultId',
  );
  @override
  late final GeneratedColumn<String> toolResultId = GeneratedColumn<String>(
    'tool_result_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _toolResultIsErrorMeta = const VerificationMeta(
    'toolResultIsError',
  );
  @override
  late final GeneratedColumn<bool> toolResultIsError = GeneratedColumn<bool>(
    'tool_result_is_error',
    aliasedName,
    true,
    type: DriftSqlType.bool,
    requiredDuringInsert: false,
    defaultConstraints: GeneratedColumn.constraintIsAlways(
      'CHECK ("tool_result_is_error" IN (0, 1))',
    ),
  );
  static const VerificationMeta _toolResultContentJsonMeta =
      const VerificationMeta('toolResultContentJson');
  @override
  late final GeneratedColumn<String> toolResultContentJson =
      GeneratedColumn<String>(
        'tool_result_content_json',
        aliasedName,
        true,
        type: DriftSqlType.string,
        requiredDuringInsert: false,
      );
  static const VerificationMeta _imageMimeTypeMeta = const VerificationMeta(
    'imageMimeType',
  );
  @override
  late final GeneratedColumn<String> imageMimeType = GeneratedColumn<String>(
    'image_mime_type',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _imageDataMeta = const VerificationMeta(
    'imageData',
  );
  @override
  late final GeneratedColumn<String> imageData = GeneratedColumn<String>(
    'image_data',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _stateMeta = const VerificationMeta('state');
  @override
  late final GeneratedColumn<String> state = GeneratedColumn<String>(
    'state',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant('closed'),
  );
  static const VerificationMeta _createdAtMeta = const VerificationMeta(
    'createdAt',
  );
  @override
  late final GeneratedColumn<DateTime> createdAt = GeneratedColumn<DateTime>(
    'created_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _updatedAtMeta = const VerificationMeta(
    'updatedAt',
  );
  @override
  late final GeneratedColumn<DateTime> updatedAt = GeneratedColumn<DateTime>(
    'updated_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _ownerKeyMeta = const VerificationMeta(
    'ownerKey',
  );
  @override
  late final GeneratedColumn<String> ownerKey = GeneratedColumn<String>(
    'owner_key',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant(''),
  );
  @override
  List<GeneratedColumn> get $columns => [
    id,
    messageId,
    blockIndex,
    type,
    textContent,
    toolUseId,
    toolUseName,
    toolUseInputJson,
    toolResultId,
    toolResultIsError,
    toolResultContentJson,
    imageMimeType,
    imageData,
    state,
    createdAt,
    updatedAt,
    ownerKey,
  ];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'chat_content_blocks';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalChatContentBlock> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    } else if (isInserting) {
      context.missing(_idMeta);
    }
    if (data.containsKey('message_id')) {
      context.handle(
        _messageIdMeta,
        messageId.isAcceptableOrUnknown(data['message_id']!, _messageIdMeta),
      );
    } else if (isInserting) {
      context.missing(_messageIdMeta);
    }
    if (data.containsKey('block_index')) {
      context.handle(
        _blockIndexMeta,
        blockIndex.isAcceptableOrUnknown(data['block_index']!, _blockIndexMeta),
      );
    } else if (isInserting) {
      context.missing(_blockIndexMeta);
    }
    if (data.containsKey('type')) {
      context.handle(
        _typeMeta,
        type.isAcceptableOrUnknown(data['type']!, _typeMeta),
      );
    } else if (isInserting) {
      context.missing(_typeMeta);
    }
    if (data.containsKey('text_content')) {
      context.handle(
        _textContentMeta,
        textContent.isAcceptableOrUnknown(
          data['text_content']!,
          _textContentMeta,
        ),
      );
    }
    if (data.containsKey('tool_use_id')) {
      context.handle(
        _toolUseIdMeta,
        toolUseId.isAcceptableOrUnknown(data['tool_use_id']!, _toolUseIdMeta),
      );
    }
    if (data.containsKey('tool_use_name')) {
      context.handle(
        _toolUseNameMeta,
        toolUseName.isAcceptableOrUnknown(
          data['tool_use_name']!,
          _toolUseNameMeta,
        ),
      );
    }
    if (data.containsKey('tool_use_input_json')) {
      context.handle(
        _toolUseInputJsonMeta,
        toolUseInputJson.isAcceptableOrUnknown(
          data['tool_use_input_json']!,
          _toolUseInputJsonMeta,
        ),
      );
    }
    if (data.containsKey('tool_result_id')) {
      context.handle(
        _toolResultIdMeta,
        toolResultId.isAcceptableOrUnknown(
          data['tool_result_id']!,
          _toolResultIdMeta,
        ),
      );
    }
    if (data.containsKey('tool_result_is_error')) {
      context.handle(
        _toolResultIsErrorMeta,
        toolResultIsError.isAcceptableOrUnknown(
          data['tool_result_is_error']!,
          _toolResultIsErrorMeta,
        ),
      );
    }
    if (data.containsKey('tool_result_content_json')) {
      context.handle(
        _toolResultContentJsonMeta,
        toolResultContentJson.isAcceptableOrUnknown(
          data['tool_result_content_json']!,
          _toolResultContentJsonMeta,
        ),
      );
    }
    if (data.containsKey('image_mime_type')) {
      context.handle(
        _imageMimeTypeMeta,
        imageMimeType.isAcceptableOrUnknown(
          data['image_mime_type']!,
          _imageMimeTypeMeta,
        ),
      );
    }
    if (data.containsKey('image_data')) {
      context.handle(
        _imageDataMeta,
        imageData.isAcceptableOrUnknown(data['image_data']!, _imageDataMeta),
      );
    }
    if (data.containsKey('state')) {
      context.handle(
        _stateMeta,
        state.isAcceptableOrUnknown(data['state']!, _stateMeta),
      );
    }
    if (data.containsKey('created_at')) {
      context.handle(
        _createdAtMeta,
        createdAt.isAcceptableOrUnknown(data['created_at']!, _createdAtMeta),
      );
    } else if (isInserting) {
      context.missing(_createdAtMeta);
    }
    if (data.containsKey('updated_at')) {
      context.handle(
        _updatedAtMeta,
        updatedAt.isAcceptableOrUnknown(data['updated_at']!, _updatedAtMeta),
      );
    } else if (isInserting) {
      context.missing(_updatedAtMeta);
    }
    if (data.containsKey('owner_key')) {
      context.handle(
        _ownerKeyMeta,
        ownerKey.isAcceptableOrUnknown(data['owner_key']!, _ownerKeyMeta),
      );
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalChatContentBlock map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalChatContentBlock(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}id'],
      )!,
      messageId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}message_id'],
      )!,
      blockIndex: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}block_index'],
      )!,
      type: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}type'],
      )!,
      textContent: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}text_content'],
      ),
      toolUseId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}tool_use_id'],
      ),
      toolUseName: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}tool_use_name'],
      ),
      toolUseInputJson: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}tool_use_input_json'],
      ),
      toolResultId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}tool_result_id'],
      ),
      toolResultIsError: attachedDatabase.typeMapping.read(
        DriftSqlType.bool,
        data['${effectivePrefix}tool_result_is_error'],
      ),
      toolResultContentJson: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}tool_result_content_json'],
      ),
      imageMimeType: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}image_mime_type'],
      ),
      imageData: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}image_data'],
      ),
      state: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}state'],
      )!,
      createdAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}created_at'],
      )!,
      updatedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}updated_at'],
      )!,
      ownerKey: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}owner_key'],
      )!,
    );
  }

  @override
  $ChatContentBlocksTable createAlias(String alias) {
    return $ChatContentBlocksTable(attachedDatabase, alias);
  }
}

class LocalChatContentBlock extends DataClass
    implements Insertable<LocalChatContentBlock> {
  final String id;
  final String messageId;

  /// 0-based 在 message.content 数组里的位置
  final int blockIndex;

  /// 'text' | 'tool_use' | 'tool_result' | 'image'
  final String type;

  /// type=text
  final String? textContent;

  /// type=tool_use
  final String? toolUseId;
  final String? toolUseName;
  final String? toolUseInputJson;

  /// type=tool_result
  final String? toolResultId;
  final bool? toolResultIsError;

  /// JSON 字符串 —— ContentBlock[] 嵌套结构
  final String? toolResultContentJson;

  /// type=image
  final String? imageMimeType;
  final String? imageData;

  /// streaming 时 block 状态：'streaming'（text delta 还在拼）| 'closed'
  final String state;
  final DateTime createdAt;
  final DateTime updatedAt;

  /// 见 ChatThreadsV2.ownerKey —— 环境 × 账号隔离键，查询必填过滤。
  final String ownerKey;
  const LocalChatContentBlock({
    required this.id,
    required this.messageId,
    required this.blockIndex,
    required this.type,
    this.textContent,
    this.toolUseId,
    this.toolUseName,
    this.toolUseInputJson,
    this.toolResultId,
    this.toolResultIsError,
    this.toolResultContentJson,
    this.imageMimeType,
    this.imageData,
    required this.state,
    required this.createdAt,
    required this.updatedAt,
    required this.ownerKey,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<String>(id);
    map['message_id'] = Variable<String>(messageId);
    map['block_index'] = Variable<int>(blockIndex);
    map['type'] = Variable<String>(type);
    if (!nullToAbsent || textContent != null) {
      map['text_content'] = Variable<String>(textContent);
    }
    if (!nullToAbsent || toolUseId != null) {
      map['tool_use_id'] = Variable<String>(toolUseId);
    }
    if (!nullToAbsent || toolUseName != null) {
      map['tool_use_name'] = Variable<String>(toolUseName);
    }
    if (!nullToAbsent || toolUseInputJson != null) {
      map['tool_use_input_json'] = Variable<String>(toolUseInputJson);
    }
    if (!nullToAbsent || toolResultId != null) {
      map['tool_result_id'] = Variable<String>(toolResultId);
    }
    if (!nullToAbsent || toolResultIsError != null) {
      map['tool_result_is_error'] = Variable<bool>(toolResultIsError);
    }
    if (!nullToAbsent || toolResultContentJson != null) {
      map['tool_result_content_json'] = Variable<String>(toolResultContentJson);
    }
    if (!nullToAbsent || imageMimeType != null) {
      map['image_mime_type'] = Variable<String>(imageMimeType);
    }
    if (!nullToAbsent || imageData != null) {
      map['image_data'] = Variable<String>(imageData);
    }
    map['state'] = Variable<String>(state);
    map['created_at'] = Variable<DateTime>(createdAt);
    map['updated_at'] = Variable<DateTime>(updatedAt);
    map['owner_key'] = Variable<String>(ownerKey);
    return map;
  }

  ChatContentBlocksCompanion toCompanion(bool nullToAbsent) {
    return ChatContentBlocksCompanion(
      id: Value(id),
      messageId: Value(messageId),
      blockIndex: Value(blockIndex),
      type: Value(type),
      textContent: textContent == null && nullToAbsent
          ? const Value.absent()
          : Value(textContent),
      toolUseId: toolUseId == null && nullToAbsent
          ? const Value.absent()
          : Value(toolUseId),
      toolUseName: toolUseName == null && nullToAbsent
          ? const Value.absent()
          : Value(toolUseName),
      toolUseInputJson: toolUseInputJson == null && nullToAbsent
          ? const Value.absent()
          : Value(toolUseInputJson),
      toolResultId: toolResultId == null && nullToAbsent
          ? const Value.absent()
          : Value(toolResultId),
      toolResultIsError: toolResultIsError == null && nullToAbsent
          ? const Value.absent()
          : Value(toolResultIsError),
      toolResultContentJson: toolResultContentJson == null && nullToAbsent
          ? const Value.absent()
          : Value(toolResultContentJson),
      imageMimeType: imageMimeType == null && nullToAbsent
          ? const Value.absent()
          : Value(imageMimeType),
      imageData: imageData == null && nullToAbsent
          ? const Value.absent()
          : Value(imageData),
      state: Value(state),
      createdAt: Value(createdAt),
      updatedAt: Value(updatedAt),
      ownerKey: Value(ownerKey),
    );
  }

  factory LocalChatContentBlock.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalChatContentBlock(
      id: serializer.fromJson<String>(json['id']),
      messageId: serializer.fromJson<String>(json['messageId']),
      blockIndex: serializer.fromJson<int>(json['blockIndex']),
      type: serializer.fromJson<String>(json['type']),
      textContent: serializer.fromJson<String?>(json['textContent']),
      toolUseId: serializer.fromJson<String?>(json['toolUseId']),
      toolUseName: serializer.fromJson<String?>(json['toolUseName']),
      toolUseInputJson: serializer.fromJson<String?>(json['toolUseInputJson']),
      toolResultId: serializer.fromJson<String?>(json['toolResultId']),
      toolResultIsError: serializer.fromJson<bool?>(json['toolResultIsError']),
      toolResultContentJson: serializer.fromJson<String?>(
        json['toolResultContentJson'],
      ),
      imageMimeType: serializer.fromJson<String?>(json['imageMimeType']),
      imageData: serializer.fromJson<String?>(json['imageData']),
      state: serializer.fromJson<String>(json['state']),
      createdAt: serializer.fromJson<DateTime>(json['createdAt']),
      updatedAt: serializer.fromJson<DateTime>(json['updatedAt']),
      ownerKey: serializer.fromJson<String>(json['ownerKey']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<String>(id),
      'messageId': serializer.toJson<String>(messageId),
      'blockIndex': serializer.toJson<int>(blockIndex),
      'type': serializer.toJson<String>(type),
      'textContent': serializer.toJson<String?>(textContent),
      'toolUseId': serializer.toJson<String?>(toolUseId),
      'toolUseName': serializer.toJson<String?>(toolUseName),
      'toolUseInputJson': serializer.toJson<String?>(toolUseInputJson),
      'toolResultId': serializer.toJson<String?>(toolResultId),
      'toolResultIsError': serializer.toJson<bool?>(toolResultIsError),
      'toolResultContentJson': serializer.toJson<String?>(
        toolResultContentJson,
      ),
      'imageMimeType': serializer.toJson<String?>(imageMimeType),
      'imageData': serializer.toJson<String?>(imageData),
      'state': serializer.toJson<String>(state),
      'createdAt': serializer.toJson<DateTime>(createdAt),
      'updatedAt': serializer.toJson<DateTime>(updatedAt),
      'ownerKey': serializer.toJson<String>(ownerKey),
    };
  }

  LocalChatContentBlock copyWith({
    String? id,
    String? messageId,
    int? blockIndex,
    String? type,
    Value<String?> textContent = const Value.absent(),
    Value<String?> toolUseId = const Value.absent(),
    Value<String?> toolUseName = const Value.absent(),
    Value<String?> toolUseInputJson = const Value.absent(),
    Value<String?> toolResultId = const Value.absent(),
    Value<bool?> toolResultIsError = const Value.absent(),
    Value<String?> toolResultContentJson = const Value.absent(),
    Value<String?> imageMimeType = const Value.absent(),
    Value<String?> imageData = const Value.absent(),
    String? state,
    DateTime? createdAt,
    DateTime? updatedAt,
    String? ownerKey,
  }) => LocalChatContentBlock(
    id: id ?? this.id,
    messageId: messageId ?? this.messageId,
    blockIndex: blockIndex ?? this.blockIndex,
    type: type ?? this.type,
    textContent: textContent.present ? textContent.value : this.textContent,
    toolUseId: toolUseId.present ? toolUseId.value : this.toolUseId,
    toolUseName: toolUseName.present ? toolUseName.value : this.toolUseName,
    toolUseInputJson: toolUseInputJson.present
        ? toolUseInputJson.value
        : this.toolUseInputJson,
    toolResultId: toolResultId.present ? toolResultId.value : this.toolResultId,
    toolResultIsError: toolResultIsError.present
        ? toolResultIsError.value
        : this.toolResultIsError,
    toolResultContentJson: toolResultContentJson.present
        ? toolResultContentJson.value
        : this.toolResultContentJson,
    imageMimeType: imageMimeType.present
        ? imageMimeType.value
        : this.imageMimeType,
    imageData: imageData.present ? imageData.value : this.imageData,
    state: state ?? this.state,
    createdAt: createdAt ?? this.createdAt,
    updatedAt: updatedAt ?? this.updatedAt,
    ownerKey: ownerKey ?? this.ownerKey,
  );
  LocalChatContentBlock copyWithCompanion(ChatContentBlocksCompanion data) {
    return LocalChatContentBlock(
      id: data.id.present ? data.id.value : this.id,
      messageId: data.messageId.present ? data.messageId.value : this.messageId,
      blockIndex: data.blockIndex.present
          ? data.blockIndex.value
          : this.blockIndex,
      type: data.type.present ? data.type.value : this.type,
      textContent: data.textContent.present
          ? data.textContent.value
          : this.textContent,
      toolUseId: data.toolUseId.present ? data.toolUseId.value : this.toolUseId,
      toolUseName: data.toolUseName.present
          ? data.toolUseName.value
          : this.toolUseName,
      toolUseInputJson: data.toolUseInputJson.present
          ? data.toolUseInputJson.value
          : this.toolUseInputJson,
      toolResultId: data.toolResultId.present
          ? data.toolResultId.value
          : this.toolResultId,
      toolResultIsError: data.toolResultIsError.present
          ? data.toolResultIsError.value
          : this.toolResultIsError,
      toolResultContentJson: data.toolResultContentJson.present
          ? data.toolResultContentJson.value
          : this.toolResultContentJson,
      imageMimeType: data.imageMimeType.present
          ? data.imageMimeType.value
          : this.imageMimeType,
      imageData: data.imageData.present ? data.imageData.value : this.imageData,
      state: data.state.present ? data.state.value : this.state,
      createdAt: data.createdAt.present ? data.createdAt.value : this.createdAt,
      updatedAt: data.updatedAt.present ? data.updatedAt.value : this.updatedAt,
      ownerKey: data.ownerKey.present ? data.ownerKey.value : this.ownerKey,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalChatContentBlock(')
          ..write('id: $id, ')
          ..write('messageId: $messageId, ')
          ..write('blockIndex: $blockIndex, ')
          ..write('type: $type, ')
          ..write('textContent: $textContent, ')
          ..write('toolUseId: $toolUseId, ')
          ..write('toolUseName: $toolUseName, ')
          ..write('toolUseInputJson: $toolUseInputJson, ')
          ..write('toolResultId: $toolResultId, ')
          ..write('toolResultIsError: $toolResultIsError, ')
          ..write('toolResultContentJson: $toolResultContentJson, ')
          ..write('imageMimeType: $imageMimeType, ')
          ..write('imageData: $imageData, ')
          ..write('state: $state, ')
          ..write('createdAt: $createdAt, ')
          ..write('updatedAt: $updatedAt, ')
          ..write('ownerKey: $ownerKey')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(
    id,
    messageId,
    blockIndex,
    type,
    textContent,
    toolUseId,
    toolUseName,
    toolUseInputJson,
    toolResultId,
    toolResultIsError,
    toolResultContentJson,
    imageMimeType,
    imageData,
    state,
    createdAt,
    updatedAt,
    ownerKey,
  );
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalChatContentBlock &&
          other.id == this.id &&
          other.messageId == this.messageId &&
          other.blockIndex == this.blockIndex &&
          other.type == this.type &&
          other.textContent == this.textContent &&
          other.toolUseId == this.toolUseId &&
          other.toolUseName == this.toolUseName &&
          other.toolUseInputJson == this.toolUseInputJson &&
          other.toolResultId == this.toolResultId &&
          other.toolResultIsError == this.toolResultIsError &&
          other.toolResultContentJson == this.toolResultContentJson &&
          other.imageMimeType == this.imageMimeType &&
          other.imageData == this.imageData &&
          other.state == this.state &&
          other.createdAt == this.createdAt &&
          other.updatedAt == this.updatedAt &&
          other.ownerKey == this.ownerKey);
}

class ChatContentBlocksCompanion
    extends UpdateCompanion<LocalChatContentBlock> {
  final Value<String> id;
  final Value<String> messageId;
  final Value<int> blockIndex;
  final Value<String> type;
  final Value<String?> textContent;
  final Value<String?> toolUseId;
  final Value<String?> toolUseName;
  final Value<String?> toolUseInputJson;
  final Value<String?> toolResultId;
  final Value<bool?> toolResultIsError;
  final Value<String?> toolResultContentJson;
  final Value<String?> imageMimeType;
  final Value<String?> imageData;
  final Value<String> state;
  final Value<DateTime> createdAt;
  final Value<DateTime> updatedAt;
  final Value<String> ownerKey;
  final Value<int> rowid;
  const ChatContentBlocksCompanion({
    this.id = const Value.absent(),
    this.messageId = const Value.absent(),
    this.blockIndex = const Value.absent(),
    this.type = const Value.absent(),
    this.textContent = const Value.absent(),
    this.toolUseId = const Value.absent(),
    this.toolUseName = const Value.absent(),
    this.toolUseInputJson = const Value.absent(),
    this.toolResultId = const Value.absent(),
    this.toolResultIsError = const Value.absent(),
    this.toolResultContentJson = const Value.absent(),
    this.imageMimeType = const Value.absent(),
    this.imageData = const Value.absent(),
    this.state = const Value.absent(),
    this.createdAt = const Value.absent(),
    this.updatedAt = const Value.absent(),
    this.ownerKey = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  ChatContentBlocksCompanion.insert({
    required String id,
    required String messageId,
    required int blockIndex,
    required String type,
    this.textContent = const Value.absent(),
    this.toolUseId = const Value.absent(),
    this.toolUseName = const Value.absent(),
    this.toolUseInputJson = const Value.absent(),
    this.toolResultId = const Value.absent(),
    this.toolResultIsError = const Value.absent(),
    this.toolResultContentJson = const Value.absent(),
    this.imageMimeType = const Value.absent(),
    this.imageData = const Value.absent(),
    this.state = const Value.absent(),
    required DateTime createdAt,
    required DateTime updatedAt,
    this.ownerKey = const Value.absent(),
    this.rowid = const Value.absent(),
  }) : id = Value(id),
       messageId = Value(messageId),
       blockIndex = Value(blockIndex),
       type = Value(type),
       createdAt = Value(createdAt),
       updatedAt = Value(updatedAt);
  static Insertable<LocalChatContentBlock> custom({
    Expression<String>? id,
    Expression<String>? messageId,
    Expression<int>? blockIndex,
    Expression<String>? type,
    Expression<String>? textContent,
    Expression<String>? toolUseId,
    Expression<String>? toolUseName,
    Expression<String>? toolUseInputJson,
    Expression<String>? toolResultId,
    Expression<bool>? toolResultIsError,
    Expression<String>? toolResultContentJson,
    Expression<String>? imageMimeType,
    Expression<String>? imageData,
    Expression<String>? state,
    Expression<DateTime>? createdAt,
    Expression<DateTime>? updatedAt,
    Expression<String>? ownerKey,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (messageId != null) 'message_id': messageId,
      if (blockIndex != null) 'block_index': blockIndex,
      if (type != null) 'type': type,
      if (textContent != null) 'text_content': textContent,
      if (toolUseId != null) 'tool_use_id': toolUseId,
      if (toolUseName != null) 'tool_use_name': toolUseName,
      if (toolUseInputJson != null) 'tool_use_input_json': toolUseInputJson,
      if (toolResultId != null) 'tool_result_id': toolResultId,
      if (toolResultIsError != null) 'tool_result_is_error': toolResultIsError,
      if (toolResultContentJson != null)
        'tool_result_content_json': toolResultContentJson,
      if (imageMimeType != null) 'image_mime_type': imageMimeType,
      if (imageData != null) 'image_data': imageData,
      if (state != null) 'state': state,
      if (createdAt != null) 'created_at': createdAt,
      if (updatedAt != null) 'updated_at': updatedAt,
      if (ownerKey != null) 'owner_key': ownerKey,
      if (rowid != null) 'rowid': rowid,
    });
  }

  ChatContentBlocksCompanion copyWith({
    Value<String>? id,
    Value<String>? messageId,
    Value<int>? blockIndex,
    Value<String>? type,
    Value<String?>? textContent,
    Value<String?>? toolUseId,
    Value<String?>? toolUseName,
    Value<String?>? toolUseInputJson,
    Value<String?>? toolResultId,
    Value<bool?>? toolResultIsError,
    Value<String?>? toolResultContentJson,
    Value<String?>? imageMimeType,
    Value<String?>? imageData,
    Value<String>? state,
    Value<DateTime>? createdAt,
    Value<DateTime>? updatedAt,
    Value<String>? ownerKey,
    Value<int>? rowid,
  }) {
    return ChatContentBlocksCompanion(
      id: id ?? this.id,
      messageId: messageId ?? this.messageId,
      blockIndex: blockIndex ?? this.blockIndex,
      type: type ?? this.type,
      textContent: textContent ?? this.textContent,
      toolUseId: toolUseId ?? this.toolUseId,
      toolUseName: toolUseName ?? this.toolUseName,
      toolUseInputJson: toolUseInputJson ?? this.toolUseInputJson,
      toolResultId: toolResultId ?? this.toolResultId,
      toolResultIsError: toolResultIsError ?? this.toolResultIsError,
      toolResultContentJson:
          toolResultContentJson ?? this.toolResultContentJson,
      imageMimeType: imageMimeType ?? this.imageMimeType,
      imageData: imageData ?? this.imageData,
      state: state ?? this.state,
      createdAt: createdAt ?? this.createdAt,
      updatedAt: updatedAt ?? this.updatedAt,
      ownerKey: ownerKey ?? this.ownerKey,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<String>(id.value);
    }
    if (messageId.present) {
      map['message_id'] = Variable<String>(messageId.value);
    }
    if (blockIndex.present) {
      map['block_index'] = Variable<int>(blockIndex.value);
    }
    if (type.present) {
      map['type'] = Variable<String>(type.value);
    }
    if (textContent.present) {
      map['text_content'] = Variable<String>(textContent.value);
    }
    if (toolUseId.present) {
      map['tool_use_id'] = Variable<String>(toolUseId.value);
    }
    if (toolUseName.present) {
      map['tool_use_name'] = Variable<String>(toolUseName.value);
    }
    if (toolUseInputJson.present) {
      map['tool_use_input_json'] = Variable<String>(toolUseInputJson.value);
    }
    if (toolResultId.present) {
      map['tool_result_id'] = Variable<String>(toolResultId.value);
    }
    if (toolResultIsError.present) {
      map['tool_result_is_error'] = Variable<bool>(toolResultIsError.value);
    }
    if (toolResultContentJson.present) {
      map['tool_result_content_json'] = Variable<String>(
        toolResultContentJson.value,
      );
    }
    if (imageMimeType.present) {
      map['image_mime_type'] = Variable<String>(imageMimeType.value);
    }
    if (imageData.present) {
      map['image_data'] = Variable<String>(imageData.value);
    }
    if (state.present) {
      map['state'] = Variable<String>(state.value);
    }
    if (createdAt.present) {
      map['created_at'] = Variable<DateTime>(createdAt.value);
    }
    if (updatedAt.present) {
      map['updated_at'] = Variable<DateTime>(updatedAt.value);
    }
    if (ownerKey.present) {
      map['owner_key'] = Variable<String>(ownerKey.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('ChatContentBlocksCompanion(')
          ..write('id: $id, ')
          ..write('messageId: $messageId, ')
          ..write('blockIndex: $blockIndex, ')
          ..write('type: $type, ')
          ..write('textContent: $textContent, ')
          ..write('toolUseId: $toolUseId, ')
          ..write('toolUseName: $toolUseName, ')
          ..write('toolUseInputJson: $toolUseInputJson, ')
          ..write('toolResultId: $toolResultId, ')
          ..write('toolResultIsError: $toolResultIsError, ')
          ..write('toolResultContentJson: $toolResultContentJson, ')
          ..write('imageMimeType: $imageMimeType, ')
          ..write('imageData: $imageData, ')
          ..write('state: $state, ')
          ..write('createdAt: $createdAt, ')
          ..write('updatedAt: $updatedAt, ')
          ..write('ownerKey: $ownerKey, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $ChatSessionsTable extends ChatSessions
    with TableInfo<$ChatSessionsTable, LocalChatSession> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $ChatSessionsTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _sessionIdMeta = const VerificationMeta(
    'sessionId',
  );
  @override
  late final GeneratedColumn<String> sessionId = GeneratedColumn<String>(
    'session_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _threadIdMeta = const VerificationMeta(
    'threadId',
  );
  @override
  late final GeneratedColumn<String> threadId = GeneratedColumn<String>(
    'thread_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _modeMeta = const VerificationMeta('mode');
  @override
  late final GeneratedColumn<String> mode = GeneratedColumn<String>(
    'mode',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _sessionTokenMeta = const VerificationMeta(
    'sessionToken',
  );
  @override
  late final GeneratedColumn<String> sessionToken = GeneratedColumn<String>(
    'session_token',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _tokenExpiresAtMeta = const VerificationMeta(
    'tokenExpiresAt',
  );
  @override
  late final GeneratedColumn<DateTime> tokenExpiresAt =
      GeneratedColumn<DateTime>(
        'token_expires_at',
        aliasedName,
        false,
        type: DriftSqlType.dateTime,
        requiredDuringInsert: true,
      );
  static const VerificationMeta _lastSeenSeqMeta = const VerificationMeta(
    'lastSeenSeq',
  );
  @override
  late final GeneratedColumn<int> lastSeenSeq = GeneratedColumn<int>(
    'last_seen_seq',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultValue: const Constant(0),
  );
  static const VerificationMeta _statusMeta = const VerificationMeta('status');
  @override
  late final GeneratedColumn<String> status = GeneratedColumn<String>(
    'status',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _createdAtMeta = const VerificationMeta(
    'createdAt',
  );
  @override
  late final GeneratedColumn<DateTime> createdAt = GeneratedColumn<DateTime>(
    'created_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _closedAtMeta = const VerificationMeta(
    'closedAt',
  );
  @override
  late final GeneratedColumn<DateTime> closedAt = GeneratedColumn<DateTime>(
    'closed_at',
    aliasedName,
    true,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _ownerKeyMeta = const VerificationMeta(
    'ownerKey',
  );
  @override
  late final GeneratedColumn<String> ownerKey = GeneratedColumn<String>(
    'owner_key',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant(''),
  );
  @override
  List<GeneratedColumn> get $columns => [
    sessionId,
    threadId,
    mode,
    sessionToken,
    tokenExpiresAt,
    lastSeenSeq,
    status,
    createdAt,
    closedAt,
    ownerKey,
  ];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'chat_sessions';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalChatSession> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('session_id')) {
      context.handle(
        _sessionIdMeta,
        sessionId.isAcceptableOrUnknown(data['session_id']!, _sessionIdMeta),
      );
    } else if (isInserting) {
      context.missing(_sessionIdMeta);
    }
    if (data.containsKey('thread_id')) {
      context.handle(
        _threadIdMeta,
        threadId.isAcceptableOrUnknown(data['thread_id']!, _threadIdMeta),
      );
    } else if (isInserting) {
      context.missing(_threadIdMeta);
    }
    if (data.containsKey('mode')) {
      context.handle(
        _modeMeta,
        mode.isAcceptableOrUnknown(data['mode']!, _modeMeta),
      );
    } else if (isInserting) {
      context.missing(_modeMeta);
    }
    if (data.containsKey('session_token')) {
      context.handle(
        _sessionTokenMeta,
        sessionToken.isAcceptableOrUnknown(
          data['session_token']!,
          _sessionTokenMeta,
        ),
      );
    } else if (isInserting) {
      context.missing(_sessionTokenMeta);
    }
    if (data.containsKey('token_expires_at')) {
      context.handle(
        _tokenExpiresAtMeta,
        tokenExpiresAt.isAcceptableOrUnknown(
          data['token_expires_at']!,
          _tokenExpiresAtMeta,
        ),
      );
    } else if (isInserting) {
      context.missing(_tokenExpiresAtMeta);
    }
    if (data.containsKey('last_seen_seq')) {
      context.handle(
        _lastSeenSeqMeta,
        lastSeenSeq.isAcceptableOrUnknown(
          data['last_seen_seq']!,
          _lastSeenSeqMeta,
        ),
      );
    }
    if (data.containsKey('status')) {
      context.handle(
        _statusMeta,
        status.isAcceptableOrUnknown(data['status']!, _statusMeta),
      );
    } else if (isInserting) {
      context.missing(_statusMeta);
    }
    if (data.containsKey('created_at')) {
      context.handle(
        _createdAtMeta,
        createdAt.isAcceptableOrUnknown(data['created_at']!, _createdAtMeta),
      );
    } else if (isInserting) {
      context.missing(_createdAtMeta);
    }
    if (data.containsKey('closed_at')) {
      context.handle(
        _closedAtMeta,
        closedAt.isAcceptableOrUnknown(data['closed_at']!, _closedAtMeta),
      );
    }
    if (data.containsKey('owner_key')) {
      context.handle(
        _ownerKeyMeta,
        ownerKey.isAcceptableOrUnknown(data['owner_key']!, _ownerKeyMeta),
      );
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {sessionId};
  @override
  LocalChatSession map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalChatSession(
      sessionId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}session_id'],
      )!,
      threadId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}thread_id'],
      )!,
      mode: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}mode'],
      )!,
      sessionToken: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}session_token'],
      )!,
      tokenExpiresAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}token_expires_at'],
      )!,
      lastSeenSeq: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}last_seen_seq'],
      )!,
      status: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}status'],
      )!,
      createdAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}created_at'],
      )!,
      closedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}closed_at'],
      ),
      ownerKey: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}owner_key'],
      )!,
    );
  }

  @override
  $ChatSessionsTable createAlias(String alias) {
    return $ChatSessionsTable(attachedDatabase, alias);
  }
}

class LocalChatSession extends DataClass
    implements Insertable<LocalChatSession> {
  /// brain agent_sessions.session_id（外部 PK）
  final String sessionId;
  final String threadId;

  /// 'chat' | 'agent' | 'task'，跟 thread.mode 一致；冗余便于查询
  final String mode;

  /// 30min session_token；过期前 5min 由 BiuSessionConnection 自动 refresh
  final String sessionToken;
  final DateTime tokenExpiresAt;

  /// 客户端已经 ack 过的最大 stream seq；resume 时给 brain ?since_seq=N
  final int lastSeenSeq;

  /// 'active' | 'completed' | 'failed' | 'cancelled'
  final String status;
  final DateTime createdAt;
  final DateTime? closedAt;

  /// 见 ChatThreadsV2.ownerKey —— 环境 × 账号隔离键，查询必填过滤。
  final String ownerKey;
  const LocalChatSession({
    required this.sessionId,
    required this.threadId,
    required this.mode,
    required this.sessionToken,
    required this.tokenExpiresAt,
    required this.lastSeenSeq,
    required this.status,
    required this.createdAt,
    this.closedAt,
    required this.ownerKey,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['session_id'] = Variable<String>(sessionId);
    map['thread_id'] = Variable<String>(threadId);
    map['mode'] = Variable<String>(mode);
    map['session_token'] = Variable<String>(sessionToken);
    map['token_expires_at'] = Variable<DateTime>(tokenExpiresAt);
    map['last_seen_seq'] = Variable<int>(lastSeenSeq);
    map['status'] = Variable<String>(status);
    map['created_at'] = Variable<DateTime>(createdAt);
    if (!nullToAbsent || closedAt != null) {
      map['closed_at'] = Variable<DateTime>(closedAt);
    }
    map['owner_key'] = Variable<String>(ownerKey);
    return map;
  }

  ChatSessionsCompanion toCompanion(bool nullToAbsent) {
    return ChatSessionsCompanion(
      sessionId: Value(sessionId),
      threadId: Value(threadId),
      mode: Value(mode),
      sessionToken: Value(sessionToken),
      tokenExpiresAt: Value(tokenExpiresAt),
      lastSeenSeq: Value(lastSeenSeq),
      status: Value(status),
      createdAt: Value(createdAt),
      closedAt: closedAt == null && nullToAbsent
          ? const Value.absent()
          : Value(closedAt),
      ownerKey: Value(ownerKey),
    );
  }

  factory LocalChatSession.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalChatSession(
      sessionId: serializer.fromJson<String>(json['sessionId']),
      threadId: serializer.fromJson<String>(json['threadId']),
      mode: serializer.fromJson<String>(json['mode']),
      sessionToken: serializer.fromJson<String>(json['sessionToken']),
      tokenExpiresAt: serializer.fromJson<DateTime>(json['tokenExpiresAt']),
      lastSeenSeq: serializer.fromJson<int>(json['lastSeenSeq']),
      status: serializer.fromJson<String>(json['status']),
      createdAt: serializer.fromJson<DateTime>(json['createdAt']),
      closedAt: serializer.fromJson<DateTime?>(json['closedAt']),
      ownerKey: serializer.fromJson<String>(json['ownerKey']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'sessionId': serializer.toJson<String>(sessionId),
      'threadId': serializer.toJson<String>(threadId),
      'mode': serializer.toJson<String>(mode),
      'sessionToken': serializer.toJson<String>(sessionToken),
      'tokenExpiresAt': serializer.toJson<DateTime>(tokenExpiresAt),
      'lastSeenSeq': serializer.toJson<int>(lastSeenSeq),
      'status': serializer.toJson<String>(status),
      'createdAt': serializer.toJson<DateTime>(createdAt),
      'closedAt': serializer.toJson<DateTime?>(closedAt),
      'ownerKey': serializer.toJson<String>(ownerKey),
    };
  }

  LocalChatSession copyWith({
    String? sessionId,
    String? threadId,
    String? mode,
    String? sessionToken,
    DateTime? tokenExpiresAt,
    int? lastSeenSeq,
    String? status,
    DateTime? createdAt,
    Value<DateTime?> closedAt = const Value.absent(),
    String? ownerKey,
  }) => LocalChatSession(
    sessionId: sessionId ?? this.sessionId,
    threadId: threadId ?? this.threadId,
    mode: mode ?? this.mode,
    sessionToken: sessionToken ?? this.sessionToken,
    tokenExpiresAt: tokenExpiresAt ?? this.tokenExpiresAt,
    lastSeenSeq: lastSeenSeq ?? this.lastSeenSeq,
    status: status ?? this.status,
    createdAt: createdAt ?? this.createdAt,
    closedAt: closedAt.present ? closedAt.value : this.closedAt,
    ownerKey: ownerKey ?? this.ownerKey,
  );
  LocalChatSession copyWithCompanion(ChatSessionsCompanion data) {
    return LocalChatSession(
      sessionId: data.sessionId.present ? data.sessionId.value : this.sessionId,
      threadId: data.threadId.present ? data.threadId.value : this.threadId,
      mode: data.mode.present ? data.mode.value : this.mode,
      sessionToken: data.sessionToken.present
          ? data.sessionToken.value
          : this.sessionToken,
      tokenExpiresAt: data.tokenExpiresAt.present
          ? data.tokenExpiresAt.value
          : this.tokenExpiresAt,
      lastSeenSeq: data.lastSeenSeq.present
          ? data.lastSeenSeq.value
          : this.lastSeenSeq,
      status: data.status.present ? data.status.value : this.status,
      createdAt: data.createdAt.present ? data.createdAt.value : this.createdAt,
      closedAt: data.closedAt.present ? data.closedAt.value : this.closedAt,
      ownerKey: data.ownerKey.present ? data.ownerKey.value : this.ownerKey,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalChatSession(')
          ..write('sessionId: $sessionId, ')
          ..write('threadId: $threadId, ')
          ..write('mode: $mode, ')
          ..write('sessionToken: $sessionToken, ')
          ..write('tokenExpiresAt: $tokenExpiresAt, ')
          ..write('lastSeenSeq: $lastSeenSeq, ')
          ..write('status: $status, ')
          ..write('createdAt: $createdAt, ')
          ..write('closedAt: $closedAt, ')
          ..write('ownerKey: $ownerKey')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(
    sessionId,
    threadId,
    mode,
    sessionToken,
    tokenExpiresAt,
    lastSeenSeq,
    status,
    createdAt,
    closedAt,
    ownerKey,
  );
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalChatSession &&
          other.sessionId == this.sessionId &&
          other.threadId == this.threadId &&
          other.mode == this.mode &&
          other.sessionToken == this.sessionToken &&
          other.tokenExpiresAt == this.tokenExpiresAt &&
          other.lastSeenSeq == this.lastSeenSeq &&
          other.status == this.status &&
          other.createdAt == this.createdAt &&
          other.closedAt == this.closedAt &&
          other.ownerKey == this.ownerKey);
}

class ChatSessionsCompanion extends UpdateCompanion<LocalChatSession> {
  final Value<String> sessionId;
  final Value<String> threadId;
  final Value<String> mode;
  final Value<String> sessionToken;
  final Value<DateTime> tokenExpiresAt;
  final Value<int> lastSeenSeq;
  final Value<String> status;
  final Value<DateTime> createdAt;
  final Value<DateTime?> closedAt;
  final Value<String> ownerKey;
  final Value<int> rowid;
  const ChatSessionsCompanion({
    this.sessionId = const Value.absent(),
    this.threadId = const Value.absent(),
    this.mode = const Value.absent(),
    this.sessionToken = const Value.absent(),
    this.tokenExpiresAt = const Value.absent(),
    this.lastSeenSeq = const Value.absent(),
    this.status = const Value.absent(),
    this.createdAt = const Value.absent(),
    this.closedAt = const Value.absent(),
    this.ownerKey = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  ChatSessionsCompanion.insert({
    required String sessionId,
    required String threadId,
    required String mode,
    required String sessionToken,
    required DateTime tokenExpiresAt,
    this.lastSeenSeq = const Value.absent(),
    required String status,
    required DateTime createdAt,
    this.closedAt = const Value.absent(),
    this.ownerKey = const Value.absent(),
    this.rowid = const Value.absent(),
  }) : sessionId = Value(sessionId),
       threadId = Value(threadId),
       mode = Value(mode),
       sessionToken = Value(sessionToken),
       tokenExpiresAt = Value(tokenExpiresAt),
       status = Value(status),
       createdAt = Value(createdAt);
  static Insertable<LocalChatSession> custom({
    Expression<String>? sessionId,
    Expression<String>? threadId,
    Expression<String>? mode,
    Expression<String>? sessionToken,
    Expression<DateTime>? tokenExpiresAt,
    Expression<int>? lastSeenSeq,
    Expression<String>? status,
    Expression<DateTime>? createdAt,
    Expression<DateTime>? closedAt,
    Expression<String>? ownerKey,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (sessionId != null) 'session_id': sessionId,
      if (threadId != null) 'thread_id': threadId,
      if (mode != null) 'mode': mode,
      if (sessionToken != null) 'session_token': sessionToken,
      if (tokenExpiresAt != null) 'token_expires_at': tokenExpiresAt,
      if (lastSeenSeq != null) 'last_seen_seq': lastSeenSeq,
      if (status != null) 'status': status,
      if (createdAt != null) 'created_at': createdAt,
      if (closedAt != null) 'closed_at': closedAt,
      if (ownerKey != null) 'owner_key': ownerKey,
      if (rowid != null) 'rowid': rowid,
    });
  }

  ChatSessionsCompanion copyWith({
    Value<String>? sessionId,
    Value<String>? threadId,
    Value<String>? mode,
    Value<String>? sessionToken,
    Value<DateTime>? tokenExpiresAt,
    Value<int>? lastSeenSeq,
    Value<String>? status,
    Value<DateTime>? createdAt,
    Value<DateTime?>? closedAt,
    Value<String>? ownerKey,
    Value<int>? rowid,
  }) {
    return ChatSessionsCompanion(
      sessionId: sessionId ?? this.sessionId,
      threadId: threadId ?? this.threadId,
      mode: mode ?? this.mode,
      sessionToken: sessionToken ?? this.sessionToken,
      tokenExpiresAt: tokenExpiresAt ?? this.tokenExpiresAt,
      lastSeenSeq: lastSeenSeq ?? this.lastSeenSeq,
      status: status ?? this.status,
      createdAt: createdAt ?? this.createdAt,
      closedAt: closedAt ?? this.closedAt,
      ownerKey: ownerKey ?? this.ownerKey,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (sessionId.present) {
      map['session_id'] = Variable<String>(sessionId.value);
    }
    if (threadId.present) {
      map['thread_id'] = Variable<String>(threadId.value);
    }
    if (mode.present) {
      map['mode'] = Variable<String>(mode.value);
    }
    if (sessionToken.present) {
      map['session_token'] = Variable<String>(sessionToken.value);
    }
    if (tokenExpiresAt.present) {
      map['token_expires_at'] = Variable<DateTime>(tokenExpiresAt.value);
    }
    if (lastSeenSeq.present) {
      map['last_seen_seq'] = Variable<int>(lastSeenSeq.value);
    }
    if (status.present) {
      map['status'] = Variable<String>(status.value);
    }
    if (createdAt.present) {
      map['created_at'] = Variable<DateTime>(createdAt.value);
    }
    if (closedAt.present) {
      map['closed_at'] = Variable<DateTime>(closedAt.value);
    }
    if (ownerKey.present) {
      map['owner_key'] = Variable<String>(ownerKey.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('ChatSessionsCompanion(')
          ..write('sessionId: $sessionId, ')
          ..write('threadId: $threadId, ')
          ..write('mode: $mode, ')
          ..write('sessionToken: $sessionToken, ')
          ..write('tokenExpiresAt: $tokenExpiresAt, ')
          ..write('lastSeenSeq: $lastSeenSeq, ')
          ..write('status: $status, ')
          ..write('createdAt: $createdAt, ')
          ..write('closedAt: $closedAt, ')
          ..write('ownerKey: $ownerKey, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $MessageReactionsV2Table extends MessageReactionsV2
    with TableInfo<$MessageReactionsV2Table, LocalMessageReactionV2> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $MessageReactionsV2Table(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<int> id = GeneratedColumn<int>(
    'id',
    aliasedName,
    false,
    hasAutoIncrement: true,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultConstraints: GeneratedColumn.constraintIsAlways(
      'PRIMARY KEY AUTOINCREMENT',
    ),
  );
  static const VerificationMeta _messageIdMeta = const VerificationMeta(
    'messageId',
  );
  @override
  late final GeneratedColumn<String> messageId = GeneratedColumn<String>(
    'message_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _threadIdMeta = const VerificationMeta(
    'threadId',
  );
  @override
  late final GeneratedColumn<String> threadId = GeneratedColumn<String>(
    'thread_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _kindMeta = const VerificationMeta('kind');
  @override
  late final GeneratedColumn<String> kind = GeneratedColumn<String>(
    'kind',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _createdAtMeta = const VerificationMeta(
    'createdAt',
  );
  @override
  late final GeneratedColumn<DateTime> createdAt = GeneratedColumn<DateTime>(
    'created_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _ownerKeyMeta = const VerificationMeta(
    'ownerKey',
  );
  @override
  late final GeneratedColumn<String> ownerKey = GeneratedColumn<String>(
    'owner_key',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant(''),
  );
  @override
  List<GeneratedColumn> get $columns => [
    id,
    messageId,
    threadId,
    kind,
    createdAt,
    ownerKey,
  ];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'message_reactions_v2';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalMessageReactionV2> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    }
    if (data.containsKey('message_id')) {
      context.handle(
        _messageIdMeta,
        messageId.isAcceptableOrUnknown(data['message_id']!, _messageIdMeta),
      );
    } else if (isInserting) {
      context.missing(_messageIdMeta);
    }
    if (data.containsKey('thread_id')) {
      context.handle(
        _threadIdMeta,
        threadId.isAcceptableOrUnknown(data['thread_id']!, _threadIdMeta),
      );
    } else if (isInserting) {
      context.missing(_threadIdMeta);
    }
    if (data.containsKey('kind')) {
      context.handle(
        _kindMeta,
        kind.isAcceptableOrUnknown(data['kind']!, _kindMeta),
      );
    } else if (isInserting) {
      context.missing(_kindMeta);
    }
    if (data.containsKey('created_at')) {
      context.handle(
        _createdAtMeta,
        createdAt.isAcceptableOrUnknown(data['created_at']!, _createdAtMeta),
      );
    } else if (isInserting) {
      context.missing(_createdAtMeta);
    }
    if (data.containsKey('owner_key')) {
      context.handle(
        _ownerKeyMeta,
        ownerKey.isAcceptableOrUnknown(data['owner_key']!, _ownerKeyMeta),
      );
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalMessageReactionV2 map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalMessageReactionV2(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}id'],
      )!,
      messageId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}message_id'],
      )!,
      threadId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}thread_id'],
      )!,
      kind: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}kind'],
      )!,
      createdAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}created_at'],
      )!,
      ownerKey: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}owner_key'],
      )!,
    );
  }

  @override
  $MessageReactionsV2Table createAlias(String alias) {
    return $MessageReactionsV2Table(attachedDatabase, alias);
  }
}

class LocalMessageReactionV2 extends DataClass
    implements Insertable<LocalMessageReactionV2> {
  final int id;
  final String messageId;
  final String threadId;

  /// 'like' | 'dislike' | 'star'
  final String kind;
  final DateTime createdAt;

  /// 见 ChatThreadsV2.ownerKey —— 环境 × 账号隔离键，查询必填过滤。
  final String ownerKey;
  const LocalMessageReactionV2({
    required this.id,
    required this.messageId,
    required this.threadId,
    required this.kind,
    required this.createdAt,
    required this.ownerKey,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<int>(id);
    map['message_id'] = Variable<String>(messageId);
    map['thread_id'] = Variable<String>(threadId);
    map['kind'] = Variable<String>(kind);
    map['created_at'] = Variable<DateTime>(createdAt);
    map['owner_key'] = Variable<String>(ownerKey);
    return map;
  }

  MessageReactionsV2Companion toCompanion(bool nullToAbsent) {
    return MessageReactionsV2Companion(
      id: Value(id),
      messageId: Value(messageId),
      threadId: Value(threadId),
      kind: Value(kind),
      createdAt: Value(createdAt),
      ownerKey: Value(ownerKey),
    );
  }

  factory LocalMessageReactionV2.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalMessageReactionV2(
      id: serializer.fromJson<int>(json['id']),
      messageId: serializer.fromJson<String>(json['messageId']),
      threadId: serializer.fromJson<String>(json['threadId']),
      kind: serializer.fromJson<String>(json['kind']),
      createdAt: serializer.fromJson<DateTime>(json['createdAt']),
      ownerKey: serializer.fromJson<String>(json['ownerKey']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<int>(id),
      'messageId': serializer.toJson<String>(messageId),
      'threadId': serializer.toJson<String>(threadId),
      'kind': serializer.toJson<String>(kind),
      'createdAt': serializer.toJson<DateTime>(createdAt),
      'ownerKey': serializer.toJson<String>(ownerKey),
    };
  }

  LocalMessageReactionV2 copyWith({
    int? id,
    String? messageId,
    String? threadId,
    String? kind,
    DateTime? createdAt,
    String? ownerKey,
  }) => LocalMessageReactionV2(
    id: id ?? this.id,
    messageId: messageId ?? this.messageId,
    threadId: threadId ?? this.threadId,
    kind: kind ?? this.kind,
    createdAt: createdAt ?? this.createdAt,
    ownerKey: ownerKey ?? this.ownerKey,
  );
  LocalMessageReactionV2 copyWithCompanion(MessageReactionsV2Companion data) {
    return LocalMessageReactionV2(
      id: data.id.present ? data.id.value : this.id,
      messageId: data.messageId.present ? data.messageId.value : this.messageId,
      threadId: data.threadId.present ? data.threadId.value : this.threadId,
      kind: data.kind.present ? data.kind.value : this.kind,
      createdAt: data.createdAt.present ? data.createdAt.value : this.createdAt,
      ownerKey: data.ownerKey.present ? data.ownerKey.value : this.ownerKey,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalMessageReactionV2(')
          ..write('id: $id, ')
          ..write('messageId: $messageId, ')
          ..write('threadId: $threadId, ')
          ..write('kind: $kind, ')
          ..write('createdAt: $createdAt, ')
          ..write('ownerKey: $ownerKey')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode =>
      Object.hash(id, messageId, threadId, kind, createdAt, ownerKey);
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalMessageReactionV2 &&
          other.id == this.id &&
          other.messageId == this.messageId &&
          other.threadId == this.threadId &&
          other.kind == this.kind &&
          other.createdAt == this.createdAt &&
          other.ownerKey == this.ownerKey);
}

class MessageReactionsV2Companion
    extends UpdateCompanion<LocalMessageReactionV2> {
  final Value<int> id;
  final Value<String> messageId;
  final Value<String> threadId;
  final Value<String> kind;
  final Value<DateTime> createdAt;
  final Value<String> ownerKey;
  const MessageReactionsV2Companion({
    this.id = const Value.absent(),
    this.messageId = const Value.absent(),
    this.threadId = const Value.absent(),
    this.kind = const Value.absent(),
    this.createdAt = const Value.absent(),
    this.ownerKey = const Value.absent(),
  });
  MessageReactionsV2Companion.insert({
    this.id = const Value.absent(),
    required String messageId,
    required String threadId,
    required String kind,
    required DateTime createdAt,
    this.ownerKey = const Value.absent(),
  }) : messageId = Value(messageId),
       threadId = Value(threadId),
       kind = Value(kind),
       createdAt = Value(createdAt);
  static Insertable<LocalMessageReactionV2> custom({
    Expression<int>? id,
    Expression<String>? messageId,
    Expression<String>? threadId,
    Expression<String>? kind,
    Expression<DateTime>? createdAt,
    Expression<String>? ownerKey,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (messageId != null) 'message_id': messageId,
      if (threadId != null) 'thread_id': threadId,
      if (kind != null) 'kind': kind,
      if (createdAt != null) 'created_at': createdAt,
      if (ownerKey != null) 'owner_key': ownerKey,
    });
  }

  MessageReactionsV2Companion copyWith({
    Value<int>? id,
    Value<String>? messageId,
    Value<String>? threadId,
    Value<String>? kind,
    Value<DateTime>? createdAt,
    Value<String>? ownerKey,
  }) {
    return MessageReactionsV2Companion(
      id: id ?? this.id,
      messageId: messageId ?? this.messageId,
      threadId: threadId ?? this.threadId,
      kind: kind ?? this.kind,
      createdAt: createdAt ?? this.createdAt,
      ownerKey: ownerKey ?? this.ownerKey,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<int>(id.value);
    }
    if (messageId.present) {
      map['message_id'] = Variable<String>(messageId.value);
    }
    if (threadId.present) {
      map['thread_id'] = Variable<String>(threadId.value);
    }
    if (kind.present) {
      map['kind'] = Variable<String>(kind.value);
    }
    if (createdAt.present) {
      map['created_at'] = Variable<DateTime>(createdAt.value);
    }
    if (ownerKey.present) {
      map['owner_key'] = Variable<String>(ownerKey.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('MessageReactionsV2Companion(')
          ..write('id: $id, ')
          ..write('messageId: $messageId, ')
          ..write('threadId: $threadId, ')
          ..write('kind: $kind, ')
          ..write('createdAt: $createdAt, ')
          ..write('ownerKey: $ownerKey')
          ..write(')'))
        .toString();
  }
}

class $AigcTasksTable extends AigcTasks
    with TableInfo<$AigcTasksTable, LocalAigcTask> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $AigcTasksTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<String> id = GeneratedColumn<String>(
    'id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _userIdMeta = const VerificationMeta('userId');
  @override
  late final GeneratedColumn<String> userId = GeneratedColumn<String>(
    'user_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _typeMeta = const VerificationMeta('type');
  @override
  late final GeneratedColumn<String> type = GeneratedColumn<String>(
    'type',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _modelCodeMeta = const VerificationMeta(
    'modelCode',
  );
  @override
  late final GeneratedColumn<String> modelCode = GeneratedColumn<String>(
    'model_code',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _providerCodeMeta = const VerificationMeta(
    'providerCode',
  );
  @override
  late final GeneratedColumn<String> providerCode = GeneratedColumn<String>(
    'provider_code',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _statusMeta = const VerificationMeta('status');
  @override
  late final GeneratedColumn<String> status = GeneratedColumn<String>(
    'status',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _progressMeta = const VerificationMeta(
    'progress',
  );
  @override
  late final GeneratedColumn<int> progress = GeneratedColumn<int>(
    'progress',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultValue: const Constant(0),
  );
  static const VerificationMeta _promptMeta = const VerificationMeta('prompt');
  @override
  late final GeneratedColumn<String> prompt = GeneratedColumn<String>(
    'prompt',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _negativePromptMeta = const VerificationMeta(
    'negativePrompt',
  );
  @override
  late final GeneratedColumn<String> negativePrompt = GeneratedColumn<String>(
    'negative_prompt',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _paramsJsonMeta = const VerificationMeta(
    'paramsJson',
  );
  @override
  late final GeneratedColumn<String> paramsJson = GeneratedColumn<String>(
    'params_json',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant('{}'),
  );
  static const VerificationMeta _outputsJsonMeta = const VerificationMeta(
    'outputsJson',
  );
  @override
  late final GeneratedColumn<String> outputsJson = GeneratedColumn<String>(
    'outputs_json',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
    defaultValue: const Constant('[]'),
  );
  static const VerificationMeta _costCreditsMeta = const VerificationMeta(
    'costCredits',
  );
  @override
  late final GeneratedColumn<int> costCredits = GeneratedColumn<int>(
    'cost_credits',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultValue: const Constant(0),
  );
  static const VerificationMeta _refundedCreditsMeta = const VerificationMeta(
    'refundedCredits',
  );
  @override
  late final GeneratedColumn<int> refundedCredits = GeneratedColumn<int>(
    'refunded_credits',
    aliasedName,
    false,
    type: DriftSqlType.int,
    requiredDuringInsert: false,
    defaultValue: const Constant(0),
  );
  static const VerificationMeta _isPublicMeta = const VerificationMeta(
    'isPublic',
  );
  @override
  late final GeneratedColumn<bool> isPublic = GeneratedColumn<bool>(
    'is_public',
    aliasedName,
    false,
    type: DriftSqlType.bool,
    requiredDuringInsert: false,
    defaultConstraints: GeneratedColumn.constraintIsAlways(
      'CHECK ("is_public" IN (0, 1))',
    ),
    defaultValue: const Constant(false),
  );
  static const VerificationMeta _errorCodeMeta = const VerificationMeta(
    'errorCode',
  );
  @override
  late final GeneratedColumn<String> errorCode = GeneratedColumn<String>(
    'error_code',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _errorMessageMeta = const VerificationMeta(
    'errorMessage',
  );
  @override
  late final GeneratedColumn<String> errorMessage = GeneratedColumn<String>(
    'error_message',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _localTempIdMeta = const VerificationMeta(
    'localTempId',
  );
  @override
  late final GeneratedColumn<String> localTempId = GeneratedColumn<String>(
    'local_temp_id',
    aliasedName,
    true,
    type: DriftSqlType.string,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _createdAtMeta = const VerificationMeta(
    'createdAt',
  );
  @override
  late final GeneratedColumn<DateTime> createdAt = GeneratedColumn<DateTime>(
    'created_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _queuedAtMeta = const VerificationMeta(
    'queuedAt',
  );
  @override
  late final GeneratedColumn<DateTime> queuedAt = GeneratedColumn<DateTime>(
    'queued_at',
    aliasedName,
    true,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _startedAtMeta = const VerificationMeta(
    'startedAt',
  );
  @override
  late final GeneratedColumn<DateTime> startedAt = GeneratedColumn<DateTime>(
    'started_at',
    aliasedName,
    true,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _completedAtMeta = const VerificationMeta(
    'completedAt',
  );
  @override
  late final GeneratedColumn<DateTime> completedAt = GeneratedColumn<DateTime>(
    'completed_at',
    aliasedName,
    true,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _updatedAtMeta = const VerificationMeta(
    'updatedAt',
  );
  @override
  late final GeneratedColumn<DateTime> updatedAt = GeneratedColumn<DateTime>(
    'updated_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  @override
  List<GeneratedColumn> get $columns => [
    id,
    userId,
    type,
    modelCode,
    providerCode,
    status,
    progress,
    prompt,
    negativePrompt,
    paramsJson,
    outputsJson,
    costCredits,
    refundedCredits,
    isPublic,
    errorCode,
    errorMessage,
    localTempId,
    createdAt,
    queuedAt,
    startedAt,
    completedAt,
    updatedAt,
  ];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'aigc_tasks';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalAigcTask> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    } else if (isInserting) {
      context.missing(_idMeta);
    }
    if (data.containsKey('user_id')) {
      context.handle(
        _userIdMeta,
        userId.isAcceptableOrUnknown(data['user_id']!, _userIdMeta),
      );
    } else if (isInserting) {
      context.missing(_userIdMeta);
    }
    if (data.containsKey('type')) {
      context.handle(
        _typeMeta,
        type.isAcceptableOrUnknown(data['type']!, _typeMeta),
      );
    } else if (isInserting) {
      context.missing(_typeMeta);
    }
    if (data.containsKey('model_code')) {
      context.handle(
        _modelCodeMeta,
        modelCode.isAcceptableOrUnknown(data['model_code']!, _modelCodeMeta),
      );
    } else if (isInserting) {
      context.missing(_modelCodeMeta);
    }
    if (data.containsKey('provider_code')) {
      context.handle(
        _providerCodeMeta,
        providerCode.isAcceptableOrUnknown(
          data['provider_code']!,
          _providerCodeMeta,
        ),
      );
    }
    if (data.containsKey('status')) {
      context.handle(
        _statusMeta,
        status.isAcceptableOrUnknown(data['status']!, _statusMeta),
      );
    } else if (isInserting) {
      context.missing(_statusMeta);
    }
    if (data.containsKey('progress')) {
      context.handle(
        _progressMeta,
        progress.isAcceptableOrUnknown(data['progress']!, _progressMeta),
      );
    }
    if (data.containsKey('prompt')) {
      context.handle(
        _promptMeta,
        prompt.isAcceptableOrUnknown(data['prompt']!, _promptMeta),
      );
    } else if (isInserting) {
      context.missing(_promptMeta);
    }
    if (data.containsKey('negative_prompt')) {
      context.handle(
        _negativePromptMeta,
        negativePrompt.isAcceptableOrUnknown(
          data['negative_prompt']!,
          _negativePromptMeta,
        ),
      );
    }
    if (data.containsKey('params_json')) {
      context.handle(
        _paramsJsonMeta,
        paramsJson.isAcceptableOrUnknown(data['params_json']!, _paramsJsonMeta),
      );
    }
    if (data.containsKey('outputs_json')) {
      context.handle(
        _outputsJsonMeta,
        outputsJson.isAcceptableOrUnknown(
          data['outputs_json']!,
          _outputsJsonMeta,
        ),
      );
    }
    if (data.containsKey('cost_credits')) {
      context.handle(
        _costCreditsMeta,
        costCredits.isAcceptableOrUnknown(
          data['cost_credits']!,
          _costCreditsMeta,
        ),
      );
    }
    if (data.containsKey('refunded_credits')) {
      context.handle(
        _refundedCreditsMeta,
        refundedCredits.isAcceptableOrUnknown(
          data['refunded_credits']!,
          _refundedCreditsMeta,
        ),
      );
    }
    if (data.containsKey('is_public')) {
      context.handle(
        _isPublicMeta,
        isPublic.isAcceptableOrUnknown(data['is_public']!, _isPublicMeta),
      );
    }
    if (data.containsKey('error_code')) {
      context.handle(
        _errorCodeMeta,
        errorCode.isAcceptableOrUnknown(data['error_code']!, _errorCodeMeta),
      );
    }
    if (data.containsKey('error_message')) {
      context.handle(
        _errorMessageMeta,
        errorMessage.isAcceptableOrUnknown(
          data['error_message']!,
          _errorMessageMeta,
        ),
      );
    }
    if (data.containsKey('local_temp_id')) {
      context.handle(
        _localTempIdMeta,
        localTempId.isAcceptableOrUnknown(
          data['local_temp_id']!,
          _localTempIdMeta,
        ),
      );
    }
    if (data.containsKey('created_at')) {
      context.handle(
        _createdAtMeta,
        createdAt.isAcceptableOrUnknown(data['created_at']!, _createdAtMeta),
      );
    } else if (isInserting) {
      context.missing(_createdAtMeta);
    }
    if (data.containsKey('queued_at')) {
      context.handle(
        _queuedAtMeta,
        queuedAt.isAcceptableOrUnknown(data['queued_at']!, _queuedAtMeta),
      );
    }
    if (data.containsKey('started_at')) {
      context.handle(
        _startedAtMeta,
        startedAt.isAcceptableOrUnknown(data['started_at']!, _startedAtMeta),
      );
    }
    if (data.containsKey('completed_at')) {
      context.handle(
        _completedAtMeta,
        completedAt.isAcceptableOrUnknown(
          data['completed_at']!,
          _completedAtMeta,
        ),
      );
    }
    if (data.containsKey('updated_at')) {
      context.handle(
        _updatedAtMeta,
        updatedAt.isAcceptableOrUnknown(data['updated_at']!, _updatedAtMeta),
      );
    } else if (isInserting) {
      context.missing(_updatedAtMeta);
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalAigcTask map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalAigcTask(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}id'],
      )!,
      userId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}user_id'],
      )!,
      type: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}type'],
      )!,
      modelCode: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}model_code'],
      )!,
      providerCode: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}provider_code'],
      ),
      status: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}status'],
      )!,
      progress: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}progress'],
      )!,
      prompt: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}prompt'],
      )!,
      negativePrompt: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}negative_prompt'],
      ),
      paramsJson: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}params_json'],
      )!,
      outputsJson: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}outputs_json'],
      )!,
      costCredits: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}cost_credits'],
      )!,
      refundedCredits: attachedDatabase.typeMapping.read(
        DriftSqlType.int,
        data['${effectivePrefix}refunded_credits'],
      )!,
      isPublic: attachedDatabase.typeMapping.read(
        DriftSqlType.bool,
        data['${effectivePrefix}is_public'],
      )!,
      errorCode: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}error_code'],
      ),
      errorMessage: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}error_message'],
      ),
      localTempId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}local_temp_id'],
      ),
      createdAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}created_at'],
      )!,
      queuedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}queued_at'],
      ),
      startedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}started_at'],
      ),
      completedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}completed_at'],
      ),
      updatedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}updated_at'],
      )!,
    );
  }

  @override
  $AigcTasksTable createAlias(String alias) {
    return $AigcTasksTable(attachedDatabase, alias);
  }
}

class LocalAigcTask extends DataClass implements Insertable<LocalAigcTask> {
  final String id;
  final String userId;
  final String type;
  final String modelCode;
  final String? providerCode;
  final String status;
  final int progress;
  final String prompt;
  final String? negativePrompt;
  final String paramsJson;
  final String outputsJson;
  final int costCredits;
  final int refundedCredits;
  final bool isPublic;
  final String? errorCode;
  final String? errorMessage;
  final String? localTempId;
  final DateTime createdAt;
  final DateTime? queuedAt;
  final DateTime? startedAt;
  final DateTime? completedAt;
  final DateTime updatedAt;
  const LocalAigcTask({
    required this.id,
    required this.userId,
    required this.type,
    required this.modelCode,
    this.providerCode,
    required this.status,
    required this.progress,
    required this.prompt,
    this.negativePrompt,
    required this.paramsJson,
    required this.outputsJson,
    required this.costCredits,
    required this.refundedCredits,
    required this.isPublic,
    this.errorCode,
    this.errorMessage,
    this.localTempId,
    required this.createdAt,
    this.queuedAt,
    this.startedAt,
    this.completedAt,
    required this.updatedAt,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<String>(id);
    map['user_id'] = Variable<String>(userId);
    map['type'] = Variable<String>(type);
    map['model_code'] = Variable<String>(modelCode);
    if (!nullToAbsent || providerCode != null) {
      map['provider_code'] = Variable<String>(providerCode);
    }
    map['status'] = Variable<String>(status);
    map['progress'] = Variable<int>(progress);
    map['prompt'] = Variable<String>(prompt);
    if (!nullToAbsent || negativePrompt != null) {
      map['negative_prompt'] = Variable<String>(negativePrompt);
    }
    map['params_json'] = Variable<String>(paramsJson);
    map['outputs_json'] = Variable<String>(outputsJson);
    map['cost_credits'] = Variable<int>(costCredits);
    map['refunded_credits'] = Variable<int>(refundedCredits);
    map['is_public'] = Variable<bool>(isPublic);
    if (!nullToAbsent || errorCode != null) {
      map['error_code'] = Variable<String>(errorCode);
    }
    if (!nullToAbsent || errorMessage != null) {
      map['error_message'] = Variable<String>(errorMessage);
    }
    if (!nullToAbsent || localTempId != null) {
      map['local_temp_id'] = Variable<String>(localTempId);
    }
    map['created_at'] = Variable<DateTime>(createdAt);
    if (!nullToAbsent || queuedAt != null) {
      map['queued_at'] = Variable<DateTime>(queuedAt);
    }
    if (!nullToAbsent || startedAt != null) {
      map['started_at'] = Variable<DateTime>(startedAt);
    }
    if (!nullToAbsent || completedAt != null) {
      map['completed_at'] = Variable<DateTime>(completedAt);
    }
    map['updated_at'] = Variable<DateTime>(updatedAt);
    return map;
  }

  AigcTasksCompanion toCompanion(bool nullToAbsent) {
    return AigcTasksCompanion(
      id: Value(id),
      userId: Value(userId),
      type: Value(type),
      modelCode: Value(modelCode),
      providerCode: providerCode == null && nullToAbsent
          ? const Value.absent()
          : Value(providerCode),
      status: Value(status),
      progress: Value(progress),
      prompt: Value(prompt),
      negativePrompt: negativePrompt == null && nullToAbsent
          ? const Value.absent()
          : Value(negativePrompt),
      paramsJson: Value(paramsJson),
      outputsJson: Value(outputsJson),
      costCredits: Value(costCredits),
      refundedCredits: Value(refundedCredits),
      isPublic: Value(isPublic),
      errorCode: errorCode == null && nullToAbsent
          ? const Value.absent()
          : Value(errorCode),
      errorMessage: errorMessage == null && nullToAbsent
          ? const Value.absent()
          : Value(errorMessage),
      localTempId: localTempId == null && nullToAbsent
          ? const Value.absent()
          : Value(localTempId),
      createdAt: Value(createdAt),
      queuedAt: queuedAt == null && nullToAbsent
          ? const Value.absent()
          : Value(queuedAt),
      startedAt: startedAt == null && nullToAbsent
          ? const Value.absent()
          : Value(startedAt),
      completedAt: completedAt == null && nullToAbsent
          ? const Value.absent()
          : Value(completedAt),
      updatedAt: Value(updatedAt),
    );
  }

  factory LocalAigcTask.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalAigcTask(
      id: serializer.fromJson<String>(json['id']),
      userId: serializer.fromJson<String>(json['userId']),
      type: serializer.fromJson<String>(json['type']),
      modelCode: serializer.fromJson<String>(json['modelCode']),
      providerCode: serializer.fromJson<String?>(json['providerCode']),
      status: serializer.fromJson<String>(json['status']),
      progress: serializer.fromJson<int>(json['progress']),
      prompt: serializer.fromJson<String>(json['prompt']),
      negativePrompt: serializer.fromJson<String?>(json['negativePrompt']),
      paramsJson: serializer.fromJson<String>(json['paramsJson']),
      outputsJson: serializer.fromJson<String>(json['outputsJson']),
      costCredits: serializer.fromJson<int>(json['costCredits']),
      refundedCredits: serializer.fromJson<int>(json['refundedCredits']),
      isPublic: serializer.fromJson<bool>(json['isPublic']),
      errorCode: serializer.fromJson<String?>(json['errorCode']),
      errorMessage: serializer.fromJson<String?>(json['errorMessage']),
      localTempId: serializer.fromJson<String?>(json['localTempId']),
      createdAt: serializer.fromJson<DateTime>(json['createdAt']),
      queuedAt: serializer.fromJson<DateTime?>(json['queuedAt']),
      startedAt: serializer.fromJson<DateTime?>(json['startedAt']),
      completedAt: serializer.fromJson<DateTime?>(json['completedAt']),
      updatedAt: serializer.fromJson<DateTime>(json['updatedAt']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<String>(id),
      'userId': serializer.toJson<String>(userId),
      'type': serializer.toJson<String>(type),
      'modelCode': serializer.toJson<String>(modelCode),
      'providerCode': serializer.toJson<String?>(providerCode),
      'status': serializer.toJson<String>(status),
      'progress': serializer.toJson<int>(progress),
      'prompt': serializer.toJson<String>(prompt),
      'negativePrompt': serializer.toJson<String?>(negativePrompt),
      'paramsJson': serializer.toJson<String>(paramsJson),
      'outputsJson': serializer.toJson<String>(outputsJson),
      'costCredits': serializer.toJson<int>(costCredits),
      'refundedCredits': serializer.toJson<int>(refundedCredits),
      'isPublic': serializer.toJson<bool>(isPublic),
      'errorCode': serializer.toJson<String?>(errorCode),
      'errorMessage': serializer.toJson<String?>(errorMessage),
      'localTempId': serializer.toJson<String?>(localTempId),
      'createdAt': serializer.toJson<DateTime>(createdAt),
      'queuedAt': serializer.toJson<DateTime?>(queuedAt),
      'startedAt': serializer.toJson<DateTime?>(startedAt),
      'completedAt': serializer.toJson<DateTime?>(completedAt),
      'updatedAt': serializer.toJson<DateTime>(updatedAt),
    };
  }

  LocalAigcTask copyWith({
    String? id,
    String? userId,
    String? type,
    String? modelCode,
    Value<String?> providerCode = const Value.absent(),
    String? status,
    int? progress,
    String? prompt,
    Value<String?> negativePrompt = const Value.absent(),
    String? paramsJson,
    String? outputsJson,
    int? costCredits,
    int? refundedCredits,
    bool? isPublic,
    Value<String?> errorCode = const Value.absent(),
    Value<String?> errorMessage = const Value.absent(),
    Value<String?> localTempId = const Value.absent(),
    DateTime? createdAt,
    Value<DateTime?> queuedAt = const Value.absent(),
    Value<DateTime?> startedAt = const Value.absent(),
    Value<DateTime?> completedAt = const Value.absent(),
    DateTime? updatedAt,
  }) => LocalAigcTask(
    id: id ?? this.id,
    userId: userId ?? this.userId,
    type: type ?? this.type,
    modelCode: modelCode ?? this.modelCode,
    providerCode: providerCode.present ? providerCode.value : this.providerCode,
    status: status ?? this.status,
    progress: progress ?? this.progress,
    prompt: prompt ?? this.prompt,
    negativePrompt: negativePrompt.present
        ? negativePrompt.value
        : this.negativePrompt,
    paramsJson: paramsJson ?? this.paramsJson,
    outputsJson: outputsJson ?? this.outputsJson,
    costCredits: costCredits ?? this.costCredits,
    refundedCredits: refundedCredits ?? this.refundedCredits,
    isPublic: isPublic ?? this.isPublic,
    errorCode: errorCode.present ? errorCode.value : this.errorCode,
    errorMessage: errorMessage.present ? errorMessage.value : this.errorMessage,
    localTempId: localTempId.present ? localTempId.value : this.localTempId,
    createdAt: createdAt ?? this.createdAt,
    queuedAt: queuedAt.present ? queuedAt.value : this.queuedAt,
    startedAt: startedAt.present ? startedAt.value : this.startedAt,
    completedAt: completedAt.present ? completedAt.value : this.completedAt,
    updatedAt: updatedAt ?? this.updatedAt,
  );
  LocalAigcTask copyWithCompanion(AigcTasksCompanion data) {
    return LocalAigcTask(
      id: data.id.present ? data.id.value : this.id,
      userId: data.userId.present ? data.userId.value : this.userId,
      type: data.type.present ? data.type.value : this.type,
      modelCode: data.modelCode.present ? data.modelCode.value : this.modelCode,
      providerCode: data.providerCode.present
          ? data.providerCode.value
          : this.providerCode,
      status: data.status.present ? data.status.value : this.status,
      progress: data.progress.present ? data.progress.value : this.progress,
      prompt: data.prompt.present ? data.prompt.value : this.prompt,
      negativePrompt: data.negativePrompt.present
          ? data.negativePrompt.value
          : this.negativePrompt,
      paramsJson: data.paramsJson.present
          ? data.paramsJson.value
          : this.paramsJson,
      outputsJson: data.outputsJson.present
          ? data.outputsJson.value
          : this.outputsJson,
      costCredits: data.costCredits.present
          ? data.costCredits.value
          : this.costCredits,
      refundedCredits: data.refundedCredits.present
          ? data.refundedCredits.value
          : this.refundedCredits,
      isPublic: data.isPublic.present ? data.isPublic.value : this.isPublic,
      errorCode: data.errorCode.present ? data.errorCode.value : this.errorCode,
      errorMessage: data.errorMessage.present
          ? data.errorMessage.value
          : this.errorMessage,
      localTempId: data.localTempId.present
          ? data.localTempId.value
          : this.localTempId,
      createdAt: data.createdAt.present ? data.createdAt.value : this.createdAt,
      queuedAt: data.queuedAt.present ? data.queuedAt.value : this.queuedAt,
      startedAt: data.startedAt.present ? data.startedAt.value : this.startedAt,
      completedAt: data.completedAt.present
          ? data.completedAt.value
          : this.completedAt,
      updatedAt: data.updatedAt.present ? data.updatedAt.value : this.updatedAt,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalAigcTask(')
          ..write('id: $id, ')
          ..write('userId: $userId, ')
          ..write('type: $type, ')
          ..write('modelCode: $modelCode, ')
          ..write('providerCode: $providerCode, ')
          ..write('status: $status, ')
          ..write('progress: $progress, ')
          ..write('prompt: $prompt, ')
          ..write('negativePrompt: $negativePrompt, ')
          ..write('paramsJson: $paramsJson, ')
          ..write('outputsJson: $outputsJson, ')
          ..write('costCredits: $costCredits, ')
          ..write('refundedCredits: $refundedCredits, ')
          ..write('isPublic: $isPublic, ')
          ..write('errorCode: $errorCode, ')
          ..write('errorMessage: $errorMessage, ')
          ..write('localTempId: $localTempId, ')
          ..write('createdAt: $createdAt, ')
          ..write('queuedAt: $queuedAt, ')
          ..write('startedAt: $startedAt, ')
          ..write('completedAt: $completedAt, ')
          ..write('updatedAt: $updatedAt')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hashAll([
    id,
    userId,
    type,
    modelCode,
    providerCode,
    status,
    progress,
    prompt,
    negativePrompt,
    paramsJson,
    outputsJson,
    costCredits,
    refundedCredits,
    isPublic,
    errorCode,
    errorMessage,
    localTempId,
    createdAt,
    queuedAt,
    startedAt,
    completedAt,
    updatedAt,
  ]);
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalAigcTask &&
          other.id == this.id &&
          other.userId == this.userId &&
          other.type == this.type &&
          other.modelCode == this.modelCode &&
          other.providerCode == this.providerCode &&
          other.status == this.status &&
          other.progress == this.progress &&
          other.prompt == this.prompt &&
          other.negativePrompt == this.negativePrompt &&
          other.paramsJson == this.paramsJson &&
          other.outputsJson == this.outputsJson &&
          other.costCredits == this.costCredits &&
          other.refundedCredits == this.refundedCredits &&
          other.isPublic == this.isPublic &&
          other.errorCode == this.errorCode &&
          other.errorMessage == this.errorMessage &&
          other.localTempId == this.localTempId &&
          other.createdAt == this.createdAt &&
          other.queuedAt == this.queuedAt &&
          other.startedAt == this.startedAt &&
          other.completedAt == this.completedAt &&
          other.updatedAt == this.updatedAt);
}

class AigcTasksCompanion extends UpdateCompanion<LocalAigcTask> {
  final Value<String> id;
  final Value<String> userId;
  final Value<String> type;
  final Value<String> modelCode;
  final Value<String?> providerCode;
  final Value<String> status;
  final Value<int> progress;
  final Value<String> prompt;
  final Value<String?> negativePrompt;
  final Value<String> paramsJson;
  final Value<String> outputsJson;
  final Value<int> costCredits;
  final Value<int> refundedCredits;
  final Value<bool> isPublic;
  final Value<String?> errorCode;
  final Value<String?> errorMessage;
  final Value<String?> localTempId;
  final Value<DateTime> createdAt;
  final Value<DateTime?> queuedAt;
  final Value<DateTime?> startedAt;
  final Value<DateTime?> completedAt;
  final Value<DateTime> updatedAt;
  final Value<int> rowid;
  const AigcTasksCompanion({
    this.id = const Value.absent(),
    this.userId = const Value.absent(),
    this.type = const Value.absent(),
    this.modelCode = const Value.absent(),
    this.providerCode = const Value.absent(),
    this.status = const Value.absent(),
    this.progress = const Value.absent(),
    this.prompt = const Value.absent(),
    this.negativePrompt = const Value.absent(),
    this.paramsJson = const Value.absent(),
    this.outputsJson = const Value.absent(),
    this.costCredits = const Value.absent(),
    this.refundedCredits = const Value.absent(),
    this.isPublic = const Value.absent(),
    this.errorCode = const Value.absent(),
    this.errorMessage = const Value.absent(),
    this.localTempId = const Value.absent(),
    this.createdAt = const Value.absent(),
    this.queuedAt = const Value.absent(),
    this.startedAt = const Value.absent(),
    this.completedAt = const Value.absent(),
    this.updatedAt = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  AigcTasksCompanion.insert({
    required String id,
    required String userId,
    required String type,
    required String modelCode,
    this.providerCode = const Value.absent(),
    required String status,
    this.progress = const Value.absent(),
    required String prompt,
    this.negativePrompt = const Value.absent(),
    this.paramsJson = const Value.absent(),
    this.outputsJson = const Value.absent(),
    this.costCredits = const Value.absent(),
    this.refundedCredits = const Value.absent(),
    this.isPublic = const Value.absent(),
    this.errorCode = const Value.absent(),
    this.errorMessage = const Value.absent(),
    this.localTempId = const Value.absent(),
    required DateTime createdAt,
    this.queuedAt = const Value.absent(),
    this.startedAt = const Value.absent(),
    this.completedAt = const Value.absent(),
    required DateTime updatedAt,
    this.rowid = const Value.absent(),
  }) : id = Value(id),
       userId = Value(userId),
       type = Value(type),
       modelCode = Value(modelCode),
       status = Value(status),
       prompt = Value(prompt),
       createdAt = Value(createdAt),
       updatedAt = Value(updatedAt);
  static Insertable<LocalAigcTask> custom({
    Expression<String>? id,
    Expression<String>? userId,
    Expression<String>? type,
    Expression<String>? modelCode,
    Expression<String>? providerCode,
    Expression<String>? status,
    Expression<int>? progress,
    Expression<String>? prompt,
    Expression<String>? negativePrompt,
    Expression<String>? paramsJson,
    Expression<String>? outputsJson,
    Expression<int>? costCredits,
    Expression<int>? refundedCredits,
    Expression<bool>? isPublic,
    Expression<String>? errorCode,
    Expression<String>? errorMessage,
    Expression<String>? localTempId,
    Expression<DateTime>? createdAt,
    Expression<DateTime>? queuedAt,
    Expression<DateTime>? startedAt,
    Expression<DateTime>? completedAt,
    Expression<DateTime>? updatedAt,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (userId != null) 'user_id': userId,
      if (type != null) 'type': type,
      if (modelCode != null) 'model_code': modelCode,
      if (providerCode != null) 'provider_code': providerCode,
      if (status != null) 'status': status,
      if (progress != null) 'progress': progress,
      if (prompt != null) 'prompt': prompt,
      if (negativePrompt != null) 'negative_prompt': negativePrompt,
      if (paramsJson != null) 'params_json': paramsJson,
      if (outputsJson != null) 'outputs_json': outputsJson,
      if (costCredits != null) 'cost_credits': costCredits,
      if (refundedCredits != null) 'refunded_credits': refundedCredits,
      if (isPublic != null) 'is_public': isPublic,
      if (errorCode != null) 'error_code': errorCode,
      if (errorMessage != null) 'error_message': errorMessage,
      if (localTempId != null) 'local_temp_id': localTempId,
      if (createdAt != null) 'created_at': createdAt,
      if (queuedAt != null) 'queued_at': queuedAt,
      if (startedAt != null) 'started_at': startedAt,
      if (completedAt != null) 'completed_at': completedAt,
      if (updatedAt != null) 'updated_at': updatedAt,
      if (rowid != null) 'rowid': rowid,
    });
  }

  AigcTasksCompanion copyWith({
    Value<String>? id,
    Value<String>? userId,
    Value<String>? type,
    Value<String>? modelCode,
    Value<String?>? providerCode,
    Value<String>? status,
    Value<int>? progress,
    Value<String>? prompt,
    Value<String?>? negativePrompt,
    Value<String>? paramsJson,
    Value<String>? outputsJson,
    Value<int>? costCredits,
    Value<int>? refundedCredits,
    Value<bool>? isPublic,
    Value<String?>? errorCode,
    Value<String?>? errorMessage,
    Value<String?>? localTempId,
    Value<DateTime>? createdAt,
    Value<DateTime?>? queuedAt,
    Value<DateTime?>? startedAt,
    Value<DateTime?>? completedAt,
    Value<DateTime>? updatedAt,
    Value<int>? rowid,
  }) {
    return AigcTasksCompanion(
      id: id ?? this.id,
      userId: userId ?? this.userId,
      type: type ?? this.type,
      modelCode: modelCode ?? this.modelCode,
      providerCode: providerCode ?? this.providerCode,
      status: status ?? this.status,
      progress: progress ?? this.progress,
      prompt: prompt ?? this.prompt,
      negativePrompt: negativePrompt ?? this.negativePrompt,
      paramsJson: paramsJson ?? this.paramsJson,
      outputsJson: outputsJson ?? this.outputsJson,
      costCredits: costCredits ?? this.costCredits,
      refundedCredits: refundedCredits ?? this.refundedCredits,
      isPublic: isPublic ?? this.isPublic,
      errorCode: errorCode ?? this.errorCode,
      errorMessage: errorMessage ?? this.errorMessage,
      localTempId: localTempId ?? this.localTempId,
      createdAt: createdAt ?? this.createdAt,
      queuedAt: queuedAt ?? this.queuedAt,
      startedAt: startedAt ?? this.startedAt,
      completedAt: completedAt ?? this.completedAt,
      updatedAt: updatedAt ?? this.updatedAt,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<String>(id.value);
    }
    if (userId.present) {
      map['user_id'] = Variable<String>(userId.value);
    }
    if (type.present) {
      map['type'] = Variable<String>(type.value);
    }
    if (modelCode.present) {
      map['model_code'] = Variable<String>(modelCode.value);
    }
    if (providerCode.present) {
      map['provider_code'] = Variable<String>(providerCode.value);
    }
    if (status.present) {
      map['status'] = Variable<String>(status.value);
    }
    if (progress.present) {
      map['progress'] = Variable<int>(progress.value);
    }
    if (prompt.present) {
      map['prompt'] = Variable<String>(prompt.value);
    }
    if (negativePrompt.present) {
      map['negative_prompt'] = Variable<String>(negativePrompt.value);
    }
    if (paramsJson.present) {
      map['params_json'] = Variable<String>(paramsJson.value);
    }
    if (outputsJson.present) {
      map['outputs_json'] = Variable<String>(outputsJson.value);
    }
    if (costCredits.present) {
      map['cost_credits'] = Variable<int>(costCredits.value);
    }
    if (refundedCredits.present) {
      map['refunded_credits'] = Variable<int>(refundedCredits.value);
    }
    if (isPublic.present) {
      map['is_public'] = Variable<bool>(isPublic.value);
    }
    if (errorCode.present) {
      map['error_code'] = Variable<String>(errorCode.value);
    }
    if (errorMessage.present) {
      map['error_message'] = Variable<String>(errorMessage.value);
    }
    if (localTempId.present) {
      map['local_temp_id'] = Variable<String>(localTempId.value);
    }
    if (createdAt.present) {
      map['created_at'] = Variable<DateTime>(createdAt.value);
    }
    if (queuedAt.present) {
      map['queued_at'] = Variable<DateTime>(queuedAt.value);
    }
    if (startedAt.present) {
      map['started_at'] = Variable<DateTime>(startedAt.value);
    }
    if (completedAt.present) {
      map['completed_at'] = Variable<DateTime>(completedAt.value);
    }
    if (updatedAt.present) {
      map['updated_at'] = Variable<DateTime>(updatedAt.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('AigcTasksCompanion(')
          ..write('id: $id, ')
          ..write('userId: $userId, ')
          ..write('type: $type, ')
          ..write('modelCode: $modelCode, ')
          ..write('providerCode: $providerCode, ')
          ..write('status: $status, ')
          ..write('progress: $progress, ')
          ..write('prompt: $prompt, ')
          ..write('negativePrompt: $negativePrompt, ')
          ..write('paramsJson: $paramsJson, ')
          ..write('outputsJson: $outputsJson, ')
          ..write('costCredits: $costCredits, ')
          ..write('refundedCredits: $refundedCredits, ')
          ..write('isPublic: $isPublic, ')
          ..write('errorCode: $errorCode, ')
          ..write('errorMessage: $errorMessage, ')
          ..write('localTempId: $localTempId, ')
          ..write('createdAt: $createdAt, ')
          ..write('queuedAt: $queuedAt, ')
          ..write('startedAt: $startedAt, ')
          ..write('completedAt: $completedAt, ')
          ..write('updatedAt: $updatedAt, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $SseCursorsTable extends SseCursors
    with TableInfo<$SseCursorsTable, LocalSseCursor> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $SseCursorsTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _scopeMeta = const VerificationMeta('scope');
  @override
  late final GeneratedColumn<String> scope = GeneratedColumn<String>(
    'scope',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _lastEventIdMeta = const VerificationMeta(
    'lastEventId',
  );
  @override
  late final GeneratedColumn<String> lastEventId = GeneratedColumn<String>(
    'last_event_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _updatedAtMeta = const VerificationMeta(
    'updatedAt',
  );
  @override
  late final GeneratedColumn<DateTime> updatedAt = GeneratedColumn<DateTime>(
    'updated_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  @override
  List<GeneratedColumn> get $columns => [scope, lastEventId, updatedAt];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'sse_cursors';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalSseCursor> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('scope')) {
      context.handle(
        _scopeMeta,
        scope.isAcceptableOrUnknown(data['scope']!, _scopeMeta),
      );
    } else if (isInserting) {
      context.missing(_scopeMeta);
    }
    if (data.containsKey('last_event_id')) {
      context.handle(
        _lastEventIdMeta,
        lastEventId.isAcceptableOrUnknown(
          data['last_event_id']!,
          _lastEventIdMeta,
        ),
      );
    } else if (isInserting) {
      context.missing(_lastEventIdMeta);
    }
    if (data.containsKey('updated_at')) {
      context.handle(
        _updatedAtMeta,
        updatedAt.isAcceptableOrUnknown(data['updated_at']!, _updatedAtMeta),
      );
    } else if (isInserting) {
      context.missing(_updatedAtMeta);
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {scope};
  @override
  LocalSseCursor map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalSseCursor(
      scope: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}scope'],
      )!,
      lastEventId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}last_event_id'],
      )!,
      updatedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}updated_at'],
      )!,
    );
  }

  @override
  $SseCursorsTable createAlias(String alias) {
    return $SseCursorsTable(attachedDatabase, alias);
  }
}

class LocalSseCursor extends DataClass implements Insertable<LocalSseCursor> {
  final String scope;
  final String lastEventId;
  final DateTime updatedAt;
  const LocalSseCursor({
    required this.scope,
    required this.lastEventId,
    required this.updatedAt,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['scope'] = Variable<String>(scope);
    map['last_event_id'] = Variable<String>(lastEventId);
    map['updated_at'] = Variable<DateTime>(updatedAt);
    return map;
  }

  SseCursorsCompanion toCompanion(bool nullToAbsent) {
    return SseCursorsCompanion(
      scope: Value(scope),
      lastEventId: Value(lastEventId),
      updatedAt: Value(updatedAt),
    );
  }

  factory LocalSseCursor.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalSseCursor(
      scope: serializer.fromJson<String>(json['scope']),
      lastEventId: serializer.fromJson<String>(json['lastEventId']),
      updatedAt: serializer.fromJson<DateTime>(json['updatedAt']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'scope': serializer.toJson<String>(scope),
      'lastEventId': serializer.toJson<String>(lastEventId),
      'updatedAt': serializer.toJson<DateTime>(updatedAt),
    };
  }

  LocalSseCursor copyWith({
    String? scope,
    String? lastEventId,
    DateTime? updatedAt,
  }) => LocalSseCursor(
    scope: scope ?? this.scope,
    lastEventId: lastEventId ?? this.lastEventId,
    updatedAt: updatedAt ?? this.updatedAt,
  );
  LocalSseCursor copyWithCompanion(SseCursorsCompanion data) {
    return LocalSseCursor(
      scope: data.scope.present ? data.scope.value : this.scope,
      lastEventId: data.lastEventId.present
          ? data.lastEventId.value
          : this.lastEventId,
      updatedAt: data.updatedAt.present ? data.updatedAt.value : this.updatedAt,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalSseCursor(')
          ..write('scope: $scope, ')
          ..write('lastEventId: $lastEventId, ')
          ..write('updatedAt: $updatedAt')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(scope, lastEventId, updatedAt);
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalSseCursor &&
          other.scope == this.scope &&
          other.lastEventId == this.lastEventId &&
          other.updatedAt == this.updatedAt);
}

class SseCursorsCompanion extends UpdateCompanion<LocalSseCursor> {
  final Value<String> scope;
  final Value<String> lastEventId;
  final Value<DateTime> updatedAt;
  final Value<int> rowid;
  const SseCursorsCompanion({
    this.scope = const Value.absent(),
    this.lastEventId = const Value.absent(),
    this.updatedAt = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  SseCursorsCompanion.insert({
    required String scope,
    required String lastEventId,
    required DateTime updatedAt,
    this.rowid = const Value.absent(),
  }) : scope = Value(scope),
       lastEventId = Value(lastEventId),
       updatedAt = Value(updatedAt);
  static Insertable<LocalSseCursor> custom({
    Expression<String>? scope,
    Expression<String>? lastEventId,
    Expression<DateTime>? updatedAt,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (scope != null) 'scope': scope,
      if (lastEventId != null) 'last_event_id': lastEventId,
      if (updatedAt != null) 'updated_at': updatedAt,
      if (rowid != null) 'rowid': rowid,
    });
  }

  SseCursorsCompanion copyWith({
    Value<String>? scope,
    Value<String>? lastEventId,
    Value<DateTime>? updatedAt,
    Value<int>? rowid,
  }) {
    return SseCursorsCompanion(
      scope: scope ?? this.scope,
      lastEventId: lastEventId ?? this.lastEventId,
      updatedAt: updatedAt ?? this.updatedAt,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (scope.present) {
      map['scope'] = Variable<String>(scope.value);
    }
    if (lastEventId.present) {
      map['last_event_id'] = Variable<String>(lastEventId.value);
    }
    if (updatedAt.present) {
      map['updated_at'] = Variable<DateTime>(updatedAt.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('SseCursorsCompanion(')
          ..write('scope: $scope, ')
          ..write('lastEventId: $lastEventId, ')
          ..write('updatedAt: $updatedAt, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $RssFeedsCacheTable extends RssFeedsCache
    with TableInfo<$RssFeedsCacheTable, LocalRssFeed> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $RssFeedsCacheTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<String> id = GeneratedColumn<String>(
    'id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _scopeIdMeta = const VerificationMeta(
    'scopeId',
  );
  @override
  late final GeneratedColumn<String> scopeId = GeneratedColumn<String>(
    'scope_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _payloadJsonMeta = const VerificationMeta(
    'payloadJson',
  );
  @override
  late final GeneratedColumn<String> payloadJson = GeneratedColumn<String>(
    'payload_json',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _cachedAtMeta = const VerificationMeta(
    'cachedAt',
  );
  @override
  late final GeneratedColumn<DateTime> cachedAt = GeneratedColumn<DateTime>(
    'cached_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  @override
  List<GeneratedColumn> get $columns => [id, scopeId, payloadJson, cachedAt];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'rss_feeds_cache';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalRssFeed> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    } else if (isInserting) {
      context.missing(_idMeta);
    }
    if (data.containsKey('scope_id')) {
      context.handle(
        _scopeIdMeta,
        scopeId.isAcceptableOrUnknown(data['scope_id']!, _scopeIdMeta),
      );
    } else if (isInserting) {
      context.missing(_scopeIdMeta);
    }
    if (data.containsKey('payload_json')) {
      context.handle(
        _payloadJsonMeta,
        payloadJson.isAcceptableOrUnknown(
          data['payload_json']!,
          _payloadJsonMeta,
        ),
      );
    } else if (isInserting) {
      context.missing(_payloadJsonMeta);
    }
    if (data.containsKey('cached_at')) {
      context.handle(
        _cachedAtMeta,
        cachedAt.isAcceptableOrUnknown(data['cached_at']!, _cachedAtMeta),
      );
    } else if (isInserting) {
      context.missing(_cachedAtMeta);
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalRssFeed map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalRssFeed(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}id'],
      )!,
      scopeId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}scope_id'],
      )!,
      payloadJson: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}payload_json'],
      )!,
      cachedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}cached_at'],
      )!,
    );
  }

  @override
  $RssFeedsCacheTable createAlias(String alias) {
    return $RssFeedsCacheTable(attachedDatabase, alias);
  }
}

class LocalRssFeed extends DataClass implements Insertable<LocalRssFeed> {
  final String id;
  final String scopeId;
  final String payloadJson;
  final DateTime cachedAt;
  const LocalRssFeed({
    required this.id,
    required this.scopeId,
    required this.payloadJson,
    required this.cachedAt,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<String>(id);
    map['scope_id'] = Variable<String>(scopeId);
    map['payload_json'] = Variable<String>(payloadJson);
    map['cached_at'] = Variable<DateTime>(cachedAt);
    return map;
  }

  RssFeedsCacheCompanion toCompanion(bool nullToAbsent) {
    return RssFeedsCacheCompanion(
      id: Value(id),
      scopeId: Value(scopeId),
      payloadJson: Value(payloadJson),
      cachedAt: Value(cachedAt),
    );
  }

  factory LocalRssFeed.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalRssFeed(
      id: serializer.fromJson<String>(json['id']),
      scopeId: serializer.fromJson<String>(json['scopeId']),
      payloadJson: serializer.fromJson<String>(json['payloadJson']),
      cachedAt: serializer.fromJson<DateTime>(json['cachedAt']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<String>(id),
      'scopeId': serializer.toJson<String>(scopeId),
      'payloadJson': serializer.toJson<String>(payloadJson),
      'cachedAt': serializer.toJson<DateTime>(cachedAt),
    };
  }

  LocalRssFeed copyWith({
    String? id,
    String? scopeId,
    String? payloadJson,
    DateTime? cachedAt,
  }) => LocalRssFeed(
    id: id ?? this.id,
    scopeId: scopeId ?? this.scopeId,
    payloadJson: payloadJson ?? this.payloadJson,
    cachedAt: cachedAt ?? this.cachedAt,
  );
  LocalRssFeed copyWithCompanion(RssFeedsCacheCompanion data) {
    return LocalRssFeed(
      id: data.id.present ? data.id.value : this.id,
      scopeId: data.scopeId.present ? data.scopeId.value : this.scopeId,
      payloadJson: data.payloadJson.present
          ? data.payloadJson.value
          : this.payloadJson,
      cachedAt: data.cachedAt.present ? data.cachedAt.value : this.cachedAt,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalRssFeed(')
          ..write('id: $id, ')
          ..write('scopeId: $scopeId, ')
          ..write('payloadJson: $payloadJson, ')
          ..write('cachedAt: $cachedAt')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode => Object.hash(id, scopeId, payloadJson, cachedAt);
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalRssFeed &&
          other.id == this.id &&
          other.scopeId == this.scopeId &&
          other.payloadJson == this.payloadJson &&
          other.cachedAt == this.cachedAt);
}

class RssFeedsCacheCompanion extends UpdateCompanion<LocalRssFeed> {
  final Value<String> id;
  final Value<String> scopeId;
  final Value<String> payloadJson;
  final Value<DateTime> cachedAt;
  final Value<int> rowid;
  const RssFeedsCacheCompanion({
    this.id = const Value.absent(),
    this.scopeId = const Value.absent(),
    this.payloadJson = const Value.absent(),
    this.cachedAt = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  RssFeedsCacheCompanion.insert({
    required String id,
    required String scopeId,
    required String payloadJson,
    required DateTime cachedAt,
    this.rowid = const Value.absent(),
  }) : id = Value(id),
       scopeId = Value(scopeId),
       payloadJson = Value(payloadJson),
       cachedAt = Value(cachedAt);
  static Insertable<LocalRssFeed> custom({
    Expression<String>? id,
    Expression<String>? scopeId,
    Expression<String>? payloadJson,
    Expression<DateTime>? cachedAt,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (scopeId != null) 'scope_id': scopeId,
      if (payloadJson != null) 'payload_json': payloadJson,
      if (cachedAt != null) 'cached_at': cachedAt,
      if (rowid != null) 'rowid': rowid,
    });
  }

  RssFeedsCacheCompanion copyWith({
    Value<String>? id,
    Value<String>? scopeId,
    Value<String>? payloadJson,
    Value<DateTime>? cachedAt,
    Value<int>? rowid,
  }) {
    return RssFeedsCacheCompanion(
      id: id ?? this.id,
      scopeId: scopeId ?? this.scopeId,
      payloadJson: payloadJson ?? this.payloadJson,
      cachedAt: cachedAt ?? this.cachedAt,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<String>(id.value);
    }
    if (scopeId.present) {
      map['scope_id'] = Variable<String>(scopeId.value);
    }
    if (payloadJson.present) {
      map['payload_json'] = Variable<String>(payloadJson.value);
    }
    if (cachedAt.present) {
      map['cached_at'] = Variable<DateTime>(cachedAt.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('RssFeedsCacheCompanion(')
          ..write('id: $id, ')
          ..write('scopeId: $scopeId, ')
          ..write('payloadJson: $payloadJson, ')
          ..write('cachedAt: $cachedAt, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

class $RssEntriesCacheTable extends RssEntriesCache
    with TableInfo<$RssEntriesCacheTable, LocalRssEntry> {
  @override
  final GeneratedDatabase attachedDatabase;
  final String? _alias;
  $RssEntriesCacheTable(this.attachedDatabase, [this._alias]);
  static const VerificationMeta _idMeta = const VerificationMeta('id');
  @override
  late final GeneratedColumn<String> id = GeneratedColumn<String>(
    'id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _scopeIdMeta = const VerificationMeta(
    'scopeId',
  );
  @override
  late final GeneratedColumn<String> scopeId = GeneratedColumn<String>(
    'scope_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _feedIdMeta = const VerificationMeta('feedId');
  @override
  late final GeneratedColumn<String> feedId = GeneratedColumn<String>(
    'feed_id',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _payloadJsonMeta = const VerificationMeta(
    'payloadJson',
  );
  @override
  late final GeneratedColumn<String> payloadJson = GeneratedColumn<String>(
    'payload_json',
    aliasedName,
    false,
    type: DriftSqlType.string,
    requiredDuringInsert: true,
  );
  static const VerificationMeta _fetchedAtMeta = const VerificationMeta(
    'fetchedAt',
  );
  @override
  late final GeneratedColumn<DateTime> fetchedAt = GeneratedColumn<DateTime>(
    'fetched_at',
    aliasedName,
    true,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: false,
  );
  static const VerificationMeta _cachedAtMeta = const VerificationMeta(
    'cachedAt',
  );
  @override
  late final GeneratedColumn<DateTime> cachedAt = GeneratedColumn<DateTime>(
    'cached_at',
    aliasedName,
    false,
    type: DriftSqlType.dateTime,
    requiredDuringInsert: true,
  );
  @override
  List<GeneratedColumn> get $columns => [
    id,
    scopeId,
    feedId,
    payloadJson,
    fetchedAt,
    cachedAt,
  ];
  @override
  String get aliasedName => _alias ?? actualTableName;
  @override
  String get actualTableName => $name;
  static const String $name = 'rss_entries_cache';
  @override
  VerificationContext validateIntegrity(
    Insertable<LocalRssEntry> instance, {
    bool isInserting = false,
  }) {
    final context = VerificationContext();
    final data = instance.toColumns(true);
    if (data.containsKey('id')) {
      context.handle(_idMeta, id.isAcceptableOrUnknown(data['id']!, _idMeta));
    } else if (isInserting) {
      context.missing(_idMeta);
    }
    if (data.containsKey('scope_id')) {
      context.handle(
        _scopeIdMeta,
        scopeId.isAcceptableOrUnknown(data['scope_id']!, _scopeIdMeta),
      );
    } else if (isInserting) {
      context.missing(_scopeIdMeta);
    }
    if (data.containsKey('feed_id')) {
      context.handle(
        _feedIdMeta,
        feedId.isAcceptableOrUnknown(data['feed_id']!, _feedIdMeta),
      );
    } else if (isInserting) {
      context.missing(_feedIdMeta);
    }
    if (data.containsKey('payload_json')) {
      context.handle(
        _payloadJsonMeta,
        payloadJson.isAcceptableOrUnknown(
          data['payload_json']!,
          _payloadJsonMeta,
        ),
      );
    } else if (isInserting) {
      context.missing(_payloadJsonMeta);
    }
    if (data.containsKey('fetched_at')) {
      context.handle(
        _fetchedAtMeta,
        fetchedAt.isAcceptableOrUnknown(data['fetched_at']!, _fetchedAtMeta),
      );
    }
    if (data.containsKey('cached_at')) {
      context.handle(
        _cachedAtMeta,
        cachedAt.isAcceptableOrUnknown(data['cached_at']!, _cachedAtMeta),
      );
    } else if (isInserting) {
      context.missing(_cachedAtMeta);
    }
    return context;
  }

  @override
  Set<GeneratedColumn> get $primaryKey => {id};
  @override
  LocalRssEntry map(Map<String, dynamic> data, {String? tablePrefix}) {
    final effectivePrefix = tablePrefix != null ? '$tablePrefix.' : '';
    return LocalRssEntry(
      id: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}id'],
      )!,
      scopeId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}scope_id'],
      )!,
      feedId: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}feed_id'],
      )!,
      payloadJson: attachedDatabase.typeMapping.read(
        DriftSqlType.string,
        data['${effectivePrefix}payload_json'],
      )!,
      fetchedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}fetched_at'],
      ),
      cachedAt: attachedDatabase.typeMapping.read(
        DriftSqlType.dateTime,
        data['${effectivePrefix}cached_at'],
      )!,
    );
  }

  @override
  $RssEntriesCacheTable createAlias(String alias) {
    return $RssEntriesCacheTable(attachedDatabase, alias);
  }
}

class LocalRssEntry extends DataClass implements Insertable<LocalRssEntry> {
  final String id;
  final String scopeId;
  final String feedId;
  final String payloadJson;
  final DateTime? fetchedAt;
  final DateTime cachedAt;
  const LocalRssEntry({
    required this.id,
    required this.scopeId,
    required this.feedId,
    required this.payloadJson,
    this.fetchedAt,
    required this.cachedAt,
  });
  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    map['id'] = Variable<String>(id);
    map['scope_id'] = Variable<String>(scopeId);
    map['feed_id'] = Variable<String>(feedId);
    map['payload_json'] = Variable<String>(payloadJson);
    if (!nullToAbsent || fetchedAt != null) {
      map['fetched_at'] = Variable<DateTime>(fetchedAt);
    }
    map['cached_at'] = Variable<DateTime>(cachedAt);
    return map;
  }

  RssEntriesCacheCompanion toCompanion(bool nullToAbsent) {
    return RssEntriesCacheCompanion(
      id: Value(id),
      scopeId: Value(scopeId),
      feedId: Value(feedId),
      payloadJson: Value(payloadJson),
      fetchedAt: fetchedAt == null && nullToAbsent
          ? const Value.absent()
          : Value(fetchedAt),
      cachedAt: Value(cachedAt),
    );
  }

  factory LocalRssEntry.fromJson(
    Map<String, dynamic> json, {
    ValueSerializer? serializer,
  }) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return LocalRssEntry(
      id: serializer.fromJson<String>(json['id']),
      scopeId: serializer.fromJson<String>(json['scopeId']),
      feedId: serializer.fromJson<String>(json['feedId']),
      payloadJson: serializer.fromJson<String>(json['payloadJson']),
      fetchedAt: serializer.fromJson<DateTime?>(json['fetchedAt']),
      cachedAt: serializer.fromJson<DateTime>(json['cachedAt']),
    );
  }
  @override
  Map<String, dynamic> toJson({ValueSerializer? serializer}) {
    serializer ??= driftRuntimeOptions.defaultSerializer;
    return <String, dynamic>{
      'id': serializer.toJson<String>(id),
      'scopeId': serializer.toJson<String>(scopeId),
      'feedId': serializer.toJson<String>(feedId),
      'payloadJson': serializer.toJson<String>(payloadJson),
      'fetchedAt': serializer.toJson<DateTime?>(fetchedAt),
      'cachedAt': serializer.toJson<DateTime>(cachedAt),
    };
  }

  LocalRssEntry copyWith({
    String? id,
    String? scopeId,
    String? feedId,
    String? payloadJson,
    Value<DateTime?> fetchedAt = const Value.absent(),
    DateTime? cachedAt,
  }) => LocalRssEntry(
    id: id ?? this.id,
    scopeId: scopeId ?? this.scopeId,
    feedId: feedId ?? this.feedId,
    payloadJson: payloadJson ?? this.payloadJson,
    fetchedAt: fetchedAt.present ? fetchedAt.value : this.fetchedAt,
    cachedAt: cachedAt ?? this.cachedAt,
  );
  LocalRssEntry copyWithCompanion(RssEntriesCacheCompanion data) {
    return LocalRssEntry(
      id: data.id.present ? data.id.value : this.id,
      scopeId: data.scopeId.present ? data.scopeId.value : this.scopeId,
      feedId: data.feedId.present ? data.feedId.value : this.feedId,
      payloadJson: data.payloadJson.present
          ? data.payloadJson.value
          : this.payloadJson,
      fetchedAt: data.fetchedAt.present ? data.fetchedAt.value : this.fetchedAt,
      cachedAt: data.cachedAt.present ? data.cachedAt.value : this.cachedAt,
    );
  }

  @override
  String toString() {
    return (StringBuffer('LocalRssEntry(')
          ..write('id: $id, ')
          ..write('scopeId: $scopeId, ')
          ..write('feedId: $feedId, ')
          ..write('payloadJson: $payloadJson, ')
          ..write('fetchedAt: $fetchedAt, ')
          ..write('cachedAt: $cachedAt')
          ..write(')'))
        .toString();
  }

  @override
  int get hashCode =>
      Object.hash(id, scopeId, feedId, payloadJson, fetchedAt, cachedAt);
  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      (other is LocalRssEntry &&
          other.id == this.id &&
          other.scopeId == this.scopeId &&
          other.feedId == this.feedId &&
          other.payloadJson == this.payloadJson &&
          other.fetchedAt == this.fetchedAt &&
          other.cachedAt == this.cachedAt);
}

class RssEntriesCacheCompanion extends UpdateCompanion<LocalRssEntry> {
  final Value<String> id;
  final Value<String> scopeId;
  final Value<String> feedId;
  final Value<String> payloadJson;
  final Value<DateTime?> fetchedAt;
  final Value<DateTime> cachedAt;
  final Value<int> rowid;
  const RssEntriesCacheCompanion({
    this.id = const Value.absent(),
    this.scopeId = const Value.absent(),
    this.feedId = const Value.absent(),
    this.payloadJson = const Value.absent(),
    this.fetchedAt = const Value.absent(),
    this.cachedAt = const Value.absent(),
    this.rowid = const Value.absent(),
  });
  RssEntriesCacheCompanion.insert({
    required String id,
    required String scopeId,
    required String feedId,
    required String payloadJson,
    this.fetchedAt = const Value.absent(),
    required DateTime cachedAt,
    this.rowid = const Value.absent(),
  }) : id = Value(id),
       scopeId = Value(scopeId),
       feedId = Value(feedId),
       payloadJson = Value(payloadJson),
       cachedAt = Value(cachedAt);
  static Insertable<LocalRssEntry> custom({
    Expression<String>? id,
    Expression<String>? scopeId,
    Expression<String>? feedId,
    Expression<String>? payloadJson,
    Expression<DateTime>? fetchedAt,
    Expression<DateTime>? cachedAt,
    Expression<int>? rowid,
  }) {
    return RawValuesInsertable({
      if (id != null) 'id': id,
      if (scopeId != null) 'scope_id': scopeId,
      if (feedId != null) 'feed_id': feedId,
      if (payloadJson != null) 'payload_json': payloadJson,
      if (fetchedAt != null) 'fetched_at': fetchedAt,
      if (cachedAt != null) 'cached_at': cachedAt,
      if (rowid != null) 'rowid': rowid,
    });
  }

  RssEntriesCacheCompanion copyWith({
    Value<String>? id,
    Value<String>? scopeId,
    Value<String>? feedId,
    Value<String>? payloadJson,
    Value<DateTime?>? fetchedAt,
    Value<DateTime>? cachedAt,
    Value<int>? rowid,
  }) {
    return RssEntriesCacheCompanion(
      id: id ?? this.id,
      scopeId: scopeId ?? this.scopeId,
      feedId: feedId ?? this.feedId,
      payloadJson: payloadJson ?? this.payloadJson,
      fetchedAt: fetchedAt ?? this.fetchedAt,
      cachedAt: cachedAt ?? this.cachedAt,
      rowid: rowid ?? this.rowid,
    );
  }

  @override
  Map<String, Expression> toColumns(bool nullToAbsent) {
    final map = <String, Expression>{};
    if (id.present) {
      map['id'] = Variable<String>(id.value);
    }
    if (scopeId.present) {
      map['scope_id'] = Variable<String>(scopeId.value);
    }
    if (feedId.present) {
      map['feed_id'] = Variable<String>(feedId.value);
    }
    if (payloadJson.present) {
      map['payload_json'] = Variable<String>(payloadJson.value);
    }
    if (fetchedAt.present) {
      map['fetched_at'] = Variable<DateTime>(fetchedAt.value);
    }
    if (cachedAt.present) {
      map['cached_at'] = Variable<DateTime>(cachedAt.value);
    }
    if (rowid.present) {
      map['rowid'] = Variable<int>(rowid.value);
    }
    return map;
  }

  @override
  String toString() {
    return (StringBuffer('RssEntriesCacheCompanion(')
          ..write('id: $id, ')
          ..write('scopeId: $scopeId, ')
          ..write('feedId: $feedId, ')
          ..write('payloadJson: $payloadJson, ')
          ..write('fetchedAt: $fetchedAt, ')
          ..write('cachedAt: $cachedAt, ')
          ..write('rowid: $rowid')
          ..write(')'))
        .toString();
  }
}

abstract class _$AppDb extends GeneratedDatabase {
  _$AppDb(QueryExecutor e) : super(e);
  $AppDbManager get managers => $AppDbManager(this);
  late final $WikiProjectsTable wikiProjects = $WikiProjectsTable(this);
  late final $WikiPagesTable wikiPages = $WikiPagesTable(this);
  late final $WikiBlocksTable wikiBlocks = $WikiBlocksTable(this);
  late final $WikiOutboxTable wikiOutbox = $WikiOutboxTable(this);
  late final $NoteNotebooksTable noteNotebooks = $NoteNotebooksTable(this);
  late final $NoteNotesTable noteNotes = $NoteNotesTable(this);
  late final $NoteTagsTable noteTags = $NoteTagsTable(this);
  late final $NoteNoteTagsTable noteNoteTags = $NoteNoteTagsTable(this);
  late final $NoteOutboxTable noteOutbox = $NoteOutboxTable(this);
  late final $CodeTasksTable codeTasks = $CodeTasksTable(this);
  late final $CodeProjectsTable codeProjects = $CodeProjectsTable(this);
  late final $CodeTaskArtifactsTable codeTaskArtifacts =
      $CodeTaskArtifactsTable(this);
  late final $ChatThreadsV2Table chatThreadsV2 = $ChatThreadsV2Table(this);
  late final $ChatMessagesV2Table chatMessagesV2 = $ChatMessagesV2Table(this);
  late final $ChatContentBlocksTable chatContentBlocks =
      $ChatContentBlocksTable(this);
  late final $ChatSessionsTable chatSessions = $ChatSessionsTable(this);
  late final $MessageReactionsV2Table messageReactionsV2 =
      $MessageReactionsV2Table(this);
  late final $AigcTasksTable aigcTasks = $AigcTasksTable(this);
  late final $SseCursorsTable sseCursors = $SseCursorsTable(this);
  late final $RssFeedsCacheTable rssFeedsCache = $RssFeedsCacheTable(this);
  late final $RssEntriesCacheTable rssEntriesCache = $RssEntriesCacheTable(
    this,
  );
  @override
  Iterable<TableInfo<Table, Object?>> get allTables =>
      allSchemaEntities.whereType<TableInfo<Table, Object?>>();
  @override
  List<DatabaseSchemaEntity> get allSchemaEntities => [
    wikiProjects,
    wikiPages,
    wikiBlocks,
    wikiOutbox,
    noteNotebooks,
    noteNotes,
    noteTags,
    noteNoteTags,
    noteOutbox,
    codeTasks,
    codeProjects,
    codeTaskArtifacts,
    chatThreadsV2,
    chatMessagesV2,
    chatContentBlocks,
    chatSessions,
    messageReactionsV2,
    aigcTasks,
    sseCursors,
    rssFeedsCache,
    rssEntriesCache,
  ];
}

typedef $$WikiProjectsTableCreateCompanionBuilder =
    WikiProjectsCompanion Function({
      required String id,
      required String name,
      required DateTime updatedAt,
      Value<int> rowid,
    });
typedef $$WikiProjectsTableUpdateCompanionBuilder =
    WikiProjectsCompanion Function({
      Value<String> id,
      Value<String> name,
      Value<DateTime> updatedAt,
      Value<int> rowid,
    });

class $$WikiProjectsTableFilterComposer
    extends Composer<_$AppDb, $WikiProjectsTable> {
  $$WikiProjectsTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get name => $composableBuilder(
    column: $table.name,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnFilters(column),
  );
}

class $$WikiProjectsTableOrderingComposer
    extends Composer<_$AppDb, $WikiProjectsTable> {
  $$WikiProjectsTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get name => $composableBuilder(
    column: $table.name,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$WikiProjectsTableAnnotationComposer
    extends Composer<_$AppDb, $WikiProjectsTable> {
  $$WikiProjectsTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get name =>
      $composableBuilder(column: $table.name, builder: (column) => column);

  GeneratedColumn<DateTime> get updatedAt =>
      $composableBuilder(column: $table.updatedAt, builder: (column) => column);
}

class $$WikiProjectsTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $WikiProjectsTable,
          LocalWikiProject,
          $$WikiProjectsTableFilterComposer,
          $$WikiProjectsTableOrderingComposer,
          $$WikiProjectsTableAnnotationComposer,
          $$WikiProjectsTableCreateCompanionBuilder,
          $$WikiProjectsTableUpdateCompanionBuilder,
          (
            LocalWikiProject,
            BaseReferences<_$AppDb, $WikiProjectsTable, LocalWikiProject>,
          ),
          LocalWikiProject,
          PrefetchHooks Function()
        > {
  $$WikiProjectsTableTableManager(_$AppDb db, $WikiProjectsTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$WikiProjectsTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$WikiProjectsTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$WikiProjectsTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> id = const Value.absent(),
                Value<String> name = const Value.absent(),
                Value<DateTime> updatedAt = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => WikiProjectsCompanion(
                id: id,
                name: name,
                updatedAt: updatedAt,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String id,
                required String name,
                required DateTime updatedAt,
                Value<int> rowid = const Value.absent(),
              }) => WikiProjectsCompanion.insert(
                id: id,
                name: name,
                updatedAt: updatedAt,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$WikiProjectsTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $WikiProjectsTable,
      LocalWikiProject,
      $$WikiProjectsTableFilterComposer,
      $$WikiProjectsTableOrderingComposer,
      $$WikiProjectsTableAnnotationComposer,
      $$WikiProjectsTableCreateCompanionBuilder,
      $$WikiProjectsTableUpdateCompanionBuilder,
      (
        LocalWikiProject,
        BaseReferences<_$AppDb, $WikiProjectsTable, LocalWikiProject>,
      ),
      LocalWikiProject,
      PrefetchHooks Function()
    >;
typedef $$WikiPagesTableCreateCompanionBuilder =
    WikiPagesCompanion Function({
      required String id,
      required String projectId,
      Value<String> title,
      Value<int> version,
      Value<String?> parentId,
      required DateTime updatedAt,
      Value<int> rowid,
    });
typedef $$WikiPagesTableUpdateCompanionBuilder =
    WikiPagesCompanion Function({
      Value<String> id,
      Value<String> projectId,
      Value<String> title,
      Value<int> version,
      Value<String?> parentId,
      Value<DateTime> updatedAt,
      Value<int> rowid,
    });

class $$WikiPagesTableFilterComposer
    extends Composer<_$AppDb, $WikiPagesTable> {
  $$WikiPagesTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get projectId => $composableBuilder(
    column: $table.projectId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get title => $composableBuilder(
    column: $table.title,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get version => $composableBuilder(
    column: $table.version,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get parentId => $composableBuilder(
    column: $table.parentId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnFilters(column),
  );
}

class $$WikiPagesTableOrderingComposer
    extends Composer<_$AppDb, $WikiPagesTable> {
  $$WikiPagesTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get projectId => $composableBuilder(
    column: $table.projectId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get title => $composableBuilder(
    column: $table.title,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get version => $composableBuilder(
    column: $table.version,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get parentId => $composableBuilder(
    column: $table.parentId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$WikiPagesTableAnnotationComposer
    extends Composer<_$AppDb, $WikiPagesTable> {
  $$WikiPagesTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get projectId =>
      $composableBuilder(column: $table.projectId, builder: (column) => column);

  GeneratedColumn<String> get title =>
      $composableBuilder(column: $table.title, builder: (column) => column);

  GeneratedColumn<int> get version =>
      $composableBuilder(column: $table.version, builder: (column) => column);

  GeneratedColumn<String> get parentId =>
      $composableBuilder(column: $table.parentId, builder: (column) => column);

  GeneratedColumn<DateTime> get updatedAt =>
      $composableBuilder(column: $table.updatedAt, builder: (column) => column);
}

class $$WikiPagesTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $WikiPagesTable,
          LocalWikiPage,
          $$WikiPagesTableFilterComposer,
          $$WikiPagesTableOrderingComposer,
          $$WikiPagesTableAnnotationComposer,
          $$WikiPagesTableCreateCompanionBuilder,
          $$WikiPagesTableUpdateCompanionBuilder,
          (
            LocalWikiPage,
            BaseReferences<_$AppDb, $WikiPagesTable, LocalWikiPage>,
          ),
          LocalWikiPage,
          PrefetchHooks Function()
        > {
  $$WikiPagesTableTableManager(_$AppDb db, $WikiPagesTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$WikiPagesTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$WikiPagesTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$WikiPagesTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> id = const Value.absent(),
                Value<String> projectId = const Value.absent(),
                Value<String> title = const Value.absent(),
                Value<int> version = const Value.absent(),
                Value<String?> parentId = const Value.absent(),
                Value<DateTime> updatedAt = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => WikiPagesCompanion(
                id: id,
                projectId: projectId,
                title: title,
                version: version,
                parentId: parentId,
                updatedAt: updatedAt,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String id,
                required String projectId,
                Value<String> title = const Value.absent(),
                Value<int> version = const Value.absent(),
                Value<String?> parentId = const Value.absent(),
                required DateTime updatedAt,
                Value<int> rowid = const Value.absent(),
              }) => WikiPagesCompanion.insert(
                id: id,
                projectId: projectId,
                title: title,
                version: version,
                parentId: parentId,
                updatedAt: updatedAt,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$WikiPagesTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $WikiPagesTable,
      LocalWikiPage,
      $$WikiPagesTableFilterComposer,
      $$WikiPagesTableOrderingComposer,
      $$WikiPagesTableAnnotationComposer,
      $$WikiPagesTableCreateCompanionBuilder,
      $$WikiPagesTableUpdateCompanionBuilder,
      (LocalWikiPage, BaseReferences<_$AppDb, $WikiPagesTable, LocalWikiPage>),
      LocalWikiPage,
      PrefetchHooks Function()
    >;
typedef $$WikiBlocksTableCreateCompanionBuilder =
    WikiBlocksCompanion Function({
      required String id,
      required String pageId,
      required double position,
      required String type,
      required String contentJson,
      Value<int> version,
      Value<bool> deleted,
      required DateTime updatedAt,
      Value<int> rowid,
    });
typedef $$WikiBlocksTableUpdateCompanionBuilder =
    WikiBlocksCompanion Function({
      Value<String> id,
      Value<String> pageId,
      Value<double> position,
      Value<String> type,
      Value<String> contentJson,
      Value<int> version,
      Value<bool> deleted,
      Value<DateTime> updatedAt,
      Value<int> rowid,
    });

class $$WikiBlocksTableFilterComposer
    extends Composer<_$AppDb, $WikiBlocksTable> {
  $$WikiBlocksTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get pageId => $composableBuilder(
    column: $table.pageId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<double> get position => $composableBuilder(
    column: $table.position,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get type => $composableBuilder(
    column: $table.type,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get contentJson => $composableBuilder(
    column: $table.contentJson,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get version => $composableBuilder(
    column: $table.version,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<bool> get deleted => $composableBuilder(
    column: $table.deleted,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnFilters(column),
  );
}

class $$WikiBlocksTableOrderingComposer
    extends Composer<_$AppDb, $WikiBlocksTable> {
  $$WikiBlocksTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get pageId => $composableBuilder(
    column: $table.pageId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<double> get position => $composableBuilder(
    column: $table.position,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get type => $composableBuilder(
    column: $table.type,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get contentJson => $composableBuilder(
    column: $table.contentJson,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get version => $composableBuilder(
    column: $table.version,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<bool> get deleted => $composableBuilder(
    column: $table.deleted,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$WikiBlocksTableAnnotationComposer
    extends Composer<_$AppDb, $WikiBlocksTable> {
  $$WikiBlocksTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get pageId =>
      $composableBuilder(column: $table.pageId, builder: (column) => column);

  GeneratedColumn<double> get position =>
      $composableBuilder(column: $table.position, builder: (column) => column);

  GeneratedColumn<String> get type =>
      $composableBuilder(column: $table.type, builder: (column) => column);

  GeneratedColumn<String> get contentJson => $composableBuilder(
    column: $table.contentJson,
    builder: (column) => column,
  );

  GeneratedColumn<int> get version =>
      $composableBuilder(column: $table.version, builder: (column) => column);

  GeneratedColumn<bool> get deleted =>
      $composableBuilder(column: $table.deleted, builder: (column) => column);

  GeneratedColumn<DateTime> get updatedAt =>
      $composableBuilder(column: $table.updatedAt, builder: (column) => column);
}

class $$WikiBlocksTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $WikiBlocksTable,
          LocalWikiBlock,
          $$WikiBlocksTableFilterComposer,
          $$WikiBlocksTableOrderingComposer,
          $$WikiBlocksTableAnnotationComposer,
          $$WikiBlocksTableCreateCompanionBuilder,
          $$WikiBlocksTableUpdateCompanionBuilder,
          (
            LocalWikiBlock,
            BaseReferences<_$AppDb, $WikiBlocksTable, LocalWikiBlock>,
          ),
          LocalWikiBlock,
          PrefetchHooks Function()
        > {
  $$WikiBlocksTableTableManager(_$AppDb db, $WikiBlocksTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$WikiBlocksTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$WikiBlocksTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$WikiBlocksTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> id = const Value.absent(),
                Value<String> pageId = const Value.absent(),
                Value<double> position = const Value.absent(),
                Value<String> type = const Value.absent(),
                Value<String> contentJson = const Value.absent(),
                Value<int> version = const Value.absent(),
                Value<bool> deleted = const Value.absent(),
                Value<DateTime> updatedAt = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => WikiBlocksCompanion(
                id: id,
                pageId: pageId,
                position: position,
                type: type,
                contentJson: contentJson,
                version: version,
                deleted: deleted,
                updatedAt: updatedAt,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String id,
                required String pageId,
                required double position,
                required String type,
                required String contentJson,
                Value<int> version = const Value.absent(),
                Value<bool> deleted = const Value.absent(),
                required DateTime updatedAt,
                Value<int> rowid = const Value.absent(),
              }) => WikiBlocksCompanion.insert(
                id: id,
                pageId: pageId,
                position: position,
                type: type,
                contentJson: contentJson,
                version: version,
                deleted: deleted,
                updatedAt: updatedAt,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$WikiBlocksTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $WikiBlocksTable,
      LocalWikiBlock,
      $$WikiBlocksTableFilterComposer,
      $$WikiBlocksTableOrderingComposer,
      $$WikiBlocksTableAnnotationComposer,
      $$WikiBlocksTableCreateCompanionBuilder,
      $$WikiBlocksTableUpdateCompanionBuilder,
      (
        LocalWikiBlock,
        BaseReferences<_$AppDb, $WikiBlocksTable, LocalWikiBlock>,
      ),
      LocalWikiBlock,
      PrefetchHooks Function()
    >;
typedef $$WikiOutboxTableCreateCompanionBuilder =
    WikiOutboxCompanion Function({
      Value<int> id,
      required String op,
      required String entityId,
      Value<String?> projectId,
      Value<String?> pageId,
      required String payloadJson,
      Value<int?> baseVersion,
      Value<int> attempts,
      Value<String?> lastError,
      required DateTime createdAt,
      required DateTime nextAttemptAt,
    });
typedef $$WikiOutboxTableUpdateCompanionBuilder =
    WikiOutboxCompanion Function({
      Value<int> id,
      Value<String> op,
      Value<String> entityId,
      Value<String?> projectId,
      Value<String?> pageId,
      Value<String> payloadJson,
      Value<int?> baseVersion,
      Value<int> attempts,
      Value<String?> lastError,
      Value<DateTime> createdAt,
      Value<DateTime> nextAttemptAt,
    });

class $$WikiOutboxTableFilterComposer
    extends Composer<_$AppDb, $WikiOutboxTable> {
  $$WikiOutboxTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<int> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get op => $composableBuilder(
    column: $table.op,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get entityId => $composableBuilder(
    column: $table.entityId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get projectId => $composableBuilder(
    column: $table.projectId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get pageId => $composableBuilder(
    column: $table.pageId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get payloadJson => $composableBuilder(
    column: $table.payloadJson,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get baseVersion => $composableBuilder(
    column: $table.baseVersion,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get attempts => $composableBuilder(
    column: $table.attempts,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get lastError => $composableBuilder(
    column: $table.lastError,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get nextAttemptAt => $composableBuilder(
    column: $table.nextAttemptAt,
    builder: (column) => ColumnFilters(column),
  );
}

class $$WikiOutboxTableOrderingComposer
    extends Composer<_$AppDb, $WikiOutboxTable> {
  $$WikiOutboxTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<int> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get op => $composableBuilder(
    column: $table.op,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get entityId => $composableBuilder(
    column: $table.entityId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get projectId => $composableBuilder(
    column: $table.projectId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get pageId => $composableBuilder(
    column: $table.pageId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get payloadJson => $composableBuilder(
    column: $table.payloadJson,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get baseVersion => $composableBuilder(
    column: $table.baseVersion,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get attempts => $composableBuilder(
    column: $table.attempts,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get lastError => $composableBuilder(
    column: $table.lastError,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get nextAttemptAt => $composableBuilder(
    column: $table.nextAttemptAt,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$WikiOutboxTableAnnotationComposer
    extends Composer<_$AppDb, $WikiOutboxTable> {
  $$WikiOutboxTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<int> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get op =>
      $composableBuilder(column: $table.op, builder: (column) => column);

  GeneratedColumn<String> get entityId =>
      $composableBuilder(column: $table.entityId, builder: (column) => column);

  GeneratedColumn<String> get projectId =>
      $composableBuilder(column: $table.projectId, builder: (column) => column);

  GeneratedColumn<String> get pageId =>
      $composableBuilder(column: $table.pageId, builder: (column) => column);

  GeneratedColumn<String> get payloadJson => $composableBuilder(
    column: $table.payloadJson,
    builder: (column) => column,
  );

  GeneratedColumn<int> get baseVersion => $composableBuilder(
    column: $table.baseVersion,
    builder: (column) => column,
  );

  GeneratedColumn<int> get attempts =>
      $composableBuilder(column: $table.attempts, builder: (column) => column);

  GeneratedColumn<String> get lastError =>
      $composableBuilder(column: $table.lastError, builder: (column) => column);

  GeneratedColumn<DateTime> get createdAt =>
      $composableBuilder(column: $table.createdAt, builder: (column) => column);

  GeneratedColumn<DateTime> get nextAttemptAt => $composableBuilder(
    column: $table.nextAttemptAt,
    builder: (column) => column,
  );
}

class $$WikiOutboxTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $WikiOutboxTable,
          OutboxEntry,
          $$WikiOutboxTableFilterComposer,
          $$WikiOutboxTableOrderingComposer,
          $$WikiOutboxTableAnnotationComposer,
          $$WikiOutboxTableCreateCompanionBuilder,
          $$WikiOutboxTableUpdateCompanionBuilder,
          (OutboxEntry, BaseReferences<_$AppDb, $WikiOutboxTable, OutboxEntry>),
          OutboxEntry,
          PrefetchHooks Function()
        > {
  $$WikiOutboxTableTableManager(_$AppDb db, $WikiOutboxTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$WikiOutboxTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$WikiOutboxTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$WikiOutboxTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<int> id = const Value.absent(),
                Value<String> op = const Value.absent(),
                Value<String> entityId = const Value.absent(),
                Value<String?> projectId = const Value.absent(),
                Value<String?> pageId = const Value.absent(),
                Value<String> payloadJson = const Value.absent(),
                Value<int?> baseVersion = const Value.absent(),
                Value<int> attempts = const Value.absent(),
                Value<String?> lastError = const Value.absent(),
                Value<DateTime> createdAt = const Value.absent(),
                Value<DateTime> nextAttemptAt = const Value.absent(),
              }) => WikiOutboxCompanion(
                id: id,
                op: op,
                entityId: entityId,
                projectId: projectId,
                pageId: pageId,
                payloadJson: payloadJson,
                baseVersion: baseVersion,
                attempts: attempts,
                lastError: lastError,
                createdAt: createdAt,
                nextAttemptAt: nextAttemptAt,
              ),
          createCompanionCallback:
              ({
                Value<int> id = const Value.absent(),
                required String op,
                required String entityId,
                Value<String?> projectId = const Value.absent(),
                Value<String?> pageId = const Value.absent(),
                required String payloadJson,
                Value<int?> baseVersion = const Value.absent(),
                Value<int> attempts = const Value.absent(),
                Value<String?> lastError = const Value.absent(),
                required DateTime createdAt,
                required DateTime nextAttemptAt,
              }) => WikiOutboxCompanion.insert(
                id: id,
                op: op,
                entityId: entityId,
                projectId: projectId,
                pageId: pageId,
                payloadJson: payloadJson,
                baseVersion: baseVersion,
                attempts: attempts,
                lastError: lastError,
                createdAt: createdAt,
                nextAttemptAt: nextAttemptAt,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$WikiOutboxTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $WikiOutboxTable,
      OutboxEntry,
      $$WikiOutboxTableFilterComposer,
      $$WikiOutboxTableOrderingComposer,
      $$WikiOutboxTableAnnotationComposer,
      $$WikiOutboxTableCreateCompanionBuilder,
      $$WikiOutboxTableUpdateCompanionBuilder,
      (OutboxEntry, BaseReferences<_$AppDb, $WikiOutboxTable, OutboxEntry>),
      OutboxEntry,
      PrefetchHooks Function()
    >;
typedef $$NoteNotebooksTableCreateCompanionBuilder =
    NoteNotebooksCompanion Function({
      required String id,
      required String name,
      Value<double> position,
      required DateTime updatedAt,
      Value<int> rowid,
    });
typedef $$NoteNotebooksTableUpdateCompanionBuilder =
    NoteNotebooksCompanion Function({
      Value<String> id,
      Value<String> name,
      Value<double> position,
      Value<DateTime> updatedAt,
      Value<int> rowid,
    });

class $$NoteNotebooksTableFilterComposer
    extends Composer<_$AppDb, $NoteNotebooksTable> {
  $$NoteNotebooksTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get name => $composableBuilder(
    column: $table.name,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<double> get position => $composableBuilder(
    column: $table.position,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnFilters(column),
  );
}

class $$NoteNotebooksTableOrderingComposer
    extends Composer<_$AppDb, $NoteNotebooksTable> {
  $$NoteNotebooksTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get name => $composableBuilder(
    column: $table.name,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<double> get position => $composableBuilder(
    column: $table.position,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$NoteNotebooksTableAnnotationComposer
    extends Composer<_$AppDb, $NoteNotebooksTable> {
  $$NoteNotebooksTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get name =>
      $composableBuilder(column: $table.name, builder: (column) => column);

  GeneratedColumn<double> get position =>
      $composableBuilder(column: $table.position, builder: (column) => column);

  GeneratedColumn<DateTime> get updatedAt =>
      $composableBuilder(column: $table.updatedAt, builder: (column) => column);
}

class $$NoteNotebooksTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $NoteNotebooksTable,
          LocalNoteNotebook,
          $$NoteNotebooksTableFilterComposer,
          $$NoteNotebooksTableOrderingComposer,
          $$NoteNotebooksTableAnnotationComposer,
          $$NoteNotebooksTableCreateCompanionBuilder,
          $$NoteNotebooksTableUpdateCompanionBuilder,
          (
            LocalNoteNotebook,
            BaseReferences<_$AppDb, $NoteNotebooksTable, LocalNoteNotebook>,
          ),
          LocalNoteNotebook,
          PrefetchHooks Function()
        > {
  $$NoteNotebooksTableTableManager(_$AppDb db, $NoteNotebooksTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$NoteNotebooksTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$NoteNotebooksTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$NoteNotebooksTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> id = const Value.absent(),
                Value<String> name = const Value.absent(),
                Value<double> position = const Value.absent(),
                Value<DateTime> updatedAt = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => NoteNotebooksCompanion(
                id: id,
                name: name,
                position: position,
                updatedAt: updatedAt,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String id,
                required String name,
                Value<double> position = const Value.absent(),
                required DateTime updatedAt,
                Value<int> rowid = const Value.absent(),
              }) => NoteNotebooksCompanion.insert(
                id: id,
                name: name,
                position: position,
                updatedAt: updatedAt,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$NoteNotebooksTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $NoteNotebooksTable,
      LocalNoteNotebook,
      $$NoteNotebooksTableFilterComposer,
      $$NoteNotebooksTableOrderingComposer,
      $$NoteNotebooksTableAnnotationComposer,
      $$NoteNotebooksTableCreateCompanionBuilder,
      $$NoteNotebooksTableUpdateCompanionBuilder,
      (
        LocalNoteNotebook,
        BaseReferences<_$AppDb, $NoteNotebooksTable, LocalNoteNotebook>,
      ),
      LocalNoteNotebook,
      PrefetchHooks Function()
    >;
typedef $$NoteNotesTableCreateCompanionBuilder =
    NoteNotesCompanion Function({
      required String id,
      Value<String?> notebookId,
      Value<String> title,
      Value<String> contentMd,
      Value<bool> isTodo,
      Value<DateTime?> todoCompletedAt,
      Value<double> position,
      Value<int> version,
      Value<bool> trashed,
      Value<DateTime?> trashedAt,
      Value<DateTime?> archivedAt,
      Value<String?> promotedPageId,
      required DateTime updatedAt,
      Value<int> rowid,
    });
typedef $$NoteNotesTableUpdateCompanionBuilder =
    NoteNotesCompanion Function({
      Value<String> id,
      Value<String?> notebookId,
      Value<String> title,
      Value<String> contentMd,
      Value<bool> isTodo,
      Value<DateTime?> todoCompletedAt,
      Value<double> position,
      Value<int> version,
      Value<bool> trashed,
      Value<DateTime?> trashedAt,
      Value<DateTime?> archivedAt,
      Value<String?> promotedPageId,
      Value<DateTime> updatedAt,
      Value<int> rowid,
    });

class $$NoteNotesTableFilterComposer
    extends Composer<_$AppDb, $NoteNotesTable> {
  $$NoteNotesTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get notebookId => $composableBuilder(
    column: $table.notebookId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get title => $composableBuilder(
    column: $table.title,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get contentMd => $composableBuilder(
    column: $table.contentMd,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<bool> get isTodo => $composableBuilder(
    column: $table.isTodo,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get todoCompletedAt => $composableBuilder(
    column: $table.todoCompletedAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<double> get position => $composableBuilder(
    column: $table.position,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get version => $composableBuilder(
    column: $table.version,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<bool> get trashed => $composableBuilder(
    column: $table.trashed,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get trashedAt => $composableBuilder(
    column: $table.trashedAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get archivedAt => $composableBuilder(
    column: $table.archivedAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get promotedPageId => $composableBuilder(
    column: $table.promotedPageId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnFilters(column),
  );
}

class $$NoteNotesTableOrderingComposer
    extends Composer<_$AppDb, $NoteNotesTable> {
  $$NoteNotesTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get notebookId => $composableBuilder(
    column: $table.notebookId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get title => $composableBuilder(
    column: $table.title,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get contentMd => $composableBuilder(
    column: $table.contentMd,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<bool> get isTodo => $composableBuilder(
    column: $table.isTodo,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get todoCompletedAt => $composableBuilder(
    column: $table.todoCompletedAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<double> get position => $composableBuilder(
    column: $table.position,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get version => $composableBuilder(
    column: $table.version,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<bool> get trashed => $composableBuilder(
    column: $table.trashed,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get trashedAt => $composableBuilder(
    column: $table.trashedAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get archivedAt => $composableBuilder(
    column: $table.archivedAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get promotedPageId => $composableBuilder(
    column: $table.promotedPageId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$NoteNotesTableAnnotationComposer
    extends Composer<_$AppDb, $NoteNotesTable> {
  $$NoteNotesTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get notebookId => $composableBuilder(
    column: $table.notebookId,
    builder: (column) => column,
  );

  GeneratedColumn<String> get title =>
      $composableBuilder(column: $table.title, builder: (column) => column);

  GeneratedColumn<String> get contentMd =>
      $composableBuilder(column: $table.contentMd, builder: (column) => column);

  GeneratedColumn<bool> get isTodo =>
      $composableBuilder(column: $table.isTodo, builder: (column) => column);

  GeneratedColumn<DateTime> get todoCompletedAt => $composableBuilder(
    column: $table.todoCompletedAt,
    builder: (column) => column,
  );

  GeneratedColumn<double> get position =>
      $composableBuilder(column: $table.position, builder: (column) => column);

  GeneratedColumn<int> get version =>
      $composableBuilder(column: $table.version, builder: (column) => column);

  GeneratedColumn<bool> get trashed =>
      $composableBuilder(column: $table.trashed, builder: (column) => column);

  GeneratedColumn<DateTime> get trashedAt =>
      $composableBuilder(column: $table.trashedAt, builder: (column) => column);

  GeneratedColumn<DateTime> get archivedAt => $composableBuilder(
    column: $table.archivedAt,
    builder: (column) => column,
  );

  GeneratedColumn<String> get promotedPageId => $composableBuilder(
    column: $table.promotedPageId,
    builder: (column) => column,
  );

  GeneratedColumn<DateTime> get updatedAt =>
      $composableBuilder(column: $table.updatedAt, builder: (column) => column);
}

class $$NoteNotesTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $NoteNotesTable,
          LocalNote,
          $$NoteNotesTableFilterComposer,
          $$NoteNotesTableOrderingComposer,
          $$NoteNotesTableAnnotationComposer,
          $$NoteNotesTableCreateCompanionBuilder,
          $$NoteNotesTableUpdateCompanionBuilder,
          (LocalNote, BaseReferences<_$AppDb, $NoteNotesTable, LocalNote>),
          LocalNote,
          PrefetchHooks Function()
        > {
  $$NoteNotesTableTableManager(_$AppDb db, $NoteNotesTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$NoteNotesTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$NoteNotesTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$NoteNotesTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> id = const Value.absent(),
                Value<String?> notebookId = const Value.absent(),
                Value<String> title = const Value.absent(),
                Value<String> contentMd = const Value.absent(),
                Value<bool> isTodo = const Value.absent(),
                Value<DateTime?> todoCompletedAt = const Value.absent(),
                Value<double> position = const Value.absent(),
                Value<int> version = const Value.absent(),
                Value<bool> trashed = const Value.absent(),
                Value<DateTime?> trashedAt = const Value.absent(),
                Value<DateTime?> archivedAt = const Value.absent(),
                Value<String?> promotedPageId = const Value.absent(),
                Value<DateTime> updatedAt = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => NoteNotesCompanion(
                id: id,
                notebookId: notebookId,
                title: title,
                contentMd: contentMd,
                isTodo: isTodo,
                todoCompletedAt: todoCompletedAt,
                position: position,
                version: version,
                trashed: trashed,
                trashedAt: trashedAt,
                archivedAt: archivedAt,
                promotedPageId: promotedPageId,
                updatedAt: updatedAt,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String id,
                Value<String?> notebookId = const Value.absent(),
                Value<String> title = const Value.absent(),
                Value<String> contentMd = const Value.absent(),
                Value<bool> isTodo = const Value.absent(),
                Value<DateTime?> todoCompletedAt = const Value.absent(),
                Value<double> position = const Value.absent(),
                Value<int> version = const Value.absent(),
                Value<bool> trashed = const Value.absent(),
                Value<DateTime?> trashedAt = const Value.absent(),
                Value<DateTime?> archivedAt = const Value.absent(),
                Value<String?> promotedPageId = const Value.absent(),
                required DateTime updatedAt,
                Value<int> rowid = const Value.absent(),
              }) => NoteNotesCompanion.insert(
                id: id,
                notebookId: notebookId,
                title: title,
                contentMd: contentMd,
                isTodo: isTodo,
                todoCompletedAt: todoCompletedAt,
                position: position,
                version: version,
                trashed: trashed,
                trashedAt: trashedAt,
                archivedAt: archivedAt,
                promotedPageId: promotedPageId,
                updatedAt: updatedAt,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$NoteNotesTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $NoteNotesTable,
      LocalNote,
      $$NoteNotesTableFilterComposer,
      $$NoteNotesTableOrderingComposer,
      $$NoteNotesTableAnnotationComposer,
      $$NoteNotesTableCreateCompanionBuilder,
      $$NoteNotesTableUpdateCompanionBuilder,
      (LocalNote, BaseReferences<_$AppDb, $NoteNotesTable, LocalNote>),
      LocalNote,
      PrefetchHooks Function()
    >;
typedef $$NoteTagsTableCreateCompanionBuilder =
    NoteTagsCompanion Function({
      required String id,
      required String name,
      Value<int> rowid,
    });
typedef $$NoteTagsTableUpdateCompanionBuilder =
    NoteTagsCompanion Function({
      Value<String> id,
      Value<String> name,
      Value<int> rowid,
    });

class $$NoteTagsTableFilterComposer extends Composer<_$AppDb, $NoteTagsTable> {
  $$NoteTagsTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get name => $composableBuilder(
    column: $table.name,
    builder: (column) => ColumnFilters(column),
  );
}

class $$NoteTagsTableOrderingComposer
    extends Composer<_$AppDb, $NoteTagsTable> {
  $$NoteTagsTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get name => $composableBuilder(
    column: $table.name,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$NoteTagsTableAnnotationComposer
    extends Composer<_$AppDb, $NoteTagsTable> {
  $$NoteTagsTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get name =>
      $composableBuilder(column: $table.name, builder: (column) => column);
}

class $$NoteTagsTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $NoteTagsTable,
          LocalNoteTag,
          $$NoteTagsTableFilterComposer,
          $$NoteTagsTableOrderingComposer,
          $$NoteTagsTableAnnotationComposer,
          $$NoteTagsTableCreateCompanionBuilder,
          $$NoteTagsTableUpdateCompanionBuilder,
          (LocalNoteTag, BaseReferences<_$AppDb, $NoteTagsTable, LocalNoteTag>),
          LocalNoteTag,
          PrefetchHooks Function()
        > {
  $$NoteTagsTableTableManager(_$AppDb db, $NoteTagsTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$NoteTagsTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$NoteTagsTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$NoteTagsTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> id = const Value.absent(),
                Value<String> name = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => NoteTagsCompanion(id: id, name: name, rowid: rowid),
          createCompanionCallback:
              ({
                required String id,
                required String name,
                Value<int> rowid = const Value.absent(),
              }) => NoteTagsCompanion.insert(id: id, name: name, rowid: rowid),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$NoteTagsTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $NoteTagsTable,
      LocalNoteTag,
      $$NoteTagsTableFilterComposer,
      $$NoteTagsTableOrderingComposer,
      $$NoteTagsTableAnnotationComposer,
      $$NoteTagsTableCreateCompanionBuilder,
      $$NoteTagsTableUpdateCompanionBuilder,
      (LocalNoteTag, BaseReferences<_$AppDb, $NoteTagsTable, LocalNoteTag>),
      LocalNoteTag,
      PrefetchHooks Function()
    >;
typedef $$NoteNoteTagsTableCreateCompanionBuilder =
    NoteNoteTagsCompanion Function({
      required String noteId,
      required String tagId,
      Value<int> rowid,
    });
typedef $$NoteNoteTagsTableUpdateCompanionBuilder =
    NoteNoteTagsCompanion Function({
      Value<String> noteId,
      Value<String> tagId,
      Value<int> rowid,
    });

class $$NoteNoteTagsTableFilterComposer
    extends Composer<_$AppDb, $NoteNoteTagsTable> {
  $$NoteNoteTagsTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get noteId => $composableBuilder(
    column: $table.noteId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get tagId => $composableBuilder(
    column: $table.tagId,
    builder: (column) => ColumnFilters(column),
  );
}

class $$NoteNoteTagsTableOrderingComposer
    extends Composer<_$AppDb, $NoteNoteTagsTable> {
  $$NoteNoteTagsTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get noteId => $composableBuilder(
    column: $table.noteId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get tagId => $composableBuilder(
    column: $table.tagId,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$NoteNoteTagsTableAnnotationComposer
    extends Composer<_$AppDb, $NoteNoteTagsTable> {
  $$NoteNoteTagsTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get noteId =>
      $composableBuilder(column: $table.noteId, builder: (column) => column);

  GeneratedColumn<String> get tagId =>
      $composableBuilder(column: $table.tagId, builder: (column) => column);
}

class $$NoteNoteTagsTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $NoteNoteTagsTable,
          NoteNoteTag,
          $$NoteNoteTagsTableFilterComposer,
          $$NoteNoteTagsTableOrderingComposer,
          $$NoteNoteTagsTableAnnotationComposer,
          $$NoteNoteTagsTableCreateCompanionBuilder,
          $$NoteNoteTagsTableUpdateCompanionBuilder,
          (
            NoteNoteTag,
            BaseReferences<_$AppDb, $NoteNoteTagsTable, NoteNoteTag>,
          ),
          NoteNoteTag,
          PrefetchHooks Function()
        > {
  $$NoteNoteTagsTableTableManager(_$AppDb db, $NoteNoteTagsTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$NoteNoteTagsTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$NoteNoteTagsTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$NoteNoteTagsTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> noteId = const Value.absent(),
                Value<String> tagId = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => NoteNoteTagsCompanion(
                noteId: noteId,
                tagId: tagId,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String noteId,
                required String tagId,
                Value<int> rowid = const Value.absent(),
              }) => NoteNoteTagsCompanion.insert(
                noteId: noteId,
                tagId: tagId,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$NoteNoteTagsTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $NoteNoteTagsTable,
      NoteNoteTag,
      $$NoteNoteTagsTableFilterComposer,
      $$NoteNoteTagsTableOrderingComposer,
      $$NoteNoteTagsTableAnnotationComposer,
      $$NoteNoteTagsTableCreateCompanionBuilder,
      $$NoteNoteTagsTableUpdateCompanionBuilder,
      (NoteNoteTag, BaseReferences<_$AppDb, $NoteNoteTagsTable, NoteNoteTag>),
      NoteNoteTag,
      PrefetchHooks Function()
    >;
typedef $$NoteOutboxTableCreateCompanionBuilder =
    NoteOutboxCompanion Function({
      Value<int> id,
      required String op,
      required String entityId,
      Value<String?> notebookId,
      required String payloadJson,
      Value<int?> baseVersion,
      Value<int> attempts,
      Value<String?> lastError,
      required DateTime createdAt,
      required DateTime nextAttemptAt,
    });
typedef $$NoteOutboxTableUpdateCompanionBuilder =
    NoteOutboxCompanion Function({
      Value<int> id,
      Value<String> op,
      Value<String> entityId,
      Value<String?> notebookId,
      Value<String> payloadJson,
      Value<int?> baseVersion,
      Value<int> attempts,
      Value<String?> lastError,
      Value<DateTime> createdAt,
      Value<DateTime> nextAttemptAt,
    });

class $$NoteOutboxTableFilterComposer
    extends Composer<_$AppDb, $NoteOutboxTable> {
  $$NoteOutboxTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<int> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get op => $composableBuilder(
    column: $table.op,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get entityId => $composableBuilder(
    column: $table.entityId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get notebookId => $composableBuilder(
    column: $table.notebookId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get payloadJson => $composableBuilder(
    column: $table.payloadJson,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get baseVersion => $composableBuilder(
    column: $table.baseVersion,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get attempts => $composableBuilder(
    column: $table.attempts,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get lastError => $composableBuilder(
    column: $table.lastError,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get nextAttemptAt => $composableBuilder(
    column: $table.nextAttemptAt,
    builder: (column) => ColumnFilters(column),
  );
}

class $$NoteOutboxTableOrderingComposer
    extends Composer<_$AppDb, $NoteOutboxTable> {
  $$NoteOutboxTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<int> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get op => $composableBuilder(
    column: $table.op,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get entityId => $composableBuilder(
    column: $table.entityId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get notebookId => $composableBuilder(
    column: $table.notebookId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get payloadJson => $composableBuilder(
    column: $table.payloadJson,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get baseVersion => $composableBuilder(
    column: $table.baseVersion,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get attempts => $composableBuilder(
    column: $table.attempts,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get lastError => $composableBuilder(
    column: $table.lastError,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get nextAttemptAt => $composableBuilder(
    column: $table.nextAttemptAt,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$NoteOutboxTableAnnotationComposer
    extends Composer<_$AppDb, $NoteOutboxTable> {
  $$NoteOutboxTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<int> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get op =>
      $composableBuilder(column: $table.op, builder: (column) => column);

  GeneratedColumn<String> get entityId =>
      $composableBuilder(column: $table.entityId, builder: (column) => column);

  GeneratedColumn<String> get notebookId => $composableBuilder(
    column: $table.notebookId,
    builder: (column) => column,
  );

  GeneratedColumn<String> get payloadJson => $composableBuilder(
    column: $table.payloadJson,
    builder: (column) => column,
  );

  GeneratedColumn<int> get baseVersion => $composableBuilder(
    column: $table.baseVersion,
    builder: (column) => column,
  );

  GeneratedColumn<int> get attempts =>
      $composableBuilder(column: $table.attempts, builder: (column) => column);

  GeneratedColumn<String> get lastError =>
      $composableBuilder(column: $table.lastError, builder: (column) => column);

  GeneratedColumn<DateTime> get createdAt =>
      $composableBuilder(column: $table.createdAt, builder: (column) => column);

  GeneratedColumn<DateTime> get nextAttemptAt => $composableBuilder(
    column: $table.nextAttemptAt,
    builder: (column) => column,
  );
}

class $$NoteOutboxTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $NoteOutboxTable,
          NoteOutboxEntry,
          $$NoteOutboxTableFilterComposer,
          $$NoteOutboxTableOrderingComposer,
          $$NoteOutboxTableAnnotationComposer,
          $$NoteOutboxTableCreateCompanionBuilder,
          $$NoteOutboxTableUpdateCompanionBuilder,
          (
            NoteOutboxEntry,
            BaseReferences<_$AppDb, $NoteOutboxTable, NoteOutboxEntry>,
          ),
          NoteOutboxEntry,
          PrefetchHooks Function()
        > {
  $$NoteOutboxTableTableManager(_$AppDb db, $NoteOutboxTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$NoteOutboxTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$NoteOutboxTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$NoteOutboxTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<int> id = const Value.absent(),
                Value<String> op = const Value.absent(),
                Value<String> entityId = const Value.absent(),
                Value<String?> notebookId = const Value.absent(),
                Value<String> payloadJson = const Value.absent(),
                Value<int?> baseVersion = const Value.absent(),
                Value<int> attempts = const Value.absent(),
                Value<String?> lastError = const Value.absent(),
                Value<DateTime> createdAt = const Value.absent(),
                Value<DateTime> nextAttemptAt = const Value.absent(),
              }) => NoteOutboxCompanion(
                id: id,
                op: op,
                entityId: entityId,
                notebookId: notebookId,
                payloadJson: payloadJson,
                baseVersion: baseVersion,
                attempts: attempts,
                lastError: lastError,
                createdAt: createdAt,
                nextAttemptAt: nextAttemptAt,
              ),
          createCompanionCallback:
              ({
                Value<int> id = const Value.absent(),
                required String op,
                required String entityId,
                Value<String?> notebookId = const Value.absent(),
                required String payloadJson,
                Value<int?> baseVersion = const Value.absent(),
                Value<int> attempts = const Value.absent(),
                Value<String?> lastError = const Value.absent(),
                required DateTime createdAt,
                required DateTime nextAttemptAt,
              }) => NoteOutboxCompanion.insert(
                id: id,
                op: op,
                entityId: entityId,
                notebookId: notebookId,
                payloadJson: payloadJson,
                baseVersion: baseVersion,
                attempts: attempts,
                lastError: lastError,
                createdAt: createdAt,
                nextAttemptAt: nextAttemptAt,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$NoteOutboxTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $NoteOutboxTable,
      NoteOutboxEntry,
      $$NoteOutboxTableFilterComposer,
      $$NoteOutboxTableOrderingComposer,
      $$NoteOutboxTableAnnotationComposer,
      $$NoteOutboxTableCreateCompanionBuilder,
      $$NoteOutboxTableUpdateCompanionBuilder,
      (
        NoteOutboxEntry,
        BaseReferences<_$AppDb, $NoteOutboxTable, NoteOutboxEntry>,
      ),
      NoteOutboxEntry,
      PrefetchHooks Function()
    >;
typedef $$CodeTasksTableCreateCompanionBuilder =
    CodeTasksCompanion Function({
      required String id,
      required String title,
      required String prompt,
      required String agent,
      required String mode,
      required String status,
      Value<String> eventsJson,
      Value<double> costUsd,
      Value<int> inputTokens,
      Value<int> outputTokens,
      required DateTime createdAt,
      Value<DateTime?> completedAt,
      Value<String?> errorMessage,
      Value<String?> workspaceJson,
      Value<String?> compareGroupId,
      Value<String?> originDeviceId,
      Value<String?> originDeviceLabel,
      Value<String?> projectId,
      Value<DateTime?> updatedAt,
      Value<String?> model,
      Value<bool> starred,
      Value<int> rowid,
    });
typedef $$CodeTasksTableUpdateCompanionBuilder =
    CodeTasksCompanion Function({
      Value<String> id,
      Value<String> title,
      Value<String> prompt,
      Value<String> agent,
      Value<String> mode,
      Value<String> status,
      Value<String> eventsJson,
      Value<double> costUsd,
      Value<int> inputTokens,
      Value<int> outputTokens,
      Value<DateTime> createdAt,
      Value<DateTime?> completedAt,
      Value<String?> errorMessage,
      Value<String?> workspaceJson,
      Value<String?> compareGroupId,
      Value<String?> originDeviceId,
      Value<String?> originDeviceLabel,
      Value<String?> projectId,
      Value<DateTime?> updatedAt,
      Value<String?> model,
      Value<bool> starred,
      Value<int> rowid,
    });

class $$CodeTasksTableFilterComposer
    extends Composer<_$AppDb, $CodeTasksTable> {
  $$CodeTasksTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get title => $composableBuilder(
    column: $table.title,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get prompt => $composableBuilder(
    column: $table.prompt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get agent => $composableBuilder(
    column: $table.agent,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get mode => $composableBuilder(
    column: $table.mode,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get status => $composableBuilder(
    column: $table.status,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get eventsJson => $composableBuilder(
    column: $table.eventsJson,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<double> get costUsd => $composableBuilder(
    column: $table.costUsd,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get inputTokens => $composableBuilder(
    column: $table.inputTokens,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get outputTokens => $composableBuilder(
    column: $table.outputTokens,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get completedAt => $composableBuilder(
    column: $table.completedAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get errorMessage => $composableBuilder(
    column: $table.errorMessage,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get workspaceJson => $composableBuilder(
    column: $table.workspaceJson,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get compareGroupId => $composableBuilder(
    column: $table.compareGroupId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get originDeviceId => $composableBuilder(
    column: $table.originDeviceId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get originDeviceLabel => $composableBuilder(
    column: $table.originDeviceLabel,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get projectId => $composableBuilder(
    column: $table.projectId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get model => $composableBuilder(
    column: $table.model,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<bool> get starred => $composableBuilder(
    column: $table.starred,
    builder: (column) => ColumnFilters(column),
  );
}

class $$CodeTasksTableOrderingComposer
    extends Composer<_$AppDb, $CodeTasksTable> {
  $$CodeTasksTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get title => $composableBuilder(
    column: $table.title,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get prompt => $composableBuilder(
    column: $table.prompt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get agent => $composableBuilder(
    column: $table.agent,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get mode => $composableBuilder(
    column: $table.mode,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get status => $composableBuilder(
    column: $table.status,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get eventsJson => $composableBuilder(
    column: $table.eventsJson,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<double> get costUsd => $composableBuilder(
    column: $table.costUsd,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get inputTokens => $composableBuilder(
    column: $table.inputTokens,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get outputTokens => $composableBuilder(
    column: $table.outputTokens,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get completedAt => $composableBuilder(
    column: $table.completedAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get errorMessage => $composableBuilder(
    column: $table.errorMessage,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get workspaceJson => $composableBuilder(
    column: $table.workspaceJson,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get compareGroupId => $composableBuilder(
    column: $table.compareGroupId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get originDeviceId => $composableBuilder(
    column: $table.originDeviceId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get originDeviceLabel => $composableBuilder(
    column: $table.originDeviceLabel,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get projectId => $composableBuilder(
    column: $table.projectId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get model => $composableBuilder(
    column: $table.model,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<bool> get starred => $composableBuilder(
    column: $table.starred,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$CodeTasksTableAnnotationComposer
    extends Composer<_$AppDb, $CodeTasksTable> {
  $$CodeTasksTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get title =>
      $composableBuilder(column: $table.title, builder: (column) => column);

  GeneratedColumn<String> get prompt =>
      $composableBuilder(column: $table.prompt, builder: (column) => column);

  GeneratedColumn<String> get agent =>
      $composableBuilder(column: $table.agent, builder: (column) => column);

  GeneratedColumn<String> get mode =>
      $composableBuilder(column: $table.mode, builder: (column) => column);

  GeneratedColumn<String> get status =>
      $composableBuilder(column: $table.status, builder: (column) => column);

  GeneratedColumn<String> get eventsJson => $composableBuilder(
    column: $table.eventsJson,
    builder: (column) => column,
  );

  GeneratedColumn<double> get costUsd =>
      $composableBuilder(column: $table.costUsd, builder: (column) => column);

  GeneratedColumn<int> get inputTokens => $composableBuilder(
    column: $table.inputTokens,
    builder: (column) => column,
  );

  GeneratedColumn<int> get outputTokens => $composableBuilder(
    column: $table.outputTokens,
    builder: (column) => column,
  );

  GeneratedColumn<DateTime> get createdAt =>
      $composableBuilder(column: $table.createdAt, builder: (column) => column);

  GeneratedColumn<DateTime> get completedAt => $composableBuilder(
    column: $table.completedAt,
    builder: (column) => column,
  );

  GeneratedColumn<String> get errorMessage => $composableBuilder(
    column: $table.errorMessage,
    builder: (column) => column,
  );

  GeneratedColumn<String> get workspaceJson => $composableBuilder(
    column: $table.workspaceJson,
    builder: (column) => column,
  );

  GeneratedColumn<String> get compareGroupId => $composableBuilder(
    column: $table.compareGroupId,
    builder: (column) => column,
  );

  GeneratedColumn<String> get originDeviceId => $composableBuilder(
    column: $table.originDeviceId,
    builder: (column) => column,
  );

  GeneratedColumn<String> get originDeviceLabel => $composableBuilder(
    column: $table.originDeviceLabel,
    builder: (column) => column,
  );

  GeneratedColumn<String> get projectId =>
      $composableBuilder(column: $table.projectId, builder: (column) => column);

  GeneratedColumn<DateTime> get updatedAt =>
      $composableBuilder(column: $table.updatedAt, builder: (column) => column);

  GeneratedColumn<String> get model =>
      $composableBuilder(column: $table.model, builder: (column) => column);

  GeneratedColumn<bool> get starred =>
      $composableBuilder(column: $table.starred, builder: (column) => column);
}

class $$CodeTasksTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $CodeTasksTable,
          LocalCodeTask,
          $$CodeTasksTableFilterComposer,
          $$CodeTasksTableOrderingComposer,
          $$CodeTasksTableAnnotationComposer,
          $$CodeTasksTableCreateCompanionBuilder,
          $$CodeTasksTableUpdateCompanionBuilder,
          (
            LocalCodeTask,
            BaseReferences<_$AppDb, $CodeTasksTable, LocalCodeTask>,
          ),
          LocalCodeTask,
          PrefetchHooks Function()
        > {
  $$CodeTasksTableTableManager(_$AppDb db, $CodeTasksTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$CodeTasksTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$CodeTasksTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$CodeTasksTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> id = const Value.absent(),
                Value<String> title = const Value.absent(),
                Value<String> prompt = const Value.absent(),
                Value<String> agent = const Value.absent(),
                Value<String> mode = const Value.absent(),
                Value<String> status = const Value.absent(),
                Value<String> eventsJson = const Value.absent(),
                Value<double> costUsd = const Value.absent(),
                Value<int> inputTokens = const Value.absent(),
                Value<int> outputTokens = const Value.absent(),
                Value<DateTime> createdAt = const Value.absent(),
                Value<DateTime?> completedAt = const Value.absent(),
                Value<String?> errorMessage = const Value.absent(),
                Value<String?> workspaceJson = const Value.absent(),
                Value<String?> compareGroupId = const Value.absent(),
                Value<String?> originDeviceId = const Value.absent(),
                Value<String?> originDeviceLabel = const Value.absent(),
                Value<String?> projectId = const Value.absent(),
                Value<DateTime?> updatedAt = const Value.absent(),
                Value<String?> model = const Value.absent(),
                Value<bool> starred = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => CodeTasksCompanion(
                id: id,
                title: title,
                prompt: prompt,
                agent: agent,
                mode: mode,
                status: status,
                eventsJson: eventsJson,
                costUsd: costUsd,
                inputTokens: inputTokens,
                outputTokens: outputTokens,
                createdAt: createdAt,
                completedAt: completedAt,
                errorMessage: errorMessage,
                workspaceJson: workspaceJson,
                compareGroupId: compareGroupId,
                originDeviceId: originDeviceId,
                originDeviceLabel: originDeviceLabel,
                projectId: projectId,
                updatedAt: updatedAt,
                model: model,
                starred: starred,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String id,
                required String title,
                required String prompt,
                required String agent,
                required String mode,
                required String status,
                Value<String> eventsJson = const Value.absent(),
                Value<double> costUsd = const Value.absent(),
                Value<int> inputTokens = const Value.absent(),
                Value<int> outputTokens = const Value.absent(),
                required DateTime createdAt,
                Value<DateTime?> completedAt = const Value.absent(),
                Value<String?> errorMessage = const Value.absent(),
                Value<String?> workspaceJson = const Value.absent(),
                Value<String?> compareGroupId = const Value.absent(),
                Value<String?> originDeviceId = const Value.absent(),
                Value<String?> originDeviceLabel = const Value.absent(),
                Value<String?> projectId = const Value.absent(),
                Value<DateTime?> updatedAt = const Value.absent(),
                Value<String?> model = const Value.absent(),
                Value<bool> starred = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => CodeTasksCompanion.insert(
                id: id,
                title: title,
                prompt: prompt,
                agent: agent,
                mode: mode,
                status: status,
                eventsJson: eventsJson,
                costUsd: costUsd,
                inputTokens: inputTokens,
                outputTokens: outputTokens,
                createdAt: createdAt,
                completedAt: completedAt,
                errorMessage: errorMessage,
                workspaceJson: workspaceJson,
                compareGroupId: compareGroupId,
                originDeviceId: originDeviceId,
                originDeviceLabel: originDeviceLabel,
                projectId: projectId,
                updatedAt: updatedAt,
                model: model,
                starred: starred,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$CodeTasksTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $CodeTasksTable,
      LocalCodeTask,
      $$CodeTasksTableFilterComposer,
      $$CodeTasksTableOrderingComposer,
      $$CodeTasksTableAnnotationComposer,
      $$CodeTasksTableCreateCompanionBuilder,
      $$CodeTasksTableUpdateCompanionBuilder,
      (LocalCodeTask, BaseReferences<_$AppDb, $CodeTasksTable, LocalCodeTask>),
      LocalCodeTask,
      PrefetchHooks Function()
    >;
typedef $$CodeProjectsTableCreateCompanionBuilder =
    CodeProjectsCompanion Function({
      required String id,
      required String name,
      required String path,
      Value<String?> branch,
      Value<int> lastOpenedAt,
      Value<bool> hiddenFromRail,
      Value<String?> avatarColor,
      Value<int> sortIndex,
      Value<int> rowid,
    });
typedef $$CodeProjectsTableUpdateCompanionBuilder =
    CodeProjectsCompanion Function({
      Value<String> id,
      Value<String> name,
      Value<String> path,
      Value<String?> branch,
      Value<int> lastOpenedAt,
      Value<bool> hiddenFromRail,
      Value<String?> avatarColor,
      Value<int> sortIndex,
      Value<int> rowid,
    });

class $$CodeProjectsTableFilterComposer
    extends Composer<_$AppDb, $CodeProjectsTable> {
  $$CodeProjectsTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get name => $composableBuilder(
    column: $table.name,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get path => $composableBuilder(
    column: $table.path,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get branch => $composableBuilder(
    column: $table.branch,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get lastOpenedAt => $composableBuilder(
    column: $table.lastOpenedAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<bool> get hiddenFromRail => $composableBuilder(
    column: $table.hiddenFromRail,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get avatarColor => $composableBuilder(
    column: $table.avatarColor,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get sortIndex => $composableBuilder(
    column: $table.sortIndex,
    builder: (column) => ColumnFilters(column),
  );
}

class $$CodeProjectsTableOrderingComposer
    extends Composer<_$AppDb, $CodeProjectsTable> {
  $$CodeProjectsTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get name => $composableBuilder(
    column: $table.name,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get path => $composableBuilder(
    column: $table.path,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get branch => $composableBuilder(
    column: $table.branch,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get lastOpenedAt => $composableBuilder(
    column: $table.lastOpenedAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<bool> get hiddenFromRail => $composableBuilder(
    column: $table.hiddenFromRail,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get avatarColor => $composableBuilder(
    column: $table.avatarColor,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get sortIndex => $composableBuilder(
    column: $table.sortIndex,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$CodeProjectsTableAnnotationComposer
    extends Composer<_$AppDb, $CodeProjectsTable> {
  $$CodeProjectsTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get name =>
      $composableBuilder(column: $table.name, builder: (column) => column);

  GeneratedColumn<String> get path =>
      $composableBuilder(column: $table.path, builder: (column) => column);

  GeneratedColumn<String> get branch =>
      $composableBuilder(column: $table.branch, builder: (column) => column);

  GeneratedColumn<int> get lastOpenedAt => $composableBuilder(
    column: $table.lastOpenedAt,
    builder: (column) => column,
  );

  GeneratedColumn<bool> get hiddenFromRail => $composableBuilder(
    column: $table.hiddenFromRail,
    builder: (column) => column,
  );

  GeneratedColumn<String> get avatarColor => $composableBuilder(
    column: $table.avatarColor,
    builder: (column) => column,
  );

  GeneratedColumn<int> get sortIndex =>
      $composableBuilder(column: $table.sortIndex, builder: (column) => column);
}

class $$CodeProjectsTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $CodeProjectsTable,
          LocalCodeProject,
          $$CodeProjectsTableFilterComposer,
          $$CodeProjectsTableOrderingComposer,
          $$CodeProjectsTableAnnotationComposer,
          $$CodeProjectsTableCreateCompanionBuilder,
          $$CodeProjectsTableUpdateCompanionBuilder,
          (
            LocalCodeProject,
            BaseReferences<_$AppDb, $CodeProjectsTable, LocalCodeProject>,
          ),
          LocalCodeProject,
          PrefetchHooks Function()
        > {
  $$CodeProjectsTableTableManager(_$AppDb db, $CodeProjectsTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$CodeProjectsTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$CodeProjectsTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$CodeProjectsTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> id = const Value.absent(),
                Value<String> name = const Value.absent(),
                Value<String> path = const Value.absent(),
                Value<String?> branch = const Value.absent(),
                Value<int> lastOpenedAt = const Value.absent(),
                Value<bool> hiddenFromRail = const Value.absent(),
                Value<String?> avatarColor = const Value.absent(),
                Value<int> sortIndex = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => CodeProjectsCompanion(
                id: id,
                name: name,
                path: path,
                branch: branch,
                lastOpenedAt: lastOpenedAt,
                hiddenFromRail: hiddenFromRail,
                avatarColor: avatarColor,
                sortIndex: sortIndex,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String id,
                required String name,
                required String path,
                Value<String?> branch = const Value.absent(),
                Value<int> lastOpenedAt = const Value.absent(),
                Value<bool> hiddenFromRail = const Value.absent(),
                Value<String?> avatarColor = const Value.absent(),
                Value<int> sortIndex = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => CodeProjectsCompanion.insert(
                id: id,
                name: name,
                path: path,
                branch: branch,
                lastOpenedAt: lastOpenedAt,
                hiddenFromRail: hiddenFromRail,
                avatarColor: avatarColor,
                sortIndex: sortIndex,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$CodeProjectsTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $CodeProjectsTable,
      LocalCodeProject,
      $$CodeProjectsTableFilterComposer,
      $$CodeProjectsTableOrderingComposer,
      $$CodeProjectsTableAnnotationComposer,
      $$CodeProjectsTableCreateCompanionBuilder,
      $$CodeProjectsTableUpdateCompanionBuilder,
      (
        LocalCodeProject,
        BaseReferences<_$AppDb, $CodeProjectsTable, LocalCodeProject>,
      ),
      LocalCodeProject,
      PrefetchHooks Function()
    >;
typedef $$CodeTaskArtifactsTableCreateCompanionBuilder =
    CodeTaskArtifactsCompanion Function({
      required String id,
      required String taskId,
      required String kind,
      required String relPath,
      Value<String?> mimeType,
      Value<int> sizeBytes,
      required String sha256,
      required String op,
      Value<String?> previewSummary,
      Value<String?> previewDataB64,
      Value<String?> previewMimeType,
      required DateTime createdAt,
      Value<int> rowid,
    });
typedef $$CodeTaskArtifactsTableUpdateCompanionBuilder =
    CodeTaskArtifactsCompanion Function({
      Value<String> id,
      Value<String> taskId,
      Value<String> kind,
      Value<String> relPath,
      Value<String?> mimeType,
      Value<int> sizeBytes,
      Value<String> sha256,
      Value<String> op,
      Value<String?> previewSummary,
      Value<String?> previewDataB64,
      Value<String?> previewMimeType,
      Value<DateTime> createdAt,
      Value<int> rowid,
    });

class $$CodeTaskArtifactsTableFilterComposer
    extends Composer<_$AppDb, $CodeTaskArtifactsTable> {
  $$CodeTaskArtifactsTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get taskId => $composableBuilder(
    column: $table.taskId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get kind => $composableBuilder(
    column: $table.kind,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get relPath => $composableBuilder(
    column: $table.relPath,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get mimeType => $composableBuilder(
    column: $table.mimeType,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get sizeBytes => $composableBuilder(
    column: $table.sizeBytes,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get sha256 => $composableBuilder(
    column: $table.sha256,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get op => $composableBuilder(
    column: $table.op,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get previewSummary => $composableBuilder(
    column: $table.previewSummary,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get previewDataB64 => $composableBuilder(
    column: $table.previewDataB64,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get previewMimeType => $composableBuilder(
    column: $table.previewMimeType,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnFilters(column),
  );
}

class $$CodeTaskArtifactsTableOrderingComposer
    extends Composer<_$AppDb, $CodeTaskArtifactsTable> {
  $$CodeTaskArtifactsTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get taskId => $composableBuilder(
    column: $table.taskId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get kind => $composableBuilder(
    column: $table.kind,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get relPath => $composableBuilder(
    column: $table.relPath,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get mimeType => $composableBuilder(
    column: $table.mimeType,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get sizeBytes => $composableBuilder(
    column: $table.sizeBytes,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get sha256 => $composableBuilder(
    column: $table.sha256,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get op => $composableBuilder(
    column: $table.op,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get previewSummary => $composableBuilder(
    column: $table.previewSummary,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get previewDataB64 => $composableBuilder(
    column: $table.previewDataB64,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get previewMimeType => $composableBuilder(
    column: $table.previewMimeType,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$CodeTaskArtifactsTableAnnotationComposer
    extends Composer<_$AppDb, $CodeTaskArtifactsTable> {
  $$CodeTaskArtifactsTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get taskId =>
      $composableBuilder(column: $table.taskId, builder: (column) => column);

  GeneratedColumn<String> get kind =>
      $composableBuilder(column: $table.kind, builder: (column) => column);

  GeneratedColumn<String> get relPath =>
      $composableBuilder(column: $table.relPath, builder: (column) => column);

  GeneratedColumn<String> get mimeType =>
      $composableBuilder(column: $table.mimeType, builder: (column) => column);

  GeneratedColumn<int> get sizeBytes =>
      $composableBuilder(column: $table.sizeBytes, builder: (column) => column);

  GeneratedColumn<String> get sha256 =>
      $composableBuilder(column: $table.sha256, builder: (column) => column);

  GeneratedColumn<String> get op =>
      $composableBuilder(column: $table.op, builder: (column) => column);

  GeneratedColumn<String> get previewSummary => $composableBuilder(
    column: $table.previewSummary,
    builder: (column) => column,
  );

  GeneratedColumn<String> get previewDataB64 => $composableBuilder(
    column: $table.previewDataB64,
    builder: (column) => column,
  );

  GeneratedColumn<String> get previewMimeType => $composableBuilder(
    column: $table.previewMimeType,
    builder: (column) => column,
  );

  GeneratedColumn<DateTime> get createdAt =>
      $composableBuilder(column: $table.createdAt, builder: (column) => column);
}

class $$CodeTaskArtifactsTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $CodeTaskArtifactsTable,
          LocalCodeTaskArtifact,
          $$CodeTaskArtifactsTableFilterComposer,
          $$CodeTaskArtifactsTableOrderingComposer,
          $$CodeTaskArtifactsTableAnnotationComposer,
          $$CodeTaskArtifactsTableCreateCompanionBuilder,
          $$CodeTaskArtifactsTableUpdateCompanionBuilder,
          (
            LocalCodeTaskArtifact,
            BaseReferences<
              _$AppDb,
              $CodeTaskArtifactsTable,
              LocalCodeTaskArtifact
            >,
          ),
          LocalCodeTaskArtifact,
          PrefetchHooks Function()
        > {
  $$CodeTaskArtifactsTableTableManager(
    _$AppDb db,
    $CodeTaskArtifactsTable table,
  ) : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$CodeTaskArtifactsTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$CodeTaskArtifactsTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$CodeTaskArtifactsTableAnnotationComposer(
                $db: db,
                $table: table,
              ),
          updateCompanionCallback:
              ({
                Value<String> id = const Value.absent(),
                Value<String> taskId = const Value.absent(),
                Value<String> kind = const Value.absent(),
                Value<String> relPath = const Value.absent(),
                Value<String?> mimeType = const Value.absent(),
                Value<int> sizeBytes = const Value.absent(),
                Value<String> sha256 = const Value.absent(),
                Value<String> op = const Value.absent(),
                Value<String?> previewSummary = const Value.absent(),
                Value<String?> previewDataB64 = const Value.absent(),
                Value<String?> previewMimeType = const Value.absent(),
                Value<DateTime> createdAt = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => CodeTaskArtifactsCompanion(
                id: id,
                taskId: taskId,
                kind: kind,
                relPath: relPath,
                mimeType: mimeType,
                sizeBytes: sizeBytes,
                sha256: sha256,
                op: op,
                previewSummary: previewSummary,
                previewDataB64: previewDataB64,
                previewMimeType: previewMimeType,
                createdAt: createdAt,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String id,
                required String taskId,
                required String kind,
                required String relPath,
                Value<String?> mimeType = const Value.absent(),
                Value<int> sizeBytes = const Value.absent(),
                required String sha256,
                required String op,
                Value<String?> previewSummary = const Value.absent(),
                Value<String?> previewDataB64 = const Value.absent(),
                Value<String?> previewMimeType = const Value.absent(),
                required DateTime createdAt,
                Value<int> rowid = const Value.absent(),
              }) => CodeTaskArtifactsCompanion.insert(
                id: id,
                taskId: taskId,
                kind: kind,
                relPath: relPath,
                mimeType: mimeType,
                sizeBytes: sizeBytes,
                sha256: sha256,
                op: op,
                previewSummary: previewSummary,
                previewDataB64: previewDataB64,
                previewMimeType: previewMimeType,
                createdAt: createdAt,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$CodeTaskArtifactsTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $CodeTaskArtifactsTable,
      LocalCodeTaskArtifact,
      $$CodeTaskArtifactsTableFilterComposer,
      $$CodeTaskArtifactsTableOrderingComposer,
      $$CodeTaskArtifactsTableAnnotationComposer,
      $$CodeTaskArtifactsTableCreateCompanionBuilder,
      $$CodeTaskArtifactsTableUpdateCompanionBuilder,
      (
        LocalCodeTaskArtifact,
        BaseReferences<_$AppDb, $CodeTaskArtifactsTable, LocalCodeTaskArtifact>,
      ),
      LocalCodeTaskArtifact,
      PrefetchHooks Function()
    >;
typedef $$ChatThreadsV2TableCreateCompanionBuilder =
    ChatThreadsV2Companion Function({
      required String id,
      Value<String> title,
      required String mode,
      Value<String?> environmentId,
      Value<String?> poolTag,
      Value<String?> model,
      Value<String?> providerId,
      Value<String?> systemPrompt,
      Value<String?> projectId,
      Value<String?> workdir,
      Value<String> autoApprove,
      Value<String> runtimeEnvMode,
      Value<String> backend,
      Value<bool> pinned,
      Value<bool> archived,
      required DateTime createdAt,
      required DateTime updatedAt,
      Value<int?> remoteUpdatedAtUs,
      Value<String> ownerKey,
      Value<int> rowid,
    });
typedef $$ChatThreadsV2TableUpdateCompanionBuilder =
    ChatThreadsV2Companion Function({
      Value<String> id,
      Value<String> title,
      Value<String> mode,
      Value<String?> environmentId,
      Value<String?> poolTag,
      Value<String?> model,
      Value<String?> providerId,
      Value<String?> systemPrompt,
      Value<String?> projectId,
      Value<String?> workdir,
      Value<String> autoApprove,
      Value<String> runtimeEnvMode,
      Value<String> backend,
      Value<bool> pinned,
      Value<bool> archived,
      Value<DateTime> createdAt,
      Value<DateTime> updatedAt,
      Value<int?> remoteUpdatedAtUs,
      Value<String> ownerKey,
      Value<int> rowid,
    });

class $$ChatThreadsV2TableFilterComposer
    extends Composer<_$AppDb, $ChatThreadsV2Table> {
  $$ChatThreadsV2TableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get title => $composableBuilder(
    column: $table.title,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get mode => $composableBuilder(
    column: $table.mode,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get environmentId => $composableBuilder(
    column: $table.environmentId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get poolTag => $composableBuilder(
    column: $table.poolTag,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get model => $composableBuilder(
    column: $table.model,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get providerId => $composableBuilder(
    column: $table.providerId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get systemPrompt => $composableBuilder(
    column: $table.systemPrompt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get projectId => $composableBuilder(
    column: $table.projectId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get workdir => $composableBuilder(
    column: $table.workdir,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get autoApprove => $composableBuilder(
    column: $table.autoApprove,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get runtimeEnvMode => $composableBuilder(
    column: $table.runtimeEnvMode,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get backend => $composableBuilder(
    column: $table.backend,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<bool> get pinned => $composableBuilder(
    column: $table.pinned,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<bool> get archived => $composableBuilder(
    column: $table.archived,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get remoteUpdatedAtUs => $composableBuilder(
    column: $table.remoteUpdatedAtUs,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get ownerKey => $composableBuilder(
    column: $table.ownerKey,
    builder: (column) => ColumnFilters(column),
  );
}

class $$ChatThreadsV2TableOrderingComposer
    extends Composer<_$AppDb, $ChatThreadsV2Table> {
  $$ChatThreadsV2TableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get title => $composableBuilder(
    column: $table.title,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get mode => $composableBuilder(
    column: $table.mode,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get environmentId => $composableBuilder(
    column: $table.environmentId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get poolTag => $composableBuilder(
    column: $table.poolTag,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get model => $composableBuilder(
    column: $table.model,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get providerId => $composableBuilder(
    column: $table.providerId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get systemPrompt => $composableBuilder(
    column: $table.systemPrompt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get projectId => $composableBuilder(
    column: $table.projectId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get workdir => $composableBuilder(
    column: $table.workdir,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get autoApprove => $composableBuilder(
    column: $table.autoApprove,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get runtimeEnvMode => $composableBuilder(
    column: $table.runtimeEnvMode,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get backend => $composableBuilder(
    column: $table.backend,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<bool> get pinned => $composableBuilder(
    column: $table.pinned,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<bool> get archived => $composableBuilder(
    column: $table.archived,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get remoteUpdatedAtUs => $composableBuilder(
    column: $table.remoteUpdatedAtUs,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get ownerKey => $composableBuilder(
    column: $table.ownerKey,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$ChatThreadsV2TableAnnotationComposer
    extends Composer<_$AppDb, $ChatThreadsV2Table> {
  $$ChatThreadsV2TableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get title =>
      $composableBuilder(column: $table.title, builder: (column) => column);

  GeneratedColumn<String> get mode =>
      $composableBuilder(column: $table.mode, builder: (column) => column);

  GeneratedColumn<String> get environmentId => $composableBuilder(
    column: $table.environmentId,
    builder: (column) => column,
  );

  GeneratedColumn<String> get poolTag =>
      $composableBuilder(column: $table.poolTag, builder: (column) => column);

  GeneratedColumn<String> get model =>
      $composableBuilder(column: $table.model, builder: (column) => column);

  GeneratedColumn<String> get providerId => $composableBuilder(
    column: $table.providerId,
    builder: (column) => column,
  );

  GeneratedColumn<String> get systemPrompt => $composableBuilder(
    column: $table.systemPrompt,
    builder: (column) => column,
  );

  GeneratedColumn<String> get projectId =>
      $composableBuilder(column: $table.projectId, builder: (column) => column);

  GeneratedColumn<String> get workdir =>
      $composableBuilder(column: $table.workdir, builder: (column) => column);

  GeneratedColumn<String> get autoApprove => $composableBuilder(
    column: $table.autoApprove,
    builder: (column) => column,
  );

  GeneratedColumn<String> get runtimeEnvMode => $composableBuilder(
    column: $table.runtimeEnvMode,
    builder: (column) => column,
  );

  GeneratedColumn<String> get backend =>
      $composableBuilder(column: $table.backend, builder: (column) => column);

  GeneratedColumn<bool> get pinned =>
      $composableBuilder(column: $table.pinned, builder: (column) => column);

  GeneratedColumn<bool> get archived =>
      $composableBuilder(column: $table.archived, builder: (column) => column);

  GeneratedColumn<DateTime> get createdAt =>
      $composableBuilder(column: $table.createdAt, builder: (column) => column);

  GeneratedColumn<DateTime> get updatedAt =>
      $composableBuilder(column: $table.updatedAt, builder: (column) => column);

  GeneratedColumn<int> get remoteUpdatedAtUs => $composableBuilder(
    column: $table.remoteUpdatedAtUs,
    builder: (column) => column,
  );

  GeneratedColumn<String> get ownerKey =>
      $composableBuilder(column: $table.ownerKey, builder: (column) => column);
}

class $$ChatThreadsV2TableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $ChatThreadsV2Table,
          LocalChatThreadV2,
          $$ChatThreadsV2TableFilterComposer,
          $$ChatThreadsV2TableOrderingComposer,
          $$ChatThreadsV2TableAnnotationComposer,
          $$ChatThreadsV2TableCreateCompanionBuilder,
          $$ChatThreadsV2TableUpdateCompanionBuilder,
          (
            LocalChatThreadV2,
            BaseReferences<_$AppDb, $ChatThreadsV2Table, LocalChatThreadV2>,
          ),
          LocalChatThreadV2,
          PrefetchHooks Function()
        > {
  $$ChatThreadsV2TableTableManager(_$AppDb db, $ChatThreadsV2Table table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$ChatThreadsV2TableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$ChatThreadsV2TableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$ChatThreadsV2TableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> id = const Value.absent(),
                Value<String> title = const Value.absent(),
                Value<String> mode = const Value.absent(),
                Value<String?> environmentId = const Value.absent(),
                Value<String?> poolTag = const Value.absent(),
                Value<String?> model = const Value.absent(),
                Value<String?> providerId = const Value.absent(),
                Value<String?> systemPrompt = const Value.absent(),
                Value<String?> projectId = const Value.absent(),
                Value<String?> workdir = const Value.absent(),
                Value<String> autoApprove = const Value.absent(),
                Value<String> runtimeEnvMode = const Value.absent(),
                Value<String> backend = const Value.absent(),
                Value<bool> pinned = const Value.absent(),
                Value<bool> archived = const Value.absent(),
                Value<DateTime> createdAt = const Value.absent(),
                Value<DateTime> updatedAt = const Value.absent(),
                Value<int?> remoteUpdatedAtUs = const Value.absent(),
                Value<String> ownerKey = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => ChatThreadsV2Companion(
                id: id,
                title: title,
                mode: mode,
                environmentId: environmentId,
                poolTag: poolTag,
                model: model,
                providerId: providerId,
                systemPrompt: systemPrompt,
                projectId: projectId,
                workdir: workdir,
                autoApprove: autoApprove,
                runtimeEnvMode: runtimeEnvMode,
                backend: backend,
                pinned: pinned,
                archived: archived,
                createdAt: createdAt,
                updatedAt: updatedAt,
                remoteUpdatedAtUs: remoteUpdatedAtUs,
                ownerKey: ownerKey,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String id,
                Value<String> title = const Value.absent(),
                required String mode,
                Value<String?> environmentId = const Value.absent(),
                Value<String?> poolTag = const Value.absent(),
                Value<String?> model = const Value.absent(),
                Value<String?> providerId = const Value.absent(),
                Value<String?> systemPrompt = const Value.absent(),
                Value<String?> projectId = const Value.absent(),
                Value<String?> workdir = const Value.absent(),
                Value<String> autoApprove = const Value.absent(),
                Value<String> runtimeEnvMode = const Value.absent(),
                Value<String> backend = const Value.absent(),
                Value<bool> pinned = const Value.absent(),
                Value<bool> archived = const Value.absent(),
                required DateTime createdAt,
                required DateTime updatedAt,
                Value<int?> remoteUpdatedAtUs = const Value.absent(),
                Value<String> ownerKey = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => ChatThreadsV2Companion.insert(
                id: id,
                title: title,
                mode: mode,
                environmentId: environmentId,
                poolTag: poolTag,
                model: model,
                providerId: providerId,
                systemPrompt: systemPrompt,
                projectId: projectId,
                workdir: workdir,
                autoApprove: autoApprove,
                runtimeEnvMode: runtimeEnvMode,
                backend: backend,
                pinned: pinned,
                archived: archived,
                createdAt: createdAt,
                updatedAt: updatedAt,
                remoteUpdatedAtUs: remoteUpdatedAtUs,
                ownerKey: ownerKey,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$ChatThreadsV2TableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $ChatThreadsV2Table,
      LocalChatThreadV2,
      $$ChatThreadsV2TableFilterComposer,
      $$ChatThreadsV2TableOrderingComposer,
      $$ChatThreadsV2TableAnnotationComposer,
      $$ChatThreadsV2TableCreateCompanionBuilder,
      $$ChatThreadsV2TableUpdateCompanionBuilder,
      (
        LocalChatThreadV2,
        BaseReferences<_$AppDb, $ChatThreadsV2Table, LocalChatThreadV2>,
      ),
      LocalChatThreadV2,
      PrefetchHooks Function()
    >;
typedef $$ChatMessagesV2TableCreateCompanionBuilder =
    ChatMessagesV2Companion Function({
      required String id,
      required String threadId,
      required String role,
      required String status,
      Value<String?> sessionId,
      Value<String?> stopReason,
      Value<String?> model,
      Value<int?> inputTokens,
      Value<int?> outputTokens,
      required int seq,
      Value<String?> errorMessage,
      required DateTime createdAt,
      Value<DateTime?> completedAt,
      Value<String> ownerKey,
      Value<int> rowid,
    });
typedef $$ChatMessagesV2TableUpdateCompanionBuilder =
    ChatMessagesV2Companion Function({
      Value<String> id,
      Value<String> threadId,
      Value<String> role,
      Value<String> status,
      Value<String?> sessionId,
      Value<String?> stopReason,
      Value<String?> model,
      Value<int?> inputTokens,
      Value<int?> outputTokens,
      Value<int> seq,
      Value<String?> errorMessage,
      Value<DateTime> createdAt,
      Value<DateTime?> completedAt,
      Value<String> ownerKey,
      Value<int> rowid,
    });

class $$ChatMessagesV2TableFilterComposer
    extends Composer<_$AppDb, $ChatMessagesV2Table> {
  $$ChatMessagesV2TableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get threadId => $composableBuilder(
    column: $table.threadId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get role => $composableBuilder(
    column: $table.role,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get status => $composableBuilder(
    column: $table.status,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get sessionId => $composableBuilder(
    column: $table.sessionId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get stopReason => $composableBuilder(
    column: $table.stopReason,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get model => $composableBuilder(
    column: $table.model,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get inputTokens => $composableBuilder(
    column: $table.inputTokens,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get outputTokens => $composableBuilder(
    column: $table.outputTokens,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get seq => $composableBuilder(
    column: $table.seq,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get errorMessage => $composableBuilder(
    column: $table.errorMessage,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get completedAt => $composableBuilder(
    column: $table.completedAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get ownerKey => $composableBuilder(
    column: $table.ownerKey,
    builder: (column) => ColumnFilters(column),
  );
}

class $$ChatMessagesV2TableOrderingComposer
    extends Composer<_$AppDb, $ChatMessagesV2Table> {
  $$ChatMessagesV2TableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get threadId => $composableBuilder(
    column: $table.threadId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get role => $composableBuilder(
    column: $table.role,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get status => $composableBuilder(
    column: $table.status,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get sessionId => $composableBuilder(
    column: $table.sessionId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get stopReason => $composableBuilder(
    column: $table.stopReason,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get model => $composableBuilder(
    column: $table.model,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get inputTokens => $composableBuilder(
    column: $table.inputTokens,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get outputTokens => $composableBuilder(
    column: $table.outputTokens,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get seq => $composableBuilder(
    column: $table.seq,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get errorMessage => $composableBuilder(
    column: $table.errorMessage,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get completedAt => $composableBuilder(
    column: $table.completedAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get ownerKey => $composableBuilder(
    column: $table.ownerKey,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$ChatMessagesV2TableAnnotationComposer
    extends Composer<_$AppDb, $ChatMessagesV2Table> {
  $$ChatMessagesV2TableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get threadId =>
      $composableBuilder(column: $table.threadId, builder: (column) => column);

  GeneratedColumn<String> get role =>
      $composableBuilder(column: $table.role, builder: (column) => column);

  GeneratedColumn<String> get status =>
      $composableBuilder(column: $table.status, builder: (column) => column);

  GeneratedColumn<String> get sessionId =>
      $composableBuilder(column: $table.sessionId, builder: (column) => column);

  GeneratedColumn<String> get stopReason => $composableBuilder(
    column: $table.stopReason,
    builder: (column) => column,
  );

  GeneratedColumn<String> get model =>
      $composableBuilder(column: $table.model, builder: (column) => column);

  GeneratedColumn<int> get inputTokens => $composableBuilder(
    column: $table.inputTokens,
    builder: (column) => column,
  );

  GeneratedColumn<int> get outputTokens => $composableBuilder(
    column: $table.outputTokens,
    builder: (column) => column,
  );

  GeneratedColumn<int> get seq =>
      $composableBuilder(column: $table.seq, builder: (column) => column);

  GeneratedColumn<String> get errorMessage => $composableBuilder(
    column: $table.errorMessage,
    builder: (column) => column,
  );

  GeneratedColumn<DateTime> get createdAt =>
      $composableBuilder(column: $table.createdAt, builder: (column) => column);

  GeneratedColumn<DateTime> get completedAt => $composableBuilder(
    column: $table.completedAt,
    builder: (column) => column,
  );

  GeneratedColumn<String> get ownerKey =>
      $composableBuilder(column: $table.ownerKey, builder: (column) => column);
}

class $$ChatMessagesV2TableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $ChatMessagesV2Table,
          LocalChatMessageV2,
          $$ChatMessagesV2TableFilterComposer,
          $$ChatMessagesV2TableOrderingComposer,
          $$ChatMessagesV2TableAnnotationComposer,
          $$ChatMessagesV2TableCreateCompanionBuilder,
          $$ChatMessagesV2TableUpdateCompanionBuilder,
          (
            LocalChatMessageV2,
            BaseReferences<_$AppDb, $ChatMessagesV2Table, LocalChatMessageV2>,
          ),
          LocalChatMessageV2,
          PrefetchHooks Function()
        > {
  $$ChatMessagesV2TableTableManager(_$AppDb db, $ChatMessagesV2Table table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$ChatMessagesV2TableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$ChatMessagesV2TableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$ChatMessagesV2TableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> id = const Value.absent(),
                Value<String> threadId = const Value.absent(),
                Value<String> role = const Value.absent(),
                Value<String> status = const Value.absent(),
                Value<String?> sessionId = const Value.absent(),
                Value<String?> stopReason = const Value.absent(),
                Value<String?> model = const Value.absent(),
                Value<int?> inputTokens = const Value.absent(),
                Value<int?> outputTokens = const Value.absent(),
                Value<int> seq = const Value.absent(),
                Value<String?> errorMessage = const Value.absent(),
                Value<DateTime> createdAt = const Value.absent(),
                Value<DateTime?> completedAt = const Value.absent(),
                Value<String> ownerKey = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => ChatMessagesV2Companion(
                id: id,
                threadId: threadId,
                role: role,
                status: status,
                sessionId: sessionId,
                stopReason: stopReason,
                model: model,
                inputTokens: inputTokens,
                outputTokens: outputTokens,
                seq: seq,
                errorMessage: errorMessage,
                createdAt: createdAt,
                completedAt: completedAt,
                ownerKey: ownerKey,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String id,
                required String threadId,
                required String role,
                required String status,
                Value<String?> sessionId = const Value.absent(),
                Value<String?> stopReason = const Value.absent(),
                Value<String?> model = const Value.absent(),
                Value<int?> inputTokens = const Value.absent(),
                Value<int?> outputTokens = const Value.absent(),
                required int seq,
                Value<String?> errorMessage = const Value.absent(),
                required DateTime createdAt,
                Value<DateTime?> completedAt = const Value.absent(),
                Value<String> ownerKey = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => ChatMessagesV2Companion.insert(
                id: id,
                threadId: threadId,
                role: role,
                status: status,
                sessionId: sessionId,
                stopReason: stopReason,
                model: model,
                inputTokens: inputTokens,
                outputTokens: outputTokens,
                seq: seq,
                errorMessage: errorMessage,
                createdAt: createdAt,
                completedAt: completedAt,
                ownerKey: ownerKey,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$ChatMessagesV2TableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $ChatMessagesV2Table,
      LocalChatMessageV2,
      $$ChatMessagesV2TableFilterComposer,
      $$ChatMessagesV2TableOrderingComposer,
      $$ChatMessagesV2TableAnnotationComposer,
      $$ChatMessagesV2TableCreateCompanionBuilder,
      $$ChatMessagesV2TableUpdateCompanionBuilder,
      (
        LocalChatMessageV2,
        BaseReferences<_$AppDb, $ChatMessagesV2Table, LocalChatMessageV2>,
      ),
      LocalChatMessageV2,
      PrefetchHooks Function()
    >;
typedef $$ChatContentBlocksTableCreateCompanionBuilder =
    ChatContentBlocksCompanion Function({
      required String id,
      required String messageId,
      required int blockIndex,
      required String type,
      Value<String?> textContent,
      Value<String?> toolUseId,
      Value<String?> toolUseName,
      Value<String?> toolUseInputJson,
      Value<String?> toolResultId,
      Value<bool?> toolResultIsError,
      Value<String?> toolResultContentJson,
      Value<String?> imageMimeType,
      Value<String?> imageData,
      Value<String> state,
      required DateTime createdAt,
      required DateTime updatedAt,
      Value<String> ownerKey,
      Value<int> rowid,
    });
typedef $$ChatContentBlocksTableUpdateCompanionBuilder =
    ChatContentBlocksCompanion Function({
      Value<String> id,
      Value<String> messageId,
      Value<int> blockIndex,
      Value<String> type,
      Value<String?> textContent,
      Value<String?> toolUseId,
      Value<String?> toolUseName,
      Value<String?> toolUseInputJson,
      Value<String?> toolResultId,
      Value<bool?> toolResultIsError,
      Value<String?> toolResultContentJson,
      Value<String?> imageMimeType,
      Value<String?> imageData,
      Value<String> state,
      Value<DateTime> createdAt,
      Value<DateTime> updatedAt,
      Value<String> ownerKey,
      Value<int> rowid,
    });

class $$ChatContentBlocksTableFilterComposer
    extends Composer<_$AppDb, $ChatContentBlocksTable> {
  $$ChatContentBlocksTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get messageId => $composableBuilder(
    column: $table.messageId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get blockIndex => $composableBuilder(
    column: $table.blockIndex,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get type => $composableBuilder(
    column: $table.type,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get textContent => $composableBuilder(
    column: $table.textContent,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get toolUseId => $composableBuilder(
    column: $table.toolUseId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get toolUseName => $composableBuilder(
    column: $table.toolUseName,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get toolUseInputJson => $composableBuilder(
    column: $table.toolUseInputJson,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get toolResultId => $composableBuilder(
    column: $table.toolResultId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<bool> get toolResultIsError => $composableBuilder(
    column: $table.toolResultIsError,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get toolResultContentJson => $composableBuilder(
    column: $table.toolResultContentJson,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get imageMimeType => $composableBuilder(
    column: $table.imageMimeType,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get imageData => $composableBuilder(
    column: $table.imageData,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get state => $composableBuilder(
    column: $table.state,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get ownerKey => $composableBuilder(
    column: $table.ownerKey,
    builder: (column) => ColumnFilters(column),
  );
}

class $$ChatContentBlocksTableOrderingComposer
    extends Composer<_$AppDb, $ChatContentBlocksTable> {
  $$ChatContentBlocksTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get messageId => $composableBuilder(
    column: $table.messageId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get blockIndex => $composableBuilder(
    column: $table.blockIndex,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get type => $composableBuilder(
    column: $table.type,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get textContent => $composableBuilder(
    column: $table.textContent,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get toolUseId => $composableBuilder(
    column: $table.toolUseId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get toolUseName => $composableBuilder(
    column: $table.toolUseName,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get toolUseInputJson => $composableBuilder(
    column: $table.toolUseInputJson,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get toolResultId => $composableBuilder(
    column: $table.toolResultId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<bool> get toolResultIsError => $composableBuilder(
    column: $table.toolResultIsError,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get toolResultContentJson => $composableBuilder(
    column: $table.toolResultContentJson,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get imageMimeType => $composableBuilder(
    column: $table.imageMimeType,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get imageData => $composableBuilder(
    column: $table.imageData,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get state => $composableBuilder(
    column: $table.state,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get ownerKey => $composableBuilder(
    column: $table.ownerKey,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$ChatContentBlocksTableAnnotationComposer
    extends Composer<_$AppDb, $ChatContentBlocksTable> {
  $$ChatContentBlocksTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get messageId =>
      $composableBuilder(column: $table.messageId, builder: (column) => column);

  GeneratedColumn<int> get blockIndex => $composableBuilder(
    column: $table.blockIndex,
    builder: (column) => column,
  );

  GeneratedColumn<String> get type =>
      $composableBuilder(column: $table.type, builder: (column) => column);

  GeneratedColumn<String> get textContent => $composableBuilder(
    column: $table.textContent,
    builder: (column) => column,
  );

  GeneratedColumn<String> get toolUseId =>
      $composableBuilder(column: $table.toolUseId, builder: (column) => column);

  GeneratedColumn<String> get toolUseName => $composableBuilder(
    column: $table.toolUseName,
    builder: (column) => column,
  );

  GeneratedColumn<String> get toolUseInputJson => $composableBuilder(
    column: $table.toolUseInputJson,
    builder: (column) => column,
  );

  GeneratedColumn<String> get toolResultId => $composableBuilder(
    column: $table.toolResultId,
    builder: (column) => column,
  );

  GeneratedColumn<bool> get toolResultIsError => $composableBuilder(
    column: $table.toolResultIsError,
    builder: (column) => column,
  );

  GeneratedColumn<String> get toolResultContentJson => $composableBuilder(
    column: $table.toolResultContentJson,
    builder: (column) => column,
  );

  GeneratedColumn<String> get imageMimeType => $composableBuilder(
    column: $table.imageMimeType,
    builder: (column) => column,
  );

  GeneratedColumn<String> get imageData =>
      $composableBuilder(column: $table.imageData, builder: (column) => column);

  GeneratedColumn<String> get state =>
      $composableBuilder(column: $table.state, builder: (column) => column);

  GeneratedColumn<DateTime> get createdAt =>
      $composableBuilder(column: $table.createdAt, builder: (column) => column);

  GeneratedColumn<DateTime> get updatedAt =>
      $composableBuilder(column: $table.updatedAt, builder: (column) => column);

  GeneratedColumn<String> get ownerKey =>
      $composableBuilder(column: $table.ownerKey, builder: (column) => column);
}

class $$ChatContentBlocksTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $ChatContentBlocksTable,
          LocalChatContentBlock,
          $$ChatContentBlocksTableFilterComposer,
          $$ChatContentBlocksTableOrderingComposer,
          $$ChatContentBlocksTableAnnotationComposer,
          $$ChatContentBlocksTableCreateCompanionBuilder,
          $$ChatContentBlocksTableUpdateCompanionBuilder,
          (
            LocalChatContentBlock,
            BaseReferences<
              _$AppDb,
              $ChatContentBlocksTable,
              LocalChatContentBlock
            >,
          ),
          LocalChatContentBlock,
          PrefetchHooks Function()
        > {
  $$ChatContentBlocksTableTableManager(
    _$AppDb db,
    $ChatContentBlocksTable table,
  ) : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$ChatContentBlocksTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$ChatContentBlocksTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$ChatContentBlocksTableAnnotationComposer(
                $db: db,
                $table: table,
              ),
          updateCompanionCallback:
              ({
                Value<String> id = const Value.absent(),
                Value<String> messageId = const Value.absent(),
                Value<int> blockIndex = const Value.absent(),
                Value<String> type = const Value.absent(),
                Value<String?> textContent = const Value.absent(),
                Value<String?> toolUseId = const Value.absent(),
                Value<String?> toolUseName = const Value.absent(),
                Value<String?> toolUseInputJson = const Value.absent(),
                Value<String?> toolResultId = const Value.absent(),
                Value<bool?> toolResultIsError = const Value.absent(),
                Value<String?> toolResultContentJson = const Value.absent(),
                Value<String?> imageMimeType = const Value.absent(),
                Value<String?> imageData = const Value.absent(),
                Value<String> state = const Value.absent(),
                Value<DateTime> createdAt = const Value.absent(),
                Value<DateTime> updatedAt = const Value.absent(),
                Value<String> ownerKey = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => ChatContentBlocksCompanion(
                id: id,
                messageId: messageId,
                blockIndex: blockIndex,
                type: type,
                textContent: textContent,
                toolUseId: toolUseId,
                toolUseName: toolUseName,
                toolUseInputJson: toolUseInputJson,
                toolResultId: toolResultId,
                toolResultIsError: toolResultIsError,
                toolResultContentJson: toolResultContentJson,
                imageMimeType: imageMimeType,
                imageData: imageData,
                state: state,
                createdAt: createdAt,
                updatedAt: updatedAt,
                ownerKey: ownerKey,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String id,
                required String messageId,
                required int blockIndex,
                required String type,
                Value<String?> textContent = const Value.absent(),
                Value<String?> toolUseId = const Value.absent(),
                Value<String?> toolUseName = const Value.absent(),
                Value<String?> toolUseInputJson = const Value.absent(),
                Value<String?> toolResultId = const Value.absent(),
                Value<bool?> toolResultIsError = const Value.absent(),
                Value<String?> toolResultContentJson = const Value.absent(),
                Value<String?> imageMimeType = const Value.absent(),
                Value<String?> imageData = const Value.absent(),
                Value<String> state = const Value.absent(),
                required DateTime createdAt,
                required DateTime updatedAt,
                Value<String> ownerKey = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => ChatContentBlocksCompanion.insert(
                id: id,
                messageId: messageId,
                blockIndex: blockIndex,
                type: type,
                textContent: textContent,
                toolUseId: toolUseId,
                toolUseName: toolUseName,
                toolUseInputJson: toolUseInputJson,
                toolResultId: toolResultId,
                toolResultIsError: toolResultIsError,
                toolResultContentJson: toolResultContentJson,
                imageMimeType: imageMimeType,
                imageData: imageData,
                state: state,
                createdAt: createdAt,
                updatedAt: updatedAt,
                ownerKey: ownerKey,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$ChatContentBlocksTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $ChatContentBlocksTable,
      LocalChatContentBlock,
      $$ChatContentBlocksTableFilterComposer,
      $$ChatContentBlocksTableOrderingComposer,
      $$ChatContentBlocksTableAnnotationComposer,
      $$ChatContentBlocksTableCreateCompanionBuilder,
      $$ChatContentBlocksTableUpdateCompanionBuilder,
      (
        LocalChatContentBlock,
        BaseReferences<_$AppDb, $ChatContentBlocksTable, LocalChatContentBlock>,
      ),
      LocalChatContentBlock,
      PrefetchHooks Function()
    >;
typedef $$ChatSessionsTableCreateCompanionBuilder =
    ChatSessionsCompanion Function({
      required String sessionId,
      required String threadId,
      required String mode,
      required String sessionToken,
      required DateTime tokenExpiresAt,
      Value<int> lastSeenSeq,
      required String status,
      required DateTime createdAt,
      Value<DateTime?> closedAt,
      Value<String> ownerKey,
      Value<int> rowid,
    });
typedef $$ChatSessionsTableUpdateCompanionBuilder =
    ChatSessionsCompanion Function({
      Value<String> sessionId,
      Value<String> threadId,
      Value<String> mode,
      Value<String> sessionToken,
      Value<DateTime> tokenExpiresAt,
      Value<int> lastSeenSeq,
      Value<String> status,
      Value<DateTime> createdAt,
      Value<DateTime?> closedAt,
      Value<String> ownerKey,
      Value<int> rowid,
    });

class $$ChatSessionsTableFilterComposer
    extends Composer<_$AppDb, $ChatSessionsTable> {
  $$ChatSessionsTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get sessionId => $composableBuilder(
    column: $table.sessionId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get threadId => $composableBuilder(
    column: $table.threadId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get mode => $composableBuilder(
    column: $table.mode,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get sessionToken => $composableBuilder(
    column: $table.sessionToken,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get tokenExpiresAt => $composableBuilder(
    column: $table.tokenExpiresAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get lastSeenSeq => $composableBuilder(
    column: $table.lastSeenSeq,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get status => $composableBuilder(
    column: $table.status,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get closedAt => $composableBuilder(
    column: $table.closedAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get ownerKey => $composableBuilder(
    column: $table.ownerKey,
    builder: (column) => ColumnFilters(column),
  );
}

class $$ChatSessionsTableOrderingComposer
    extends Composer<_$AppDb, $ChatSessionsTable> {
  $$ChatSessionsTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get sessionId => $composableBuilder(
    column: $table.sessionId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get threadId => $composableBuilder(
    column: $table.threadId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get mode => $composableBuilder(
    column: $table.mode,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get sessionToken => $composableBuilder(
    column: $table.sessionToken,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get tokenExpiresAt => $composableBuilder(
    column: $table.tokenExpiresAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get lastSeenSeq => $composableBuilder(
    column: $table.lastSeenSeq,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get status => $composableBuilder(
    column: $table.status,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get closedAt => $composableBuilder(
    column: $table.closedAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get ownerKey => $composableBuilder(
    column: $table.ownerKey,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$ChatSessionsTableAnnotationComposer
    extends Composer<_$AppDb, $ChatSessionsTable> {
  $$ChatSessionsTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get sessionId =>
      $composableBuilder(column: $table.sessionId, builder: (column) => column);

  GeneratedColumn<String> get threadId =>
      $composableBuilder(column: $table.threadId, builder: (column) => column);

  GeneratedColumn<String> get mode =>
      $composableBuilder(column: $table.mode, builder: (column) => column);

  GeneratedColumn<String> get sessionToken => $composableBuilder(
    column: $table.sessionToken,
    builder: (column) => column,
  );

  GeneratedColumn<DateTime> get tokenExpiresAt => $composableBuilder(
    column: $table.tokenExpiresAt,
    builder: (column) => column,
  );

  GeneratedColumn<int> get lastSeenSeq => $composableBuilder(
    column: $table.lastSeenSeq,
    builder: (column) => column,
  );

  GeneratedColumn<String> get status =>
      $composableBuilder(column: $table.status, builder: (column) => column);

  GeneratedColumn<DateTime> get createdAt =>
      $composableBuilder(column: $table.createdAt, builder: (column) => column);

  GeneratedColumn<DateTime> get closedAt =>
      $composableBuilder(column: $table.closedAt, builder: (column) => column);

  GeneratedColumn<String> get ownerKey =>
      $composableBuilder(column: $table.ownerKey, builder: (column) => column);
}

class $$ChatSessionsTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $ChatSessionsTable,
          LocalChatSession,
          $$ChatSessionsTableFilterComposer,
          $$ChatSessionsTableOrderingComposer,
          $$ChatSessionsTableAnnotationComposer,
          $$ChatSessionsTableCreateCompanionBuilder,
          $$ChatSessionsTableUpdateCompanionBuilder,
          (
            LocalChatSession,
            BaseReferences<_$AppDb, $ChatSessionsTable, LocalChatSession>,
          ),
          LocalChatSession,
          PrefetchHooks Function()
        > {
  $$ChatSessionsTableTableManager(_$AppDb db, $ChatSessionsTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$ChatSessionsTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$ChatSessionsTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$ChatSessionsTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> sessionId = const Value.absent(),
                Value<String> threadId = const Value.absent(),
                Value<String> mode = const Value.absent(),
                Value<String> sessionToken = const Value.absent(),
                Value<DateTime> tokenExpiresAt = const Value.absent(),
                Value<int> lastSeenSeq = const Value.absent(),
                Value<String> status = const Value.absent(),
                Value<DateTime> createdAt = const Value.absent(),
                Value<DateTime?> closedAt = const Value.absent(),
                Value<String> ownerKey = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => ChatSessionsCompanion(
                sessionId: sessionId,
                threadId: threadId,
                mode: mode,
                sessionToken: sessionToken,
                tokenExpiresAt: tokenExpiresAt,
                lastSeenSeq: lastSeenSeq,
                status: status,
                createdAt: createdAt,
                closedAt: closedAt,
                ownerKey: ownerKey,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String sessionId,
                required String threadId,
                required String mode,
                required String sessionToken,
                required DateTime tokenExpiresAt,
                Value<int> lastSeenSeq = const Value.absent(),
                required String status,
                required DateTime createdAt,
                Value<DateTime?> closedAt = const Value.absent(),
                Value<String> ownerKey = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => ChatSessionsCompanion.insert(
                sessionId: sessionId,
                threadId: threadId,
                mode: mode,
                sessionToken: sessionToken,
                tokenExpiresAt: tokenExpiresAt,
                lastSeenSeq: lastSeenSeq,
                status: status,
                createdAt: createdAt,
                closedAt: closedAt,
                ownerKey: ownerKey,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$ChatSessionsTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $ChatSessionsTable,
      LocalChatSession,
      $$ChatSessionsTableFilterComposer,
      $$ChatSessionsTableOrderingComposer,
      $$ChatSessionsTableAnnotationComposer,
      $$ChatSessionsTableCreateCompanionBuilder,
      $$ChatSessionsTableUpdateCompanionBuilder,
      (
        LocalChatSession,
        BaseReferences<_$AppDb, $ChatSessionsTable, LocalChatSession>,
      ),
      LocalChatSession,
      PrefetchHooks Function()
    >;
typedef $$MessageReactionsV2TableCreateCompanionBuilder =
    MessageReactionsV2Companion Function({
      Value<int> id,
      required String messageId,
      required String threadId,
      required String kind,
      required DateTime createdAt,
      Value<String> ownerKey,
    });
typedef $$MessageReactionsV2TableUpdateCompanionBuilder =
    MessageReactionsV2Companion Function({
      Value<int> id,
      Value<String> messageId,
      Value<String> threadId,
      Value<String> kind,
      Value<DateTime> createdAt,
      Value<String> ownerKey,
    });

class $$MessageReactionsV2TableFilterComposer
    extends Composer<_$AppDb, $MessageReactionsV2Table> {
  $$MessageReactionsV2TableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<int> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get messageId => $composableBuilder(
    column: $table.messageId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get threadId => $composableBuilder(
    column: $table.threadId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get kind => $composableBuilder(
    column: $table.kind,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get ownerKey => $composableBuilder(
    column: $table.ownerKey,
    builder: (column) => ColumnFilters(column),
  );
}

class $$MessageReactionsV2TableOrderingComposer
    extends Composer<_$AppDb, $MessageReactionsV2Table> {
  $$MessageReactionsV2TableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<int> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get messageId => $composableBuilder(
    column: $table.messageId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get threadId => $composableBuilder(
    column: $table.threadId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get kind => $composableBuilder(
    column: $table.kind,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get ownerKey => $composableBuilder(
    column: $table.ownerKey,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$MessageReactionsV2TableAnnotationComposer
    extends Composer<_$AppDb, $MessageReactionsV2Table> {
  $$MessageReactionsV2TableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<int> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get messageId =>
      $composableBuilder(column: $table.messageId, builder: (column) => column);

  GeneratedColumn<String> get threadId =>
      $composableBuilder(column: $table.threadId, builder: (column) => column);

  GeneratedColumn<String> get kind =>
      $composableBuilder(column: $table.kind, builder: (column) => column);

  GeneratedColumn<DateTime> get createdAt =>
      $composableBuilder(column: $table.createdAt, builder: (column) => column);

  GeneratedColumn<String> get ownerKey =>
      $composableBuilder(column: $table.ownerKey, builder: (column) => column);
}

class $$MessageReactionsV2TableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $MessageReactionsV2Table,
          LocalMessageReactionV2,
          $$MessageReactionsV2TableFilterComposer,
          $$MessageReactionsV2TableOrderingComposer,
          $$MessageReactionsV2TableAnnotationComposer,
          $$MessageReactionsV2TableCreateCompanionBuilder,
          $$MessageReactionsV2TableUpdateCompanionBuilder,
          (
            LocalMessageReactionV2,
            BaseReferences<
              _$AppDb,
              $MessageReactionsV2Table,
              LocalMessageReactionV2
            >,
          ),
          LocalMessageReactionV2,
          PrefetchHooks Function()
        > {
  $$MessageReactionsV2TableTableManager(
    _$AppDb db,
    $MessageReactionsV2Table table,
  ) : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$MessageReactionsV2TableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$MessageReactionsV2TableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$MessageReactionsV2TableAnnotationComposer(
                $db: db,
                $table: table,
              ),
          updateCompanionCallback:
              ({
                Value<int> id = const Value.absent(),
                Value<String> messageId = const Value.absent(),
                Value<String> threadId = const Value.absent(),
                Value<String> kind = const Value.absent(),
                Value<DateTime> createdAt = const Value.absent(),
                Value<String> ownerKey = const Value.absent(),
              }) => MessageReactionsV2Companion(
                id: id,
                messageId: messageId,
                threadId: threadId,
                kind: kind,
                createdAt: createdAt,
                ownerKey: ownerKey,
              ),
          createCompanionCallback:
              ({
                Value<int> id = const Value.absent(),
                required String messageId,
                required String threadId,
                required String kind,
                required DateTime createdAt,
                Value<String> ownerKey = const Value.absent(),
              }) => MessageReactionsV2Companion.insert(
                id: id,
                messageId: messageId,
                threadId: threadId,
                kind: kind,
                createdAt: createdAt,
                ownerKey: ownerKey,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$MessageReactionsV2TableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $MessageReactionsV2Table,
      LocalMessageReactionV2,
      $$MessageReactionsV2TableFilterComposer,
      $$MessageReactionsV2TableOrderingComposer,
      $$MessageReactionsV2TableAnnotationComposer,
      $$MessageReactionsV2TableCreateCompanionBuilder,
      $$MessageReactionsV2TableUpdateCompanionBuilder,
      (
        LocalMessageReactionV2,
        BaseReferences<
          _$AppDb,
          $MessageReactionsV2Table,
          LocalMessageReactionV2
        >,
      ),
      LocalMessageReactionV2,
      PrefetchHooks Function()
    >;
typedef $$AigcTasksTableCreateCompanionBuilder =
    AigcTasksCompanion Function({
      required String id,
      required String userId,
      required String type,
      required String modelCode,
      Value<String?> providerCode,
      required String status,
      Value<int> progress,
      required String prompt,
      Value<String?> negativePrompt,
      Value<String> paramsJson,
      Value<String> outputsJson,
      Value<int> costCredits,
      Value<int> refundedCredits,
      Value<bool> isPublic,
      Value<String?> errorCode,
      Value<String?> errorMessage,
      Value<String?> localTempId,
      required DateTime createdAt,
      Value<DateTime?> queuedAt,
      Value<DateTime?> startedAt,
      Value<DateTime?> completedAt,
      required DateTime updatedAt,
      Value<int> rowid,
    });
typedef $$AigcTasksTableUpdateCompanionBuilder =
    AigcTasksCompanion Function({
      Value<String> id,
      Value<String> userId,
      Value<String> type,
      Value<String> modelCode,
      Value<String?> providerCode,
      Value<String> status,
      Value<int> progress,
      Value<String> prompt,
      Value<String?> negativePrompt,
      Value<String> paramsJson,
      Value<String> outputsJson,
      Value<int> costCredits,
      Value<int> refundedCredits,
      Value<bool> isPublic,
      Value<String?> errorCode,
      Value<String?> errorMessage,
      Value<String?> localTempId,
      Value<DateTime> createdAt,
      Value<DateTime?> queuedAt,
      Value<DateTime?> startedAt,
      Value<DateTime?> completedAt,
      Value<DateTime> updatedAt,
      Value<int> rowid,
    });

class $$AigcTasksTableFilterComposer
    extends Composer<_$AppDb, $AigcTasksTable> {
  $$AigcTasksTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get userId => $composableBuilder(
    column: $table.userId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get type => $composableBuilder(
    column: $table.type,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get modelCode => $composableBuilder(
    column: $table.modelCode,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get providerCode => $composableBuilder(
    column: $table.providerCode,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get status => $composableBuilder(
    column: $table.status,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get progress => $composableBuilder(
    column: $table.progress,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get prompt => $composableBuilder(
    column: $table.prompt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get negativePrompt => $composableBuilder(
    column: $table.negativePrompt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get paramsJson => $composableBuilder(
    column: $table.paramsJson,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get outputsJson => $composableBuilder(
    column: $table.outputsJson,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get costCredits => $composableBuilder(
    column: $table.costCredits,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<int> get refundedCredits => $composableBuilder(
    column: $table.refundedCredits,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<bool> get isPublic => $composableBuilder(
    column: $table.isPublic,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get errorCode => $composableBuilder(
    column: $table.errorCode,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get errorMessage => $composableBuilder(
    column: $table.errorMessage,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get localTempId => $composableBuilder(
    column: $table.localTempId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get queuedAt => $composableBuilder(
    column: $table.queuedAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get startedAt => $composableBuilder(
    column: $table.startedAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get completedAt => $composableBuilder(
    column: $table.completedAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnFilters(column),
  );
}

class $$AigcTasksTableOrderingComposer
    extends Composer<_$AppDb, $AigcTasksTable> {
  $$AigcTasksTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get userId => $composableBuilder(
    column: $table.userId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get type => $composableBuilder(
    column: $table.type,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get modelCode => $composableBuilder(
    column: $table.modelCode,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get providerCode => $composableBuilder(
    column: $table.providerCode,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get status => $composableBuilder(
    column: $table.status,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get progress => $composableBuilder(
    column: $table.progress,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get prompt => $composableBuilder(
    column: $table.prompt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get negativePrompt => $composableBuilder(
    column: $table.negativePrompt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get paramsJson => $composableBuilder(
    column: $table.paramsJson,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get outputsJson => $composableBuilder(
    column: $table.outputsJson,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get costCredits => $composableBuilder(
    column: $table.costCredits,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<int> get refundedCredits => $composableBuilder(
    column: $table.refundedCredits,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<bool> get isPublic => $composableBuilder(
    column: $table.isPublic,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get errorCode => $composableBuilder(
    column: $table.errorCode,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get errorMessage => $composableBuilder(
    column: $table.errorMessage,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get localTempId => $composableBuilder(
    column: $table.localTempId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get createdAt => $composableBuilder(
    column: $table.createdAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get queuedAt => $composableBuilder(
    column: $table.queuedAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get startedAt => $composableBuilder(
    column: $table.startedAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get completedAt => $composableBuilder(
    column: $table.completedAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$AigcTasksTableAnnotationComposer
    extends Composer<_$AppDb, $AigcTasksTable> {
  $$AigcTasksTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get userId =>
      $composableBuilder(column: $table.userId, builder: (column) => column);

  GeneratedColumn<String> get type =>
      $composableBuilder(column: $table.type, builder: (column) => column);

  GeneratedColumn<String> get modelCode =>
      $composableBuilder(column: $table.modelCode, builder: (column) => column);

  GeneratedColumn<String> get providerCode => $composableBuilder(
    column: $table.providerCode,
    builder: (column) => column,
  );

  GeneratedColumn<String> get status =>
      $composableBuilder(column: $table.status, builder: (column) => column);

  GeneratedColumn<int> get progress =>
      $composableBuilder(column: $table.progress, builder: (column) => column);

  GeneratedColumn<String> get prompt =>
      $composableBuilder(column: $table.prompt, builder: (column) => column);

  GeneratedColumn<String> get negativePrompt => $composableBuilder(
    column: $table.negativePrompt,
    builder: (column) => column,
  );

  GeneratedColumn<String> get paramsJson => $composableBuilder(
    column: $table.paramsJson,
    builder: (column) => column,
  );

  GeneratedColumn<String> get outputsJson => $composableBuilder(
    column: $table.outputsJson,
    builder: (column) => column,
  );

  GeneratedColumn<int> get costCredits => $composableBuilder(
    column: $table.costCredits,
    builder: (column) => column,
  );

  GeneratedColumn<int> get refundedCredits => $composableBuilder(
    column: $table.refundedCredits,
    builder: (column) => column,
  );

  GeneratedColumn<bool> get isPublic =>
      $composableBuilder(column: $table.isPublic, builder: (column) => column);

  GeneratedColumn<String> get errorCode =>
      $composableBuilder(column: $table.errorCode, builder: (column) => column);

  GeneratedColumn<String> get errorMessage => $composableBuilder(
    column: $table.errorMessage,
    builder: (column) => column,
  );

  GeneratedColumn<String> get localTempId => $composableBuilder(
    column: $table.localTempId,
    builder: (column) => column,
  );

  GeneratedColumn<DateTime> get createdAt =>
      $composableBuilder(column: $table.createdAt, builder: (column) => column);

  GeneratedColumn<DateTime> get queuedAt =>
      $composableBuilder(column: $table.queuedAt, builder: (column) => column);

  GeneratedColumn<DateTime> get startedAt =>
      $composableBuilder(column: $table.startedAt, builder: (column) => column);

  GeneratedColumn<DateTime> get completedAt => $composableBuilder(
    column: $table.completedAt,
    builder: (column) => column,
  );

  GeneratedColumn<DateTime> get updatedAt =>
      $composableBuilder(column: $table.updatedAt, builder: (column) => column);
}

class $$AigcTasksTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $AigcTasksTable,
          LocalAigcTask,
          $$AigcTasksTableFilterComposer,
          $$AigcTasksTableOrderingComposer,
          $$AigcTasksTableAnnotationComposer,
          $$AigcTasksTableCreateCompanionBuilder,
          $$AigcTasksTableUpdateCompanionBuilder,
          (
            LocalAigcTask,
            BaseReferences<_$AppDb, $AigcTasksTable, LocalAigcTask>,
          ),
          LocalAigcTask,
          PrefetchHooks Function()
        > {
  $$AigcTasksTableTableManager(_$AppDb db, $AigcTasksTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$AigcTasksTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$AigcTasksTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$AigcTasksTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> id = const Value.absent(),
                Value<String> userId = const Value.absent(),
                Value<String> type = const Value.absent(),
                Value<String> modelCode = const Value.absent(),
                Value<String?> providerCode = const Value.absent(),
                Value<String> status = const Value.absent(),
                Value<int> progress = const Value.absent(),
                Value<String> prompt = const Value.absent(),
                Value<String?> negativePrompt = const Value.absent(),
                Value<String> paramsJson = const Value.absent(),
                Value<String> outputsJson = const Value.absent(),
                Value<int> costCredits = const Value.absent(),
                Value<int> refundedCredits = const Value.absent(),
                Value<bool> isPublic = const Value.absent(),
                Value<String?> errorCode = const Value.absent(),
                Value<String?> errorMessage = const Value.absent(),
                Value<String?> localTempId = const Value.absent(),
                Value<DateTime> createdAt = const Value.absent(),
                Value<DateTime?> queuedAt = const Value.absent(),
                Value<DateTime?> startedAt = const Value.absent(),
                Value<DateTime?> completedAt = const Value.absent(),
                Value<DateTime> updatedAt = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => AigcTasksCompanion(
                id: id,
                userId: userId,
                type: type,
                modelCode: modelCode,
                providerCode: providerCode,
                status: status,
                progress: progress,
                prompt: prompt,
                negativePrompt: negativePrompt,
                paramsJson: paramsJson,
                outputsJson: outputsJson,
                costCredits: costCredits,
                refundedCredits: refundedCredits,
                isPublic: isPublic,
                errorCode: errorCode,
                errorMessage: errorMessage,
                localTempId: localTempId,
                createdAt: createdAt,
                queuedAt: queuedAt,
                startedAt: startedAt,
                completedAt: completedAt,
                updatedAt: updatedAt,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String id,
                required String userId,
                required String type,
                required String modelCode,
                Value<String?> providerCode = const Value.absent(),
                required String status,
                Value<int> progress = const Value.absent(),
                required String prompt,
                Value<String?> negativePrompt = const Value.absent(),
                Value<String> paramsJson = const Value.absent(),
                Value<String> outputsJson = const Value.absent(),
                Value<int> costCredits = const Value.absent(),
                Value<int> refundedCredits = const Value.absent(),
                Value<bool> isPublic = const Value.absent(),
                Value<String?> errorCode = const Value.absent(),
                Value<String?> errorMessage = const Value.absent(),
                Value<String?> localTempId = const Value.absent(),
                required DateTime createdAt,
                Value<DateTime?> queuedAt = const Value.absent(),
                Value<DateTime?> startedAt = const Value.absent(),
                Value<DateTime?> completedAt = const Value.absent(),
                required DateTime updatedAt,
                Value<int> rowid = const Value.absent(),
              }) => AigcTasksCompanion.insert(
                id: id,
                userId: userId,
                type: type,
                modelCode: modelCode,
                providerCode: providerCode,
                status: status,
                progress: progress,
                prompt: prompt,
                negativePrompt: negativePrompt,
                paramsJson: paramsJson,
                outputsJson: outputsJson,
                costCredits: costCredits,
                refundedCredits: refundedCredits,
                isPublic: isPublic,
                errorCode: errorCode,
                errorMessage: errorMessage,
                localTempId: localTempId,
                createdAt: createdAt,
                queuedAt: queuedAt,
                startedAt: startedAt,
                completedAt: completedAt,
                updatedAt: updatedAt,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$AigcTasksTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $AigcTasksTable,
      LocalAigcTask,
      $$AigcTasksTableFilterComposer,
      $$AigcTasksTableOrderingComposer,
      $$AigcTasksTableAnnotationComposer,
      $$AigcTasksTableCreateCompanionBuilder,
      $$AigcTasksTableUpdateCompanionBuilder,
      (LocalAigcTask, BaseReferences<_$AppDb, $AigcTasksTable, LocalAigcTask>),
      LocalAigcTask,
      PrefetchHooks Function()
    >;
typedef $$SseCursorsTableCreateCompanionBuilder =
    SseCursorsCompanion Function({
      required String scope,
      required String lastEventId,
      required DateTime updatedAt,
      Value<int> rowid,
    });
typedef $$SseCursorsTableUpdateCompanionBuilder =
    SseCursorsCompanion Function({
      Value<String> scope,
      Value<String> lastEventId,
      Value<DateTime> updatedAt,
      Value<int> rowid,
    });

class $$SseCursorsTableFilterComposer
    extends Composer<_$AppDb, $SseCursorsTable> {
  $$SseCursorsTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get scope => $composableBuilder(
    column: $table.scope,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get lastEventId => $composableBuilder(
    column: $table.lastEventId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnFilters(column),
  );
}

class $$SseCursorsTableOrderingComposer
    extends Composer<_$AppDb, $SseCursorsTable> {
  $$SseCursorsTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get scope => $composableBuilder(
    column: $table.scope,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get lastEventId => $composableBuilder(
    column: $table.lastEventId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get updatedAt => $composableBuilder(
    column: $table.updatedAt,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$SseCursorsTableAnnotationComposer
    extends Composer<_$AppDb, $SseCursorsTable> {
  $$SseCursorsTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get scope =>
      $composableBuilder(column: $table.scope, builder: (column) => column);

  GeneratedColumn<String> get lastEventId => $composableBuilder(
    column: $table.lastEventId,
    builder: (column) => column,
  );

  GeneratedColumn<DateTime> get updatedAt =>
      $composableBuilder(column: $table.updatedAt, builder: (column) => column);
}

class $$SseCursorsTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $SseCursorsTable,
          LocalSseCursor,
          $$SseCursorsTableFilterComposer,
          $$SseCursorsTableOrderingComposer,
          $$SseCursorsTableAnnotationComposer,
          $$SseCursorsTableCreateCompanionBuilder,
          $$SseCursorsTableUpdateCompanionBuilder,
          (
            LocalSseCursor,
            BaseReferences<_$AppDb, $SseCursorsTable, LocalSseCursor>,
          ),
          LocalSseCursor,
          PrefetchHooks Function()
        > {
  $$SseCursorsTableTableManager(_$AppDb db, $SseCursorsTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$SseCursorsTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$SseCursorsTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$SseCursorsTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> scope = const Value.absent(),
                Value<String> lastEventId = const Value.absent(),
                Value<DateTime> updatedAt = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => SseCursorsCompanion(
                scope: scope,
                lastEventId: lastEventId,
                updatedAt: updatedAt,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String scope,
                required String lastEventId,
                required DateTime updatedAt,
                Value<int> rowid = const Value.absent(),
              }) => SseCursorsCompanion.insert(
                scope: scope,
                lastEventId: lastEventId,
                updatedAt: updatedAt,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$SseCursorsTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $SseCursorsTable,
      LocalSseCursor,
      $$SseCursorsTableFilterComposer,
      $$SseCursorsTableOrderingComposer,
      $$SseCursorsTableAnnotationComposer,
      $$SseCursorsTableCreateCompanionBuilder,
      $$SseCursorsTableUpdateCompanionBuilder,
      (
        LocalSseCursor,
        BaseReferences<_$AppDb, $SseCursorsTable, LocalSseCursor>,
      ),
      LocalSseCursor,
      PrefetchHooks Function()
    >;
typedef $$RssFeedsCacheTableCreateCompanionBuilder =
    RssFeedsCacheCompanion Function({
      required String id,
      required String scopeId,
      required String payloadJson,
      required DateTime cachedAt,
      Value<int> rowid,
    });
typedef $$RssFeedsCacheTableUpdateCompanionBuilder =
    RssFeedsCacheCompanion Function({
      Value<String> id,
      Value<String> scopeId,
      Value<String> payloadJson,
      Value<DateTime> cachedAt,
      Value<int> rowid,
    });

class $$RssFeedsCacheTableFilterComposer
    extends Composer<_$AppDb, $RssFeedsCacheTable> {
  $$RssFeedsCacheTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get scopeId => $composableBuilder(
    column: $table.scopeId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get payloadJson => $composableBuilder(
    column: $table.payloadJson,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get cachedAt => $composableBuilder(
    column: $table.cachedAt,
    builder: (column) => ColumnFilters(column),
  );
}

class $$RssFeedsCacheTableOrderingComposer
    extends Composer<_$AppDb, $RssFeedsCacheTable> {
  $$RssFeedsCacheTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get scopeId => $composableBuilder(
    column: $table.scopeId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get payloadJson => $composableBuilder(
    column: $table.payloadJson,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get cachedAt => $composableBuilder(
    column: $table.cachedAt,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$RssFeedsCacheTableAnnotationComposer
    extends Composer<_$AppDb, $RssFeedsCacheTable> {
  $$RssFeedsCacheTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get scopeId =>
      $composableBuilder(column: $table.scopeId, builder: (column) => column);

  GeneratedColumn<String> get payloadJson => $composableBuilder(
    column: $table.payloadJson,
    builder: (column) => column,
  );

  GeneratedColumn<DateTime> get cachedAt =>
      $composableBuilder(column: $table.cachedAt, builder: (column) => column);
}

class $$RssFeedsCacheTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $RssFeedsCacheTable,
          LocalRssFeed,
          $$RssFeedsCacheTableFilterComposer,
          $$RssFeedsCacheTableOrderingComposer,
          $$RssFeedsCacheTableAnnotationComposer,
          $$RssFeedsCacheTableCreateCompanionBuilder,
          $$RssFeedsCacheTableUpdateCompanionBuilder,
          (
            LocalRssFeed,
            BaseReferences<_$AppDb, $RssFeedsCacheTable, LocalRssFeed>,
          ),
          LocalRssFeed,
          PrefetchHooks Function()
        > {
  $$RssFeedsCacheTableTableManager(_$AppDb db, $RssFeedsCacheTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$RssFeedsCacheTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$RssFeedsCacheTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$RssFeedsCacheTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> id = const Value.absent(),
                Value<String> scopeId = const Value.absent(),
                Value<String> payloadJson = const Value.absent(),
                Value<DateTime> cachedAt = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => RssFeedsCacheCompanion(
                id: id,
                scopeId: scopeId,
                payloadJson: payloadJson,
                cachedAt: cachedAt,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String id,
                required String scopeId,
                required String payloadJson,
                required DateTime cachedAt,
                Value<int> rowid = const Value.absent(),
              }) => RssFeedsCacheCompanion.insert(
                id: id,
                scopeId: scopeId,
                payloadJson: payloadJson,
                cachedAt: cachedAt,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$RssFeedsCacheTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $RssFeedsCacheTable,
      LocalRssFeed,
      $$RssFeedsCacheTableFilterComposer,
      $$RssFeedsCacheTableOrderingComposer,
      $$RssFeedsCacheTableAnnotationComposer,
      $$RssFeedsCacheTableCreateCompanionBuilder,
      $$RssFeedsCacheTableUpdateCompanionBuilder,
      (
        LocalRssFeed,
        BaseReferences<_$AppDb, $RssFeedsCacheTable, LocalRssFeed>,
      ),
      LocalRssFeed,
      PrefetchHooks Function()
    >;
typedef $$RssEntriesCacheTableCreateCompanionBuilder =
    RssEntriesCacheCompanion Function({
      required String id,
      required String scopeId,
      required String feedId,
      required String payloadJson,
      Value<DateTime?> fetchedAt,
      required DateTime cachedAt,
      Value<int> rowid,
    });
typedef $$RssEntriesCacheTableUpdateCompanionBuilder =
    RssEntriesCacheCompanion Function({
      Value<String> id,
      Value<String> scopeId,
      Value<String> feedId,
      Value<String> payloadJson,
      Value<DateTime?> fetchedAt,
      Value<DateTime> cachedAt,
      Value<int> rowid,
    });

class $$RssEntriesCacheTableFilterComposer
    extends Composer<_$AppDb, $RssEntriesCacheTable> {
  $$RssEntriesCacheTableFilterComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnFilters<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get scopeId => $composableBuilder(
    column: $table.scopeId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get feedId => $composableBuilder(
    column: $table.feedId,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<String> get payloadJson => $composableBuilder(
    column: $table.payloadJson,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get fetchedAt => $composableBuilder(
    column: $table.fetchedAt,
    builder: (column) => ColumnFilters(column),
  );

  ColumnFilters<DateTime> get cachedAt => $composableBuilder(
    column: $table.cachedAt,
    builder: (column) => ColumnFilters(column),
  );
}

class $$RssEntriesCacheTableOrderingComposer
    extends Composer<_$AppDb, $RssEntriesCacheTable> {
  $$RssEntriesCacheTableOrderingComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  ColumnOrderings<String> get id => $composableBuilder(
    column: $table.id,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get scopeId => $composableBuilder(
    column: $table.scopeId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get feedId => $composableBuilder(
    column: $table.feedId,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<String> get payloadJson => $composableBuilder(
    column: $table.payloadJson,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get fetchedAt => $composableBuilder(
    column: $table.fetchedAt,
    builder: (column) => ColumnOrderings(column),
  );

  ColumnOrderings<DateTime> get cachedAt => $composableBuilder(
    column: $table.cachedAt,
    builder: (column) => ColumnOrderings(column),
  );
}

class $$RssEntriesCacheTableAnnotationComposer
    extends Composer<_$AppDb, $RssEntriesCacheTable> {
  $$RssEntriesCacheTableAnnotationComposer({
    required super.$db,
    required super.$table,
    super.joinBuilder,
    super.$addJoinBuilderToRootComposer,
    super.$removeJoinBuilderFromRootComposer,
  });
  GeneratedColumn<String> get id =>
      $composableBuilder(column: $table.id, builder: (column) => column);

  GeneratedColumn<String> get scopeId =>
      $composableBuilder(column: $table.scopeId, builder: (column) => column);

  GeneratedColumn<String> get feedId =>
      $composableBuilder(column: $table.feedId, builder: (column) => column);

  GeneratedColumn<String> get payloadJson => $composableBuilder(
    column: $table.payloadJson,
    builder: (column) => column,
  );

  GeneratedColumn<DateTime> get fetchedAt =>
      $composableBuilder(column: $table.fetchedAt, builder: (column) => column);

  GeneratedColumn<DateTime> get cachedAt =>
      $composableBuilder(column: $table.cachedAt, builder: (column) => column);
}

class $$RssEntriesCacheTableTableManager
    extends
        RootTableManager<
          _$AppDb,
          $RssEntriesCacheTable,
          LocalRssEntry,
          $$RssEntriesCacheTableFilterComposer,
          $$RssEntriesCacheTableOrderingComposer,
          $$RssEntriesCacheTableAnnotationComposer,
          $$RssEntriesCacheTableCreateCompanionBuilder,
          $$RssEntriesCacheTableUpdateCompanionBuilder,
          (
            LocalRssEntry,
            BaseReferences<_$AppDb, $RssEntriesCacheTable, LocalRssEntry>,
          ),
          LocalRssEntry,
          PrefetchHooks Function()
        > {
  $$RssEntriesCacheTableTableManager(_$AppDb db, $RssEntriesCacheTable table)
    : super(
        TableManagerState(
          db: db,
          table: table,
          createFilteringComposer: () =>
              $$RssEntriesCacheTableFilterComposer($db: db, $table: table),
          createOrderingComposer: () =>
              $$RssEntriesCacheTableOrderingComposer($db: db, $table: table),
          createComputedFieldComposer: () =>
              $$RssEntriesCacheTableAnnotationComposer($db: db, $table: table),
          updateCompanionCallback:
              ({
                Value<String> id = const Value.absent(),
                Value<String> scopeId = const Value.absent(),
                Value<String> feedId = const Value.absent(),
                Value<String> payloadJson = const Value.absent(),
                Value<DateTime?> fetchedAt = const Value.absent(),
                Value<DateTime> cachedAt = const Value.absent(),
                Value<int> rowid = const Value.absent(),
              }) => RssEntriesCacheCompanion(
                id: id,
                scopeId: scopeId,
                feedId: feedId,
                payloadJson: payloadJson,
                fetchedAt: fetchedAt,
                cachedAt: cachedAt,
                rowid: rowid,
              ),
          createCompanionCallback:
              ({
                required String id,
                required String scopeId,
                required String feedId,
                required String payloadJson,
                Value<DateTime?> fetchedAt = const Value.absent(),
                required DateTime cachedAt,
                Value<int> rowid = const Value.absent(),
              }) => RssEntriesCacheCompanion.insert(
                id: id,
                scopeId: scopeId,
                feedId: feedId,
                payloadJson: payloadJson,
                fetchedAt: fetchedAt,
                cachedAt: cachedAt,
                rowid: rowid,
              ),
          withReferenceMapper: (p0) => p0
              .map((e) => (e.readTable(table), BaseReferences(db, table, e)))
              .toList(),
          prefetchHooksCallback: null,
        ),
      );
}

typedef $$RssEntriesCacheTableProcessedTableManager =
    ProcessedTableManager<
      _$AppDb,
      $RssEntriesCacheTable,
      LocalRssEntry,
      $$RssEntriesCacheTableFilterComposer,
      $$RssEntriesCacheTableOrderingComposer,
      $$RssEntriesCacheTableAnnotationComposer,
      $$RssEntriesCacheTableCreateCompanionBuilder,
      $$RssEntriesCacheTableUpdateCompanionBuilder,
      (
        LocalRssEntry,
        BaseReferences<_$AppDb, $RssEntriesCacheTable, LocalRssEntry>,
      ),
      LocalRssEntry,
      PrefetchHooks Function()
    >;

class $AppDbManager {
  final _$AppDb _db;
  $AppDbManager(this._db);
  $$WikiProjectsTableTableManager get wikiProjects =>
      $$WikiProjectsTableTableManager(_db, _db.wikiProjects);
  $$WikiPagesTableTableManager get wikiPages =>
      $$WikiPagesTableTableManager(_db, _db.wikiPages);
  $$WikiBlocksTableTableManager get wikiBlocks =>
      $$WikiBlocksTableTableManager(_db, _db.wikiBlocks);
  $$WikiOutboxTableTableManager get wikiOutbox =>
      $$WikiOutboxTableTableManager(_db, _db.wikiOutbox);
  $$NoteNotebooksTableTableManager get noteNotebooks =>
      $$NoteNotebooksTableTableManager(_db, _db.noteNotebooks);
  $$NoteNotesTableTableManager get noteNotes =>
      $$NoteNotesTableTableManager(_db, _db.noteNotes);
  $$NoteTagsTableTableManager get noteTags =>
      $$NoteTagsTableTableManager(_db, _db.noteTags);
  $$NoteNoteTagsTableTableManager get noteNoteTags =>
      $$NoteNoteTagsTableTableManager(_db, _db.noteNoteTags);
  $$NoteOutboxTableTableManager get noteOutbox =>
      $$NoteOutboxTableTableManager(_db, _db.noteOutbox);
  $$CodeTasksTableTableManager get codeTasks =>
      $$CodeTasksTableTableManager(_db, _db.codeTasks);
  $$CodeProjectsTableTableManager get codeProjects =>
      $$CodeProjectsTableTableManager(_db, _db.codeProjects);
  $$CodeTaskArtifactsTableTableManager get codeTaskArtifacts =>
      $$CodeTaskArtifactsTableTableManager(_db, _db.codeTaskArtifacts);
  $$ChatThreadsV2TableTableManager get chatThreadsV2 =>
      $$ChatThreadsV2TableTableManager(_db, _db.chatThreadsV2);
  $$ChatMessagesV2TableTableManager get chatMessagesV2 =>
      $$ChatMessagesV2TableTableManager(_db, _db.chatMessagesV2);
  $$ChatContentBlocksTableTableManager get chatContentBlocks =>
      $$ChatContentBlocksTableTableManager(_db, _db.chatContentBlocks);
  $$ChatSessionsTableTableManager get chatSessions =>
      $$ChatSessionsTableTableManager(_db, _db.chatSessions);
  $$MessageReactionsV2TableTableManager get messageReactionsV2 =>
      $$MessageReactionsV2TableTableManager(_db, _db.messageReactionsV2);
  $$AigcTasksTableTableManager get aigcTasks =>
      $$AigcTasksTableTableManager(_db, _db.aigcTasks);
  $$SseCursorsTableTableManager get sseCursors =>
      $$SseCursorsTableTableManager(_db, _db.sseCursors);
  $$RssFeedsCacheTableTableManager get rssFeedsCache =>
      $$RssFeedsCacheTableTableManager(_db, _db.rssFeedsCache);
  $$RssEntriesCacheTableTableManager get rssEntriesCache =>
      $$RssEntriesCacheTableTableManager(_db, _db.rssEntriesCache);
}
