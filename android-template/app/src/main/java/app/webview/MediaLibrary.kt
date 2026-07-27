package app.webview

import android.Manifest
import android.content.ContentUris
import android.content.pm.PackageManager
import android.os.Build
import android.provider.MediaStore
import android.util.Log
import android.webkit.JavascriptInterface
import org.json.JSONArray
import org.json.JSONObject
import java.io.IOException
import java.io.InputStream
import java.io.OutputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.util.concurrent.Executors

/**
 * Lecture de la médiathèque du téléphone (musique/vidéos/photos), exposée en
 * JS sous `AndroidMedia` (voir [MediaBridge]). Les fichiers sont diffusés par
 * un vrai petit serveur HTTP en boucle locale (127.0.0.1) — voir
 * [LoopbackMediaServer] pour la raison (le domaine virtuel https de
 * WebViewAssetLoader n'est pas joignable par le lecteur média natif).
 */
internal class MediaLibrary(private val activity: MainActivity) {
    private val pendingPermission = HashMap<Int, String>()
    private val server = LoopbackMediaServer(::buildMediaBody).also { it.start() }

    private companion object {
        const val REQ_AUDIO = 4801
        const val REQ_VIDEO = 4802
        const val REQ_IMAGE = 4803
    }

    fun close() = server.close()

    // Répond à onRequestPermissionsResult ; renvoie false si ce n'est pas notre requestCode.
    fun onPermissionResult(requestCode: Int, granted: Boolean): Boolean {
        val type = pendingPermission.remove(requestCode) ?: return false
        activity.webView.evaluateJavascript(
            "window.onMediaPermission && window.onMediaPermission(${JSONObject.quote(type)}, $granted);",
            null,
        )
        return true
    }

    private fun permissionName(type: String): String = when {
        Build.VERSION.SDK_INT >= 33 && type == "audio" -> Manifest.permission.READ_MEDIA_AUDIO
        Build.VERSION.SDK_INT >= 33 && type == "video" -> Manifest.permission.READ_MEDIA_VIDEO
        Build.VERSION.SDK_INT >= 33 && type == "image" -> Manifest.permission.READ_MEDIA_IMAGES
        else -> Manifest.permission.READ_EXTERNAL_STORAGE
    }

    fun hasPermission(type: String): Boolean =
        activity.checkSelfPermission(permissionName(type)) == PackageManager.PERMISSION_GRANTED

    fun requestPermission(type: String) = activity.runOnUiThread {
        if (hasPermission(type)) {
            activity.webView.evaluateJavascript(
                "window.onMediaPermission && window.onMediaPermission(${JSONObject.quote(type)}, true);",
                null,
            )
            return@runOnUiThread
        }
        val code = when (type) { "audio" -> REQ_AUDIO; "video" -> REQ_VIDEO; else -> REQ_IMAGE }
        pendingPermission[code] = type
        activity.requestPermissions(arrayOf(permissionName(type)), code)
    }

