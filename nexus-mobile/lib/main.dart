import 'package:flutter/material.dart';
import 'package:flutter/services.dart' as services;
import 'package:permission_handler/permission_handler.dart';
import 'package:file_picker/file_picker.dart';
import 'package:image_picker/image_picker.dart';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import 'dart:io';

import 'services/database_service.dart';
import 'services/worker_service.dart';
import 'services/nexus_service.dart';
import 'services/task_handler.dart';
import 'ui/files_page.dart';
import 'ui/tasks_page.dart';
import 'ui/settings_page.dart';
import 'ui/widgets/speed_dial_fab.dart';
import 'services/settings_service.dart';
import 'utils/l10n.dart';

void main() async {
  try {
    WidgetsFlutterBinding.ensureInitialized();
    
    _initForegroundTask();

    // Enable Full Screen / Edge-to-Edge
    services.SystemChrome.setSystemUIOverlayStyle(const services.SystemUiOverlayStyle(
      statusBarColor: Colors.transparent,
      systemNavigationBarColor: Colors.transparent,
    ));
    await services.SystemChrome.setEnabledSystemUIMode(services.SystemUiMode.edgeToEdge);

    await _requestPermissions();
    
    final db = DatabaseService();
    await db.database; 
    
    final settings = SettingsService();
    await settings.init();

    runApp(const NexusApp());
  } catch (error, stackTrace) {
    print('CRITICAL STARTUP ERROR: $error');
    print(stackTrace);
    runApp(MaterialApp(
      home: Scaffold(
        body: Center(
          child: SelectableText('Initialization Error: $error\n\n$stackTrace'),
        ),
      ),
    ));
  }
}

void _initForegroundTask() {
  FlutterForegroundTask.init(
    androidNotificationOptions: AndroidNotificationOptions(
      channelId: 'nexus_upload_channel',
      channelName: 'Nexus Upload Service',
      channelDescription: 'Handles secure file uploads in background.',
      channelImportance: NotificationChannelImportance.LOW,
      priority: NotificationPriority.LOW,
      iconData: const NotificationIconData(
        resType: ResourceType.mipmap,
        resPrefix: ResourcePrefix.ic,
        name: 'launcher',
      ),
    ),
    iosNotificationOptions: const IOSNotificationOptions(
      showNotification: true,
      playSound: false,
    ),
    foregroundTaskOptions: const ForegroundTaskOptions(
      interval: 5000,
      isOnceEvent: false,
      autoRunOnBoot: true,
      allowWakeLock: true,
      allowWifiLock: true,
    ),
  );
}

Future<void> _requestPermissions() async {
  if (Platform.isAndroid) {
    await [
      Permission.storage,
      Permission.photos,
      Permission.videos,
      Permission.notification,
    ].request();
  }
}

class NexusApp extends StatelessWidget {
  const NexusApp({super.key});

  @override
  Widget build(BuildContext context) {
    final settings = SettingsService();

    return ValueListenableBuilder<ThemeMode>(
      valueListenable: settings.themeMode,
      builder: (context, mode, child) {
        return ValueListenableBuilder<String>(
          valueListenable: settings.language,
          builder: (context, lang, child) {
            return MaterialApp(
              title: 'Nexus Storage',
              debugShowCheckedModeBanner: false,
              themeMode: mode,
              theme: ThemeData(
                brightness: Brightness.light,
                scaffoldBackgroundColor: const Color(0xFFF8FAFC),
                colorScheme: ColorScheme.fromSeed(
                  seedColor: const Color(0xFF1A73E8),
                  brightness: Brightness.light,
                ),
                useMaterial3: true,
                fontFamily: 'Roboto',
              ),
              darkTheme: ThemeData(
                brightness: Brightness.dark,
                scaffoldBackgroundColor: const Color(0xFF020617),
                colorScheme: ColorScheme.fromSeed(
                  seedColor: const Color(0xFF6366F1),
                  primary: const Color(0xFF6366F1),
                  surface: const Color(0xFF0F172A),
                  brightness: Brightness.dark,
                ),
                useMaterial3: true,
                fontFamily: 'Roboto',
              ),
              home: const MainScreen(),
            );
          },
        );
      },
    );
  }
}

