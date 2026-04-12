import 'dart:io';

enum DeviceTier { low, mid, high }

class DeviceProfiler {
  static DeviceTier? _cached;

  static Future<DeviceTier> getTier() async {
    if (_cached != null) return _cached!;

    final cores = Platform.numberOfProcessors;
    final ram = await _getRamMb();

    _cached = switch ((cores, ram)) {
      (>= 8, >= 6000) => DeviceTier.high,  // Flagship
      (>= 4, >= 3000) => DeviceTier.mid,   // Mid-range
      _               => DeviceTier.low,   // Budget / vieux
    };
    return _cached!;
  }

  static Future<int> _getRamMb() async {
    if (!Platform.isAndroid) return 8000; // Fallback for Desktop/iOS if needed
    
    try {
      final f = File('/proc/meminfo');
      if (await f.exists()) {
        final lines = await f.readAsLines();
        for (final line in lines) {
          if (line.startsWith('MemTotal')) {
            final kb = int.parse(line.replaceAll(RegExp(r'[^0-9]'), ''));
            return kb ~/ 1024;
          }
        }
      }
    } catch (_) {}
    return 2000; // fallback conservateur
  }
}