    // Interroge le MediaStore et renvoie un JSON avec l'URL de streaming (servie
    // par buildMediaBody() via le serveur HTTP local).
    fun list(type: String): String {
        if (!hasPermission(type)) return "[]"
        val (uri, nameCol, extraCols) = when (type) {
            "audio" -> Triple(
                MediaStore.Audio.Media.EXTERNAL_CONTENT_URI,
                MediaStore.Audio.Media.DISPLAY_NAME,
                arrayOf(MediaStore.Audio.Media.ARTIST, MediaStore.Audio.Media.DURATION),
            )
            "video" -> Triple(
                MediaStore.Video.Media.EXTERNAL_CONTENT_URI,
                MediaStore.Video.Media.DISPLAY_NAME,
                arrayOf(MediaStore.Video.Media.DURATION),
            )
            else -> Triple(
                MediaStore.Images.Media.EXTERNAL_CONTENT_URI,
                MediaStore.Images.Media.DISPLAY_NAME,
                emptyArray(),
            )
        }
        val idCol = MediaStore.MediaColumns._ID
        val sizeCol = MediaStore.MediaColumns.SIZE
        val projection = arrayOf(idCol, nameCol, sizeCol, *extraCols)
        // Pas de "LIMIT" dans le sortOrder : MediaProvider valide strictement ce
        // paramètre depuis Android 10 (stockage cloisonné) et rejette tout ce qui
        // n'est pas juste "colonne ASC/DESC" -> on plafonne dans la boucle à la place.
        val sort = "${MediaStore.MediaColumns.DATE_ADDED} DESC"
        val out = JSONArray()
        try {
            activity.contentResolver.query(uri, projection, null, null, sort)?.use { c ->
                val idIdx = c.getColumnIndexOrThrow(idCol)
                val nameIdx = c.getColumnIndexOrThrow(nameCol)
                val sizeIdx = c.getColumnIndexOrThrow(sizeCol)
                val artistIdx = if (type == "audio") c.getColumnIndex(MediaStore.Audio.Media.ARTIST) else -1
                val durationIdx = if (type != "image") c.getColumnIndex(MediaStore.MediaColumns.DURATION) else -1
                while (c.moveToNext() && out.length() < 300) {
                    val id = c.getLong(idIdx)
                    val o = JSONObject()
                    o.put("id", id)
                    o.put("name", c.getString(nameIdx) ?: "")
                    o.put("size", c.getLong(sizeIdx))
                    o.put("url", "http://127.0.0.1:${server.port}/media/$type/$id")
                    if (artistIdx >= 0) o.put("artist", c.getString(artistIdx) ?: "")
                    if (durationIdx >= 0) o.put("duration", c.getLong(durationIdx))
                    out.put(o)
                }
            }
        } catch (e: Exception) {
            Log.e("MediaBridge", "list($type) a échoué", e)
            return "[]"
        }
        return out.toString()
    }

    // Construit le corps de réponse d'un fichier de la médiathèque pour le
    // serveur HTTP local, avec support des requêtes "Range" (requis par
    // <audio>/<video> pour démarrer la lecture).
    private fun buildMediaBody(path: String, rangeHeader: String?): MediaBody? {
        val m = Regex("^/media/(audio|video|image)/(\\d+)$").find(path) ?: return null
        val type = m.groupValues[1]
        val id = m.groupValues[2].toLongOrNull() ?: return null
        val base = when (type) {
            "audio" -> MediaStore.Audio.Media.EXTERNAL_CONTENT_URI
            "video" -> MediaStore.Video.Media.EXTERNAL_CONTENT_URI
            else -> MediaStore.Images.Media.EXTERNAL_CONTENT_URI
        }
        val uri = ContentUris.withAppendedId(base, id)
        return try {
            val afd = activity.contentResolver.openAssetFileDescriptor(uri, "r") ?: return null
            val total = afd.length
            val mime = activity.contentResolver.getType(uri) ?: "application/octet-stream"
            val range = if (rangeHeader != null && total > 0) parseRange(rangeHeader, total) else null
            val stream = afd.createInputStream()
            if (range != null) {
                val (start, end) = range
                stream.skip(start)
                val len = end - start + 1
                MediaBody(BoundedInputStream(stream, len), mime, len, true, start, end, total)
            } else {
                MediaBody(stream, mime, total, false)
            }
        } catch (e: Exception) {
            Log.e("MediaBridge", "buildMediaBody($path) a échoué", e)
            null
        }
    }

    private fun parseRange(header: String, total: Long): Pair<Long, Long>? {
        val m = Regex("""bytes=(\d*)-(\d*)""").find(header) ?: return null
        val start = m.groupValues[1].toLongOrNull() ?: 0L
        val end = m.groupValues[2].toLongOrNull() ?: (total - 1)
        if (start >= total || start > end) return null
        return start to minOf(end, total - 1)
    }
}

/** Pont JS pour lire la médiathèque du téléphone (musique/vidéos/photos). */
internal class MediaBridge(private val library: MediaLibrary) {
    @JavascriptInterface
    fun hasPermission(type: String): Boolean = library.hasPermission(type)

    @JavascriptInterface
    fun requestPermission(type: String) = library.requestPermission(type)

    @JavascriptInterface
    fun list(type: String): String = library.list(type)
}

/**
 * Limite un InputStream à `limit` octets (pour servir une tranche "Range" d'un
 * fichier sans lire au-delà de ce qui a été annoncé en Content-Length).
 */
