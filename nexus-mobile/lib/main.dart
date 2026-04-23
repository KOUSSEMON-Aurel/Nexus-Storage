import 'package:flutter/material.dart';
import 'package:flutter/services.dart' as services;
import 'package:flutter_native_splash/flutter_native_splash.dart';
import 'package:permission_handler/permission_handler.dart';
import 'package:file_picker/file_picker.dart';
import 'package:image_picker/image_picker.dart';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import 'package:device_info_plus/device_info_plus.dart';
import 'dart:io';

import 'package:nexus_mobile/services/database_service.dart';
import 'package:nexus_mobile/services/nexus_service.dart';
// background task handler imported only in isolate entrypoint; not used here
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

    // Validation at startup (Rule 8)
    await Future.wait([
      _requestPermissions(),
      _initNotificationChannels(),
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
    runApp(
      MaterialApp(
        theme: AppTheme.darkTheme,
        home: Scaffold(
          body: Padding(
            padding: const EdgeInsets.all(AppSpacing.xl),
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                const Icon(
                  Icons.error_outline,
                  color: AppColors.error,
                  size: 64,
                ),
                const SizedBox(height: AppSpacing.lg),
                const Text(
                  'System Error',
                  style: TextStyle(fontSize: 24, fontWeight: FontWeight.bold),
                ),
                const SizedBox(height: AppSpacing.md),
                SelectableText(error.toString(), textAlign: TextAlign.center),
                const SizedBox(height: AppSpacing.xl),
                AppButton(label: 'Retry', onPressed: () => main()),
              ],
            ),
          ),
        ),
      ),
    );
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
      autoRunOnBoot: false,
      allowWakeLock: true,
      allowWifiLock: true,
    ),
  );
}

/// Creates the Android notification channels needed for progress and completion notifications.
/// Must be called after [FlutterLocalNotificationsPlugin] is initialized.
Future<void> _initNotificationChannels() async {
  const initSettings = InitializationSettings(
    android: AndroidInitializationSettings('@mipmap/launcher_icon'),
  );
  final plugin = FlutterLocalNotificationsPlugin();
  await plugin.initialize(settings: initSettings);

  final androidImpl = plugin
      .resolvePlatformSpecificImplementation<
        AndroidFlutterLocalNotificationsPlugin
      >();

  await androidImpl?.createNotificationChannel(
    const AndroidNotificationChannel(
      'nexus_upload_channel',
      'Nexus Transferts',
      description: 'Progression des transferts Nexus (upload/download).',
      importance: Importance.low,
      showBadge: false,
    ),
  );

  await androidImpl?.createNotificationChannel(
    const AndroidNotificationChannel(
      'nexus_final_channel',
      'Nexus Tâches Terminées',
      description: 'Notifications de fin de transfert Nexus.',
      importance: Importance.high,
    ),
  );

  AppLogger.info('Notification channels created.');
}

Future<void> _requestPermissions() async {
  if (Platform.isAndroid) {
    final deviceInfo = DeviceInfoPlugin();
    final androidInfo = await deviceInfo.androidInfo;
    final sdkInt = androidInfo.version.sdkInt;

    if (sdkInt < 33) {
      // Android 12 (API 32) and below: legacy storage is needed for some operations
      await Permission.storage.request();
    } else {
      // Android 13+ (API 33+): We use MediaStore for downloads, 
      // but we might need photo/video permissions for picking files.
      await [
        Permission.photos,
        Permission.videos,
      ].request();
    }

    // Common permissions
    await Permission.notification.request();
  }
}

class NexusApp extends StatefulWidget {
  const NexusApp({super.key});

  @override
  State<NexusApp> createState() => _NexusAppState();
}

