import 'dart:convert';
import 'dart:io';
import 'package:http/http.dart' as http;
import 'package:crypto/crypto.dart';
import 'package:connectivity_plus/connectivity_plus.dart';
import 'database_service.dart';
import 'auth_service.dart';
import 'logger_service.dart';
import '../utils/exceptions.dart';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';

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

  Future<void> sync() async {
    try {
      await _checkConnectivity();
      final token = await _auth.getAccessToken();
      if (token == null) throw AuthException('Google connection required for sync.');

      AppLogger.info('Starting smart sync...');

      final folderId = await _getFolderId('Nexus-Recovery', token);
      final remoteManifestBytes = await _downloadFileFromDrive('nexus-sync.json', token, folderId: folderId);
      
      // 1. Get Local State
      await _db.checkpointWAL();
      final localStats = await _db.getSyncStats();
      final localHash = await _db.calculateLogicalHash();
      final localModified = await _db.getLastModified() ?? '1970-01-01T00:00:00Z';
      final localLSNStr = await _db.getKV('manifest_version') ?? '0';
      int localLSN = int.parse(localLSNStr);

      // 2. Decision Logic
      if (remoteManifestBytes == null) {
        AppLogger.info('No remote manifest. Initial push required.');
        await _performPush(token, folderId, localLSN, localHash, localStats, localModified);
        return;
      }

      final remoteManifest = jsonDecode(utf8.decode(remoteManifestBytes));
      final remoteLSN = remoteManifest['lsn'] as int;
      final remoteHash = remoteManifest['logical_hash'] as String;
      final remoteModified = remoteManifest['last_modified'] as String;
      final remoteStats = Map<String, int>.from(remoteManifest['stats'] as Map);

      // Rule 2: In Sync check
      bool statsEqual = localStats['files'] == remoteStats['files'] &&
                        localStats['folders'] == remoteStats['folders'] &&
                        localStats['tasks'] == remoteStats['tasks'];
      
      if (statsEqual && localHash == remoteHash) {
        AppLogger.info('DB already in sync (LSN $localLSN)');
        return;
      }

      // Rule 3 & 4: Row Counts
      int localTotal = localStats.values.reduce((a, b) => a + b);
      int remoteTotal = remoteStats.values.reduce((a, b) => a + b);

      if (localTotal > remoteTotal) {
        AppLogger.info('Local is richer ($localTotal > $remoteTotal). Pushing...');
        await _performPush(token, folderId, localLSN, localHash, localStats, localModified);
      } else if (remoteTotal > localTotal) {
        AppLogger.info('Remote is richer ($remoteTotal > $localTotal). Pulling...');
        await _performPull(token, folderId, remoteLSN, remoteHash);
      } else {
        // Rule 5: Totals equal but hash diff (Divergence)
        AppLogger.info('Row counts equal but hashes differ. Using date tiebreaker.');
        
        DateTime localDate = DateTime.parse(localModified);
        DateTime remoteDate = DateTime.parse(remoteModified);

        if (localDate.isAfter(remoteDate)) {
          AppLogger.info('Local is newer. Pushing...');
          await _performPush(token, folderId, localLSN, localHash, localStats, localModified);
        } else if (remoteDate.isAfter(localDate)) {
          AppLogger.info('Remote is newer. Pulling...');
          await _performPull(token, folderId, remoteLSN, remoteHash);
        } else {
          throw SyncException('CONFLICT: Same size, same date, different data. Manual resolution required.');
        }
      }
    } catch (e, s) {
      AppLogger.error('Sync failed: $e', e, s);
      rethrow;
    } finally {
      if (await FlutterForegroundTask.isRunningService) {
        await _db.checkpointWAL();
        FlutterForegroundTask.sendDataToMain('refresh');
      }
    }
  }

  Future<void> _performPush(String token, String? folderId, int lsn, String hash, Map<String, int> stats, String lastModified) async {
    final dbPath = await _db.getDatabasePath();
    final dbFile = File(dbPath);
    final bytes = await dbFile.readAsBytes();

    final newLSN = lsn + 1;
    final manifest = {
      'lsn': newLSN,
      'logical_hash': hash,
      'last_modified': lastModified,
      'stats': stats,
      'pushed_at': DateTime.now().toIso8601String(),
    };

    await _uploadFileToDrive('nexus-sync.json', utf8.encode(jsonEncode(manifest)), 'application/json', token, folderId: folderId);
    await _uploadFileToDrive('nexus.db', bytes, 'application/x-sqlite3', token, folderId: folderId);

    // Rule 6: Post-push verification
    final verifyBytes = await _downloadFileFromDrive('nexus.db', token, folderId: folderId);
    if (verifyBytes == null) throw SyncException('Verification failed: Could not re-download DB after push.');
    
    // For simplicity, we compare binary bytes for upload integrity here.
    final downloadedHash = sha256.convert(verifyBytes).toString();
    final localBinaryHash = sha256.convert(bytes).toString();
    
    if (downloadedHash != localBinaryHash) {
      throw SyncException('Verification failed: Uploaded binary hash mismatch.');
    }

    await _db.setKV('manifest_version', newLSN.toString());
    AppLogger.info('✅ DB pushed and verified (LSN $newLSN)');
  }

  Future<void> _performPull(String token, String? folderId, int remoteLSN, String remoteHash) async {
    final dbBytes = await _downloadFileFromDrive('nexus.db', token, folderId: folderId);
    if (dbBytes == null) throw SyncException('Sync manifest exists but DB file not found on Drive.');

    final dbPath = await _db.getDatabasePath();
    
    // Verify logical hash after pull?
    // Write to temp, open and calculate logical hash
    final tempPath = '$dbPath.new';
    await File(tempPath).writeAsBytes(dbBytes);
    
    // To calculate logical hash, we'd need to open the new DB.
    // For now, satisfy the user's request for verification.
    
    await _db.close();
    if (await File(dbPath).exists()) {
      await File(dbPath).rename('$dbPath.bak');
    }
    await File(tempPath).rename(dbPath);
    await _db.database;
    
    final newLocalHash = await _db.calculateLogicalHash();
    if (newLocalHash != remoteHash) {
      // Revert if possible
      await _db.close();
      await File(dbPath).delete();
      await File('$dbPath.bak').rename(dbPath);
      await _db.database;
      throw SyncException('Pull verification failed: Logical hash mismatch.');
    }

    if (await File('$dbPath.bak').exists()) {
      await File('$dbPath.bak').delete();
    }

    await _db.setKV('manifest_version', remoteLSN.toString());
    AppLogger.info('✅ DB successfully pulled and verified (LSN $remoteLSN)');
  }

  // Old methods kept for compatibility if called, but redirecting to sync()
  Future<void> pushDatabase() => sync();
  Future<void> pullDatabase() => sync();

  Future<String?> _getFolderId(String name, String token) async {
    final listResponse = await http.get(
      Uri.parse('https://www.googleapis.com/drive/v3/files?q=${Uri.encodeComponent('name="$name" and mimeType="application/vnd.google-apps.folder" and trashed=false')}'),
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
      Uri.parse('https://www.googleapis.com/drive/v3/files?q=${Uri.encodeComponent(query)}'),
      headers: {'Authorization': 'Bearer $token'},
    );
    
    final files = jsonDecode(listResponse.body)['files'] as List;
    String? fileId;
    if (files.isNotEmpty) fileId = files.first['id'];

    if (fileId != null) {
      await http.patch(
        Uri.parse('https://www.googleapis.com/upload/drive/v3/files/$fileId?uploadType=media'),
        headers: {
          'Authorization': 'Bearer $token',
          'Content-Type': mimeType,
        },
        body: content,
      );
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
      
      final newId = jsonDecode(createResponse.body)['id'];
      await http.patch(
        Uri.parse('https://www.googleapis.com/upload/drive/v3/files/$newId?uploadType=media'),
        headers: {
          'Authorization': 'Bearer $token',
          'Content-Type': mimeType,
        },
        body: content,
      );
    }
  }

  Future<List<int>?> _downloadFileFromDrive(String name, String token, {String? folderId}) async {
    String query = 'name="$name" and trashed=false';
    if (folderId != null) query += ' and "$folderId" in parents';

    final listResponse = await http.get(
      Uri.parse('https://www.googleapis.com/drive/v3/files?q=${Uri.encodeComponent(query)}'),
      headers: {'Authorization': 'Bearer $token'},
    );
    
    final files = jsonDecode(listResponse.body)['files'] as List;
    if (files.isEmpty) return null;

    final fileId = files.first['id'];
    final downloadResponse = await http.get(
      Uri.parse('https://www.googleapis.com/drive/v3/files/$fileId?alt=media'),
      headers: {'Authorization': 'Bearer $token'},
    );
    
    return downloadResponse.bodyBytes;
  }
}
