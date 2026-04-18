import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'dart:async';
import 'dart:io';
import 'package:permission_handler/permission_handler.dart';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import 'package:nexus_mobile/services/database_service.dart';
import 'package:nexus_mobile/services/settings_service.dart';
import 'package:nexus_mobile/utils/l10n.dart';
import 'package:nexus_mobile/models/file_record.dart';
import 'package:nexus_mobile/services/auth_service.dart';
import 'package:nexus_mobile/theme/app_colors.dart';
import 'package:nexus_mobile/theme/app_spacing.dart';
import 'package:nexus_mobile/ui/widgets/glass_card.dart';
import 'package:nexus_mobile/ui/widgets/app_button.dart';
import 'package:nexus_mobile/ui/widgets/skeleton_item.dart';
import 'package:nexus_mobile/ui/settings_page.dart';
import 'package:nexus_mobile/services/nexus_service.dart';
import 'package:nexus_mobile/services/logger_service.dart';
import 'package:google_sign_in/google_sign_in.dart';

class FilesPage extends StatefulWidget {
  final Function(FileRecord)? onDownload;
  const FilesPage({super.key, this.onDownload});

  @override
  State<FilesPage> createState() => _FilesPageState();
}

class _FilesPageState extends State<FilesPage> {
  final DatabaseService _db = DatabaseService();
  final SettingsService _settings = SettingsService();
  final NexusService _nexus = NexusService();
  List<FileRecord> _files = [];
  String _currentTab = 'my-drive';
  bool _isLoading = true;

  // Multi-select state
  bool _isSelecting = false;
  final Set<int> _selectedIds = {};

  final Map<String, String> _tabs = {
    'my-drive': 'my_drive',
    'recent': 'recent',
    'starred': 'starred',
    'trash': 'trash'
  };

  StreamSubscription<void>? _dbSubscription;

  @override
  void initState() {
    super.initState();
    _refreshFiles();
    _dbSubscription = _db.onChange.listen((_) => _refreshFiles());
  }

  @override
  void dispose() {
    _dbSubscription?.cancel();
    super.dispose();
  }

  Future<void> _refreshFiles() async {
    final isInitial = _files.isEmpty && _isLoading;
    if (isInitial) {
      setState(() => _isLoading = true);
    }
    
    try {
      final files = await _db.listFiles(category: _currentTab);
      if (mounted) {
        setState(() {
          _files = files;
          _isLoading = false;
        });
      }
    } catch (e) {
      if (mounted) setState(() => _isLoading = false);
      AppLogger.error('Refresh files error: $e');
    }
  }

  void _onTabSelected(String tabKey) {
    setState(() {
      _currentTab = tabKey;
      _isSelecting = false;
      _selectedIds.clear();
    });
    _refreshFiles();
  }

  void _toggleSelection(int id) {
    HapticFeedback.selectionClick();
    setState(() {
      if (_selectedIds.contains(id)) {
        _selectedIds.remove(id);
        if (_selectedIds.isEmpty) _isSelecting = false;
      } else {
        _selectedIds.add(id);
      }
    });
  }

  void _startSelecting(int id) {
    HapticFeedback.heavyImpact();
    setState(() {
      _isSelecting = true;
      _selectedIds.add(id);
    });
  }

  void _exitSelecting() {
    setState(() {
      _isSelecting = false;
      _selectedIds.clear();
    });
  }

  Future<void> _handleBulkAction(String action) async {
    final ids = _selectedIds.toList();
    _exitSelecting();
    
    setState(() => _isLoading = true);
    
    try {
      if (action == 'trash') {
        for (var id in ids) {
          await _db.softDelete(id);
        }
      } else if (action == 'delete') {
        final db = await _db.database;
        for (var id in ids) {
          await db.delete('files', where: 'id = ?', whereArgs: [id]);
        }
      } else if (action == 'restore') {
        final db = await _db.database;
        for (var id in ids) {
          await db.update('files', {'deleted_at': null}, where: 'id = ?', whereArgs: [id]);
        }
      } else if (action == 'star') {
        // Toggle star for first item and apply to all? 
        // Better: toggle based on first item
        final first = _files.firstWhere((f) => f.id == ids.first);
        final newStarred = !first.starred;
        for (var id in ids) {
          final file = _files.firstWhere((f) => f.id == id);
          await _db.saveFile(file.copyWith(starred: newStarred));
        }
      }
    } catch (e) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Error: $e')));
    }
    
