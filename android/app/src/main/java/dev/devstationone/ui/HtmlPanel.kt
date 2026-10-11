package dev.devstationone.ui

import android.content.Context
import android.os.Handler
import android.os.Looper
import android.util.Base64
import android.view.View
import android.view.inputmethod.InputMethodManager
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.runtime.Composable
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import dev.devstationone.data.Server
import dev.devstationone.net.Api
import dev.devstationone.net.Auth
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.Request
import java.io.ByteArrayInputStream

private const val NOTE_PAGE = "file:///android_asset/note.html"

/** Quotes a Kotlin string for the one-line JS commands the native toolbar sends. */
internal fun jsString(s: String): String =
    s.replace("\\", "\\\\").replace("'", "\\'").replace("\n", "\\n").replace("\\r", "")

/**
 * Bridges one WebView subresource load whose path is under `/api/v1/`
 * through OkHttp with the device signature; everything else returns null
 * (WebView resolves it itself). Shared by the note view ([HtmlPanel]) and
 * the file preview ([FilePreview]) — the only surface that can fetch
 * beyond the asset page.
 */
internal fun bridgeServerResponse(server: Server, path: String, query: String?): WebResourceResponse? {
    if (!path.startsWith("/api/v1/")) return null
    val base = server.url.trimEnd('/')
    val httpUrl = (base + path + (query?.let { "?$it" } ?: "")).toHttpUrl()
    return try {
        val resp = Api.client.newCall(
            Request.Builder().url(httpUrl)
                .header("Authorization", Auth.header(server, "GET", httpUrl))
                .build()
        ).execute()
        val body = resp.body ?: return WebResourceResponse("text/plain", "utf-8", resp.code, resp.message, null, ByteArrayInputStream(ByteArray(0)))
        val type = resp.header("Content-Type") ?: "application/octet-stream"
        val mime = type.substringBefore(';').trim()
        val charset = type.substringAfter("charset=", "").substringBefore(';').ifEmpty { null }
        // WebView closes the stream when done reading, which closes the call.
        WebResourceResponse(mime, charset, body.byteStream())
    } catch (e: Exception) {
        WebResourceResponse("text/plain", "utf-8", 502, "unreachable", null, ByteArrayInputStream(ByteArray(0)))
    }
}

/**
 * Renders one note's `body_html` in a WebView, or edits it ([editing] = true).
 * The note page sanitizes with the same DOMPurify file the web client vendors
 * — output is never trusted by the app.
 *
 * In edit mode the page pushes a debounced sanitized snapshot of the
 * contenteditable body through [onHtml]; the caller treats it both as the
 * draft (NFR-3) and as the HTML to save. Anything the app ever sends to the
 * server therefore passed DOMPurify, matching web-client behavior.
 *
 * Images inside the body reference server URLs (`/api/v1/...`) by absolute
 * path. Browser clients ride session cookies; this app signs every request
 * instead, so those loads are bridged through OkHttp with the Sig header —
 * no cookies cross from the app, and nothing else in the page can fetch
 * beyond that surface (file asset loads only otherwise).
 */
