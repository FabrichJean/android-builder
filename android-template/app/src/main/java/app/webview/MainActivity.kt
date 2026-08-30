package app.webview

import android.annotation.SuppressLint
import android.app.Activity
import android.app.PictureInPictureParams
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.util.Rational
import android.view.Gravity
import android.view.KeyEvent
import android.view.ViewGroup
import android.webkit.ConsoleMessage
import android.webkit.PermissionRequest
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import android.widget.ImageView
import android.widget.Toast
import androidx.webkit.ServiceWorkerClientCompat
import androidx.webkit.ServiceWorkerControllerCompat
import androidx.webkit.WebViewAssetLoader
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature

class MainActivity : Activity() {

    internal lateinit var webView: WebView
    private var splash: ViewGroup? = null
    private var splashHidden = false
    private val fixImages by lazy { resources.getBoolean(R.bool.fix_images) }
    private val backgroundPlayer by lazy { resources.getBoolean(R.bool.background_player) }
    private val pipEnabled by lazy { resources.getBoolean(R.bool.picture_in_picture) }
    // Détection play/pause utile aux deux options ci-dessus, pas seulement à background_player.
    private val needsPlaybackDetection by lazy { backgroundPlayer || pipEnabled }
    private var isMediaPlaying = false

    private lateinit var downloads: Downloads
    private lateinit var mediaLibrary: MediaLibrary
    private lateinit var webRtc: WebRtcPermissions
    private lateinit var deviceCapabilities: DeviceCapabilities

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        downloads = Downloads(this)
        mediaLibrary = MediaLibrary(this)
        webRtc = WebRtcPermissions(this)
        deviceCapabilities = DeviceCapabilities(this)

        // Sert le dist embarqué (assets/www) sur https://appassets.androidplatform.net/
        val assetLoader = WebViewAssetLoader.Builder()
            .addPathHandler("/", WwwPathHandler(assets))
            .build()

        // Certains frameworks (Flutter web, Next.js/Vite en mode PWA, Angular
        // service worker...) enregistrent un service worker qui prend en
        // charge lui-même les requêtes une fois actif — y compris son propre
        // précache initial des assets. Sans ceci, ces requêtes partent en
        // dehors du WebViewClient ci-dessous (donc jamais servies par
        // assetLoader) et échouent contre le faux domaine appassets.*, ce qui
        // casse silencieusement l'app après le tout premier chargement.
        if (WebViewFeature.isFeatureSupported(WebViewFeature.SERVICE_WORKER_BASIC_USAGE) &&
            WebViewFeature.isFeatureSupported(WebViewFeature.SERVICE_WORKER_SHOULD_INTERCEPT_REQUEST)
        ) {
            ServiceWorkerControllerCompat.getInstance().serviceWorkerWebSettings.allowFileAccess = false
            ServiceWorkerControllerCompat.getInstance().setServiceWorkerClient(object : ServiceWorkerClientCompat() {
                override fun shouldInterceptRequest(request: WebResourceRequest): WebResourceResponse? =
                    assetLoader.shouldInterceptRequest(request.url)
            })
        }

