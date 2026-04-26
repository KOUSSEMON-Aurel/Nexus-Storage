import 'dart:io';
import 'package:path_provider/path_provider.dart';
import 'database_service.dart';
import 'logger_service.dart';
import 'youtube_service.dart';

class CleanupService {
  static Future<void> performStartupCleanup() async {
    try {
      final db = DatabaseService();
      
      // 1. Nettoyage des dossiers temporaires orphelins (existant)
      await _cleanupTempFiles(db);

      // 2. Nettoyage de la corbeille expirée (nouveau)
      await _cleanupExpiredTrash(db);

    } catch (e) {
      AppLogger.error('Startup cleanup failed: $e');
    }
  }

  static Future<void> _cleanupTempFiles(DatabaseService db) async {
    final tmpDir = await getTemporaryDirectory();
    if (!await tmpDir.exists()) return;

    final activeTasks = await db.getPendingTasks();
    final activeIds = activeTasks.map((t) => t['id'].toString()).toSet();

    final entities = tmpDir.listSync();
    int deletedCount = 0;

    for (var entity in entities) {
      if (entity is Directory) {
        final name = entity.path.split(Platform.pathSeparator).last;
        if (name.startsWith('nexus-')) {
          final id = name
              .replaceFirst('nexus-dl-', '')
              .replaceFirst('nexus-', '');

          if (!activeIds.contains(id)) {
            AppLogger.info('Cleaning up orphaned directory: $name');
            await entity.delete(recursive: true);
            deletedCount++;
          }
        }
      }
    }
    if (deletedCount > 0) {
      AppLogger.info('Cleanup finished: $deletedCount directories removed.');
    }
  }

  static Future<void> _cleanupExpiredTrash(DatabaseService db) async {
    final retentionStr = await db.getKV('trash_retention') ?? '30';
    final retentionDays = int.tryParse(retentionStr) ?? 30;

    final expiredFiles = await db.getExpiredTrashFiles(retentionDays);
    if (expiredFiles.isEmpty) return;

    AppLogger.info('Found ${expiredFiles.length} expired files in trash. Cleaning up...');
    
    final yt = YouTubeService();
    final sqlite = await db.database;

    for (var file in expiredFiles) {
      try {
        if (file.videoId.isNotEmpty) {
          await yt.deleteVideo(file.videoId);
        }
        await sqlite.delete('files', where: 'id = ?', whereArgs: [file.id]);
        AppLogger.info('Permanently deleted expired file: ${file.path}');
      } catch (e) {
        AppLogger.error('Failed to cleanup expired file ${file.path}: $e');
      }
    }
  }
}
