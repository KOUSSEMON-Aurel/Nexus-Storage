import 'dart:async';
import 'package:flutter/services.dart';
import '../services/logger_service.dart';

class ThermalMonitor {
  static const _channel = MethodChannel('nexus/thermal');
  static bool _throttled = false;
  static Timer? _timer;

  static bool get isThrottled => _throttled;

  static void start() {
    _timer?.cancel();
    _timer = Timer.periodic(const Duration(seconds: 10), (_) async {
      try {
        final temp = await _channel.invokeMethod<double>('getCpuTemp');
        if (temp != null) {
          // Hysteresis: Throttle at 42°C, Resume at 38°C
          if (temp > 42.0 && !_throttled) {
            _throttled = true;
            AppLogger.warn('THERMAL THROTTLE: CPU Temp $temp°C. Slowing down...');
          } else if (temp < 38.0 && _throttled) {
            _throttled = false;
            AppLogger.info('THERMAL RESUME: CPU Temp $temp°C. Resuming normal speed.');
          }
        }
      } catch (_) {
        // Silent fail if thermal file is not accessible
      }
    });
  }

  static void stop() {
    _timer?.cancel();
  }
}
