package com.pravera.flutter_foreground_task.models

import android.content.Context
import android.content.pm.ServiceInfo
import android.os.Build
import com.pravera.flutter_foreground_task.PreferencesKey

data class ForegroundServiceTypes(val value: Int) {
    companion object {
        fun getData(context: Context): ForegroundServiceTypes {
            val prefs = context.getSharedPreferences(
                PreferencesKey.FOREGROUND_SERVICE_TYPES_PREFS, Context.MODE_PRIVATE)

            var value = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                prefs.getInt(PreferencesKey.FOREGROUND_SERVICE_TYPES, ServiceInfo.FOREGROUND_SERVICE_TYPE_MANIFEST)
            } else {
                prefs.getInt(PreferencesKey.FOREGROUND_SERVICE_TYPES, 0) // none
            }

            // Android 10+ (Q) strictness: if we have a type in manifest, it's better to pass it.
            // On Android 14+ (UPSIDE_DOWN_CAKE) it is MANDATORY.
            // We force DATA_SYNC (1) for Nexus to avoid "foregroundServiceType : 0" errors.
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                if (value == 0 || value == ServiceInfo.FOREGROUND_SERVICE_TYPE_MANIFEST) {
                    value = ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC
                    android.util.Log.d("NexusPlugin", "Android 10+ detected: Forced DATA_SYNC (1) for service initialization (prev was 0 or -1)")
                }
            }

            return ForegroundServiceTypes(value = value)
        }

        fun setData(context: Context, map: Map<*, *>?) {
            val prefs = context.getSharedPreferences(
                PreferencesKey.FOREGROUND_SERVICE_TYPES_PREFS, Context.MODE_PRIVATE)

            var value = 0
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                val serviceTypes = map?.get(PreferencesKey.FOREGROUND_SERVICE_TYPES) as? List<*>
                if (serviceTypes != null) {
                    for (serviceType in serviceTypes) {
                        getForegroundServiceTypeFlag(serviceType)?.let {
                            value = value or it
                        }
                    }
                }
            }

            // Always save the value to ensure it's persisted even if it's 0 (none)
            // or if we're overwriting a previous value.
            with(prefs.edit()) {
                putInt(PreferencesKey.FOREGROUND_SERVICE_TYPES, value)
                commit()
            }
        }

        fun clearData(context: Context) {
            val prefs = context.getSharedPreferences(
                PreferencesKey.FOREGROUND_SERVICE_TYPES_PREFS, Context.MODE_PRIVATE)

            with(prefs.edit()) {
                clear()
                commit()
            }
        }

        private fun getForegroundServiceTypeFlag(type: Any?): Int? {
            val typeInt = when (type) {
                is Int -> type
                is Long -> type.toInt()
                is String -> type.toIntOrNull()
                else -> null
            } ?: return null

            return when (typeInt) {
                0 -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) ServiceInfo.FOREGROUND_SERVICE_TYPE_CAMERA else null
                1 -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE else null
                2 -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC else null
                3 -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) ServiceInfo.FOREGROUND_SERVICE_TYPE_HEALTH else null
                4 -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) ServiceInfo.FOREGROUND_SERVICE_TYPE_LOCATION else null
                5 -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK else null
                6 -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PROJECTION else null
                7 -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE else null
                8 -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) ServiceInfo.FOREGROUND_SERVICE_TYPE_PHONE_CALL else null
                9 -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) ServiceInfo.FOREGROUND_SERVICE_TYPE_REMOTE_MESSAGING else null
                10 -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) ServiceInfo.FOREGROUND_SERVICE_TYPE_SHORT_SERVICE else null
                11 -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE else null
                12 -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) ServiceInfo.FOREGROUND_SERVICE_TYPE_SYSTEM_EXEMPTED else null
                13 -> if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.VANILLA_ICE_CREAM) ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PROCESSING else null
                else -> null
            }
        }
    }
}
