import 'dart:convert';
import 'dart:io';
import 'package:http/http.dart' as http;
import 'package:path_provider/path_provider.dart';
import 'package:crypto/crypto.dart';
import 'package:connectivity_plus/connectivity_plus.dart';
import 'database_service.dart';
import 'auth_service.dart';
import 'logger_service.dart';
import '../utils/exceptions.dart';

class SyncService {
  static final SyncService _instance = SyncService._internal();
  factory SyncService() => _instance;
  SyncService._internal();

  final DatabaseService _db = DatabaseService();
  final AuthService _auth = AuthService();

  Future<void> _checkConnectivity() async {
    final status = await Connectivity().checkConnectivity();
    if (status.contains(ConnectivityResult.none)) {
      throw NetworkException('No internet connection available.');
    }
  }

  Future<void> pushDatabase() async {
    try {
      await _checkConnectivity();
      final token = await _auth.getAccessToken();
      if (token == null) {
        AppLogger.warn('Sync push aborted: Token is null');
        throw AuthException('Google connection required for sync.');
      }

      AppLogger.info('Starting sync push with token starting with ${token.substring(0, 10)}...');

      final folderId = await _getFolderId('Nexus-Recovery', token);
      final manifestBytes = await _downloadFileFromDrive('nexus-sync.json', token, folderId: folderId);
      int remoteLSN = 0;
      String remoteHash = '';
      
      if (manifestBytes != null) {
        AppLogger.info('Found existing remote manifest');
        final remoteManifest = jsonDecode(utf8.decode(manifestBytes));
        remoteLSN = remoteManifest['lsn'] as int;
        remoteHash = remoteManifest['hash_sha256'] as String;
      } else {
        AppLogger.info('No remote manifest found, will create new');
      }

      final localLSNStr = await _db.getKV('manifest_version') ?? '0';
      int localLSN = int.parse(localLSNStr);

      final dbFile = File(await _db.getDatabasePath());
      if (!await dbFile.exists()) throw SyncException('Local database file not found');
      
      final bytes = await dbFile.readAsBytes();
      final localHash = sha256.convert(bytes).toString();

      AppLogger.info('Local DB: LSN $localLSN, Hash ${localHash.substring(0, 8)}');

      if (manifestBytes != null) {
        if (remoteLSN > localLSN) {
          AppLogger.warn('Sync Conflict: Remote LSN ($remoteLSN) > Local LSN ($localLSN)');
          throw SyncException('Remote version ($remoteLSN) is newer. Pull required.', code: 'PULL_REQUIRED');
        }
        if (remoteLSN == localLSN && remoteHash == localHash) {
          AppLogger.info('DB already in sync (LSN $localLSN)');
          return;
        }
      }

      localLSN++;
      await _db.setKV('manifest_version', localLSN.toString());
      final count = await _db.getTotalFileCount();

      final manifest = {
        'lsn': localLSN,
        'hash_sha256': localHash,
        'pushed_at': DateTime.now().toIso8601String(),
        'record_count': count,
      };

      await _uploadFileToDrive('nexus-sync.json', utf8.encode(jsonEncode(manifest)), 'application/json', token, folderId: folderId);
      await _uploadFileToDrive('nexus.db', bytes, 'application/x-sqlite3', token, folderId: folderId);

      await _db.setKV('last_push_lsn', localLSN.toString());
      await _db.setKV('last_push_hash', localHash);
      
      AppLogger.info('✅ DB pushed to Drive (LSN $localLSN)');
      await _db.insertTask({
        'id': DateTime.now().millisecondsSinceEpoch.toString(),
        'file_path': 'Database Push',
        'status': 'completed',
        'progress': 1.0,
        'created_at': DateTime.now().toIso8601String()
      });
    } catch (e, s) {
      AppLogger.error('Sync push failed: $e', e, s);
      final errMsg = e.toString().contains('\\n') ? e.toString().split('\\n').first : e.toString();
      await _db.insertTask({
        'id': DateTime.now().millisecondsSinceEpoch.toString(),
        'file_path': 'Database Push Error',
        'status': 'Failed: $errMsg',
        'progress': 1.0,
        'created_at': DateTime.now().toIso8601String()
      });
      rethrow;
    }
  }

  Future<void> pullDatabase() async {
    try {
      await _checkConnectivity();
      final token = await _auth.getAccessToken();
      if (token == null) throw AuthException('Google connection required for sync.');

      AppLogger.info('Starting sync pull...');
      final folderId = await _getFolderId('Nexus-Recovery', token);
      final manifestBytes = await _downloadFileFromDrive('nexus-sync.json', token, folderId: folderId);
      if (manifestBytes == null) return;

      final manifest = jsonDecode(utf8.decode(manifestBytes));
      final remoteLSN = manifest['lsn'] as int;
      final remoteHash = manifest['hash_sha256'] as String;

      final localLSNStr = await _db.getKV('manifest_version') ?? '0';
      final localLSN = int.parse(localLSNStr);

      if (remoteLSN > localLSN) {
        final dbBytes = await _downloadFileFromDrive('nexus.db', token, folderId: folderId);
        if (dbBytes == null) throw SyncException('Sync manifest exists but DB file not found on Drive.');

        final downloadedHash = sha256.convert(dbBytes).toString();
        if (downloadedHash != remoteHash) {
          throw SyncException('Downloaded DB hash mismatch. Corruption suspected.');
        }

        final dbPath = await _db.getDatabasePath();
        await _db.close();
        await File(dbPath).writeAsBytes(dbBytes);
        await _db.database;
        
        AppLogger.info('✅ DB successfully pulled (LSN $remoteLSN)');
      } else {
        AppLogger.info('Local DB is newer or equal. Pull skipped.');
      }
      
      await _db.insertTask({
        'id': DateTime.now().millisecondsSinceEpoch.toString(),
        'file_path': 'Database Pull',
        'status': 'completed',
        'progress': 1.0,
        'created_at': DateTime.now().toIso8601String()
      });
    } catch (e, s) {
      AppLogger.error('Sync pull failed: $e', e, s);
      final errMsg = e.toString().contains('\\n') ? e.toString().split('\\n').first : e.toString();
      await _db.insertTask({
        'id': DateTime.now().millisecondsSinceEpoch.toString(),
        'file_path': 'Database Pull Error',
        'status': 'Failed: $errMsg',
        'progress': 1.0,
        'created_at': DateTime.now().toIso8601String()
      });
      rethrow;
    }
  }

