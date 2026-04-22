import 'dart:convert';
import 'dart:io';
import 'package:http/http.dart' as http;
import 'package:youtube_explode_dart/youtube_explode_dart.dart' as yt_explode;
import 'package:path_provider/path_provider.dart';
import 'auth_service.dart';
import 'logger_service.dart';

class YouTubeService {
  static final YouTubeService _instance = YouTubeService._internal();
  factory YouTubeService() => _instance;
  YouTubeService._internal();

  final AuthService _auth = AuthService();
  final yt_explode.YoutubeExplode _yt = yt_explode.YoutubeExplode();

  Future<String?> uploadVideo({
    required File videoFile,
    required String title,
    required String description,
    Function(double)? onProgress,
  }) async {
    final token = await _auth.getAccessToken();
    if (token == null) return null;

    final totalBytes = await videoFile.length();

    // 1. Initialize resumable upload session
    final metadata = {
      'snippet': {
        'title': title,
        'description': description,
        'categoryId': '22',
      },
      'status': {'privacyStatus': 'unlisted', 'selfDeclaredMadeForKids': false},
    };

    final initResponse = await http.post(
      Uri.parse(
        'https://www.googleapis.com/upload/youtube/v3/videos?uploadType=resumable&part=snippet,status',
      ),
      headers: {
        'Authorization': 'Bearer $token',
        'Content-Type': 'application/json; charset=UTF-8',
        'X-Upload-Content-Length': totalBytes.toString(),
        'X-Upload-Content-Type': 'video/mp4',
      },
      body: jsonEncode(metadata),
    );

    if (initResponse.statusCode != 200) {
      AppLogger.error('Upload initialization failed: ${initResponse.body}');
      return null;
    }

    final uploadUrl = initResponse.headers['location'];
    if (uploadUrl == null) {
      AppLogger.error('Upload URL missing from response headers');
      return null;
    }

    AppLogger.info(
      'YouTube resumable upload started. Total: ${(totalBytes / 1024 / 1024).toStringAsFixed(1)} MB',
    );

    // 2. Upload by chunks
    int currentOffset = 0;
    const int uploadChunkSize = 10 * 1024 * 1024; // 10MB chunks
    final raf = await videoFile.open(mode: FileMode.read);

    try {
      while (currentOffset < totalBytes) {
        final end = (currentOffset + uploadChunkSize < totalBytes)
            ? currentOffset + uploadChunkSize
            : totalBytes;
        final currentChunkLength = end - currentOffset;

        await raf.setPosition(currentOffset);
        final chunkData = await raf.read(currentChunkLength);

        AppLogger.info(
          'YouTube: Uploading chunk $currentOffset-${end - 1}/$totalBytes (${(currentChunkLength / 1024 / 1024).toStringAsFixed(1)} MB)',
        );

        bool chunkSuccess = false;
        int retries = 0;

        while (!chunkSuccess && retries < 3) {
          try {
            final response = await http.put(
              Uri.parse(uploadUrl),
              headers: {
                'Content-Length': currentChunkLength.toString(),
                'Content-Range': 'bytes $currentOffset-${end - 1}/$totalBytes',
              },
              body: chunkData,
            );

            if (response.statusCode == 308) {
              // Chunk accepted, session still open
              chunkSuccess = true;
              currentOffset = end;
              onProgress?.call(currentOffset / totalBytes);
            } else if (response.statusCode == 200 ||
                response.statusCode == 201) {
              // Final chunk accepted, upload complete
              final json = jsonDecode(response.body);
              AppLogger.info(
                'YouTube: Upload complete! Video ID: ${json['id']}',
              );
              return json['id'] as String?;
            } else if (response.statusCode >= 500 ||
                response.statusCode == 408) {
              // Server error or timeout, retry
              retries++;
              AppLogger.warn(
                'YouTube: Chunk upload failed (${response.statusCode}), retrying ($retries/3)...',
              );
              await Future.delayed(Duration(seconds: 2 * retries));
            } else {
              // Fatal error (4xx)
              AppLogger.error(
                'YouTube: Fatal upload error (${response.statusCode}): ${response.body}',
              );
              return null;
            }
          } catch (e) {
            retries++;
            AppLogger.warn(
              'YouTube: Chunk upload exception: $e, retrying ($retries/3)...',
            );
            await Future.delayed(Duration(seconds: 2 * retries));
          }
        }

        if (!chunkSuccess) {
          AppLogger.error(
            'YouTube: Failed to upload chunk after retries. Aborting.',
          );
          return null;
        }
      }
    } finally {
      await raf.close();
    }

    return null;
  }

  Future<bool> deleteVideo(String videoId) async {
    final token = await _auth.getAccessToken();
    if (token == null) return false;

    final response = await http.delete(
      Uri.parse('https://www.googleapis.com/youtube/v3/videos?id=$videoId'),
      headers: {'Authorization': 'Bearer $token'},
    );

    return response.statusCode == 204;
  }

