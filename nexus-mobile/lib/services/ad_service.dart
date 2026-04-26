import 'package:google_mobile_ads/google_mobile_ads.dart';
import '../config/ad_units.dart';
import 'logger_service.dart';

class AdService {
  static final AdService _instance = AdService._internal();
  factory AdService() => _instance;
  AdService._internal();

  late final AppOpenAdManager appOpenAdManager;
  late final InterstitialAdManager interstitialAdManager;
  late final BannerAdManager bannerAdManager;

  bool _initialized = false;

  Future<void> init() async {
    if (_initialized) return;

    AppLogger.info('AdService: Initializing Mobile Ads SDK');
    await MobileAds.instance.initialize();

    appOpenAdManager = AppOpenAdManager()..loadAd();
    interstitialAdManager = InterstitialAdManager()..loadAd();
    bannerAdManager =
        BannerAdManager(); // Not loaded immediately to respect UI constraints

    _initialized = true;
  }
}

class BannerAdManager {
  BannerAd? _bannerAd;
  bool _isLoaded = false;

  bool get isLoaded => _isLoaded;
  BannerAd? get ad => _bannerAd;

  void load(int viewWidth) {
    if (_isLoaded) return;

    // We get the size asynchronously, so we wrap it
    AdSize.getCurrentOrientationAnchoredAdaptiveBannerAdSize(viewWidth).then((
      size,
    ) {
      _bannerAd = BannerAd(
        adUnitId: AdUnits.bannerId,
        size: size ?? AdSize.banner,
        request: const AdRequest(),
        listener: BannerAdListener(
          onAdLoaded: (_) => _isLoaded = true,
          onAdFailedToLoad: (ad, error) {
            AppLogger.error('BannerAd failed to load: $error');
            ad.dispose();
            _bannerAd = null;
            _isLoaded = false;
          },
        ),
      )..load();
    });
  }

  void dispose() {
    _bannerAd?.dispose();
    _bannerAd = null;
    _isLoaded = false;
  }
}

class AppOpenAdManager {
  AppOpenAd? _appOpenAd;
  bool _isShowingAd = false;
  DateTime? _lastShowTime;

  bool get isAdAvailable => _appOpenAd != null;

  /// Load an AppOpenAd.
  void loadAd() {
    AppLogger.info('AdService: Loading AppOpenAd');
    AppOpenAd.load(
      adUnitId: AdUnits.appOpenId,
      request: const AdRequest(),
      adLoadCallback: AppOpenAdLoadCallback(
        onAdLoaded: (ad) {
          AppLogger.info('AdService: AppOpenAd loaded');
          _appOpenAd = ad;
        },
        onAdFailedToLoad: (error) {
          AppLogger.error('AdService: AppOpenAd failed to load: $error');
          _appOpenAd = null;
        },
      ),
    );
  }

  /// Show the ad if available.
  void showAdIfAvailable() {
    if (_appOpenAd == null || _isShowingAd) {
      AppLogger.info('AdService: AppOpenAd not available or already showing');
      loadAd();
      return;
    }

    // Limit frequency: don't show more than once every 4 hours to avoid annoying users
    if (_lastShowTime != null &&
        DateTime.now().difference(_lastShowTime!).inMinutes < 30) {
      return;
    }

    _appOpenAd!.fullScreenContentCallback = FullScreenContentCallback(
      onAdShowedFullScreenContent: (ad) {
        _isShowingAd = true;
      },
      onAdDismissedFullScreenContent: (ad) {
        _isShowingAd = false;
        _lastShowTime = DateTime.now();
        ad.dispose();
        _appOpenAd = null;
        loadAd();
      },
      onAdFailedToShowFullScreenContent: (ad, error) {
        _isShowingAd = false;
        ad.dispose();
        _appOpenAd = null;
        loadAd();
      },
    );

    _appOpenAd!.show();
  }
}

class InterstitialAdManager {
  InterstitialAd? _interstitialAd;
  int _numAttempts = 0;

  void loadAd() {
    InterstitialAd.load(
      adUnitId: AdUnits.interstitialId,
      request: const AdRequest(),
      adLoadCallback: InterstitialAdLoadCallback(
        onAdLoaded: (ad) {
          AppLogger.info('AdService: InterstitialAd loaded');
          _interstitialAd = ad;
          _numAttempts = 0;
        },
        onAdFailedToLoad: (error) {
          _numAttempts++;
          _interstitialAd = null;
          if (_numAttempts <= 2) {
            loadAd();
          }
        },
      ),
    );
  }

  void showAd() {
    if (_interstitialAd == null) {
      loadAd();
      return;
    }

    _interstitialAd!.fullScreenContentCallback = FullScreenContentCallback(
      onAdDismissedFullScreenContent: (ad) {
        ad.dispose();
        _interstitialAd = null;
        loadAd();
      },
      onAdFailedToShowFullScreenContent: (ad, error) {
        ad.dispose();
        _interstitialAd = null;
        loadAd();
      },
    );

    _interstitialAd!.show();
  }
}
