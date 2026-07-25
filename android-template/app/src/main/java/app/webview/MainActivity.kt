package app.webview

import android.annotation.SuppressLint
import android.app.Activity
import android.content.res.AssetManager
import android.net.Uri
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.Gravity
import android.view.KeyEvent
import android.view.ViewGroup
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import android.widget.ImageView
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
            settings.mixedContentMode = WebSettings.MIXED_CONTENT_COMPATIBILITY_MODE
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
                    Handler(Looper.getMainLooper()).postDelayed({ hideSplash() }, 500)
                }
            }
            webChromeClient = WebChromeClient()
        }

        val root = FrameLayout(this)
        root.addView(webView)
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
            val stream = assets.open("www/$clean")
            WebResourceResponse(mimeOf(clean), null, stream)
        } catch (e: IOException) {
            null
        }
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
