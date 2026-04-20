import 'dart:ffi';
import 'dart:io';
import 'dart:typed_data';
import 'package:ffi/ffi.dart';
import 'package:path_provider/path_provider.dart';
import 'package:ffmpeg_kit_flutter_new/ffmpeg_kit.dart';
import 'package:ffmpeg_kit_flutter_new/return_code.dart';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import 'package:ffmpeg_kit_flutter_new/ffprobe_kit.dart';
import 'package:ffmpeg_kit_flutter_new/ffmpeg_kit_config.dart';
import '../ffi/nexus_bindings.dart';
import '../ffi/nexus_loader.dart';
import 'database_service.dart';
import 'youtube_service.dart';
import 'sync_service.dart';
import '../models/file_record.dart';
import 'logger_service.dart';
import '../utils/exceptions.dart';
import '../core/task_config.dart';

class NexusService {
  final NexusCoreBindings _native = NexusLoader.bindings;
  final DatabaseService _db = DatabaseService();
  final YouTubeService _youtube = YouTubeService();
  DateTime _lastRefreshTime = DateTime.fromMillisecondsSinceEpoch(0);
  static const bool keepFramesForDebug = false;

  Future<void> _updateStatus(String taskId, double progress, String status, {String? fileName}) async {
    await _db.updateTaskProgress(taskId, progress, status);
    if (await FlutterForegroundTask.isRunningService) {
      FlutterForegroundTask.updateService(
        notificationTitle: fileName != null ? 'Nexus: Transfer $fileName' : 'Nexus active task',
        notificationText: '$status (${(progress * 100).toInt()}%)',
      );
      
      final now = DateTime.now();
      if (now.difference(_lastRefreshTime).inMilliseconds > 1500 || status == 'completed' || status.startsWith('Failed')) {
        await _db.checkpointWAL();
        FlutterForegroundTask.sendDataToMain('refresh');
        _lastRefreshTime = now;
      }
    } else {
      // Fallback for when we're running in Android 14 direct mode
      final plugin = FlutterLocalNotificationsPlugin();
      await plugin.show(
        id: taskId.hashCode,
        title: fileName != null ? 'Nexus: Transfer $fileName' : 'Nexus active task',
        body: '$status (${(progress * 100).toInt()}%)',
        notificationDetails: const NotificationDetails(
          android: AndroidNotificationDetails(
            'nexus_upload_channel',
            'Nexus Transferts',
            importance: Importance.low,
            priority: Priority.low,
            showProgress: true,
            maxProgress: 100,
            onlyAlertOnce: true,
          ),
        ),
      );
      
      final now = DateTime.now();
      if (now.difference(_lastRefreshTime).inMilliseconds > 1500 || status == 'completed' || status.startsWith('Failed')) {
        await _db.checkpointWAL();
        _lastRefreshTime = now;
      }
    }
  }

