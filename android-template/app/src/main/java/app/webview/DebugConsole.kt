package app.webview

import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.text.method.ScrollingMovementMethod
import android.view.Gravity
import android.view.MotionEvent
import android.view.View
import android.view.ViewGroup
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.TextView
import kotlin.math.abs

/** Console de debug flottante : panneau réductible en une bulle déplaçable. */
internal class DebugConsole(private val activity: MainActivity) {
    private val log = TextView(activity).apply {
        setTextColor(Color.parseColor("#9be29b"))
        typeface = Typeface.MONOSPACE
        textSize = 10f
        setPadding(dp(12), dp(8), dp(12), dp(12))
        movementMethod = ScrollingMovementMethod()
        setTextIsSelectable(true)
        text = "— console de debug —\n"
    }
    private val panel = LinearLayout(activity).apply {
        orientation = LinearLayout.VERTICAL
        setBackgroundColor(0xE6000000.toInt())
        visibility = View.GONE
        addView(buildHeader())
        addView(log, LinearLayout.LayoutParams(LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f))
    }
    private val bubble = TextView(activity).apply {
        text = "🐞"
        textSize = 18f
        gravity = Gravity.CENTER
        setPadding(dp(12), dp(10), dp(12), dp(10))
        background = pill(0xCC1B1B1B.toInt())
    }

    private fun buildHeader(): View {
        val header = LinearLayout(activity).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setBackgroundColor(0xFF141414.toInt())
            setPadding(dp(12), dp(4), dp(6), dp(4))
        }
        val title = TextView(activity).apply {
            text = "🐞 console"
            setTextColor(Color.parseColor("#e0c07d"))
            textSize = 12f
        }
        header.addView(title, LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f))
        header.addView(headerBtn("Vider") { log.text = "" })
        header.addView(headerBtn("▁ réduire") { collapse() })
        return header
    }

    private fun headerBtn(label: String, onClick: () -> Unit) = TextView(activity).apply {
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
                (activity.resources.displayMetrics.heightPixels * 0.34f).toInt(),
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

    fun append(line: String) = activity.runOnUiThread {
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
    private fun dp(v: Int) = (v * activity.resources.displayMetrics.density).toInt()
}