        // Console de debug optionnelle : widget flottant réductible et déplaçable.
        val debug = resources.getBoolean(R.bool.debug_console)
        val console = if (debug) DebugConsole(this) else null

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
            // Lecture en arrière-plan : fait croire à la page (et à ses iframes)
            // qu'elle reste au premier plan, avant même l'exécution de ses
            // propres scripts — beaucoup de sites vidéo (Facebook, YouTube...)
            // mettent la lecture en pause dès que document.hidden devient vrai.
            if (backgroundPlayer && WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT)) {
                WebViewCompat.addDocumentStartJavaScript(this, VISIBILITY_SPOOF_JS, setOf("*"))
            }
            // Mode mini-fenêtre (PiP) : en entrant réellement en PiP, la fenêtre
            // (et donc window.innerWidth/innerHeight) rétrécit pour de vrai —
            // certains sites (YouTube...) réagissent à ce redimensionnement en
            // mettant la lecture en pause. On fige la taille rapportée à la page
            // sur sa valeur d'origine. Best-effort : le rendu réel reste petit,
            // seule la taille *rapportée* en JS est figée (risque de décalages
            // visuels sur des pages qui ajustent finement leur layout dessus).
            if (pipEnabled && WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT)) {
                WebViewCompat.addDocumentStartJavaScript(this, VIEWPORT_SPOOF_JS, setOf("*"))
            }
            webViewClient = object : WebViewClient() {
                override fun shouldInterceptRequest(
                    view: WebView?,
                    request: WebResourceRequest?,
                ): WebResourceResponse? {
                    val req = request ?: return null
                    val url = req.url
                    assetLoader.shouldInterceptRequest(url)?.let { return it }
                    if (fixImages) ImageProxy.intercept(req)?.let { return it }
                    return null
                }

                override fun onPageFinished(view: WebView?, url: String?) {
                    if (console != null) {
                        // Capture les erreurs non gérées et rejets de promesses (fetch).
                        view?.evaluateJavascript(DEBUG_CAPTURE_JS, null)
                    }
                    // Replis pour les WebView trop anciens pour l'injection document-start
                    // (protection partielle : les scripts déjà exécutés au chargement ont pu
                    // agir avant celui-ci).
                    if (backgroundPlayer && !WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT)) {
                        view?.evaluateJavascript(VISIBILITY_SPOOF_JS, null)
                    }
                    if (pipEnabled && !WebViewFeature.isFeatureSupported(WebViewFeature.DOCUMENT_START_SCRIPT)) {
                        view?.evaluateJavascript(VIEWPORT_SPOOF_JS, null)
                    }
                    if (needsPlaybackDetection) {
                        // Détecte play/pause/ended sur tous les <audio>/<video> de la page :
                        // utile au service d'arrière-plan et/ou au passage en mini-fenêtre (PiP).
                        view?.evaluateJavascript(PLAYBACK_JS, null)
                    }
                    Handler(Looper.getMainLooper()).postDelayed({ hideSplash() }, 500)
                }
            }
            // Téléchargements déclenchés par la page -> dossier Téléchargements.
            setDownloadListener { url, userAgent, contentDisposition, mimetype, _ ->
                downloads.start(url, userAgent, contentDisposition, mimetype)
            }
            addJavascriptInterface(DownloadBridge(downloads, this@MainActivity), "AndroidDownload")
            addJavascriptInterface(MediaBridge(mediaLibrary), "AndroidMedia")
            addJavascriptInterface(DeviceBridge(deviceCapabilities), "AndroidDevice")
            addJavascriptInterface(PlaybackBridge(this@MainActivity), "AndroidPlayback")

            val dbg = console
            webChromeClient = object : WebChromeClient() {
                override fun onConsoleMessage(m: ConsoleMessage): Boolean {
                    if (dbg == null) return super.onConsoleMessage(m)
                    dbg.append("[${m.messageLevel()}] ${m.message()}  (${m.sourceId()}:${m.lineNumber()})")
                    return true
                }

                // getUserMedia() (caméra/micro WebRTC) : la WebView demande ici les
                // "ressources" web ; on les relaie vers les permissions Android
                // runtime correspondantes puis on accorde/refuse en conséquence.
                override fun onPermissionRequest(request: PermissionRequest) {
                    webRtc.handle(request)
                }
            }
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

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        val granted = grantResults.isNotEmpty() && grantResults[0] == PackageManager.PERMISSION_GRANTED
        if (downloads.onPermissionResult(requestCode, granted)) return
        if (webRtc.onPermissionResult(requestCode, granted)) return
        mediaLibrary.onPermissionResult(requestCode, granted)
    }

    fun toast(msg: String) = Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()

    // Appelé par PlaybackBridge (JS AndroidPlayback) quand la page détecte un
    // <audio>/<video> qui démarre ou dont plus aucun n'est en lecture.
    fun onMediaPlaybackStateChanged(playing: Boolean) {
        isMediaPlaying = playing
        if (!backgroundPlayer) return
        if (playing) BackgroundPlaybackService.start(this) else BackgroundPlaybackService.stop(this)
    }

    // Appelé quand l'utilisateur quitte l'app (bouton Accueil, app switcher...) :
    // si un média est en lecture, on passe en mini-fenêtre (PiP) au lieu de
    // laisser l'app disparaître complètement — plus fiable qu'un simple
    // service en arrière-plan puisque l'app reste visible, donc jamais limitée.
    override fun onUserLeaveHint() {
        super.onUserLeaveHint()
        if (pipEnabled && isMediaPlaying && Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val params = PictureInPictureParams.Builder()
                .setAspectRatio(Rational(16, 9))
                .build()
            try {
                enterPictureInPictureMode(params)
            } catch (e: IllegalStateException) {
                // L'appareil peut refuser (ex: mode multi-fenêtre déjà actif) : on ignore.
            }
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        mediaLibrary.close()
        if (backgroundPlayer) BackgroundPlaybackService.stop(this)
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

// Injecté dans la page pour capturer les erreurs JS/promesses non gérées et les
// remonter dans la console de debug flottante (avec sniff du type réel des
// ressources image en échec, pour diagnostiquer les mismatch de Content-Type).
private const val DEBUG_CAPTURE_JS = """(function(){if(window.__dbg)return;window.__dbg=1;
   window.addEventListener('error',function(e){
     var t=e.target||e.srcElement;
     if(t&&(t.tagName==='IMG'||t.tagName==='SCRIPT'||t.tagName==='LINK')){
       var u=t.currentSrc||t.src||t.href;
       console.error('RES fail ['+t.tagName+']: '+u);
       try{fetch(u).then(function(r){console.error('  -> HTTP '+r.status+' ct='+r.headers.get('content-type'));return r.arrayBuffer();}).then(function(buf){var a=new Uint8Array(buf).subarray(0,4);var hex=[].map.call(a,function(x){return ('0'+x.toString(16)).slice(-2);}).join('');var kind=(hex.slice(0,4)==='ffd8')?'JPEG ok':(hex.slice(0,8)==='89504e47')?'PNG ok':(hex.slice(0,6)==='474946')?'GIF ok':'PAS une image (chiffre?)';console.error('  -> '+buf.byteLength+'o magic='+hex+' '+kind);}).catch(function(err){console.error('  -> fetch err: '+err);});}catch(_){}
     } else { console.error('JS: '+e.message+' @'+e.filename+':'+e.lineno); }
   },true);
   window.addEventListener('unhandledrejection',function(e){var r=e.reason;console.error('Promise: '+((r&&(r.stack||r.message))||r));});
})();"""

// Détecte la lecture audio/vidéo dans la page pour prévenir le natif (voir
// PlaybackBridge). "play"/"pause"/"ended" ne remontent pas par bubbling sur
// les éléments média -> écoute en phase de capture sur document, ce qui
// fonctionne pour tout <audio>/<video>, y compris ajouté dynamiquement.
private const val PLAYBACK_JS = """(function(){if(window.__bgplay)return;window.__bgplay=1;
   function anyPlaying(){
     var els=document.querySelectorAll('audio,video');
     for(var i=0;i<els.length;i++){ if(!els[i].paused && !els[i].ended) return true; }
     return false;
   }
   document.addEventListener('play',function(){ try{AndroidPlayback.notifyPlaying();}catch(e){} },true);
   document.addEventListener('pause',function(){ try{if(!anyPlaying())AndroidPlayback.notifyStopped();}catch(e){} },true);
   document.addEventListener('ended',function(){ try{if(!anyPlaying())AndroidPlayback.notifyStopped();}catch(e){} },true);
})();"""

// Fait croire à la page qu'elle reste au premier plan : de nombreux sites
// vidéo (Facebook, YouTube...) mettent volontairement la lecture en pause dès
// que document.hidden devient vrai (API Page Visibility). Idéalement injecté
// en "document start" (avant les scripts de la page, y compris dans les
// iframes) via WebViewCompat.addDocumentStartJavaScript.
private const val VISIBILITY_SPOOF_JS = """(function(){if(window.__bgvis)return;window.__bgvis=1;
   try{
     Object.defineProperty(document,'hidden',{get:function(){return false;},configurable:true});
     Object.defineProperty(document,'visibilityState',{get:function(){return 'visible';},configurable:true});
     document.hasFocus=function(){return true;};
     var origAdd=EventTarget.prototype.addEventListener;
     EventTarget.prototype.addEventListener=function(type,listener,options){
       if(type==='visibilitychange'&&this===document)return;
       return origAdd.call(this,type,listener,options);
     };
   }catch(e){}
})();"""

// Fige la taille de fenêtre rapportée en JS (expérimental) : certains sites
// (YouTube...) réagissent au vrai rétrécissement de fenêtre en PiP en mettant
// la lecture en pause. On fige innerWidth/innerHeight/outerWidth/outerHeight
// sur leur valeur d'origine, on bloque l'événement "resize" sur window, et on
// fait de même pour ResizeObserver (best-effort : ne couvre pas tous les
// signaux possibles qu'une page peut utiliser pour détecter sa vraie taille).
private const val VIEWPORT_SPOOF_JS = """(function(){if(window.__bgresize)return;window.__bgresize=1;
   try{
     var fw=window.innerWidth, fh=window.innerHeight;
     var fow=window.outerWidth, foh=window.outerHeight;
     Object.defineProperty(window,'innerWidth',{get:function(){return fw;},configurable:true});
     Object.defineProperty(window,'innerHeight',{get:function(){return fh;},configurable:true});
     Object.defineProperty(window,'outerWidth',{get:function(){return fow;},configurable:true});
     Object.defineProperty(window,'outerHeight',{get:function(){return foh;},configurable:true});
   }catch(e){}
   try{
     var origAdd=EventTarget.prototype.addEventListener;
     EventTarget.prototype.addEventListener=function(type,listener,options){
       if(type==='resize'&&this===window)return;
       return origAdd.call(this,type,listener,options);
     };
   }catch(e){}
   try{
     if(window.ResizeObserver){
       var NativeRO=window.ResizeObserver;
       window.ResizeObserver=function(callback){
         var frozen=new WeakMap();
         return new NativeRO(function(entries,observer){
           var patched=entries.map(function(entry){
             if(!frozen.has(entry.target))frozen.set(entry.target,entry);
             return frozen.get(entry.target);
           });
           callback(patched,observer);
         });
       };
       window.ResizeObserver.prototype=NativeRO.prototype;
     }
   }catch(e){}
})();"""
