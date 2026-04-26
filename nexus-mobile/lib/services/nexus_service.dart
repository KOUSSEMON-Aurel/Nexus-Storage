import 'dart:ffi';
import 'package:flutter/services.dart' hide Size;
import 'package:crypto/crypto.dart';
import 'package:convert/convert.dart';
import 'dart:io';
import 'dart:typed_data';
import 'package:ffi/ffi.dart';
import 'package:path_provider/path_provider.dart';
import 'package:ffmpeg_kit_flutter_new/ffmpeg_kit.dart';
import 'package:ffmpeg_kit_flutter_new/return_code.dart';
import 'package:flutter_foreground_task/flutter_foreground_task.dart';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import 'package:ffmpeg_kit_flutter_new/ffmpeg_kit_config.dart';
import 'package:ffmpeg_kit_flutter_new/session_state.dart';
import '../ffi/nexus_bindings.dart';
import '../ffi/nexus_loader.dart';
import 'database_service.dart';
import 'youtube_service.dart';
import 'sync_service.dart';
import 'package:nexus_mobile/services/auth_service.dart';
import '../models/file_record.dart';
import 'logger_service.dart';
import '../utils/exceptions.dart';
import '../core/task_config.dart';
import '../core/transfer_state.dart';

class NexusService {
  final NexusCoreBindings _native = NexusLoader.bindings;
  final DatabaseService _db = DatabaseService();
  static const _mediaChannel = MethodChannel('com.aurel.nexus/media_store');
  final YouTubeService _youtube = YouTubeService();
  final AuthService _auth = AuthService();
  DateTime _lastRefreshTime = DateTime.fromMillisecondsSinceEpoch(0);
  DateTime _lastProgressUpdate = DateTime.fromMillisecondsSinceEpoch(0);
  static const bool keepFramesForDebug = false;

  static bool? _isEmulatorCache;
  Future<bool> _isEmulator() async {
    if (_isEmulatorCache != null) return _isEmulatorCache!;
    _isEmulatorCache = false;
    if (Platform.isAndroid) {
      try {
        final result = await Process.run('getprop', ['ro.product.model']);
        final model = result.stdout.toString().toLowerCase();
        if (model.contains('sdk_gphone') ||
            model.contains('emulator') ||
            model.contains('google_sdk')) {
          _isEmulatorCache = true;
        }
      } catch (_) {}
    }
    return _isEmulatorCache!;
  }

