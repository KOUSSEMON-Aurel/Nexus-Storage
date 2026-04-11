import 'package:flutter/material.dart';
import 'dart:ui';
import 'package:nexus_mobile/theme/app_colors.dart';
import 'package:nexus_mobile/theme/app_spacing.dart';

class GlassCard extends StatelessWidget {
  final Widget child;
  final double? width;
  final double? height;
  final double borderRadius;
  final BorderRadius? customBorderRadius;
  final double blur;
  final EdgeInsetsGeometry? padding;
  final double borderOpacity;

  const GlassCard({
    super.key,
    required this.child,
    this.width,
    this.height,
    this.borderRadius = AppSpacing.radiusLg,
    this.customBorderRadius,
    this.blur = 12.0,
    this.padding,
    this.borderOpacity = 0.12,
  });

  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final glassColor = AppColors.getGlassBackground(context);
    final borderColor = AppColors.getGlassBorder(context);

    return Container(
      width: width,
      height: height,
      decoration: BoxDecoration(
        borderRadius: customBorderRadius ?? BorderRadius.circular(borderRadius),
        boxShadow: [
          BoxShadow(
            color: AppColors.primary.withOpacity(isDark ? 0.05 : 0.02),
            blurRadius: 20,
            offset: const Offset(0, 8),
          ),
        ],
      ),
      child: ClipRRect(
        borderRadius: customBorderRadius ?? BorderRadius.circular(borderRadius),
        child: BackdropFilter(
          filter: ImageFilter.blur(sigmaX: blur, sigmaY: blur),
          child: Container(
            padding: padding ?? const EdgeInsets.all(AppSpacing.cardPadding),
            decoration: BoxDecoration(
              borderRadius: customBorderRadius ?? BorderRadius.circular(borderRadius),
              color: glassColor,
              border: Border.all(
                color: borderColor,
                width: 1.0,
              ),
              gradient: LinearGradient(
                begin: Alignment.topLeft,
                end: Alignment.bottomRight,
                colors: [
                  borderColor.withOpacity(0.1),
                  borderColor.withOpacity(0.01),
                ],
              ),
            ),
            child: child,
          ),
        ),
      ),
    );
  }
}
