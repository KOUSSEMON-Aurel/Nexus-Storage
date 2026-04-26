import 'package:youtube_explode_dart/youtube_explode_dart.dart';
import 'package:nexus_mobile/services/logger_service.dart';

void main() async {
  final yt = YoutubeExplode();
  final videoId = 'Q-6teAgx8HA'; // ID vu dans les logs logcat

  try {
    AppLogger.info('--- Diagnostic YouTube Explode ---');
    AppLogger.info('Vidéo ID: $videoId');

    final manifest = await yt.videos.streamsClient.getManifest(videoId);
    AppLogger.info(
      'Flux vidéo uniquement (Adaptive): ${manifest.videoOnly.length}',
    );
    AppLogger.info('Flux multiplexés (Muxed): ${manifest.muxed.length}');

    final allStreams = [
      ...manifest.videoOnly,
      ...manifest.muxed,
    ].cast<VideoStreamInfo>();
    for (final VideoStreamInfo stream in allStreams) {
      AppLogger.info(
        ' - [${stream.container.name}] ${stream.videoQuality} (Index: ${stream.videoQuality.index}) | Adaptive: ${stream is VideoOnlyStreamInfo} | Size: ${stream.size.totalMegaBytes.toStringAsFixed(2)}MB',
      );
    }

    if (allStreams.isEmpty) {
      AppLogger.error('ERREUR: Aucun flux trouvé !');
    }
  } catch (e, s) {
    AppLogger.error('EXCEPTION FATALE: $e', e, s);
  } finally {
    yt.close();
  }
}