  Future<void> _updateStatus(
    String taskId,
    double progress,
    String status, {
    String? fileName,
  }) async {
    final now = DateTime.now();
    if (now.difference(_lastProgressUpdate).inMilliseconds < 500 &&
        status != 'completed' &&
        !status.startsWith('Failed') &&
        progress > 0.05 &&
        progress < 1.0) {
      return;
    }
    _lastProgressUpdate = now;

    await _db.updateTaskProgress(taskId, progress, status);
    if (await FlutterForegroundTask.isRunningService) {
      FlutterForegroundTask.updateService(
        notificationTitle: fileName != null
            ? 'Nexus: Transfer $fileName'
            : 'Nexus active task',
        notificationText: '$status (${(progress * 100).toInt()}%)',
      );

      final now = DateTime.now();
      if (now.difference(_lastRefreshTime).inMilliseconds > 1500 ||
          status == 'completed' ||
          status.startsWith('Failed')) {
        if (status == 'completed' || status.startsWith('Failed')) {
          await _db.checkpointWAL();
        }
        FlutterForegroundTask.sendDataToMain('refresh');
        _lastRefreshTime = now;
      }
    } else {
      // Fallback for when we're running in Android 14 direct mode
      final plugin = FlutterLocalNotificationsPlugin();
      await plugin.show(
        id: taskId.hashCode,
        title: fileName != null
            ? 'Nexus: Transfer $fileName'
            : 'Nexus active task',
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
      if (now.difference(_lastRefreshTime).inMilliseconds > 1500 ||
          status == 'completed' ||
          status.startsWith('Failed')) {
        await _db.checkpointWAL();
        _lastRefreshTime = now;
      }
    }
  }

  /// Encrypt and encode a file into frames, then upload using a streaming pipeline.
  Future<void> encodeAndUpload(
    File inputFile,
    String password, {
    String? explicitTaskId,
  }) async {
    TransferState.activeTaskCount++;
    final taskId =
        explicitTaskId ?? DateTime.now().millisecondsSinceEpoch.toString();
    final fileName = inputFile.path.split('/').last;
    final fileSize = await inputFile.length();

    Pointer<Uint8>? keyPtr;
    Pointer<Uint8>? noncePrefixPtr;
    Pointer<StreamingContext>? cryptoCtx;
    Pointer<StreamingEncoder>? encoderCtx;
    Pointer<Pointer<Uint8>>? outPtrPtr;
    Pointer<Size>? outLenPtr;
    Pointer<Uint8>? reusableChunkPtr;
    final maxChunkSize = 1024 * 1024; // Increased to 1MB for safety
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

      await _updateStatus(
        taskId,
        0.05,
        'Initializing stream...',
        fileName: fileName,
      );

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
      if (cryptoCtx == nullptr) {
        throw NexusException('Failed to init crypto stream');
      }

      encoderCtx = _native.nexus_encode_stream_init(0);
      if (encoderCtx == nullptr) {
        throw NexusException('Failed to init encoder stream');
      }

      outPtrPtr = malloc<Pointer<Uint8>>();
      outLenPtr = malloc<Size>();

      // CRITICAL: Add nonce_prefix to FEC stream so downloader can extract it
      int nonceRes = _native.nexus_encode_stream_push_fec(
        encoderCtx,
        noncePrefixPtr,
        16,
      );
      if (nonceRes < 0) {
        throw NexusException('Failed to push nonce to FEC encoder: $nonceRes');
      }
      AppLogger.info('NexusService: Added nonce to FEC stream');

      int frameCount = 0;
      int processedBytes = 0;

      reusableChunkPtr = malloc<Uint8>(maxChunkSize);

      final taskConfig = await TaskConfig.forDevice();
      bool forceSoftware = await _isEmulator();
      if (forceSoftware) {
        AppLogger.info(
          'NexusService: Emulator detected, forcing software encode.',
        );
      }

      String? pipePath;
      for (int retry = 0; retry < 3; retry++) {
        pipePath = await FFmpegKitConfig.registerNewFFmpegPipe();
        if (pipePath != null && pipePath.isNotEmpty) break;
        AppLogger.warn(
          'NexusService: FFmpeg pipe registration failed, retrying ($retry/3)...',
        );
        await Future.delayed(const Duration(milliseconds: 500));
      }

      if (pipePath == null || pipePath.isEmpty) {
        throw NexusException('Failed to create FFmpeg pipe (Context Error)');
      }

      AppLogger.info('NexusService: FFmpeg pipe registered: $pipePath');
      videoFile = File('${framesDir.path}/out.mp4');

      final videoSize = '1280x720';
      final swCommand =
          '-threads ${taskConfig.ffmpegThreads} -f rawvideo -vcodec rawvideo -pix_fmt gray -s $videoSize -r 30 -i $pipePath -c:v mpeg4 -b:v 8M -maxrate 10M -bufsize 20M -pix_fmt yuv420p -color_range pc -y ${videoFile.path}';
      final hwCommand =
          '-threads ${taskConfig.ffmpegThreads} -f rawvideo -vcodec rawvideo -pix_fmt gray -s $videoSize -r 30 -i $pipePath -c:v h264_mediacodec -b:v 8M -maxrate 10M -bufsize 20M -profile:v high -pix_fmt yuv420p -color_range pc -y ${videoFile.path}';

      if (await FlutterForegroundTask.isRunningService) {
        FFmpegKitConfig.setSessionHistorySize(10);
        FFmpegKitConfig.enableLogCallback((log) {
          AppLogger.error('[ffmpeg] ${log.getMessage()}');
        });
        FFmpegKitConfig.enableStatisticsCallback(null);
      }

      AppLogger.info('NexusService: Starting FFmpeg pipe encode session...');
      final sessionPromise = FFmpegKit.executeAsync(
        forceSoftware ? swCommand : hwCommand,
      );
      final pipeSink = File(pipePath).openWrite();

      Uint8List? lastFrameData;

      final sw = Stopwatch()..start();
      final sha256Sink = AccumulatorSink<Digest>();
      final sha256Input = sha256.startChunkedConversion(sha256Sink);

      await for (final chunk in inputFile.openRead()) {
        sha256Input.add(chunk);

        final result = _processEncodeChunkIsolate({
          'cryptoCtx': cryptoCtx.address,
          'encoderCtx': encoderCtx.address,
          'chunk': chunk,
        });

        final frames = result['frames'] as List<Uint8List>;
        for (final frame in frames) {
          frameCount++;
          pipeSink.add(frame);
          await pipeSink.flush();
          await Future.delayed(Duration.zero);
        }

        processedBytes += chunk.length;
        final progress = 0.1 + (processedBytes / fileSize * 0.4);
        await _updateStatus(
          taskId,
          progress,
          'Processing data...',
          fileName: fileName,
        );
      }

      AppLogger.info(
        'NEXUS_PERF: OpenRead & Stream Encode: ${sw.elapsedMilliseconds}ms',
      );
      sw.reset();
      sw.start();

      final result = _processEncodeFinalizeIsolate({
        'cryptoCtx': cryptoCtx.address,
        'encoderCtx': encoderCtx.address,
      });

      final frames = result['frames'] as List<Uint8List>;
      for (final frame in frames) {
        frameCount++;
        lastFrameData = frame;
        pipeSink.add(frame);
        await Future.delayed(Duration.zero);
      }

      AppLogger.info(
        'NEXUS_PERF: Finalize Encode & Frames: ${sw.elapsedMilliseconds}ms',
      );
      sw.reset();
      sw.start();

      if (frameCount < 90 && lastFrameData != null) {
        for (int j = frameCount + 1; j <= 90; j++) {
          pipeSink.add(lastFrameData);
        }
        frameCount = 90;
      }

      await pipeSink.close();
      await FFmpegKitConfig.closeFFmpegPipe(pipePath);

      await _updateStatus(
        taskId,
        0.6,
        'Finishing video assembly...',
        fileName: fileName,
      );

      AppLogger.info('NexusService: Waiting for FFmpeg session to finish...');
      var session = await sessionPromise;
      // Wait for session to finish
      while (await session.getState() != SessionState.completed &&
          await session.getState() != SessionState.failed) {
        await Future.delayed(Duration(milliseconds: 200));
      }
      var rc = await session.getReturnCode();
      final rcValue = rc == null ? -1 : rc.getValue();
      AppLogger.info(
        'NexusService: FFmpeg session finished with ReturnCode: $rcValue',
      );

      if (!ReturnCode.isSuccess(rc)) {
        final logs = await session.getLogs();
        var logMsg = logs.map((l) => l.getMessage()).join('\n');
        if (logMsg.isEmpty) {
          logMsg =
              "No logs captured (check adb for [ffmpeg] tags). RC: $rcValue";
        } else if (logMsg.length > 2000) {
          logMsg = logMsg.substring(logMsg.length - 2000);
        }
        throw NexusException('FFmpeg Assembly Failed: $logMsg');
      }

      AppLogger.info(
        'NEXUS_PERF: FFmpeg Assembly: ${sw.elapsedMilliseconds}ms',
      );
      sw.reset();
      sw.start();

      final videoId = await _youtube.uploadVideo(
        videoFile: videoFile,
        title: 'Nexus Data ($fileName)',
        description: 'Secure Nexus Storage Object',
        onProgress: (p) => _updateStatus(
          taskId,
          0.7 + (p * 0.3),
          'Uploading...',
          fileName: fileName,
        ),
      );

      if (videoId == null) throw NexusException('YouTube upload failed');

      AppLogger.info('NEXUS_PERF: YouTube Upload: ${sw.elapsedMilliseconds}ms');

      sha256Input.close();
      final fileSha256 = sha256Sink.events.single.toString();

      final record = FileRecord(
        path: fileName,
        videoId: videoId,
        size: fileSize,
        hash: fileSha256.substring(0, 16),
        key: password,
        lastUpdate: DateTime.now().toIso8601String(),
        starred: false,
        sha256: fileSha256,
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
      if (reusableChunkPtr != null) malloc.free(reusableChunkPtr);
      if (keyPtr != null) malloc.free(keyPtr);
      if (noncePrefixPtr != null) malloc.free(noncePrefixPtr);
      if (outPtrPtr != null) malloc.free(outPtrPtr);
      if (outLenPtr != null) malloc.free(outLenPtr);
      if (framesDir != null && await framesDir.exists()) {
        await framesDir.delete(recursive: true);
      }
      if (videoFile != null && await videoFile.exists()) {
        await videoFile.delete();
      }
      TransferState.activeTaskCount--;
    }
  }

  static final Map<String, Uint8List> _keyCache = {};

  /// Utility to derive a 32-byte key from a password string.
  Future<Uint8List> _deriveKeyFromPassword(String password) async {
    final googleSub = _auth.googleSub ?? '';
    final combinedPassword = googleSub + password;

    if (_keyCache.containsKey(combinedPassword)) {
      AppLogger.info('NexusService: Using cached key for derivation');
      return _keyCache[combinedPassword]!;
    }

    if (combinedPassword.isEmpty) {
      AppLogger.warn(
        'NexusService: Both googleSub and password are empty during derivation',
      );
    } else if (password.isNotEmpty) {
      AppLogger.info(
        'NexusService: Using double-encryption (googleSub + custom password)',
      );
    } else {
      AppLogger.info('NexusService: Using googleSub as default master key');
    }

    final passPtr = combinedPassword.toNativeUtf8().cast<Char>();
    final outPtrPtr = malloc<Pointer<Uint8>>();
    final outLenPtr = malloc<Size>();
    final salt = Uint8List(16)..fillRange(0, 16, 0x42);
    final saltPtr = malloc<Uint8>(16);
    saltPtr.asTypedList(16).setAll(0, salt);

    try {
      final res = _native.nexus_derive_master_key(
        passPtr,
        password.length,
        saltPtr,
        16,
        outPtrPtr,
        outLenPtr,
      );
      if (res != 0) throw NexusException('Key derivation failed');
      final result = Uint8List.fromList(
        outPtrPtr.value.asTypedList(outLenPtr.value),
      );
      _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);

      _keyCache[combinedPassword] = result;
      return result;
    } finally {
      malloc.free(passPtr);
      malloc.free(saltPtr);
      malloc.free(outPtrPtr);
      malloc.free(outLenPtr);
    }
  }

