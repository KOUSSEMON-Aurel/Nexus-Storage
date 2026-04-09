import 'dart:io';
import 'dart:isolate';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import 'nexus_service.dart';
import 'database_service.dart';

@pragma('vm:entry-point')
void startCallback() {
  FlutterForegroundTask.setTaskHandler(UploadTaskHandler());
}

class UploadTaskHandler extends TaskHandler {
  SendPort? _sendPort;

  @override
  void onRepeatEvent(DateTime timestamp, SendPort? sendPort) {
    // Repeated events if interval is set
  }

  @override
  void onDestroy(DateTime timestamp, SendPort? sendPort) {
    // Cleanup
  }

  @override
  void onStart(DateTime timestamp, SendPort? sendPort) async {
    _sendPort = sendPort;

    final taskId = await FlutterForegroundTask.getData<String>(key: 'upload_id');
    final filePath = await FlutterForegroundTask.getData<String>(key: 'upload_path');
    final password = await FlutterForegroundTask.getData<String>(key: 'upload_pwd') ?? '';

    if (filePath != null && taskId != null) {
      try {
        final nexus = NexusService();
        await nexus.encodeAndUpload(File(filePath), password, explicitTaskId: taskId);
        
        FlutterForegroundTask.updateService(
          notificationTitle: 'Nexus: Upload Complete',
          notificationText: 'File uploaded successfully.',
        );
      } catch (e) {
        FlutterForegroundTask.updateService(
          notificationTitle: 'Nexus: Upload Failed',
          notificationText: 'Error: $e',
        );
      }
    }
  }
}
