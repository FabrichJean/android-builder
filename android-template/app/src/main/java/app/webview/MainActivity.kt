package app.webview

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.app.DownloadManager
import android.content.ContentValues
import android.content.pm.PackageManager
import android.content.res.AssetManager
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.os.Handler
import android.os.Looper
import android.provider.MediaStore
import android.text.method.ScrollingMovementMethod
import android.util.Base64
import android.view.Gravity
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.View
import android.view.ViewGroup
import android.webkit.ConsoleMessage
import android.webkit.CookieManager
import android.webkit.JavascriptInterface
import android.webkit.URLUtil
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.webkit.WebViewAssetLoader
import java.io.ByteArrayInputStream
import java.io.File
import java.io.IOException
import java.net.HttpURLConnection
import java.net.URL
import kotlin.math.abs

class MainActivity : Activity() {

    private lateinit var webView: WebView
    private var splash: ViewGroup? = null
    private var splashHidden = false
    private var pendingDownload: (() -> Unit)? = null
    private val fixImages by lazy { resources.getBoolean(R.bool.fix_images) }

    private companion object {
        const val REQ_STORAGE = 4711
        const val MAX_IMAGE_BYTES = 25 * 1024 * 1024
    }

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        // Sert le dist embarqué (assets/www) sur https://appassets.androidplatform.net/
        val assetLoader = WebViewAssetLoader.Builder()
            .addPathHandler("/", WwwPathHandler(assets))
            .build()

        // Console de debug optionnelle : widget flottant réductible et déplaçable.
        val debug = resources.getBoolean(R.bool.debug_console)
        val console = if (debug) DebugConsole() else null

        webView = WebView(this).apply {
            layoutParams = ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            )
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            settings.loadWithOverviewMode = true
            settings.useWideViewPort = true
            settings.allowFileAccess = false
            // Autorise les appels http depuis la page https interne (API en clair).
            settings.mixedContentMode = WebSettings.MIXED_CONTENT_ALWAYS_ALLOW
            // Barre de défilement (option de build).
            if (resources.getBoolean(R.bool.hide_scrollbar)) {
                isVerticalScrollBarEnabled = false
                isHorizontalScrollBarEnabled = false
                overScrollMode = WebView.OVER_SCROLL_NEVER
            }
            webViewClient = object : WebViewClient() {
                override fun shouldInterceptRequest(
                    view: WebView?,
                    request: WebResourceRequest?,
                ): WebResourceResponse? {
                    val url = request?.url ?: return null
                    assetLoader.shouldInterceptRequest(url)?.let { return it }
                    if (fixImages) proxyImage(request)?.let { return it }
                    return null
                }

                override fun onPageFinished(view: WebView?, url: String?) {
                    if (console != null) {
                        // Capture les erreurs non gérées et rejets de promesses (fetch).
                        view?.evaluateJavascript(
                            """(function(){if(window.__dbg)return;window.__dbg=1;
                               window.addEventListener('error',function(e){
                                 var t=e.target||e.srcElement;
                                 if(t&&(t.tagName==='IMG'||t.tagName==='SCRIPT'||t.tagName==='LINK')){
                                   var u=t.currentSrc||t.src||t.href;
                                   console.error('RES fail ['+t.tagName+']: '+u);
                                   try{fetch(u).then(function(r){console.error('  -> HTTP '+r.status+' ct='+r.headers.get('content-type'));}).catch(function(err){console.error('  -> fetch err: '+err);});}catch(_){}
                                 } else { console.error('JS: '+e.message+' @'+e.filename+':'+e.lineno); }
                               },true);
                               window.addEventListener('unhandledrejection',function(e){var r=e.reason;console.error('Promise: '+((r&&(r.stack||r.message))||r));});
                            })();""",
                            null,
                        )
                    }
                    Handler(Looper.getMainLooper()).postDelayed({ hideSplash() }, 500)
                }
            }
            // Téléchargements déclenchés par la page -> dossier Téléchargements.
            setDownloadListener { url, userAgent, contentDisposition, mimetype, _ ->
                startDownload(url, userAgent, contentDisposition, mimetype)
            }
            addJavascriptInterface(DownloadBridge(this@MainActivity), "AndroidDownload")

