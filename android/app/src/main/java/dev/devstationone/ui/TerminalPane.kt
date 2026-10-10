package dev.devstationone.ui

import android.annotation.SuppressLint
import android.content.Context
import android.view.inputmethod.InputMethodManager
import android.webkit.JavascriptInterface
import android.webkit.WebView
import android.webkit.WebViewClient
import android.app.AlertDialog
import android.content.Context.CLIPBOARD_SERVICE
import android.content.Intent
import android.net.Uri
import android.widget.Toast
import androidx.compose.ui.Modifier
import dev.devstationone.data.ThemeStore
import dev.devstationone.net.TerminalConnection

/**
 * One terminal pane: a rendering-only WebView (xterm.js) over a fully
 * native input path. All keyboard input is captured by [KeyCaptureView]
 * (see Input.kt) and sent straight over the WebSocket; the WebView never
 * processes keys, which is what makes vi, Ctrl-C and hardware keyboards
 * behave.
 */
@SuppressLint("SetJavaScriptEnabled")
class TerminalPaneView(
    ctx: Context,
    private val conn: TerminalConnection,
    private val enc: InputEncoder,
) : android.widget.FrameLayout(ctx) {
    private val web = WebView(ctx)
    private val keys = KeyCaptureView(ctx, enc)
    private val main = android.os.Handler(ctx.mainLooper)
    private val pending = ByteArrayQueue()
    @Volatile private var ready = false
    @Volatile private var disposed = false
    private var flushQueued = false
    private var font = 14

    init {
        web.settings.javaScriptEnabled = true
        web.settings.allowFileAccess = false // android_asset URLs don't need it
        web.webViewClient = object : WebViewClient() {
            // the terminal page never navigates; block anything that tries
            override fun shouldOverrideUrlLoading(view: WebView, request: android.webkit.WebResourceRequest) = true
        }
        web.isFocusable = false
        web.isFocusableInTouchMode = false
        web.overScrollMode = android.view.View.OVER_SCROLL_NEVER
        web.isVerticalScrollBarEnabled = false
        web.setBackgroundColor(0xFF000000.toInt())
        web.addJavascriptInterface(Bridge(), "Android")
        web.loadUrl("file:///android_asset/terminal.html")
        addView(web, LayoutParams(LayoutParams.MATCH_PARENT, LayoutParams.MATCH_PARENT))
        addView(keys, LayoutParams(1, 1))

        conn.onData = { bytes -> main.post { enqueue(bytes) } }
        conn.onOpen = {
            main.post {
                // Fresh attach: the server replays scrollback, so start clean.
                pending.reset()
                js("tty.reset()")
            }
        }
    }

    private fun js(code: String) {
        if (!disposed) web.evaluateJavascript(code, null)
    }

    private fun enqueue(bytes: ByteArray) {
        if (disposed) return
        pending.write(bytes)
        if (ready && !flushQueued) {
            flushQueued = true
            main.postDelayed({ flush() }, 8)
        }
    }

    private fun flush() {
        flushQueued = false
        if (!ready || disposed || pending.size() == 0) return
        val all = pending.toByteArray()
        pending.reset()
        var off = 0
        while (off < all.size) {
            val n = minOf(48 * 1024, all.size - off)
            val b64 = android.util.Base64.encodeToString(all, off, n, android.util.Base64.NO_WRAP)
            js("tty.write('$b64')")
            off += n
        }
    }

    fun copyToClipboard(text: String) {
        val cm = context.getSystemService(CLIPBOARD_SERVICE) as android.content.ClipboardManager
        cm.setPrimaryClip(android.content.ClipData.newPlainText("terminal", text))
        Toast.makeText(context, "Copied", Toast.LENGTH_SHORT).show()
    }

    /** OSC 52 arrives from terminal output, so a program or file could overwrite the clipboard: ask first. */
    private fun confirmClipboard(text: String) {
        if (disposed || confirming || text.isEmpty()) return
        confirming = true
        val preview = if (text.length > 300) text.take(300) + "…" else text
        AlertDialog.Builder(context)
            .setTitle("Copy to clipboard?")
            .setMessage("The terminal wants to copy ${text.length} characters:\n\n$preview")
            .setPositiveButton("Copy") { _, _ -> copyToClipboard(text) }
            .setNegativeButton("Deny", null)
            .setOnDismissListener { confirming = false }
            .show()
    }
    private var confirming = false

    /** OSC 8 links arrive from terminal output: http(s) only, and ask before leaving the app. */
    private fun confirmLink(url: String) {
        if (disposed || confirmingLink || Uri.parse(url).scheme?.lowercase() !in listOf("http", "https")) return
        confirmingLink = true
        val preview = if (url.length > 300) url.take(300) + "…" else url
        AlertDialog.Builder(context)
            .setTitle("Open link?")
            .setMessage(preview)
            .setPositiveButton("Open") { _, _ ->
                try {
                    context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                } catch (e: android.content.ActivityNotFoundException) {
                    Toast.makeText(context, "No app can open this link", Toast.LENGTH_SHORT).show()
                }
            }
            .setNegativeButton("Cancel", null)
            .setOnDismissListener { confirmingLink = false }
            .show()
    }
    private var confirmingLink = false

    fun showKeyboard() {
        keys.requestFocus()
        (context.getSystemService(Context.INPUT_METHOD_SERVICE) as InputMethodManager)
            .showSoftInput(keys, InputMethodManager.SHOW_IMPLICIT)
    }

    fun setFontSize(px: Int) {
        if (px == font) return
        font = px
        if (ready) js("tty.setFont($px)")
    }

    fun setActive(active: Boolean) {
        visibility = if (active) android.view.View.VISIBLE else android.view.View.GONE
        if (active) {
            main.post {
                js("tty.fit()")
                keys.requestFocus()
            }
        }
    }

    fun dispose() {
        disposed = true
        conn.onData = null
        conn.onOpen = null
        web.removeJavascriptInterface("Android")
        web.destroy()
    }

    /** Called from the WebView's JS thread; hop to main where needed. */
    inner class Bridge {
        @JavascriptInterface
        fun onReady(cols: Int, rows: Int) {
            main.post {
                ready = true
                if (font != 14) js("tty.setFont($font)")
                js("tty.setTheme(${ThemeStore(context).themeJson()})")
                conn.resize(cols, rows)
                conn.start()
                flush()
            }
        }

        @JavascriptInterface
        fun onResize(cols: Int, rows: Int) {
            conn.resize(cols, rows)
        }

        @JavascriptInterface
        fun onModes(appCursor: Boolean, bracketed: Boolean, mouse: String) {
            enc.appCursor = appCursor
            enc.bracketed = bracketed
        }

        @JavascriptInterface
        fun onClipboard(text: String) {
            main.post { confirmClipboard(text) }
        }

        @JavascriptInterface
        fun onLink(url: String) {
            main.post { confirmLink(url) }
        }

        @JavascriptInterface
        fun onSelection(text: String) {
            main.post { copyToClipboard(text) }
        }

        @JavascriptInterface
        fun onTap() {
            main.post { showKeyboard() }
        }

        @JavascriptInterface
        fun onSend(s: String) {
            enc.raw(s)
        }
    }
}

/** Fixed-width FIFO for coalescing socket bytes between flushes. */
class ByteArrayQueue {
    private var buf = ByteArray(0)
    @Synchronized fun write(b: ByteArray) {
        buf = if (buf.isEmpty()) b.copyOf() else buf + b
    }
    @Synchronized fun size() = buf.size
    @Synchronized fun toByteArray() = buf.copyOf()
    @Synchronized fun reset() {
        buf = ByteArray(0)
    }
}

@androidx.compose.runtime.Composable
fun TerminalPane(
    conn: TerminalConnection,
    enc: InputEncoder,
    visible: Boolean,
    fontSize: Int,
    modifier: Modifier = Modifier,
) {
    androidx.compose.ui.viewinterop.AndroidView(
        modifier = modifier,
        factory = { ctx -> TerminalPaneView(ctx, conn, enc) },
        update = { v ->
            v.setFontSize(fontSize)
            v.setActive(visible)
        },
        onRelease = { it.dispose() },
    )
}