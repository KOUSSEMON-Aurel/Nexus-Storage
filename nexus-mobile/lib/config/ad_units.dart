import 'package:flutter/foundation.dart';

class AdUnits {
  // Toggle this to false before final production release!
  static const bool isTestMode = true;

  // Real Unit IDs provided by user
  static const String _prodAppOpenId = 'ca-app-pub-5869960840326306/5479807987';
  static const String _prodInterstitialId =
      'ca-app-pub-5869960840326306/9595891829';
  static const String _prodNativeId = 'ca-app-pub-5869960840326306/9961978471';
  static const String _prodBannerId = 'ca-app-pub-5869960840326306/7093626225';

  // Standard Google Test IDs (Android)
  static const String _testAppOpenId = 'ca-app-pub-3940256099942544/9257395915';
  static const String _testInterstitialId =
      'ca-app-pub-3940256099942544/1033173712';
  static const String _testNativeId = 'ca-app-pub-3940256099942544/2247696110';
  static const String _testBannerId = 'ca-app-pub-3940256099942544/6300978111';

  static String get appOpenId =>
      (isTestMode || kDebugMode) ? _testAppOpenId : _prodAppOpenId;

  static String get interstitialId =>
      (isTestMode || kDebugMode) ? _testInterstitialId : _prodInterstitialId;

  static String get nativeId =>
      (isTestMode || kDebugMode) ? _testNativeId : _prodNativeId;

  static String get bannerId =>
      (isTestMode || kDebugMode) ? _testBannerId : _prodBannerId;
}
