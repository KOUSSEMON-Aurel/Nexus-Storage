import 'dart:io';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import 'nexus_service.dart';
import 'auth_service.dart';
import '../models/file_record.dart';
import 'logger_service.dart';

@pragma('vm:entry-point')
void startCallback() {
  FlutterForegroundTask.setTaskHandler(NexusTaskHandler());
}

class NexusTaskHandler extends TaskHandler {
  final FlutterLocalNotificationsPlugin _localNotifs = FlutterLocalNotificationsPlugin();

  Future<void> _initLocalNotifs() async {
    try {
      const AndroidInitializationSettings initializationSettingsAndroid =
          AndroidInitializationSettings('@mipmap/launcher_icon');
      const InitializationSettings initializationSettings =
          InitializationSettings(android: initializationSettingsAndroid);
      await _localNotifs.initialize(settings: initializationSettings);
    } catch (e) {
      AppLogger.warn('Failed to init local notifications in background: $e');
    }
  }

  Future<void> _showFinalNotification(String title, String body, bool success) async {
    try {
      await _initLocalNotifs();
      const AndroidNotificationDetails androidPlatformChannelSpecifics =
          AndroidNotificationDetails(
        'nexus_final_channel',
        'Nexus Task Finished',
        channelDescription: 'Notifications for completed Nexus tasks',
        importance: Importance.high,
        priority: Priority.high,
        showWhen: true,
        ongoing: false, 
        autoCancel: true,
      );
      const NotificationDetails platformChannelSpecifics =
          NotificationDetails(android: androidPlatformChannelSpecifics);
      
      await _localNotifs.show(
        id: DateTime.now().millisecond,
        title: title,
        body: body,
        notificationDetails: platformChannelSpecifics,
      );
    } catch (e) {
      AppLogger.error('Failed to show final notification: $e');
    }
  }

  @override
  void onRepeatEvent(DateTime timestamp) {
  }

  @override
  Future<void> onDestroy(DateTime timestamp, bool isTimeout) async {
  }

  @override
  Future<void> onStart(DateTime timestamp, TaskStarter starter) async {
    AppLogger.info('BACKGROUND TASK STARTING AT $timestamp');
    FlutterForegroundTask.sendDataToMain('refresh');

    await AuthService().signInSilently();
    
    final token = await FlutterForegroundTask.getData<String>(key: 'token');
    if (token != null) {
      AuthService().setBackgroundToken(token);
    }

    final type = await FlutterForegroundTask.getData<String>(key: 'type');
    final taskId = await FlutterForegroundTask.getData<String>(key: 'id');
    final password = await FlutterForegroundTask.getData<String>(key: 'pwd') ?? '';

    if (taskId == null) {
      FlutterForegroundTask.stopService();
      return;
    }

    try {
      final nexus = NexusService();
      String finalTitle = 'Task Complete';
      String finalBody = 'Your file is ready.';
      
      if (type == 'upload') {
        final filePath = await FlutterForegroundTask.getData<String>(key: 'path');
        if (filePath == null) throw Exception('Missing upload path');
        
        await nexus.encodeAndUpload(File(filePath), password, explicitTaskId: taskId);
        finalTitle = '✅ Upload Complete';
        finalBody = 'File secured on YouTube.';
      } else if (type == 'download') {
        final videoId = await FlutterForegroundTask.getData<String>(key: 'video_id');
        final fileName = await FlutterForegroundTask.getData<String>(key: 'file_name');
        
        if (videoId == null || fileName == null) throw Exception('Missing download data');

        final record = FileRecord(
          id: 0,
          path: fileName,
          videoId: videoId,
          size: 0,
          hash: '',
          key: password,
          lastUpdate: '',
          starred: false,
          sha256: '',
          fileKey: '',
          isArchive: false,
          hasCustomPassword: password.isNotEmpty,
          customPasswordHint: '',
          mode: 'base',
        );

        await nexus.downloadAndDecrypt(record, password, explicitTaskId: taskId);
        finalTitle = '✅ Download Complete';
        finalBody = 'Saved: /Download/NexusStorage/$fileName';
      }

      FlutterForegroundTask.sendDataToMain('refresh');
      
      // 1. Stop Foreground Service (removes non-swipable notif)
      await FlutterForegroundTask.stopService();

      // 2. Show Standard swipable notification
      await _showFinalNotification(finalTitle, finalBody, true);

    } catch (e) {
      AppLogger.error('BACKGROUND ERROR: $e');
      FlutterForegroundTask.sendDataToMain('refresh');
      
      await FlutterForegroundTask.stopService();
      await _showFinalNotification('❌ Task Failed', e.toString().split('\n').first, false);
    }
  }
}
