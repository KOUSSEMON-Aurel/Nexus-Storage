import 'dart:convert';
import 'dart:io';
import 'package:http/http.dart' as http;
import 'package:crypto/crypto.dart';
import 'package:connectivity_plus/connectivity_plus.dart';
import 'package:sqflite/sqflite.dart';
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
      if (token == null) {
        throw AuthException('Google connection required for sync.');
      }

      AppLogger.info('Starting robust sync...');

      final folderId = await _getFolderId('Nexus-Recovery', token);
      final remoteManifestBytes = await _downloadFileFromDrive(
        'nexus-sync.json',
        token,
        folderId: folderId,
      );

      // 1. Get Local State
      await _db.checkpointWAL();
      final localLSNStr = await _db.getKV('manifest_version') ?? '0';
      int localLSN = int.parse(localLSNStr);
      final localStats = await _db.getSyncStats();
      final localModified =
          await _db.getLastModified() ?? DateTime.now().toIso8601String();

      // 2. Decision Logic
      if (remoteManifestBytes == null) {
        // No remote backup at all. Only push if we have actual data.
        if (localLSN == 0 || localStats['files'] == 0) {
          AppLogger.info(
            'No remote manifest and local DB is empty. Nothing to sync.',
          );
          return;
        }
        AppLogger.info('No remote manifest. Initial push required.');
        final localHash = await _db.calculateLogicalHash();
        await _performPush(
          token,
          folderId,
          localLSN,
          localHash,
          localStats,
          localModified,
        );
        return;
      }

      final remoteManifest = jsonDecode(utf8.decode(remoteManifestBytes));
      final remoteLSN = remoteManifest['lsn'] as int;
      final remoteHash = remoteManifest['logical_hash'] as String;

      // PRIORITY 0: If local is empty/reset, always pull from remote.
      if (localLSN == 0 || localStats['files'] == 0) {
        AppLogger.info(
          'Local DB is empty (LSN=$localLSN). Forcing pull from remote (LSN=$remoteLSN)...',
        );
        await _performPull(token, folderId, remoteLSN, remoteHash);
        return;
      }

      final localHash = await _db.calculateLogicalHash();

      // PRIORITY 1: LSN based comparison
      if (remoteLSN > localLSN) {
        AppLogger.info(
          'Remote is ahead (Remote LSN: $remoteLSN, Local LSN: $localLSN). Pulling...',
        );
        await _performPull(token, folderId, remoteLSN, remoteHash);
        return;
      } else if (localLSN > remoteLSN) {
        AppLogger.info(
          'Local is ahead (Local LSN: $localLSN, Remote LSN: $remoteLSN). Pushing...',
        );
        await _performPush(
          token,
          folderId,
          localLSN,
          localHash,
          localStats,
          localModified,
        );
        return;
      }

      // PRIORITY 2: If LSN equal, check logical hash for divergence
      if (localHash != remoteHash) {
        AppLogger.warn('LSNs match but hashes differ! Divergence detected.');
        // Tie-breaker: richer database wins
        final remoteStats = Map<String, int>.from(
          remoteManifest['stats'] as Map,
        );
        int localTotal = localStats.values.reduce((a, b) => a + b);
        int remoteTotal = remoteStats.values.reduce((a, b) => a + b);

        if (localTotal > remoteTotal) {
          AppLogger.info('Local is richer. Pushing...');
          await _performPush(
            token,
            folderId,
            localLSN,
            localHash,
            localStats,
            localModified,
          );
        } else if (remoteTotal > localTotal) {
          AppLogger.info('Remote is richer. Pulling...');
          await _performPull(token, folderId, remoteLSN, remoteHash);
        } else {
          AppLogger.warn(
            'Conflict detected: same size, same LSN, different hash. Manual intervention might be needed.',
          );
        }
      } else {
        AppLogger.info('DB already in sync (LSN $localLSN)');
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

  Future<void> _performPush(
    String token,
    String? folderId,
    int lsn,
    String hash,
    Map<String, int> stats,
    String lastModified,
  ) async {
    // 1. Pre-push Integrity Check
    if (!await _db.checkIntegrity()) {
      throw SyncException(
        'Local database integrity check failed. Push aborted for safety.',
      );
    }

    // 2. Create Atomic Snapshot
    final dbPath = await _db.getDatabasePath();
    final pushTmpPath = '$dbPath.push_tmp';
    await _db.makeAtomicSnapshot(pushTmpPath);
    final pushFile = File(pushTmpPath);
    final bytes = await pushFile.readAsBytes();

    try {
      // 3. Drive History & Update
      await _db.checkpointWAL();
      final newLSN = lsn + 1;
      final manifest = {
        'lsn': newLSN,
        'logical_hash': hash,
        'last_modified': lastModified,
        'stats': stats,
        'pushed_at': DateTime.now().toIso8601String(),
      };

      // Upload manifest first
      await _uploadFileToDrive(
        'nexus-sync.json',
        utf8.encode(jsonEncode(manifest)),
        'application/json',
        token,
        folderId: folderId,
      );

      // Upload DB with Drive backup history
      await _uploadFileToDrive(
        'nexus.db',
        bytes,
        'application/x-sqlite3',
        token,
        folderId: folderId,
        createBackupOnDrive: true, // NEW: creates .bak on Drive
      );

      // 4. Post-push verification
      final verifyBytes = await _downloadFileFromDrive(
        'nexus.db',
        token,
        folderId: folderId,
      );
      if (verifyBytes == null) {
        throw SyncException(
          'Verification failed: Could not re-download DB after push.',
        );
      }

      final downloadedHash = sha256.convert(verifyBytes).toString();
      final localBinaryHash = sha256.convert(bytes).toString();

      if (downloadedHash != localBinaryHash) {
        throw SyncException(
          'Verification failed: Uploaded binary hash mismatch.',
        );
      }

      await _db.setKV('manifest_version', newLSN.toString());
      await _db.setKV('last_push_lsn', newLSN.toString());
      AppLogger.info('✅ DB pushed and verified (LSN $newLSN)');
    } finally {
      if (await pushFile.exists()) await pushFile.delete();
    }
  }

  Future<void> _performPull(
    String token,
    String? folderId,
    int remoteLSN,
    String remoteHash,
  ) async {
    final dbPath = await _db.getDatabasePath();
    final backupPath = '$dbPath.backup_pre_pull';
    final downloadPath = '$dbPath.downloading';

    // 1. Create Pre-pull Local Backup
    await _db.makeAtomicSnapshot(backupPath);

    try {
      // 2. Download from Drive
      final dbBytes = await _downloadFileFromDrive(
        'nexus.db',
        token,
        folderId: folderId,
      );
      if (dbBytes == null) {
        throw SyncException('Remote manifest exists but DB file not found.');
      }

      final downloadFile = File(downloadPath);
      await downloadFile.writeAsBytes(dbBytes);

      // 3. Verify Downloaded File Integrity
      // Temporarily open the downloaded file to check its integrity and logical hash
      final tempDB = await openDatabase(downloadPath, readOnly: true);
      try {
        final res = await tempDB.rawQuery('PRAGMA integrity_check');
        if (res.first.values.first.toString() != 'ok') {
          throw SyncException('Downloaded database is corrupted.');
        }
      } finally {
        await tempDB.close();
      }

      // 4. Atomic Replace
      await _db.close();
      if (await File(dbPath).exists()) {
        await File(dbPath).delete();
      }
      await downloadFile.rename(dbPath);

      // 5. Re-open and verify
      try {
        await _db.database;
        final newLocalHash = await _db.calculateLogicalHash();
        if (newLocalHash != remoteHash) {
          throw SyncException('Logical hash mismatch after pull.');
        }
        AppLogger.info(
          '✅ DB successfully pulled and verified (LSN $remoteLSN)',
        );
        _db.notifyChange();
      } catch (e) {
        AppLogger.error(
          '❌ Failed to re-open pulled DB: $e. Attempting rollback...',
        );
        // Rollback
        await _db.close();
        if (await File(dbPath).exists()) await File(dbPath).delete();
        await File(backupPath).rename(dbPath);
        await _db.database;
        throw SyncException(
          'Pull failed, but successfully rolled back to local backup.',
        );
      }

      await _db.setKV('manifest_version', remoteLSN.toString());
      await _db.setKV('last_push_lsn', remoteLSN.toString());
    } finally {
      if (await File(downloadPath).exists()) await File(downloadPath).delete();
      if (await File(backupPath).exists()) await File(backupPath).delete();
    }
  }

  // Old methods kept for compatibility if called, but redirecting to sync()
  Future<void> pushDatabase() => sync();
  Future<void> pullDatabase() => sync();

  Future<String?> _getFolderId(String name, String token) async {
    final listResponse = await http.get(
      Uri.parse(
        'https://www.googleapis.com/drive/v3/files?q=${Uri.encodeComponent('name="$name" and mimeType="application/vnd.google-apps.folder" and trashed=false')}',
      ),
      headers: {'Authorization': 'Bearer $token'},
    );

    if (listResponse.statusCode == 200) {
      final files = jsonDecode(listResponse.body)['files'] as List;
      if (files.isNotEmpty) return files.first['id'];
    }

    final metadata = {
      'name': name,
      'mimeType': 'application/vnd.google-apps.folder',
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

  Future<void> _uploadFileToDrive(
    String name,
    List<int> content,
    String mimeType,
    String token, {
    String? folderId,
    bool createBackupOnDrive = false,
  }) async {
    String query = 'name="$name" and trashed=false';
    if (folderId != null) query += ' and "$folderId" in parents';

    final listResponse = await http.get(
      Uri.parse(
        'https://www.googleapis.com/drive/v3/files?q=${Uri.encodeComponent(query)}',
      ),
      headers: {'Authorization': 'Bearer $token'},
    );

    final files = jsonDecode(listResponse.body)['files'] as List;
    String? fileId;
    if (files.isNotEmpty) {
      fileId = files.first['id'];
      // Cleanup duplicates if any
      if (files.length > 1) {
        AppLogger.warn(
          'Multiple "$name" files found on Drive. Cleaning up duplicates...',
        );
        for (int i = 1; i < files.length; i++) {
          await http.delete(
            Uri.parse(
              'https://www.googleapis.com/drive/v3/files/${files[i]['id']}',
            ),
            headers: {'Authorization': 'Bearer $token'},
          );
        }
      }
    }

    if (fileId != null) {
      // Drive-side history backup logic
      if (createBackupOnDrive) {
        final timestamp = DateTime.now()
            .toIso8601String()
            .replaceAll(':', '')
            .replaceAll('-', '')
            .split('.')
            .first;
        final backupName = '$name.bak.$timestamp';
        AppLogger.info('Creating Drive-side backup: $backupName');
        await http.post(
          Uri.parse('https://www.googleapis.com/drive/v3/files/$fileId/copy'),
          headers: {
            'Authorization': 'Bearer $token',
            'Content-Type': 'application/json',
          },
          body: jsonEncode({'name': backupName}),
        );
      }

      await http.patch(
        Uri.parse(
          'https://www.googleapis.com/upload/drive/v3/files/$fileId?uploadType=media',
        ),
        headers: {'Authorization': 'Bearer $token', 'Content-Type': mimeType},
        body: content,
      );
    } else {
      final metadata = {
        'name': name,
        'mimeType': mimeType,
        if (folderId != null) 'parents': [folderId],
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
        Uri.parse(
          'https://www.googleapis.com/upload/drive/v3/files/$newId?uploadType=media',
        ),
        headers: {'Authorization': 'Bearer $token', 'Content-Type': mimeType},
        body: content,
      );
    }
  }

  Future<List<int>?> _downloadFileFromDrive(
    String name,
    String token, {
    String? folderId,
  }) async {
    String query = 'name="$name" and trashed=false';
    if (folderId != null) query += ' and "$folderId" in parents';

    final listResponse = await http.get(
      Uri.parse(
        'https://www.googleapis.com/drive/v3/files?q=${Uri.encodeComponent(query)}',
      ),
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
