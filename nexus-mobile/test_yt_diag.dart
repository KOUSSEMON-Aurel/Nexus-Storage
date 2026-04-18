import 'package:youtube_explode_dart/youtube_explode_dart.dart';

void main() async {
  final yt = YoutubeExplode();
  final videoId = 'Q-6teAgx8HA'; // ID vu dans les logs logcat
  
  try {
    print('--- Diagnostic YouTube Explode ---');
    print('Vidéo ID: $videoId');
    
    final manifest = await yt.videos.streamsClient.getManifest(videoId);
    print('Flux vidéo uniquement (Adaptive): ${manifest.videoOnly.length}');
    print('Flux multiplexés (Muxed): ${manifest.muxed.length}');
    
    final allStreams = [...manifest.videoOnly, ...manifest.muxed];
    for (var stream in allStreams) {
      if (stream is VideoStreamInfo) {
        print(' - [${stream.container.name}] ${stream.videoQuality} (Index: ${stream.videoQuality.index}) | Adaptive: ${stream is VideoOnlyStreamInfo} | Size: ${stream.size.totalMegaBytes.toStringAsFixed(2)}MB');
      }
    }
    
    if (allStreams.isEmpty) {
      print('ERREUR: Aucun flux trouvé !');
    }
    
  } catch (e) {
    print('EXCEPTION FATALE: $e');
  } finally {
    yt.close();
  }
}
