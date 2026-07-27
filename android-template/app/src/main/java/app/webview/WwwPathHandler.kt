package app.webview

import android.content.res.AssetManager
import android.net.Uri
import android.webkit.WebResourceResponse
import androidx.webkit.WebViewAssetLoader
import java.io.IOException

/**
 * Sert les fichiers du dossier assets/www comme si www/ était la racine du site.
 * Une requête vers https://appassets.androidplatform.net/assets/x.js renvoie
 * assets/www/assets/x.js — d'où le support des chemins absolus des SPA.
 */
internal class WwwPathHandler(
    private val assets: AssetManager,
) : WebViewAssetLoader.PathHandler {
    override fun handle(path: String): WebResourceResponse? {
        var clean = Uri.decode(path).trimStart('/')
        if (clean.isEmpty() || clean.endsWith("/")) clean += "index.html"
        return try {
            open(clean)
        } catch (e: IOException) {
            // Fallback SPA (routing par URL) : une "route" sans extension de
            // fichier retombe sur index.html, comme "try_files $uri /index.html".
            if (!clean.substringAfterLast('/').contains('.')) {
                try { open("index.html") } catch (e2: IOException) { null }
            } else {
                null
            }
        }
    }

    private fun open(rel: String): WebResourceResponse {
        val stream = assets.open("www/$rel")
        return WebResourceResponse(mimeOf(rel), null, stream)
    }

    private fun mimeOf(name: String): String = when (name.substringAfterLast('.').lowercase()) {
        "html", "htm" -> "text/html"
        "js", "mjs" -> "application/javascript"
        "css" -> "text/css"
        "json", "map" -> "application/json"
        "svg" -> "image/svg+xml"
        "png" -> "image/png"
        "jpg", "jpeg" -> "image/jpeg"
        "gif" -> "image/gif"
        "webp" -> "image/webp"
        "avif" -> "image/avif"
        "ico" -> "image/x-icon"
        "woff2" -> "font/woff2"
        "woff" -> "font/woff"
        "ttf" -> "font/ttf"
        "wasm" -> "application/wasm"
        "txt" -> "text/plain"
        else -> "application/octet-stream"
    }
}
