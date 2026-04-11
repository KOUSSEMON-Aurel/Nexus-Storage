import 'package:flutter/material.dart';
import 'dart:async';
import 'package:nexus_mobile/services/database_service.dart';
import 'package:nexus_mobile/services/settings_service.dart';
import 'package:nexus_mobile/utils/l10n.dart';
import 'package:nexus_mobile/theme/app_colors.dart';
import 'package:nexus_mobile/theme/app_spacing.dart';
import 'package:nexus_mobile/ui/widgets/glass_card.dart';
import 'package:nexus_mobile/ui/widgets/app_button.dart';

class TasksPage extends StatefulWidget {
  const TasksPage({super.key});

  @override
  State<TasksPage> createState() => _TasksPageState();
}

class _TasksPageState extends State<TasksPage> {
  final DatabaseService _db = DatabaseService();
  List<Map<String, dynamic>> _tasks = [];
  Timer? _timer;
  StreamSubscription<void>? _dbSubscription;

  @override
  void initState() {
    super.initState();
    _fetchTasks();
    _timer = Timer.periodic(const Duration(seconds: 2), (_) => _fetchTasks());
    _dbSubscription = _db.onChange.listen((_) => _fetchTasks());
  }

  @override
  void dispose() {
    _timer?.cancel();
    _dbSubscription?.cancel();
    super.dispose();
  }