class _NexusAppState extends State<NexusApp> with WidgetsBindingObserver {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      // Re-check critical permissions when returning from settings
      _requestPermissions();
    }
  }

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
      systemNavigationBarColor: isDark
          ? const Color(0xFF020617)
          : const Color(0xFFF8FAFC),
      systemNavigationBarIconBrightness: isDark
          ? Brightness.light
          : Brightness.dark,
      statusBarIconBrightness: isDark ? Brightness.light : Brightness.dark,
      systemNavigationBarDividerColor: Colors.transparent,
    );
  }

  void _showUploadPreview(
    BuildContext context,
    File file,
    String name,
    bool isDirectory,
  ) {
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
              insetPadding: const EdgeInsets.symmetric(
                horizontal: AppSpacing.lg,
              ),
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
                            borderRadius: BorderRadius.circular(
                              AppSpacing.radiusMd,
                            ),
                          ),
                          child: Icon(
                            isDirectory
                                ? Icons.folder_outlined
                                : Icons.insert_drive_file_outlined,
                            color: AppColors.primary,
                            size: 28,
                          ),
                        ),
                        const SizedBox(width: AppSpacing.md),
                        Expanded(
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                L10n.get('upload', lang),
                                style: Theme.of(context).textTheme.titleLarge,
                              ),
                              Text(
                                name,
                                style: Theme.of(context).textTheme.bodyMedium,
                                overflow: TextOverflow.ellipsis,
                              ),
                            ],
                          ),
                        ),
                      ],
                    ),
                    const SizedBox(height: AppSpacing.xl),
                    Text(
                      'DOUBLE ENCRYPTION (OPTIONAL)',
                      style: Theme.of(context).textTheme.labelLarge,
                    ),
                    const SizedBox(height: AppSpacing.sm),
                    TextField(
                      obscureText: true,
                      onChanged: (v) => password = v,
                      decoration: InputDecoration(
                        hintText: 'Passphrase for extra security',
                        filled: true,
                        fillColor: Colors.white.withValues(alpha: 0.05),
                        border: OutlineInputBorder(
                          borderRadius: BorderRadius.circular(
                            AppSpacing.radiusMd,
                          ),
                          borderSide: BorderSide.none,
                        ),
                        prefixIcon: const Icon(
                          Icons.lock_outline,
                          size: 20,
                          color: AppColors.primary,
                        ),
                      ),
                    ),
                    const SizedBox(height: AppSpacing.xl),
                    Row(
                      children: [
                        Expanded(
                          child: TextButton(
                            onPressed: () => Navigator.pop(context),
                            child: const Text(
                              'Cancel',
                              style: TextStyle(color: AppColors.textSecondary),
                            ),
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
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: const Text('Cancel'),
          ),
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

  Future<void> _startBackgroundUpload(
    File file,
    String name,
    String password,
  ) async {
    final taskId = DateTime.now().millisecondsSinceEpoch.toString();
    final notifId = taskId.hashCode;
    AppLogger.info(
      'NexusDebug: Starting direct async upload for $name, taskId: $taskId',
    );

    // Restauration du ForegroundService pour empêcher l'OS de geler l'application
    // Indispensable sur Android 13/14 (systèmes Griffin/Hiber)
    try {
      if (!await FlutterForegroundTask.isRunningService) {
        await FlutterForegroundTask.startService(
          notificationTitle: 'Nexus : Sécurisation en cours...',
          notificationText: 'Préparation du fichier : $name',
          callback: null, // Callback non nécessaire en mode direct
        );
      }

      final nexus = NexusService();
      await nexus.encodeAndUpload(file, password, explicitTaskId: taskId);
      DatabaseService().notifyChange();

      await FlutterForegroundTask.stopService();
      // Dismiss progress notification and show final success
      await FlutterLocalNotificationsPlugin().cancel(id: notifId);
      await FlutterLocalNotificationsPlugin().show(
        id: notifId + 1,
        title: '✅ Upload terminé',
        body: 'Fichier sécurisé : $name',
        notificationDetails: const NotificationDetails(
          android: AndroidNotificationDetails(
            'nexus_final_channel',
            'Nexus Tâches Terminées',
            importance: Importance.high,
            priority: Priority.high,
            autoCancel: true,
          ),
        ),
      );
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('✅ Upload Complete: $name'),
            backgroundColor: Colors.green,
          ),
        );
      }
    } catch (e) {
      AppLogger.error('Upload Error: $e');
      AppLogger.error('NEXUS_UPLOAD_CRITICAL_ERROR: $e');
      // Dismiss progress notification and show final failure
      try {
        await FlutterLocalNotificationsPlugin().cancel(id: notifId);
        await FlutterLocalNotificationsPlugin().show(
          id: notifId + 1,
          title: '❌ Upload échoué',
          body: '$name — ${e.toString().split('\n').first}',
          notificationDetails: const NotificationDetails(
            android: AndroidNotificationDetails(
              'nexus_final_channel',
              'Nexus Tâches Terminées',
              importance: Importance.high,
              priority: Priority.high,
              autoCancel: true,
              icon: '@mipmap/launcher_icon',
            ),
          ),
        );
      } catch (notifErr) {
        AppLogger.warn(
          'NEXUS_UPLOAD: Failed to show error notification: $notifErr',
        );
      }
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('❌ Upload Failed: $e'),
            backgroundColor: Colors.red,
          ),
        );
      }
    }
  }

  Future<void> _startBackgroundDownload(FileRecord record) async {
    final taskId = DateTime.now().millisecondsSinceEpoch.toString();
    final notifId = taskId.hashCode;
    final fileName = record.path.split('/').last;
    AppLogger.info(
      'NexusDebug: Starting direct async download for $fileName, taskId: $taskId',
    );

    // Restauration du ForegroundService pour empêcher l'OS de geler l'application
    try {
      if (!await FlutterForegroundTask.isRunningService) {
        await FlutterForegroundTask.startService(
          notificationTitle: 'Nexus : Récupération en cours...',
          notificationText: 'Téléchargement de $fileName',
          callback: null,
        );
      }

      final nexus = NexusService();
      await nexus.downloadAndDecrypt(
        record,
        record.key,
        explicitTaskId: taskId,
      );
      DatabaseService().notifyChange();

      await FlutterForegroundTask.stopService();
      // Dismiss progress notification and show final success
      await FlutterLocalNotificationsPlugin().cancel(id: notifId);
      await FlutterLocalNotificationsPlugin().show(
        id: notifId + 1,
        title: '✅ Download terminé',
        body: 'Sauvegardé dans /Download/NexusStorage/$fileName',
        notificationDetails: const NotificationDetails(
          android: AndroidNotificationDetails(
            'nexus_final_channel',
            'Nexus Tâches Terminées',
            importance: Importance.high,
            priority: Priority.high,
            autoCancel: true,
          ),
        ),
      );
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('✅ Download Complete: $fileName'),
            backgroundColor: Colors.green,
          ),
        );
      }
    } catch (e) {
      AppLogger.error('Download Error: $e');
      await FlutterLocalNotificationsPlugin().cancel(id: notifId);
      await FlutterLocalNotificationsPlugin().show(
        id: notifId + 1,
        title: '❌ Download échoué',
        body: '$fileName — ${e.toString().split('\n').first}',
        notificationDetails: const NotificationDetails(
          android: AndroidNotificationDetails(
            'nexus_final_channel',
            'Nexus Tâches Terminées',
            importance: Importance.high,
            priority: Priority.high,
            autoCancel: true,
          ),
        ),
      );
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text('❌ Download Failed: $e'),
            backgroundColor: Colors.red,
          ),
        );
      }
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
            Container(color: AppColors.getBackground(context)),

            // Background Blobs for Glassmorphisme (Design Rule)
            Positioned(
              top: -100,
              left: -100,
              child: Container(
                width: 300,
                height: 300,
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  color: AppColors.primary.withValues(
                    alpha: isDark ? 0.15 : 0.08,
                  ),
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
                  color: AppColors.secondary.withValues(
                    alpha: isDark ? 0.1 : 0.05,
                  ),
                ),
              ),
            ),

            SafeArea(child: FilesPage(onDownload: _startBackgroundDownload)),

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
                      },
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
                          _showUploadPreview(
                            context,
                            file,
                            result.files.single.name,
                            false,
                          );
                        }
                      } else if (action == 'Camera') {
                        final ImagePicker imagePicker = ImagePicker();
                        final XFile? photo = await imagePicker.pickImage(
                          source: ImageSource.camera,
                        );
                        if (photo != null && mounted) {
                          if (!context.mounted) return;
                          File file = File(photo.path);
                          _showUploadPreview(
                            context,
                            file,
                            file.path.split('/').last,
                            false,
                          );
                        }
                      } else if (action == 'Folder') {
                        String? path = await FilePicker.getDirectoryPath();
                        if (path != null && mounted) {
                          if (!context.mounted) return;
                          File file = File(path);
                          _showUploadPreview(
                            context,
                            file,
                            path.split('/').last,
                            true,
                          );
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