  /// Download from YouTube, extract frames, and decrypt using a streaming pipeline.
  Future<void> downloadAndDecrypt(
    FileRecord record,
    String password, {
    String? explicitTaskId,
  }) async {
    TransferState.activeTaskCount++;
    final taskId =
        explicitTaskId ?? DateTime.now().millisecondsSinceEpoch.toString();
    final fileName = record.path.split('/').last;

    File? tempFile;
    RandomAccessFile? ios;
    Pointer<StreamingDecoder>? decoderCtx;
    Pointer<StreamingContext>? cryptoCtx;
    Pointer<Uint8>? keyPtr;

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

      await _updateStatus(
        taskId,
        0.05,
        'Starting download...',
        fileName: fileName,
      );

      AppLogger.info(
        'Starting streaming download for $fileName (ID: ${record.videoId}) Mode: ${record.mode}',
      );

      final isHighMode = record.mode == 'high';
      final targetWidth = isHighMode ? 3840 : 1280;
      final targetHeight = isHighMode ? 2160 : 720;

      AppLogger.info(
        'NexusService: Starting Sequential Pipeline. Target: ${targetWidth}x$targetHeight',
      );

      final videoStream = await _youtube.getVideoStream(record.videoId);
      if (videoStream == null) throw NexusException('YouTube stream failed');

      // Phase 1: Download strictly to temp file
      final cacheDir = await getTemporaryDirectory();
      final videoFile = File('${cacheDir.path}/nexus_dl_tmp_$taskId.mp4');
      if (await videoFile.exists()) await videoFile.delete();

      AppLogger.info(
        'NexusService: Phase 1 - Downloading to ${videoFile.path}',
      );
      final fileSink = videoFile.openWrite();

      // Use addStream for automatic backpressure management
      // This prevents the network from overwhelming the disk, stabilizing RAM and CPU.
      await _updateStatus(
        taskId,
        0.10,
        'Downloading video data...',
        fileName: fileName,
      );

      await fileSink.addStream(videoStream);
      await fileSink.flush();
      await fileSink.close();

      final downloadedBytes = await videoFile.length();
      AppLogger.info('NexusService: Download complete: $downloadedBytes bytes');

      await _updateStatus(
        taskId,
        0.40,
        'Download complete. Starting decryption...',
        fileName: fileName,
      );

      // Phase 2: Decrypt from file
      String? outputPipePath;
      for (int retry = 0; retry < 3; retry++) {
        outputPipePath = await FFmpegKitConfig.registerNewFFmpegPipe();
        if (outputPipePath != null) break;
        await Future.delayed(const Duration(milliseconds: 500));
      }

      if (outputPipePath == null) {
        throw NexusException('Failed to create FFmpeg output pipe');
      }

      final ffmpegArgs = [
        '-i',
        videoFile.path,
        '-vf',
        'scale=$targetWidth:$targetHeight:force_original_aspect_ratio=increase:flags=neighbor,crop=$targetWidth:$targetHeight,format=gray',
        '-f',
        'rawvideo',
        '-pix_fmt',
        'gray',
        '-r',
        '30',
        '-color_range',
        'pc',
        '-y',
        outputPipePath,
      ];

      AppLogger.info(
        'NexusService: Running Native FFmpeg command array: $ffmpegArgs',
      );

      await _updateStatus(
        taskId,
        0.45,
        'Preparing decoder...',
        fileName: fileName,
      );

      final keyBytes = await _deriveKeyFromPassword(password);
      keyPtr = malloc<Uint8>(32);
      keyPtr.asTypedList(32).setAll(0, keyBytes);

      // Initialize decoder with the correct mode
      decoderCtx = _native.nexus_decode_stream_init(isHighMode ? 1 : 0);
      if (decoderCtx == nullptr) throw NexusException('Failed to init decoder');

      // Option B: Decrypt to a temporary file in the app's internal cache first.
      // This avoids permission issues with direct writing to public storage on Android 11+.
      final tempDir = await getTemporaryDirectory();
      tempFile = File('${tempDir.path}/nexus_dec_tmp_$taskId');
      if (await tempFile.exists()) await tempFile.delete();

      ios = await tempFile.open(mode: FileMode.write);
      AppLogger.info(
        'NexusService: Temporary output file opened: ${tempFile.path}',
      );

      int i = 0;

      // Enable logs for download as well to catch decoding/scaling errors
      FFmpegKitConfig.enableLogCallback((log) {
        final msg = log.getMessage();
        final lowerMsg = msg.toLowerCase();
        if (lowerMsg.contains('error') ||
            lowerMsg.contains('fail') ||
            lowerMsg.contains('invalid')) {
          AppLogger.error('[ffmpeg-download] $msg');
        } else if (i % 50 == 0) {
          // Log occasionally to avoid spamming
          AppLogger.info('[ffmpeg-download] $msg');
        }
      });

      final startTime = DateTime.now();
      final sessionPromise = FFmpegKit.executeWithArgumentsAsync(ffmpegArgs);

      AppLogger.info('NexusService: Reading frames from FFmpeg pipe...');
      final pipeStream = File(outputPipePath).openRead();

      bool initializedCrypto = false;
      Uint8List nonceBuffer = Uint8List(0);

      int totalDecryptedBytes = 0;

      final frameSize = targetWidth * targetHeight;
      final reusableFramePtr = malloc<Uint8>(frameSize);

      final byteBuffer = BytesBuilder(copy: false);

      await for (final chunk in pipeStream) {
        // Laisser l'UI Isolate traiter ses messages (évite Stop FGS timeout)
        await Future.delayed(Duration.zero);

        byteBuffer.add(chunk);

        while (byteBuffer.length >= frameSize) {
          final fullBytes = byteBuffer.takeBytes();
          final frameData = fullBytes.sublist(0, frameSize);
          if (fullBytes.length > frameSize) {
            byteBuffer.add(fullBytes.sublist(frameSize));
          }

          if (i == 0) {
            final hex = frameData
                .take(64)
                .map((b) => b.toRadixString(16).padLeft(2, '0'))
                .join(' ');
            AppLogger.info(
              'NexusService: Processing first frame, total bytes: ${frameData.length}',
            );
            AppLogger.info('NexusService: First 64 bytes (hex): $hex');

            // 2D Sampling Diagnostics
            AppLogger.info(
              'NexusService: Block sampling diagnostics (10x10 center pixels):',
            );
            for (int r = 0; r < 10; r++) {
              String rowStr = "";
              for (int c = 0; c < 10; c++) {
                final cx = c * 4 + 2;
                final cy = r * 4 + 2;
                final pixel = frameData[cy * targetWidth + cx];
                rowStr += pixel.toString().padLeft(4);
              }
              AppLogger.info('Row $r: $rowStr');
            }
          }

          if (i % 20 == 0) {
            // Estimate total frames: approx 2KB of source data per 720p frame in Base mode
            // For a 13MB file, it's about 6500 frames.
            final estimatedFrames = (record.size / 2000).clamp(100, 20000);
            await _updateStatus(
              taskId,
              0.4 + ((i / estimatedFrames) * 0.55).clamp(0.0, 0.55),
              'Decoding frame $i...',
              fileName: fileName,
            );
            await Future.delayed(Duration.zero);
          }

          final isolateResult = _processDecodeFrameIsolate({
            'decoderCtx': decoderCtx.address,
            'cryptoCtx': cryptoCtx?.address,
            'keyPtr': keyPtr.address,
            'frame': frameData,
            'initializedCrypto': initializedCrypto,
            'nonceBuffer': nonceBuffer,
          });

          final decDataList = isolateResult['decryptedData'] as List<Uint8List>;
          initializedCrypto = isolateResult['initializedCrypto'];
          nonceBuffer = isolateResult['nonceBuffer'];
          final int newCryptoAddr = isolateResult['cryptoCtxAddr'];
          if (newCryptoAddr != 0) {
            cryptoCtx = Pointer<StreamingContext>.fromAddress(newCryptoAddr);
          }

          for (final dataChunk in decDataList) {
            totalDecryptedBytes += dataChunk.length;
            await ios.writeFrom(dataChunk);
            await Future.delayed(Duration.zero);
          }
          i++;
        }
      }
      malloc.free(reusableFramePtr);
      await FFmpegKitConfig.closeFFmpegPipe(outputPipePath);

      final session = await sessionPromise;

      // Wait for FFmpeg to actually finish (EOF on pipe doesn't guarantee process termination)
      AppLogger.info(
        'NexusService: EOF reached on pipe, waiting for FFmpeg to exit...',
      );
      int waitTicks = 0;
      while (await session.getState() == SessionState.running ||
          await session.getState() == SessionState.created) {
        await Future.delayed(const Duration(milliseconds: 100));
        waitTicks++;
        if (waitTicks > 100) {
          AppLogger.warn(
            'NexusService: Timeout waiting for FFmpeg to exit (10s)',
          );
          break;
        }
      }

      final returnCode = await session.getReturnCode();
      final duration = DateTime.now().difference(startTime);

      AppLogger.info(
        'NexusService: FFmpeg extraction finished in ${duration.inSeconds}s (State: ${await session.getState()}, Code: $returnCode)',
      );

      if (!ReturnCode.isSuccess(returnCode)) {
        final logs = await session.getLogs();
        final fullLog = logs.map((l) => l.getMessage()).join('\n');

        // Prendre les 1000 derniers caractères pour avoir l'erreur réelle
        String errorContext = fullLog.length > 1000
            ? '...${fullLog.substring(fullLog.length - 1000)}'
            : fullLog;

        AppLogger.error(
          'NexusService: FFmpeg failed (code: $returnCode). Last logs:\n$errorContext',
        );
        throw NexusException('FFmpeg Extraction Failed (Code: $returnCode)');
      }

      if (i == 0) {
        throw NexusException('No frames extracted from video');
      }

      AppLogger.info(
        'NexusService: Decryption loop finished. Total bytes decrypted: $totalDecryptedBytes. Initializing finalization.',
      );

      if (cryptoCtx != null) {
        AppLogger.info('NexusService: Finalizing crypto stream...');
        final isolateResult = _processDecodeFinalizeIsolate({
          'cryptoCtx': cryptoCtx.address,
        });

        final finalData = isolateResult['finalData'] as Uint8List?;
        if (finalData != null) {
          totalDecryptedBytes += finalData.length;
          await ios.writeFrom(finalData);
          await Future.delayed(Duration.zero);
        }
        AppLogger.info(
          'NexusService: Finalization successful. Total final bytes: $totalDecryptedBytes',
        );
      }

      try {
        await ios.close();
        ios = null; // Important to avoid double close in finally

        AppLogger.info(
          'NexusService: Decryption finished. Exporting via Native MediaStore...',
        );

        // Option 2: Use Native Platform Channel for robust MediaStore handling
        // This avoids RAM issues and works without MANAGE_EXTERNAL_STORAGE
        final bool success = await _mediaChannel
            .invokeMethod('saveFileToDownloads', {
              'tempPath': tempFile.path,
              'fileName': fileName,
              'relativePath': 'NexusStorage',
            });

        if (success) {
          AppLogger.info(
            'NexusService: Successfully exported to public Downloads/NexusStorage via Native',
          );
        } else {
          AppLogger.error(
            'NexusService: Native MediaStore export returned false.',
          );
          throw NexusException(
            'Failed to save file to public storage via Native Channel',
          );
        }
      } catch (e) {
        AppLogger.error('NexusService: Final native export error: $e');
        rethrow;
      }

      final finalSize = await tempFile.length();
      AppLogger.info(
        'NexusService: Decryption complete. Temp file size: $finalSize bytes',
      );
      if (finalSize == 0) {
        throw NexusException(
          'Final file is empty. Decryption might have failed.',
        );
      }

      await _updateStatus(taskId, 1.0, 'completed', fileName: fileName);
    } catch (e, s) {
      AppLogger.error('STREAM DOWNLOAD ERROR: $e', e, s);
      await _updateStatus(taskId, 0.0, 'Failed', fileName: fileName);
      rethrow;
    } finally {
      try {
        if (ios != null) await ios.close();
      } catch (e) {
        AppLogger.warn(
          'NexusService: Ignored error closing output file in finally: $e',
        );
      }

      // Cleanup temp files
      if (tempFile != null && await tempFile.exists()) {
        try {
          await tempFile.delete();
          AppLogger.info('NexusService: Cleaned up temp decrypted file.');
        } catch (e) {
          AppLogger.warn('NexusService: Failed to delete temp file: $e');
        }
      }

      if (decoderCtx != null) _native.nexus_decoder_stream_drop(decoderCtx);
      if (cryptoCtx != null) _native.nexus_crypto_stream_drop(cryptoCtx);
      if (keyPtr != null) malloc.free(keyPtr);

      // Cleanup sequential file
      final cacheDir = await getTemporaryDirectory();
      final videoFile = File('${cacheDir.path}/nexus_dl_tmp_$taskId.mp4');
      if (await videoFile.exists()) {
        try {
          await videoFile.delete();
          AppLogger.info('NexusService: Cleaned up temporary video file.');
        } catch (e) {
          AppLogger.warn('NexusService: Failed to delete temp video: $e');
        }
      }
      TransferState.activeTaskCount--;
    }
  }
}

