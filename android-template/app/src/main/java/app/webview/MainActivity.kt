package app.webview

import android.annotation.SuppressLint
import android.app.Activity
import android.content.res.AssetManager
import android.graphics.Color
import android.graphics.Typeface
import android.net.Uri
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.text.method.ScrollingMovementMethod
import android.view.Gravity
import android.view.KeyEvent
import android.view.ViewGroup
import android.webkit.ConsoleMessage
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.TextView
import androidx.webkit.WebViewAssetLoader
import java.io.IOException

class MainActivity : Activity() {

    private lateinit var webView: WebView
    private var splash: ViewGroup? = null
    private var splashHidden = false

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        // Sert le dist embarqué (assets/www) sur https://appassets.androidplatform.net/
        val assetLoader = WebViewAssetLoader.Builder()
            .addPathHandler("/", WwwPathHandler(assets))
            .build()

        // Console de debug optionnelle (affiche erreurs JS, CORS, mixed content).
        val debug = resources.getBoolean(R.bool.debug_console)
        val console: TextView? = if (debug) TextView(this).apply {
            setBackgroundColor(0xCC000000.toInt())
            setTextColor(Color.parseColor("#9be29b"))
            typeface = Typeface.MONOSPACE
            textSize = 9f
            setPadding(16, 16, 16, 16)
            movementMethod = ScrollingMovementMethod()
            setTextIsSelectable(true)
            text = "— console de debug —\n"
        } else null

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
                    return assetLoader.shouldInterceptRequest(url)
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
            webChromeClient = if (console != null) object : WebChromeClient() {
                override fun onConsoleMessage(m: ConsoleMessage): Boolean {
                    runOnUiThread {
                        console.append("[${m.messageLevel()}] ${m.message()}  (${m.sourceId()}:${m.lineNumber()})\n")
                        val scroll = console.layout?.getLineTop(console.lineCount) ?: 0
                        val pad = scroll - console.height + console.paddingTop + console.paddingBottom
                        if (pad > 0) console.scrollTo(0, pad)
                    }
                    return true
                }
            } else WebChromeClient()
        }

        val root = FrameLayout(this)
        root.addView(webView)
        if (console != null) {
            root.addView(
                console,
                FrameLayout.LayoutParams(
                    ViewGroup.LayoutParams.MATCH_PARENT,
                    (resources.displayMetrics.heightPixels * 0.4f).toInt(),
                    Gravity.BOTTOM,
                ),
            )
        }
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
