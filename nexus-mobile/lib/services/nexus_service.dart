import 'dart:ffi';
import 'dart:io';
import 'package:ffi/ffi.dart';
import 'package:path_provider/path_provider.dart';
import 'package:ffmpeg_kit_flutter_new/ffmpeg_kit.dart';
import 'package:ffmpeg_kit_flutter_new/return_code.dart';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import '../ffi/nexus_bindings.dart';
import '../ffi/nexus_loader.dart';
import 'database_service.dart';
import 'youtube_service.dart';
import '../models/file_record.dart';
import 'sync_service.dart';
import 'logger_service.dart';
import '../utils/exceptions.dart';

class NexusService {
  final NexusCoreBindings _native = NexusLoader.bindings;
  final DatabaseService _db = DatabaseService();
  final YouTubeService _youtube = YouTubeService();

  /// Encrypt and encode a file into frames, then upload.
  Future<void> encodeAndUpload(File inputFile, String password, {String? explicitTaskId}) async {
    final taskId = explicitTaskId ?? DateTime.now().millisecondsSinceEpoch.toString();
    final fileName = inputFile.path.split('/').last;

    try {
      if (!await inputFile.exists()) {
        throw NexusException('Input file does not exist: $fileName');
      }

      final bytes = await inputFile.readAsBytes();

      // 0. Register Task
      await _db.insertTask({
        'id': taskId,
        'type': 1, // Upload
        'file_path': inputFile.path,
        'status': 'Encrypting...',
        'progress': 0.1,
        'created_at': DateTime.now().toIso8601String(),
      });

      if (await FlutterForegroundTask.isRunningService) {
        FlutterForegroundTask.updateService(
          notificationTitle: 'Nexus: Processing $fileName',
          notificationText: 'Status: Encrypting...',
        );
      }

      AppLogger.info('Starting encodeAndUpload for $fileName');

      // 1. Encrypt & Compress (Rust)
      final inPtr = malloc<Uint8>(bytes.length);
      inPtr.asTypedList(bytes.length).setAll(0, bytes);

      final outPtrPtr = malloc<Pointer<Uint8>>();
      final outLenPtr = malloc<Size>();
      final passPtr = password.toNativeUtf8().cast<Char>();

      String? hash;
      Directory? framesDir;

      try {
        int res = _native.nexus_encrypt(inPtr, bytes.length, passPtr, outPtrPtr, outLenPtr);
        if (res != 0) throw NexusException('Encryption failed with code: $res');

        await _db.updateTaskProgress(taskId, 0.3, 'Encoding frames...');
        if (await FlutterForegroundTask.isRunningService) {
          FlutterForegroundTask.updateService(notificationText: 'Status: Encoding frames...');
        }

        // Compute Hash
        final hashPtr = malloc<Char>(65);
        _native.nexus_sha256_hex(outPtrPtr.value, outLenPtr.value, hashPtr);
        hash = hashPtr.cast<Utf8>().toDartString();
        malloc.free(hashPtr);

        // 2. Encode to Frames (Rust)
        final tmpDir = await getTemporaryDirectory();
        framesDir = Directory('${tmpDir.path}/nexus-$taskId');
        await framesDir.create();

        final framesDirPtr = framesDir.path.toNativeUtf8().cast<Char>();
        int frameCount = _native.nexus_encode_to_frames(outPtrPtr.value, outLenPtr.value, framesDirPtr, 0); 
        malloc.free(framesDirPtr);
        
        if (frameCount <= 0) throw NexusException('Encoding failed: frameCount=$frameCount');

        // Pad to min 90 frames (3 seconds at 30fps) for YouTube stability
        if (frameCount < 90) {
          AppLogger.info('Padding video from $frameCount to 90 frames...');
          final lastFrameFile = File('${framesDir.path}/frame_${frameCount.toString().padLeft(6, '0')}.png');
          final lastFrameData = await lastFrameFile.readAsBytes();
          for (int j = frameCount + 1; j <= 90; j++) {
            final padFile = File('${framesDir.path}/frame_${j.toString().padLeft(6, '0')}.png');
            await padFile.writeAsBytes(lastFrameData);
          }
        }

        await _db.updateTaskProgress(taskId, 0.5, 'Assembling video...');
        if (await FlutterForegroundTask.isRunningService) {
          FlutterForegroundTask.updateService(notificationText: 'Status: Assembling video...');
        }

        // 3. Assemble Video (FFmpeg)
        final videoFile = File('${framesDir.path}/out.mp4');
        final ffmpegCommand = '-framerate 30 -i ${framesDir.path}/frame_%06d.png -c:v libx264 -pix_fmt yuv420p -y ${videoFile.path}';
        
        final session = await FFmpegKit.execute(ffmpegCommand);
        final returnCode = await session.getReturnCode();

        if (!ReturnCode.isSuccess(returnCode)) {
          final logs = await session.getAllLogsAsString();
          throw NexusException('FFmpeg assembly failed: $logs');
        }

        await _db.updateTaskProgress(taskId, 0.7, 'Uploading to Cloud...');
        if (await FlutterForegroundTask.isRunningService) {
          FlutterForegroundTask.updateService(notificationText: 'Status: Uploading to Cloud...');
        }

        // 4. Upload (YouTube)
        final videoId = await _youtube.uploadVideo(
          videoFile: videoFile,
          title: 'Nexus Data: $hash',
          description: 'Secure Nexus Storage Object ($fileName)',
          onProgress: (p) {
            _db.updateTaskProgress(taskId, 0.7 + (p * 0.3), 'Uploading... ${(p * 100).toInt()}%');
          },
        );

        if (videoId == null) throw NexusException('YouTube upload returned null videoId');

        // 5. Finalize Record
        final record = FileRecord(
          path: fileName,
          videoId: videoId,
          size: bytes.length,
          hash: hash,
          key: password,
          lastUpdate: DateTime.now().toIso8601String(),
          starred: false,
          sha256: hash,
          fileKey: '',
          isArchive: false,
          hasCustomPassword: password.isNotEmpty,
          customPasswordHint: '',
          mode: 'base',
        );

        await _db.saveFile(record);
        await _db.updateTaskProgress(taskId, 1.0, 'completed');

        // Auto-Sync after successful upload
        try {
          await SyncService().pushDatabase();
        } catch (syncError) {
          AppLogger.warn('Auto-sync failed after upload: $syncError');
        }
        
      } finally {
        malloc.free(inPtr);
        malloc.free(outPtrPtr);
        malloc.free(outLenPtr);
        malloc.free(passPtr);
        if (framesDir != null && await framesDir.exists()) {
          await framesDir.delete(recursive: true);
        }
      }
    } catch (e, s) {
      AppLogger.error('UPLOAD ERROR for $fileName: $e', e, s);
      await _db.updateTaskProgress(taskId, 0.0, 'Failed: ${e.toString().split('\n').first}');
      rethrow;
    }
  }
}
