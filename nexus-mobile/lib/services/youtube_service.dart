import 'dart:convert';
import 'dart:io';
import 'package:http/http.dart' as http;
import 'package:youtube_explode_dart/youtube_explode_dart.dart' as yt_explode;
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

    // 2. Adaptive Chunking Calculation
    int uploadChunkSize;
    if (totalBytes < 50 * 1024 * 1024) {
      uploadChunkSize = totalBytes; // 1 chunk
    } else if (totalBytes < 200 * 1024 * 1024) {
      uploadChunkSize = 50 * 1024 * 1024;
    } else if (totalBytes < 500 * 1024 * 1024) {
      uploadChunkSize = 100 * 1024 * 1024;
    } else {
      uploadChunkSize = 256 * 1024 * 1024;
    }

    AppLogger.info(
      'YouTube: Using adaptive chunk size of ${(uploadChunkSize / 1024 / 1024).toStringAsFixed(1)} MB',
    );

    // 3. Upload by chunks using StreamedRequest
    int currentOffset = 0;
    try {
      while (currentOffset < totalBytes) {
        final end = (currentOffset + uploadChunkSize < totalBytes)
            ? currentOffset + uploadChunkSize
            : totalBytes;
        final currentChunkLength = end - currentOffset;

        AppLogger.info(
          'YouTube: Uploading chunk $currentOffset-${end - 1}/$totalBytes (${(currentChunkLength / 1024 / 1024).toStringAsFixed(1)} MB)',
        );

        bool chunkSuccess = false;
        int retries = 0;

        while (!chunkSuccess && retries < 3) {
          try {
            final request = http.StreamedRequest('PUT', Uri.parse(uploadUrl));
            request.headers.addAll({
              'Content-Length': currentChunkLength.toString(),
              'Content-Range': 'bytes $currentOffset-${end - 1}/$totalBytes',
            });

            // Pipe the chunk from file to request
            final chunkStream = videoFile.openRead(currentOffset, end);
            chunkStream.listen(
              request.sink.add,
              onDone: request.sink.close,
              onError: (e) => request.sink.close(),
              cancelOnError: true,
            );

            final streamedResponse = await request.send();
            final response = await http.Response.fromStream(streamedResponse);

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
              retries++;
              AppLogger.warn(
                'YouTube: Chunk upload failed (${response.statusCode}), retrying ($retries/3)...',
              );
              await Future.delayed(Duration(seconds: 2 * retries));
            } else {
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
      // Future-proofing
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

  Future<Stream<List<int>>?> getVideoStream(
    String videoId, {
    Function(double)? onProgress,
  }) async {
    try {
      final manifest = await _yt.videos.streamsClient.getManifest(videoId);
      final allStreams = [...manifest.videoOnly, ...manifest.muxed];
      final videoStreams = allStreams
          .whereType<yt_explode.VideoStreamInfo>()
          .toList();

      if (videoStreams.isEmpty) throw Exception('No video streams found');

      // Sort by quality
      videoStreams.sort(
        (a, b) => b.videoQuality.index.compareTo(a.videoQuality.index),
      );

      yt_explode.StreamInfo streamInfo;
      final streams720mp4 = manifest.videoOnly.where(
        (s) =>
            s.videoQuality.index == yt_explode.VideoQuality.high720.index &&
            s.container == yt_explode.StreamContainer.mp4,
      );

      if (streams720mp4.isNotEmpty) {
        streamInfo = streams720mp4.withHighestBitrate();
      } else {
        streamInfo = manifest.videoOnly
            .where(
              (s) =>
                  s.videoQuality.index <= yt_explode.VideoQuality.high720.index,
            )
            .withHighestBitrate();
      }

      return _yt.videos.streamsClient.get(streamInfo);
    } catch (e) {
      AppLogger.error('YouTube Get Stream Error: $e');
      return null;
    }
  }

  void dispose() {
    _yt.close();
  }
}
