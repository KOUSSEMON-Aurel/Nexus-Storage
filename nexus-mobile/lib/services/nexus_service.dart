import 'dart:ffi';
import 'dart:io';
import 'package:ffi/ffi.dart';
import 'package:path_provider/path_provider.dart';
import 'package:ffmpeg_kit_flutter_new/ffmpeg_kit.dart';
import 'package:ffmpeg_kit_flutter_new/return_code.dart';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import 'package:permission_handler/permission_handler.dart';
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

  /// Download from YouTube, extract frames, decode and decrypt.
  Future<void> downloadAndDecrypt(FileRecord record, String password, {String? explicitTaskId}) async {
    final taskId = explicitTaskId ?? DateTime.now().millisecondsSinceEpoch.toString();
    final fileName = record.path.split('/').last;

    try {
      // 0. Register Task
      await _db.insertTask({
        'id': taskId,
        'type': 2, // Download
        'file_path': record.path,
        'status': 'Starting download...',
        'progress': 0.1,
        'created_at': DateTime.now().toIso8601String(),
      });

      if (await FlutterForegroundTask.isRunningService) {
        FlutterForegroundTask.updateService(
          notificationTitle: 'Nexus: Downloading $fileName',
          notificationText: 'Status: Downloading from YouTube...',
        );
      }

      AppLogger.info('Starting downloadAndDecrypt for $fileName (ID: ${record.videoId})');

      // 1. Download Video (YouTube)
      final videoFile = await _youtube.downloadVideo(
        record.videoId,
        onProgress: (p) {
          _db.updateTaskProgress(taskId, 0.1 + (p * 0.4), 'Downloading... ${(p * 100).toInt()}%');
        },
      );

      if (videoFile == null) throw NexusException('YouTube download failed');

      await _db.updateTaskProgress(taskId, 0.5, 'Extracting frames...');
      if (await FlutterForegroundTask.isRunningService) {
        FlutterForegroundTask.updateService(notificationText: 'Status: Extracting frames...');
      }

      final tmpDir = await getTemporaryDirectory();
      final framesDir = Directory('${tmpDir.path}/nexus-dl-$taskId');
      await framesDir.create();

      try {
        // 2. Extract Frames (FFmpeg)
        // We use -vsync 0 to get all frames or -r 30 if we know it's 30fps
        final ffmpegCommand = '-i ${videoFile.path} -vsync 0 ${framesDir.path}/frame_%06d.png';
        
        final session = await FFmpegKit.execute(ffmpegCommand);
        final returnCode = await session.getReturnCode();

        if (!ReturnCode.isSuccess(returnCode)) {
          final logs = await session.getAllLogsAsString();
          throw NexusException('FFmpeg extraction failed: $logs');
        }

        // Check if frames were actually extracted
        final frames = framesDir.listSync().where((e) => e.path.endsWith('.png')).toList();
        if (frames.isEmpty) {
          throw NexusException('No frames extracted from video. Decoding aborted.');
        }

        await _db.updateTaskProgress(taskId, 0.7, 'Decoding data...');
        if (await FlutterForegroundTask.isRunningService) {
          FlutterForegroundTask.updateService(notificationText: 'Status: Decoding data...');
        }

        // 3. Decode Frames to Encrypted Data (Rust)
        final outPtrPtr = malloc<Pointer<Uint8>>();
        final outLenPtr = malloc<Size>();
        final framesDirPtr = framesDir.path.toNativeUtf8().cast<Char>();

        try {
          // Mode 0 = Tank (standard for this project)
          int decodeRes = _native.nexus_decode_from_frames(framesDirPtr, 0, outPtrPtr, outLenPtr);
          malloc.free(framesDirPtr);

          if (decodeRes != 0) throw NexusException('Decoding failed with code: $decodeRes');

          await _db.updateTaskProgress(taskId, 0.8, 'Decrypting...');
          if (await FlutterForegroundTask.isRunningService) {
            FlutterForegroundTask.updateService(notificationText: 'Status: Decrypting...');
          }

          // 4. Decrypt (Rust)
          final decryptedPtrPtr = malloc<Pointer<Uint8>>();
          final decryptedLenPtr = malloc<Size>();
          final passPtr = password.toNativeUtf8().cast<Char>();

          try {
            int decryptRes = _native.nexus_decrypt(outPtrPtr.value, outLenPtr.value, passPtr, decryptedPtrPtr, decryptedLenPtr);
            malloc.free(passPtr);

            if (decryptRes != 0) throw NexusException('Decryption failed with code: $decryptRes (Check password)');

            await _db.updateTaskProgress(taskId, 0.9, 'Saving to Downloads...');
            
            // 5. Save to Downloads
            final downloadsDir = await _getPublicDownloadsDirectory();
            final outputFile = File('${downloadsDir.path}/$fileName');
            
            // Ensure unique name if exists
            var finalFile = outputFile;
            int counter = 1;
            while (await finalFile.exists()) {
              final nameParts = fileName.split('.');
              final ext = nameParts.length > 1 ? '.${nameParts.removeLast()}' : '';
              final baseName = nameParts.join('.');
              finalFile = File('${downloadsDir.path}/$baseName ($counter)$ext');
              counter++;
            }

            final data = decryptedPtrPtr.value.asTypedList(decryptedLenPtr.value);
            await finalFile.writeAsBytes(data);

            await _db.updateTaskProgress(taskId, 1.0, 'completed');
            AppLogger.info('Download completed: ${finalFile.path}');

          } finally {
            _native.nexus_free_bytes(decryptedPtrPtr.value, decryptedLenPtr.value);
            malloc.free(decryptedPtrPtr);
            malloc.free(decryptedLenPtr);
          }
        } finally {
          _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
          malloc.free(outPtrPtr);
          malloc.free(outLenPtr);
        }
      } finally {
        if (await framesDir.exists()) await framesDir.delete(recursive: true);
        if (await videoFile.exists()) await videoFile.delete();
        // Also cleanup the parent tmp directory if empty
        try {
          if (videoFile.parent.listSync().isEmpty) await videoFile.parent.delete();
        } catch (_) {}
      }
    } catch (e, s) {
      AppLogger.error('DOWNLOAD ERROR for $fileName: $e', e, s);
      await _db.updateTaskProgress(taskId, 0.0, 'Failed: ${e.toString().split('\n').first}');
      rethrow;
    }
  }

  Future<Directory> _getPublicDownloadsDirectory() async {
    if (Platform.isAndroid) {
      if (!await Permission.manageExternalStorage.isGranted) {
        await Permission.manageExternalStorage.request();
      }
      final dir = Directory('/storage/emulated/0/Download/NexusStorage');
      if (!await dir.exists()) await dir.create(recursive: true);
      return dir;
    } else {
      final dir = await getDownloadsDirectory();
      return dir ?? await getApplicationDocumentsDirectory();
    }
  }
}