            val dbg = console
            webChromeClient = if (dbg != null) object : WebChromeClient() {
                override fun onConsoleMessage(m: ConsoleMessage): Boolean {
                    dbg.append("[${m.messageLevel()}] ${m.message()}  (${m.sourceId()}:${m.lineNumber()})")
                    return true
                }
            } else WebChromeClient()
        }

        val root = FrameLayout(this)
        root.addView(webView)
        console?.addTo(root)
        root.addView(buildSplash())
        setContentView(root)

        // Filet de sécurité : efface le splash même si la page ne se charge jamais.
        Handler(Looper.getMainLooper()).postDelayed({ hideSplash() }, 3000)

        if (savedInstanceState == null) {
            webView.loadUrl(getString(R.string.app_url))
        }
    }

    private fun buildSplash(): ViewGroup {
        val overlay = FrameLayout(this)
        overlay.setBackgroundColor(getColor(R.color.splash_bg))
        overlay.layoutParams = ViewGroup.LayoutParams(
            ViewGroup.LayoutParams.MATCH_PARENT,
            ViewGroup.LayoutParams.MATCH_PARENT,
        )
        val dm = resources.displayMetrics
        val size = (minOf(dm.widthPixels, dm.heightPixels) * 0.34f).toInt()
        val icon = ImageView(this)
        icon.setImageResource(R.mipmap.ic_launcher)
        icon.layoutParams = FrameLayout.LayoutParams(size, size, Gravity.CENTER)
        overlay.addView(icon)
        splash = overlay
        return overlay
    }

    private fun hideSplash() {
        val overlay = splash
        if (splashHidden || overlay == null) return
        splashHidden = true
        overlay.animate().alpha(0f).setDuration(300).withEndAction {
            (overlay.parent as? ViewGroup)?.removeView(overlay)
            splash = null
        }.start()
    }

    /** Console de debug flottante : panneau réductible en une bulle déplaçable. */
    private inner class DebugConsole {
        private val log = TextView(this@MainActivity).apply {
            setTextColor(Color.parseColor("#9be29b"))
            typeface = Typeface.MONOSPACE
            textSize = 10f
            setPadding(dp(12), dp(8), dp(12), dp(12))
            movementMethod = ScrollingMovementMethod()
            setTextIsSelectable(true)
            text = "— console de debug —\n"
        }
        private val panel = LinearLayout(this@MainActivity).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(0xE6000000.toInt())
            visibility = View.GONE
            addView(buildHeader())
            addView(log, LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f))
        }
        private val bubble = TextView(this@MainActivity).apply {
            text = "🐞"
            textSize = 18f
            gravity = Gravity.CENTER
            setPadding(dp(12), dp(10), dp(12), dp(10))
            background = pill(0xCC1B1B1B.toInt())
        }

        private fun buildHeader(): View {
            val header = LinearLayout(this@MainActivity).apply {
                orientation = LinearLayout.HORIZONTAL
                gravity = Gravity.CENTER_VERTICAL
                setBackgroundColor(0xFF141414.toInt())
                setPadding(dp(12), dp(4), dp(6), dp(4))
            }
            val title = TextView(this@MainActivity).apply {
                text = "🐞 console"
                setTextColor(Color.parseColor("#e0c07d"))
                textSize = 12f
            }
            header.addView(title, LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f))
            header.addView(headerBtn("Vider") { log.text = "" })
            header.addView(headerBtn("▁ réduire") { collapse() })
            return header
        }

        private fun headerBtn(label: String, onClick: () -> Unit) = TextView(this@MainActivity).apply {
            text = label
            setTextColor(Color.parseColor("#ece3d0"))
            textSize = 12f
            setPadding(dp(10), dp(6), dp(10), dp(6))
            setOnClickListener { onClick() }
        }

        fun addTo(root: FrameLayout) {
            root.addView(
                panel,
                FrameLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT,
                    (resources.displayMetrics.heightPixels * 0.34f).toInt(),
                    Gravity.BOTTOM,
                ),
            )
            root.addView(
                bubble,
                FrameLayout.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT),
            )
            bubble.post {
                val p = bubble.parent as View
                bubble.x = (p.width - bubble.width - dp(12)).toFloat()
                bubble.y = (p.height - bubble.height - dp(96)).toFloat()
            }
            wireDrag()
        }

        private fun wireDrag() {
            var downX = 0f
            var downY = 0f
            var startX = 0f
            var startY = 0f
            var moved = false
            val slop = dp(8).toFloat()
            bubble.setOnTouchListener { v, e ->
                when (e.actionMasked) {
                    MotionEvent.ACTION_DOWN -> {
                        downX = e.rawX; downY = e.rawY; startX = v.x; startY = v.y; moved = false; true
                    }
                    MotionEvent.ACTION_MOVE -> {
                        val dx = e.rawX - downX
                        val dy = e.rawY - downY
                        if (abs(dx) > slop || abs(dy) > slop) moved = true
                        val p = v.parent as View
                        v.x = (startX + dx).coerceIn(0f, (p.width - v.width).toFloat())
                        v.y = (startY + dy).coerceIn(0f, (p.height - v.height).toFloat())
                        true
                    }
                    MotionEvent.ACTION_UP -> { if (!moved) { v.performClick(); expand() }; true }
                    else -> false
                }
            }
        }

        private fun expand() {
            panel.visibility = View.VISIBLE
            bubble.visibility = View.GONE
        }

        private fun collapse() {
            panel.visibility = View.GONE
            bubble.visibility = View.VISIBLE
            bubble.background = pill(0xCC1B1B1B.toInt())
        }

        fun append(line: String) = runOnUiThread {
            log.append(line + "\n")
            val s = log.text
            if (s.length > 12000) log.text = s.subSequence(s.length - 8000, s.length)
            log.post {
                val l = log.layout ?: return@post
                val amount = l.getLineTop(log.lineCount) - log.height + log.paddingTop + log.paddingBottom
                log.scrollTo(0, if (amount > 0) amount else 0)
            }
            // Signale visuellement un nouveau message quand la console est réduite.
            if (panel.visibility != View.VISIBLE) bubble.background = pill(0xCCC85A4A.toInt())
        }

        private fun pill(color: Int) = GradientDrawable().apply { setColor(color); cornerRadius = dp(16).toFloat() }
        private fun dp(v: Int) = (v * resources.displayMetrics.density).toInt()
    }

    // ---- Correction des images (proxy natif) ----
    // Récupère l'image en natif et la renvoie avec le bon Content-Type. Corrige
    // les serveurs qui renvoient application/octet-stream (ex. fichiers .enc) et
    // contourne les blocages CORS des <img crossorigin>.
    private fun proxyImage(request: WebResourceRequest): WebResourceResponse? {
        if (!request.method.equals("GET", true)) return null
        val headers = request.requestHeaders
        val accept = headers.entries.firstOrNull { it.key.equals("Accept", true) }?.value ?: ""
        if (!accept.contains("image/")) return null // seulement les requêtes d'image
        if (headers.keys.any { it.equals("Range", true) }) return null // pas de streaming
        val urlStr = request.url.toString()
        if (!urlStr.startsWith("http")) return null

        var conn: HttpURLConnection? = null
        return try {
            conn = (URL(urlStr).openConnection() as HttpURLConnection).apply {
                requestMethod = "GET"
                connectTimeout = 15000
                readTimeout = 20000
                instanceFollowRedirects = true
                CookieManager.getInstance().getCookie(urlStr)?.let { setRequestProperty("Cookie", it) }
                // On ne transmet pas Accept-Encoding/Range/Host : laisser HttpURLConnection
                // gérer la compression (sinon octets gzip non décompressés -> image cassée).
                val skip = setOf("accept-encoding", "range", "host", "connection", "content-length")
                headers.forEach { (k, v) -> if (k.lowercase() !in skip) setRequestProperty(k, v) }
            }
            if (conn.responseCode !in 200..299) return null
            val serverCt = conn.contentType
            val bytes = conn.inputStream.use { it.readBytes() }
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

    // ---- Téléchargements ----

    private fun startDownload(url: String, userAgent: String?, contentDisposition: String?, mimetype: String?) {
        when {
            url.startsWith("blob:") -> downloadBlob(url)
            url.startsWith("data:") -> saveDataUrl(url)
            else -> withStoragePermission { enqueueHttp(url, userAgent, contentDisposition, mimetype) }
        }
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
            (getSystemService(DOWNLOAD_SERVICE) as DownloadManager).enqueue(req)
            toast("Téléchargement de $fileName…")
        } catch (e: Exception) {
            toast("Téléchargement impossible : ${e.message}")
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
        webView.evaluateJavascript(js, null)
    }

    private fun saveDataUrl(dataUrl: String) {
        val comma = dataUrl.indexOf(',')
        if (comma < 0) { toast("data URL invalide"); return }
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
            toast("Téléchargement échoué : ${e.message}")
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
                    val uri = contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
                        ?: throw IOException("insertion MediaStore impossible")
                    contentResolver.openOutputStream(uri)?.use { it.write(bytes) }
                    values.clear()
                    values.put(MediaStore.Downloads.IS_PENDING, 0)
                    contentResolver.update(uri, values, null, null)
                } else {
                    @Suppress("DEPRECATION")
                    val dir = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS)
                    dir.mkdirs()
                    File(dir, name).outputStream().use { it.write(bytes) }
                }
                toast("Enregistré dans Téléchargements : $name")
            } catch (e: Exception) {
                toast("Téléchargement échoué : ${e.message}")
            }
        }
    }

    // WRITE_EXTERNAL_STORAGE n'est requis que sur Android ≤ 9 (API < 29).
    private fun withStoragePermission(action: () -> Unit) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q ||
            checkSelfPermission(Manifest.permission.WRITE_EXTERNAL_STORAGE) == PackageManager.PERMISSION_GRANTED
        ) {
            action()
        } else {
            pendingDownload = action
            requestPermissions(arrayOf(Manifest.permission.WRITE_EXTERNAL_STORAGE), REQ_STORAGE)
        }
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode == REQ_STORAGE) {
            val granted = grantResults.isNotEmpty() && grantResults[0] == PackageManager.PERMISSION_GRANTED
            val action = pendingDownload
            pendingDownload = null
            if (granted) action?.invoke() else toast("Permission de stockage refusée")
        }
    }

    fun toast(msg: String) = Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()

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

    override fun onKeyDown(keyCode: Int, event: KeyEvent?): Boolean {
        if (keyCode == KeyEvent.KEYCODE_BACK && webView.canGoBack()) {
            webView.goBack()
            return true
        }
        return super.onKeyDown(keyCode, event)
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        webView.saveState(outState)
    }

    override fun onRestoreInstanceState(savedInstanceState: Bundle) {
        super.onRestoreInstanceState(savedInstanceState)
        webView.restoreState(savedInstanceState)
    }
}

/** Pont JS pour récupérer les téléchargements blob: (base64 -> fichier). */
private class DownloadBridge(private val activity: MainActivity) {
    @JavascriptInterface
    fun saveBase64(base64: String, type: String) {
        activity.runOnUiThread { activity.saveBase64ToDownloads(base64, type) }
    }

    @JavascriptInterface
    fun onError(msg: String) {
        activity.runOnUiThread { activity.toast("Téléchargement échoué : $msg") }
    }
}

/**
 * Sert les fichiers du dossier assets/www comme si www/ était la racine du site.
 * Une requête vers https://appassets.androidplatform.net/assets/x.js renvoie
 * assets/www/assets/x.js — d'où le support des chemins absolus des SPA.
 */
private class WwwPathHandler(
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
