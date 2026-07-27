package app.webview

import android.content.Intent
import android.os.BatteryManager
import android.os.Build
import android.os.VibrationEffect
import android.os.Vibrator
import android.webkit.JavascriptInterface

/** Quelques capacités natives supplémentaires (démo) : vibration, partage, batterie. */
internal class DeviceCapabilities(private val activity: MainActivity) {
    fun vibrate(ms: Long) {
        val v = activity.getSystemService(Vibrator::class.java) ?: return
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            v.vibrate(VibrationEffect.createOneShot(ms, VibrationEffect.DEFAULT_AMPLITUDE))
        } else {
            @Suppress("DEPRECATION")
            v.vibrate(ms)
        }
    }

    fun share(text: String) = activity.runOnUiThread {
        val intent = Intent(Intent.ACTION_SEND).apply {
            type = "text/plain"
            putExtra(Intent.EXTRA_TEXT, text)
        }
        activity.startActivity(Intent.createChooser(intent, null))
    }

    fun batteryLevel(): Int {
        val bm = activity.getSystemService(BatteryManager::class.java)
        return bm?.getIntProperty(BatteryManager.BATTERY_PROPERTY_CAPACITY) ?: -1
    }
}

/** Pont JS pour les capacités natives (vibration, partage, batterie). */
internal class DeviceBridge(private val caps: DeviceCapabilities) {
    @JavascriptInterface
    fun vibrate(ms: Long) = caps.vibrate(ms)

    @JavascriptInterface
    fun share(text: String) = caps.share(text)

    @JavascriptInterface
    fun batteryLevel(): Int = caps.batteryLevel()
}
