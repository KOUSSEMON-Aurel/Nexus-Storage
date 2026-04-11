import 'dart:io';
import 'dart:isolate';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import 'nexus_service.dart';
import 'database_service.dart';
import 'auth_service.dart';

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

    // 1. Initialize background isolate state
    await AuthService().signInSilently();
    
    final token = await FlutterForegroundTask.getData<String>(key: 'upload_token');
    if (token != null) {
      AuthService().setBackgroundToken(token);
    }

    final taskId = await FlutterForegroundTask.getData<String>(key: 'upload_id');
    final filePath = await FlutterForegroundTask.getData<String>(key: 'upload_path');
    final password = await FlutterForegroundTask.getData<String>(key: 'upload_pwd') ?? '';

    print('BACKGROUND: onStart called. TaskID: $taskId, Path: $filePath');

    if (filePath != null && taskId != null) {
      try {
        final nexus = NexusService();
        print('BACKGROUND: Starting upload for $taskId');
        await nexus.encodeAndUpload(File(filePath), password, explicitTaskId: taskId);
        
        print('BACKGROUND: Upload completed for $taskId');
        FlutterForegroundTask.updateService(
          notificationTitle: 'Nexus: Upload Complete',
          notificationText: 'File uploaded successfully.',
        );
        // Wait briefly so user can see it completed, then stop so it becomes dismissible
        Future.delayed(const Duration(seconds: 3), () {
          FlutterForegroundTask.stopService();
        });
      } catch (e) {
        print('BACKGROUND ERROR for $taskId: $e');
        FlutterForegroundTask.updateService(
          notificationTitle: 'Nexus: Upload Failed',
          notificationText: 'Error: $e',
        );
        Future.delayed(const Duration(seconds: 3), () {
          FlutterForegroundTask.stopService();
        });
      }
    } else {
      print('BACKGROUND: Missing data. taskId: $taskId, filePath: $filePath');
      FlutterForegroundTask.stopService();
    }
  }
}