  Future<File?> downloadVideo(
    String videoId, {
    bool isHighMode = false,
    Function(double)? onProgress,
  }) async {
    try {
      AppLogger.info('YT API: Getting manifest for $videoId');
      final manifest = await _yt.videos.streamsClient.getManifest(videoId);
      AppLogger.info(
        'YT API: Manifest acquired. Video streams: ${manifest.videoOnly.length}, Muxed streams: ${manifest.muxed.length}',
      );

      // Fusionner tous les flux vidéo disponibles (Adaptive et Muxed)
      final allStreams = [...manifest.videoOnly, ...manifest.muxed];
      final videoStreams = allStreams
          .whereType<yt_explode.VideoStreamInfo>()
          .toList();

      if (videoStreams.isEmpty) {
        throw Exception('No video streams found in manifest');
      }

      // Trier par qualité décroissante (Index le plus haut en premier)
      videoStreams.sort(
        (a, b) => b.videoQuality.index.compareTo(a.videoQuality.index),
      );

      AppLogger.info(
        'YT API: Found ${videoStreams.length} video streams. Top quality: ${videoStreams.first.videoQuality}',
      );
      for (var s in videoStreams.take(8)) {
        AppLogger.info(
          ' - Qualité: ${s.videoQuality}, Format: ${s.container.name}, Adaptive: ${s.container.name != "muxed"}, Taille: ${(s.size.totalBytes / 1024 / 1024).toStringAsFixed(2)}MB',
        );
      }

      late yt_explode.StreamInfo streamInfo;

      // 1. Chercher d'abord un flux vidéo seul (Adaptive) en 720p MP4 (le plus stable)
      final streams720mp4 = manifest.videoOnly.where(
        (s) =>
            s.videoQuality.index == yt_explode.VideoQuality.high720.index &&
            s.container == yt_explode.StreamContainer.mp4,
      );

      if (streams720mp4.isNotEmpty) {
        streamInfo = streams720mp4.withHighestBitrate();
      } else {
        // 2. Sinon, prendre n'importe quel Adaptive 720p (WebM possible)
        final streams720any = manifest.videoOnly.where(
          (s) => s.videoQuality.index == yt_explode.VideoQuality.high720.index,
        );

        if (streams720any.isNotEmpty) {
          streamInfo = streams720any.withHighestBitrate();
        } else {
          // 3. Enfin, fallback sur le meilleur bitrate disponible ne dépassant pas 720p
          streamInfo = manifest.videoOnly
              .where(
                (s) =>
                    s.videoQuality.index <=
                    yt_explode.VideoQuality.high720.index,
              )
              .withHighestBitrate();
        }
      }

      if (streamInfo is yt_explode.VideoStreamInfo) {
        final yt_explode.VideoStreamInfo vInfo = streamInfo;
        AppLogger.info(
          'YT API: NATIVE CHOICE -> ${vInfo.videoQuality} (${vInfo.container.name}) - Bitrate: ${vInfo.bitrate}',
        );
      }

      AppLogger.info(
        'YT API: Selected stream size: ${streamInfo.size.totalBytes} (Format: ${streamInfo.container.name})',
      );

      final cacheDir = await getTemporaryDirectory();
      final videoFile = File('${cacheDir.path}/$videoId.mp4');
      if (await videoFile.exists()) await videoFile.delete();

      AppLogger.info(
        'YT API: Starting streaming download to ${videoFile.path}',
      );
      final fileSink = videoFile.openWrite();

      int downloadedBytes = 0;
      try {
        final stream = _yt.videos.streamsClient.get(streamInfo);
        final totalBytes = streamInfo.size.totalBytes;

        await for (final chunk in stream) {
          fileSink.add(chunk);
          downloadedBytes += chunk.length;

          if (onProgress != null) {
            onProgress(downloadedBytes / totalBytes);
          }

          // Optional: throttling logs to avoid spam
          if (downloadedBytes % (5 * 1024 * 1024) < 1024 * 1024) {
            final percent = (downloadedBytes / totalBytes * 100)
                .toStringAsFixed(1);
            AppLogger.info(
              'YT API: Progress $percent% (${(downloadedBytes / 1024 / 1024).toStringAsFixed(1)}MB / ${(totalBytes / 1024 / 1024).toStringAsFixed(1)}MB)',
            );
          }
        }
      } finally {
        await fileSink.flush();
        await fileSink.close();
      }

      AppLogger.info('YT API: Download finished. Real Size: $downloadedBytes');
      return videoFile;
    } catch (e, s) {
      AppLogger.error('YouTube Download Error in Service: $e', e, s);
      return null;
    }
  }

  void dispose() {
    _yt.close();
  }
}
