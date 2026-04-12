import 'dart:ffi';
import 'dart:io';
import 'dart:typed_data';
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
import 'sync_service.dart';
import '../models/file_record.dart';
import 'logger_service.dart';
import '../utils/exceptions.dart';

class NexusService {
  final NexusCoreBindings _native = NexusLoader.bindings;
  final DatabaseService _db = DatabaseService();
  final YouTubeService _youtube = YouTubeService();

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
        FlutterForegroundTask.updateService(
          notificationTitle: 'Nexus: Processing $fileName',
          notificationText: 'Status: Initializing...',
        );
      }

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
      
      int frameCount = 0;
      int processedBytes = 0;

      await for (final chunk in inputFile.openRead()) {
        final chunkPtr = malloc<Uint8>(chunk.length);
        chunkPtr.asTypedList(chunk.length).setAll(0, chunk);

        int res = _native.nexus_encrypt_stream_update(cryptoCtx, chunkPtr, chunk.length, outPtrPtr, outLenPtr);
        malloc.free(chunkPtr);
        if (res != 0) throw NexusException('Streaming encryption failed: $res');

        if (outLenPtr.value > 0) {
          res = _native.nexus_encode_stream_push(encoderCtx, outPtrPtr.value, outLenPtr.value);
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
        await _db.updateTaskProgress(taskId, progress, 'Processing data... ${(progress * 100).toInt()}%');
      }

      int res = _native.nexus_encrypt_stream_finalize(cryptoCtx, nullptr, 0, outPtrPtr, outLenPtr);
      if (res != 0) throw NexusException('Streaming encryption finalize failed: $res');

      if (outLenPtr.value > 0) {
        res = _native.nexus_encode_stream_push(encoderCtx, outPtrPtr.value, outLenPtr.value);
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
        }
      }

      await _db.updateTaskProgress(taskId, 0.6, 'Assembling video...');
      
      final videoFile = File('${framesDir.path}/out.mp4');
      final ffmpegCommand = '-framerate 30 -i ${framesDir.path}/frame_%06d.png -c:v libx264 -pix_fmt yuv420p -y ${videoFile.path}';
      final session = await FFmpegKit.execute(ffmpegCommand);
      if (!ReturnCode.isSuccess(await session.getReturnCode())) throw NexusException('FFmpeg failed');

      final videoId = await _youtube.uploadVideo(
        videoFile: videoFile,
        title: 'Nexus Data ($fileName)',
        description: 'Secure Nexus Storage Object',
        onProgress: (p) => _db.updateTaskProgress(taskId, 0.7 + (p * 0.3), 'Uploading... ${(p * 100).toInt()}%'),
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
      try {
        await SyncService().pushDatabase();
      } catch (e) {
        AppLogger.warn('Auto-sync failed: $e');
      }
      await _db.updateTaskProgress(taskId, 1.0, 'completed');
      AppLogger.info('Upload complete for $fileName');
    } catch (e, s) {
      AppLogger.error('Upload Error: $e', e, s);
      await _db.updateTaskProgress(taskId, 0.0, 'Failed');
      rethrow;
    } finally {
      if (cryptoCtx != null) _native.nexus_crypto_stream_drop(cryptoCtx);
      if (encoderCtx != null) _native.nexus_encoder_stream_drop(encoderCtx);
      if (keyPtr != null) malloc.free(keyPtr);
      if (noncePrefixPtr != null) malloc.free(noncePrefixPtr);
      if (outPtrPtr != null) malloc.free(outPtrPtr);
      if (outLenPtr != null) malloc.free(outLenPtr);
      if (framesDir != null && await framesDir.exists()) await framesDir.delete(recursive: true);
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
        'progress': 0.1,
        'created_at': DateTime.now().toIso8601String(),
      });

      if (await FlutterForegroundTask.isRunningService) {
        FlutterForegroundTask.updateService(
          notificationTitle: 'Nexus: Downloading $fileName',
          notificationText: 'Status: Downloading...',
        );
      }

      AppLogger.info('Starting streaming download for $fileName (ID: ${record.videoId})');

      videoFile = await _youtube.downloadVideo(
        record.videoId,
        onProgress: (p) => _db.updateTaskProgress(taskId, 0.1 + (p * 0.3), 'Downloading...'),
      );
      if (videoFile == null) throw NexusException('YouTube download failed');

      final tmpDir = await getTemporaryDirectory();
      framesDir = Directory('${tmpDir.path}/nexus-dl-$taskId');
      if (await framesDir.exists()) await framesDir.delete(recursive: true);
      await framesDir.create();

      final ffmpegCommand = '-i ${videoFile.path} -vsync 0 ${framesDir.path}/frame_%06d.png';
      final session = await FFmpegKit.execute(ffmpegCommand);
      if (!ReturnCode.isSuccess(await session.getReturnCode())) throw NexusException('FFmpeg failed');

      final frames = framesDir.listSync()
          .where((e) => e.path.endsWith('.png'))
          .map((e) => e.path)
          .toList()..sort();
      
      if (frames.isEmpty) throw NexusException('No frames extracted');

      final keyBytes = await _deriveKeyFromPassword(password);
      keyPtr = malloc<Uint8>(32);
      keyPtr.asTypedList(32).setAll(0, keyBytes);

      decoderCtx = _native.nexus_decode_stream_init(0);
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

      bool initializedCrypto = false;

      for (int i = 0; i < frames.length; i++) {
        final frameData = await File(frames[i]).readAsBytes();
        final framePtr = malloc<Uint8>(frameData.length);
        framePtr.asTypedList(frameData.length).setAll(0, frameData);

        int res = _native.nexus_decode_stream_push(decoderCtx, framePtr, frameData.length);
        malloc.free(framePtr);
        if (res != 0) throw NexusException('Frame push error: $res');

        while (true) {
          final popRes = _native.nexus_decode_stream_pop(decoderCtx, outPtrPtr, outLenPtr);
          if (popRes == 1) break;
          if (popRes != 0) throw NexusException('Frame pop error: $popRes');

          final decodedBytes = outPtrPtr.value.asTypedList(outLenPtr.value);

          if (!initializedCrypto) {
            if (decodedBytes.length < 16) {
                _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
                continue; 
            }
            final noncePrefix = decodedBytes.sublist(0, 16);
            final noncePtr = malloc<Uint8>(16);
            noncePtr.asTypedList(16).setAll(0, noncePrefix);
            
            cryptoCtx = _native.nexus_decrypt_stream_init(keyPtr, noncePtr);
            malloc.free(noncePtr);
            if (cryptoCtx == nullptr) throw NexusException('Crypto init failed');
            
            initializedCrypto = true;
            
            if (decodedBytes.length > 16) {
                final remaining = malloc<Uint8>(decodedBytes.length - 16);
                remaining.asTypedList(decodedBytes.length - 16).setAll(0, decodedBytes.sublist(16));
                
                final decPtrPtr = malloc<Pointer<Uint8>>();
                final decLenPtr = malloc<Size>();
                final decRes = _native.nexus_decrypt_stream_update(cryptoCtx, remaining, decodedBytes.length - 16, decPtrPtr, decLenPtr);
                malloc.free(remaining);
                
                if (decRes == 0 && decLenPtr.value > 0) {
                    await ios.writeFrom(decPtrPtr.value.asTypedList(decLenPtr.value));
                }
                if (decRes == 0) _native.nexus_free_bytes(decPtrPtr.value, decLenPtr.value);
                malloc.free(decPtrPtr);
                malloc.free(decLenPtr);
                if (decRes != 0) throw NexusException('Decryption update error');
            }
          } else {
            final decPtrPtr = malloc<Pointer<Uint8>>();
            final decLenPtr = malloc<Size>();
            final decRes = _native.nexus_decrypt_stream_update(cryptoCtx!, outPtrPtr.value, outLenPtr.value, decPtrPtr, decLenPtr);
            
            if (decRes == 0 && decLenPtr.value > 0) {
                await ios.writeFrom(decPtrPtr.value.asTypedList(decLenPtr.value));
            }
            if (decRes == 0) _native.nexus_free_bytes(decPtrPtr.value, decLenPtr.value);
            malloc.free(decPtrPtr);
            malloc.free(decLenPtr);
            if (decRes != 0) throw NexusException('Decryption update error');
          }

          _native.nexus_free_bytes(outPtrPtr.value, outLenPtr.value);
        }
        
        if (i % 10 == 0) {
            await _db.updateTaskProgress(taskId, 0.4 + (i / frames.length * 0.5), 'Decoding...');
        }
      }

      if (cryptoCtx != null) {
          final decPtrPtr = malloc<Pointer<Uint8>>();
          final decLenPtr = malloc<Size>();
          final finRes = _native.nexus_decrypt_stream_finalize(cryptoCtx, nullptr, 0, decPtrPtr, decLenPtr);
          if (finRes == 0 && decLenPtr.value > 0) {
              await ios.writeFrom(decPtrPtr.value.asTypedList(decLenPtr.value));
          }
          if (finRes == 0) _native.nexus_free_bytes(decPtrPtr.value, decLenPtr.value);
          malloc.free(decPtrPtr);
          malloc.free(decLenPtr);
          if (finRes != 0) throw NexusException('Decryption finalization failed');
      }

      await ios.close();
      await _db.updateTaskProgress(taskId, 1.0, 'completed');
    } catch (e, s) {
      AppLogger.error('STREAM DOWNLOAD ERROR: $e', e, s);
      await _db.updateTaskProgress(taskId, 0.0, 'Failed');
      rethrow;
    } finally {
      if (ios != null) await ios.close();
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
