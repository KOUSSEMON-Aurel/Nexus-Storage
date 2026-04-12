import 'package:flutter/material.dart';
import 'package:flutter/services.dart' as services;
import 'package:flutter_native_splash/flutter_native_splash.dart';
import 'package:permission_handler/permission_handler.dart';
import 'package:file_picker/file_picker.dart';
import 'package:image_picker/image_picker.dart';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import 'dart:io';

import 'package:nexus_mobile/services/database_service.dart';
import 'package:nexus_mobile/services/task_handler.dart';
import 'package:nexus_mobile/models/file_record.dart';
import 'package:nexus_mobile/ui/files_page.dart';
import 'package:nexus_mobile/ui/tasks_page.dart';
import 'package:nexus_mobile/ui/settings_page.dart';
import 'package:nexus_mobile/ui/widgets/speed_dial_fab.dart';
import 'package:nexus_mobile/services/settings_service.dart';
import 'package:nexus_mobile/services/auth_service.dart';
import 'package:nexus_mobile/services/logger_service.dart';
import 'package:nexus_mobile/theme/app_theme.dart';
import 'package:nexus_mobile/theme/app_colors.dart';
import 'package:nexus_mobile/theme/app_spacing.dart';
import 'package:nexus_mobile/utils/l10n.dart';
import 'package:animations/animations.dart';
import 'package:nexus_mobile/ui/widgets/glass_card.dart';
import 'package:nexus_mobile/ui/widgets/app_button.dart';

import 'package:nexus_mobile/services/cleanup_service.dart';
import 'package:nexus_mobile/core/thermal_monitor.dart';

void main() async {
  try {
    WidgetsBinding widgetsBinding = WidgetsFlutterBinding.ensureInitialized();
    FlutterNativeSplash.preserve(widgetsBinding: widgetsBinding);
    
    AppLogger.info('Initializing Nexus Storage...');
    
    _initForegroundTask();
    ThermalMonitor.start(); // Start thermal monitoring

    // ... (rest of main code until Validation at startup)

    // Validation at startup (Rule 8)
    await Future.wait([
      _requestPermissions(),
      DatabaseService().database,
      SettingsService().init(),
      AuthService().signInSilently(),
    ]);

    // Cleanup orphaned sessions
    await CleanupService.performStartupCleanup();

    AppLogger.info('Startup validation successful.');
    FlutterNativeSplash.remove();
    runApp(const NexusApp());
  } catch (error, stackTrace) {
    AppLogger.error('CRITICAL STARTUP ERROR', error, stackTrace);
    FlutterNativeSplash.remove();
    runApp(MaterialApp(
      theme: AppTheme.darkTheme,
      home: Scaffold(
        body: Padding(
          padding: const EdgeInsets.all(AppSpacing.xl),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              const Icon(Icons.error_outline, color: AppColors.error, size: 64),
              const SizedBox(height: AppSpacing.lg),
              const Text('System Error', style: TextStyle(fontSize: 24, fontWeight: FontWeight.bold)),
              const SizedBox(height: AppSpacing.md),
              SelectableText(error.toString(), textAlign: TextAlign.center),
              const SizedBox(height: AppSpacing.xl),
              AppButton(label: 'Retry', onPressed: () => main()),
            ],
          ),
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
    ),
    iosNotificationOptions: const IOSNotificationOptions(
      showNotification: true,
      playSound: false,
    ),
    foregroundTaskOptions: ForegroundTaskOptions(
      eventAction: ForegroundTaskEventAction.nothing(),
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
              theme: AppTheme.lightTheme,
              darkTheme: AppTheme.darkTheme,
              home: const EntrancePage(),
            );
          },
        );
      },
    );
  }
}

class EntrancePage extends StatelessWidget {
  const EntrancePage({super.key});

