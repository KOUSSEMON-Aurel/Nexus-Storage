import 'package:flutter/material.dart';
import 'package:shimmer/shimmer.dart';
import 'package:nexus_mobile/theme/app_colors.dart';
import 'package:nexus_mobile/theme/app_spacing.dart';

class SkeletonItem extends StatelessWidget {
  final double width;
  final double height;
  final double borderRadius;

  const SkeletonItem({
    super.key,
    this.width = double.infinity,
    this.height = 20.0,
    this.borderRadius = AppSpacing.radiusSm,
  });

  @override
  Widget build(BuildContext context) {
    return Shimmer.fromColors(
      baseColor: AppColors.surfaceElevated,
      highlightColor: AppColors.surfaceElevated.withOpacity(0.8),
      period: const Duration(milliseconds: 1500),
      child: Container(
        width: width,
        height: height,
        decoration: BoxDecoration(
          color: Colors.black,
          borderRadius: BorderRadius.circular(borderRadius),
        ),
      ),
    );
  }
}

class FileSkeletonList extends StatelessWidget {
  const FileSkeletonList({super.key});

  @override
  Widget build(BuildContext context) {
    return ListView.builder(
      itemCount: 6,
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      itemBuilder: (context, index) {
        return Padding(
          padding: const EdgeInsets.only(bottom: AppSpacing.md),
          child: Row(
            children: [
              const SkeletonItem(width: 48, height: 48, borderRadius: AppSpacing.radiusMd),
              const SizedBox(width: AppSpacing.md),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    SkeletonItem(width: MediaQuery.of(context).size.width * 0.4, height: 16),
                    const SizedBox(height: AppSpacing.xs),
                    SkeletonItem(width: MediaQuery.of(context).size.width * 0.6, height: 12),
                  ],
                ),
              ),
            ],
          ),
        );
      },
    );
  }
}