  /// Encrypt and encode a file into frames, then upload using a streaming pipeline.
  Future<void> encodeAndUpload(File inputFile, String password, {String? explicitTaskId}) async {
    final taskId = explicitTaskId ?? DateTime.now().millisecondsSinceEpoch.toString();
    final fileName = inputFile.path.split('/').last;
    final fileSize = await inputFile.length();

    Pointer<Uint8>? keyPtr;
    Pointer<Uint8>? noncePrefixPtr;
    Pointer<StreamingContext>? cryptoCtx;
    Pointer<StreamingEncoder>? encoderCtx;
    Pointer<Pointer<Uint8>>? outPtrPtr;
    Pointer<Size>? outLenPtr;
    Directory? framesDir;
    File? videoFile;
    // local debug flag removed (use class-level `keepFramesForDebug`)

    try {
      if (!await inputFile.exists()) {
        throw NexusException('Input file does not exist: $fileName');
      }

      await _db.insertTask({
        'id': taskId,
        'type': 1, // Upload
        'file_path': inputFile.path,
        'status': 'Initializing stream...',
        'progress': 0.05,
        'created_at': DateTime.now().toIso8601String(),
      });
      if (await FlutterForegroundTask.isRunningService) {
        await _db.checkpointWAL();
        FlutterForegroundTask.sendDataToMain('refresh');
      }

      await _updateStatus(taskId, 0.05, 'Initializing stream...', fileName: fileName);

      AppLogger.info('Starting streaming encodeAndUpload for $fileName');

      final tmpDir = await getTemporaryDirectory();
      framesDir = Directory('${tmpDir.path}/nexus-$taskId');
      if (await framesDir.exists()) await framesDir.delete(recursive: true);
      await framesDir.create();

      final keyBytes = await _deriveKeyFromPassword(password);
      keyPtr = malloc<Uint8>(32);
      keyPtr.asTypedList(32).setAll(0, keyBytes);

      noncePrefixPtr = malloc<Uint8>(16);
      cryptoCtx = _native.nexus_encrypt_stream_init(keyPtr, noncePrefixPtr);
      if (cryptoCtx == nullptr) throw NexusException('Failed to init crypto stream');

      encoderCtx = _native.nexus_encode_stream_init(0);
      if (encoderCtx == nullptr) throw NexusException('Failed to init encoder stream');

      outPtrPtr = malloc<Pointer<Uint8>>();
      outLenPtr = malloc<Size>();
      
      // CRITICAL: Add nonce_prefix to FEC stream so downloader can extract it
      int nonceRes = _native.nexus_encode_stream_push_fec(encoderCtx, noncePrefixPtr, 16);
      if (nonceRes < 0) throw NexusException('Failed to push nonce to FEC encoder: $nonceRes');
      AppLogger.info('NexusService: Added nonce to FEC stream');
      
      int frameCount = 0;
      int processedBytes = 0;

      await for (final chunk in inputFile.openRead()) {
        final chunkPtr = malloc<Uint8>(chunk.length);
        chunkPtr.asTypedList(chunk.length).setAll(0, chunk);

        int res = _native.nexus_encrypt_stream_update(cryptoCtx, chunkPtr, chunk.length, outPtrPtr, outLenPtr);
        malloc.free(chunkPtr);
        if (res != 0) throw NexusException('Streaming encryption failed: $res');

        if (outLenPtr.value > 0) {
          res = _native.nexus_encode_stream_push_fec(encoderCtx, outPtrPtr.value, outLenPtr.value);
          _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
          if (res < 0) throw NexusException('Streaming encoding push failed: $res');
        }

        while (true) {
          final popRes = _native.nexus_encode_stream_pop_frame(encoderCtx, outPtrPtr, outLenPtr);
          if (popRes == 1) break; 
          if (popRes != 0) throw NexusException('Frame generation failed: $popRes');

          frameCount++;
          final frameFile = File('${framesDir.path}/frame_${frameCount.toString().padLeft(6, '0')}.png');
          await frameFile.writeAsBytes(outPtrPtr.value.asTypedList(outLenPtr.value));
          _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
        }

        processedBytes += chunk.length;
        final progress = 0.1 + (processedBytes / fileSize * 0.4);
        await _updateStatus(taskId, progress, 'Processing data...', fileName: fileName);
      }

      int res = _native.nexus_encrypt_stream_finalize(cryptoCtx, nullptr, 0, outPtrPtr, outLenPtr);
      if (res != 0) throw NexusException('Streaming encryption finalize failed: $res');

      if (outLenPtr.value > 0) {
        res = _native.nexus_encode_stream_push_fec(encoderCtx, outPtrPtr.value, outLenPtr.value);
        _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
        if (res < 0) throw NexusException('Final encoding push failed: $res');
      }

      res = _native.nexus_encode_stream_finalize(encoderCtx);
      if (res != 0) throw NexusException('Streaming encoding finalize failed: $res');

      while (true) {
        final popRes = _native.nexus_encode_stream_pop_frame(encoderCtx, outPtrPtr, outLenPtr);
        if (popRes == 1) break;
        if (popRes != 0) throw NexusException('Final frame generation failed: $popRes');

        frameCount++;
        final frameFile = File('${framesDir.path}/frame_${frameCount.toString().padLeft(6, '0')}.png');
        await frameFile.writeAsBytes(outPtrPtr.value.asTypedList(outLenPtr.value));
        _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
      }

      if (frameCount < 90) {
        final lastFrameFile = File('${framesDir.path}/frame_${frameCount.toString().padLeft(6, '0')}.png');
        if (await lastFrameFile.exists()) {
          final lastFrameData = await lastFrameFile.readAsBytes();
          for (int j = frameCount + 1; j <= 90; j++) {
            final padFile = File('${framesDir.path}/frame_${j.toString().padLeft(6, '0')}.png');
            await padFile.writeAsBytes(lastFrameData);
          }
          frameCount = 90;
        }
      }

      final taskConfig = await TaskConfig.forDevice();
      
      await _updateStatus(taskId, 0.6, 'Assembling video...', fileName: fileName);
      
      videoFile = File('${framesDir.path}/out.mp4');
      // Using libx264 with crf 12 and High profile for a balance of quality and standard compliance
      final swCommand = '-threads ${taskConfig.ffmpegThreads} -framerate 30 -i ${framesDir.path}/frame_%06d.png -c:v libx264 -crf 12 -preset ${taskConfig.ffmpegPreset} -profile:v high -level 4.1 -pix_fmt yuv420p -movflags +faststart -y ${videoFile.path}';
      final hwCommand = '-threads ${taskConfig.ffmpegThreads} -framerate 30 -i ${framesDir.path}/frame_%06d.png -c:v h264_mediacodec -b:v 15M -profile:v high -pix_fmt yuv420p -movflags +faststart -y ${videoFile.path}';
      
      if (await FlutterForegroundTask.isRunningService) {
        FFmpegKitConfig.setSessionHistorySize(0);
        FFmpegKitConfig.enableLogCallback(null);
        FFmpegKitConfig.enableStatisticsCallback(null);
      }
      
      AppLogger.info('NexusService: Trying hardware encode...');
      var session = await FFmpegKit.execute(hwCommand);
      var rc = await session.getReturnCode();
      
      if (!ReturnCode.isSuccess(rc)) {
        AppLogger.warn('NexusService: Hardware encode failed. Falling back to software encode: $swCommand');
        session = await FFmpegKit.execute(swCommand);
        rc = await session.getReturnCode();
        if (!ReturnCode.isSuccess(rc)) {
          final logs = await session.getLogs();
          var logMsg = logs.map((l) => l.getMessage()).join('\n');
          if (logMsg.length > 500) logMsg = logMsg.substring(0, 500);
          throw NexusException('FFmpeg Assembly Failed: $logMsg');
        }
      }

      final videoId = await _youtube.uploadVideo(
        videoFile: videoFile,
        title: 'Nexus Data ($fileName)',
        description: 'Secure Nexus Storage Object',
        onProgress: (p) => _updateStatus(taskId, 0.7 + (p * 0.3), 'Uploading...', fileName: fileName),
      );

      if (videoId == null) throw NexusException('YouTube upload failed');

      final record = FileRecord(
        path: fileName,
        videoId: videoId,
        size: fileSize,
        hash: 'streaming-hash-placeholder',
        key: password,
        lastUpdate: DateTime.now().toIso8601String(),
        starred: false,
        sha256: '',
        fileKey: '',
        isArchive: false,
        hasCustomPassword: password.isNotEmpty,
        customPasswordHint: '',
        mode: 'base',
      );

      await _db.saveFile(record);
      if (await FlutterForegroundTask.isRunningService) {
        await _db.checkpointWAL();
        FlutterForegroundTask.sendDataToMain('refresh');
      }
      try {
        await SyncService().pushDatabase();
      } catch (e) {
        AppLogger.warn('Auto-sync failed: $e');
      }
      await _updateStatus(taskId, 1.0, 'completed', fileName: fileName);
      AppLogger.info('Upload complete for $fileName');
    } catch (e, s) {
      AppLogger.error('Upload Error: $e', e, s);
      await _updateStatus(taskId, 0.0, 'Failed', fileName: fileName);
      rethrow;
    } finally {
      if (cryptoCtx != null) _native.nexus_crypto_stream_drop(cryptoCtx);
      if (encoderCtx != null) _native.nexus_encoder_stream_drop(encoderCtx);
      if (keyPtr != null) malloc.free(keyPtr);
      if (noncePrefixPtr != null) malloc.free(noncePrefixPtr);
      if (outPtrPtr != null) malloc.free(outPtrPtr);
      if (outLenPtr != null) malloc.free(outLenPtr);
      if (framesDir != null && await framesDir.exists()) await framesDir.delete(recursive: true);
      if (videoFile != null && await videoFile.exists()) await videoFile.delete();
    }
  }

