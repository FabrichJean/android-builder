package app.webview

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.IBinder
import android.webkit.JavascriptInterface

/**
 * Service au premier plan qui garde le processus (et donc l'audio joué dans
 * la WebView) actif quand l'utilisateur revient à l'accueil ou éteint l'écran.
 * Démarré/arrêté par [MainActivity] au fil des événements play/pause détectés
 * dans la page (pont JS `AndroidPlayback`).
 */
class BackgroundPlaybackService : Service() {

    companion object {
        private const val CHANNEL_ID = "background_playback"
        private const val NOTIF_ID = 4201

        fun start(context: Context) {
            val intent = Intent(context, BackgroundPlaybackService::class.java)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                context.startForegroundService(intent)
            } else {
                context.startService(intent)
            }
        }

        fun stop(context: Context) {
            context.stopService(Intent(context, BackgroundPlaybackService::class.java))
        }
    }

    override fun onCreate() {
        super.onCreate()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID, "Lecture audio en arrière-plan", NotificationManager.IMPORTANCE_LOW,
            )
            channel.setShowBadge(false)
            getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        startForeground(NOTIF_ID, buildNotification())
        return START_NOT_STICKY
    }

    private fun buildNotification(): Notification {
        val openApp = Intent(this, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_CLEAR_TOP
        }
        val pending = PendingIntent.getActivity(
            this, 0, openApp,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val builder = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            Notification.Builder(this, CHANNEL_ID)
        } else {
            @Suppress("DEPRECATION")
            Notification.Builder(this)
        }
        return builder
            .setContentTitle(getString(R.string.app_name))
            .setContentText("Lecture audio en cours…")
            .setSmallIcon(R.mipmap.ic_launcher)
            .setContentIntent(pending)
            .setOngoing(true)
            .build()
    }

    override fun onBind(intent: Intent?): IBinder? = null
}

/** Pont JS : la page signale ici le début/la fin de la lecture audio/vidéo. */
internal class PlaybackBridge(private val activity: MainActivity) {
    @JavascriptInterface
    fun notifyPlaying() = activity.runOnUiThread { activity.onMediaPlaybackStateChanged(true) }

    @JavascriptInterface
    fun notifyStopped() = activity.runOnUiThread { activity.onMediaPlaybackStateChanged(false) }
}