class MainScreen extends StatefulWidget {
  const MainScreen({super.key});

  @override
  State<MainScreen> createState() => _MainScreenState();
}

class _MainScreenState extends State<MainScreen> {
  @override
  void initState() {
    super.initState();
    FlutterForegroundTask.receivePort?.listen(_onReceiveTaskData);
  }

  @override
  void dispose() {
    super.dispose();
  }

  void _onReceiveTaskData(dynamic data) {
    // Handle data from background
  }

  services.SystemUiOverlayStyle _getSystemUIStyle(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    return services.SystemUiOverlayStyle(
      statusBarColor: Colors.transparent,
      systemNavigationBarColor: isDark ? const Color(0xFF020617) : const Color(0xFFF8FAFC),
      systemNavigationBarIconBrightness: isDark ? Brightness.light : Brightness.dark,
      statusBarIconBrightness: isDark ? Brightness.light : Brightness.dark,
      systemNavigationBarDividerColor: Colors.transparent,
    );
  }

  void _showUploadPreview(BuildContext context, File file, String name, bool isDirectory) {
    String password = '';
    final isDark = Theme.of(context).brightness == Brightness.dark;
    
    showDialog(
      context: context,
      barrierDismissible: false,
      builder: (ctx) => Dialog(
        backgroundColor: Colors.transparent,
        insetPadding: const EdgeInsets.symmetric(horizontal: 24),
        child: Container(
          padding: const EdgeInsets.all(28),
          decoration: BoxDecoration(
            color: isDark ? const Color(0xFF1E293B) : Colors.white,
            borderRadius: BorderRadius.circular(32),
            boxShadow: [
              BoxShadow(
                color: Colors.black.withOpacity(0.2),
                blurRadius: 20,
                offset: const Offset(0, 10),
              )
            ],
          ),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Container(
                    padding: const EdgeInsets.all(12),
                    decoration: BoxDecoration(
                      color: const Color(0xFF1A73E8).withOpacity(0.1),
                      borderRadius: BorderRadius.circular(16),
                    ),
                    child: Icon(
                      isDirectory ? Icons.folder_outlined : Icons.insert_drive_file_outlined, 
                      color: const Color(0xFF1A73E8), size: 32
                    ),
                  ),
                  const SizedBox(width: 20),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        const Text('Upload Preview', style: TextStyle(fontWeight: FontWeight.w800, fontSize: 18)),
                        Text(name, style: TextStyle(color: Colors.grey[600], fontSize: 13, overflow: TextOverflow.ellipsis)),
                      ],
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 32),
              const Text('Double Encryption (Optional)', style: TextStyle(fontWeight: FontWeight.w700, fontSize: 14)),
              const SizedBox(height: 12),
              TextField(
                obscureText: true,
                onChanged: (v) => password = v,
                style: TextStyle(color: isDark ? Colors.white : Colors.black87),
                decoration: InputDecoration(
                  hintText: 'Enter password for extra security',
                  hintStyle: TextStyle(color: Colors.grey.withOpacity(0.5)),
                  filled: true,
                  fillColor: isDark ? Colors.white.withOpacity(0.05) : Colors.black.withOpacity(0.05),
                  border: OutlineInputBorder(borderRadius: BorderRadius.circular(16), borderSide: BorderSide.none),
                  prefixIcon: const Icon(Icons.lock_outline, size: 20, color: Color(0xFF1A73E8)),
                ),
              ),
              const SizedBox(height: 32),
              Row(
                children: [
                  Expanded(
                    child: TextButton(
                      onPressed: () => Navigator.pop(ctx),
                      child: Text('Cancel', style: TextStyle(color: Colors.grey[600], fontWeight: FontWeight.w600)),
                    ),
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    flex: 2,
                    child: ElevatedButton(
                      onPressed: () {
                        Navigator.pop(ctx);
                        ScaffoldMessenger.of(context).showSnackBar(
                          SnackBar(
                            content: Text('Starting upload: $name'),
                            action: SnackBarAction(
                              label: 'View Activity',
                              onPressed: () => Navigator.push(context, MaterialPageRoute(builder: (_) => const TasksPage())),
                            ),
                          ),
                        );
                        _startBackgroundUpload(file, name, password);
                      },
                      style: ElevatedButton.styleFrom(
                        backgroundColor: const Color(0xFF1A73E8),
                        foregroundColor: Colors.white,
                        padding: const EdgeInsets.symmetric(vertical: 16),
                        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(16)),
                        elevation: 0,
                      ),
                      child: const Text('Confirm', style: TextStyle(fontWeight: FontWeight.w800)),
                    ),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  Future<void> _startBackgroundUpload(File file, String name, String password) async {
    final taskId = DateTime.now().millisecondsSinceEpoch.toString();
    
    // Start Service if not running
    if (!await FlutterForegroundTask.isRunningService) {
      await FlutterForegroundTask.startService(
        notificationTitle: 'Nexus: Upload Starting',
        notificationText: 'Preparing $name...',
        callback: startCallback,
      );
    }

    // Save task data for background isolate
    await FlutterForegroundTask.saveData(key: 'upload_path', value: file.path);
    await FlutterForegroundTask.saveData(key: 'upload_pwd', value: password);
    await FlutterForegroundTask.saveData(key: 'upload_id', value: taskId);
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;

    return AnnotatedRegion<services.SystemUiOverlayStyle>(
      value: _getSystemUIStyle(context),
      child: Scaffold(
        extendBodyBehindAppBar: true,
        body: Stack(
          children: [
            Positioned.fill(
              child: Container(
                decoration: BoxDecoration(
                  gradient: RadialGradient(
                    center: const Alignment(-1, -1),
                    radius: 1.5,
                    colors: isDark
                        ? [const Color(0xFF1E1B4B), const Color(0xFF020617)]
                        : [const Color(0xFFE0E7FF), const Color(0xFFF8FAFC)],
                  ),
                ),
              ),
            ),
            
            const SafeArea(
              child: FilesPage(),
            ),

            // Speed Dial FAB
            Positioned.fill(
              child: ValueListenableBuilder<String>(
                valueListenable: SettingsService().language,
                builder: (context, lang, child) {
                  return SpeedDialFab(
                    actions: {
                      L10n.get('upload', lang): Icons.cloud_upload_outlined,
                      L10n.get('activity', lang): Icons.sync_outlined,
                      L10n.get('settings', lang): Icons.settings_outlined,
                    },
                    nestedActions: {
                      L10n.get('upload', lang): {
                        'File': Icons.insert_drive_file_outlined,
                        'Camera': Icons.camera_alt_outlined,
                        'Folder': Icons.folder_open_outlined,
                      }
                    },
                    onActionTap: (action) async {
                      if (action == L10n.get('settings', lang)) {
                        Navigator.push(context, MaterialPageRoute(builder: (_) => const SettingsPage()));
                      } else if (action == L10n.get('activity', lang)) {
                        Navigator.push(context, MaterialPageRoute(builder: (_) => const TasksPage()));
                      } else if (action == 'File') {
                        FilePickerResult? result = await FilePicker.pickFiles();
                        if (result != null && mounted) {
                          File file = File(result.files.single.path!);
                          _showUploadPreview(context, file, result.files.single.name, false);
                        }
                      } else if (action == 'Camera') {
                        final ImagePicker picker = ImagePicker();
                        final XFile? photo = await picker.pickImage(source: ImageSource.camera);
                        if (photo != null && mounted) {
                          File file = File(photo.path);
                          _showUploadPreview(context, file, file.path.split('/').last, false);
                        }
                      } else if (action == 'Folder') {
                        String? path = await FilePicker.getDirectoryPath();
                        if (path != null && mounted) {
                          File file = File(path);
                          _showUploadPreview(context, file, path.split('/').last, true);
                        }
                      }
                    },
                  );
                },
              ),
            ),
          ],
        ),
      ),
    );
  }
}
