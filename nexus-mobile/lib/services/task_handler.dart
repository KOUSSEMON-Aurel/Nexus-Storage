import 'dart:ui';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import 'logger_service.dart';

@pragma('vm:entry-point')
void startCallback() {
  // L'import dart:ui est parfois requis pour le point d'entrée de l'isolate
  AppLogger.info('NEXUS_ISOLATE_ENTRY: startCallback invoked');
  FlutterForegroundTask.setTaskHandler(NexusTaskHandler());
}

class NexusTaskHandler extends TaskHandler {
  @override
  void onRepeatEvent(DateTime timestamp) {}

  @override
  Future<void> onDestroy(DateTime timestamp, bool isTimeout) async {
    AppLogger.info('NexusTaskHandler: Isolate destroyed');
  }

  @override
  Future<void> onStart(DateTime timestamp, TaskStarter starter) async {
    AppLogger.info('NexusTaskHandler: Isolate started, fetching data');
    
    final type = await FlutterForegroundTask.getData<String>(key: 'type');
    final taskId = await FlutterForegroundTask.getData<String>(key: 'id');
    final path = await FlutterForegroundTask.getData<String>(key: 'path');
    final pwd = await FlutterForegroundTask.getData<String>(key: 'pwd') ?? '';
    final videoId = await FlutterForegroundTask.getData<String>(key: 'video_id');
    final fileName = await FlutterForegroundTask.getData<String>(key: 'file_name');
    final token = await FlutterForegroundTask.getData<String>(key: 'token');

    AppLogger.info('NexusTaskHandler: Sending start_task to Main Isolate for $taskId');

    FlutterForegroundTask.sendDataToMain({
      'action': 'start_task',
      'type': type,
      'id': taskId,
      'path': path,
      'pwd': pwd,
      'video_id': videoId,
      'file_name': fileName,
      'token': token,
    });
  }

  @override
  void onReceiveData(Object data) {
    if (data is Map<String, dynamic>) {
      final action = data['action'];
      if (action == 'stop_service') {
        AppLogger.info('NexusTaskHandler: Stopping service by order from Main');
        FlutterForegroundTask.stopService();
      }
    }
  }
}
