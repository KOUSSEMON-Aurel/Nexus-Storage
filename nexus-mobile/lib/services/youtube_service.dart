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

  /// Chunk size for resumable YouTube upload (5 MB — minimum allowed by YouTube API).
  static const int _chunkSize = 5 * 1024 * 1024;

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
      'status': {
        'privacyStatus': 'unlisted',
        'selfDeclaredMadeForKids': false,
      }
    };

    final initResponse = await http.post(
      Uri.parse('https://www.googleapis.com/upload/youtube/v3/videos?uploadType=resumable&part=snippet,status'),
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

    AppLogger.info('YouTube resumable upload started. Total: ${(totalBytes / 1024 / 1024).toStringAsFixed(1)} MB');

    AppLogger.info('YouTube: Uploading via StreamedRequest (${totalBytes} bytes)...');
    
    final request = http.StreamedRequest('PUT', Uri.parse(uploadUrl));
    request.headers.addAll({
      // Authorization header is removed as the uploadUrl is already a signed session URL
      'Content-Type': 'video/mp4',
      'Content-Length': totalBytes.toString(),
      'Content-Range': 'bytes 0-${totalBytes - 1}/$totalBytes',
    });
    
    int bytesSent = 0;
    final fileStream = videoFile.openRead();
    
    fileStream.listen(
      (data) {
        request.sink.add(data);
        bytesSent += data.length;
        onProgress?.call(bytesSent / totalBytes);
      },
      onDone: () => request.sink.close(),
      onError: (e) => request.sink.addError(e),
    );

    final streamedResponse = await request.send();
    final response = await http.Response.fromStream(streamedResponse);

    if (response.statusCode == 200 || response.statusCode == 201) {
      final json = jsonDecode(response.body);
      AppLogger.info('YouTube: Upload complete! Video ID: ${json['id']}');
      return json['id'] as String?;
    } else {
      AppLogger.error('YouTube upload failed (${response.statusCode}): ${response.body}');
      return null;
    }

    AppLogger.error('Upload loop exited without completing');
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

  Future<File?> downloadVideo(String videoId, {bool isHighMode = false, Function(double)? onProgress}) async {
    try {
      AppLogger.info('YT API: Getting manifest for $videoId');
      final manifest = await _yt.videos.streamsClient.getManifest(videoId);
      AppLogger.info('YT API: Manifest acquired. Video streams: ${manifest.videoOnly.length}, Muxed streams: ${manifest.muxed.length}');

      // Fusionner tous les flux vidéo disponibles (Adaptive et Muxed)
      final allStreams = [...manifest.videoOnly, ...manifest.muxed];
      final videoStreams = allStreams.whereType<yt_explode.VideoStreamInfo>().toList();
      
      if (videoStreams.isEmpty) throw Exception('No video streams found in manifest');

      // Trier par qualité décroissante (Index le plus haut en premier)
      videoStreams.sort((a, b) => b.videoQuality.index.compareTo(a.videoQuality.index));

      AppLogger.info('YT API: Found ${videoStreams.length} video streams. Top quality: ${videoStreams.first.videoQuality}');
      for (var s in videoStreams.take(8)) {
        AppLogger.info(' - Qualité: ${s.videoQuality}, Format: ${s.container.name}, Adaptive: ${s.container.name != "muxed"}, Taille: ${(s.size.totalBytes / 1024 / 1024).toStringAsFixed(2)}MB');
      }

      late yt_explode.StreamInfo streamInfo;

      // 1. Chercher d'abord un flux vidéo seul (Adaptive) en 720p MP4 (le plus stable)
      final streams720mp4 = manifest.videoOnly.where((s) => 
        s.videoQuality.index == yt_explode.VideoQuality.high720.index && 
        s.container == yt_explode.StreamContainer.mp4
      );

      if (streams720mp4.isNotEmpty) {
        streamInfo = streams720mp4.withHighestBitrate();
      } else {
        // 2. Sinon, prendre n'importe quel Adaptive 720p (WebM possible)
        final streams720any = manifest.videoOnly.where((s) => 
          s.videoQuality.index == yt_explode.VideoQuality.high720.index
        );
        
        if (streams720any.isNotEmpty) {
          streamInfo = streams720any.withHighestBitrate();
        } else {
          // 3. Enfin, fallback sur le meilleur bitrate disponible ne dépassant pas 720p
          streamInfo = manifest.videoOnly
            .where((s) => s.videoQuality.index <= yt_explode.VideoQuality.high720.index)
            .withHighestBitrate();
        }
      }

        if (streamInfo is yt_explode.VideoStreamInfo) {
          final yt_explode.VideoStreamInfo vInfo = streamInfo;
          AppLogger.info('YT API: NATIVE CHOICE -> ${vInfo.videoQuality} (${vInfo.container.name}) - Bitrate: ${vInfo.bitrate}');
        }
      
      AppLogger.info('YT API: Selected stream size: ${streamInfo.size.totalBytes} (Format: ${streamInfo.container.name})');

      final cacheDir = await getTemporaryDirectory();
      final videoFile = File('${cacheDir.path}/$videoId.mp4');
      if (await videoFile.exists()) await videoFile.delete();
      
      AppLogger.info('YT API: Starting stream download to ${videoFile.path}');
      final stream = _yt.videos.streamsClient.get(streamInfo);
      final fileStream = videoFile.openWrite();
      
      int totalBytes = streamInfo.size.totalBytes;
      int downloadedBytes = 0;
      int lastLogBytes = 0;

      try {
        await for (final data in stream) {
          fileStream.add(data);
          downloadedBytes += data.length;
          
          // Log progress every 1MB
          if (downloadedBytes - lastLogBytes > 1024 * 1024) {
            final percent = (downloadedBytes / totalBytes * 100).toStringAsFixed(1);
            AppLogger.info('YT API: Progress $percent% (${(downloadedBytes/1024/1024).toStringAsFixed(1)}MB / ${(totalBytes/1024/1024).toStringAsFixed(1)}MB)');
            lastLogBytes = downloadedBytes;
          }

          if (onProgress != null) {
            onProgress(downloadedBytes / totalBytes);
          }
        }
      } finally {
        await fileStream.flush();
        await fileStream.close();
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