  /// Utility to derive a 32-byte key from a password string.
  Future<Uint8List> _deriveKeyFromPassword(String password) async {
    final passPtr = password.toNativeUtf8().cast<Char>();
    final outPtrPtr = malloc<Pointer<Uint8>>();
    final outLenPtr = malloc<Size>();
    final salt = Uint8List(16)..fillRange(0, 16, 0x42); 
    final saltPtr = malloc<Uint8>(16);
    saltPtr.asTypedList(16).setAll(0, salt);

    try {
      final res = _native.nexus_derive_master_key(passPtr, password.length, saltPtr, 16, outPtrPtr, outLenPtr);
      if (res != 0) throw NexusException('Key derivation failed');
      final result = Uint8List.fromList(outPtrPtr.value.asTypedList(outLenPtr.value));
      _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
      return result;
    } finally {
      malloc.free(passPtr);
      malloc.free(saltPtr);
      malloc.free(outPtrPtr);
      malloc.free(outLenPtr);
    }
  }

  /// Download from YouTube, extract frames, and decrypt using a streaming pipeline.
  Future<void> downloadAndDecrypt(FileRecord record, String password, {String? explicitTaskId}) async {
    final taskId = explicitTaskId ?? DateTime.now().millisecondsSinceEpoch.toString();
    final fileName = record.path.split('/').last;

    File? videoFile;
    Directory? framesDir;
    RandomAccessFile? ios;
    Pointer<StreamingDecoder>? decoderCtx;
    Pointer<StreamingContext>? cryptoCtx;
    Pointer<Uint8>? keyPtr;
    Pointer<Pointer<Uint8>>? outPtrPtr;
    Pointer<Size>? outLenPtr;

    try {
      await _db.insertTask({
        'id': taskId,
        'type': 2, // Download
        'file_path': record.path,
        'status': 'Starting download...',
        'progress': 0.05,
        'created_at': DateTime.now().toIso8601String(),
      });
      if (await FlutterForegroundTask.isRunningService) {
        await _db.checkpointWAL();
        FlutterForegroundTask.sendDataToMain('refresh');
      }

      await _updateStatus(taskId, 0.05, 'Starting download...', fileName: fileName);

      AppLogger.info('Starting streaming download for $fileName (ID: ${record.videoId}) Mode: ${record.mode}');

      // Fix 4 & 1: Detect mode and prefer WebM for High mode
      final isHighMode = record.mode == 'high';
      
      videoFile = await _youtube.downloadVideo(
        record.videoId,
        isHighMode: isHighMode,
        onProgress: (p) => _updateStatus(taskId, 0.1 + (p * 0.3), 'Downloading...', fileName: fileName),
      );
      if (videoFile == null) {
        AppLogger.error('NexusService: YouTube download returned null. Aborting.');
        throw NexusException('YouTube download failed');
      }

      final videoSize = await videoFile.length();
      AppLogger.info('NexusService: Downloaded video size: $videoSize bytes');
      if (videoSize == 0) throw NexusException('Downloaded video is empty (0 bytes)');
      if (!await videoFile.exists()) throw NexusException('Video file missing after download');

      final tmpDir = await getTemporaryDirectory();
      framesDir = Directory('${tmpDir.path}/nexus-dl-$taskId');
      if (await framesDir.exists()) await framesDir.delete(recursive: true);
      await framesDir.create();

      await _updateStatus(taskId, 0.4, 'Analyzing video...', fileName: fileName);

      final probeSession = await FFprobeKit.getMediaInformation(videoFile.path);
      final mediaInformation = probeSession.getMediaInformation();
      
      if (mediaInformation != null) {
        final streams = mediaInformation.getStreams();
        int? width, height;
        for (var stream in streams) {
          if (stream.getType() == 'video') {
            final props = stream.getAllProperties();
            width = int.tryParse(props?['width']?.toString() ?? '');
            height = int.tryParse(props?['height']?.toString() ?? '');
            break;
          }
        }
        
          AppLogger.info('NexusService: Video resolution detected: ${width}x$height');
        
        if (width != null && height != null) {
          // Validation de sécurité pour éviter le décodage de bruit (ex: 144p)
          if (!isHighMode && (width < 640 || height < 360)) { // Seuil minimal absolu pour Base (idéal 720p)
            throw NexusException('Qualité insuffisante : ${width}x$height. Le mode Base requiert au moins du 360p (idéal 720p).');
          }
          // (debug) frames retention handled later per-frame during decoding loop
          if (isHighMode && (width < 1280 || height < 720)) {
            throw NexusException('Qualité insuffisante pour le mode High : ${width}x$height. 720p minimum requis.');
          }
           
          if (width != 1280 || height != 720) {
            if (!isHighMode) {
                AppLogger.warn('NexusService: Resolution mismatch (Base). Expected 1280x720, got ${width}x$height. FFmpeg will rescale.');
            }
          }
        }
      }

      final targetWidth = isHighMode ? 3840 : 1280;
      final targetHeight = isHighMode ? 2160 : 720;

      // Force scaling to target resolution with neighbor flags to maintain block alignment
      // Improvement: Force 16:9 aspect ratio with increase/crop to avoid black bars on unusual YT sources
      // 'flags=neighbor' is attached to 'scale' specifically to ensure block alignment doesn't get smoothed out
      // Use grayscale extraction to match the encoder's Luma frames and avoid color conversion artifacts
      final ffmpegCommand = '-i "${videoFile.path}" -vf "scale=$targetWidth:$targetHeight:force_original_aspect_ratio=increase:flags=neighbor,crop=$targetWidth:$targetHeight,format=gray" -vsync 0 "${framesDir.path}/frame_%06d.png"';
      AppLogger.info('NexusService: Running Native FFmpeg command: $ffmpegCommand');
      
      final startTime = DateTime.now();
      final session = await FFmpegKit.execute(ffmpegCommand);
      final returnCode = await session.getReturnCode();
      final duration = DateTime.now().difference(startTime);
      
      AppLogger.info('NexusService: FFmpeg finished in ${duration.inSeconds}s with return code: $returnCode');

      if (!ReturnCode.isSuccess(returnCode)) {
        final logs = await session.getLogs();
        var logMsg = logs.map((l) => l.getMessage()).join('\n');
        AppLogger.error('NexusService: FFmpeg failed. Logs: ${logMsg.length > 1000 ? logMsg.substring(0, 1000) : logMsg}');
        if (logMsg.length > 500) logMsg = logMsg.substring(0, 500);
        throw NexusException('FFmpeg Extraction Failed: $logMsg');
      }

      AppLogger.info('NexusService: Listing frames in ${framesDir.path}');
      final framesList = framesDir.listSync();
      final frames = framesList
          .where((e) => e.path.endsWith('.png'))
          .map((e) => e.path)
          .toList()..sort();
      
      AppLogger.info('NexusService: ${frames.length} frames found after extraction');
      if (frames.isEmpty) {
        final logs = await session.getLogs();
        final logTail = logs.reversed.take(20).map((l) => l.getMessage()).join('\n');
        AppLogger.error('NexusService: No frames extracted. FFmpeg Log Tail:\n$logTail');
        throw NexusException('No frames extracted from video');
      }

      await _updateStatus(taskId, 0.45, 'Preparing decoder...', fileName: fileName);

      final keyBytes = await _deriveKeyFromPassword(password);
      keyPtr = malloc<Uint8>(32);
      keyPtr.asTypedList(32).setAll(0, keyBytes);

      // Initialize decoder with the correct mode
      decoderCtx = _native.nexus_decode_stream_init(isHighMode ? 1 : 0);
      if (decoderCtx == nullptr) throw NexusException('Failed to init decoder');

      outPtrPtr = malloc<Pointer<Uint8>>();
      outLenPtr = malloc<Size>();
      
      final downloadsDir = await _getPublicDownloadsDirectory();
      var finalFile = File('${downloadsDir.path}/$fileName');
      int counter = 1;
      while (await finalFile.exists()) {
          final nameParts = fileName.split('.');
          final ext = nameParts.length > 1 ? '.${nameParts.removeLast()}' : '';
          final baseName = nameParts.join('.');
          finalFile = File('${downloadsDir.path}/$baseName ($counter)$ext');
          counter++;
      }
      ios = await finalFile.open(mode: FileMode.write);
      AppLogger.info('NexusService: Output file opened: ${finalFile.path}');

      bool initializedCrypto = false;
      Uint8List nonceBuffer = Uint8List(0);

      int totalDecryptedBytes = 0;
      bool headerInspected = false;

      for (int i = 0; i < frames.length; i++) {
        final frameFile = File(frames[i]);
        final frameData = await frameFile.readAsBytes();
        
        if (i == 0 || i == frames.length - 1) {
          AppLogger.info('NexusService: Processing frame $i/${frames.length}, size: ${frameData.length} bytes');
        }

        final framePtr = malloc<Uint8>(frameData.length);
        framePtr.asTypedList(frameData.length).setAll(0, frameData);

        if (i % 20 == 0) {
            await _updateStatus(taskId, 0.4 + (i / frames.length * 0.5), 'Decoding frame $i...', fileName: fileName);
        }

        int res = _native.nexus_decode_stream_push_fec(decoderCtx, framePtr, frameData.length);
        malloc.free(framePtr);
        
        // Debug option: keep frames for inspection when diagnosing decode/decrypt issues
        if (!NexusService.keepFramesForDebug) {
          try {
            await frameFile.delete();
          } catch (e) {
            AppLogger.warn('Failed to delete temporary frame: $e');
          }
        } else {
          AppLogger.info('NexusService: Keeping frame for debug: ${frameFile.path}');
        }

        if (res != 0) throw NexusException('Frame push error at frame $i: $res');

        while (true) {
          final popRes = _native.nexus_decode_stream_pop(decoderCtx, outPtrPtr, outLenPtr);
          if (popRes == 1) break; // Need more data
          if (popRes != 0) throw NexusException('Frame pop error at frame $i: $popRes');

          final decodedBytes = outPtrPtr.value.asTypedList(outLenPtr.value);
          if (i < 3) {
            final hex = decodedBytes.take(16).map((b) => b.toRadixString(16).padLeft(2, '0')).join(' ');
            AppLogger.info('NexusService: Decoder frame $i produced ${outLenPtr.value} bytes. First 16: $hex');
          } else {
            AppLogger.info('NexusService: Decoder produced ${outLenPtr.value} bytes');
          }

          if (!initializedCrypto) {
            nonceBuffer = Uint8List.fromList([...nonceBuffer, ...decodedBytes]);
            if (nonceBuffer.length < 16) {
              _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
              continue;
            }

            final noncePrefix = nonceBuffer.sublist(0, 16);
            final nonceHex = noncePrefix.map((b) => b.toRadixString(16).padLeft(2, '0')).join(' ');
            AppLogger.info('NexusService: Nonce extracted ($nonceHex), initializing crypto stream');
            
            final noncePtr = malloc<Uint8>(16);
            noncePtr.asTypedList(16).setAll(0, noncePrefix);

            cryptoCtx = _native.nexus_decrypt_stream_init(keyPtr, noncePtr);
            malloc.free(noncePtr);
            if (cryptoCtx == nullptr) throw NexusException('Crypto stream init failed (NULL)');

            initializedCrypto = true;

            if (nonceBuffer.length > 16) {
              final remainingData = nonceBuffer.sublist(16);
              final remaining = malloc<Uint8>(remainingData.length);
              remaining.asTypedList(remainingData.length).setAll(0, remainingData);

              final decPtrPtr = malloc<Pointer<Uint8>>();
              final decLenPtr = malloc<Size>();
              final hex1 = remainingData.take(16).map((b) => b.toRadixString(16).padLeft(2, '0')).join(' ');
              AppLogger.info('NexusService: About to decrypt ${remainingData.length} bytes (post-nonce). First 16: $hex1');
              final decRes = _native.nexus_decrypt_stream_update(cryptoCtx, remaining, remainingData.length, decPtrPtr, decLenPtr);
              malloc.free(remaining);
              AppLogger.info('NexusService: Decrypt returned: res=$decRes, outLen=${decLenPtr.value}');

              if (decRes == 0 && decLenPtr.value > 0) {
                final decData = decPtrPtr.value.asTypedList(decLenPtr.value);
                if (!headerInspected && decData.isNotEmpty) {
                   final hex = decData.take(16).map((e) => e.toRadixString(16).padLeft(2, '0')).join(' ');
                   AppLogger.info('NexusService: First decrypted bytes: $hex');
                   headerInspected = true;
                }
                totalDecryptedBytes += decLenPtr.value;
                await ios.writeFrom(decData);
              }
              if (decRes == 0) {
                if (decLenPtr.value == 0) AppLogger.info('NexusService: Decryption update returned 0 bytes');
                _native.nexus_free_bytes(decPtrPtr.value, decLenPtr.value);
              }
              malloc.free(decPtrPtr);
              malloc.free(decLenPtr);
              if (decRes != 0) throw NexusException('Initial decryption update error: $decRes');
            }
          } else {
            final decPtrPtr = malloc<Pointer<Uint8>>();
            final decLenPtr = malloc<Size>();
            final decRes = _native.nexus_decrypt_stream_update(cryptoCtx!, outPtrPtr.value, outLenPtr.value, decPtrPtr, decLenPtr);
            
            if (decRes == 0 && decLenPtr.value > 0) {
                final decData = decPtrPtr.value.asTypedList(decLenPtr.value);
                if (!headerInspected && decData.isNotEmpty) {
                   final hex = decData.take(16).map((e) => e.toRadixString(16).padLeft(2, '0')).join(' ');
                   AppLogger.info('NexusService: First decrypted bytes: $hex');
                   headerInspected = true;
                }
                totalDecryptedBytes += decLenPtr.value;
                await ios.writeFrom(decData);
            }
            if (decRes == 0) _native.nexus_free_bytes(decPtrPtr.value, decLenPtr.value);
            malloc.free(decPtrPtr);
            malloc.free(decLenPtr);
            if (decRes != 0) throw NexusException('Decryption update error: $decRes at frame $i');
          }

          _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
        }
      }

      AppLogger.info('NexusService: Decryption loop finished. Total bytes decrypted: $totalDecryptedBytes. Initializing finalization.');

      if (cryptoCtx != null) {
          final decPtrPtr = malloc<Pointer<Uint8>>();
          final decLenPtr = malloc<Size>();
          AppLogger.info('NexusService: Finalizing crypto stream...');
          final finRes = _native.nexus_decrypt_stream_finalize(cryptoCtx, nullptr, 0, decPtrPtr, decLenPtr);
          
          if (finRes == 0) {
            if (decLenPtr.value > 0) {
              await ios.writeFrom(decPtrPtr.value.asTypedList(decLenPtr.value));
              totalDecryptedBytes += decLenPtr.value;
            }
            _native.nexus_free_bytes(decPtrPtr.value, decLenPtr.value);
            AppLogger.info('NexusService: Finalization successful. Total final bytes: $totalDecryptedBytes');
          } else {
            AppLogger.error('NexusService: Finalization failed with error code: $finRes');
            malloc.free(decPtrPtr);
            malloc.free(decLenPtr);
            throw NexusException('Decryption finalization failed (Code: $finRes). Data might be corrupted or password incorrect.');
          }
          malloc.free(decPtrPtr);
          malloc.free(decLenPtr);
      }

      try {
        await ios.close();
      } catch (e) {
        AppLogger.warn('NexusService: Failed to close output file (ignored): $e');
      }
      final finalSize = await finalFile.length();
      AppLogger.info('NexusService: Decryption complete. Final file size: $finalSize bytes');
      if (finalSize == 0) throw NexusException('Final file is empty. Decryption might have failed.');
      
      await _updateStatus(taskId, 1.0, 'completed', fileName: fileName);
    } catch (e, s) {
      AppLogger.error('STREAM DOWNLOAD ERROR: $e', e, s);
      await _updateStatus(taskId, 0.0, 'Failed', fileName: fileName);
      rethrow;
    } finally {
      try {
        await ios!.close();
      } catch (e) {
        AppLogger.warn('NexusService: Ignored error closing output file in finally: $e');
      }
      if (decoderCtx != null) _native.nexus_decoder_stream_drop(decoderCtx);
      if (cryptoCtx != null) _native.nexus_crypto_stream_drop(cryptoCtx);
      if (keyPtr != null) malloc.free(keyPtr);
      if (outPtrPtr != null) malloc.free(outPtrPtr);
      if (outLenPtr != null) malloc.free(outLenPtr);
      if (framesDir != null && await framesDir.exists()) await framesDir.delete(recursive: true);
      if (videoFile != null && await videoFile.exists()) await videoFile.delete();
    }
  }

  Future<Directory> _getPublicDownloadsDirectory() async {
    if (Platform.isAndroid) {
      final dir = Directory('/storage/emulated/0/Download/NexusStorage');
      try {
        if (!await dir.exists()) await dir.create(recursive: true);
        return dir;
      } catch (e) {
        AppLogger.error('Failed to create public downloads dir: $e');
        return await getTemporaryDirectory();
      }
    } else {
      final dir = await getDownloadsDirectory();
      return dir ?? await getApplicationDocumentsDirectory();
    }
  }
}
