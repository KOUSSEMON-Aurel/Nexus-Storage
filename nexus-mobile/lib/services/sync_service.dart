import 'dart:convert';
import 'dart:io';
import 'package:http/http.dart' as http;
import 'package:path_provider/path_provider.dart';
import 'package:crypto/crypto.dart';
import 'database_service.dart';
import 'auth_service.dart';

class SyncService {
  static final SyncService _instance = SyncService._internal();
  factory SyncService() => _instance;
  SyncService._internal();

  final DatabaseService _db = DatabaseService();
  final AuthService _auth = AuthService();

  Future<void> pushDatabase() async {
    final token = await _auth.getAccessToken();
    if (token == null) return;

    // 1. Increment LSN
    final lsnStr = await _db.getKV('manifest_version') ?? '0';
    int lsn = int.parse(lsnStr) + 1;
    await _db.setKV('manifest_version', lsn.toString());

    // 2. Prepare Snapshot
    final dbFile = File(await _db.getDatabasePath());
    final bytes = await dbFile.readAsBytes();
    final hash = sha256.convert(bytes).toString();
    final countStr = await _db.getKV('total_file_count') ?? '0';

    // 3. Upload Manifest (nexus-sync.json)
    final manifest = {
      'lsn': lsn,
      'hash_sha256': hash,
      'pushed_at': DateTime.now().toIso8601String(),
      'record_count': int.parse(countStr),
    };

    // Upload to Drive (simplified for this version)
    // In a real implementation, we'd use the Drive API to find/replace nexus-sync.json
    // For now, let's assume we have a helper to upload a file to a specific Drive location.
    await _uploadFileToDrive('nexus-sync.json', utf8.encode(jsonEncode(manifest)), 'application/json', token);
    await _uploadFileToDrive('nexus.db', bytes, 'application/x-sqlite3', token);

    await _db.setKV('last_push_lsn', lsn.toString());
    await _db.setKV('last_push_hash', hash);
  }

  Future<void> pullDatabase() async {
    final token = await _auth.getAccessToken();
    if (token == null) return;

    // 1. Download Manifest
    final manifestBytes = await _downloadFileFromDrive('nexus-sync.json', token);
    if (manifestBytes == null) return;

    final manifest = jsonDecode(utf8.decode(manifestBytes));
    final remoteLSN = manifest['lsn'] as int;

    final localLSNStr = await _db.getKV('manifest_version') ?? '0';
    final localLSN = int.parse(localLSNStr);

    if (remoteLSN > localLSN) {
      // 2. Download DB
      final dbBytes = await _downloadFileFromDrive('nexus.db', token);
      if (dbBytes == null) return;

      // 3. Replace Local DB
      final dbPath = await _db.getDatabasePath();
      await _db.close();
      await File(dbPath).writeAsBytes(dbBytes);
      
      // Re-init
      await _db.database;
    }
  }

  // --- Drive API Helpers (Simplified) ---

  Future<void> _uploadFileToDrive(String name, List<int> content, String mimeType, String token) async {
    // First find if file exists
    final listResponse = await http.get(
      Uri.parse('https://www.googleapis.com/drive/v3/files?q=name="$name" and trashed=false'),
      headers: {'Authorization': 'Bearer $token'},
    );
    
    final files = jsonDecode(listResponse.body)['files'] as List;
    String? fileId;
    if (files.isNotEmpty) fileId = files.first['id'];

    if (fileId != null) {
      // Update
      await http.patch(
        Uri.parse('https://www.googleapis.com/upload/drive/v3/files/$fileId?uploadType=media'),
        headers: {
          'Authorization': 'Bearer $token',
          'Content-Type': mimeType,
        },
        body: content,
      );
    } else {
      // Create
      final metadata = {'name': name, 'mimeType': mimeType};
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

  Future<List<int>?> _downloadFileFromDrive(String name, String token) async {
    final listResponse = await http.get(
      Uri.parse('https://www.googleapis.com/drive/v3/files?q=name="$name" and trashed=false'),
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
