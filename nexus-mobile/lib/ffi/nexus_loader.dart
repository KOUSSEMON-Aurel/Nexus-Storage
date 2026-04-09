import 'dart:ffi';
import 'dart:io';
import 'nexus_bindings.dart';

class NexusLoader {
  static const String _libName = 'nexus_core';

  static NexusCoreBindings? _bindings;

  static NexusCoreBindings get bindings {
    if (_bindings == null) {
      _bindings = NexusCoreBindings(_loadLibrary());
    }
    return _bindings!;
  }

  static DynamicLibrary _loadLibrary() {
    if (Platform.isAndroid) {
      return DynamicLibrary.open('lib$_libName.so');
    }
    if (Platform.isIOS) {
      // On iOS, static libraries are linked into the executable
      return DynamicLibrary.executable();
    }
    if (Platform.isLinux) {
      return DynamicLibrary.open('lib$_libName.so');
    }
    if (Platform.isWindows) {
      return DynamicLibrary.open('$_libName.dll');
    }
    if (Platform.isMacOS) {
      return DynamicLibrary.open('lib$_libName.dylib');
    }
    throw UnsupportedError('Platform not supported: ${Platform.operatingSystem}');
  }
}
