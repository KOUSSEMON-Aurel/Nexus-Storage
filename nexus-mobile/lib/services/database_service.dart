import 'package:sqflite/sqflite.dart';
import 'package:path/path.dart';
import 'dart:async';
import '../models/file_record.dart';

class DatabaseService {
  static final DatabaseService _instance = DatabaseService._internal();
  factory DatabaseService() => _instance;
  DatabaseService._internal();

  Database? _db;
  Future<Database>? _dbFuture;

  // Stream pour le rafraîchissement automatique
  final _changeController = StreamController<void>.broadcast();
  Stream<void> get onChange => _changeController.stream;

  void notifyChange() => _changeController.add(null);

  Future<String> getDatabasePath() async {
    return join(await getDatabasesPath(), 'nexus.db');
  }

  Future<void> close() async {
    if (_db != null) {
      await _db!.close();
      _db = null;
      _dbFuture = null;
    }
  }

  Future<Database> get database async {
    if (_db != null) return _db!;
    _dbFuture ??= _initDatabase();
    _db = await _dbFuture;
    return _db!;
  }

  Future<Database> _initDatabase() async {
    String path = join(await getDatabasesPath(), 'nexus.db');
    return await openDatabase(
      path,
      version: 1,
      onCreate: _onCreate,
      onConfigure: (db) async {
        // In onConfigure, only rawQuery is allowed (not execute)
        // foreign_keys must be enabled here (before onCreate)
        await db.rawQuery('PRAGMA foreign_keys=ON');
      },
      onOpen: (db) async {
        // WAL mode can be set here after the database is fully opened
        await db.rawQuery('PRAGMA journal_mode=WAL');
      },
    );
  }

  Future<void> _onCreate(Database db, int version) async {
    // 1. Files Table
    await db.execute('''
      CREATE TABLE files (
        id         INTEGER PRIMARY KEY AUTOINCREMENT,
        path       TEXT UNIQUE,
        video_id   TEXT,
        size       INTEGER,
        hash       TEXT,
        key        TEXT,
        starred    BOOLEAN DEFAULT 0,
        deleted_at TIMESTAMP,
        last_update TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
        parent_id  INTEGER,
        sha256     TEXT,
        file_key   TEXT DEFAULT '',
        is_archive BOOLEAN DEFAULT 0,
        has_custom_password BOOLEAN DEFAULT 0,
        custom_password_hint TEXT DEFAULT '',
        mode       TEXT DEFAULT 'base'
      )
    ''');

    // 2. Folders Table
    await db.execute('''
      CREATE TABLE folders (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        name TEXT,
        parent_id INTEGER REFERENCES folders(id) ON DELETE CASCADE,
        playlist_id TEXT,
        UNIQUE(name, parent_id)
      )
    ''');

    // 3. Shards Table
    await db.execute('''
      CREATE TABLE shards (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        file_id INTEGER REFERENCES files(id) ON DELETE CASCADE,
        video_id TEXT,
        position INTEGER
      )
    ''');

    // 4. KV Store
    await db.execute('CREATE TABLE kv_store (key TEXT PRIMARY KEY, value TEXT)');

    // 5. Meta Sync & Quota Log
    await db.execute('''
      CREATE TABLE meta_sync (
        key TEXT PRIMARY KEY, 
        value TEXT, 
        last_sync TIMESTAMP DEFAULT CURRENT_TIMESTAMP
      )
    ''');
    await db.execute('CREATE TABLE quota_log (date TEXT PRIMARY KEY, units INTEGER DEFAULT 0)');

    // 6. Tasks
    await db.execute('''
      CREATE TABLE tasks (
        id TEXT PRIMARY KEY,
        type INTEGER,
        file_path TEXT,
        mode TEXT,
        is_manifest BOOLEAN,
        status TEXT,
        progress REAL,
        created_at TIMESTAMP,
        parent_id INTEGER,
        sha256 TEXT
      )
    ''');

    // 7. Recovery State
    await db.execute('''
      CREATE TABLE recovery_state (
        id INTEGER PRIMARY KEY CHECK (id = 1),
        recovery_salt TEXT NOT NULL,
        manifest_revision INTEGER DEFAULT 1,
        last_backup_ts TEXT,
        recovery_packet_drive_id TEXT,
        created_at TEXT DEFAULT CURRENT_TIMESTAMP
      )
    ''');

    // 8. Tombstones & Pending Sync
    await db.execute('''
      CREATE TABLE tombstones (
        file_hash TEXT PRIMARY KEY,
        deleted_at_lsn INTEGER,
        deleted_at_ts TIMESTAMP DEFAULT CURRENT_TIMESTAMP
      )
    ''');
    await db.execute('''
      CREATE TABLE pending_sync (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        file_path TEXT,
        lsn INTEGER,
        status TEXT DEFAULT 'pending',
        created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
      )
    ''');

    // 9. FTS5 Search Index (if possible, we'll try-catch it later or just assume it works on modern mobile)
    try {
      await db.execute("CREATE VIRTUAL TABLE files_fts USING fts5(path, content='files', content_rowid='id')");
      await db.execute('''
        CREATE TRIGGER files_ai AFTER INSERT ON files BEGIN
          INSERT INTO files_fts(rowid, path) VALUES (new.id, new.path);
        END
      ''');
      await db.execute('''
        CREATE TRIGGER files_ad AFTER DELETE ON files BEGIN
          INSERT INTO files_fts(files_fts, rowid, path) VALUES('delete', old.id, old.path);
        END
      ''');
      await db.execute('''
        CREATE TRIGGER files_au AFTER UPDATE ON files BEGIN
          INSERT INTO files_fts(files_fts, rowid, path) VALUES('delete', old.id, old.path);
          INSERT INTO files_fts(rowid, path) VALUES (new.id, new.path);
        END
      ''');
    } catch (e) {
      print("⚠️ FTS5 not supported: $e");
    }

    // Default Values
    await db.insert('kv_store', {'key': 'manifest_version', 'value': '0'});
    await db.insert('kv_store', {'key': 'last_push_lsn', 'value': '0'});
  }

