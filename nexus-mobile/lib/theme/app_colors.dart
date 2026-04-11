import 'package:flutter/material.dart';

class AppColors {
  // Dark Theme (Default)
  static const Color background = Color(0xFF0D0F14);
  static const Color surface = Color(0xFF161B22);
  static const Color surfaceElevated = Color(0xFF21262D);
  
  static const Color textPrimary = Color.fromRGBO(255, 255, 255, 0.90);
  static const Color textSecondary = Color.fromRGBO(255, 255, 255, 0.45);
  static const Color textDisabled = Color.fromRGBO(255, 255, 255, 0.38);

  // Light Theme
  static const Color backgroundLight = Color(0xFFF8FAFC);
  static const Color surfaceLight = Color(0xFFFFFFFF);
  static const Color surfaceElevatedLight = Color(0xFFF1F5F9);

  static const Color textPrimaryLight = Color(0xFF0F172A);
  static const Color textSecondaryLight = Color(0xFF64748B);
  static const Color textDisabledLight = Color(0xFF94A3B8);
  
  // Accents (Shared)
  static const Color primary = Color(0xFF5B8DEF);
  static const Color secondary = Color(0xFF7B6CF6);
  static const Color accent = Color(0xFF00D4FF);
  
  // Semantic
  static const Color success = Color(0xFF4ADE80);
  static const Color error = Color(0xFFF87171);
  static const Color warning = Color(0xFFFBBF24);
  static const Color info = Color(0xFF38BDF8);
  
  // Glassmorphism defaults
  static const Color glassBackground = Color.fromRGBO(255, 255, 255, 0.08);
  static const Color glassBorder = Color.fromRGBO(255, 255, 255, 0.12);

  static const Color glassBackgroundLight = Color.fromRGBO(15, 23, 42, 0.04);
  static const Color glassBorderLight = Color.fromRGBO(15, 23, 42, 0.08);

  // Helper methods to get colors based on theme
  static Color getBackground(BuildContext context) => 
      Theme.of(context).brightness == Brightness.dark ? background : backgroundLight;

  static Color getTextPrimary(BuildContext context) => 
      Theme.of(context).brightness == Brightness.dark ? textPrimary : textPrimaryLight;

  static Color getTextSecondary(BuildContext context) => 
      Theme.of(context).brightness == Brightness.dark ? textSecondary : textSecondaryLight;

  static Color getSurface(BuildContext context) => 
      Theme.of(context).brightness == Brightness.dark ? surface : surfaceLight;

  static Color getSurfaceElevated(BuildContext context) => 
      Theme.of(context).brightness == Brightness.dark ? surfaceElevated : surfaceElevatedLight;

  static Color getGlassBackground(BuildContext context) => 
      Theme.of(context).brightness == Brightness.dark ? glassBackground : glassBackgroundLight;

  static Color getGlassBorder(BuildContext context) => 
      Theme.of(context).brightness == Brightness.dark ? glassBorder : glassBorderLight;
}
