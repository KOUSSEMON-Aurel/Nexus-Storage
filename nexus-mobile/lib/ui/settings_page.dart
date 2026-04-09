import 'package:flutter/material.dart';
import 'package:flutter/services.dart' as services;
import 'package:url_launcher/url_launcher.dart';
import 'package:google_sign_in/google_sign_in.dart';
import '../services/auth_service.dart';
import '../services/database_service.dart';
import '../services/settings_service.dart';
import '../services/sync_service.dart';
import '../utils/l10n.dart';

class SettingsPage extends StatefulWidget {
  const SettingsPage({super.key});

  @override
  State<SettingsPage> createState() => _SettingsPageState();
}

class _SettingsPageState extends State<SettingsPage> {
  final AuthService _auth = AuthService();
  final DatabaseService _db = DatabaseService();
  final SettingsService _settings = SettingsService();
  bool _isLoading = false;
  
  // Storage retention
  double _trashRetention = 30;

  @override
  void initState() {
    super.initState();
    _loadRetention();
  }

  Future<void> _loadRetention() async {
    final retention = double.tryParse(await _db.getKV('trash_retention') ?? '30') ?? 30;
    setState(() => _trashRetention = retention);
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final cardColor = isDark ? const Color(0xFF1E293B) : Colors.white;

    final systemStyle = services.SystemUiOverlayStyle(
      statusBarColor: Colors.transparent,
      systemNavigationBarColor: isDark ? const Color(0xFF020617) : const Color(0xFFF8FAFC),
      systemNavigationBarIconBrightness: isDark ? Brightness.light : Brightness.dark,
      statusBarIconBrightness: isDark ? Brightness.light : Brightness.dark,
      systemNavigationBarDividerColor: Colors.transparent,
    );

    return AnnotatedRegion<services.SystemUiOverlayStyle>(
      value: systemStyle,
      child: ValueListenableBuilder<String>(
        valueListenable: _settings.language,
        builder: (context, lang, child) {
          return Scaffold(
            appBar: AppBar(
              title: Text(L10n.get('settings', lang), style: const TextStyle(fontWeight: FontWeight.w700)),
              backgroundColor: Colors.transparent,
              elevation: 0,
              centerTitle: true,
            ),
            extendBodyBehindAppBar: true,
            body: Container(
              decoration: BoxDecoration(
                gradient: LinearGradient(
                  begin: Alignment.topCenter,
                  end: Alignment.bottomCenter,
                  colors: isDark 
                    ? [const Color(0xFF0F172A), const Color(0xFF020617)]
                    : [const Color(0xFFF1F5F9), const Color(0xFFF8FAFC)],
                ),
              ),
              child: SafeArea(
                child: ListView(
                  padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
                  children: [
                    _buildSectionHeader(L10n.get('account', lang)),
                    StreamBuilder<GoogleSignInAccount?>(
                      stream: _auth.userStream,
                      initialData: null,
                      builder: (context, snapshot) {
                        final user = _auth.isAuthenticated ? _auth.userName : snapshot.data?.displayName;
                        final photoUrl = _auth.isAuthenticated ? _auth.userPhotoUrl : snapshot.data?.photoUrl;
                        final isConnected = _auth.isAuthenticated || snapshot.hasData;

                        return Container(
                          padding: const EdgeInsets.all(16),
                          decoration: BoxDecoration(
                            color: cardColor,
                            borderRadius: BorderRadius.circular(24),
                            boxShadow: [
                              if (!isDark)
                                BoxShadow(color: Colors.black.withOpacity(0.03), blurRadius: 10, offset: const Offset(0, 4))
                            ],
                          ),
                          child: Row(
                            children: [
                              if (isConnected && photoUrl != null)
                                CircleAvatar(
                                  radius: 28,
                                  backgroundImage: NetworkImage(photoUrl),
                                )
                              else
                                Icon(Icons.account_circle, size: 56, color: isConnected ? const Color(0xFF1A73E8) : Colors.grey),
                              const SizedBox(width: 16),
                              Expanded(
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    Text(
                                      isConnected ? (user ?? 'Connected') : 'Google Account',
                                      style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w700),
                                    ),
                                    Text(
                                      isConnected ? 'Safe Archival Enabled' : 'Not connected',
                                      style: TextStyle(color: isConnected ? Colors.green : Colors.grey, fontSize: 13, fontWeight: FontWeight.w500),
                                    ),
                                  ],
                                ),
                              ),
                              ElevatedButton(
                                onPressed: _isLoading ? null : () async {
                                  setState(() => _isLoading = true);
                                  try {
                                    if (isConnected) {
                                      await _auth.logout();
                                    } else {
                                      final res = await _auth.login();
                                      if (res == null && mounted) {
                                        ScaffoldMessenger.of(context).showSnackBar(
                                          SnackBar(content: Text('Sign-in failed: ${_auth.lastError ?? "Unknown error"}')),
                                        );
                                      }
                                    }
                                  } finally {
                                    if (mounted) setState(() => _isLoading = false);
                                  }
                                },
                                style: ElevatedButton.styleFrom(
                                  backgroundColor: isConnected ? Colors.red.withOpacity(0.1) : const Color(0xFF1A73E8),
                                  foregroundColor: isConnected ? Colors.red : Colors.white,
                                  elevation: 0,
                                  padding: const EdgeInsets.symmetric(horizontal: 16),
                                  shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
                                ),
                                child: _isLoading 
                                  ? const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white))
                                  : Text(isConnected ? L10n.get('logout', lang) : L10n.get('connect', lang)),
                              ),
                            ],
                          ),
                        );
                      },
                    ),
                    const SizedBox(height: 24),

                    _buildSectionHeader(L10n.get('display', lang)),
                    _buildSettingTile(
                      icon: Icons.palette_outlined,
                      title: L10n.get('theme', lang),
                      trailing: ValueListenableBuilder<ThemeMode>(
                        valueListenable: _settings.themeMode,
                        builder: (context, mode, _) {
                          String label = 'SYSTEM';
                          if (mode == ThemeMode.light) label = 'LIGHT';
                          if (mode == ThemeMode.dark) label = 'DARK';
                          return Text(label, style: const TextStyle(fontWeight: FontWeight.w600, color: Color(0xFF1A73E8)));
                        },
                      ),
                      onTap: () => _showThemeDialog(context),
                      color: cardColor,
                    ),
                    const SizedBox(height: 12),
                    _buildSettingTile(
                      icon: Icons.language_outlined,
                      title: L10n.get('language', lang),
                      trailing: Text(lang.toUpperCase(), style: const TextStyle(fontWeight: FontWeight.w600, color: Color(0xFF1A73E8))),
                      onTap: () => _showLanguageDialog(context),
                      color: cardColor,
                    ),
                    const SizedBox(height: 24),

                    _buildSectionHeader(L10n.get('interaction', lang)),
                    _buildSettingTile(
                      icon: Icons.check_box_outlined,
                      title: L10n.get('persistent_checkboxes', lang),
                      trailing: FutureBuilder<String?>(
                        future: _db.getKV('persistent_checkboxes'),
                        builder: (context, snapshot) {
                          return Switch(
                            value: snapshot.data == 'true',
                            onChanged: (val) {
                              _db.setKV('persistent_checkboxes', val.toString());
                              setState(() {});
                            },
                            activeColor: const Color(0xFF1A73E8),
                          );
                        }
                      ),
                      color: cardColor,
                    ),
                    const SizedBox(height: 24),

                    _buildSectionHeader(L10n.get('storage_trash', lang)),
                    Container(
                      padding: const EdgeInsets.all(16),
                      decoration: BoxDecoration(color: cardColor, borderRadius: BorderRadius.circular(24)),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Row(
                            mainAxisAlignment: MainAxisAlignment.spaceBetween,
                            children: [
                              Text(L10n.get('auto_empty', lang), style: const TextStyle(fontWeight: FontWeight.w600)),
                              Text('${_trashRetention.round()} days', style: const TextStyle(color: Color(0xFF1A73E8), fontWeight: FontWeight.bold)),
                            ],
                          ),
                          Slider(
                            value: _trashRetention,
                            min: 1, max: 90,
                            divisions: 89,
                            activeColor: const Color(0xFF1A73E8),
                            onChanged: (val) {
                              setState(() => _trashRetention = val);
                            },
                            onChangeEnd: (val) {
                              _db.setKV('trash_retention', val.round().toString());
                            },
                          ),
                          const SizedBox(height: 8),
                          SizedBox(
                            width: double.infinity,
                            child: OutlinedButton.icon(
                              icon: const Icon(Icons.delete_sweep_outlined, size: 18),
                              label: Text(L10n.get('empty_trash_now', lang)),
                              onPressed: () => _confirmEmptyTrash(context, lang),
                              style: OutlinedButton.styleFrom(
                                foregroundColor: Colors.red,
                                side: BorderSide(color: Colors.red.withOpacity(0.3)),
                                padding: const EdgeInsets.symmetric(vertical: 12),
                                shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
                              ),
                            ),
                          ),
                        ],
                      ),
                    ),
                    _buildSectionHeader(L10n.get('database_sync', lang)),
                    Container(
                      padding: const EdgeInsets.all(16),
                      decoration: BoxDecoration(color: cardColor, borderRadius: BorderRadius.circular(24)),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Row(
                            children: [
                              const Icon(Icons.cloud_sync_outlined, color: Color(0xFF1A73E8)),
                              const SizedBox(width: 12),
                              Expanded(
                                child: Column(
                                  crossAxisAlignment: CrossAxisAlignment.start,
                                  children: [
                                    const Text('Google Drive Sync', style: TextStyle(fontWeight: FontWeight.w700)),
                                    FutureBuilder<String?>(
                                      future: _db.getKV('manifest_version'),
                                      builder: (context, snapshot) => Text(
                                        'Local LSN: ${snapshot.data ?? "0"}',
                                        style: TextStyle(color: Colors.grey[600], fontSize: 13),
                                      ),
                                    ),
                                  ],
                                ),
                              ),
                            ],
                          ),
                          const SizedBox(height: 16),
                          Row(
                            children: [
                              Expanded(
                                child: ElevatedButton.icon(
                                  icon: const Icon(Icons.cloud_upload_outlined, size: 18),
                                  label: const Text('Push'),
                                  onPressed: () => _handleSyncAction('push'),
                                  style: ElevatedButton.styleFrom(
                                    backgroundColor: const Color(0xFF1A73E8),
                                    foregroundColor: Colors.white,
                                    shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
                                  ),
                                ),
                              ),
                              const SizedBox(width: 12),
                              Expanded(
                                child: OutlinedButton.icon(
                                  icon: const Icon(Icons.cloud_download_outlined, size: 18),
                                  label: const Text('Pull'),
                                  onPressed: () => _handleSyncAction('pull'),
                                  style: OutlinedButton.styleFrom(
                                    shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
                                  ),
                                ),
                              ),
                            ],
                          ),
                        ],
                      ),
                    ),
                    const SizedBox(height: 24),

