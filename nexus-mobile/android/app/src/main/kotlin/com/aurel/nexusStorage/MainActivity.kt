package com.aurel.nexusStorage

import androidx.annotation.NonNull
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import java.io.File

class MainActivity: FlutterActivity() {
    private val CHANNEL = "nexus/thermal"

    override fun configureFlutterEngine(@NonNull flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, CHANNEL).setMethodCallHandler {
            call, result ->
            if (call.method == "getCpuTemp") {
                val temp = getCpuTemp()
                if (temp != null) {
                    result.success(temp)
                } else {
                    result.error("UNAVAILABLE", "Could not read CPU temperature", null)
                }
            } else {
                result.notImplemented()
            }
        }
    }

    private fun getCpuTemp(): Double? {
        val thermalFiles = arrayOf(
            "/sys/class/thermal/thermal_zone0/temp",
            "/sys/class/thermal/thermal_zone1/temp",
            "/sys/class/thermal/thermal_zone2/temp"
        )
        
        for (path in thermalFiles) {
            try {
                val f = File(path)
                if (f.exists()) {
                    val tempStr = f.readText().trim()
                    val tempRaw = tempStr.toDoubleOrNull()
                    if (tempRaw != null) {
                        // Some devices return millidegrees (50000 = 50°C), some return degrees (50 = 50°C)
                        return if (tempRaw > 1000) tempRaw / 1000.0 else tempRaw
                    }
                }
            } catch (e: Exception) {
                // Try next file
            }
        }
        return null
    }
}