  Future<void> _fetchTasks() async {
    final tasks = await _db.getActiveTasks();
    if (mounted) setState(() => _tasks = tasks);
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;

    return Scaffold(
      backgroundColor: AppColors.getBackground(context),
      body: Stack(
        children: [
          // Background Blobs (Design Rule)
          Positioned(
            top: -100,
            left: -100,
            child: Container(
              width: 300,
              height: 300,
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                color: AppColors.primary.withOpacity(isDark ? 0.15 : 0.08),
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
                color: AppColors.secondary.withOpacity(isDark ? 0.1 : 0.05),
              ),
            ),
          ),
          
          ValueListenableBuilder<String>(
            valueListenable: SettingsService().language,
            builder: (context, lang, child) {
              return SafeArea(
                child: CustomScrollView(
                  physics: const BouncingScrollPhysics(),
                  slivers: [
                    SliverAppBar(
                      floating: true,
                      pinned: false,
                      backgroundColor: Colors.transparent,
                      elevation: 0,
                      leading: IconButton(
                        icon: const Icon(Icons.arrow_back_rounded, color: AppColors.textPrimary),
                        onPressed: () => Navigator.pop(context),
                      ),
                      actions: [
                        if (_tasks.isNotEmpty)
                          IconButton(
                            icon: const Icon(Icons.delete_sweep_outlined, color: AppColors.textSecondary),
                            onPressed: () => _showClearConfirmation(context, lang),
                          ),
                      ],
                    ),
                    
                    SliverPadding(
                      padding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg),
                      sliver: SliverList(
                        delegate: SliverChildListDelegate([
                          Text(
                            L10n.get('activity', lang).toUpperCase(),
                            style: Theme.of(context).textTheme.labelLarge,
                          ),
                          const SizedBox(height: AppSpacing.sm),
                          Text(
                            'Transfers',
                            style: Theme.of(context).textTheme.displayLarge,
                          ),
                          const SizedBox(height: AppSpacing.xl),
                        ]),
                      ),
                    ),

                    if (_tasks.isEmpty)
                      SliverFillRemaining(
                        hasScrollBody: false,
                        child: _buildEmptyState(lang),
                      )
                    else
                      SliverPadding(
                        padding: const EdgeInsets.symmetric(horizontal: AppSpacing.lg),
                        sliver: SliverList(
                          delegate: SliverChildBuilderDelegate(
                            (context, index) {
                              final t = _tasks[index];
                              final progress = (t['progress'] as num?)?.toDouble() ?? 0.0;
                              final status = t['status'] as String? ?? 'unknown';
                              final path = t['file_path'] as String? ?? 'Unknown File';
                              final name = path.split('/').last;
                              
                              final isCompleted = status == 'completed';
                              final isFailed = status.toLowerCase().contains('failed') || status.toLowerCase().contains('error');
                              
                              // Staggered Animation
                              return TweenAnimationBuilder<double>(
                                duration: Duration(milliseconds: 400 + (index * 100)),
                                tween: Tween(begin: 0.0, end: 1.0),
                                curve: Curves.easeOutCubic,
                                builder: (context, value, child) {
                                  return Transform.translate(
                                    offset: Offset(0, 20 * (1 - value)),
                                    child: Opacity(opacity: value, child: child),
                                  );
                                },
                                child: Dismissible(
                                  key: Key(t['id'].toString()),
                                  direction: DismissDirection.endToStart,
                                  background: Container(
                                    alignment: Alignment.centerRight,
                                    padding: const EdgeInsets.only(right: AppSpacing.lg),
                                    margin: const EdgeInsets.only(bottom: AppSpacing.md),
                                    decoration: BoxDecoration(
                                      color: AppColors.error.withOpacity(0.2),
                                      borderRadius: BorderRadius.circular(AppSpacing.radiusMd),
                                    ),
                                    child: const Icon(Icons.delete_outline_rounded, color: AppColors.error),
                                  ),
                                  onDismissed: (_) async {
                                    await _db.deleteTask(t['id']);
                                    _fetchTasks();
                                  },
                                  child: _buildTaskItem(name, status, progress, isCompleted, isFailed),
                                ),
                              );
                            },
                            childCount: _tasks.length,
                          ),
                        ),
                      ),
                  ],
                ),
              );
            },
          ),
        ],
      ),
    );
  }

  void _showClearConfirmation(BuildContext context, String lang) {
    showDialog(
      context: context,
      builder: (c) => AlertDialog(
        title: const Text('Clear History?'),
        content: const Text('This will remove all completed and failed tasks from your activity history.'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(c), child: const Text('Cancel')),
          AppButton(
            label: 'Clear All',
            isFullWidth: false,
            backgroundColor: AppColors.error.withOpacity(0.8),
            onPressed: () async {
              Navigator.pop(c);
              await _db.clearCompletedTasks();
              _fetchTasks();
            },
          ),
        ],
      ),
    );
  }

  Widget _buildEmptyState(String lang) {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.check_circle_outline_rounded, size: 80, color: AppColors.surfaceElevated),
          const SizedBox(height: AppSpacing.lg),
          Text(L10n.get('no_active_tasks', lang), style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: AppSpacing.sm),
          const Text('All your transfers will appear here', style: TextStyle(color: AppColors.textSecondary)),
        ],
      ),
    );
  }

  Widget _buildTaskItem(String title, String status, double progress, bool isCompleted, bool isFailed) {
    final iconData = isCompleted ? Icons.check_circle_rounded : (isFailed ? Icons.error_rounded : Icons.sync_rounded);
    final iconColor = isCompleted ? AppColors.success : (isFailed ? AppColors.error : AppColors.primary);

    return Padding(
      padding: const EdgeInsets.only(bottom: AppSpacing.md),
      child: GlassCard(
        padding: const EdgeInsets.all(AppSpacing.md),
        borderRadius: AppSpacing.radiusMd,
        child: Column(
          children: [
            Row(
              children: [
                Icon(iconData, color: iconColor, size: 24),
                const SizedBox(width: AppSpacing.md),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(title, style: const TextStyle(fontWeight: FontWeight.bold), overflow: TextOverflow.ellipsis),
                      Text(status.toUpperCase(), style: TextStyle(color: iconColor, fontSize: 11, fontWeight: FontWeight.bold, letterSpacing: 0.5)),
                    ],
                  ),
                ),
                Text('${(progress * 100).toInt()}%', style: const TextStyle(fontWeight: FontWeight.w700)),
              ],
            ),
            const SizedBox(height: AppSpacing.md),
            ClipRRect(
              borderRadius: BorderRadius.circular(AppSpacing.radiusSm),
              child: LinearProgressIndicator(
                value: progress,
                minHeight: 6,
                backgroundColor: Colors.white.withOpacity(0.05),
                color: iconColor,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
