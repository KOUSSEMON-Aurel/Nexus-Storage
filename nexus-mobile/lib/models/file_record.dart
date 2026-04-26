class FileRecord {
  final int? id;
  final String path;
  final String videoId;
  final int size;
  final String hash;
  final String key;
  final String lastUpdate;
  final bool starred;
  final String? deletedAt;
  final int? parentId;
  final String sha256;
  final String fileKey;
  final bool isArchive;
  final bool hasCustomPassword;
  final String customPasswordHint;
  final String mode;

  FileRecord({
    this.id,
    required this.path,
    required this.videoId,
    required this.size,
    required this.hash,
    required this.key,
    required this.lastUpdate,
    required this.starred,
    this.deletedAt,
    this.parentId,
    required this.sha256,
    required this.fileKey,
    required this.isArchive,
    required this.hasCustomPassword,
    required this.customPasswordHint,
    required this.mode,
  });

  factory FileRecord.fromMap(Map<String, dynamic> map) {
    return FileRecord(
      id: map['id'],
      path: map['path'],
      videoId: map['video_id'] ?? '',
      size: map['size'],
      hash: map['hash'] ?? '',
      key: map['key'] ?? '',
      lastUpdate: map['last_update'],
      starred: map['starred'] == 1,
      deletedAt: map['deleted_at'],
      parentId: map['parent_id'],
      sha256: map['sha256'] ?? '',
      fileKey: map['file_key'] ?? '',
      isArchive: map['is_archive'] == 1,
      hasCustomPassword: map['has_custom_password'] == 1,
      customPasswordHint: map['custom_password_hint'] ?? '',
      mode: map['mode'] ?? 'base',
    );
  }

  Map<String, dynamic> toMap() {
    return {
      if (id != null) 'id': id,
      'path': path,
      'video_id': videoId,
      'size': size,
      'hash': hash,
      'key': key,
      'starred': starred ? 1 : 0,
      'deleted_at': deletedAt,
      'parent_id': parentId,
      'sha256': sha256,
      'file_key': fileKey,
      'is_archive': isArchive ? 1 : 0,
      'has_custom_password': hasCustomPassword ? 1 : 0,
      'custom_password_hint': customPasswordHint,
      'mode': mode,
    };
  }

  FileRecord copyWith({
    int? id,
    String? path,
    String? videoId,
    int? size,
    String? hash,
    String? key,
    String? lastUpdate,
    bool? starred,
    String? deletedAt,
    int? parentId,
    String? sha256,
    String? fileKey,
    bool? isArchive,
    bool? hasCustomPassword,
    String? customPasswordHint,
    String? mode,
  }) {
    return FileRecord(
      id: id ?? this.id,
      path: path ?? this.path,
      videoId: videoId ?? this.videoId,
      size: size ?? this.size,
      hash: hash ?? this.hash,
      key: key ?? this.key,
      lastUpdate: lastUpdate ?? this.lastUpdate,
      starred: starred ?? this.starred,
      deletedAt: deletedAt ?? this.deletedAt,
      parentId: parentId ?? this.parentId,
      sha256: sha256 ?? this.sha256,
      fileKey: fileKey ?? this.fileKey,
      isArchive: isArchive ?? this.isArchive,
      hasCustomPassword: hasCustomPassword ?? this.hasCustomPassword,
      customPasswordHint: customPasswordHint ?? this.customPasswordHint,
      mode: mode ?? this.mode,
    );
  }
}

class FolderRecord {
  final int? id;
  final String name;
  final int? parentId;
  final String? playlistId;

  FolderRecord({this.id, required this.name, this.parentId, this.playlistId});

  factory FolderRecord.fromMap(Map<String, dynamic> map) {
    return FolderRecord(
      id: map['id'],
      name: map['name'],
      parentId: map['parent_id'],
      playlistId: map['playlist_id'],
    );
  }

  Map<String, dynamic> toMap() {
    return {
      if (id != null) 'id': id,
      'name': name,
      'parent_id': parentId,
      'playlist_id': playlistId,
    };
  }
}
