package com.aurel.nexusStorage

import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import android.content.ContentValues
import android.content.Context
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import java.io.File
import java.io.FileInputStream
import java.io.FileOutputStream
import java.io.OutputStream

class MainActivity: FlutterActivity() {
    private val MEDIA_CHANNEL = "com.aurel.nexus/media_store"
    private val THERMAL_CHANNEL = "nexus/thermal"

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        
        // Media Store Channel
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, MEDIA_CHANNEL).setMethodCallHandler { call, result ->
            if (call.method == "saveFileToDownloads") {
                val tempPath = call.argument<String>("tempPath")
                val fileName = call.argument<String>("fileName")
                val relativePath = call.argument<String>("relativePath")

                if (tempPath != null && fileName != null) {
                    val success = saveFileToDownloads(tempPath, fileName, relativePath ?: "NexusStorage")
                    if (success) {
                        result.success(true)
                    } else {
                        result.error("STORAGE_ERROR", "Failed to save file to MediaStore", null)
                    }
                } else {
                    result.error("INVALID_ARGUMENTS", "Path or FileName is null", null)
                }
            } else {
                result.notImplemented()
            }
        }

        // Thermal Channel
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, THERMAL_CHANNEL).setMethodCallHandler { call, result ->
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

    private fun saveFileToDownloads(tempPath: String, fileName: String, relativeSubDir: String): Boolean {
        val resolver = contentResolver
        val sourceFile = File(tempPath)
        if (!sourceFile.exists()) return false

        val contentValues = ContentValues().apply {
            put(MediaStore.MediaColumns.DISPLAY_NAME, fileName)
            val mimeType = when {
                fileName.endsWith(".mp4") -> "video/mp4"
                fileName.endsWith(".pdf") -> "application/pdf"
                fileName.endsWith(".jpg") || fileName.endsWith(".jpeg") -> "image/jpeg"
                fileName.endsWith(".png") -> "image/png"
                else -> "application/octet-stream"
            }
            put(MediaStore.MediaColumns.MIME_TYPE, mimeType)
            
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                put(MediaStore.MediaColumns.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS + File.separator + relativeSubDir)
                put(MediaStore.MediaColumns.IS_PENDING, 1)
            }
        }

        val collection = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            MediaStore.Downloads.EXTERNAL_CONTENT_URI
        } else {
            MediaStore.Files.getContentUri("external")
        }

        val uri = resolver.insert(collection, contentValues) ?: return false

        return try {
            resolver.openOutputStream(uri)?.use { outputStream ->
                FileInputStream(sourceFile).use { inputStream ->
                    inputStream.copyTo(outputStream)
                }
            }

            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                contentValues.clear()
                contentValues.put(MediaStore.MediaColumns.IS_PENDING, 0)
                resolver.update(uri, contentValues, null, null)
            }
            true
        } catch (e: Exception) {
            resolver.delete(uri, null, null)
            false
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