private class BoundedInputStream(private val src: InputStream, private var remaining: Long) : InputStream() {
    override fun read(): Int {
        if (remaining <= 0) return -1
        val b = src.read()
        if (b >= 0) remaining--
        return b
    }
    override fun read(b: ByteArray, off: Int, len: Int): Int {
        if (remaining <= 0) return -1
        val n = src.read(b, off, minOf(len.toLong(), remaining).toInt())
        if (n > 0) remaining -= n
        return n
    }
    override fun close() = src.close()
}

/** Réponse à servir par [LoopbackMediaServer] pour un fichier de la médiathèque. */
private class MediaBody(
    val stream: InputStream,
    val mime: String,
    val length: Long,
    val partial: Boolean,
    val start: Long = 0,
    val end: Long = 0,
    val total: Long = 0,
)

/**
 * Mini serveur HTTP en boucle locale (127.0.0.1, port aléatoire) qui sert la
 * médiathèque. Contrairement au domaine virtuel https de WebViewAssetLoader,
 * un vrai socket loopback est joignable par le lecteur média natif d'Android
 * (utilisé en coulisse par <audio>/<video>), qui ouvre sa propre connexion
 * réseau en dehors des hooks de la WebView.
 */
private class LoopbackMediaServer(
    private val serve: (path: String, range: String?) -> MediaBody?,
) : Thread("media-server") {
    private val server = ServerSocket(0, 50, InetAddress.getByName("127.0.0.1"))
    // Pool réutilisable plutôt qu'un Thread natif par connexion : pendant une
    // recherche (seek), le lecteur vidéo peut ouvrir de nombreuses requêtes en
    // rafale, et un Thread par connexion a fini par épuiser la mémoire de
    // l'appareil (déclenchant le tueur mémoire système).
    private val pool = Executors.newCachedThreadPool()
    val port: Int get() = server.localPort

    override fun run() {
        while (!server.isClosed) {
            val socket = try { server.accept() } catch (e: IOException) { return }
            pool.execute { handle(socket) }
        }
    }

    fun close() {
        try { server.close() } catch (e: IOException) { /* déjà fermé */ }
        pool.shutdownNow()
    }

    // Connexion persistante (keep-alive) : traite toutes les requêtes envoyées
    // sur le même socket (le lecteur vidéo réutilise la connexion pour chaque
    // segment/seek au lieu d'en ouvrir une nouvelle à chaque fois).
    private fun handle(socket: Socket) {
        socket.use { s ->
            s.soTimeout = 15000
            try {
                val reader = s.getInputStream().bufferedReader(Charsets.ISO_8859_1)
                while (!s.isClosed) {
                    val requestLine = reader.readLine() ?: break
                    if (requestLine.isBlank()) continue
                    val path = requestLine.split(" ").getOrNull(1)?.substringBefore('?') ?: break
                    var range: String? = null
                    while (true) {
                        val line = reader.readLine() ?: return
                        if (line.isEmpty()) break
                        val i = line.indexOf(':')
                        if (i > 0 && line.take(i).equals("Range", true)) range = line.substring(i + 1).trim()
                    }
                    writeResponse(s.getOutputStream(), serve(path, range))
                }
            } catch (e: Exception) {
                // Connexion interrompue par le lecteur (seek, arrêt) : normal.
            }
        }
    }

    private fun writeResponse(out: OutputStream, body: MediaBody?) {
        if (body == null) {
            out.write("HTTP/1.1 404 Not Found\r\nConnection: keep-alive\r\n\r\n".toByteArray(Charsets.ISO_8859_1))
            return
        }
        val status = if (body.partial) "206 Partial Content" else "200 OK"
        val headers = buildString {
            append("HTTP/1.1 $status\r\n")
            append("Content-Type: ${body.mime}\r\n")
            append("Content-Length: ${body.length}\r\n")
            append("Accept-Ranges: bytes\r\n")
            if (body.partial) append("Content-Range: bytes ${body.start}-${body.end}/${body.total}\r\n")
            append("Connection: keep-alive\r\n\r\n")
        }
        out.write(headers.toByteArray(Charsets.ISO_8859_1))
        body.stream.use { it.copyTo(out) }
        out.flush()
    }
}
