import 'dart:io';
import 'package:path_provider/path_provider.dart';
import 'database_service.dart';
import 'logger_service.dart';

class CleanupService {
  static Future<void> performStartupCleanup() async {
    try {
      final tmpDir = await getTemporaryDirectory();
      if (!await tmpDir.exists()) return;

      final db = DatabaseService();
      final activeTasks = await db.getPendingTasks();
      final activeIds = activeTasks.map((t) => t['id'].toString()).toSet();

      final entities = tmpDir.listSync();
      int deletedCount = 0;

      for (var entity in entities) {
        if (entity is Directory) {
          final name = entity.path.split(Platform.pathSeparator).last;
          
          // Pattern: nexus-ID ou nexus-dl-ID
          if (name.startsWith('nexus-')) {
            final id = name.replaceFirst('nexus-dl-', '').replaceFirst('nexus-', '');
            
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
    } catch (e) {
      AppLogger.error('Startup cleanup failed: $e');
    }
  }
}