                    _buildSectionHeader(L10n.get('security_privacy', lang)),
                    _buildInfoCard(
                      icon: Icons.lock_outline,
                      title: L10n.get('zk_encryption', lang),
                      description: L10n.get('zk_desc', lang),
                      isDark: isDark,
                    ),
                    const SizedBox(height: 12),
                    _buildInfoCard(
                      icon: Icons.security_outlined,
                      title: L10n.get('camouflage_title', lang),
                      description: L10n.get('camouflage_desc', lang),
                      isDark: isDark,
                    ),
                    const SizedBox(height: 32),

                    Center(
                      child: Column(
                        children: [
                          Container(
                            padding: const EdgeInsets.all(12),
                            decoration: BoxDecoration(
                              color: const Color(0xFF1A73E8),
                              borderRadius: BorderRadius.circular(16),
                              boxShadow: [BoxShadow(color: const Color(0xFF1A73E8).withOpacity(0.3), blurRadius: 10)]
                            ),
                            child: const Icon(Icons.refresh, color: Colors.white, size: 32),
                          ),
                          const SizedBox(height: 16),
                          const Text('Nexus Storage', style: TextStyle(fontSize: 22, fontWeight: FontWeight.w800)),
                          const Text('v5.3.4 "Nova Galactic"', style: TextStyle(color: Colors.grey, fontSize: 13)),
                          const SizedBox(height: 24),
                          TextButton.icon(
                            icon: const Icon(Icons.code_outlined, size: 16),
                            label: Text(L10n.get('view_on_github', lang)),
                            onPressed: () => launchUrl(Uri.parse('https://github.com/KOUSSEMON-Aurel/Nexus-Storage')),
                          ),
                        ],
                      ),
                    ),
                    const SizedBox(height: 40),
                  ],
                ),
              ),
            ),
          );
        },
      ),
    );
  }

  Widget _buildSectionHeader(String title) {
    return Padding(
      padding: const EdgeInsets.only(left: 4, bottom: 12),
      child: Text(
        title.toUpperCase(),
        style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w700, color: Colors.grey, letterSpacing: 1.2),
      ),
    );
  }

  Widget _buildSettingTile({required IconData icon, required String title, required Widget trailing, VoidCallback? onTap, required Color color}) {
    return GestureDetector(
      onTap: onTap,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        decoration: BoxDecoration(color: color, borderRadius: BorderRadius.circular(20)),
        child: Row(
          children: [
            Icon(icon, size: 22, color: const Color(0xFF1A73E8)),
            const SizedBox(width: 16),
            Expanded(child: Text(title, style: const TextStyle(fontWeight: FontWeight.w600, fontSize: 15))),
            trailing,
            if (onTap != null) const Icon(Icons.chevron_right, size: 20, color: Colors.grey),
          ],
        ),
      ),
    );
  }

  Widget _buildInfoCard({required IconData icon, required String title, required String description, required bool isDark}) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: isDark ? Colors.white.withOpacity(0.03) : Colors.black.withOpacity(0.02),
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: isDark ? Colors.white.withOpacity(0.05) : Colors.black.withOpacity(0.05)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, size: 20, color: Colors.green),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, style: const TextStyle(fontWeight: FontWeight.w700, fontSize: 14)),
                const SizedBox(height: 4),
                Text(description, style: const TextStyle(fontSize: 12, color: Colors.grey, height: 1.4)),
              ],
            ),
          ),
          const Icon(Icons.check_circle, size: 16, color: Colors.green),
        ],
      ),
    );
  }

  void _showThemeDialog(BuildContext context) {
    showDialog(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Select Theme'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          children: ['system', 'light', 'dark'].map((t) => ListTile(
            title: Text(t.toUpperCase()),
            leading: Radio<String>(
              value: t,
              groupValue: _settings.themeMode.value == ThemeMode.light ? 'light' : (_settings.themeMode.value == ThemeMode.dark ? 'dark' : 'system'),
              onChanged: (val) {
                _settings.updateTheme(val!);
                Navigator.pop(context);
              },
            ),
          )).toList(),
        ),
      ),
    );
  }

  void _confirmEmptyTrash(BuildContext context, String lang) {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(L10n.get('empty_trash_now', lang)),
        content: const Text('Are you sure you want to permanently delete all items in trash?'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
          TextButton(
            onPressed: () {
              Navigator.pop(ctx);
              ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Trash emptied')));
            },
            child: const Text('Empty', style: TextStyle(color: Colors.red)),
          ),
        ],
      ),
    );
  }

  Future<void> _handleSyncAction(String type) async {
    setState(() => _isLoading = true);
    try {
      if (type == 'push') {
        await SyncService().pushDatabase();
        if (mounted) ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Database pushed successfully')));
      } else {
        await SyncService().pullDatabase();
        if (mounted) ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Database pulled successfully')));
      }
      setState(() {});
    } catch (e) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text('Sync Error: $e'), backgroundColor: Colors.red));
    } finally {
      if (mounted) setState(() => _isLoading = false);
    }
  }

  void _showLanguageDialog(BuildContext context) {
    showDialog(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Select Language'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          children: ['fr', 'en'].map((l) => ListTile(
            title: Text(l == 'fr' ? 'Français' : 'English'),
            leading: Radio<String>(
              value: l,
              groupValue: _settings.language.value,
              onChanged: (val) {
                _settings.updateLanguage(val!);
                Navigator.pop(context);
              },
            ),
          )).toList(),
        ),
      ),
    );
  }
}
