package app.webview

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.app.DownloadManager
import android.content.ContentUris
import android.content.ContentValues
import android.content.Intent
import android.content.pm.PackageManager
import android.content.res.AssetManager
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Build
import android.os.BatteryManager
import android.os.Bundle
import android.os.Environment
import android.os.Handler
import android.os.Looper
import android.os.VibrationEffect
import android.os.Vibrator
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
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayInputStream
import java.io.File
import java.io.IOException
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URL
import kotlin.math.abs

class MainActivity : Activity() {

    private lateinit var webView: WebView
    private var splash: ViewGroup? = null
    private var splashHidden = false
    private var pendingDownload: (() -> Unit)? = null
    private val fixImages by lazy { resources.getBoolean(R.bool.fix_images) }
    private val pendingMediaPermission = HashMap<Int, String>()

    private companion object {
        const val REQ_STORAGE = 4711
        const val REQ_MEDIA_AUDIO = 4801
        const val REQ_MEDIA_VIDEO = 4802
        const val REQ_MEDIA_IMAGE = 4803
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
                    val req = request ?: return null
                    val url = req.url
                    // Servi à part (avec support des requêtes Range) : le PathHandler de
                    // WebViewAssetLoader n'a pas accès aux en-têtes de la requête.
                    if (url.path?.startsWith("/media/") == true) return serveMedia(req)
                    assetLoader.shouldInterceptRequest(url)?.let { return it }
                    if (fixImages) proxyImage(req)?.let { return it }
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
                                   try{fetch(u).then(function(r){console.error('  -> HTTP '+r.status+' ct='+r.headers.get('content-type'));return r.arrayBuffer();}).then(function(buf){var a=new Uint8Array(buf).subarray(0,4);var hex=[].map.call(a,function(x){return ('0'+x.toString(16)).slice(-2);}).join('');var kind=(hex.slice(0,4)==='ffd8')?'JPEG ok':(hex.slice(0,8)==='89504e47')?'PNG ok':(hex.slice(0,6)==='474946')?'GIF ok':'PAS une image (chiffre?)';console.error('  -> '+buf.byteLength+'o magic='+hex+' '+kind);}).catch(function(err){console.error('  -> fetch err: '+err);});}catch(_){}
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
            addJavascriptInterface(MediaBridge(this@MainActivity), "AndroidMedia")
            addJavascriptInterface(DeviceBridge(this@MainActivity), "AndroidDevice")

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
        val granted = grantResults.isNotEmpty() && grantResults[0] == PackageManager.PERMISSION_GRANTED
        if (requestCode == REQ_STORAGE) {
            val action = pendingDownload
            pendingDownload = null
            if (granted) action?.invoke() else toast("Permission de stockage refusée")
            return
        }
        val mediaType = pendingMediaPermission.remove(requestCode) ?: return
        webView.evaluateJavascript(
            "window.onMediaPermission && window.onMediaPermission(${JSONObject.quote(mediaType)}, $granted);",
            null,
        )
    }

    fun toast(msg: String) = Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()

    // ---- Médiathèque (musique / vidéos / photos) ----

    private fun mediaPermissionName(type: String): String = when {
        Build.VERSION.SDK_INT >= 33 && type == "audio" -> Manifest.permission.READ_MEDIA_AUDIO
        Build.VERSION.SDK_INT >= 33 && type == "video" -> Manifest.permission.READ_MEDIA_VIDEO
        Build.VERSION.SDK_INT >= 33 && type == "image" -> Manifest.permission.READ_MEDIA_IMAGES
        else -> Manifest.permission.READ_EXTERNAL_STORAGE
    }

    fun hasMediaPermission(type: String): Boolean =
        checkSelfPermission(mediaPermissionName(type)) == PackageManager.PERMISSION_GRANTED

    fun requestMediaPermission(type: String) = runOnUiThread {
        if (hasMediaPermission(type)) {
            webView.evaluateJavascript("window.onMediaPermission && window.onMediaPermission(${JSONObject.quote(type)}, true);", null)
            return@runOnUiThread
        }
        val code = when (type) { "audio" -> REQ_MEDIA_AUDIO; "video" -> REQ_MEDIA_VIDEO; else -> REQ_MEDIA_IMAGE }
        pendingMediaPermission[code] = type
        requestPermissions(arrayOf(mediaPermissionName(type)), code)
    }

