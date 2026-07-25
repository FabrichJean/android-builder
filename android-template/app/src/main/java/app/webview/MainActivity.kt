package app.webview

import android.annotation.SuppressLint
import android.app.Activity
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.Gravity
import android.view.KeyEvent
import android.view.ViewGroup
import android.webkit.WebChromeClient
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import android.widget.ImageView

class MainActivity : Activity() {

    private lateinit var webView: WebView
    private var splash: ViewGroup? = null
    private var splashHidden = false

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        webView = WebView(this).apply {
            layoutParams = ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT,
            )
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            settings.loadWithOverviewMode = true
            settings.useWideViewPort = true
            // Nécessaire pour charger un dist embarqué via file:///android_asset (mode hors-ligne).
            settings.allowFileAccess = true
            @Suppress("DEPRECATION")
            settings.allowUniversalAccessFromFileURLs = true
            // Barre de défilement (option de build).
            if (resources.getBoolean(R.bool.hide_scrollbar)) {
                isVerticalScrollBarEnabled = false
                isHorizontalScrollBarEnabled = false
                overScrollMode = WebView.OVER_SCROLL_NEVER
            }
            webViewClient = object : WebViewClient() {
                override fun onPageFinished(view: WebView?, url: String?) {
                    // Laisse le splash visible un court instant, puis l'efface.
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

        // Bouton retour : naviguer dans l'historique WebView plutôt que quitter.
        // (géré via onKeyDown ci-dessous)

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
