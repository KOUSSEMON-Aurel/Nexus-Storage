import 'device_profiler.dart';

class TaskConfig {
  final int maxConcurrentTasks;
  final int ffmpegThreads;       // -threads N passé à FFmpeg
  final int chunkSizeMb;         // taille des blocs traités par Rust
  final Duration pauseBetweenFrames;
  final String ffmpegPreset;     // ultrafast, medium, etc.

  const TaskConfig({
    required this.maxConcurrentTasks,
    required this.ffmpegThreads,
    required this.chunkSizeMb,
    required this.pauseBetweenFrames,
    required this.ffmpegPreset,
  });

  static Future<TaskConfig> forDevice() async {
    final tier = await DeviceProfiler.getTier();
    return switch (tier) {
      DeviceTier.high => const TaskConfig(
          maxConcurrentTasks: 3,
          ffmpegThreads: 4,
          chunkSizeMb: 16,
          pauseBetweenFrames: Duration(milliseconds: 0),
          ffmpegPreset: 'medium',
        ),
      DeviceTier.mid => const TaskConfig(
          maxConcurrentTasks: 2,
          ffmpegThreads: 2,
          chunkSizeMb: 8,
          pauseBetweenFrames: Duration(milliseconds: 16), // ~1 frame 60fps
          ffmpegPreset: 'faster',
        ),
      DeviceTier.low => const TaskConfig(
          maxConcurrentTasks: 1,
          ffmpegThreads: 1,
          chunkSizeMb: 4,
          pauseBetweenFrames: Duration(milliseconds: 33), // ~30fps
          ffmpegPreset: 'ultrafast',
        ),
    };
  }
}