  @override
  Widget build(BuildContext context) {
    return PageTransitionSwitcher(
      duration: const Duration(milliseconds: 500),
      transitionBuilder: (child, primaryAnimation, secondaryAnimation) {
        return SharedAxisTransition(
          animation: primaryAnimation,
          secondaryAnimation: secondaryAnimation,
          transitionType: SharedAxisTransitionType.horizontal,
          child: child,
        );
      },
      child: const MainScreen(),
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
    FlutterForegroundTask.addTaskDataCallback(_onReceiveTaskData);
  }

  @override
  void dispose() {
    FlutterForegroundTask.removeTaskDataCallback(_onReceiveTaskData);
    super.dispose();
  }

  void _onReceiveTaskData(dynamic data) {
    if (data == 'refresh') {
      AppLogger.info('UI: Refresh signal received from background task');
      DatabaseService().notifyChange();
    }
  }

  void _pushSmooth(Widget page) {
    Navigator.push(
      context,
      PageRouteBuilder(
        pageBuilder: (context, animation, secondaryAnimation) => page,
        transitionsBuilder: (context, animation, secondaryAnimation, child) {
          return SharedAxisTransition(
            animation: animation,
            secondaryAnimation: secondaryAnimation,
            transitionType: SharedAxisTransitionType.scaled,
            child: child,
          );
        },
      ),
    );
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
    final lang = SettingsService().language.value;
    
    showGeneralDialog(
      context: context,
      barrierDismissible: true,
      barrierLabel: 'UploadPreview',
      pageBuilder: (context, anim1, anim2) => const SizedBox.shrink(),
      transitionBuilder: (context, anim1, anim2, child) {
        return FadeTransition(
          opacity: anim1,
          child: ScaleTransition(
            scale: Tween<double>(begin: 0.9, end: 1.0).animate(anim1),
            child: Dialog(
              backgroundColor: Colors.transparent,
              insetPadding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg),
              child: GlassCard(
                padding: const EdgeInsets.all(AppSpacing.lg),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Row(
                      children: [
                        Container(
                          padding: const EdgeInsets.all(AppSpacing.sm),
                          decoration: BoxDecoration(
                            color: AppColors.primary.withValues(alpha: 0.1),
                            borderRadius: BorderRadius.circular(AppSpacing.radiusMd),
                          ),
                          child: Icon(
                            isDirectory ? Icons.folder_outlined : Icons.insert_drive_file_outlined, 
                            color: AppColors.primary, size: 28
                          ),
                        ),
                        const SizedBox(width: AppSpacing.md),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(L10n.get('upload', lang), style: Theme.of(context).textTheme.titleLarge),
                              Text(name, style: Theme.of(context).textTheme.bodyMedium, overflow: TextOverflow.ellipsis),
                            ],
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: AppSpacing.xl),
                    Text('DOUBLE ENCRYPTION (OPTIONAL)', style: Theme.of(context).textTheme.labelLarge),
                    const SizedBox(height: AppSpacing.sm),
                    TextField(
                      obscureText: true,
                      onChanged: (v) => password = v,
                      decoration: InputDecoration(
                        hintText: 'Passphrase for extra security',
                        filled: true,
                        fillColor: Colors.white.withValues(alpha: 0.05),
                        border: OutlineInputBorder(borderRadius: BorderRadius.circular(AppSpacing.radiusMd), borderSide: BorderSide.none),
                        prefixIcon: const Icon(Icons.lock_outline, size: 20, color: AppColors.primary),
                      ),
                    ),
                    const SizedBox(height: AppSpacing.xl),
                    Row(
                      children: [
                        Expanded(
                          child: TextButton(
                            onPressed: () => Navigator.pop(context),
                            child: const Text('Cancel', style: TextStyle(color: AppColors.textSecondary)),
                          ),
                        ),
                        const SizedBox(width: AppSpacing.sm),
                        Expanded(
                          flex: 2,
                          child: AppButton(
                            label: 'Confirm',
                            onPressed: () {
                              if (!AuthService().isAuthenticated) {
                                Navigator.pop(context);
                                _showAuthRequiredDialog(context);
                                return;
                              }
                              Navigator.pop(context);
                              _startBackgroundUpload(file, name, password);
                            },
                          ),
                        ),
                      ],
                    ),
                  ],
                ),
              ),
            ),
          ),
        );
      },
    );
  }

  void _showAuthRequiredDialog(BuildContext context) {
    final lang = SettingsService().language.value;
    showDialog(
      context: context,
      builder: (c) => AlertDialog(
        title: Row(
          children: [
            const Icon(Icons.account_circle_outlined, color: AppColors.primary),
            const SizedBox(width: AppSpacing.sm),
            Text(L10n.get('auth_required', lang)),
          ],
        ),
        content: Text(L10n.get('please_connect_google', lang)),
        actions: [
          TextButton(onPressed: () => Navigator.pop(c), child: const Text('Cancel')),
          AppButton(
            label: 'Connect',
            isFullWidth: false,
            onPressed: () {
              Navigator.pop(c);
              _pushSmooth(const SettingsPage());
            },
          ),
        ],
      ),
    );
  }

  Future<void> _startBackgroundUpload(File file, String name, String password) async {
    final taskId = DateTime.now().millisecondsSinceEpoch.toString();
    
    await FlutterForegroundTask.saveData(key: 'type', value: 'upload');
    await FlutterForegroundTask.saveData(key: 'path', value: file.path);
    await FlutterForegroundTask.saveData(key: 'pwd', value: password);
    await FlutterForegroundTask.saveData(key: 'id', value: taskId);
    
    final token = await AuthService().getAccessToken();
    if (token != null) {
      await FlutterForegroundTask.saveData(key: 'token', value: token);
    }
    
    if (!await FlutterForegroundTask.isRunningService) {
      await FlutterForegroundTask.startService(
        notificationTitle: 'Nexus: Upload Starting',
        notificationText: 'Preparing $name...',
        serviceTypes: [ForegroundServiceTypes.dataSync],
        callback: startCallback,
      );
    }
  }

  Future<void> _startBackgroundDownload(FileRecord record) async {
    final taskId = DateTime.now().millisecondsSinceEpoch.toString();
    
    await FlutterForegroundTask.saveData(key: 'type', value: 'download');
    await FlutterForegroundTask.saveData(key: 'video_id', value: record.videoId);
    await FlutterForegroundTask.saveData(key: 'file_name', value: record.path.split('/').last);
    await FlutterForegroundTask.saveData(key: 'pwd', value: record.key);
    await FlutterForegroundTask.saveData(key: 'id', value: taskId);
    
    final token = await AuthService().getAccessToken();
    if (token != null) {
      await FlutterForegroundTask.saveData(key: 'token', value: token);
    }
    
    if (!await FlutterForegroundTask.isRunningService) {
      await FlutterForegroundTask.startService(
        notificationTitle: 'Nexus: Download Starting',
        notificationText: 'Fetching ${record.path.split('/').last}...',
        serviceTypes: [ForegroundServiceTypes.dataSync],
        callback: startCallback,
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    
    return AnnotatedRegion<services.SystemUiOverlayStyle>(
      value: _getSystemUIStyle(context),
      child: Scaffold(
        body: Stack(
          children: [
            // Theme-aware background
            Container(
              color: AppColors.getBackground(context),
            ),
            
            // Background Blobs for Glassmorphisme (Design Rule)
            Positioned(
              top: -100,
              left: -100,
              child: Container(
                width: 300,
                height: 300,
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  color: AppColors.primary.withValues(alpha: isDark ? 0.15 : 0.08),
                ),
              ),
            ),
            Positioned(
              bottom: -50,
              right: -50,
              child: Container(
                width: 250,
                height: 250,
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  color: AppColors.secondary.withValues(alpha: isDark ? 0.1 : 0.05),
                ),
              ),
            ),
            
            SafeArea(
              child: FilesPage(onDownload: _startBackgroundDownload),
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
                        _pushSmooth(const SettingsPage());
                      } else if (action == L10n.get('activity', lang)) {
                        _pushSmooth(const TasksPage());
                      } else if (action == 'File') {
                          FilePickerResult? result = await FilePicker.pickFiles();
                          if (result != null && mounted) {
                            if (!context.mounted) return;
                            File file = File(result.files.single.path!);
                            _showUploadPreview(context, file, result.files.single.name, false);
                          }
                      } else if (action == 'Camera') {
                        final ImagePicker imagePicker = ImagePicker();
                        final XFile? photo = await imagePicker.pickImage(source: ImageSource.camera);
                        if (photo != null && mounted) {
                          if (!context.mounted) return;
                          File file = File(photo.path);
                          _showUploadPreview(context, file, file.path.split('/').last, false);
                        }
                      } else if (action == 'Folder') {
                        String? path = await FilePicker.getDirectoryPath();
                        if (path != null && mounted) {
                          if (!context.mounted) return;
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
