import 'package:flutter/material.dart';

class AppColors {
  // Dark Theme (Default)
  static const Color background = Color(0xFF0D0F14);
  static const Color surface = Color(0xFF161B22);
  static const Color surfaceElevated = Color(0xFF21262D);
  
  static const Color textPrimary = Color.fromRGBO(255, 255, 255, 0.90);
  static const Color textSecondary = Color.fromRGBO(255, 255, 255, 0.45);
  static const Color textDisabled = Color.fromRGBO(255, 255, 255, 0.38);

  // Light Theme (Optimisé pour la visibilité)
  static const Color backgroundLight = Color(0xFFEDF2F7); // Plus gris pour détacher les cartes
  static const Color surfaceLight = Color(0xFFFFFFFF);
  static const Color surfaceElevatedLight = Color(0xFFCBD5E0);

  static const Color textPrimaryLight = Color(0xFF0F172A);
  static const Color textSecondaryLight = Color(0xFF1E293B); // Très sombre pour contraste
  static const Color textDisabledLight = Color(0xFF64748B);
  
  // Accents (Shared)
  static const Color primary = Color(0xFF312E81); // Indigo très profond pour le Light
  static const Color secondary = Color(0xFF5B21B6); // Violet profond
  static const Color accent = Color(0xFF0891B2);
  
  // Semantic Dark
  static const Color success = Color(0xFF4ADE80);
  static const Color error = Color(0xFFF87171);
  static const Color warning = Color(0xFFFBBF24);
  static const Color info = Color(0xFF38BDF8);

  // Semantic Light (Très saturé pour visibilité maximale)
  static const Color successLight = Color(0xFF166534);
  static const Color errorLight = Color(0xFF991B1B); // Rouge Sang très foncé
  static const Color warningLight = Color(0xFF92400E);
  static const Color infoLight = Color(0xFF075985);
  
  // Glassmorphism defaults
  static const Color glassBackground = Color.fromRGBO(255, 255, 255, 0.08);
  static const Color glassBorder = Color.fromRGBO(255, 255, 255, 0.12);

  static const Color glassBackgroundLight = Color.fromRGBO(15, 23, 42, 0.10);
  static const Color glassBorderLight = Color.fromRGBO(15, 23, 42, 0.20);

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

  static Color getError(BuildContext context) => 
      Theme.of(context).brightness == Brightness.dark ? error : errorLight;

  static Color getSuccess(BuildContext context) => 
      Theme.of(context).brightness == Brightness.dark ? success : successLight;
}
