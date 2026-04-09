import 'dart:convert';
import 'dart:io';
import 'package:http/http.dart' as http;
import 'auth_service.dart';

class YouTubeService {
  static final YouTubeService _instance = YouTubeService._internal();
  factory YouTubeService() => _instance;
  YouTubeService._internal();

  final AuthService _auth = AuthService();

  Future<String?> uploadVideo({
    required File videoFile,
    required String title,
    required String description,
    Function(double)? onProgress,
  }) async {
    final token = await _auth.getAccessToken();
    if (token == null) return null;

    // Resumable upload start
    final metadata = {
      'snippet': {
        'title': title,
        'description': description,
        'categoryId': '22', // People & Blogs
      },
      'status': {
        'privacyStatus': 'unlisted', // Most secure for storage
        'selfDeclaredMadeForKids': false,
      }
    };

    final response = await http.post(
      Uri.parse('https://www.googleapis.com/upload/youtube/v3/videos?uploadType=resumable&part=snippet,status'),
      headers: {
        'Authorization': 'Bearer $token',
        'Content-Type': 'application/json; charset=UTF-8',
        'X-Upload-Content-Length': videoFile.lengthSync().toString(),
        'X-Upload-Content-Type': 'video/*',
      },
      body: jsonEncode(metadata),
    );

    if (response.statusCode != 200) {
      print('Upload initialization failed: ${response.body}');
      return null;
    }

    final uploadUrl = response.headers['location'];
    if (uploadUrl == null) return null;

    // Actual upload in chunks or full (Mobile is safer with smaller chunks if background, but let's do a basic stream for now)
    final totalBytes = videoFile.lengthSync();
    int sentBytes = 0;

    final request = http.StreamedRequest('PUT', Uri.parse(uploadUrl));
    request.headers['Content-Length'] = totalBytes.toString();
    
    final fileStream = videoFile.openRead();
    fileStream.listen(
      (chunk) {
        request.sink.add(chunk);
        sentBytes += chunk.length;
        if (onProgress != null) {
          onProgress(sentBytes / totalBytes);
        }
      },
      onDone: () async {
        await request.sink.close();
      },
      onError: (e) {
        request.sink.addError(e);
      },
      cancelOnError: true,
    );

    final uploadResponse = await http.Response.fromStream(await request.send());
    if (uploadResponse.statusCode == 200 || uploadResponse.statusCode == 201) {
      final json = jsonDecode(uploadResponse.body);
      return json['id'];
    } else {
      print('Upload failed: ${uploadResponse.body}');
      return null;
    }
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
}
