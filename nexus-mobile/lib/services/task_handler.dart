import 'dart:io';
import 'dart:ui';
import 'package:flutter/material.dart';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import 'nexus_service.dart';
import 'auth_service.dart';
import '../models/file_record.dart';
import 'logger_service.dart';

import 'package:ffmpeg_kit_flutter_new/ffmpeg_kit_config.dart';

@pragma('vm:entry-point')
void startCallback() {
  // Initialize Flutter binding for background isolate
  DartPluginRegistrant.ensureInitialized();
  WidgetsFlutterBinding.ensureInitialized();

  // Disable session history but allow minimal logging for diagnostics
  FFmpegKitConfig.setSessionHistorySize(0);
  FFmpegKitConfig.enableLogCallback((log) {
    if (log.getLevel() <= 16) { // Level 16 is Level.ERROR
       AppLogger.error('FFmpeg Background: ${log.getMessage()}');
    }
  });
  FFmpegKitConfig.enableStatisticsCallback(null);
  
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
    print('NexusDebug: Background task onStart at $timestamp, starter: $starter');
    AppLogger.info('BACKGROUND TASK STARTING AT $timestamp');
    FlutterForegroundTask.sendDataToMain('refresh');

    print('NexusDebug: Attempting silent sign-in');
    final user = await AuthService().signInSilently();
    
    if (user != null) {
      print('NexusDebug: Silent sign-in success. Using refreshed user session.');
    } else {
      print('NexusDebug: Silent sign-in failed. Attempting to use stored token fallback.');
      final token = await FlutterForegroundTask.getData<String>(key: 'token');
      if (token != null) {
        print('NexusDebug: Falling back to token retrieved from storage');
        AuthService().setBackgroundToken(token);
      } else {
        print('NexusDebug: No token found in foreground task storage');
      }
    }

    final type = await FlutterForegroundTask.getData<String>(key: 'type');
    final taskId = await FlutterForegroundTask.getData<String>(key: 'id');
    final password = await FlutterForegroundTask.getData<String>(key: 'pwd') ?? '';

    print('NexusDebug: Task info - type: $type, id: $taskId, pwd length: ${password.length}');

    if (taskId == null) {
      print('NexusDebug: taskId is NULL, stopping service');
      FlutterForegroundTask.stopService();
      return;
    }

    try {
      final nexus = NexusService();
      String finalTitle = 'Task Complete';
      String finalBody = 'Your file is ready.';
      
      if (type == 'upload') {
        final filePath = await FlutterForegroundTask.getData<String>(key: 'path');
        print('NexusDebug: Starting upload for $filePath');
        if (filePath == null) throw Exception('Missing upload path');
        
        await nexus.encodeAndUpload(File(filePath), password, explicitTaskId: taskId);
        finalTitle = '✅ Upload Complete';
        finalBody = 'File secured on YouTube.';
        print('NexusDebug: Upload finished successfully');
      } else if (type == 'download') {
        final videoId = await FlutterForegroundTask.getData<String>(key: 'video_id');
        final fileName = await FlutterForegroundTask.getData<String>(key: 'file_name');
        
        print('NexusDebug: Starting download for $fileName ($videoId)');
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
        print('NexusDebug: Download finished successfully');
      }

      FlutterForegroundTask.sendDataToMain('refresh');
      
      // 1. Stop Foreground Service (removes non-swipable notif)
      print('NexusDebug: Stopping service after success');
      await FlutterForegroundTask.stopService();

      // 2. Show Standard swipable notification
      await _showFinalNotification(finalTitle, finalBody, true);

    } catch (e) {
      print('NexusDebug: BACKGROUND ERROR: $e');
      AppLogger.error('BACKGROUND ERROR: $e');
      FlutterForegroundTask.sendDataToMain('refresh');
      
      print('NexusDebug: Stopping service after failure');
      await FlutterForegroundTask.stopService();
      await _showFinalNotification('❌ Task Failed', e.toString().split('\n').first, false);
    }
  }
}