Map<String, dynamic> _processEncodeChunkIsolate(Map<String, dynamic> args) {
  final int cryptoCtxAddr = args['cryptoCtx'];
  final int encoderCtxAddr = args['encoderCtx'];
  final Uint8List chunk = args['chunk'];

  final cryptoCtx = Pointer<StreamingContext>.fromAddress(cryptoCtxAddr);
  final encoderCtx = Pointer<StreamingEncoder>.fromAddress(encoderCtxAddr);
  final native = NexusLoader.bindings;

  final outPtrPtr = malloc<Pointer<Uint8>>();
  final outLenPtr = malloc<Size>();
  final reusableChunkPtr = malloc<Uint8>(chunk.length);
  reusableChunkPtr.asTypedList(chunk.length).setAll(0, chunk);

  int res = native.nexus_encrypt_stream_update(
    cryptoCtx,
    reusableChunkPtr,
    chunk.length,
    outPtrPtr,
    outLenPtr,
  );
  if (res != 0) throw Exception('Streaming encryption failed: $res');

  if (outLenPtr.value > 0) {
    res = native.nexus_encode_stream_push_fec(
      encoderCtx,
      outPtrPtr.value,
      outLenPtr.value,
    );
    native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
    if (res < 0) throw Exception('Streaming encoding push failed: $res');
  }

  List<Uint8List> frames = [];
  while (true) {
    final popRes = native.nexus_encode_stream_pop_frame(
      encoderCtx,
      outPtrPtr,
      outLenPtr,
    );
    if (popRes == 1) break;
    if (popRes != 0) throw Exception('Frame generation failed: $popRes');

    final frameData = outPtrPtr.value.asTypedList(outLenPtr.value);
    frames.add(Uint8List.fromList(frameData)); // copy
    native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
  }

  malloc.free(outPtrPtr);
  malloc.free(outLenPtr);
  malloc.free(reusableChunkPtr);

  return {'frames': frames};
}