    // Interroge le MediaStore et renvoie un JSON avec l'URL de streaming (servie
    // par serveMedia() via le domaine virtuel https de la WebView).
    fun listMedia(type: String): String {
        if (!hasMediaPermission(type)) return "[]"
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
            contentResolver.query(uri, projection, null, null, sort)?.use { c ->
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
                    o.put("url", "https://appassets.androidplatform.net/media/$type/$id")
                    if (artistIdx >= 0) o.put("artist", c.getString(artistIdx) ?: "")
                    if (durationIdx >= 0) o.put("duration", c.getLong(durationIdx))
                    out.put(o)
                }
            }
        } catch (e: Exception) {
            android.util.Log.e("MediaBridge", "listMedia($type) a échoué", e)
            return "[]"
        }
        return out.toString()
    }

    // Diffuse un fichier de la médiathèque avec support des requêtes "Range" —
    // requis par <audio>/<video> pour démarrer la lecture (beaucoup de WebView,
    // dont ceux de Samsung, refusent de lire un média si la réponse à la requête
    // Range initiale n'est pas un vrai 206 Partial Content avec Content-Length).
    // On ne peut pas passer par un WebViewAssetLoader.PathHandler ici : son
    // interface ne donne accès qu'au chemin, jamais aux en-têtes de la requête.
    private fun serveMedia(request: WebResourceRequest): WebResourceResponse? {
        val path = request.url.path ?: return null
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
            val afd = contentResolver.openAssetFileDescriptor(uri, "r") ?: return null
            val total = afd.length
            val mime = contentResolver.getType(uri) ?: "application/octet-stream"
            val rangeHeader = request.requestHeaders.entries.firstOrNull { it.key.equals("Range", true) }?.value
            val range = if (rangeHeader != null && total > 0) parseRange(rangeHeader, total) else null
            val stream = afd.createInputStream()
            if (range != null) {
                val (start, end) = range
                stream.skip(start)
                val len = end - start + 1
                WebResourceResponse(mime, null, BoundedInputStream(stream, len)).apply {
                    setStatusCodeAndReasonPhrase(206, "Partial Content")
                    responseHeaders = mapOf(
                        "Accept-Ranges" to "bytes",
                        "Content-Range" to "bytes $start-$end/$total",
                        "Content-Length" to len.toString(),
                    )
                }
            } else {
                WebResourceResponse(mime, null, stream).apply {
                    setStatusCodeAndReasonPhrase(200, "OK")
                    responseHeaders = if (total > 0) mapOf(
                        "Accept-Ranges" to "bytes",
                        "Content-Length" to total.toString(),
                    ) else mapOf("Accept-Ranges" to "bytes")
                }
            }
        } catch (e: Exception) {
            android.util.Log.e("MediaBridge", "serveMedia($path) a échoué", e)
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

    // ---- Autres capacités natives (démo) ----

    fun vibrateDevice(ms: Long) {
        val v = getSystemService(Vibrator::class.java) ?: return
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            v.vibrate(VibrationEffect.createOneShot(ms, VibrationEffect.DEFAULT_AMPLITUDE))
        } else {
            @Suppress("DEPRECATION")
            v.vibrate(ms)
        }
    }

    fun shareText(text: String) = runOnUiThread {
        val intent = Intent(Intent.ACTION_SEND).apply {
            type = "text/plain"
            putExtra(Intent.EXTRA_TEXT, text)
        }
        startActivity(Intent.createChooser(intent, null))
    }

    fun batteryLevel(): Int {
        val bm = getSystemService(BATTERY_SERVICE) as BatteryManager
        return bm.getIntProperty(BatteryManager.BATTERY_PROPERTY_CAPACITY)
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
 * Pont JS pour lire la médiathèque du téléphone (musique/vidéos/photos).
 * `list(type)` renvoie un JSON avec, pour chaque élément, une `url` https
 * jouable/affichable directement (servie par [MediaPathHandler]).
 */
private class MediaBridge(private val activity: MainActivity) {
    @JavascriptInterface
    fun hasPermission(type: String): Boolean = activity.hasMediaPermission(type)

    @JavascriptInterface
    fun requestPermission(type: String) = activity.requestMediaPermission(type)

    @JavascriptInterface
    fun list(type: String): String = activity.listMedia(type)
}

/** Pont JS pour quelques capacités natives supplémentaires (démo). */
private class DeviceBridge(private val activity: MainActivity) {
    @JavascriptInterface
    fun vibrate(ms: Long) = activity.vibrateDevice(ms)

    @JavascriptInterface
    fun share(text: String) = activity.shareText(text)

    @JavascriptInterface
    fun batteryLevel(): Int = activity.batteryLevel()
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
