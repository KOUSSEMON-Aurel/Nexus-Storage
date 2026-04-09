import 'package:flutter/material.dart';
import 'dart:ui';
import 'package:flutter/services.dart' as services;
import '../services/database_service.dart';
import '../services/settings_service.dart';
import '../utils/l10n.dart';
import '../models/file_record.dart';

class FilesPage extends StatefulWidget {
  const FilesPage({super.key});

  @override
  State<FilesPage> createState() => _FilesPageState();
}

class _FilesPageState extends State<FilesPage> {
  final DatabaseService _db = DatabaseService();
  final SettingsService _settings = SettingsService();
  List<FileRecord> _files = [];
  String _currentTab = 'my-drive';

  final Map<String, String> _tabs = {
    'my-drive': 'my_drive',
    'recent': 'recent',
    'starred': 'starred',
    'trash': 'trash'
  };

  @override
  void initState() {
    super.initState();
    _refreshFiles();
  }

  Future<void> _refreshFiles() async {
    final files = await _db.listFiles(category: _currentTab);
    setState(() => _files = files);
  }

  void _onTabSelected(String tabKey) {
    setState(() {
      _currentTab = tabKey;
    });
    _refreshFiles();
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final textColor = isDark ? Colors.white : const Color(0xFF1F2937);



    return ValueListenableBuilder<String>(
      valueListenable: _settings.language,
      builder: (context, lang, child) {
        return Padding(
          padding: const EdgeInsets.only(top: 16.0, left: 16.0, right: 16.0),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const SizedBox(height: 16),
              // Title
              Padding(
                padding: const EdgeInsets.only(left: 8.0),
                child: Text(
                  L10n.get(_tabs[_currentTab] ?? 'nexus', lang),
                  style: TextStyle(
                    fontSize: 32,
                    fontWeight: FontWeight.w800,
                    color: textColor,
                    letterSpacing: -0.5,
                  ),
                ),
              ),
              const SizedBox(height: 16),
              
              // Horizontal Tabs
              SingleChildScrollView(
                scrollDirection: Axis.horizontal,
                physics: const BouncingScrollPhysics(),
                child: Row(
                  children: _tabs.entries.map((entry) {
                    final isSelected = _currentTab == entry.key;
                    return GestureDetector(
                      onTap: () => _onTabSelected(entry.key),
                      child: AnimatedContainer(
                        duration: const Duration(milliseconds: 200),
                        margin: const EdgeInsets.only(right: 12),
                        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                        decoration: BoxDecoration(
                          color: isSelected 
                            ? (isDark ? const Color(0xFF6366F1) : const Color(0xFF1A73E8))
                            : (isDark ? Colors.white.withOpacity(0.05) : Colors.black.withOpacity(0.05)),
                          borderRadius: BorderRadius.circular(20),
                          border: Border.all(
                            color: isSelected ? Colors.transparent : (isDark ? Colors.white.withOpacity(0.1) : Colors.black.withOpacity(0.05)),
                          ),
                        ),
                        child: Text(
                          L10n.get(entry.value, lang),
                          style: TextStyle(
                            fontWeight: FontWeight.w600,
                            fontSize: 14,
                            color: isSelected 
                              ? Colors.white 
                              : (isDark ? Colors.white70 : Colors.black87),
                          ),
                        ),
                      ),
                    );
                  }).toList(),
                ),
              ),
              const SizedBox(height: 24),

              // File List
              Expanded(
                child: _files.isEmpty
                    ? Center(
                        child: Column(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Icon(Icons.folder_open, size: 64, color: isDark ? Colors.white24 : Colors.black26),
                            const SizedBox(height: 16),
                            Text(
                              'No files found', // Can add L10n later if needed
                              style: TextStyle(color: isDark ? Colors.white54 : Colors.black54, fontSize: 18),
                            ),
                          ],
                        ),
                      )
                    : ListView.builder(
                        padding: EdgeInsets.only(bottom: 24 + MediaQuery.of(context).padding.bottom + 80), // Padding to clear FAB
                        itemCount: _files.length,
                        itemBuilder: (context, index) {
                          final file = _files[index];
                          return Container(
                            margin: const EdgeInsets.only(bottom: 12),
                            decoration: BoxDecoration(
                              color: isDark ? Colors.white.withOpacity(0.05) : Colors.white.withOpacity(0.8),
                              borderRadius: BorderRadius.circular(16),
                              border: Border.all(
                                color: isDark ? Colors.white.withOpacity(0.1) : Colors.black.withOpacity(0.05),
                              ),
                              boxShadow: [
                                if (!isDark)
                                  BoxShadow(
                                    color: Colors.black.withOpacity(0.03),
                                    blurRadius: 10,
                                    offset: const Offset(0, 4),
                                  )
                              ],
                            ),
                            child: ClipRRect(
                              borderRadius: BorderRadius.circular(16),
                              child: BackdropFilter(
                                filter: ImageFilter.blur(sigmaX: 10, sigmaY: 10),
                                child: ListTile(
                                  contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                                  leading: Container(
                                    padding: const EdgeInsets.all(10),
                                    decoration: BoxDecoration(
                                      color: (isDark ? Colors.white : const Color(0xFF1A73E8)).withOpacity(0.1),
                                      borderRadius: BorderRadius.circular(12),
                                    ),
                                    child: Icon(Icons.insert_drive_file, color: isDark ? Colors.white : const Color(0xFF1A73E8)),
                                  ),
                                  title: Text(
                                    file.path.split('/').last,
                                    style: TextStyle(fontWeight: FontWeight.w600, color: textColor),
                                  ),
                                  subtitle: Text(
                                    '${(file.size / 1024 / 1024).toStringAsFixed(2)} MB  •  ${DateTime.parse(file.lastUpdate).toLocal().toString().substring(0, 16)}',
                                    style: TextStyle(color: isDark ? Colors.white54 : Colors.black54, fontSize: 12),
                                  ),
                                  trailing: IconButton(
                                    icon: Icon(Icons.more_vert, color: isDark ? Colors.white54 : Colors.black54),
                                    onPressed: () {},
                                  ),
                                ),
                              ),
                            ),
                          );
                        },
                      ),
              ),
            ],
          ),
        );
      },
    );
  }
}
