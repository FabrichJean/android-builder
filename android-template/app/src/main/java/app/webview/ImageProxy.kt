package app.webview

import android.webkit.CookieManager
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import java.io.ByteArrayInputStream
import java.net.HttpURLConnection
import java.net.URL

/**
 * Corrige les images dont le serveur renvoie un Content-Type erroné (ex.
 * application/octet-stream pour un fichier .enc) et contourne les blocages
 * CORS des <img crossorigin>, en récupérant l'image en natif.
 */
internal object ImageProxy {
    private const val MAX_IMAGE_BYTES = 25 * 1024 * 1024

    fun intercept(request: WebResourceRequest): WebResourceResponse? {
        if (!request.method.equals("GET", true)) return null
        val headers = request.requestHeaders
        val accept = headers.entries.firstOrNull { it.key.equals("Accept", true) }?.value ?: ""
        if (!accept.contains("image/")) return null // seulement les requêtes d'image
        if (headers.keys.any { it.equals("Range", true) }) return null // pas de streaming
        val urlStr = request.url.toString()
        if (!urlStr.startsWith("http")) return null

        var conn: HttpURLConnection? = null
        return try {
            val c = URL(urlStr).openConnection() as HttpURLConnection
            conn = c
            c.requestMethod = "GET"
            c.connectTimeout = 15000
            c.readTimeout = 20000
            c.instanceFollowRedirects = true
            CookieManager.getInstance().getCookie(urlStr)?.let { c.setRequestProperty("Cookie", it) }
            // On ne transmet pas Accept-Encoding/Range/Host : laisser HttpURLConnection
            // gérer la compression (sinon octets gzip non décompressés -> image cassée).
            val skip = setOf("accept-encoding", "range", "host", "connection", "content-length")
            headers.forEach { (k, v) -> if (k.lowercase() !in skip) c.setRequestProperty(k, v) }

            if (c.responseCode !in 200..299) return null
            val serverCt = c.contentType
            val bytes = c.inputStream.use { it.readBytes() }
            if (bytes.size > MAX_IMAGE_BYTES) return null
            val mime = imageMime(urlStr, serverCt, bytes) ?: return null
            WebResourceResponse(mime, null, ByteArrayInputStream(bytes)).apply {
                setStatusCodeAndReasonPhrase(200, "OK")
                responseHeaders = mapOf(
                    "Access-Control-Allow-Origin" to "*",
                    "Cache-Control" to "public, max-age=3600",
                )
            }
        } catch (e: Exception) {
            null
        } finally {
            conn?.disconnect()
        }
    }

    private fun imageMime(url: String, serverCt: String?, bytes: ByteArray): String? {
        sniffImage(bytes)?.let { return it } // le contenu réel prime
        val lower = url.lowercase()
        when {
            ".png" in lower -> return "image/png"
            ".jpg" in lower || ".jpeg" in lower -> return "image/jpeg"
            ".webp" in lower -> return "image/webp"
            ".gif" in lower -> return "image/gif"
            ".svg" in lower -> return "image/svg+xml"
            ".avif" in lower -> return "image/avif"
            ".bmp" in lower -> return "image/bmp"
        }
        serverCt?.substringBefore(';')?.trim()?.let { if (it.startsWith("image/")) return it }
        return null
    }

    private fun sniffImage(b: ByteArray): String? {
        if (b.size < 12) return null
        fun c(i: Int, ch: Char) = b[i] == ch.code.toByte()
        return when {
            b[0] == 0x89.toByte() && c(1, 'P') && c(2, 'N') && c(3, 'G') -> "image/png"
            b[0] == 0xFF.toByte() && b[1] == 0xD8.toByte() -> "image/jpeg"
            c(0, 'G') && c(1, 'I') && c(2, 'F') -> "image/gif"
            c(0, 'R') && c(1, 'I') && c(2, 'F') && c(8, 'W') && c(9, 'E') && c(10, 'B') && c(11, 'P') -> "image/webp"
            c(0, 'B') && c(1, 'M') -> "image/bmp"
            c(0, '<') && (c(1, 's') || c(1, '?')) -> "image/svg+xml"
            else -> null
        }
    }
}