    _refreshFiles();
  }

  @override
  Widget build(BuildContext context) {
    return ValueListenableBuilder<String>(
      valueListenable: _settings.language,
      builder: (context, lang, child) {
        return PopScope(
          canPop: !_isSelecting,
          onPopInvokedWithResult: (didPop, result) {
            if (!didPop && _isSelecting) _exitSelecting();
          },
          child: RefreshIndicator(
            onRefresh: _refreshFiles,
            backgroundColor: AppColors.surfaceElevated,
            color: AppColors.primary,
            child: CustomScrollView(
              physics: const BouncingScrollPhysics(parent: AlwaysScrollableScrollPhysics()),
              slivers: [
                SliverPadding(
                  padding: const EdgeInsets.all(AppSpacing.md),
                  sliver: SliverList(
                    delegate: SliverChildListDelegate([
                      _buildAuthBanner(context),
                      const SizedBox(height: AppSpacing.md),
                      Row(
                        children: [
                          Expanded(
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Text(
                                  L10n.get(_tabs[_currentTab] ?? 'nexus', lang).toUpperCase(),
                                  style: Theme.of(context).textTheme.labelLarge,
                                ),
                                const SizedBox(height: AppSpacing.sm),
                                Text(
                                  _isSelecting ? '${_selectedIds.length} Selected' : 'Overview', 
                                  style: Theme.of(context).textTheme.displayLarge
                                ),
                              ],
                            ),
                          ),
                          if (_isSelecting)
                            IconButton(
                              icon: const Icon(Icons.close_rounded, color: AppColors.textSecondary),
                              onPressed: _exitSelecting,
                            ),
                        ],
                      ),
                      const SizedBox(height: AppSpacing.lg),
                      _buildTabs(lang),
                      if (_isSelecting) _buildSelectionActions(context, lang),
                      const SizedBox(height: AppSpacing.xl),
                    ]),
                  ),
                ),

                if (_isLoading)
                  const SliverPadding(
                    padding: EdgeInsets.symmetric(horizontal: AppSpacing.md),
                    sliver: SliverToBoxAdapter(child: FileSkeletonList()),
                  )
                else if (_files.isEmpty)
                  SliverFillRemaining(
                    hasScrollBody: false,
                    child: _buildEmptyState(),
                  )
                else
                  SliverPadding(
                    padding: const EdgeInsets.symmetric(horizontal: AppSpacing.md),
                    sliver: SliverList(
                      delegate: SliverChildBuilderDelegate(
                        (context, index) {
                          final file = _files[index];
                          return TweenAnimationBuilder<double>(
                            duration: Duration(milliseconds: 350 + (index * 60)),
                            tween: Tween(begin: 0.0, end: 1.0),
                            curve: Curves.easeOutCubic,
                            builder: (context, value, child) => Transform.translate(
                              offset: Offset(0, 24 * (1 - value)),
                              child: Opacity(opacity: value, child: child),
                            ),
                            child: _buildFileItem(file, lang),
                          );
                        },
                        childCount: _files.length,
                      ),
                    ),
                  ),

                const SliverToBoxAdapter(child: SizedBox(height: 120)),
              ],
            ),
          ),
        );
      },
    );
  }

  Widget _buildSelectionActions(BuildContext context, String lang) {
    final bool isTrash = _currentTab == 'trash';
    return Padding(
      padding: const EdgeInsets.only(top: AppSpacing.md),
      child: GlassCard(
        padding: const EdgeInsets.symmetric(horizontal: AppSpacing.sm, vertical: AppSpacing.xs),
        borderRadius: AppSpacing.radiusMd,
        child: Row(
          mainAxisAlignment: MainAxisAlignment.spaceAround,
          children: [
            if (!isTrash) ...[
              IconButton(onPressed: () => _handleBulkAction('star'), icon: const Icon(Icons.star_border_rounded, color: AppColors.primary)),
              IconButton(onPressed: () => _handleBulkAction('trash'), icon: const Icon(Icons.delete_outline_rounded, color: AppColors.error)),
            ] else ...[
              IconButton(onPressed: () => _handleBulkAction('restore'), icon: const Icon(Icons.restore_page_rounded, color: AppColors.primary)),
              IconButton(onPressed: () => _handleBulkAction('delete'), icon: const Icon(Icons.delete_forever_rounded, color: AppColors.error)),
            ],
            IconButton(
              onPressed: () async {
                for (var id in _selectedIds) {
                  final file = _files.firstWhere((f) => f.id == id);
                  if (!mounted) return;
                  if (widget.onDownload != null) {
                    // Request battery optimization once before starting the batch if possible
                    if (Platform.isAndroid && !await FlutterForegroundTask.isIgnoringBatteryOptimizations) {
                       await FlutterForegroundTask.requestIgnoreBatteryOptimization();
                    }
                    widget.onDownload!(file);
                  } else {
                    if (Platform.isAndroid && !await Permission.manageExternalStorage.isGranted) {
                        await Permission.manageExternalStorage.request();
                    }
                    _nexus.downloadAndDecrypt(file, file.key);
                  }
                }
                if (!mounted) return;
                WidgetsBinding.instance.addPostFrameCallback((_) {
                  if (!mounted) return;
                  ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Download started for selected items')));
                });
                _exitSelecting();
              }, 
              icon: const Icon(Icons.download_rounded, color: AppColors.primary)
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildAuthBanner(BuildContext context) {
    return StreamBuilder<GoogleSignInAccount?>(
      stream: AuthService().userStream,
      initialData: AuthService().currentUser,
      builder: (context, snapshot) {
        if (snapshot.hasData || AuthService().isAuthenticated) return const SizedBox.shrink();
        
        return GlassCard(
          padding: const EdgeInsets.all(AppSpacing.md),
          borderRadius: AppSpacing.radiusMd,
          borderOpacity: 0.2,
          child: Row(
            children: [
              const Icon(Icons.warning_amber_rounded, color: AppColors.warning),
              const SizedBox(width: AppSpacing.md),
              const Expanded(
                child: Text(
                  'Cloud features restricted. Connect to Google to sync your library.',
                  style: TextStyle(fontSize: 13, fontWeight: FontWeight.w500),
                ),
              ),
              AppButton(
                label: 'Connect',
                isFullWidth: false,
                backgroundColor: AppColors.warning.withValues(alpha: 0.2),
                onPressed: () {
                  Navigator.push(context, MaterialPageRoute(builder: (_) => const SettingsPage()));
                },
              ),
            ],
          ),
        );
      },
    );
  }

  Widget _buildTabs(String lang) {
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      physics: const BouncingScrollPhysics(),
      child: Row(
        children: _tabs.entries.map((entry) {
          final isSelected = _currentTab == entry.key;
          return GestureDetector(
            onTap: () => _onTabSelected(entry.key),
            child: AnimatedContainer(
              duration: const Duration(milliseconds: 300),
              curve: Curves.easeOutCubic,
              margin: const EdgeInsets.only(right: AppSpacing.sm),
              padding: const EdgeInsets.symmetric(horizontal: AppSpacing.md, vertical: 10),
              decoration: BoxDecoration(
                color: isSelected ? AppColors.primary : AppColors.surfaceElevated,
                borderRadius: BorderRadius.circular(AppSpacing.radiusMd),
                boxShadow: isSelected 
                  ? [BoxShadow(color: AppColors.primary.withValues(alpha: 0.3), blurRadius: 15, offset: const Offset(0, 4))] 
                  : null,
              ),
              child: Text(
                L10n.get(entry.value, lang),
                style: TextStyle(
                  fontWeight: isSelected ? FontWeight.w700 : FontWeight.w500,
                  color: isSelected ? Colors.white : AppColors.textSecondary,
                ),
              ),
            ),
          );
        }).toList(),
      ),
    );
  }

  Widget _buildFileItem(FileRecord file, String lang) {
    final bool isSelected = _selectedIds.contains(file.id);
    
    return Padding(
      padding: const EdgeInsets.only(bottom: AppSpacing.md),
      child: GestureDetector(
        onLongPress: () {
          if (!_isSelecting) {
            _startSelecting(file.id!);
          }
        },
        onTap: () {
          if (_isSelecting) {
            _toggleSelection(file.id!);
          } else {
            _showFileOptions(file);
          }
        },
        child: AnimatedContainer(
          duration: const Duration(milliseconds: 200),
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(AppSpacing.radiusMd),
            border: Border.all(
              color: isSelected ? AppColors.primary : Colors.white.withValues(alpha: 0.1),
              width: isSelected ? 2 : 1,
            ),
          ),
          child: GlassCard(
            padding: EdgeInsets.zero,
            borderRadius: AppSpacing.radiusMd,
            borderOpacity: 0,
            child: ListTile(
              contentPadding: const EdgeInsets.symmetric(horizontal: AppSpacing.md, vertical: 6),
              leading: isSelected 
                ? Container(
                    padding: const EdgeInsets.all(AppSpacing.sm),
                    decoration: const BoxDecoration(color: AppColors.primary, shape: BoxShape.circle),
                    child: const Icon(Icons.check_rounded, color: Colors.white, size: 20),
                  )
                : Container(
                    padding: const EdgeInsets.all(AppSpacing.sm),
                    decoration: BoxDecoration(
                      color: AppColors.primary.withValues(alpha: 0.1),
                      borderRadius: BorderRadius.circular(AppSpacing.radiusSm),
                    ),
                    child: Icon(
                      _getFileIcon(file.path),
                      color: AppColors.primary,
                    ),
                  ),
              title: Text(
                file.path.split('/').last,
                style: const TextStyle(fontWeight: FontWeight.w600),
                overflow: TextOverflow.ellipsis,
              ),
              subtitle: Padding(
                padding: const EdgeInsets.only(top: 4),
                child: Text(
                  '${(file.size / 1024 / 1024).toStringAsFixed(2)} MB  •  ${file.lastUpdate.split('.')[0]}',
                  style: const TextStyle(fontSize: 12),
                ),
              ),
              trailing: _isSelecting ? null : IconButton(
                icon: const Icon(Icons.more_vert_rounded, size: 20, color: AppColors.textSecondary),
                onPressed: () => _showFileOptions(file),
              ),
            ),
          ),
        ),
      ),
    );
  }

  IconData _getFileIcon(String path) {
    final ext = path.split('.').last.toLowerCase();
    if (['mp4', 'mov', 'avi', 'mkv'].contains(ext)) return Icons.videocam_outlined;
    if (['jpg', 'jpeg', 'png', 'gif', 'webp'].contains(ext)) return Icons.image_outlined;
    if (['mp3', 'aac', 'flac', 'wav'].contains(ext)) return Icons.audio_file_outlined;
    if (['pdf'].contains(ext)) return Icons.picture_as_pdf_outlined;
    if (['zip', 'tar', 'gz', 'rar'].contains(ext)) return Icons.folder_zip_outlined;
    return Icons.insert_drive_file_outlined;
  }

  Widget _buildEmptyState() {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.cloud_off_rounded, size: 80, color: AppColors.surfaceElevated),
          const SizedBox(height: AppSpacing.lg),
          Text('No files found here', style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: AppSpacing.sm),
          const Text('Upload some content or check other tabs.', style: TextStyle(color: AppColors.textSecondary)),
        ],
      ),
    );
  }

  void _showFileOptions(FileRecord file) {
    final bool isTrash = _currentTab == 'trash';
    // Rehausser le menu pour éviter la barre de navigation OS
    final bottomPad = MediaQuery.of(context).viewPadding.bottom + AppSpacing.xl;

    showModalBottomSheet(      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      builder: (context) {
        return GlassCard(
          customBorderRadius: const BorderRadius.vertical(top: Radius.circular(AppSpacing.radiusLg)),
          padding: EdgeInsets.fromLTRB(0, AppSpacing.lg, 0, bottomPad),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Container(
                width: 40, height: 4,
                decoration: BoxDecoration(color: Colors.white24, borderRadius: BorderRadius.circular(2)),
              ),
              const SizedBox(height: AppSpacing.md),
              Text(file.path.split('/').last, style: const TextStyle(fontWeight: FontWeight.bold)),
              const SizedBox(height: AppSpacing.md),
              if (!isTrash) ...[
                ListTile(
                  leading: Icon(file.starred ? Icons.star_rounded : Icons.star_border_rounded, color: AppColors.primary),
                  title: Text(file.starred ? 'Remove from Starred' : 'Add to Starred'),
                  onTap: () {
                    Navigator.pop(context);
                    _db.saveFile(file.copyWith(starred: !file.starred));
                    _refreshFiles();
                  },
                ),
                ListTile(
                  leading: const Icon(Icons.download_rounded, color: AppColors.primary),
                  title: const Text('Download Offline'),
                  onTap: () async {
                    Navigator.pop(context);
                    
                    // Request absolute path permission and battery optimization ignore for background stability
                    if (Platform.isAndroid) {
                      if (!await Permission.manageExternalStorage.isGranted) {
                        await Permission.manageExternalStorage.request();
                      }
                      if (!await FlutterForegroundTask.isIgnoringBatteryOptimizations) {
                        await FlutterForegroundTask.requestIgnoreBatteryOptimization();
                      }
                    }

                    if (widget.onDownload != null) {
                      widget.onDownload!(file);
                    } else {
                      _nexus.downloadAndDecrypt(file, file.key);
                    }
                    if (!mounted) return;
                    WidgetsBinding.instance.addPostFrameCallback((_) {
                      if (!mounted) return;
                      ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Download started...')));
                    });
                  },
                ),
                ListTile(
                  leading: const Icon(Icons.delete_outline_rounded, color: AppColors.error),
                  title: const Text('Move to Trash'),
                  onTap: () {
                    Navigator.pop(context);
                    _db.softDelete(file.id!);
                    _refreshFiles();
                  },
                ),
              ] else ...[
                ListTile(
                  leading: const Icon(Icons.restore_rounded, color: AppColors.primary),
                  title: const Text('Restore File'),
                  onTap: () {
                    Navigator.pop(context);
                    _db.database.then((db) => db.update('files', {'deleted_at': null}, where: 'id = ?', whereArgs: [file.id]));
                    _refreshFiles();
                  },
                ),
                ListTile(
                  leading: const Icon(Icons.delete_forever_rounded, color: AppColors.error),
                  title: const Text('Delete Permanently'),
                  onTap: () {
                    Navigator.pop(context);
                    _db.database.then((db) => db.delete('files', where: 'id = ?', whereArgs: [file.id]));
                    _refreshFiles();
                  },
                ),
              ],
            ],
          ),
        );
      }
    );
  }
}