Map<String, dynamic> _processEncodeFinalizeIsolate(Map<String, dynamic> args) {
  final int cryptoCtxAddr = args['cryptoCtx'];
  final int encoderCtxAddr = args['encoderCtx'];

  final cryptoCtx = Pointer<StreamingContext>.fromAddress(cryptoCtxAddr);
  final encoderCtx = Pointer<StreamingEncoder>.fromAddress(encoderCtxAddr);
  final native = NexusLoader.bindings;

  final outPtrPtr = malloc<Pointer<Uint8>>();
  final outLenPtr = malloc<Size>();

  int res = native.nexus_encrypt_stream_finalize(
    cryptoCtx,
    nullptr,
    0,
    outPtrPtr,
    outLenPtr,
  );
  if (res != 0) throw Exception('Streaming encryption finalize failed: $res');

  if (outLenPtr.value > 0) {
    res = native.nexus_encode_stream_push_fec(
      encoderCtx,
      outPtrPtr.value,
      outLenPtr.value,
    );
    native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
    if (res < 0) throw Exception('Final encoding push failed: $res');
  }

  res = native.nexus_encode_stream_finalize(encoderCtx);
  if (res != 0) throw Exception('Streaming encoding finalize failed: $res');

  List<Uint8List> frames = [];
  while (true) {
    final popRes = native.nexus_encode_stream_pop_frame(
      encoderCtx,
      outPtrPtr,
      outLenPtr,
    );
    if (popRes == 1) break;
    if (popRes != 0) throw Exception('Final frame generation failed: $popRes');

    final frameData = outPtrPtr.value.asTypedList(outLenPtr.value);
    frames.add(Uint8List.fromList(frameData));
    native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
  }

  malloc.free(outPtrPtr);
  malloc.free(outLenPtr);

  return {'frames': frames};
}

