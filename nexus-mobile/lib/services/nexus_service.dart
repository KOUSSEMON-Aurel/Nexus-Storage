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
import 'package:ffmpeg_kit_flutter_new/ffprobe_kit.dart';
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

class NexusService {
  static const MethodChannel _mediaChannel = MethodChannel('nexus/media');

  final NexusCoreBindings _native = NexusLoader.bindings;
  final DatabaseService _db = DatabaseService();
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
    if (now.difference(_lastProgressUpdate).inMilliseconds < 250 &&
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
        await _db.checkpointWAL();
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

      final pipePath = await FFmpegKitConfig.registerNewFFmpegPipe();
      if (pipePath == null) {
        throw NexusException('Failed to create FFmpeg pipe');
      }
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
        assert(chunk.length <= maxChunkSize, 'Chunk too large for FFI buffer');
        reusableChunkPtr.asTypedList(chunk.length).setAll(0, chunk);

        int res = _native.nexus_encrypt_stream_update(
          cryptoCtx,
          reusableChunkPtr,
          chunk.length,
          outPtrPtr,
          outLenPtr,
        );
        // malloc.free is not needed since reusableChunkPtr will be freed at end
        if (res != 0) throw NexusException('Streaming encryption failed: $res');

        if (outLenPtr.value > 0) {
          res = _native.nexus_encode_stream_push_fec(
            encoderCtx,
            outPtrPtr.value,
            outLenPtr.value,
          );
          _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
          if (res < 0) {
            throw NexusException('Streaming encoding push failed: $res');
          }
        }

        while (true) {
          final popRes = _native.nexus_encode_stream_pop_frame(
            encoderCtx,
            outPtrPtr,
            outLenPtr,
          );
          if (popRes == 1) break;
          if (popRes != 0) {
            throw NexusException('Frame generation failed: $popRes');
          }

          frameCount++;
          final frameData = outPtrPtr.value.asTypedList(outLenPtr.value);
          // Only copy if we must free the C memory immediately
          final copy = Uint8List.fromList(frameData);
          _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);

          pipeSink.add(copy);

          // Force backpressure: flush after every frame
          // This blocks the Dart loop if the OS FIFO is full
          await pipeSink.flush();

          // Rule 3: Maintain UI responsiveness during heavy processing
          if (frameCount % 5 == 0) {
            await Future.delayed(Duration.zero);
          }
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

      int res = _native.nexus_encrypt_stream_finalize(
        cryptoCtx,
        nullptr,
        0,
        outPtrPtr,
        outLenPtr,
      );
      if (res != 0) {
        throw NexusException('Streaming encryption finalize failed: $res');
      }

      if (outLenPtr.value > 0) {
        res = _native.nexus_encode_stream_push_fec(
          encoderCtx,
          outPtrPtr.value,
          outLenPtr.value,
        );
        _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
        if (res < 0) throw NexusException('Final encoding push failed: $res');
      }

      res = _native.nexus_encode_stream_finalize(encoderCtx);
      if (res != 0) {
        throw NexusException('Streaming encoding finalize failed: $res');
      }

      while (true) {
        final popRes = _native.nexus_encode_stream_pop_frame(
          encoderCtx,
          outPtrPtr,
          outLenPtr,
        );
        if (popRes == 1) break;
        if (popRes != 0) {
          throw NexusException('Final frame generation failed: $popRes');
        }

        frameCount++;
        final frameData = outPtrPtr.value.asTypedList(outLenPtr.value);
        lastFrameData = Uint8List.fromList(frameData);
        pipeSink.add(lastFrameData);
        _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
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
    final taskId =
        explicitTaskId ?? DateTime.now().millisecondsSinceEpoch.toString();
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

      await _updateStatus(
        taskId,
        0.05,
        'Starting download...',
        fileName: fileName,
      );

      AppLogger.info(
        'Starting streaming download for $fileName (ID: ${record.videoId}) Mode: ${record.mode}',
      );

      // Fix 4 & 1: Detect mode and prefer WebM for High mode
      final isHighMode = record.mode == 'high';

      videoFile = await _youtube.downloadVideo(
        record.videoId,
        isHighMode: isHighMode,
        onProgress: (p) => _updateStatus(
          taskId,
          0.1 + (p * 0.3),
          'Downloading...',
          fileName: fileName,
        ),
      );
      if (videoFile == null) {
        AppLogger.error(
          'NexusService: YouTube download returned null. Aborting.',
        );
        throw NexusException('YouTube download failed');
      }

      final videoSize = await videoFile.length();
      AppLogger.info('NexusService: Downloaded video size: $videoSize bytes');
      if (videoSize == 0) {
        throw NexusException('Downloaded video is empty (0 bytes)');
      }
      if (!await videoFile.exists()) {
        throw NexusException('Video file missing after download');
      }

      final tmpDir = await getTemporaryDirectory();
      framesDir = Directory('${tmpDir.path}/nexus-dl-$taskId');
      if (await framesDir.exists()) await framesDir.delete(recursive: true);
      await framesDir.create();

      await _updateStatus(
        taskId,
        0.4,
        'Analyzing video...',
        fileName: fileName,
      );

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

        AppLogger.info(
          'NexusService: Video resolution detected: $width x $height',
        );

        if (width != null && height != null) {
          // Validation de sécurité pour éviter le décodage de bruit (ex: 144p)
          if (!isHighMode && (width < 640 || height < 360)) {
            // Seuil minimal absolu pour Base (idéal 720p)
            throw NexusException(
              'Qualité insuffisante : ${width}x$height. Le mode Base requiert au moins du 360p (idéal 720p).',
            );
          }
          // (debug) frames retention handled later per-frame during decoding loop
          if (isHighMode && (width < 1280 || height < 720)) {
            throw NexusException(
              'Qualité insuffisante pour le mode High : ${width}x$height. 720p minimum requis.',
            );
          }

          if (width != 1280 || height != 720) {
            if (!isHighMode) {
              AppLogger.warn(
                'NexusService: Resolution mismatch (Base). Expected 1280x720, got ${width}x$height. FFmpeg will rescale.',
              );
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
      final pipePath = await FFmpegKitConfig.registerNewFFmpegPipe();
      if (pipePath == null) {
        throw NexusException('Failed to create FFmpeg pipe for download');
      }

      AppLogger.info(
        'NexusService: Download mode: ${isHighMode ? "High" : "Base"}',
      );
      AppLogger.info(
        'NexusService: Target resolution: $targetWidth x $targetHeight',
      );

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
        pipePath,
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
      final pipeStream = File(pipePath).openRead();

      bool initializedCrypto = false;
      Uint8List nonceBuffer = Uint8List(0);

      int totalDecryptedBytes = 0;
      bool headerInspected = false;

      final frameSize = targetWidth * targetHeight;
      final reusableFramePtr = malloc<Uint8>(frameSize);

      final byteBuffer = BytesBuilder(copy: false);

      await for (final chunk in pipeStream) {
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

          reusableFramePtr.asTypedList(frameSize).setAll(0, frameData);

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

          int res = _native.nexus_decode_stream_push_fec(
            decoderCtx,
            reusableFramePtr,
            frameSize,
          );

          if (res != 0) {
            throw NexusException('Frame push error at frame $i: $res');
          }

          while (true) {
            final popRes = _native.nexus_decode_stream_pop(
              decoderCtx,
              outPtrPtr,
              outLenPtr,
            );
            if (popRes == 1) break; // Need more data
            if (popRes != 0) {
              throw NexusException('Frame pop error at frame $i: $popRes');
            }

            final decodedBytes = outPtrPtr.value.asTypedList(outLenPtr.value);
            final hex = decodedBytes
                .take(16)
                .map((b) => b.toRadixString(16).padLeft(2, '0'))
                .join(' ');

            AppLogger.info(
              'NexusService: Decoder produced ${outLenPtr.value} bytes. First 16: $hex',
            );

            if (!initializedCrypto) {
              nonceBuffer = Uint8List.fromList([
                ...nonceBuffer,
                ...decodedBytes,
              ]);
              if (nonceBuffer.length < 16) {
                _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
                continue;
              }

              final noncePrefix = nonceBuffer.sublist(0, 16);
              final nonceHex = noncePrefix
                  .map((b) => b.toRadixString(16).padLeft(2, '0'))
                  .join(' ');
              AppLogger.info(
                'NexusService: Nonce extracted ($nonceHex), initializing crypto stream',
              );

              final noncePtr = malloc<Uint8>(16);
              noncePtr.asTypedList(16).setAll(0, noncePrefix);

              cryptoCtx = _native.nexus_decrypt_stream_init(keyPtr, noncePtr);
              malloc.free(noncePtr);
              if (cryptoCtx == nullptr) {
                throw NexusException('Crypto stream init failed (NULL)');
              }

              initializedCrypto = true;

              if (nonceBuffer.length > 16) {
                final remainingData = nonceBuffer.sublist(16);
                final remaining = malloc<Uint8>(remainingData.length);
                remaining
                    .asTypedList(remainingData.length)
                    .setAll(0, remainingData);

                final decPtrPtr = malloc<Pointer<Uint8>>();
                final decLenPtr = malloc<Size>();
                final hex1 = remainingData
                    .take(16)
                    .map((b) => b.toRadixString(16).padLeft(2, '0'))
                    .join(' ');
                AppLogger.info(
                  'NexusService: About to decrypt ${remainingData.length} bytes (post-nonce). First 16: $hex1',
                );
                final decRes = _native.nexus_decrypt_stream_update(
                  cryptoCtx,
                  remaining,
                  remainingData.length,
                  decPtrPtr,
                  decLenPtr,
                );
                malloc.free(remaining);
                AppLogger.info(
                  'NexusService: Decrypt returned: res=$decRes, outLen=${decLenPtr.value}',
                );

                if (decRes == 0 && decLenPtr.value > 0) {
                  final decData = decPtrPtr.value.asTypedList(decLenPtr.value);
                  if (!headerInspected && decData.isNotEmpty) {
                    final hex = decData
                        .take(16)
                        .map((e) => e.toRadixString(16).padLeft(2, '0'))
                        .join(' ');
                    AppLogger.info('NexusService: First decrypted bytes: $hex');
                    headerInspected = true;
                  }
                  totalDecryptedBytes += decLenPtr.value;
                  await ios.writeFrom(decData);
                }
                if (decRes == 0) {
                  if (decLenPtr.value == 0) {
                    AppLogger.info(
                      'NexusService: Decryption update returned 0 bytes',
                    );
                  }
                  _native.nexus_free_bytes(decPtrPtr.value, decLenPtr.value);
                }
                malloc.free(decPtrPtr);
                malloc.free(decLenPtr);
                if (decRes != 0) {
                  throw NexusException(
                    'Initial decryption update error: $decRes',
                  );
                }
              }
            } else {
              final decPtrPtr = malloc<Pointer<Uint8>>();
              final decLenPtr = malloc<Size>();
              final decRes = _native.nexus_decrypt_stream_update(
                cryptoCtx!,
                outPtrPtr.value,
                outLenPtr.value,
                decPtrPtr,
                decLenPtr,
              );

              if (decRes == 0 && decLenPtr.value > 0) {
                final decData = decPtrPtr.value.asTypedList(decLenPtr.value);
                if (!headerInspected && decData.isNotEmpty) {
                  final hex = decData
                      .take(16)
                      .map((e) => e.toRadixString(16).padLeft(2, '0'))
                      .join(' ');
                  AppLogger.info('NexusService: First decrypted bytes: $hex');
                  headerInspected = true;
                }
                totalDecryptedBytes += decLenPtr.value;
                await ios.writeFrom(decData);
              }
              if (decRes == 0) {
                _native.nexus_free_bytes(decPtrPtr.value, decLenPtr.value);
              }
              malloc.free(decPtrPtr);
              malloc.free(decLenPtr);
              if (decRes != 0) {
                throw NexusException(
                  'Decryption update error: $decRes at frame $i',
                );
              }
            }

            _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
          }
          i++;
        }
      }
      malloc.free(reusableFramePtr);
      await FFmpegKitConfig.closeFFmpegPipe(pipePath);

      final session = await sessionPromise;
      final returnCode = await session.getReturnCode();
      final duration = DateTime.now().difference(startTime);

      AppLogger.info(
        'NexusService: FFmpeg extraction finished in ${duration.inSeconds}s with return code: $returnCode',
      );

      if (!ReturnCode.isSuccess(returnCode)) {
        final logs = await session.getLogs();
        var logMsg = logs.map((l) => l.getMessage()).join('\n');
        AppLogger.error(
          'NexusService: FFmpeg failed. Logs: ${logMsg.length > 1000 ? logMsg.substring(0, 1000) : logMsg}',
        );
        if (logMsg.length > 500) logMsg = logMsg.substring(0, 500);
        throw NexusException('FFmpeg Extraction Failed: $logMsg');
      }

      if (i == 0) {
        throw NexusException('No frames extracted from video');
      }

      AppLogger.info(
        'NexusService: Decryption loop finished. Total bytes decrypted: $totalDecryptedBytes. Initializing finalization.',
      );

      if (cryptoCtx != null) {
        final decPtrPtr = malloc<Pointer<Uint8>>();
        final decLenPtr = malloc<Size>();
        AppLogger.info('NexusService: Finalizing crypto stream...');
        final finRes = _native.nexus_decrypt_stream_finalize(
          cryptoCtx,
          nullptr,
          0,
          decPtrPtr,
          decLenPtr,
        );

        if (finRes == 0) {
          if (decLenPtr.value > 0) {
            await ios.writeFrom(decPtrPtr.value.asTypedList(decLenPtr.value));
            totalDecryptedBytes += decLenPtr.value;
          }
          _native.nexus_free_bytes(decPtrPtr.value, decLenPtr.value);
          AppLogger.info(
            'NexusService: Finalization successful. Total final bytes: $totalDecryptedBytes',
          );
        } else {
          AppLogger.error(
            'NexusService: Finalization failed with error code: $finRes',
          );
          malloc.free(decPtrPtr);
          malloc.free(decLenPtr);
          throw NexusException(
            'Decryption finalization failed (Code: $finRes). Data might be corrupted or password incorrect.',
          );
        }
        malloc.free(decPtrPtr);
        malloc.free(decLenPtr);
      }

      try {
        await ios.close();
        // Trigger media scan to make file visible in Downloads
        await _scanMediaFile(finalFile.path);
      } catch (e) {
        AppLogger.warn(
          'NexusService: Failed to close output file (ignored): $e',
        );
      }
      final finalSize = await finalFile.length();
      AppLogger.info(
        'NexusService: Decryption complete. Final file size: $finalSize bytes',
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
        await ios!.close();
      } catch (e) {
        AppLogger.warn(
          'NexusService: Ignored error closing output file in finally: $e',
        );
      }
      if (decoderCtx != null) _native.nexus_decoder_stream_drop(decoderCtx);
      if (cryptoCtx != null) _native.nexus_crypto_stream_drop(cryptoCtx);
      if (keyPtr != null) malloc.free(keyPtr);
      if (outPtrPtr != null) malloc.free(outPtrPtr);
      if (outLenPtr != null) malloc.free(outLenPtr);
      if (framesDir != null && await framesDir.exists()) {
        await framesDir.delete(recursive: true);
      }
      if (videoFile != null && await videoFile.exists()) {
        await videoFile.delete();
      }
    }
  }

  Future<void> _scanMediaFile(String path) async {
    if (!Platform.isAndroid) return;
    try {
      await _mediaChannel.invokeMethod('scanFile', {'path': path});
      AppLogger.info('NexusService: Media scan triggered for $path');
    } catch (e) {
      AppLogger.error('NexusService: Failed to trigger media scan: $e');
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