  // --- Accessors ---

  Future<List<FileRecord>> listFiles({String category = 'my-drive'}) async {
    final db = await database;
    String whereClause = 'deleted_at IS NULL';
    String orderBy = 'last_update DESC';

    if (category == 'trash') {
      whereClause = 'deleted_at IS NOT NULL';
    } else if (category == 'starred') {
      whereClause = 'starred = 1 AND deleted_at IS NULL';
    } else if (category == 'recent') {
      // same where, just order by
      orderBy = 'last_update DESC';
    }

    final List<Map<String, dynamic>> maps = await db.query(
      'files',
      where: whereClause,
      orderBy: orderBy,
      limit: category == 'recent' ? 20 : null,
    );
    return List.generate(maps.length, (i) => FileRecord.fromMap(maps[i]));
  }

  Future<int> saveFile(FileRecord file) async {
    final db = await database;
    final res = await db.insert(
      'files',
      file.toMap(),
      conflictAlgorithm: ConflictAlgorithm.replace,
    );
    notifyChange();
    return res;
  }

  Future<void> softDelete(int id) async {
    final db = await database;
    await db.update(
      'files',
      {'deleted_at': DateTime.now().toIso8601String()},
      where: 'id = ?',
      whereArgs: [id],
    );
    notifyChange();
  }

  Future<void> setKV(String key, String value) async {
    final db = await database;
    await db.insert(
      'kv_store',
      {'key': key, 'value': value},
      conflictAlgorithm: ConflictAlgorithm.replace,
    );
  }

  Future<String?> getKV(String key) async {
    final db = await database;
    final List<Map<String, dynamic>> maps = await db.query(
      'kv_store',
      where: 'key = ?',
      whereArgs: [key],
    );
    if (maps.isNotEmpty) {
      return maps.first['value'];
    }
    return null;
  }

  Future<List<Map<String, dynamic>>> getActiveTasks() async {
    final db = await database;
    // Show all tasks, including completed ones, so the user has an activity history
    return await db.query('tasks', orderBy: 'created_at DESC');
  }

  Future<void> clearCompletedTasks() async {
    final db = await database;
    await db.delete('tasks', where: "status = 'completed' or status LIKE 'Failed%'");
  }

  Future<List<Map<String, dynamic>>> getPendingTasks() async {
    final db = await database;
    return await db.query('tasks', where: "status = 'pending' or status = 'failed'", orderBy: 'created_at ASC');
  }

  Future<void> insertTask(Map<String, dynamic> task) async {
    final db = await database;
    await db.insert('tasks', task, conflictAlgorithm: ConflictAlgorithm.replace);
    notifyChange();
  }

  Future<void> updateTaskProgress(String id, double progress, String status) async {
    final db = await database;
    await db.update(
      'tasks',
      {'progress': progress, 'status': status},
      where: 'id = ?',
      whereArgs: [id],
    );
    notifyChange();
  }

  Future<int> getTotalFileCount() async {
    final db = await database;
    final count = Sqflite.firstIntValue(await db.rawQuery('SELECT COUNT(*) FROM files WHERE deleted_at IS NULL'));
    return count ?? 0;
  }

  Future<void> deleteTask(String id) async {
    final db = await database;
    await db.delete('tasks', where: 'id = ?', whereArgs: [id]);
  }
}
