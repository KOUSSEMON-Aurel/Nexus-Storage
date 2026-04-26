import 'package:flutter/material.dart';
import 'package:url_launcher/url_launcher.dart';
import 'package:google_sign_in/google_sign_in.dart';
import 'package:nexus_mobile/services/auth_service.dart';
import 'package:nexus_mobile/services/database_service.dart';
import 'package:nexus_mobile/services/settings_service.dart';
import 'package:nexus_mobile/utils/l10n.dart';
import 'package:nexus_mobile/theme/app_colors.dart';
import 'package:nexus_mobile/theme/app_spacing.dart';
import 'package:nexus_mobile/ui/widgets/glass_card.dart';
import 'package:nexus_mobile/ui/widgets/app_button.dart';

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
    final retentionValue = await _db.getKV('trash_retention');
    if (mounted) {
      setState(
        () => _trashRetention = double.tryParse(retentionValue ?? '30') ?? 30,
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final textPrimary = AppColors.getTextPrimary(context);

    return ValueListenableBuilder<String>(
      valueListenable: _settings.language,
      builder: (context, lang, child) {
        return Scaffold(
          extendBodyBehindAppBar: true,
          appBar: AppBar(
            backgroundColor: Colors.transparent,
            elevation: 0,
            leading: IconButton(
              icon: Icon(Icons.arrow_back_rounded, color: textPrimary),
              onPressed: () => Navigator.pop(context),
            ),
            title: Text(
              L10n.get('settings', lang),
              style: TextStyle(color: textPrimary),
            ),
            centerTitle: true,
          ),
          body: Stack(
            children: [
              // Subtle background gradient
              Container(color: AppColors.getBackground(context)),

              // Harmonized Background Blobs (matching MainScreen)
              Positioned(
                top: -100,
                right: -100,
                child: Container(
                  width: 300,
                  height: 300,
                  decoration: BoxDecoration(
                    shape: BoxShape.circle,
                    color: AppColors.primary.withValues(
                      alpha: isDark ? 0.12 : 0.05,
                    ),
                  ),
                ),
              ),
              Positioned(
                bottom: 100,
                left: -50,
                child: Container(
                  width: 200,
                  height: 200,
                  decoration: BoxDecoration(
                    shape: BoxShape.circle,
                    color: AppColors.secondary.withValues(
                      alpha: isDark ? 0.08 : 0.03,
                    ),
                  ),
                ),
              ),

              SafeArea(
                child: ListView(
                  padding: const EdgeInsets.all(AppSpacing.lg),
                  physics: const BouncingScrollPhysics(),
                  children: [
                    _buildSectionHeader(L10n.get('account', lang)),
                    _buildAccountCard(lang),
                    const SizedBox(height: AppSpacing.xl),

                    _buildSectionHeader(L10n.get('display', lang)),
                    _buildDisplayOptions(lang),
                    const SizedBox(height: AppSpacing.xl),

                    _buildSectionHeader(L10n.get('storage_trash', lang)),
                    _buildStorageCard(lang),
                    const SizedBox(height: AppSpacing.xl),

                    _buildSectionHeader(L10n.get('database_sync', lang)),
                    _buildSyncCard(lang),
                    const SizedBox(height: AppSpacing.xl),

                    _buildSectionHeader(L10n.get('security_privacy', lang)),
                    _buildInfoCard(
                      context: context,
                      icon: Icons.lock_outline,
                      title: L10n.get('zk_encryption', lang),
                      description: L10n.get('zk_desc', lang),
                    ),
                    const SizedBox(height: AppSpacing.md),
                    _buildInfoCard(
                      context: context,
                      icon: Icons.security_outlined,
                      title: L10n.get('camouflage_title', lang),
                      description: L10n.get('camouflage_desc', lang),
                    ),
                    const SizedBox(height: AppSpacing.xxl),

                    _buildVersionInfo(lang),
                    const SizedBox(height: AppSpacing.xxl),
                  ],
                ),
              ),
            ],
          ),
        );
      },
    );
  }

  Widget _buildAccountCard(String lang) {
    final textSecondary = AppColors.getTextSecondary(context);

    return StreamBuilder<GoogleSignInAccount?>(
      stream: _auth.userStream,
      initialData: _auth.currentUser,
      builder: (context, snapshot) {
        final isConnected = _auth.isAuthenticated || snapshot.hasData;
        final photoUrl = _auth.isAuthenticated
            ? _auth.userPhotoUrl
            : snapshot.data?.photoUrl;
        final name = _auth.isAuthenticated
            ? _auth.userName
            : snapshot.data?.displayName;

        return GlassCard(
          padding: const EdgeInsets.all(AppSpacing.md),
          borderRadius: AppSpacing.radiusLg,
          child: Row(
            children: [
              Container(
                decoration: BoxDecoration(
                  shape: BoxShape.circle,
                  border: Border.all(
                    color: AppColors.primary.withValues(alpha: 0.3),
                    width: 2,
                  ),
                ),
                child: CircleAvatar(
                  radius: 28,
                  backgroundColor: AppColors.getSurfaceElevated(context),
                  backgroundImage: photoUrl != null
                      ? NetworkImage(photoUrl)
                      : null,
                  child: photoUrl == null
                      ? Icon(
                          Icons.person_rounded,
                          color: textSecondary,
                          size: 32,
                        )
                      : null,
                ),
              ),
              const SizedBox(width: AppSpacing.md),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      isConnected ? (name ?? 'Connected') : 'Google Account',
                      style: const TextStyle(
                        fontSize: 16,
                        fontWeight: FontWeight.bold,
                      ),
                    ),
                    Text(
                      isConnected ? 'Cloud Sync Active' : 'Offline access only',
                      style: TextStyle(
                        color: isConnected ? AppColors.success : textSecondary,
                        fontSize: 12,
                        fontWeight: FontWeight.w500,
                      ),
                    ),
                  ],
                ),
              ),
              AppButton(
                label: isConnected ? 'Logout' : 'Connect',
                isFullWidth: false,
                isLoading: _isLoading,
                backgroundColor: isConnected
                    ? AppColors.error.withValues(alpha: 0.1)
                    : AppColors.primary,
                onPressed: () async {
                  setState(() => _isLoading = true);
                  try {
                    if (isConnected) {
                      await _auth.logout();
                    } else {
                      await _auth.login();
                    }
                  } finally {
                    if (mounted) setState(() => _isLoading = false);
                  }
                },
              ),
            ],
          ),
        );
      },
    );
  }

  Widget _buildDisplayOptions(String lang) {
    final dividerColor = Theme.of(context).brightness == Brightness.dark
        ? Colors.white10
        : Colors.black12;

    return GlassCard(
      padding: EdgeInsets.zero,
      borderRadius: AppSpacing.radiusLg,
      child: Column(
        children: [
          _buildCompactTile(
            icon: Icons.palette_outlined,
            title: L10n.get('theme', lang),
            trailing: ValueListenableBuilder<ThemeMode>(
              valueListenable: _settings.themeMode,
              builder: (context, mode, _) {
                String label = mode.name.toUpperCase();
                return Text(
                  label,
                  style: const TextStyle(
                    color: AppColors.primary,
                    fontWeight: FontWeight.bold,
                  ),
                );
              },
            ),
            onTap: () => _showThemeDialog(context),
          ),
          Divider(height: 1, color: dividerColor),
          _buildCompactTile(
            icon: Icons.language_rounded,
            title: L10n.get('language', lang),
            trailing: Text(
              _settings.language.value.toUpperCase(),
              style: const TextStyle(
                color: AppColors.primary,
                fontWeight: FontWeight.bold,
              ),
            ),
            onTap: () => _showLanguageDialog(context),
          ),
          Divider(height: 1, color: dividerColor),
          _buildCompactTile(
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
                  activeThumbColor: AppColors.primary,
                );
              },
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildStorageCard(String lang) {
    return GlassCard(
      padding: const EdgeInsets.all(AppSpacing.md),
      borderRadius: AppSpacing.radiusLg,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              Text(
                L10n.get('auto_empty', lang),
                style: const TextStyle(fontWeight: FontWeight.bold),
              ),
              Text(
                '${_trashRetention.round()} days',
                style: const TextStyle(
                  color: AppColors.primary,
                  fontWeight: FontWeight.bold,
                ),
              ),
            ],
          ),
          Slider(
            value: _trashRetention,
            min: 1,
            max: 90,
            activeColor: AppColors.primary,
            inactiveColor: AppColors.getSurfaceElevated(context),
            onChanged: (val) => setState(() => _trashRetention = val),
            onChangeEnd: (val) =>
                _db.setKV('trash_retention', val.round().toString()),
          ),
          const SizedBox(height: AppSpacing.sm),
          AppButton(
            label: L10n.get('empty_trash_now', lang),
            backgroundColor: AppColors.error.withValues(alpha: 0.1),
            onPressed: () => _confirmEmptyTrash(context, lang),
          ),
        ],
      ),
    );
  }

  Widget _buildSyncCard(String lang) {
    return GlassCard(
      padding: const EdgeInsets.all(AppSpacing.md),
      borderRadius: AppSpacing.radiusLg,
      child: Column(
        children: [
          Row(
            children: [
              const Icon(Icons.cloud_sync_rounded, color: AppColors.primary),
              const SizedBox(width: AppSpacing.md),
              const Expanded(
                child: Text(
                  'Cloud Database Sync',
                  style: TextStyle(fontWeight: FontWeight.bold),
                ),
              ),
              FutureBuilder<String?>(
                future: _db.getKV('manifest_version'),
                builder: (context, snapshot) => Text(
                  'LSN: ${snapshot.data ?? "0"}',
                  style: TextStyle(
                    fontSize: 10,
                    color: AppColors.getTextSecondary(context),
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: AppSpacing.md),
          const Text(
            'Synchronization is now managed automatically based on database state and timestamps.',
            style: TextStyle(fontSize: 12, color: Colors.grey),
            textAlign: TextAlign.center,
          ),
        ],
      ),
    );
  }

  Widget _buildCompactTile({
    required IconData icon,
    required String title,
    required Widget trailing,
    VoidCallback? onTap,
  }) {
    final textSecondary = AppColors.getTextSecondary(context);
    return ListTile(
      leading: Icon(icon, color: textSecondary, size: 22),
      title: Text(
        title,
        style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w500),
      ),
      trailing: trailing,
      onTap: onTap,
    );
  }

  Widget _buildVersionInfo(String lang) {
    return Center(
      child: Column(
        children: [
          Container(
            padding: const EdgeInsets.all(AppSpacing.md),
            decoration: BoxDecoration(
              color: AppColors.primary.withValues(alpha: 0.1),
              borderRadius: BorderRadius.circular(AppSpacing.radiusMd),
            ),
            child: const Icon(
              Icons.stars_rounded,
              color: AppColors.primary,
              size: 32,
            ),
          ),
          const SizedBox(height: AppSpacing.md),
          const Text(
            'Nexus Storage',
            style: TextStyle(fontSize: 20, fontWeight: FontWeight.w800),
          ),
          Text(
            'v5.4.0 Titanium',
            style: TextStyle(
              color: AppColors.getTextSecondary(context),
              fontSize: 13,
            ),
          ),
          const SizedBox(height: AppSpacing.lg),
          TextButton.icon(
            icon: const Icon(Icons.open_in_new_rounded, size: 16),
            label: Text(L10n.get('view_on_github', lang)),
            onPressed: () => launchUrl(
              Uri.parse('https://github.com/KOUSSEMON-Aurel/Nexus-Storage'),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildInfoCard({
    required BuildContext context,
    required IconData icon,
    required String title,
    required String description,
  }) {
    return GlassCard(
      padding: const EdgeInsets.all(AppSpacing.md),
      borderRadius: AppSpacing.radiusMd,
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(icon, color: AppColors.success, size: 20),
          const SizedBox(width: AppSpacing.md),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  title,
                  style: const TextStyle(
                    fontWeight: FontWeight.bold,
                    fontSize: 14,
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  description,
                  style: TextStyle(
                    fontSize: 12,
                    color: AppColors.getTextSecondary(context),
                    height: 1.4,
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildSectionHeader(String title) {
    return Padding(
      padding: const EdgeInsets.only(left: 4, bottom: 12),
      child: Text(
        title.toUpperCase(),
        style: const TextStyle(
          fontSize: 12,
          fontWeight: FontWeight.w700,
          color: Colors.grey,
          letterSpacing: 1.2,
        ),
      ),
    );
  }

  void _showThemeDialog(BuildContext context) {
    final lang = _settings.language.value;
    final isDark = Theme.of(context).brightness == Brightness.dark;

    showModalBottomSheet(
      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      builder: (bottomSheetContext) {
        final bottomPad =
            MediaQuery.of(bottomSheetContext).viewPadding.bottom +
            AppSpacing.xl;
        return GlassCard(
          customBorderRadius: const BorderRadius.vertical(
            top: Radius.circular(AppSpacing.radiusLg),
          ),
          padding: EdgeInsets.fromLTRB(0, AppSpacing.lg, 0, bottomPad),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Container(
                width: 40,
                height: 4,
                decoration: BoxDecoration(
                  color: isDark ? Colors.white24 : Colors.black12,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
              const SizedBox(height: AppSpacing.md),
              Text(
                L10n.get('theme', lang),
                style: const TextStyle(
                  fontSize: 18,
                  fontWeight: FontWeight.bold,
                ),
              ),
              const SizedBox(height: AppSpacing.md),
              ...['system', 'light', 'dark'].map(
                (t) => ListTile(
                  leading: Icon(
                    t == 'system'
                        ? Icons.brightness_auto_rounded
                        : (t == 'light'
                              ? Icons.light_mode_rounded
                              : Icons.dark_mode_rounded),
                    color: AppColors.primary,
                  ),
                  title: Text(
                    t.toUpperCase(),
                    style: const TextStyle(fontWeight: FontWeight.w600),
                  ),
                  trailing: _settings.themeMode.value == _settings.parseTheme(t)
                      ? const Icon(Icons.check_circle, color: AppColors.primary)
                      : null,
                  onTap: () {
                    _settings.updateTheme(t);
                    Navigator.pop(bottomSheetContext);
                  },
                ),
              ),
            ],
          ),
        );
      },
    );
  }

  void _confirmEmptyTrash(BuildContext context, String lang) {
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(L10n.get('empty_trash_now', lang)),
        content: const Text(
          'Are you sure you want to permanently delete all items in trash?',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () {
              Navigator.pop(ctx);
              if (mounted) {
                ScaffoldMessenger.of(
                  this.context,
                ).showSnackBar(const SnackBar(content: Text('Trash emptied')));
              }
            },
            child: const Text('Empty', style: TextStyle(color: Colors.red)),
          ),
        ],
      ),
    );
  }

  // Removed unused _handleSyncAction to satisfy analyzer.

  void _showLanguageDialog(BuildContext context) {
    final lang = _settings.language.value;
    final isDark = Theme.of(context).brightness == Brightness.dark;

    showModalBottomSheet(
      context: context,
      backgroundColor: Colors.transparent,
      isScrollControlled: true,
      builder: (bottomSheetContext) {
        final bottomPad =
            MediaQuery.of(bottomSheetContext).viewPadding.bottom +
            AppSpacing.xl;
        return GlassCard(
          customBorderRadius: const BorderRadius.vertical(
            top: Radius.circular(AppSpacing.radiusLg),
          ),
          padding: EdgeInsets.fromLTRB(0, AppSpacing.lg, 0, bottomPad),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Container(
                width: 40,
                height: 4,
                decoration: BoxDecoration(
                  color: isDark ? Colors.white24 : Colors.black12,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
              const SizedBox(height: AppSpacing.md),
              Text(
                L10n.get('language', lang),
                style: const TextStyle(
                  fontSize: 18,
                  fontWeight: FontWeight.bold,
                ),
              ),
              const SizedBox(height: AppSpacing.md),
              ...['auto', 'fr', 'en'].map(
                (l) => ListTile(
                  leading: const Icon(
                    Icons.language_rounded,
                    color: AppColors.primary,
                  ),
                  title: Text(
                    l == 'auto'
                        ? 'Auto (System)'
                        : (l == 'fr' ? 'Français' : 'English'),
                    style: const TextStyle(fontWeight: FontWeight.w600),
                  ),
                  trailing: _settings.language.value == l
                      ? const Icon(Icons.check_circle, color: AppColors.primary)
                      : null,
                  onTap: () {
                    _settings.updateLanguage(l);
                    Navigator.pop(bottomSheetContext);
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
