import 'dart:io';
import 'package:ffmpeg_kit_flutter_new/ffmpeg_kit.dart';
import 'package:ffmpeg_kit_flutter_new/return_code.dart';
import 'package:path_provider/path_provider.dart';
import 'nexus_service.dart';
import 'logger_service.dart';

extension NexusFFmpeg on NexusService {
  Future<File?> assembleVideo(Directory framesDir, {File? coverVideo}) async {
    final tmpDir = await getTemporaryDirectory();
    final dataVideo = File('${tmpDir.path}/data_video_${DateTime.now().millisecondsSinceEpoch}.mp4');
    
    // 1. Synthesize data video from frames
    final dataArgs = '-framerate 30 -i ${framesDir.path}/frame_%05d.png -c:v libx264 -pix_fmt yuv420p -y ${dataVideo.path}';
    final dataSession = await FFmpegKit.execute(dataArgs);
    final dataRc = await dataSession.getReturnCode();

    if (!ReturnCode.isSuccess(dataRc)) {
      AppLogger.error('FFmpeg error (data): ${await dataSession.getOutput()}');
      return null;
    }

    if (coverVideo == null) {
      return dataVideo;
    }

    // 2. Trojan Horse Concatenation
    final outputVideo = File('${tmpDir.path}/nexus_final_${DateTime.now().millisecondsSinceEpoch}.mp4');
    
    // We use a simple concat filter. 
    // Note: This requires both videos to have similar properties (resolution, framerate).
    // In a production app, we would re-encode the data video to match the cover.
    final concatArgs = '-i ${coverVideo.path} -i ${dataVideo.path} '
        '-filter_complex "[0:v][1:v]concat=n=2:v=1[v]" '
        '-map "[v]" -c:v libx264 -preset ultrafast -y ${outputVideo.path}';
    
    final concatSession = await FFmpegKit.execute(concatArgs);
    final concatRc = await concatSession.getReturnCode();

    if (ReturnCode.isSuccess(concatRc)) {
      return outputVideo;
    } else {
      AppLogger.error('FFmpeg error (concat): ${await concatSession.getOutput()}');
      return dataVideo; // Fallback to raw data video if concat fails
    }
  }
}