@Composable
fun HtmlPanel(
    server: Server,
    html: String,
    modifier: Modifier = Modifier,
    editing: Boolean = false,
    onHtml: ((String) -> Unit)? = null,
    webViewRef: MutableState<WebView?>? = null,
) {
    var ready by remember { mutableStateOf(false) }
    var rendered by remember { mutableStateOf("") }
    var editingOn by remember { mutableStateOf(false) }
    val latestOnHtml by rememberUpdatedState(onHtml)
    // Factory runs once; the holder survives recompositions so update() can
    // reach the WebView the factory built. [0] = WebView, [1] = capture view.
    val views = remember { arrayOfNulls<android.view.View>(2) }

    AndroidView(
        modifier = modifier,
        factory = { c ->
            // Terminal-pane shape (TerminalPaneView): the WebView renders only —
            // its Chromium IME path is unreliable in this app and never moves IME
            // text into a contenteditable — while a NoteCaptureView owns the soft
            // keyboard and forwards text/keys into the page. The WebView is left
            // unfocusable so taps reposition the DOM caret without stealing the
            // IME connection from the capture view.
            // The capture view is created after the WebView (its lambdas call
            // into the page); the bridge resolves it through this holder.
            val keysRef = arrayOfNulls<NoteCaptureView>(1)
            val wv = WebView(c).apply {
                settings.javaScriptEnabled = true
                settings.allowFileAccess = false // the android_asset page needs none
                settings.allowContentAccess = false
                setBackgroundColor(android.graphics.Color.TRANSPARENT)
                isVerticalScrollBarEnabled = false
                overScrollMode = View.OVER_SCROLL_NEVER
                isFocusable = false
                isFocusableInTouchMode = false
                webViewClient = object : WebViewClient() {
                    // the note page never navigates anywhere
                    override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest) = true

                    // Serve asset files normally; bridge the note's server-URL
                    // references through OkHttp with the device signature.
                    override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? =
                        bridgeServerResponse(server, request.url.path ?: return null, request.url.query)
                }
                addJavascriptInterface(object {
                    @android.webkit.JavascriptInterface
                    fun ready() {
                        Handler(Looper.getMainLooper()).post { ready = true }
                    }

                    /** The page (a tap in the body or editOn) wants the IME. */
                    @android.webkit.JavascriptInterface
                    fun ime() {
                        Handler(Looper.getMainLooper()).post {
                            keysRef[0]?.let { k ->
                                k.requestFocus()
                                (context.getSystemService(Context.INPUT_METHOD_SERVICE) as InputMethodManager)
                                    .showSoftInput(k, InputMethodManager.SHOW_IMPLICIT)
                            }
                        }
                    }

                    /** Sanitized-edit snapshot pushed by the page; UTF-8 HTML in base64. */
                    @android.webkit.JavascriptInterface
                    fun html(b64: String) {
                        if (onHtml == null) return
                        val text = String(Base64.decode(b64, Base64.NO_WRAP), Charsets.UTF_8)
                        Handler(Looper.getMainLooper()).post { latestOnHtml?.invoke(text) }
                    }
                }, "Android")
                loadUrl(NOTE_PAGE)
            }
            val keys = NoteCaptureView(
                c,
                // The page's insert() expects base64 UTF-8 (quotes/HTML can't
                // break the JS string) — encode the IME's raw commit here.
                onText = { text: String ->
                    val b64 = Base64.encodeToString(text.toByteArray(Charsets.UTF_8), Base64.NO_WRAP)
                    wv.evaluateJavascript("insert('$b64')", null)
                },
                onKey = { name: String ->
                    wv.evaluateJavascript("key('$name')", null)
                },
            )
            webViewRef?.value = wv
            keysRef[0] = keys
            views[0] = wv
            views[1] = keys
            android.widget.FrameLayout(c).apply {
                addView(wv, android.widget.FrameLayout.LayoutParams(
                    android.widget.FrameLayout.LayoutParams.MATCH_PARENT, android.widget.FrameLayout.LayoutParams.MATCH_PARENT))
                addView(keys, android.widget.FrameLayout.LayoutParams(1, 1))
            }
        },
        update = {
            val wv = views[0] as? WebView
            if (ready && wv != null) {
                when {
                    editing && !editingOn -> {
                        editingOn = true
                        if (html != rendered) {
                            rendered = html
                            wv.evaluateJavascript("setBody('${Base64.encodeToString(html.toByteArray(), Base64.NO_WRAP)}')", null)
                        }
                        wv.evaluateJavascript("editOn()", null)
                    }
                    !editing && editingOn -> {
                        editingOn = false
                        rendered = "" // discard pending edits in the DOM: re-render below
                    }
                }
                // base64 keeps quotes/HTML out of the JS string
                if (html != rendered && !editingOn) {
                    rendered = html
                    wv.evaluateJavascript("setBody('${Base64.encodeToString(html.toByteArray(), Base64.NO_WRAP)}')", null)
                }
            }
        },
        onRelease = { (views[0] as? WebView)?.destroy() },
    )
}