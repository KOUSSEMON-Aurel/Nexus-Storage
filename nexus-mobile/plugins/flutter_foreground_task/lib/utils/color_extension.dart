import 'dart:ui';

extension ColorExtension on Color {
  String get toRgbString =>
      '${(r * 255).toInt()},${(g * 255).toInt()},${(b * 255).toInt()}';
}
