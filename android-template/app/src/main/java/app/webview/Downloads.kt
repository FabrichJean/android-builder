package app.webview

import android.Manifest
import android.app.DownloadManager
import android.content.ContentValues
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import android.util.Base64
import android.webkit.CookieManager
import android.webkit.JavascriptInterface
import android.webkit.URLUtil
import java.io.File
import java.io.IOException

/** Gère les téléchargements déclenchés par la page (liens http, blob:, data:). */
internal class Downloads(private val activity: MainActivity) {
    private var pendingAction: (() -> Unit)? = null

    private companion object {
        const val REQ_STORAGE = 4711
    }

    fun start(url: String, userAgent: String?, contentDisposition: String?, mimetype: String?) {
        when {
            url.startsWith("blob:") -> downloadBlob(url)
            url.startsWith("data:") -> saveDataUrl(url)
            else -> withStoragePermission { enqueueHttp(url, userAgent, contentDisposition, mimetype) }
        }
    }

    // Répond à onRequestPermissionsResult ; renvoie false si ce n'est pas notre requestCode.
    fun onPermissionResult(requestCode: Int, granted: Boolean): Boolean {
        if (requestCode != REQ_STORAGE) return false
        val action = pendingAction
        pendingAction = null
        if (granted) action?.invoke() else activity.toast("Permission de stockage refusée")
        return true
    }

    private fun enqueueHttp(url: String, userAgent: String?, contentDisposition: String?, mimetype: String?) {
        try {
            val fileName = URLUtil.guessFileName(url, contentDisposition, mimetype)
            val req = DownloadManager.Request(Uri.parse(url)).apply {
                setMimeType(mimetype)
                CookieManager.getInstance().getCookie(url)?.let { addRequestHeader("cookie", it) }
                if (!userAgent.isNullOrEmpty()) addRequestHeader("User-Agent", userAgent)
                setTitle(fileName)
                setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
                setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, fileName)
            }
            activity.getSystemService(DownloadManager::class.java).enqueue(req)
            activity.toast("Téléchargement de $fileName…")
        } catch (e: Exception) {
            activity.toast("Téléchargement impossible : ${e.message}")
        }
    }

    // blob: -> lu en JS (FileReader) puis renvoyé en base64 via l'interface.
    private fun downloadBlob(blobUrl: String) {
        val js = """
            (function(){
              try{
                fetch('$blobUrl').then(function(r){return r.blob();}).then(function(b){
                  var fr=new FileReader();
                  fr.onloadend=function(){ AndroidDownload.saveBase64((fr.result.split(',')[1]||''), (b.type||'')); };
                  fr.readAsDataURL(b);
                }).catch(function(e){ AndroidDownload.onError(''+e); });
              }catch(e){ AndroidDownload.onError(''+e); }
            })();
        """.trimIndent()
        activity.webView.evaluateJavascript(js, null)
    }

    private fun saveDataUrl(dataUrl: String) {
        val comma = dataUrl.indexOf(',')
        if (comma < 0) { activity.toast("data URL invalide"); return }
        val meta = dataUrl.substring(5, comma) // après "data:"
        val mime = meta.substringBefore(';').ifEmpty { "application/octet-stream" }
        val payload = dataUrl.substring(comma + 1)
        val bytes = if (meta.contains("base64")) Base64.decode(payload, Base64.DEFAULT)
        else Uri.decode(payload).toByteArray()
        saveBytesToDownloads("download_${System.currentTimeMillis()}${extFromMime(mime)}", mime, bytes)
    }

    // Appelé par l'interface JS (blob).
    fun saveBase64ToDownloads(base64: String, mime: String) {
        try {
            val bytes = Base64.decode(base64, Base64.DEFAULT)
            val m = mime.ifEmpty { "application/octet-stream" }
            saveBytesToDownloads("download_${System.currentTimeMillis()}${extFromMime(m)}", m, bytes)
        } catch (e: Exception) {
            activity.toast("Téléchargement échoué : ${e.message}")
        }
    }

    private fun saveBytesToDownloads(name: String, mime: String, bytes: ByteArray) {
        withStoragePermission {
            try {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                    val values = ContentValues().apply {
                        put(MediaStore.Downloads.DISPLAY_NAME, name)
                        put(MediaStore.Downloads.MIME_TYPE, mime)
                        put(MediaStore.Downloads.IS_PENDING, 1)
                    }
                    val uri = activity.contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
                        ?: throw IOException("insertion MediaStore impossible")
                    activity.contentResolver.openOutputStream(uri)?.use { it.write(bytes) }
                    values.clear()
                    values.put(MediaStore.Downloads.IS_PENDING, 0)
                    activity.contentResolver.update(uri, values, null, null)
                } else {
                    @Suppress("DEPRECATION")
                    val dir = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS)
                    dir.mkdirs()
                    File(dir, name).outputStream().use { it.write(bytes) }
                }
                activity.toast("Enregistré dans Téléchargements : $name")
            } catch (e: Exception) {
                activity.toast("Téléchargement échoué : ${e.message}")
            }
        }
    }

    // WRITE_EXTERNAL_STORAGE n'est requis que sur Android ≤ 9 (API < 29).
    private fun withStoragePermission(action: () -> Unit) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q ||
            activity.checkSelfPermission(Manifest.permission.WRITE_EXTERNAL_STORAGE) == PackageManager.PERMISSION_GRANTED
        ) {
            action()
        } else {
            pendingAction = action
            activity.requestPermissions(arrayOf(Manifest.permission.WRITE_EXTERNAL_STORAGE), REQ_STORAGE)
        }
    }

    private fun extFromMime(mime: String): String = when (mime.substringBefore(';').trim()) {
        "image/png" -> ".png"
        "image/jpeg" -> ".jpg"
        "image/gif" -> ".gif"
        "image/webp" -> ".webp"
        "image/svg+xml" -> ".svg"
        "application/pdf" -> ".pdf"
        "text/plain" -> ".txt"
        "text/csv" -> ".csv"
        "application/json" -> ".json"
        "application/zip" -> ".zip"
        "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" -> ".xlsx"
        "application/vnd.openxmlformats-officedocument.wordprocessingml.document" -> ".docx"
        else -> ""
    }
}

/** Pont JS pour récupérer les téléchargements blob: (base64 -> fichier). */
internal class DownloadBridge(private val downloads: Downloads, private val activity: MainActivity) {
    @JavascriptInterface
    fun saveBase64(base64: String, type: String) {
        activity.runOnUiThread { downloads.saveBase64ToDownloads(base64, type) }
    }

    @JavascriptInterface
    fun onError(msg: String) {
        activity.runOnUiThread { activity.toast("Téléchargement échoué : $msg") }
    }
}
