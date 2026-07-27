package app.webview

import android.Manifest
import android.content.pm.PackageManager
import android.net.Uri
import android.webkit.PermissionRequest

/**
 * Relaie getUserMedia() (caméra/micro, WebRTC) vers les permissions Android
 * runtime correspondantes. C'est le WebChromeClient.onPermissionRequest qui
 * appelle [handle] ; le résultat de la popup système revient dans
 * [onPermissionResult] via onRequestPermissionsResult de l'Activity.
 */
internal class WebRtcPermissions(private val activity: MainActivity) {
    private var pendingRequest: PermissionRequest? = null

    private companion object {
        const val REQ_CODE = 4901
    }

    fun handle(request: PermissionRequest) {
        // Sécurité : ne répond qu'aux demandes venant de l'app elle-même
        // (dist embarqué sur le domaine virtuel, ou l'URL configurée au build).
        val origin = request.origin?.host ?: ""
        val appHost = Uri.parse(activity.getString(R.string.app_url)).host ?: ""
        if (origin != "appassets.androidplatform.net" && origin != appHost) {
            request.deny()
            return
        }
        val wanted = request.resources.mapNotNull { webPermissionFor(it) }.distinct()
        if (wanted.isEmpty()) { request.deny(); return }

        val missing = wanted.filter { activity.checkSelfPermission(it) != PackageManager.PERMISSION_GRANTED }
        if (missing.isEmpty()) {
            request.grant(request.resources.filter { webPermissionFor(it) != null }.toTypedArray())
            return
        }
        // Une seule demande WebRTC à la fois : la précédente encore en vol est refusée.
        pendingRequest?.deny()
        pendingRequest = request
        activity.requestPermissions(missing.toTypedArray(), REQ_CODE)
    }

    // Répond à onRequestPermissionsResult ; renvoie false si ce n'est pas notre requestCode.
    // Le paramètre "granted" global n'est pas utilisé : plusieurs permissions
    // peuvent avoir été demandées à la fois, on revérifie donc chacune séparément.
    fun onPermissionResult(requestCode: Int, @Suppress("UNUSED_PARAMETER") granted: Boolean): Boolean {
        if (requestCode != REQ_CODE) return false
        val req = pendingRequest
        pendingRequest = null
        if (req == null) return true
        // Accorde uniquement les ressources dont la permission Android est
        // effectivement accordée (l'utilisateur peut n'en accepter qu'une).
        val allowed = req.resources.filter { res ->
            webPermissionFor(res)?.let {
                activity.checkSelfPermission(it) == PackageManager.PERMISSION_GRANTED
            } ?: false
        }
        if (allowed.isEmpty()) req.deny() else req.grant(allowed.toTypedArray())
        return true
    }

    // Ressource WebView -> permission Android runtime correspondante.
    private fun webPermissionFor(resource: String): String? = when (resource) {
        PermissionRequest.RESOURCE_VIDEO_CAPTURE -> Manifest.permission.CAMERA
        PermissionRequest.RESOURCE_AUDIO_CAPTURE -> Manifest.permission.RECORD_AUDIO
        else -> null
    }
}
