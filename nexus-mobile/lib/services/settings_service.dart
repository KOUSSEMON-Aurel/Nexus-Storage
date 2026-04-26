import 'package:flutter/material.dart';
import 'database_service.dart';

class SettingsService {
  static final SettingsService _instance = SettingsService._internal();
  factory SettingsService() => _instance;
  SettingsService._internal();

  final DatabaseService _db = DatabaseService();

  // Theme state
  final ValueNotifier<ThemeMode> themeMode = ValueNotifier(ThemeMode.system);

  // Language state
  final ValueNotifier<String> language = ValueNotifier('fr');

  Future<void> init() async {
    final theme = await _db.getKV('app_theme') ?? 'system';
    final lang = await _db.getKV('app_language') ?? 'auto';

    themeMode.value = parseTheme(theme);
    language.value = lang;
  }

  Future<void> updateTheme(String theme) async {
    await _db.setKV('app_theme', theme);
    themeMode.value = parseTheme(theme);
  }

  Future<void> updateLanguage(String lang) async {
    await _db.setKV('app_language', lang);
    language.value = lang;
  }

  ThemeMode parseTheme(String theme) {
    switch (theme) {
      case 'light':
        return ThemeMode.light;
      case 'dark':
        return ThemeMode.dark;
      default:
        return ThemeMode.system;
    }
  }
}
