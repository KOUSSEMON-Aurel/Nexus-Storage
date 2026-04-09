# Flutter ProGuard Rules

# Keep FFmpegKit classes and native methods (Original and Fork)
-keep class com.arthenica.ffmpegkit.** { *; }
-keep class com.antonkarpenko.ffmpegkit.** { *; }
-keep class com.arthenica.mobileffmpeg.** { *; }

# Keep native methods
-keepclasseswithmembernames class * {
    native <methods>;
}

# Keep sqflite
-keep class com.tekartik.sqflite.** { *; }