  Future<String?> _getFolderId(String name, String token) async {
    final listResponse = await http.get(
      Uri.parse('https://www.googleapis.com/drive/v3/files?q=name="$name" and mimeType="application/vnd.google-apps.folder" and trashed=false'),
      headers: {'Authorization': 'Bearer $token'},
    );
    
    if (listResponse.statusCode == 200) {
      final files = jsonDecode(listResponse.body)['files'] as List;
      if (files.isNotEmpty) return files.first['id'];
    }

    final metadata = {
      'name': name,
      'mimeType': 'application/vnd.google-apps.folder'
    };
    final createResponse = await http.post(
      Uri.parse('https://www.googleapis.com/drive/v3/files'),
      headers: {
        'Authorization': 'Bearer $token',
        'Content-Type': 'application/json',
      },
      body: jsonEncode(metadata),
    );
    
    if (createResponse.statusCode == 200) {
      return jsonDecode(createResponse.body)['id'];
    }
    return null;
  }

  Future<void> _uploadFileToDrive(String name, List<int> content, String mimeType, String token, {String? folderId}) async {
    String query = 'name="$name" and trashed=false';
    if (folderId != null) query += ' and "$folderId" in parents';

    final listResponse = await http.get(
      Uri.parse('https://www.googleapis.com/drive/v3/files?q=\${Uri.encodeComponent(query)}'),
      headers: {'Authorization': 'Bearer $token'},
    );
    
    if (listResponse.statusCode != 200) {
      throw SyncException('Drive API Error (\${listResponse.statusCode}): \${listResponse.body}');
    }
    
    final files = jsonDecode(listResponse.body)['files'] as List;
    String? fileId;
    if (files.isNotEmpty) fileId = files.first['id'];

    if (fileId != null) {
      final updateResponse = await http.patch(
        Uri.parse('https://www.googleapis.com/upload/drive/v3/files/\$fileId?uploadType=media'),
        headers: {
          'Authorization': 'Bearer $token',
          'Content-Type': mimeType,
        },
        body: content,
      );
      if (updateResponse.statusCode != 200) {
        throw SyncException('Drive Update Error (\${updateResponse.statusCode})');
      }
    } else {
      final metadata = {
        'name': name,
        'mimeType': mimeType,
        if (folderId != null) 'parents': [folderId]
      };
      final createResponse = await http.post(
        Uri.parse('https://www.googleapis.com/drive/v3/files'),
        headers: {
          'Authorization': 'Bearer $token',
          'Content-Type': 'application/json',
        },
        body: jsonEncode(metadata),
      );
      
      if (createResponse.statusCode != 200) {
        throw SyncException('Drive Create Error (\${createResponse.statusCode})');
      }
      
      final newId = jsonDecode(createResponse.body)['id'];
      final uploadResponse = await http.patch(
        Uri.parse('https://www.googleapis.com/upload/drive/v3/files/\$newId?uploadType=media'),
        headers: {
          'Authorization': 'Bearer $token',
          'Content-Type': mimeType,
        },
        body: content,
      );
      
      if (uploadResponse.statusCode != 200) {
        throw SyncException('Drive Media Upload Error (\${uploadResponse.statusCode})');
      }
    }
  }

  Future<List<int>?> _downloadFileFromDrive(String name, String token, {String? folderId}) async {
    String query = 'name="$name" and trashed=false';
    if (folderId != null) query += ' and "$folderId" in parents';

    final listResponse = await http.get(
      Uri.parse('https://www.googleapis.com/drive/v3/files?q=\${Uri.encodeComponent(query)}'),
      headers: {'Authorization': 'Bearer $token'},
    );
    
    if (listResponse.statusCode != 200) {
      throw SyncException('Drive List Error (\${listResponse.statusCode})');
    }
    
    final files = jsonDecode(listResponse.body)['files'] as List;
    if (files.isEmpty) return null;

    final fileId = files.first['id'];
    final downloadResponse = await http.get(
      Uri.parse('https://www.googleapis.com/drive/v3/files/\$fileId?alt=media'),
      headers: {'Authorization': 'Bearer $token'},
    );
    
    if (downloadResponse.statusCode != 200) {
      throw SyncException('Drive Download Error (\${downloadResponse.statusCode})');
    }

    return downloadResponse.bodyBytes;
  }
}