Map<String, dynamic> _processDecodeFrameIsolate(Map<String, dynamic> args) {
  final int decoderCtxAddr = args['decoderCtx'];
  final int? cryptoCtxAddr = args['cryptoCtx'];
  final int? keyPtrAddr = args['keyPtr'];
  final Uint8List frame = args['frame'];
  final bool initializedCrypto = args['initializedCrypto'];
  final Uint8List prevNonceBuffer = args['nonceBuffer'];

  final decoderCtx = Pointer<StreamingDecoder>.fromAddress(decoderCtxAddr);
  Pointer<StreamingContext>? cryptoCtx;
  if (cryptoCtxAddr != null && cryptoCtxAddr != 0) {
    cryptoCtx = Pointer<StreamingContext>.fromAddress(cryptoCtxAddr);
  }
  Pointer<Uint8>? keyPtr;
  if (keyPtrAddr != null && keyPtrAddr != 0) {
    keyPtr = Pointer<Uint8>.fromAddress(keyPtrAddr);
  }

  final native = NexusLoader.bindings;

  final outPtrPtr = malloc<Pointer<Uint8>>();
  final outLenPtr = malloc<Size>();
  final reusableFramePtr = malloc<Uint8>(frame.length);
  reusableFramePtr.asTypedList(frame.length).setAll(0, frame);

  int res = native.nexus_decode_stream_push_fec(
    decoderCtx,
    reusableFramePtr,
    frame.length,
  );
  if (res != 0) throw Exception('Frame push error: $res');

  List<Uint8List> decryptedData = [];
  bool newInitCrypto = initializedCrypto;
  Uint8List newNonceBuffer = prevNonceBuffer;
  int newCryptoCtxAddr = cryptoCtxAddr ?? 0;

  while (true) {
    final popRes = native.nexus_decode_stream_pop(
      decoderCtx,
      outPtrPtr,
      outLenPtr,
    );
    if (popRes == 1) break;
    if (popRes != 0) throw Exception('Frame pop error: $popRes');

    final decodedBytes = outPtrPtr.value.asTypedList(outLenPtr.value);

    if (!newInitCrypto) {
      final combined = BytesBuilder();
      combined.add(newNonceBuffer);
      combined.add(decodedBytes);
      newNonceBuffer = combined.toBytes();

      if (newNonceBuffer.length >= 16) {
        final noncePrefix = newNonceBuffer.sublist(0, 16);
        final noncePtr = malloc<Uint8>(16);
        noncePtr.asTypedList(16).setAll(0, noncePrefix);

        cryptoCtx = native.nexus_decrypt_stream_init(keyPtr!, noncePtr);
        malloc.free(noncePtr);
        if (cryptoCtx == nullptr) throw Exception('Crypto stream init failed');

        newInitCrypto = true;
        newCryptoCtxAddr = cryptoCtx.address;

        if (newNonceBuffer.length > 16) {
          final remainingData = newNonceBuffer.sublist(16);
          final remaining = malloc<Uint8>(remainingData.length);
          remaining.asTypedList(remainingData.length).setAll(0, remainingData);

          final decPtrPtr = malloc<Pointer<Uint8>>();
          final decLenPtr = malloc<Size>();
          final decRes = native.nexus_decrypt_stream_update(
            cryptoCtx,
            remaining,
            remainingData.length,
            decPtrPtr,
            decLenPtr,
          );
          malloc.free(remaining);
          if (decRes == 0 && decLenPtr.value > 0) {
            decryptedData.add(
              Uint8List.fromList(decPtrPtr.value.asTypedList(decLenPtr.value)),
            );
            native.nexus_free_bytes(decPtrPtr.value, decLenPtr.value);
          }
          malloc.free(decPtrPtr);
          malloc.free(decLenPtr);
          if (decRes != 0) throw Exception('Initial decryption error: $decRes');
        }
      }
      native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
    } else {
      final decPtrPtr = malloc<Pointer<Uint8>>();
      final decLenPtr = malloc<Size>();
      final decRes = native.nexus_decrypt_stream_update(
        cryptoCtx!,
        outPtrPtr.value,
        outLenPtr.value,
        decPtrPtr,
        decLenPtr,
      );
      if (decRes == 0 && decLenPtr.value > 0) {
        decryptedData.add(
          Uint8List.fromList(decPtrPtr.value.asTypedList(decLenPtr.value)),
        );
        native.nexus_free_bytes(decPtrPtr.value, decLenPtr.value);
      }
      malloc.free(decPtrPtr);
      malloc.free(decLenPtr);
      native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
      if (decRes != 0) throw Exception('Decryption update error: $decRes');
    }
  }

  malloc.free(outPtrPtr);
  malloc.free(outLenPtr);
  malloc.free(reusableFramePtr);

  return {
    'decryptedData': decryptedData,
    'initializedCrypto': newInitCrypto,
    'nonceBuffer': newNonceBuffer,
    'cryptoCtxAddr': newCryptoCtxAddr,
  };
}

Map<String, dynamic> _processDecodeFinalizeIsolate(Map<String, dynamic> args) {
  final int cryptoCtxAddr = args['cryptoCtx'];
  final cryptoCtx = Pointer<StreamingContext>.fromAddress(cryptoCtxAddr);
  final native = NexusLoader.bindings;

  final decPtrPtr = malloc<Pointer<Uint8>>();
  final decLenPtr = malloc<Size>();
  final finRes = native.nexus_decrypt_stream_finalize(
    cryptoCtx,
    nullptr,
    0,
    decPtrPtr,
    decLenPtr,
  );

  Uint8List? finalData;
  if (finRes == 0) {
    if (decLenPtr.value > 0) {
      finalData = Uint8List.fromList(
        decPtrPtr.value.asTypedList(decLenPtr.value),
      );
    }
    if (decLenPtr.value > 0 || finRes == 0) {
      if (decLenPtr.value > 0) {
        native.nexus_free_bytes(decPtrPtr.value, decLenPtr.value);
      }
    }
  }

  malloc.free(decPtrPtr);
  malloc.free(decLenPtr);

  if (finRes != 0) throw Exception('Decryption finalize failed: $finRes');

  return {'finalData': finalData};
}
