import 'package:flutter/material.dart';
import 'dart:async';
import '../services/database_service.dart';
import '../services/settings_service.dart';
import '../utils/l10n.dart';

class TasksPage extends StatefulWidget {
  const TasksPage({super.key});

  @override
  State<TasksPage> createState() => _TasksPageState();
}

class _TasksPageState extends State<TasksPage> {
  final DatabaseService _db = DatabaseService();
  List<Map<String, dynamic>> _tasks = [];
  Timer? _timer;

  @override
  void initState() {
    super.initState();
    _fetchTasks();
    _timer = Timer.periodic(const Duration(seconds: 2), (_) => _fetchTasks());
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _fetchTasks() async {
    final tasks = await _db.getActiveTasks();
    if (mounted) setState(() => _tasks = tasks);
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final textColor = isDark ? Colors.white : const Color(0xFF1F2937);
    final cardColor = isDark ? Colors.white.withOpacity(0.05) : Colors.white.withOpacity(0.6);

    return Scaffold(
      extendBodyBehindAppBar: true,
      backgroundColor: Colors.transparent,
      appBar: AppBar(
        backgroundColor: Colors.transparent,
        elevation: 0,
        leading: IconButton(
          icon: Icon(Icons.arrow_back, color: textColor),
          onPressed: () => Navigator.pop(context),
        ),
      ),
      body: Container(
        decoration: BoxDecoration(
          gradient: RadialGradient(
            center: const Alignment(-1, -1),
            radius: 1.5,
            colors: isDark
                ? [const Color(0xFF1E1B4B), const Color(0xFF030712)]
                : [const Color(0xFFE0E7FF), const Color(0xFFF8FAFC)],
          ),
        ),
        child: ValueListenableBuilder<String>(
          valueListenable: SettingsService().language,
          builder: (context, lang, child) {
            return SafeArea(
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 24.0),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      L10n.get('activity', lang),
                      style: TextStyle(fontSize: 32, fontWeight: FontWeight.bold, color: textColor),
                    ),
                    const SizedBox(height: 24),
                    Expanded(
                      child: _tasks.isEmpty
                          ? Center(
                              child: Column(
                                mainAxisSize: MainAxisSize.min,
                                children: [
                                  Icon(Icons.check_circle_outline, size: 60, color: isDark ? Colors.white24 : Colors.black26),
                                  const SizedBox(height: 16),
                                  Text(L10n.get('no_active_tasks', lang), style: TextStyle(color: isDark ? Colors.white54 : Colors.black54, fontSize: 16)),
                                ],
                              ),
                            )
                          : ListView.builder(
                              itemCount: _tasks.length,
                              itemBuilder: (context, index) {
                                final t = _tasks[index];
                                final progress = (t['progress'] as num?)?.toDouble() ?? 0.0;
                                final status = t['status'] as String? ?? 'unknown';
                                final path = t['file_path'] as String? ?? 'Unknown File';
                                final name = path.split('/').last;

                                return _buildTaskItem(
                                  name,
                                  status,
                                  progress,
                                  Icons.sync,
                                  isDark,
                                  textColor,
                                  cardColor,
                                );
                              },
                            ),
                    ),
                  ],
                ),
              ),
            );
          },
        ),
      ),
    );
  }

  Widget _buildTaskItem(String title, String status, double progress, IconData icon, bool isDark, Color textColor, Color cardColor) {
    return Container(
      margin: const EdgeInsets.only(bottom: 16),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: cardColor,
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: isDark ? Colors.white.withOpacity(0.05) : Colors.black.withOpacity(0.05)),
        boxShadow: [
          if (!isDark)
            BoxShadow(color: Colors.black.withOpacity(0.03), blurRadius: 10, offset: const Offset(0, 4))
        ],
      ),
      child: Column(
        children: [
          Row(
            children: [
              Icon(icon, color: const Color(0xFF1A73E8)),
              const SizedBox(width: 16),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(title, style: TextStyle(fontWeight: FontWeight.bold, color: textColor)),
                    Text(status, style: TextStyle(color: isDark ? Colors.white54 : Colors.black54, fontSize: 13)),
                  ],
                ),
              ),
              Text('${(progress * 100).toInt()}%', style: TextStyle(color: textColor, fontWeight: FontWeight.w600)),
            ],
          ),
          const SizedBox(height: 12),
          LinearProgressIndicator(
            value: progress,
            backgroundColor: isDark ? Colors.white.withOpacity(0.05) : Colors.black.withOpacity(0.05),
            color: const Color(0xFF1A73E8),
            borderRadius: BorderRadius.circular(4),
          ),
        ],
      ),
    );
  }
}
