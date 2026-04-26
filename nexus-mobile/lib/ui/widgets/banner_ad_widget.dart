import 'package:flutter/material.dart';
import 'package:google_mobile_ads/google_mobile_ads.dart';
import '../../services/ad_service.dart';

class BannerAdWidget extends StatefulWidget {
  const BannerAdWidget({super.key});

  @override
  State<BannerAdWidget> createState() => _BannerAdWidgetState();
}

class _BannerAdWidgetState extends State<BannerAdWidget> {
  late final BannerAdManager _manager;

  @override
  void initState() {
    super.initState();
    _manager = AdService().bannerAdManager;
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (!_manager.isLoaded) {
      _manager.load(MediaQuery.of(context).size.width.truncate());
      Future.delayed(const Duration(seconds: 2), () {
        if (mounted) setState(() {});
      });
    }
  }

  @override
  void dispose() {
    _manager.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (!_manager.isLoaded || _manager.ad == null) {
      return const SizedBox.shrink();
    }

    return SafeArea(
      top: false,
      child: SizedBox(
        width: _manager.ad!.size.width.toDouble(),
        height: _manager.ad!.size.height.toDouble(),
        child: AdWidget(ad: _manager.ad!),
      ),
    );
  }
}
